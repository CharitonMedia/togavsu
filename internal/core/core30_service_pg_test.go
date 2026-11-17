package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// assertServiceWriteAudit requires a practice/calendar write audit row with source
// "service" and reason naming the module (CORE-30 / CU2-02).
func assertServiceWriteAudit(t *testing.T, x *ext, entity, entityID, module string) {
	t.Helper()
	ctx := context.Background()
	var source, reason string
	err := x.app.Pool.QueryRowContext(ctx,
		`SELECT source, reason FROM core.audit_log WHERE entity = $1 AND entity_id = $2 ORDER BY created_at DESC LIMIT 1`,
		entity, entityID).Scan(&source, &reason)
	if err != nil {
		t.Fatalf("audit %s/%s: %v", entity, entityID, err)
	}
	if source != "service" {
		t.Fatalf("audit source %q want service", source)
	}
	if !strings.Contains(reason, "module="+module) {
		t.Fatalf("audit reason %q missing module=%s", reason, module)
	}
}

func core30ModuleScopes() string {
	return "permissions:\n" +
		"  - scope: contacts:read\n    reason: Read contacts.\n" +
		"  - scope: contacts:write\n    reason: Write contacts.\n" +
		"  - scope: matters:write\n    reason: Write matters.\n" +
		"  - scope: conflicts:read\n    reason: Conflicts.\n" +
		"  - scope: calendar:read\n    reason: Calendar.\n" +
		"  - scope: calendar:write\n    reason: Calendar write.\n" +
		"  - scope: deadlines:write\n    reason: Deadlines.\n" +
		"  - scope: notes:write\n    reason: Notes.\n" +
		"  - scope: files:read\n    reason: Renditions.\n"
}

// TestCORE30ServiceCallsWalls: every CORE-30 service call omits walled matters and
// answers not_found on a walled get; lifting the wall returns them (CU2-02).
func TestCORE30ServiceCallsWalls(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte(core30ModuleScopes()), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	const module = "matter-clock"

	open, openClient := x.matter("CORE30 Open", "core30.open@client.example", nil)
	walled, walledClient := x.matter("CORE30 Walled", "core30.walled@client.example", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	day := func(d int) string {
		return time.Now().Add(time.Duration(d) * 24 * time.Hour).UTC().Format("2006-01-02")
	}
	at := func(h int) string {
		return time.Now().Add(time.Duration(h) * time.Hour).UTC().Format(time.RFC3339)
	}
	attyP := x.principal(x.atty)

	// Seed walled-matter records as the attorney (inside the wall).
	x.atty.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{
		"matter_id": walled, "title": "walled note", "body": "secret",
	})
	wEv := str(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Walled hearing", "kind": "court_date", "starts_at": at(30), "ends_at": at(31),
		"matter_id": walled, "attendee_user_ids": []string{x.attyID},
	}).json, "id")
	wDL := str(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{
		"matter_id": walled, "title": "Walled deadline", "due_on": day(3), "responsible_user_id": x.attyID,
	}).json, "id")
	wCheck := str(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/matters/"+walled+"/conflict-checks", nil).json, "id")
	wFile := x.upload(x.atty, walled, "walled.txt", "text/plain", []byte("walled body"), nil)
	wFileID := str(t, wFile, "id")
	wVerID := str(t, wFile, "current_version_id")
	wMD, err := x.app.Files.AddRendition(ctx, attyP, domain.ID(wFileID), domain.ID(wVerID), "markdown",
		strings.NewReader("# walled"), int64(len("# walled")))
	if err != nil {
		t.Fatal(err)
	}

	openFile := x.upload(x.atty, open, "open.txt", "text/plain", []byte("open body"), nil)
	openFileID := str(t, openFile, "id")
	openVerID := str(t, openFile, "current_version_id")
	openMD, err := x.app.Files.AddRendition(ctx, attyP, domain.ID(openFileID), domain.ID(openVerID), "markdown",
		strings.NewReader("# open"), int64(len("# open")))
	if err != nil {
		t.Fatal(err)
	}
	openPDF, err := x.app.Files.AddRendition(ctx, attyP, domain.ID(openFileID), domain.ID(openVerID), "pdf_preview",
		strings.NewReader("%PDF-1.4"), int64(len("%PDF-1.4")))
	if err != nil {
		t.Fatal(err)
	}

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last(module).Secrets["togavsu_api_token"]

	mustNotFound := func(method, path string, body any) {
		t.Helper()
		r := bot.do(method, path, body)
		if r.code != http.StatusNotFound || r.json["code"] != "not_found" {
			t.Fatalf("%s %s: want not_found, got %d %s", method, path, r.code, r.body)
		}
	}

	// --- listMatter / getMatter ---
	listed := bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	if strings.Contains(listed.body, walled) || !strings.Contains(listed.body, open) {
		t.Fatalf("listMatter walls: %s", listed.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil)
	mustNotFound("GET", "/api/v1/matters/"+walled, nil)

	// --- createNote (positive + author) / walled createNote ---
	note := bot.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{
		"matter_id": open, "title": "svc note", "body": "from service",
	})
	assertServiceWriteAudit(t, x, "note", str(t, note.json, "id"), module)
	mustNotFound("POST", "/api/v1/notes", map[string]any{"matter_id": walled, "title": "walled", "body": "no"})

	// --- listMatterTimeline ---
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+open+"/timeline", nil)
	mustNotFound("GET", "/api/v1/matters/"+walled+"/timeline", nil)

	// --- contacts: get / list by email / phones / emails ---
	bot.must(http.StatusOK, "GET", "/api/v1/contacts/"+openClient, nil)
	mustNotFound("GET", "/api/v1/contacts/"+walledClient, nil)
	exact := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.open@client.example", nil)
	if !strings.Contains(exact.body, openClient) {
		t.Fatalf("email exact: %s", exact.body)
	}
	walledExact := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.walled@client.example", nil)
	if strings.Contains(walledExact.body, walledClient) {
		t.Fatalf("walled contact email lookup leaked: %s", walledExact.body)
	}
	openPhone := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": openClient, "label": "mobile", "e164": "+15555550111", "effective_from": "2026-01-01", "primary": true,
	}).json, "id")
	walledPhone := str(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": walledClient, "label": "mobile", "e164": "+15555550112", "effective_from": "2026-01-01", "primary": true,
	}).json, "id")
	bot.must(http.StatusOK, "GET", "/api/v1/contact-phones/"+openPhone, nil)
	mustNotFound("GET", "/api/v1/contact-phones/"+walledPhone, nil)

	// listContactEmail / listContactPhone as the service principal (CU2-02).
	openEmailList := bot.must(http.StatusOK, "GET", "/api/v1/contact-emails?contact_id="+openClient, nil)
	if len(items(t, openEmailList)) == 0 {
		t.Fatal("expected open client email via listContactEmail")
	}
	openEmail := str(t, items(t, openEmailList)[0], "id")
	mustNotFound("GET", "/api/v1/contact-emails?contact_id="+walledClient, nil)
	walledEmails := items(t, x.atty.must(http.StatusOK, "GET", "/api/v1/contact-emails?contact_id="+walledClient, nil))
	if len(walledEmails) == 0 {
		t.Fatal("expected walled client email")
	}
	walledEmailID := str(t, walledEmails[0], "id")
	bot.must(http.StatusOK, "GET", "/api/v1/contact-emails/"+openEmail, nil)
	mustNotFound("GET", "/api/v1/contact-emails/"+walledEmailID, nil)

	openPhoneList := bot.must(http.StatusOK, "GET", "/api/v1/contact-phones?contact_id="+openClient, nil)
	if !strings.Contains(openPhoneList.body, openPhone) {
		t.Fatalf("listContactPhone open: %s", openPhoneList.body)
	}
	mustNotFound("GET", "/api/v1/contact-phones?contact_id="+walledClient, nil)

	createdContact := bot.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Svc", "family_name": "Contact",
	})
	assertServiceWriteAudit(t, x, "contact", str(t, createdContact.json, "id"), module)
	emailWrite := bot.must(http.StatusCreated, "POST", "/api/v1/contact-emails", map[string]any{
		"contact_id": str(t, createdContact.json, "id"), "label": "work",
		"email": "svc.contact@example.com", "effective_from": "2026-01-01", "primary": true,
	})
	assertServiceWriteAudit(t, x, "contact_email", str(t, emailWrite.json, "id"), module)
	phoneWrite := bot.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": str(t, createdContact.json, "id"), "label": "desk",
		"e164": "+15555550113", "effective_from": "2026-01-01", "primary": true,
	})
	assertServiceWriteAudit(t, x, "contact_phone", str(t, phoneWrite.json, "id"), module)
	bot.must(http.StatusOK, "PATCH", "/api/v1/contact-emails/"+str(t, emailWrite.json, "id"), map[string]any{"label": "home"})
	assertServiceWriteAudit(t, x, "contact_email", str(t, emailWrite.json, "id"), module)
	bot.must(http.StatusOK, "PATCH", "/api/v1/contact-phones/"+str(t, phoneWrite.json, "id"), map[string]any{"label": "cell"})
	assertServiceWriteAudit(t, x, "contact_phone", str(t, phoneWrite.json, "id"), module)
	mustNotFound("POST", "/api/v1/contact-emails", map[string]any{
		"contact_id": walledClient, "label": "x", "email": "no@example.com", "effective_from": "2026-01-01",
	})
	mustNotFound("POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": walledClient, "label": "x", "e164": "+15555550999", "effective_from": "2026-01-01",
	})
	mustNotFound("PATCH", "/api/v1/contact-emails/"+walledEmailID, map[string]any{"label": "no"})
	mustNotFound("PATCH", "/api/v1/contact-phones/"+walledPhone, map[string]any{"label": "no"})

	// --- createMatter / createMatterParty ---
	newMatter := bot.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{
		"name": "CORE30 Svc Matter", "practice_area_id": x.area, "billing_type": "hourly",
		"responsible_attorney_user_id": x.attyID,
	})
	assertServiceWriteAudit(t, x, "matter", str(t, newMatter.json, "id"), module)
	party := bot.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{
		"matter_id": str(t, newMatter.json, "id"), "contact_id": str(t, createdContact.json, "id"), "role": "client",
	})
	assertServiceWriteAudit(t, x, "matter_party", str(t, party.json, "id"), module)
	mustNotFound("POST", "/api/v1/matter-parties", map[string]any{
		"matter_id": walled, "contact_id": str(t, createdContact.json, "id"), "role": "other",
	})

	// --- conflict checks ---
	openCheck := bot.must(http.StatusCreated, "POST", "/api/v1/matters/"+open+"/conflict-checks", nil)
	assertServiceWriteAudit(t, x, "conflict_check", str(t, openCheck.json, "id"), module)
	bot.must(http.StatusOK, "GET", "/api/v1/conflict-checks/"+str(t, openCheck.json, "id"), nil)
	mustNotFound("POST", "/api/v1/matters/"+walled+"/conflict-checks", nil)
	mustNotFound("GET", "/api/v1/conflict-checks/"+wCheck, nil)

	// --- calendar events ---
	oEv := bot.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{
		"title": "Open svc hearing", "kind": "court_date", "starts_at": at(40), "ends_at": at(41),
		"matter_id": open, "attendee_user_ids": []string{x.attyID},
	})
	assertServiceWriteAudit(t, x, "calendar_event", str(t, oEv.json, "id"), module)
	mustNotFound("POST", "/api/v1/calendar-events", map[string]any{
		"title": "Walled svc", "kind": "court_date", "starts_at": at(42), "ends_at": at(43),
		"matter_id": walled, "attendee_user_ids": []string{x.attyID},
	})
	evList := bot.must(http.StatusOK, "GET", "/api/v1/calendar-events", nil)
	if strings.Contains(evList.body, wEv) || !strings.Contains(evList.body, str(t, oEv.json, "id")) {
		t.Fatalf("listCalendarEvents walls: %s", evList.body)
	}
	bot.must(http.StatusOK, "PATCH", "/api/v1/calendar-events/"+str(t, oEv.json, "id"), map[string]any{"location": "Court A"})
	assertServiceWriteAudit(t, x, "calendar_event", str(t, oEv.json, "id"), module)
	mustNotFound("PATCH", "/api/v1/calendar-events/"+wEv, map[string]any{"location": "no"})
	mustNotFound("DELETE", "/api/v1/calendar-events/"+wEv, nil)
	bot.must(http.StatusNoContent, "DELETE", "/api/v1/calendar-events/"+str(t, oEv.json, "id"), nil)

	// --- previewAvailableDates ---
	bot.must(http.StatusOK, "POST", "/api/v1/calendar/available-dates", map[string]any{
		"user_id": x.attyID, "start_date": day(1), "end_date": day(14), "matter_id": open,
	})
	mustNotFound("POST", "/api/v1/calendar/available-dates", map[string]any{
		"user_id": x.attyID, "start_date": day(1), "end_date": day(14), "matter_id": walled,
	})

	// --- deadlines ---
	oDL := bot.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{
		"matter_id": open, "title": "Open svc deadline", "due_on": day(5), "responsible_user_id": x.attyID,
	})
	assertServiceWriteAudit(t, x, "deadline", str(t, oDL.json, "id"), module)
	mustNotFound("POST", "/api/v1/deadlines", map[string]any{
		"matter_id": walled, "title": "no", "due_on": day(5), "responsible_user_id": x.attyID,
	})
	bot.must(http.StatusOK, "PATCH", "/api/v1/deadlines/"+str(t, oDL.json, "id"), map[string]any{"title": "Open svc deadline 2"})
	assertServiceWriteAudit(t, x, "deadline", str(t, oDL.json, "id"), module)
	mustNotFound("PATCH", "/api/v1/deadlines/"+wDL, map[string]any{"title": "no"})

	// --- renditions: open matter markdown ok; walled not_found; non-markdown refused ---
	rends := bot.must(http.StatusOK, "GET", "/api/v1/files/"+openFileID+"/renditions", nil)
	if strings.Contains(rends.body, string(openPDF.ID)) || !strings.Contains(rends.body, string(openMD.ID)) {
		t.Fatalf("listFileRenditions must be markdown-only for service: %s", rends.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/files/"+openFileID+"/renditions/"+string(openMD.ID), nil)
	if r := bot.do("GET", "/api/v1/files/"+openFileID+"/renditions/"+string(openPDF.ID), nil); r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("non-markdown download: %d %s", r.code, r.body)
	}
	mustNotFound("GET", "/api/v1/files/"+wFileID+"/renditions", nil)
	mustNotFound("GET", "/api/v1/files/"+wFileID+"/renditions/"+string(wMD.ID), nil)

	// Lift wall → walled records return on the next read (CU2-02).
	x.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{}})
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+walled, nil)
	liftedMatters := bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	if !strings.Contains(liftedMatters.body, walled) {
		t.Fatalf("listMatter after lift missing walled: %s", liftedMatters.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/contacts/"+walledClient, nil)
	liftedByEmail := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.walled@client.example", nil)
	if !strings.Contains(liftedByEmail.body, walledClient) {
		t.Fatalf("listContacts by email after lift: %s", liftedByEmail.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/contact-emails/"+walledEmailID, nil)
	bot.must(http.StatusOK, "GET", "/api/v1/contact-phones/"+walledPhone, nil)
	liftedEmails := bot.must(http.StatusOK, "GET", "/api/v1/contact-emails?contact_id="+walledClient, nil)
	if !strings.Contains(liftedEmails.body, walledEmailID) {
		t.Fatalf("listContactEmail after lift: %s", liftedEmails.body)
	}
	liftedPhones := bot.must(http.StatusOK, "GET", "/api/v1/contact-phones?contact_id="+walledClient, nil)
	if !strings.Contains(liftedPhones.body, walledPhone) {
		t.Fatalf("listContactPhone after lift: %s", liftedPhones.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/conflict-checks/"+wCheck, nil)
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+walled+"/timeline", nil)
	liftedEvents := bot.must(http.StatusOK, "GET", "/api/v1/calendar-events", nil)
	if !strings.Contains(liftedEvents.body, wEv) {
		t.Fatalf("listCalendarEvents after lift: %s", liftedEvents.body)
	}
	liftedRends := bot.must(http.StatusOK, "GET", "/api/v1/files/"+wFileID+"/renditions", nil)
	if !strings.Contains(liftedRends.body, string(wMD.ID)) {
		t.Fatalf("listFileRenditions after lift: %s", liftedRends.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/files/"+wFileID+"/renditions/"+string(wMD.ID), nil)
}

func TestCORE30ServiceCallRefusedWithoutScope(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(nil) // matter-clock has matters:read/events:read; not contacts:write
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	r := bot.do("POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "No", "family_name": "Scope"})
	if r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
		t.Fatalf("missing scope: %d %v", r.code, r.json)
	}
}

// TestCORE30ServiceCallPerOpLimit: each CORE-30 op has its own modules.service_call_limit.*
// setting (default 120); exceeding it answers rate_limited (CU2-01).
func TestCORE30ServiceCallPerOpLimit(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(nil)
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.service_call_limit.listMatter", map[string]any{"value": 2})
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	bot.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	r := bot.do("GET", "/api/v1/matters", nil)
	if r.code != http.StatusTooManyRequests || r.json["code"] != "rate_limited" {
		t.Fatalf("per-op limit: %d %v", r.code, r.json)
	}
	// A different operation still has its own budget.
	mid, _ := x.matter("CORE30 Limit", "core30.limit@client.example", nil)
	bot.must(http.StatusOK, "GET", "/api/v1/matters/"+mid, nil)
}

// TestCORE30ServiceListContactsExactOnly: service listContacts requires exactly one of
// email or phone and refuses other filters (CORE-30).
func TestCORE30ServiceListContactsExactOnly(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte(core30ModuleScopes()), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	_, openClient := x.matter("CORE30 Exact", "core30.exact@client.example", nil)
	x.admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{
		"contact_id": openClient, "label": "mobile", "e164": "+15555550200", "effective_from": "2026-01-01", "primary": true,
	})
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	refuse := func(path string) {
		t.Helper()
		r := bot.do("GET", path, nil)
		if r.code != http.StatusForbidden || r.json["code"] != "scope_denied" {
			t.Fatalf("%s: %d %s", path, r.code, r.body)
		}
	}
	refuse("/api/v1/contacts")
	refuse("/api/v1/contacts?q=Exact")
	refuse("/api/v1/contacts?kind=person")
	refuse("/api/v1/contacts?tag=x")
	refuse("/api/v1/contacts?email=core30.exact@client.example&phone=%2B15555550200")
	refuse("/api/v1/contacts?email=core30.exact@client.example&q=Exact")

	byEmail := bot.must(http.StatusOK, "GET", "/api/v1/contacts?email=core30.exact@client.example", nil)
	if !strings.Contains(byEmail.body, openClient) {
		t.Fatalf("email exact: %s", byEmail.body)
	}
	byPhone := bot.must(http.StatusOK, "GET", "/api/v1/contacts?phone=%2B15555550200", nil)
	if !strings.Contains(byPhone.body, openClient) {
		t.Fatalf("phone exact: %s", byPhone.body)
	}
}

type core30NopAudit struct{}

func (core30NopAudit) SettingChanged(context.Context, settings.Change) error { return nil }

// TestCORE30ServiceCallLimitFailClosed: a corrupt modules.service_call_limit.* value
// refuses the call instead of failing open.
func TestCORE30ServiceCallLimitFailClosed(t *testing.T) {
	ctx := context.Background()
	mem := settings.NewMemory()
	reg, err := settings.New(mem, core30NopAudit{})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Put(ctx, "modules.service_call_limit.listMatter", "not-an-int", time.Now().UTC(), domain.SystemUserID); err != nil {
		t.Fatal(err)
	}
	a := &App{Settings: reg}
	err = a.enforceServiceCallLimit(ctx, domain.Principal{Source: domain.SourceService, ModuleID: "matter-clock"}, "listMatter")
	api, ok := err.(*domain.APIError)
	if !ok || api.Code != "scope_denied" {
		t.Fatalf("corrupt limit: %v", err)
	}
	// Unknown operation setting remains unlimited.
	if err := a.enforceServiceCallLimit(ctx, domain.Principal{Source: domain.SourceService, ModuleID: "matter-clock"}, "notACore30Op"); err != nil {
		t.Fatalf("unknown op: %v", err)
	}
}

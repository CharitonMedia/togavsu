package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// eventCount counts core.events rows with action.
func actionCount(t *testing.T, app *App, action string) int {
	t.Helper()
	var n int
	if err := app.Pool.QueryRowContext(context.Background(), `SELECT count(*) FROM events WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// appendedFor counts the event.appended rows naming the appended event id, and returns
// the matter one of them carries.
func appendedFor(t *testing.T, app *App, id string) (int, string) {
	t.Helper()
	var n int
	var matter string
	if err := app.Pool.QueryRowContext(context.Background(), `SELECT count(*), COALESCE(max(matter_id), '') FROM events
WHERE action = 'event.appended' AND record_type = 'event' AND record_id = $1 AND client_visible = 0`, id).Scan(&n, &matter); err != nil {
		t.Fatal(err)
	}
	return n, matter
}

func feedIDs(t *testing.T, r resp) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	rows, _ := r.json["data"].([]any)
	for _, row := range rows {
		out[str(t, row.(map[string]any), "id")] = true
	}
	return out
}

// TestEventsFeedAndAppend covers listEvents, createEvent and bulkEvent (PRD 5 events,
// 8.13 Activities Feed; AC-002): the actor comes from the token, matter walls apply to
// every role including admin, a batch with one inaccessible matter writes nothing,
// Idempotency-Key replays, the feed omits walled rows, and /mcp runs the same operations.
func TestEventsFeedAndAppend(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	open, _ := x.matter("Estate of Orla Synth", "", nil)
	walled, _ := x.matter("Estate of Wren Synth", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}
	ev := func(matter, action, summary string) map[string]any {
		out := map[string]any{"action": action, "record_type": "intake_answer", "record_id": "answer-1", "matter_id": nil, "summary": summary}
		if matter != "" {
			out["matter_id"] = matter
		}
		return out
	}

	made := x.staff.must(http.StatusCreated, "POST", "/api/v1/events", ev(open, "intake.answer_saved", "Saved an intake answer"))
	if made.json["actor_kind"] != "staff" || made.json["actor_id"] != x.staffID || made.json["matter_id"] != open || made.json["client_visible"] != false {
		t.Fatalf("created event: %s", made.body)
	}
	// The contract's x-togavsu-events: createEvent emits event.appended for the new row.
	if n, matter := appendedFor(t, x.app, str(t, made.json, "id")); n != 1 || matter != open {
		t.Fatalf("createEvent emitted %d event.appended rows (matter %q)", n, matter)
	}
	spoof := ev(open, "intake.answer_saved", "Spoofed")
	spoof["actor_kind"] = "system"
	if r := x.staff.do("POST", "/api/v1/events", spoof); r.code != http.StatusBadRequest {
		t.Fatalf("a body naming its own actor: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/events", ev(open, "matter.created", "Forged core event")); r.code != http.StatusBadRequest {
		t.Fatalf("an appended event took a core action: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/events", ev(open, "intake.answer_saved", "")); r.code != http.StatusBadRequest {
		t.Fatalf("an empty summary: %d %s", r.code, r.body)
	}
	for who, c := range map[string]*client{"staff": x.staff, "admin": x.admin} {
		if r := c.do("POST", "/api/v1/events", ev(walled, "intake.answer_saved", "Behind the wall")); r.code != http.StatusNotFound {
			t.Fatalf("%s wrote to a walled matter it is not on: %d %s", who, r.code, r.body)
		}
	}
	missing, _ := domain.NewID()
	if r := x.staff.do("POST", "/api/v1/events", ev(string(missing), "intake.answer_saved", "No such matter")); r.code != http.StatusNotFound {
		t.Fatalf("a missing matter: %d %s", r.code, r.body)
	}
	hidden := x.atty.must(http.StatusCreated, "POST", "/api/v1/events", ev(walled, "intake.answer_saved", "On the wall"))
	if r := x.acct.do("POST", "/api/v1/events", ev(open, "intake.answer_saved", "Accountant")); r.code != http.StatusForbidden {
		t.Fatalf("accountant append: %d %s", r.code, r.body)
	}
	if r := x.acct.do("GET", "/api/v1/events", nil); r.code != http.StatusForbidden {
		t.Fatalf("accountant feed: %d %s", r.code, r.body)
	}

	// A batch with one walled matter is not_found and writes none of it.
	before := actionCount(t, x.app, "intake.batch")
	appendedBefore := actionCount(t, x.app, "event.appended")
	if r := x.staff.do("POST", "/api/v1/events:bulk", map[string]any{"items": []any{ev(open, "intake.batch", "one"), ev(walled, "intake.batch", "two")}}); r.code != http.StatusNotFound {
		t.Fatalf("batch with a walled matter: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/events:bulk", map[string]any{"items": []any{}}); r.code != http.StatusBadRequest {
		t.Fatalf("empty batch: %d %s", r.code, r.body)
	}
	// One invalid item rejects the batch with batch_rejected, as bulkEvent's contract lists.
	if r := x.staff.do("POST", "/api/v1/events:bulk", map[string]any{"items": []any{ev(open, "intake.batch", "one"), ev(open, "intake.batch", "")}}); r.code != http.StatusUnprocessableEntity ||
		!strings.Contains(r.body, "batch_rejected") || !strings.Contains(r.body, "items[1].summary") {
		t.Fatalf("batch with an invalid item: %d %s", r.code, r.body)
	}
	if got := actionCount(t, x.app, "intake.batch"); got != before {
		t.Fatalf("a rejected batch wrote %d events", got-before)
	}
	if got := actionCount(t, x.app, "event.appended"); got != appendedBefore {
		t.Fatalf("a rejected batch emitted %d event.appended", got-appendedBefore)
	}
	batch := x.staff.must(http.StatusCreated, "POST", "/api/v1/events:bulk", map[string]any{"items": []any{ev(open, "intake.batch", "one"), ev("", "intake.batch", "firm")}})
	if rows, _ := batch.json["data"].([]any); len(rows) != 2 || actionCount(t, x.app, "intake.batch") != before+2 {
		t.Fatalf("batch: %s", batch.body)
	}
	// bulkEvent emits one event.appended per appended row, on that row's matter.
	for i, want := range []string{open, ""} {
		row := batch.json["data"].([]any)[i].(map[string]any)
		if n, matter := appendedFor(t, x.app, str(t, row, "id")); n != 1 || matter != want {
			t.Fatalf("bulk item %d emitted %d event.appended (matter %q)", i, n, matter)
		}
	}

	// Idempotency-Key: the same body replays the same event and writes one row.
	body := ev(open, "intake.replayed", "Replayed once")
	first := x.staff.do("POST", "/api/v1/events", body, "Idempotency-Key", "evt-key-1")
	again := x.staff.do("POST", "/api/v1/events", body, "Idempotency-Key", "evt-key-1")
	if first.code != http.StatusCreated || again.code != http.StatusCreated || first.json["id"] != again.json["id"] || actionCount(t, x.app, "intake.replayed") != 1 {
		t.Fatalf("replay: %d %s / %d %s", first.code, first.body, again.code, again.body)
	}
	if n, _ := appendedFor(t, x.app, str(t, first.json, "id")); n != 1 {
		t.Fatalf("a replay emitted event.appended %d times", n)
	}
	if r := x.staff.do("POST", "/api/v1/events", ev(open, "intake.replayed", "Different"), "Idempotency-Key", "evt-key-1"); r.code != http.StatusConflict || !strings.Contains(r.body, "idempotency_conflict") {
		t.Fatalf("same key, different body: %d %s", r.code, r.body)
	}

	// The feed: staff do not see the walled row; the attorney on the wall does.
	staffFeed := feedIDs(t, x.staff.must(http.StatusOK, "GET", "/api/v1/events?limit=200", nil))
	attyFeed := feedIDs(t, x.atty.must(http.StatusOK, "GET", "/api/v1/events?limit=200", nil))
	if !staffFeed[str(t, made.json, "id")] || staffFeed[str(t, hidden.json, "id")] || !attyFeed[str(t, hidden.json, "id")] {
		t.Fatalf("feed walls: staff %v atty %v", staffFeed, attyFeed)
	}
	if r := x.admin.must(http.StatusOK, "GET", "/api/v1/events?limit=200", nil); feedIDs(t, r)[str(t, hidden.json, "id")] {
		t.Fatal("admin saw a walled matter's event")
	}
	if r := x.staff.do("GET", "/api/v1/events?matter_id="+walled, nil); r.code != http.StatusNotFound {
		t.Fatalf("staff filtered to a walled matter: %d %s", r.code, r.body)
	}
	byMatter := x.staff.must(http.StatusOK, "GET", "/api/v1/events?matter_id="+open+"&actor_kind=staff&actor_id="+x.staffID, nil)
	for _, row := range byMatter.json["data"].([]any) {
		m := row.(map[string]any)
		if m["matter_id"] != open || m["actor_kind"] != "staff" {
			t.Fatalf("filtered feed row: %v", m)
		}
	}
	if r := x.staff.do("GET", "/api/v1/events?actor_kind=robot", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad actor_kind: %d", r.code)
	}
	if r := x.staff.do("GET", "/api/v1/events?limit=100000", nil); r.code != http.StatusBadRequest {
		t.Fatalf("limit above api.max_page_size: %d", r.code)
	}
	// Pages of one walk the whole feed newest first, once each.
	seen := map[string]bool{}
	cursor, last := "", ""
	for pages := 0; ; pages++ {
		path := "/api/v1/events?limit=1"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		r := x.staff.must(http.StatusOK, "GET", path, nil)
		for id := range feedIDs(t, r) {
			if seen[id] {
				t.Fatalf("page %d repeated %s", pages, id)
			}
			seen[id] = true
			if last != "" && strings.Compare(r.json["data"].([]any)[0].(map[string]any)["created_at"].(string), last) > 0 {
				t.Fatal("feed is not newest first")
			}
			last = r.json["data"].([]any)[0].(map[string]any)["created_at"].(string)
		}
		next, _ := r.json["page"].(map[string]any)["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != len(staffFeed) {
		t.Fatalf("paged feed has %d rows, one page has %d", len(seen), len(staffFeed))
	}
	if r := x.staff.do("GET", "/api/v1/events?cursor=garbage", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", r.code)
	}

	// A bearer token needs events:write to append and events:read to list, on HTTP and /mcp.
	tok := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "events", "owner_user_id": x.attyID, "scopes": []string{"events:read", "events:write"}})
	agent := newClient(t, x.srv.URL)
	agent.bearer = str(t, tok.json, "secret")
	viaMCP := mcpCall(agent, toolName(t, "POST", "/api/v1/events"), map[string]any{"body": ev(open, "intake.mcp", "Through MCP")})
	if sc, _ := viaMCP.json["result"].(map[string]any)["structuredContent"].(map[string]any); sc["actor_kind"] != "agent" || sc["matter_id"] != open {
		t.Fatalf("MCP createEvent: %s", viaMCP.body)
	}
	listed := mcpCall(agent, toolName(t, "GET", "/api/v1/events"), map[string]any{"query": map[string]any{"matter_id": open}})
	if !strings.Contains(listed.body, "Through MCP") || strings.Contains(listed.body, `"isError":true`) {
		t.Fatalf("MCP listEvents: %s", listed.body)
	}
	readOnly := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "read", "owner_user_id": x.staffID, "scopes": []string{"events:read"}})
	reader := newClient(t, x.srv.URL)
	reader.bearer = str(t, readOnly.json, "secret")
	if r := reader.do("POST", "/api/v1/events", ev(open, "intake.read_only", "No")); r.code != http.StatusForbidden {
		t.Fatalf("token without events:write: %d %s", r.code, r.body)
	}

	// A module's own token appends as the module on an unwalled matter only, and reads
	// the feed only through a viewer.
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("  - scope: events:read\n"),
			[]byte("  - scope: events:write\n    reason: Add a timeline row when a target is met.\n  - scope: events:read\n"), 1)
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("  - matter.created\n"), []byte("  - matter.created\n  - event.appended\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")
	mod := newClient(t, x.srv.URL)
	mod.bearer = x.rt.last("matter-clock").Secrets["togavsu_api_token"]
	byModule := mod.must(http.StatusCreated, "POST", "/api/v1/events", ev(open, "matter-clock.target_met", "Target met"))
	if byModule.json["actor_kind"] != "module" || byModule.json["actor_id"] != nil {
		t.Fatalf("module event: %s", byModule.body)
	}
	if r := mod.do("POST", "/api/v1/events", ev(walled, "matter-clock.target_met", "Walled")); r.code != http.StatusNotFound {
		t.Fatalf("module wrote to a walled matter: %d %s", r.code, r.body)
	}
	if r := mod.do("GET", "/api/v1/events", nil); r.code != http.StatusForbidden {
		t.Fatalf("module read the feed with no viewer: %d %s", r.code, r.body)
	}
	// A module subscribed to event.appended is queued a delivery naming the appended row.
	later := x.staff.must(http.StatusCreated, "POST", "/api/v1/events", ev(open, "intake.subscribed", "Delivered to subscribers"))
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT d.payload FROM core.module_deliveries d JOIN core.events e ON e.id = d.event_id
WHERE d.module_id = 'matter-clock' AND d.action = 'event.appended' AND e.record_id = $1`, str(t, later.json, "id")).Scan(&payload); err != nil {
		t.Fatalf("no event.appended delivery to the subscribed module: %v", err)
	}
	if !strings.Contains(payload, `"record_type":"event"`) || !strings.Contains(payload, str(t, later.json, "id")) {
		t.Fatalf("event.appended delivery: %s", payload)
	}
}

// TestAIProviderKeys covers listAiProviders, putAiProviderKey and checkAiProvider (PRD
// 8.7 AI and inference tab; AC-002): admin only, the key is stored sealed and never
// returned, logged or put on an event, and the Provider check answers ok false on a
// refused key without the key in its detail.
func TestAIProviderKeys(t *testing.T) {
	x := newExt(t)
	const key = "synthetic-openai-key-WXYZ"
	list := x.admin.must(http.StatusOK, "GET", "/api/v1/ai/providers", nil)
	if rows := list.json["data"].([]any); len(rows) != 4 || rows[0].(map[string]any)["configured"] != false {
		t.Fatalf("providers: %s", list.body)
	}
	if r := x.staff.do("GET", "/api/v1/ai/providers", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff listed providers: %d", r.code)
	}
	if r := x.staff.do("PUT", "/api/v1/ai/providers/openai/key", map[string]any{"api_key": key}); r.code != http.StatusForbidden {
		t.Fatalf("staff set a key: %d", r.code)
	}
	if r := x.admin.do("PUT", "/api/v1/ai/providers/gemini/key", map[string]any{"api_key": key}); r.code != http.StatusNotFound {
		t.Fatalf("unknown provider: %d", r.code)
	}
	if r := x.admin.do("PUT", "/api/v1/ai/providers/openai/key", map[string]any{"api_key": ""}); r.code != http.StatusBadRequest {
		t.Fatalf("empty key: %d", r.code)
	}
	if r := x.admin.do("PUT", "/api/v1/ai/providers/openai/key", map[string]any{"api_key": key, "base_url": "https://elsewhere.example"}); r.code != http.StatusBadRequest {
		t.Fatalf("base_url on a fixed endpoint: %d", r.code)
	}
	if r := x.admin.do("PUT", "/api/v1/ai/providers/local/key", map[string]any{"api_key": key, "base_url": "ftp://models.example"}); r.code != http.StatusBadRequest {
		t.Fatalf("non-http base_url: %d", r.code)
	}
	set := x.admin.must(http.StatusOK, "PUT", "/api/v1/ai/providers/openai/key", map[string]any{"api_key": key})
	if set.json["configured"] != true || set.json["last4"] != "WXYZ" || strings.Contains(set.body, key) {
		t.Fatalf("set key: %s", set.body)
	}
	if actionCount(t, x.app, "setting.updated") == 0 {
		t.Fatal("no setting.updated event")
	}
	for _, path := range []string{"/api/v1/ai/providers", "/api/v1/settings", "/api/v1/audit-log?limit=200", "/api/v1/events?limit=200"} {
		if r := x.admin.must(http.StatusOK, "GET", path, nil); strings.Contains(r.body, key) {
			t.Fatalf("%s carries the key", path)
		}
	}
	var stored string
	if err := x.app.Pool.QueryRowContext(context.Background(), `SELECT value FROM settings WHERE key = 'ai.openai_api_key'`).Scan(&stored); err != nil || strings.Contains(stored, key) {
		t.Fatalf("key stored in the clear: %v", err)
	}

	// The Provider check runs against the endpoint's model list with the stored key.
	var accept bool
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || !accept || r.Header.Get("Authorization") != "Bearer synthetic-compatible-key-1234" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer models.Close()
	half := x.admin.must(http.StatusOK, "GET", "/api/v1/ai/providers", nil)
	for _, row := range half.json["data"].([]any) {
		if m := row.(map[string]any); m["provider"] == "openai_compatible" && m["configured"] != false {
			t.Fatalf("compatible configured with no key: %v", m)
		}
	}
	x.admin.must(http.StatusOK, "PUT", "/api/v1/ai/providers/openai_compatible/key", map[string]any{"api_key": "synthetic-compatible-key-1234", "base_url": models.URL + "/"})
	failed := x.admin.must(http.StatusOK, "POST", "/api/v1/ai/providers/openai_compatible/check", nil)
	if failed.json["ok"] != false || failed.json["detail"] == nil || strings.Contains(failed.body, "synthetic-compatible-key-1234") {
		t.Fatalf("refused check: %s", failed.body)
	}
	accept = true
	if r := x.admin.must(http.StatusOK, "POST", "/api/v1/ai/providers/openai_compatible/check", nil); r.json["ok"] != true {
		t.Fatalf("passing check: %s", r.body)
	}
	if n := actionCount(t, x.app, "ai.provider_checked"); n != 2 {
		t.Fatalf("ai.provider_checked events: %d", n)
	}
	if r := x.staff.do("POST", "/api/v1/ai/providers/openai_compatible/check", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff ran a check: %d", r.code)
	}
	listed := x.admin.must(http.StatusOK, "GET", "/api/v1/ai/providers", nil)
	for _, row := range listed.json["data"].([]any) {
		if m := row.(map[string]any); m["provider"] == "openai_compatible" && (m["configured"] != true || m["base_url"] != models.URL || m["last4"] != "1234") {
			t.Fatalf("compatible provider: %v", m)
		}
	}
	tok := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "ai admin", "scopes": []string{"ai:admin"}})
	agent := newClient(t, x.srv.URL)
	agent.bearer = str(t, tok.json, "secret")
	if r := mcpCall(agent, toolName(t, "GET", "/api/v1/ai/providers"), map[string]any{}); !strings.Contains(r.body, `"last4":"WXYZ"`) || strings.Contains(r.body, key) {
		t.Fatalf("MCP listAiProviders: %s", r.body)
	}
}

// TestEmailChange covers requestEmailChange and confirmEmailChange (PRD 8.7 users;
// AC-002): nothing changes until the emailed token is confirmed by the same user, an
// address another user holds is duplicate_email, and the token works once.
func TestEmailChange(t *testing.T) {
	x := newExt(t)
	const next = "sam.next@firm.example"
	meBefore := str(t, x.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "email")
	if r := x.staff.do("POST", "/api/v1/me/email-change", map[string]any{"email": adminEmail}); r.code != http.StatusConflict || !strings.Contains(r.body, "duplicate_email") {
		t.Fatalf("another user's address: %d %s", r.code, r.body)
	}
	for _, bad := range []string{"", "not-an-address", "Sam <sam@firm.example>", meBefore} {
		if r := x.staff.do("POST", "/api/v1/me/email-change", map[string]any{"email": bad}); r.code != http.StatusBadRequest {
			t.Fatalf("email %q: %d %s", bad, r.code, r.body)
		}
	}
	asked := x.staff.must(http.StatusOK, "POST", "/api/v1/me/email-change", map[string]any{"email": "Sam.Next@firm.example"})
	if asked.json["status"] != "verification_sent" || asked.json["email"] != next {
		t.Fatalf("request: %s", asked.body)
	}
	if got := str(t, x.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "email"); got != meBefore {
		t.Fatalf("email changed before confirmation: %s", got)
	}
	var token string
	for _, m := range x.smtp.to(next) {
		if tok := regexp.MustCompile(`Confirmation token: (\S+)`).FindStringSubmatch(m.Data); tok != nil {
			token = tok[1]
		}
	}
	if token == "" {
		t.Fatalf("no verification mail reached %s: %v", next, x.smtp.messages())
	}
	if r := x.atty.do("POST", "/api/v1/me/email-change/confirm", map[string]any{"token": token}); r.code != http.StatusForbidden {
		t.Fatalf("another user confirmed the token: %d %s", r.code, r.body)
	}
	if r := x.staff.do("POST", "/api/v1/me/email-change/confirm", map[string]any{"token": "forged.token"}); r.code != http.StatusUnauthorized {
		t.Fatalf("forged token: %d %s", r.code, r.body)
	}
	done := x.staff.must(http.StatusOK, "POST", "/api/v1/me/email-change/confirm", map[string]any{"token": token})
	if done.json["email"] != next || done.json["id"] != x.staffID {
		t.Fatalf("confirm: %s", done.body)
	}
	if got := str(t, x.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "email"); got != next {
		t.Fatalf("me after confirm: %s", got)
	}
	if r := x.staff.do("POST", "/api/v1/me/email-change/confirm", map[string]any{"token": token}); r.code != http.StatusUnauthorized {
		t.Fatalf("token reused: %d %s", r.code, r.body)
	}
	if actionCount(t, x.app, "user.email_change_requested") != 1 || actionCount(t, x.app, "user.updated") == 0 {
		t.Fatal("email change events missing")
	}
	if r := x.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?limit=200", nil); !strings.Contains(r.body, next) {
		t.Fatal("the email change is not in the audit log")
	}

	// The same operations through /mcp with a profile:write token.
	tok := x.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "profile", "owner_user_id": x.attyID, "scopes": []string{"profile:write"}})
	agent := newClient(t, x.srv.URL)
	agent.bearer = str(t, tok.json, "secret")
	r := mcpCall(agent, toolName(t, "POST", "/api/v1/me/email-change"), map[string]any{"body": map[string]any{"email": "ada.next@firm.example"}})
	if !strings.Contains(r.body, "verification_sent") {
		t.Fatalf("MCP requestEmailChange: %s", r.body)
	}
	if r := mcpCall(agent, toolName(t, "POST", "/api/v1/me/email-change"), map[string]any{"body": map[string]any{"email": next}}); !strings.Contains(r.body, "duplicate_email") {
		t.Fatalf("MCP duplicate: %s", r.body)
	}
}

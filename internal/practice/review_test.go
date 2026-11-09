package practice

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestWalledContactHiddenFromConflictCheck(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	open := makeMatter(t, svc, staff, area, "Open check", "")
	walled := makeMatter(t, svc, staff, area, "Behind the wall", "")
	hidden, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, FullName: "Visible Twin"})
	if err != nil {
		t.Fatal(err)
	}
	twin, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, FullName: "Visible Twin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, walled.ID, PartyInput{ContactID: hidden.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, open.ID, PartyInput{ContactID: twin.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Visible Twin", Relationship: "prior client"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, walled.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, caller := range []domain.Principal{staff, admin} {
		run, err := svc.RunConflictCheck(ctx, caller, open.ID)
		if err != nil {
			t.Fatal(err)
		}
		foundRecord := false
		for _, hit := range run.Hits {
			if hit.MatchedID == hidden.ID {
				t.Fatalf("%v received the walled contact %s", caller.Roles, hit.Name)
			}
			if hit.MatchedRecordType == "conflict_record" {
				foundRecord = true
			}
		}
		if !foundRecord {
			t.Fatal("expected the firm conflict record to score")
		}
	}
}

func TestClientSeesOnlySharedMatterItems(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Client matter", "")
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Cara", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	client := person(tid(9), domain.RoleClient)
	roles, err := domain.MarshalRoles(client.Roles)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutUser(ctx, domain.User{Meta: domain.Meta{ID: client.UserID}, Name: "Cara Client", Roles: roles, ContactID: contact.ID, Active: true}); err != nil {
		t.Fatal(err)
	}
	note, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "Internal", Body: "strategy-secret-body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateNote(ctx, staff, note.ID, NoteInput{Title: "Internal", Body: "strategy-secret-body revised"}); err != nil {
		t.Fatal(err)
	}
	now := svc.clock()
	if err := db.AppendAudit(ctx, nil, []domain.Event{
		{Meta: domain.Meta{ID: tid(70), CreatedAt: now}, MatterID: matter.ID, Summary: "client download", ClientVisible: true, ActorKind: "client"},
		{Meta: domain.Meta{ID: tid(71), CreatedAt: now}, MatterID: matter.ID, Summary: "internal strategy note", ClientVisible: false, ActorKind: "staff"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetMatter(ctx, client, matter.ID); err != nil {
		t.Fatal("client should read their matter", err)
	}
	if _, err := svc.History(ctx, client, "matter", matter.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	if _, err := svc.GetNote(ctx, client, note.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	runs := len(svc.db.Runs(matter.ID))
	if _, err := svc.RunConflictCheck(ctx, client, matter.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	if len(svc.db.Runs(matter.ID)) != runs {
		t.Fatal("client conflict check wrote a run")
	}
	if _, _, err := svc.PreviewClose(ctx, client, matter.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	timeline, err := svc.Timeline(ctx, client, matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHas(timeline, "client download") || timelineHas(timeline, "internal strategy note") || timelineHas(timeline, "strategy-secret-body") {
		t.Fatal(timeline)
	}
	staffLine, err := svc.Timeline(ctx, staff, matter.ID)
	if err != nil || !timelineHas(staffLine, "internal strategy note") {
		t.Fatal(err, staffLine)
	}
}

func TestIdempotencyKeyIsScoped(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	other := person(tid(4), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	first := makeMatter(t, svc, staff, area, "First close", "")
	second := makeMatter(t, svc, staff, area, "Second close", "")
	if _, _, err := svc.CloseMatter(ctx, staff, first.ID, CloseInput{Reason: "done", Acknowledge: true, IdempotencyKey: "same-key"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.CloseMatter(ctx, staff, second.ID, CloseInput{Reason: "done", Acknowledge: true, IdempotencyKey: "same-key"}); !isCode(err, domain.CodeConflict) {
		t.Fatal(err)
	}
	still, err := svc.db.Matter(ctx, second.ID)
	if err != nil || still.Status != StatusOpen {
		t.Fatal(still.Status, err)
	}
	replay, _, err := svc.CloseMatter(ctx, staff, first.ID, CloseInput{Reason: "done", Acknowledge: true, IdempotencyKey: "same-key"})
	if err != nil || replay.ID != first.ID || replay.Status != StatusClosed {
		t.Fatal(err, replay.Status)
	}
	created, err := svc.CreateMatter(ctx, staff, MatterInput{Name: "Keyed", PracticeAreaID: area, BillingType: BillingHourly, IdempotencyKey: "matter-key"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(svc.db.Matters())
	if _, err := svc.CreateMatter(ctx, other, MatterInput{Name: "Keyed", PracticeAreaID: area, BillingType: BillingHourly, IdempotencyKey: "matter-key"}); !isCode(err, domain.CodeConflict) {
		t.Fatal(err)
	}
	if len(svc.db.Matters()) != before {
		t.Fatal("second caller created a matter with someone else's key")
	}
	again, err := svc.CreateMatter(ctx, staff, MatterInput{Name: "Keyed", PracticeAreaID: area, BillingType: BillingHourly, IdempotencyKey: "matter-key"})
	if err != nil || again.ID != created.ID {
		t.Fatal(err, again.ID)
	}
	contacts := len(svc.db.Contacts())
	if _, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Pat", FamilyName: "Lee", IdempotencyKey: "same-key"}); !isCode(err, domain.CodeConflict) {
		t.Fatal(err)
	}
	if len(svc.db.Contacts()) != contacts {
		t.Fatal("close key created a contact")
	}
}

func TestEveryMatterFieldIsAuditedBeforeWrite(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	putUser(t, db, staff, "Sam Staff")
	probate := makeArea(t, svc, admin, "Probate")
	criminal, err := svc.CreateArea(ctx, admin, "Criminal", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	county, err := svc.CreateCounty(ctx, admin, "Boone", "MO")
	if err != nil {
		t.Fatal(err)
	}
	referral, err := svc.CreateReferral(ctx, admin, "Web", "web", domain.ID(""))
	if err != nil {
		t.Fatal(err)
	}
	numberField, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Label: "Count", FieldType: FieldNumber})
	if err != nil {
		t.Fatal(err)
	}
	before := len(svc.db.Matters())
	audits := len(svc.db.Audits())
	if _, err := svc.CreateMatter(ctx, staff, MatterInput{
		Name: "Bad custom", PracticeAreaID: probate, BillingType: BillingHourly,
		Custom: []CustomInput{{DefinitionID: numberField.ID, Value: "nope"}},
	}); err == nil {
		t.Fatal("invalid custom value accepted")
	}
	if len(svc.db.Matters()) != before || len(svc.db.Audits()) != audits {
		t.Fatal("invalid custom value persisted a matter or an audit row")
	}
	matter := makeMatter(t, svc, staff, probate, "Editable", "")
	updated, err := svc.UpdateMatter(ctx, staff, matter.ID, MatterInput{
		Name: "Editable", PracticeAreaID: criminal.ID, Stage: "", CaseNumber: "23AR-CV00825", Court: "Circuit",
		CountyID: county.ID, ClientPosition: "Petitioner", OpenedOn: "2026-01-02", StatuteOfLimitations: "2027-01-02",
		BillingType: BillingHourly, MatterRateCents: 15000, ResponsibleAttorneyID: attorney.UserID,
		AssignedStaffID: staff.UserID, ReferralSourceID: referral.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, attorney, "matter", updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"practice_area_id", "case_number", "court", "county_id", "client_position", "opened_on",
		"statute_of_limitations", "matter_rate_cents", "responsible_attorney_id", "assigned_staff_id", "referral_source_id",
	} {
		if !historyField(history, field) {
			t.Fatalf("history missing %s: %+v", field, history)
		}
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Rae", FamilyName: "Post"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddEmail(ctx, staff, contact.ID, "work", "rae@example.com", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddAddress(ctx, staff, domain.ContactAddress{ContactID: contact.ID, Line1: "1 Main", City: "Columbia", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddContactTag(ctx, staff, contact.ID, "witness"); err != nil {
		t.Fatal(err)
	}
	rows := svc.db.Audits()
	if !auditHas(rows, "contact_email", "email") || !auditHas(rows, "contact_address", "line1") || !auditHas(rows, "contact_tag", "tag") {
		t.Fatal("contact child write was not audited")
	}
	if !auditHas(rows, "custom_field_definition", "label") {
		t.Fatal("field definition was not audited")
	}
	if _, err := svc.RetireField(ctx, admin, numberField.ID); err != nil {
		t.Fatal(err)
	}
	if !auditHas(svc.db.Audits(), "custom_field_definition", "retired") {
		t.Fatal("retire was not audited")
	}
}

func TestCustomKeyPrefersActiveAndCSVUsesDefinitionID(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	first, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Key: "note", Label: "Note", FieldType: FieldText})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RetireField(ctx, admin, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Key: "note", Label: "Note", FieldType: FieldText})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Key: "other", Label: "Note", FieldType: FieldText})
	if err != nil {
		t.Fatal(err)
	}
	matter := makeMatter(t, svc, staff, area, "Two notes", "")
	if err := svc.SetCustomFields(ctx, staff, RecordMatter, matter.ID, area, []CustomInput{{Key: "note", Value: "active-value"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCustomFields(ctx, staff, RecordMatter, matter.ID, area, []CustomInput{{DefinitionID: other.ID, Value: "other-value"}}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetMatter(ctx, staff, matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[domain.ID]string{}
	for _, field := range view.CustomFields {
		got[field.DefinitionID] = field.Value
	}
	if got[second.ID] != "active-value" || got[first.ID] != "" || got[other.ID] != "other-value" {
		t.Fatalf("values %+v", got)
	}
	csv, err := svc.ExportMattersCSV(ctx, staff)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv, string(second.ID)) || !strings.Contains(csv, string(other.ID)) || !strings.Contains(csv, "active-value") || !strings.Contains(csv, "other-value") {
		t.Fatal(csv)
	}
}

func TestSearchShortcutIsAStaticFile(t *testing.T) {
	layout, err := os.ReadFile(filepath.Join(repoRoot(t), "web", "templates", "practice", "layout.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(layout), "<script>") || !strings.Contains(string(layout), `src="/practice/static/practice.js"`) {
		t.Fatal("shortcut is still inline")
	}
	svc, _ := newSvc(t)
	h := testHandler(t, svc)
	status, body := getAPI(t, svc, person(tid(3), domain.RoleStaff), "/practice/static/practice.js")
	_ = h
	if status != 200 || !strings.Contains(body, "keydown") || strings.Contains(body, "<script>") {
		t.Fatalf("script %d %s", status, body)
	}
}

func TestActivityFeedHidesWalledContact(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	public, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Public", FamilyName: "Person"})
	if err != nil {
		t.Fatal(err)
	}
	_ = public
	hidden, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Hidden", FamilyName: "Party"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, hidden.ID, "mobile", "+15555550199", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	matter := makeMatter(t, svc, staff, area, "Walled matter", "")
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: hidden.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, matter.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, caller := range []domain.Principal{staff, admin} {
		feed, err := svc.ActivityFeed(ctx, caller, "")
		if err != nil {
			t.Fatal(err)
		}
		if feedHas(feed, "Hidden Party") || feedHas(feed, "+15555550199") {
			t.Fatalf("%v still sees the walled contact: %+v", caller.Roles, feed)
		}
		if !feedHas(feed, "Public Person") {
			t.Fatal("public contact disappeared from the feed")
		}
		page := getHTML(t, svc, caller, "/practice/activity")
		if strings.Contains(page, "Hidden Party") || strings.Contains(page, "15555550199") {
			t.Fatal(page)
		}
	}
	attorneyFeed, err := svc.ActivityFeed(ctx, attorney, "")
	if err != nil || !feedHas(attorneyFeed, "Hidden Party") || !feedHas(attorneyFeed, "+15555550199") {
		t.Fatal(err, attorneyFeed)
	}
}

func TestPartyFieldEditsReachMatterHistory(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Party edits", "")
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Pat", FamilyName: "Lee"})
	if err != nil {
		t.Fatal(err)
	}
	party, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateParty(ctx, staff, party.ID, PartyInput{
		ContactID: contact.ID, Role: "client", Primary: false, RoleAttributes: "pro se",
		ServiceAddress: "1 Main", Notes: "call first",
	}); err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, staff, "matter", matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !historyHas(history, "primary", "true", "false") || !historyHas(history, "notes", "", "call first") || !historyHas(history, "service_address", "", "1 Main") || !historyHas(history, "role_attributes", "", "pro se") {
		t.Fatal(history)
	}
}

func TestContactHistoryCardAndSSNRedaction(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Ida", FamilyName: "Stone"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "+15555550100", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddEmail(ctx, staff, contact.ID, "work", "ida@example.com", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddAddress(ctx, staff, domain.ContactAddress{ContactID: contact.ID, Line1: "9 Oak", City: "Columbia"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddContactTag(ctx, staff, contact.ID, "client"); err != nil {
		t.Fatal(err)
	}
	const secret = "999-88-7777"
	if _, err := svc.UpdateContact(ctx, attorney, contact.ID, ContactInput{
		Kind: KindPerson, GivenName: "Ida", FamilyName: "Stone", SSNEIN: secret,
	}); err != nil {
		t.Fatal(err)
	}
	staffRows, err := svc.History(ctx, staff, "contact", contact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !historyField(staffRows, "phone_e164") || !historyField(staffRows, "email") || !historyField(staffRows, "line1") || !historyField(staffRows, "tag") {
		t.Fatal(staffRows)
	}
	if !historyRedacted(staffRows, "ssn_ein_ciphertext") {
		t.Fatal(staffRows)
	}
	raw, err := json.Marshal(staffRows)
	if err != nil || strings.Contains(string(raw), secret) {
		t.Fatal(err, string(raw))
	}
	status, body := getAPI(t, svc, staff, "/api/v1/contacts/"+string(contact.ID)+"/history")
	if status != 200 || strings.Contains(body, secret) || !strings.Contains(body, `"redacted":true`) || !strings.Contains(body, "+15555550100") {
		t.Fatalf("contact history api %d %s", status, body)
	}
	page := getHTML(t, svc, staff, "/practice/contacts/"+string(contact.ID))
	if !strings.Contains(page, "Edit history") || !strings.Contains(page, "15555550100") || !strings.Contains(page, "ida@example.com") || !strings.Contains(page, "9 Oak") || strings.Contains(page, secret) {
		t.Fatal(page)
	}
	attorneyRows, err := svc.History(ctx, attorney, "contact", contact.ID)
	if err != nil || !historyHas(attorneyRows, "ssn_ein_ciphertext", "", "changed") {
		t.Fatal(err, attorneyRows)
	}
	stored, err := svc.db.Contact(ctx, contact.ID)
	if err != nil || strings.Contains(stored.SSNEINCiphertext, secret) || !strings.HasPrefix(stored.SSNEINCiphertext, sealPrefix) {
		t.Fatal(stored.SSNEINCiphertext, err)
	}
	for _, row := range svc.db.Audits() {
		if strings.Contains(row.Entry.OldValue, secret) || strings.Contains(row.Entry.NewValue, secret) {
			t.Fatal(row.Entry)
		}
	}
}

func TestHiddenContactCannotBeAddedToAVisibleMatter(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	open := makeMatter(t, svc, staff, area, "Open matter", "")
	walled := makeMatter(t, svc, staff, area, "Secret matter", "")
	hidden, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Secret", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, walled.ID, PartyInput{ContactID: hidden.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, walled.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, open.ID, PartyInput{ContactID: hidden.ID, Role: "witness"}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, admin, open.ID, PartyInput{ContactID: hidden.ID, Role: "witness"}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: open.ID, ContactID: hidden.ID, Title: "Leak", Body: "no"}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.GetContact(ctx, staff, hidden.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	for _, party := range svc.db.Parties(open.ID, domain.ID("")) {
		if party.ContactID == hidden.ID {
			t.Fatal("hidden contact was linked")
		}
	}
}

func TestPracticeAPINotesAndActivityPages(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	status, body := postJSON(t, svc, staff, "/api/v1/matters", map[string]any{
		"Name": "API matter", "PracticeAreaID": area, "BillingType": BillingHourly,
	}, "")
	if status != 200 || !strings.Contains(body, "API matter") {
		t.Fatalf("create matter %d %s", status, body)
	}
	var created domain.Matter
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID.IsZero() {
		t.Fatal(err, body)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/contacts", map[string]any{
		"Kind": KindPerson, "GivenName": "Ada", "FamilyName": "API",
	}, "")
	if status != 200 || !strings.Contains(body, "Ada API") {
		t.Fatalf("create contact %d %s", status, body)
	}
	var contact domain.Contact
	if err := json.Unmarshal([]byte(body), &contact); err != nil {
		t.Fatal(err)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/matter-parties", map[string]any{
		"matter_id": created.ID, "ContactID": contact.ID, "Role": "client", "Primary": true,
	}, "")
	if status != 200 {
		t.Fatalf("party %d %s", status, body)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/notes", map[string]any{
		"MatterID": created.ID, "Title": "Tagged", "Body": "hello", "Tags": []string{"alpha"},
	}, "")
	if status != 200 {
		t.Fatalf("note %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/notes?tag=alpha")
	if status != 200 || !strings.Contains(body, "Tagged") {
		t.Fatalf("notes %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(created.ID)+"/timeline")
	if status != 200 || !strings.Contains(body, "Opened matter") || !strings.Contains(body, "Added note Tagged") {
		t.Fatalf("timeline %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(created.ID)+"/close-preview")
	if status != 200 || !strings.Contains(body, "warnings") {
		t.Fatalf("preview %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/worklists/matters-missing-case-number")
	if status != 200 {
		t.Fatalf("worklist %d %s", status, body)
	}
	status, _ = postJSON(t, svc, staff, "/api/v1/matters/"+string(created.ID)+"/conflict-checks", map[string]any{}, "")
	if status != 200 {
		t.Fatalf("check %d", status)
	}
	page := getHTML(t, svc, staff, "/practice/notes?tag=alpha")
	if !strings.Contains(page, "Tagged") || !strings.Contains(page, `name="matter"`) || !strings.Contains(page, `name="author"`) || !strings.Contains(page, `name="from"`) {
		t.Fatal(page)
	}
	feed := getHTML(t, svc, staff, "/practice/activity?tab=agents")
	if !strings.Contains(feed, "All (") || !strings.Contains(feed, "Clients (") || !strings.Contains(feed, "Staff (") || !strings.Contains(feed, "Agents (") {
		t.Fatal(feed)
	}
	view, err := svc.Activity(ctx, staff, ActivityQuery{Tab: "agents"})
	if err != nil || view.Agents < 0 {
		t.Fatal(err)
	}
	now := svc.clock()
	if err := svc.db.AppendAudit(ctx, nil, []domain.Event{
		{Meta: domain.Meta{ID: tid(80), CreatedAt: now}, ActorKind: "agent", Summary: "agent did a thing", Action: "agent.did"},
		{Meta: domain.Meta{ID: tid(81), CreatedAt: now}, ActorKind: "module", Summary: "module did a thing", Action: "module.did"},
	}); err != nil {
		t.Fatal(err)
	}
	agents, err := svc.Activity(ctx, staff, ActivityQuery{Tab: "agents"})
	if err != nil || !feedHas(agents.Groups, "agent did a thing") || !feedHas(agents.Groups, "module did a thing") || agents.Agents < 2 {
		t.Fatal(err, agents)
	}
	staffOnly, err := svc.Activity(ctx, staff, ActivityQuery{Tab: "staff"})
	if err != nil || feedHas(staffOnly.Groups, "agent did a thing") || feedHas(staffOnly.Groups, "module did a thing") {
		t.Fatal(staffOnly)
	}
	text := strings.Join(Pending, "\n")
	for _, needle := range []string{"contacts:bulk", "merge", "archive matter", "MCP transport"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("pending list lost %q", needle)
		}
	}
}

func TestSessionJSONRequiresCSRFAndHTTPSCookieIsSecure(t *testing.T) {
	svc, _ := newSvc(t)
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "CSRF matter", "")
	if _, _, err := svc.CloseMatter(context.Background(), staff, matter.ID, CloseInput{Reason: "done", Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, svc)
	token, _ := csrf(t, svc, attorney, "/practice/matters")
	res := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(`{"reason":"back"}`), attorney)
	req.Header.Set("Cookie", "session=browser")
	h.Routes().ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatalf("missing csrf %d %s", res.Code, res.Body.String())
	}
	res = httptest.NewRecorder()
	req = authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(`{"reason":"back"}`), attorney)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Idempotency-Key", "reopen-once")
	h.Routes().ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("csrf reopen %d %s", res.Code, res.Body.String())
	}
	secureReq := httptest.NewRequest(http.MethodGet, "/practice/matters", nil)
	secureReq.TLS = &tls.ConnectionState{}
	secureReq.Header.Set("X-Test-User", string(staff.UserID))
	secureReq.Header.Set("X-Test-Roles", "staff")
	secureRes := httptest.NewRecorder()
	h.Routes().ServeHTTP(secureRes, secureReq)
	cookie := secureRes.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Secure") || !strings.Contains(cookie, "Path=/") || strings.Contains(cookie, "Path=/practice") {
		t.Fatal(cookie)
	}
}

func TestConflictAuditCustomLinkAndReopenForm(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	if !auditHas(svc.db.Audits(), "practice_area", "name") {
		t.Fatal("practice area was not audited")
	}
	record, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Opposing Co", Relationship: "opposing party", Reason: "secret-reason"})
	if err != nil {
		t.Fatal(err)
	}
	if !auditHas(svc.db.Audits(), "conflict_record", "reason") {
		t.Fatal("conflict reason was not audited")
	}
	for _, row := range svc.db.Audits() {
		if row.Entry.Entity == "conflict_record" && row.Entry.Field == "reason" && !row.AttorneyOnly {
			t.Fatal("conflict reason is not attorney-only")
		}
	}
	_ = record
	staffMatter := makeMatter(t, svc, staff, area, "Visible", "")
	hiddenMatter := makeMatter(t, svc, staff, area, "Hidden matter", "")
	hidden, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Hidden", FamilyName: "Link"})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Visible", FamilyName: "Link"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, hiddenMatter.ID, PartyInput{ContactID: hidden.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, hiddenMatter.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	field, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Label: "Related", FieldType: FieldContact})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCustomFields(ctx, staff, RecordMatter, staffMatter.ID, area, []CustomInput{{DefinitionID: field.ID, Value: string(hidden.ID)}}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, staffMatter.ID, PartyInput{ContactID: visible.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCustomFields(ctx, staff, RecordMatter, staffMatter.ID, area, []CustomInput{{DefinitionID: field.ID, Value: string(visible.ID)}}); err != nil {
		t.Fatal(err)
	}
	open := makeMatter(t, svc, staff, area, "Check A", "")
	other := makeMatter(t, svc, staff, area, "Check B", "")
	if _, err := svc.AddParty(ctx, staff, open.ID, PartyInput{ContactID: visible.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, other.ID, PartyInput{ContactID: visible.ID, Role: "witness"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: visible.FullName, Relationship: "prior client"}); err != nil {
		t.Fatal(err)
	}
	first, err := svc.RunConflictCheck(ctx, attorney, open.ID)
	if err != nil || len(first.Hits) == 0 {
		t.Fatal(err, first.Hits)
	}
	second, err := svc.RunConflictCheck(ctx, attorney, other.ID)
	if err != nil || len(second.Hits) == 0 {
		t.Fatal(err, second.Hits)
	}
	if _, err := svc.DecideConflictOnCheck(ctx, attorney, second.ID, first.Hits[0].ID, DecisionClear, "no"); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.DecideConflictOnCheck(ctx, attorney, first.ID, first.Hits[0].ID, DecisionClear, "ok"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.CloseMatter(ctx, staff, staffMatter.ID, CloseInput{Reason: "done", Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	putUser(t, db, staff, "Sam Staff")
	if _, err := svc.CreateCounty(ctx, admin, "Boone", "MO"); err != nil || !auditHas(svc.db.Audits(), "county", "name") {
		t.Fatal(err)
	}
	if _, err := svc.CreateReferral(ctx, admin, "Neighbor", "person", domain.ID("")); err != nil || !auditHas(svc.db.Audits(), "referral_source", "name") {
		t.Fatal(err)
	}
	attorneyPage := getHTML(t, svc, attorney, "/practice/matters/"+string(staffMatter.ID))
	if !strings.Contains(attorneyPage, "Reopen matter") || !strings.Contains(attorneyPage, "Visible Link") || !strings.Contains(attorneyPage, "Sam Staff") {
		t.Fatal(attorneyPage)
	}
	staffPage := getHTML(t, svc, staff, "/practice/matters/"+string(staffMatter.ID))
	if strings.Contains(staffPage, "Reopen matter") {
		t.Fatal("staff saw reopen")
	}
}

func feedHas(groups []ActivityGroup, needle string) bool {
	for _, group := range groups {
		for _, row := range group.Rows {
			if strings.Contains(row.Summary, needle) {
				return true
			}
		}
	}
	return false
}

func historyField(rows []HistoryRow, field string) bool {
	for _, row := range rows {
		if row.Field == field && row.New != "" {
			return true
		}
	}
	return false
}

func auditHas(rows []storedAudit, entity, field string) bool {
	for _, row := range rows {
		if row.Entry.Entity == entity && row.Entry.Field == field {
			return true
		}
	}
	return false
}

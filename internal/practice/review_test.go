package practice

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
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
	opened, err := svc.GetContact(ctx, attorney, contact.ID)
	if err != nil || opened.SSNEINCiphertext != secret {
		t.Fatal(err, opened.SSNEINCiphertext)
	}
	hiddenID, err := svc.GetContact(ctx, staff, contact.ID)
	if err != nil || hiddenID.SSNEINCiphertext != "" {
		t.Fatal(err, hiddenID.SSNEINCiphertext)
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
		"name": "API matter", "practice_area_id": area, "billing_type": BillingHourly,
	}, "")
	if status != 200 || !strings.Contains(body, "API matter") || strings.Contains(body, "PracticeAreaID") {
		t.Fatalf("create matter %d %s", status, body)
	}
	var created struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID.IsZero() {
		t.Fatal(err, body)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/contacts", map[string]any{
		"kind": KindPerson, "given_name": "Ada", "family_name": "API",
	}, "")
	if status != 200 || !strings.Contains(body, "Ada API") || strings.Contains(body, "GivenName") {
		t.Fatalf("create contact %d %s", status, body)
	}
	var contact struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &contact); err != nil {
		t.Fatal(err)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/matter-parties", map[string]any{
		"matter_id": created.ID, "contact_id": contact.ID, "role": "client", "primary": true,
	}, "")
	if status != 200 {
		t.Fatalf("party %d %s", status, body)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/notes", map[string]any{
		"matter_id": created.ID, "title": "Tagged", "body": "hello", "tags": []string{"alpha"},
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

func TestHiddenContactLinkDoesNotPersist(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	hiddenMatter := makeMatter(t, svc, staff, area, "Hidden matter", "")
	hidden, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Hidden", FamilyName: "Link"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, hiddenMatter.ID, PartyInput{ContactID: hidden.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, hiddenMatter.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	matterField, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Label: "Related", FieldType: FieldContact})
	if err != nil {
		t.Fatal(err)
	}
	contactField, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordContact, Label: "Related person", FieldType: FieldContact})
	if err != nil {
		t.Fatal(err)
	}
	textField, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Label: "Note", FieldType: FieldText})
	if err != nil {
		t.Fatal(err)
	}
	mattersBefore := len(svc.db.Matters())
	contactsBefore := len(svc.db.Contacts())
	auditsBefore := len(svc.db.Audits())
	if _, err := svc.CreateMatter(ctx, staff, MatterInput{
		Name: "Should not exist", PracticeAreaID: area, BillingType: BillingHourly,
		Custom: []CustomInput{{DefinitionID: textField.ID, Value: "kept"}, {DefinitionID: matterField.ID, Value: string(hidden.ID)}},
	}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.CreateContact(ctx, staff, ContactInput{
		Kind: KindPerson, GivenName: "Also", FamilyName: "Hidden",
		Custom: []CustomInput{{DefinitionID: contactField.ID, Value: string(hidden.ID)}},
	}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if len(svc.db.Matters()) != mattersBefore || len(svc.db.Contacts()) != contactsBefore || len(svc.db.Audits()) != auditsBefore {
		t.Fatal("failed custom link persisted a row or an audit")
	}
	open := makeMatter(t, svc, staff, area, "Still open", "")
	personRow, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Pat", FamilyName: "Lee"})
	if err != nil {
		t.Fatal(err)
	}
	auditsBefore = len(svc.db.Audits())
	if _, err := svc.UpdateMatter(ctx, staff, open.ID, MatterInput{
		Name: "Renamed", PracticeAreaID: area, BillingType: BillingHourly,
		Custom: []CustomInput{{DefinitionID: textField.ID, Value: "nope"}, {DefinitionID: matterField.ID, Value: string(hidden.ID)}},
	}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	if _, err := svc.UpdateContact(ctx, staff, personRow.ID, ContactInput{
		Kind: KindPerson, GivenName: "Patricia", FamilyName: "Lee",
		Custom: []CustomInput{{DefinitionID: contactField.ID, Value: string(hidden.ID)}},
	}); !isCode(err, domain.CodeNotFound) {
		t.Fatal(err)
	}
	view, err := svc.GetMatter(ctx, staff, open.ID)
	if err != nil || view.Matter.Name != "Still open" || len(svc.db.Values(open.ID)) != 0 {
		t.Fatal(err, view.Matter.Name, svc.db.Values(open.ID))
	}
	got, err := svc.GetContact(ctx, staff, personRow.ID)
	if err != nil || got.GivenName != "Pat" || len(svc.db.Audits()) != auditsBefore {
		t.Fatal(err, got.GivenName, len(svc.db.Audits()), auditsBefore)
	}
}

func TestContactNotePhoneAndPartyChangesAreAudited(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Audit matter", "")
	row, err := svc.CreateContact(ctx, staff, ContactInput{
		Kind: KindOrganization, FullName: "Lee Co", EntityType: "LLC", FormationState: "MO", FormationDate: "2020-01-02", Source: "intake",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.UpdateContact(ctx, staff, row.ID, ContactInput{
		Kind: KindPerson, Prefix: "Ms", GivenName: "Pat", MiddleName: "Q", FamilyName: "Lee", Suffix: "Jr",
		PreferredName: "Patsy", AlsoKnownAs: "P Lee", DateOfBirth: "1980-01-02", DateOfDeath: "2024-02-03", Source: "desk",
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, staff, "contact", updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"given_name", "middle_name", "family_name", "prefix", "suffix", "preferred_name", "date_of_death", "source"} {
		if !historyField(history, field) {
			t.Fatal(field, history)
		}
	}
	if !historyHas(history, "entity_type", "LLC", "") || !historyHas(history, "formation_state", "MO", "") || !historyHas(history, "formation_date", "2020-01-02", "") {
		t.Fatal(history)
	}
	first, err := svc.AddPhone(ctx, staff, row.ID, "mobile", "+15555550100", "2026-01-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, row.ID, "work", "+15555550101", "2026-02-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePhone(ctx, staff, first.ID, "+15555550102", "", ""); err != nil {
		t.Fatal(err)
	}
	history, err = svc.History(ctx, staff, "contact", row.ID)
	if err != nil || !historyHas(history, "primary", "true", "false") || !historyHas(history, "effective_to", "", "2026-06-15") {
		t.Fatal(err, history)
	}
	firstParty, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: row.ID, Role: "client", Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Other", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: other.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	matterHistory, err := svc.History(ctx, staff, "matter", matter.ID)
	if err != nil || !historyHas(matterHistory, "primary", "true", "false") {
		t.Fatal(err, matterHistory)
	}
	_ = firstParty
	note, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "First", Body: "hello", Status: NoteActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateNote(ctx, staff, note.ID, NoteInput{
		Title: "Second", Body: "hello there", Pinned: true, Status: NoteArchived, Tags: []string{"heir"},
	}); err != nil {
		t.Fatal(err)
	}
	matterHistory, err = svc.History(ctx, staff, "matter", matter.ID)
	if err != nil || !historyHas(matterHistory, "title", "First", "Second") || !historyHas(matterHistory, "pinned", "false", "true") || !historyHas(matterHistory, "status", NoteActive, NoteArchived) || !historyHas(matterHistory, "tags", "", "heir") || !historyHas(matterHistory, "body", "hello", "hello there") {
		t.Fatal(err, matterHistory)
	}
}

func TestMarkersAPIAndScreen(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	client := person(tid(4), domain.RoleClient)
	putUser(t, db, staff, "Sam Staff")
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Marked", "")
	if err := svc.ensureMarkers(ctx); err != nil {
		t.Fatal(err)
	}
	zero, ok := svc.db.MarkerByLabel(MarkerZeroFee)
	if !ok {
		t.Fatal("zero fee marker missing")
	}
	status, body := postJSON(t, svc, staff, "/api/v1/matter-markers", map[string]string{"matter_id": string(matter.ID), "marker_id": string(zero.ID)}, "")
	if status != 200 || !strings.Contains(body, `"marker_id"`) || strings.Contains(body, "MarkerID") {
		t.Fatalf("set %d %s", status, body)
	}
	view, err := svc.GetMatter(ctx, staff, matter.ID)
	if err != nil || !hasMarker(view, MarkerZeroFee) || !svc.ZeroFee(matter.ID) {
		t.Fatal(err, view.Markers)
	}
	if !auditHas(svc.db.Audits(), "matter", "marker") {
		t.Fatal("marker was not audited")
	}
	page := getHTML(t, svc, staff, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(page, "Set marker") || !strings.Contains(page, "Sam Staff") || !strings.Contains(page, "2026-06-15 15:00") || !strings.Contains(page, "Opened matter") {
		t.Fatal(page)
	}
	token, _ := csrf(t, svc, staff, "/practice/matters/"+string(matter.ID))
	form := url.Values{}
	form.Set("csrf", token)
	form.Set("label", MarkerProBono)
	code, text := postForm(t, svc, staff, "/practice/matters/"+string(matter.ID)+"/markers", form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("form %d %s", code, text)
	}
	page = getHTML(t, svc, staff, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(page, "Remove "+MarkerProBono) {
		t.Fatal(page)
	}
	var proID domain.ID
	for _, marker := range svc.db.MatterMarkers(matter.ID) {
		if marker.Label == MarkerProBono {
			proID = marker.ID
		}
	}
	req := authReq(http.MethodDelete, "/api/v1/matter-markers/"+string(proID), nil, staff)
	res := httptest.NewRecorder()
	testHandler(t, svc).Routes().ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("remove %d %s", res.Code, res.Body.String())
	}
	view, err = svc.GetMatter(ctx, staff, matter.ID)
	if err != nil || hasMarker(view, MarkerProBono) || !hasMarker(view, MarkerZeroFee) {
		t.Fatal(err, view.Markers)
	}
	status, _ = postJSON(t, svc, client, "/api/v1/matter-markers", map[string]string{"matter_id": string(matter.ID), "marker_id": string(zero.ID)}, "")
	if status < 400 {
		t.Fatal(status)
	}
}

func TestPrefixedIdentifierMustOpen(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	attorney := person(tid(2), domain.RoleAttorney)
	if _, err := svc.CreateContact(ctx, attorney, ContactInput{Kind: KindPerson, GivenName: "Bad", FamilyName: "Seal", SSNEIN: "enc:v1:123-45-6789"}); !isCode(err, domain.CodeValidation) {
		t.Fatal(err)
	}
	real, err := svc.CreateContact(ctx, attorney, ContactInput{Kind: KindPerson, GivenName: "Real", FamilyName: "Seal", SSNEIN: "999-88-7777"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.db.Contact(ctx, real.ID)
	if err != nil || strings.Contains(stored.SSNEINCiphertext, "999-88-7777") {
		t.Fatal(stored.SSNEINCiphertext, err)
	}
	again, err := svc.CreateContact(ctx, attorney, ContactInput{Kind: KindPerson, GivenName: "Copy", FamilyName: "Seal", SSNEIN: stored.SSNEINCiphertext})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := svc.db.Contact(ctx, again.ID)
	if err != nil || copied.SSNEINCiphertext != stored.SSNEINCiphertext {
		t.Fatal(copied.SSNEINCiphertext, err)
	}
}

func TestCSVNeutralizesFormulas(t *testing.T) {
	got := csvJoin([]string{"=cmd", "+1", "-1", "@sum", "\t=cmd", "\r=cmd", "a\rb", `say "hi"`})
	for _, needle := range []string{"'=cmd", "'+1", "'-1", "'@sum", "'\t=cmd", "\"'\r=cmd\"", "\"a\rb\"", "\"say \"\"hi\"\"\""} {
		if !strings.Contains(got, needle) {
			t.Fatal(got)
		}
	}
}

func TestIdempotencyReserveIsAtomic(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	in := MatterInput{Name: "Race", PracticeAreaID: area, BillingType: BillingHourly, IdempotencyKey: "race-key"}
	rec := idemRecord{Actor: staff.UserID, Op: "create_matter", Target: "", Fingerprint: fingerprint(in)}
	if _, replay, err := svc.db.IdemClaim(ctx, "race-key", rec); err != nil || replay {
		t.Fatal(err, replay)
	}
	if _, _, err := svc.db.IdemClaim(ctx, "race-key", rec); !isCode(err, domain.CodeConflict) {
		t.Fatal(err)
	}
	svc.db.IdemAbort("race-key", rec)
	var wg sync.WaitGroup
	ids := make(chan domain.ID, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := svc.CreateMatter(ctx, staff, in)
			if err != nil {
				errs <- err
				return
			}
			ids <- row.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	seen := map[domain.ID]bool{}
	for id := range ids {
		seen[id] = true
	}
	for err := range errs {
		if !isCode(err, domain.CodeConflict) {
			t.Fatal(err)
		}
	}
	if len(seen) != 1 {
		t.Fatal(seen)
	}
	count := 0
	for _, row := range svc.db.Matters() {
		if row.Name == "Race" {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
}

func TestWalledNoteStaysOutOfContactHistory(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	visible := makeMatter(t, svc, staff, area, "Visible matter", "")
	walled := makeMatter(t, svc, staff, area, "Walled matter", "")
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Shared", FamilyName: "Person"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, visible.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, attorney, walled.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, walled.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	note, err := svc.CreateNote(ctx, attorney, NoteInput{MatterID: walled.ID, ContactID: contact.ID, Title: "Secret title", Body: "secret body"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateNote(ctx, attorney, note.ID, NoteInput{Title: "Secret title", Body: "updated secret"}); err != nil {
		t.Fatal(err)
	}
	staffRows, err := svc.History(ctx, staff, "contact", contact.ID)
	if err != nil {
		t.Fatal(err)
	}
	page := getHTML(t, svc, staff, "/practice/contacts/"+string(contact.ID))
	apiStatus, apiBody := getAPI(t, svc, staff, "/api/v1/contacts/"+string(contact.ID)+"/history")
	if apiStatus != 200 || strings.Contains(apiBody, "updated secret") || strings.Contains(apiBody, "Secret title") || strings.Contains(page, "updated secret") || historyHas(staffRows, "body", "secret body", "updated secret") {
		t.Fatal(apiBody, page, staffRows)
	}
	attorneyRows, err := svc.History(ctx, attorney, "contact", contact.ID)
	if err != nil || !historyHas(attorneyRows, "body", "secret body", "updated secret") {
		t.Fatal(err, attorneyRows)
	}
}

func TestContractRoutesAndCustomFilter(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	status, body := postJSON(t, svc, admin, "/api/v1/practice-areas", map[string]any{"name": "Estate", "stages": []string{"Open"}}, "")
	if status != 200 || !strings.Contains(body, "Estate") {
		t.Fatalf("area %d %s", status, body)
	}
	var area struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &area); err != nil {
		t.Fatal(err)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/practice-areas/"+string(area.ID))
	if status != 200 || !strings.Contains(body, "Estate") {
		t.Fatalf("get area %d %s", status, body)
	}
	status, body = postJSON(t, svc, admin, "/api/v1/custom-field-definitions", map[string]any{
		"record_type": "matter", "label": "Heir", "field_type": "text", "practice_area_ids": []string{string(area.ID)},
	}, "")
	if status != 200 || !strings.Contains(body, "Heir") {
		t.Fatalf("field %d %s", status, body)
	}
	var field struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &field); err != nil {
		t.Fatal(err)
	}
	matter := makeMatter(t, svc, staff, area.ID, "Filtered", "")
	_ = makeMatter(t, svc, staff, area.ID, "Plain", "")
	if err := svc.SetCustomFields(ctx, staff, RecordMatter, matter.ID, area.ID, []CustomInput{{DefinitionID: field.ID, Value: "niece"}}); err != nil {
		t.Fatal(err)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters?custom[heir]="+url.QueryEscape("niece"))
	if status != 200 || !strings.Contains(body, "Filtered") || strings.Contains(body, "Plain") {
		t.Fatalf("filter %d %s", status, body)
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Rita", FamilyName: "Stone"})
	if err != nil {
		t.Fatal(err)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/contacts/"+string(contact.ID))
	if status != 200 || !strings.Contains(body, "Rita") {
		t.Fatalf("contact %d %s", status, body)
	}
	status, body = postJSON(t, svc, staff, "/api/v1/contact-phones", map[string]any{"contact_id": contact.ID, "e164": "+15555550100", "primary": true, "effective_from": "2026-01-01"}, "")
	if status != 200 {
		t.Fatalf("phone %d %s", status, body)
	}
	var phone struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &phone); err != nil {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, staff, "/api/v1/contact-phones/"+string(phone.ID), map[string]any{"e164": "+15555550109"})
	if status != 200 || !strings.Contains(body, "15555550109") {
		t.Fatalf("change phone %d %s", status, body)
	}
	note, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "Old", Body: "body"})
	if err != nil {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, staff, "/api/v1/notes/"+string(note.ID), map[string]any{"title": "New", "body": "body", "pinned": true, "status": "active"})
	if status != 200 || !strings.Contains(body, "New") {
		t.Fatalf("note %d %s", status, body)
	}
	record, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Opposing", Relationship: "other"})
	if err != nil {
		t.Fatal(err)
	}
	status, body = getAPI(t, svc, attorney, "/api/v1/conflict-records/"+string(record.ID))
	if status != 200 || !strings.Contains(body, "Opposing") {
		t.Fatalf("conflict %d %s", status, body)
	}
	status, body = postJSON(t, svc, admin, "/api/v1/custom-field-definitions/"+string(field.ID)+"/retire", map[string]any{}, "")
	if status != 200 || !strings.Contains(body, "true") {
		t.Fatalf("retire %d %s", status, body)
	}
}

func TestPartialPatchKeepsOmittedFields(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	status, body := postJSON(t, svc, staff, "/api/v1/matters", map[string]any{
		"name": "Patch me", "practice_area_id": area, "billing_type": BillingHourly,
		"court": "Circuit", "stage": "Intake", "responsible_attorney_user_id": attorney.UserID,
	}, "")
	if status != 200 || !strings.Contains(body, `"practice_area_id"`) {
		t.Fatalf("create %d %s", status, body)
	}
	var created struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, staff, "/api/v1/matters/"+string(created.ID), map[string]any{"case_number": "2026-CV-1"})
	if status != 200 || !strings.Contains(body, "2026-CV-1") || !strings.Contains(body, "Circuit") || !strings.Contains(body, "Intake") {
		t.Fatalf("patch %d %s", status, body)
	}
	view, err := svc.GetMatter(ctx, staff, created.ID)
	if err != nil || view.Matter.Court != "Circuit" || view.Matter.Stage != "Intake" || view.Matter.ResponsibleAttorneyID != attorney.UserID || view.Matter.CaseNumber != "2026-CV-1" {
		t.Fatal(err, view.Matter)
	}
	history, err := svc.History(ctx, staff, "matter", created.ID)
	if err != nil || !historyHas(history, "case_number", "", "2026-CV-1") || historyHas(history, "court", "Circuit", "") || historyHas(history, "stage", "Intake", "") {
		t.Fatal(err, history)
	}
	contactStatus, contactBody := postJSON(t, svc, staff, "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Pat", "family_name": "Lee", "preferred_name": "Patsy",
	}, "")
	if contactStatus != 200 {
		t.Fatal(contactBody)
	}
	var contact struct {
		ID domain.ID `json:"id"`
	}
	if err := json.Unmarshal([]byte(contactBody), &contact); err != nil {
		t.Fatal(err)
	}
	contactStatus, contactBody = patchJSON(t, svc, staff, "/api/v1/contacts/"+string(contact.ID), map[string]any{"preferred_name": "Patricia"})
	if contactStatus != 200 || !strings.Contains(contactBody, "Patricia") || !strings.Contains(contactBody, "Lee") {
		t.Fatal(contactBody)
	}
	got, err := svc.GetContact(ctx, staff, contact.ID)
	if err != nil || got.FamilyName != "Lee" || got.GivenName != "Pat" || got.PreferredName != "Patricia" {
		t.Fatal(err, got)
	}
	note, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: created.ID, Title: "Keep", Body: "still here", Pinned: true})
	if err != nil {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, staff, "/api/v1/notes/"+string(note.ID), map[string]any{"title": "Renamed"})
	if status != 200 {
		t.Fatal(body)
	}
	saved, err := svc.GetNote(ctx, staff, note.ID)
	if err != nil || saved.Title != "Renamed" || saved.Body != "still here" || !saved.Pinned {
		t.Fatal(err, saved)
	}
	first, err := svc.AddParty(ctx, staff, created.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true, Notes: "original"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Other", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.AddParty(ctx, staff, created.ID, PartyInput{ContactID: other.ID, Role: "client", Notes: "second"})
	if err != nil {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, staff, "/api/v1/matter-parties/"+string(second.ID), map[string]any{"primary": true})
	if status != 200 || !strings.Contains(body, "second") || !strings.Contains(body, `"primary":true`) {
		t.Fatalf("party patch %d %s", status, body)
	}
	again, err := svc.GetParty(ctx, staff, second.ID)
	if err != nil || !again.Primary || again.Notes != "second" || again.Role != "client" {
		t.Fatal(err, again)
	}
	demoted, err := svc.GetParty(ctx, staff, first.ID)
	if err != nil || demoted.Primary || demoted.Notes != "original" {
		t.Fatal(err, demoted)
	}
}

func TestCatalogEditsAreAuditedAndPartialPatchKeepsFlags(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area, err := svc.CreateArea(ctx, admin, "Probate", "", []string{"Intake"})
	if err != nil {
		t.Fatal(err)
	}
	inactive := false
	if _, err := svc.UpdateArea(ctx, admin, area.ID, "", []string{"Intake", "Open"}, &inactive); err != nil {
		t.Fatal(err)
	}
	if !auditHas(svc.db.Audits(), "practice_area", "stages") || !auditHas(svc.db.Audits(), "practice_area", "active") || !eventHas(svc, "practice_area.updated") {
		t.Fatal(svc.db.Audits())
	}
	status, body := patchJSON(t, svc, admin, "/api/v1/practice-areas/"+string(area.ID), map[string]any{"name": "Estate"})
	if status != 200 || !strings.Contains(body, `"active":false`) {
		t.Fatalf("area patch %d %s", status, body)
	}
	county, err := svc.CreateCounty(ctx, admin, "Boone", "MO")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateCounty(ctx, admin, county.ID, "", "IA"); err != nil || !auditHas(svc.db.Audits(), "county", "state") {
		t.Fatal(err)
	}
	referral, err := svc.CreateReferral(ctx, admin, "Neighbor", "person", domain.ID(""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateReferral(ctx, admin, referral.ID, "", "firm", &inactive); err != nil {
		t.Fatal(err)
	}
	if !auditHas(svc.db.Audits(), "referral_source", "kind") || !auditHas(svc.db.Audits(), "referral_source", "active") {
		t.Fatal(svc.db.Audits())
	}
	status, body = patchJSON(t, svc, admin, "/api/v1/referral-sources/"+string(referral.ID), map[string]any{"name": "Friend"})
	if status != 200 || !strings.Contains(body, `"active":false`) {
		t.Fatalf("referral patch %d %s", status, body)
	}
	required := true
	field, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordMatter, Label: "Heir", FieldType: FieldText, Required: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateField(ctx, admin, field.ID, "", nil, []domain.ID{area.ID}); err != nil || !auditHas(svc.db.Audits(), "custom_field_definition", "practice_area_ids") {
		t.Fatal(err)
	}
	status, body = patchJSON(t, svc, admin, "/api/v1/custom-field-definitions/"+string(field.ID), map[string]any{"label": "Heir note"})
	if status != 200 || !strings.Contains(body, `"required":true`) {
		t.Fatalf("field patch %d %s", status, body)
	}
	_ = required
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Pat", FamilyName: "Lee"})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "+15555550100", "2026-01-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevisePhone(ctx, staff, phone.ID, "", "", "cell"); err != nil || !auditHas(svc.db.Audits(), "contact_phone", "label") || !eventHas(svc, "contact.phone_updated") {
		t.Fatal(err)
	}
	email, err := svc.AddEmail(ctx, staff, contact.ID, "work", "pat@example.com", "2026-01-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviseEmail(ctx, staff, email.ID, "", "home"); err != nil || !auditHas(svc.db.Audits(), "contact_email", "label") || !eventHas(svc, "contact.email_updated") {
		t.Fatal(err)
	}
	address, err := svc.AddAddress(ctx, staff, domain.ContactAddress{ContactID: contact.ID, Label: "home", Line1: "1 Main", City: "Columbia", Region: "MO", PostalCode: "65201", Country: "US"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviseAddress(ctx, staff, address.ID, "", "mailing"); err != nil || !auditHas(svc.db.Audits(), "contact_address", "label") || !eventHas(svc, "contact.address_updated") {
		t.Fatal(err)
	}
	tag, err := svc.AddContactTag(ctx, staff, contact.ID, "vip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReviseTag(ctx, staff, tag.ID, "lead"); err != nil || !auditHas(svc.db.Audits(), "contact_tag", "tag") || !eventHas(svc, "contact.tag_updated") {
		t.Fatal(err)
	}
}

func eventHas(svc *Service, action string) bool {
	for _, row := range svc.db.Events() {
		if row.Action == action {
			return true
		}
	}
	return false
}

func TestReopenReplayChecksTheWall(t *testing.T) {
	svc, db := newSvc(t)
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, admin, "Admin")
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Walled replay", "")
	if _, _, err := svc.CloseMatter(context.Background(), staff, matter.ID, CloseInput{Reason: "done", Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, svc)
	token, _ := csrf(t, svc, attorney, "/practice/matters")
	body := `{"reason":"back"}`
	first := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(body), attorney)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Idempotency-Key", "reopen-wall")
	h.Routes().ServeHTTP(first, req)
	if first.Code != 200 {
		t.Fatal(first.Body.String())
	}
	if err := svc.SetWall(context.Background(), admin, matter.ID, []domain.ID{admin.UserID}); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	req = authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(body), attorney)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Idempotency-Key", "reopen-wall")
	h.Routes().ServeHTTP(second, req)
	if second.Code == 200 || strings.Contains(second.Body.String(), "Walled replay") {
		t.Fatalf("replay leaked %d %s", second.Code, second.Body.String())
	}
}

func TestWalledConflictHitIsHiddenOnRead(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, admin, "Admin")
	area := makeArea(t, svc, admin, "Probate")
	open := makeMatter(t, svc, staff, area, "Open matter", "")
	walled := makeMatter(t, svc, staff, area, "Walled matter", "")
	visible, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Jane", FamilyName: "Doe"})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := svc.CreateContact(ctx, admin, ContactInput{Kind: KindPerson, GivenName: "Jane", FamilyName: "Doe"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, open.ID, PartyInput{ContactID: visible.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	secretParty, err := svc.AddParty(ctx, admin, walled.ID, PartyInput{ContactID: secret.ID, Role: "client", Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, walled.ID, []domain.ID{admin.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunConflictCheck(ctx, admin, open.ID); err != nil {
		t.Fatal(err)
	}
	adminRows, err := svc.ConflictChecks(ctx, admin, open.ID)
	if err != nil || !hitMatches(adminRows, secretParty.ID) {
		t.Fatal(err, adminRows)
	}
	staffRows, err := svc.ConflictChecks(ctx, staff, open.ID)
	if err != nil || hitMatches(staffRows, secretParty.ID) {
		t.Fatal(err, staffRows)
	}
	status, body := getAPI(t, svc, staff, "/api/v1/matters/"+string(open.ID)+"/conflict-checks")
	if status != 200 || strings.Contains(body, string(secretParty.ID)) || strings.Contains(body, "MatterID") || !strings.Contains(body, `"matter_id"`) || !strings.Contains(body, `"hits"`) {
		t.Fatalf("staff checks %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(open.ID)+"/timeline")
	if status != 200 || strings.Contains(body, "ActorKind") || !strings.Contains(body, `"actor_kind"`) || !strings.Contains(body, `"summary"`) {
		t.Fatalf("timeline %d %s", status, body)
	}
	if _, err := svc.CreateCounty(ctx, admin, "Boone", "MO"); err != nil {
		t.Fatal(err)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/counties")
	if status != 200 || strings.Contains(body, "CreatedAt") || !strings.Contains(body, `"created_at"`) || !strings.Contains(body, `"state"`) {
		t.Fatalf("counties %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(open.ID)+"/wall")
	if status != 200 || strings.Contains(body, "UserID") || !strings.Contains(body, `"user_ids"`) {
		t.Fatalf("wall %d %s", status, body)
	}
}

func hitMatches(runs []ConflictRun, id domain.ID) bool {
	for _, run := range runs {
		for _, hit := range run.Hits {
			if hit.MatchedID == id {
				return true
			}
		}
	}
	return false
}

func TestPhoneNumberChangeKeepsTheRequestedLabel(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Pat", FamilyName: "Lee"})
	if err != nil {
		t.Fatal(err)
	}
	phone, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "+15555550100", "2026-01-01", true)
	if err != nil {
		t.Fatal(err)
	}
	next, err := svc.RevisePhone(ctx, staff, phone.ID, "+15555550109", "", "desk")
	if err != nil || next.Label != "desk" || next.PhoneE164 != "+15555550109" {
		t.Fatal(err, next)
	}
	if !auditHas(svc.db.Audits(), "contact_phone", "label") {
		t.Fatal(svc.db.Audits())
	}
}

func TestContactNoteCreateIsInContactHistory(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Note", FamilyName: "Only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateNote(ctx, staff, NoteInput{ContactID: contact.ID, Title: "Only contact", Body: "private"}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.History(ctx, staff, "contact", contact.ID)
	if err != nil || !historyHas(rows, "body", "", "Only contact") {
		t.Fatal(err, rows)
	}
}

func TestContactCustomFieldRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	field, err := svc.CreateField(ctx, admin, FieldDef{RecordType: RecordContact, Label: "Nickname", FieldType: FieldText})
	if err != nil {
		t.Fatal(err)
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Rita", FamilyName: "Stone"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetCustomFields(ctx, staff, RecordContact, contact.ID, domain.ID(""), []CustomInput{{DefinitionID: field.ID, Value: "R"}}); err != nil {
		t.Fatal(err)
	}
	status, body := getAPI(t, svc, staff, "/api/v1/contacts/"+string(contact.ID))
	if status != 200 || !strings.Contains(body, `"custom_fields"`) || !strings.Contains(body, "nickname") || !strings.Contains(body, `"value":"R"`) {
		t.Fatalf("read %d %s", status, body)
	}
	page := getHTML(t, svc, staff, "/practice/contacts/"+string(contact.ID))
	if !strings.Contains(page, "Nickname") || !strings.Contains(page, `value="R"`) || !strings.Contains(page, "Save custom fields") {
		t.Fatal(page)
	}
}

func TestSealingKeyCanBeSupplied(t *testing.T) {
	db := NewMemory()
	reg, err := settings.New(settings.NewMemory(), quietAudit{})
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	svc := NewWithKey(db, reg, key)
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC) }
	var n uint64
	svc.newID = func() (domain.ID, error) {
		n++
		return domain.ComposeV7(1_700_000_000_000+n, uint16(n%4000), n+1)
	}
	attorney := person(tid(2), domain.RoleAttorney)
	row, err := svc.CreateContact(context.Background(), attorney, ContactInput{Kind: KindPerson, GivenName: "Sealed", FamilyName: "Key", SSNEIN: "111-22-3333"})
	if err != nil {
		t.Fatal(err)
	}
	same := NewWithKey(db, reg, key)
	same.now = svc.now
	opened, err := same.GetContact(context.Background(), attorney, row.ID)
	if err != nil || opened.SSNEINCiphertext != "111-22-3333" {
		t.Fatal(err, opened.SSNEINCiphertext)
	}
	other := NewWithKey(db, reg, bytes.Repeat([]byte{9}, 32))
	other.now = svc.now
	blank, err := other.GetContact(context.Background(), attorney, row.ID)
	if err != nil || blank.SSNEINCiphertext != "" {
		t.Fatal(err, blank.SSNEINCiphertext)
	}
}

func TestReopenReplayReturnsTheSameDocument(t *testing.T) {
	svc, _ := newSvc(t)
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Replay", "")
	if _, _, err := svc.CloseMatter(context.Background(), staff, matter.ID, CloseInput{Reason: "done", Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, svc)
	token, _ := csrf(t, svc, attorney, "/practice/matters")
	body := `{"reason":"back"}`
	first := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(body), attorney)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Idempotency-Key", "reopen-doc")
	h.Routes().ServeHTTP(first, req)
	second := httptest.NewRecorder()
	req = authReq(http.MethodPost, "/api/v1/matters/"+string(matter.ID)+"/reopen", strings.NewReader(body), attorney)
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Idempotency-Key", "reopen-doc")
	h.Routes().ServeHTTP(second, req)
	if first.Code != 200 || second.Body.String() != first.Body.String() || !strings.Contains(first.Body.String(), `"practice_area_id"`) || !strings.Contains(first.Body.String(), `"billing_type"`) {
		t.Fatalf("first %d %s\nsecond %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
}

func TestPracticeScreensCreateEditAndDecide(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	area := makeArea(t, svc, admin, "Probate")
	token, _ := csrf(t, svc, staff, "/practice/matters")
	form := url.Values{}
	form.Set("csrf", token)
	form.Set("name", "Screen matter")
	form.Set("practice_area_id", string(area))
	form.Set("billing_type", BillingHourly)
	code, text := postForm(t, svc, staff, "/practice/matters", form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("create matter %d %s", code, text)
	}
	var matter domain.Matter
	for _, row := range svc.db.Matters() {
		if row.Name == "Screen matter" {
			matter = row
		}
	}
	if matter.ID.IsZero() {
		t.Fatal("matter was not created")
	}
	token, _ = csrf(t, svc, staff, "/practice/matters/"+string(matter.ID))
	form = url.Values{}
	form.Set("csrf", token)
	form.Set("name", "Screen matter")
	form.Set("court", "Circuit")
	form.Set("responsible_attorney_user_id", string(attorney.UserID))
	code, text = postForm(t, svc, staff, "/practice/matters/"+string(matter.ID), form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("edit %d %s", code, text)
	}
	page := getHTML(t, svc, staff, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(page, "Screen matter") || !strings.Contains(page, "Responsible attorney Ada Attorney") || !strings.Contains(page, "Add party") || !strings.Contains(page, "Add note") || !strings.Contains(page, "Run conflict check") {
		t.Fatal(page)
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Party", FamilyName: "One"})
	if err != nil {
		t.Fatal(err)
	}
	form = url.Values{}
	form.Set("csrf", token)
	form.Set("contact_id", string(contact.ID))
	form.Set("role", "client")
	form.Set("primary", "1")
	code, text = postForm(t, svc, staff, "/practice/matters/"+string(matter.ID)+"/parties", form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("party %d %s", code, text)
	}
	form = url.Values{}
	form.Set("csrf", token)
	form.Set("title", "Hearing")
	form.Set("body", "Bring the file")
	code, text = postForm(t, svc, staff, "/practice/matters/"+string(matter.ID)+"/notes", form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("note %d %s", code, text)
	}
	page = getHTML(t, svc, staff, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(page, "Party One") || !strings.Contains(page, "Hearing") {
		t.Fatal(page)
	}
	if _, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Party One", Relationship: "other"}); err != nil {
		t.Fatal(err)
	}
	form = url.Values{}
	form.Set("csrf", token)
	code, text = postForm(t, svc, attorney, "/practice/matters/"+string(matter.ID)+"/conflict-checks", form, token)
	if code != http.StatusSeeOther {
		t.Fatalf("check %d %s", code, text)
	}
	checks, err := svc.ConflictChecks(ctx, attorney, matter.ID)
	if err != nil || len(checks) == 0 || len(checks[0].Hits) == 0 {
		t.Fatal(err, checks)
	}
	atoken, _ := csrf(t, svc, attorney, "/practice/matters/"+string(matter.ID))
	form = url.Values{}
	form.Set("csrf", atoken)
	form.Set("hit_id", string(checks[0].Hits[0].ID))
	form.Set("decision", DecisionClear)
	form.Set("reason", "known client")
	code, text = postForm(t, svc, attorney, "/practice/conflict-checks/"+string(checks[0].ID)+"/decisions", form, atoken)
	if code != http.StatusSeeOther {
		t.Fatalf("decision %d %s", code, text)
	}
	ctoken, _ := csrf(t, svc, staff, "/practice/contacts")
	form = url.Values{}
	form.Set("csrf", ctoken)
	form.Set("kind", KindPerson)
	form.Set("given_name", "New")
	form.Set("family_name", "Person")
	code, text = postForm(t, svc, staff, "/practice/contacts", form, ctoken)
	if code != http.StatusSeeOther {
		t.Fatalf("contact %d %s", code, text)
	}
	var created domain.ID
	for _, row := range svc.db.Contacts() {
		if row.FullName == "New Person" {
			created = row.ID
		}
	}
	page = getHTML(t, svc, staff, "/practice/contacts/"+string(created))
	if !strings.Contains(page, "Save contact") || !strings.Contains(page, "Archive contact") {
		t.Fatal(page)
	}
	form = url.Values{}
	form.Set("csrf", ctoken)
	code, text = postForm(t, svc, staff, "/practice/contacts/"+string(created)+"/archive", form, ctoken)
	if code != http.StatusSeeOther {
		t.Fatalf("archive %d %s", code, text)
	}
	got, err := svc.GetContact(ctx, staff, created)
	if err != nil || !got.Archived {
		t.Fatal(err, got.Archived)
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

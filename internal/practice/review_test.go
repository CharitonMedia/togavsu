package practice

import (
	"context"
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

package practice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestAuditLogKeepsWallAndRedaction is the service half of the audit-log read scope
// (AC-003, AC-024): an admin outside a wall gets no ledger row for that matter, its
// party contact or its note; an admin on the wall does; attorney-only values stay
// redacted for a caller who cannot read them; rows from other services pass through.
func TestAuditLogKeepsWallAndRedaction(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	inside := person(tid(1), domain.RoleAdmin)
	outside := person(tid(4), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	for _, u := range []struct {
		p    domain.Principal
		name string
	}{{inside, "Ines Inside"}, {outside, "Otto Outside"}, {attorney, "Ada Attorney"}, {staff, "Sam Staff"}} {
		putUser(t, db, u.p, u.name)
	}
	area := makeArea(t, svc, inside, "Probate")

	walled := makeMatter(t, svc, inside, area, "Walled Quist estate", "")
	if err := svc.SetWall(ctx, inside, walled.ID, []domain.ID{inside.UserID}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMatter(ctx, inside, walled.ID, MatterInput{Name: "Walled Quist estate II", PracticeAreaID: area, BillingType: BillingHourly}); err != nil {
		t.Fatal(err)
	}
	heir, err := svc.CreateContact(ctx, inside, ContactInput{Kind: KindPerson, FullName: "Hidden Heir Zebulon"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, inside, walled.ID, PartyInput{ContactID: heir.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateNote(ctx, inside, NoteInput{MatterID: walled.ID, Title: "Walled memo title", Body: "Walled memo body"}); err != nil {
		t.Fatal(err)
	}

	open := makeMatter(t, svc, attorney, area, "Open Rivera estate", "")
	secret := "privilege-strategy-4b8c"
	if _, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Opposing Co", Relationship: "opposing party", MatterID: open.ID, Reason: secret}); err != nil {
		t.Fatal(err)
	}

	var ledger []domain.AuditEntry
	for _, row := range db.Audits() {
		ledger = append(ledger, row.Entry)
	}
	// Rows practice did not record: another service's setting row passes; a note row
	// with no practice scope is dropped; a matter row is scoped by its entity id.
	ledger = append(ledger,
		domain.AuditEntry{Meta: domain.Meta{ID: tid(90)}, Entity: "setting", EntityID: "practice.matter_page_size", Field: "value", NewValue: "25"},
		domain.AuditEntry{Meta: domain.Meta{ID: tid(91)}, Entity: "note", EntityID: tid(92), Field: "body", NewValue: "orphan-note-body"},
		domain.AuditEntry{Meta: domain.Meta{ID: tid(93)}, Entity: "matter", EntityID: walled.ID, Field: "name", NewValue: "ledger-only-walled-name"},
	)

	dump := func(rows []HistoryRow) string {
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	leaks := []string{string(walled.ID), string(heir.ID), "Quist", "Zebulon", "Walled memo", "orphan-note-body", "ledger-only-walled-name"}

	out := dump(svc.AuditLog(ctx, outside, ledger))
	for _, leak := range leaks {
		if strings.Contains(out, leak) {
			t.Fatalf("admin outside the wall received %q: %s", leak, out)
		}
	}
	if !strings.Contains(out, "Open Rivera estate") || !strings.Contains(out, "practice.matter_page_size") {
		t.Fatalf("admin outside the wall lost readable rows: %s", out)
	}

	in := dump(svc.AuditLog(ctx, inside, ledger))
	for _, want := range []string{"Walled Quist estate II", "Zebulon", "Walled memo", "ledger-only-walled-name", secret} {
		if !strings.Contains(in, want) {
			t.Fatalf("admin on the wall is missing %q: %s", want, in)
		}
	}
	if strings.Contains(in, "orphan-note-body") {
		t.Fatalf("an unscoped note row reached an admin: %s", in)
	}

	staffRows := svc.AuditLog(ctx, staff, ledger)
	if s := dump(staffRows); strings.Contains(s, secret) || !historyRedacted(staffRows, "reason") {
		t.Fatalf("attorney-only reason not redacted for staff: %s", s)
	}
}

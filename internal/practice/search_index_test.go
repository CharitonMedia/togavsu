package practice

import (
	"context"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestSearchIndexFollowsWritesAndWalls: the memory store's search-text index is built on
// the first search, so a later matter, contact, phone, email or invoice write must show in
// the next search, and a wall set after the index exists must still hide the matter, its
// invoice and a person who is only on it (AC-017 performance, AC-020 search, AC-003 walls).
func TestSearchIndexFollowsWritesAndWalls(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	putUser(t, db, staff, "Sam Staff")
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Original name", staff.UserID)
	search := func(q, scope string) SearchResult {
		t.Helper()
		r, err := svc.Search(ctx, staff, q, scope)
		if err != nil {
			t.Fatal(q, err)
		}
		return r
	}
	if !searchHas(search("original", ScopeMatters), matter.ID) {
		t.Fatal("matter not found before any write")
	}

	matter.Name = "Renamed estate"
	if err := db.UpdateMatter(ctx, matter); err != nil {
		t.Fatal(err)
	}
	if searchHas(search("original", ScopeMatters), matter.ID) || !searchHas(search("renamed", ScopeMatters), matter.ID) {
		t.Fatal("matter rename not searched")
	}
	second := makeMatter(t, svc, staff, area, "Second probate", staff.UserID)
	if !searchHas(search("second probate", ScopeMatters), second.ID) {
		t.Fatal("new matter not searched")
	}

	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Rita", FamilyName: "Stone"})
	if err != nil {
		t.Fatal(err)
	}
	if !searchHas(search("rita", ScopePeople), contact.ID) {
		t.Fatal("new contact not searched")
	}
	if _, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "+15555550142", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddEmail(ctx, staff, contact.ID, "work", "rita@stone.example", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if !searchHas(search("+15555550142", ScopePeople), contact.ID) || !searchHas(search("stone.example", ScopePeople), contact.ID) {
		t.Fatal("added phone or email not searched")
	}
	contact.FullName = "Rita Granite"
	if err := db.UpdateContact(ctx, contact); err != nil {
		t.Fatal(err)
	}
	if !searchHas(search("granite", ScopePeople), contact.ID) {
		t.Fatal("contact rename not searched")
	}

	if searchHas(search("INV-LATE", ScopeInvoices), tid(50)) {
		t.Fatal("invoice found before it exists")
	}
	if err := db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(50)}, MatterID: matter.ID, Number: "INV-LATE", Status: "sent", IssuedOn: "2026-06-02"}); err != nil {
		t.Fatal(err)
	}
	if !searchHas(search("inv-late", ScopeInvoices), tid(50)) {
		t.Fatal("new invoice not searched")
	}

	// A wall set after the index was built still hides every hit on the walled matter.
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, matter.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		q     string
		id    domain.ID
		scope string
	}{{"renamed", matter.ID, ScopeMatters}, {"inv-late", tid(50), ScopeInvoices}, {"granite", contact.ID, ScopePeople}, {"renamed", matter.ID, ScopeAll}} {
		if searchHas(search(q.q, q.scope), q.id) {
			t.Fatalf("staff found %s on a walled matter", q.q)
		}
		r, err := svc.Search(ctx, attorney, q.q, q.scope)
		if err != nil || !searchHas(r, q.id) {
			t.Fatalf("wall member missed %s: %v", q.q, err)
		}
	}
	if !searchHas(search("second probate", ScopeMatters), second.ID) {
		t.Fatal("unwalled matter hidden")
	}
}

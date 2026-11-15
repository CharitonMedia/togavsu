package practice

import (
	"context"
	"errors"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestCountMattersBatchMatchesEachMatter: CountMatters reads walls and client parties
// once per call and shares the store's matter key list (AC-017 performance). For every
// role, a wall member, a walled-out user and a client, the count must equal a per-matter
// visibleMatter count, and a wall, party or matter write made after the key list and a
// count exist must change the very next count (AC-003 walls).
func TestCountMattersBatchMatchesEachMatter(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	client := person(tid(4), domain.RoleClient)
	// A staff user who is also a client party: the batch reads their contact's parties.
	staffClient := person(tid(5), domain.RoleStaff, domain.RoleClient)
	putUser(t, db, attorney, "Ada Attorney")
	putUser(t, db, staff, "Sam Staff")
	area := makeArea(t, svc, admin, "Probate")
	other := makeArea(t, svc, admin, "Family")
	open1 := makeMatter(t, svc, staff, area, "Open one", staff.UserID)
	open2 := makeMatter(t, svc, staff, area, "Open two", staff.UserID)
	family := makeMatter(t, svc, staff, other, "Family open", staff.UserID)
	closed := makeMatter(t, svc, staff, area, "Closed one", staff.UserID)
	closed.Status = "closed"
	if err := db.UpdateMatter(ctx, closed); err != nil {
		t.Fatal(err)
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Cleo", FamilyName: "Client"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []domain.Principal{client, staffClient} {
		roles, err := domain.MarshalRoles(u.Roles)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.PutUser(ctx, domain.User{Meta: domain.Meta{ID: u.UserID}, Name: "Cleo Client", Roles: roles, Active: true, ContactID: contact.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.AddParty(ctx, staff, open2.ID, PartyInput{ContactID: contact.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetWall(ctx, admin, open1.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}

	// each is CountMatters before batching: the list gate, then visibleMatter per matter.
	each := func(p domain.Principal, status string, area domain.ID) int {
		if _, err := svc.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
			return -1
		}
		n := 0
		for _, k := range svc.db.Matters() { // whole matters, not the shared key list
			if (status == "" || k.Status == status) && (area.IsZero() || k.PracticeAreaID == area) && svc.visibleMatter(ctx, p, k.ID) {
				n++
			}
		}
		return n
	}
	count := func(p domain.Principal, status string, area domain.ID) int {
		t.Helper()
		n, err := svc.CountMatters(ctx, p, status, area)
		if err != nil {
			return -1 // refused by the list gate
		}
		return n
	}
	check := func(when string, want map[string]int) {
		t.Helper()
		for name, p := range map[string]domain.Principal{"admin": admin, "attorney": attorney, "staff": staff, "client": client, "staff+client": staffClient} {
			for _, f := range []struct {
				status string
				area   domain.ID
			}{{"", ""}, {StatusOpen, ""}, {"closed", ""}, {StatusOpen, area}, {"", other}} {
				got, per := count(p, f.status, f.area), each(p, f.status, f.area)
				if got != per {
					t.Fatalf("%s: %s %q %s: CountMatters %d, per-matter %d", when, name, f.status, f.area, got, per)
				}
			}
			if w, ok := want[name]; ok {
				if got := count(p, StatusOpen, ""); got != w {
					t.Fatalf("%s: %s open count %d, want %d", when, name, got, w)
				}
			}
		}
	}
	// open1 is walled to the attorney: staff and the admin see open2 and family; the
	// attorney sees all three open matters.
	check("walled", map[string]int{"staff": 2, "attorney": 3, "staff+client": 2})

	// The wall now names staff instead: the next count follows it.
	if err := svc.SetWall(ctx, admin, open1.ID, []domain.ID{staff.UserID}); err != nil {
		t.Fatal(err)
	}
	check("wall moved", map[string]int{"staff": 3, "attorney": 2})

	// A reopened matter, then a new one, each after the shared key list was built. The
	// new matter has no staff assignment, so only InsertMatter writes it.
	closed.Status = StatusOpen
	if err := db.UpdateMatter(ctx, closed); err != nil {
		t.Fatal(err)
	}
	check("reopened", map[string]int{"staff": 4, "attorney": 3})
	makeMatter(t, svc, staff, area, "Open three", "")
	check("new", map[string]int{"staff": 5, "attorney": 4})

	// A wall over the client's own matter hides it from the client, too.
	if err := svc.SetWall(ctx, admin, open2.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	check("client walled", map[string]int{"staff": 4, "attorney": 4, "staff+client": 3})
	_ = family
}

// TestBatchWallsRefusesAnotherPrincipal: the batch holds one user's wall rows, so a view
// asked for anyone else fails closed instead of answering with those rows.
func TestBatchWallsRefusesAnotherPrincipal(t *testing.T) {
	b := &batchWalls{user: tid(1), walled: map[domain.ID]bool{tid(9): true}, allows: map[domain.ID]bool{tid(9): true}, client: map[domain.ID]bool{}}
	if _, err := b.View(context.Background(), person(tid(2), domain.RoleStaff), tid(9)); !errors.Is(err, errBatchPrincipal) {
		t.Fatalf("view for another user: %v", err)
	}
	svc, _ := newSvc(t)
	if svc.visibleIn(context.Background(), b, person(tid(2), domain.RoleAdmin), tid(9)) {
		t.Fatal("visibleIn allowed another user's batch")
	}
}

// TestCountMattersUsesInjectedWallReader: a Policy whose walls come from elsewhere is
// never bypassed by the batch path.
func TestCountMattersUsesInjectedWallReader(t *testing.T) {
	svc, _ := newSvc(t)
	admin := person(tid(1), domain.RoleAdmin)
	area := makeArea(t, svc, admin, "Probate")
	makeMatter(t, svc, admin, area, "Open one", "")
	svc.Policy = domain.Policy{Walls: denyAll{}}
	n, err := svc.CountMatters(context.Background(), admin, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("count %d through a wall reader that denies everything", n)
	}
}

type denyAll struct{}

func (denyAll) View(context.Context, domain.Principal, domain.ID) (domain.WallView, error) {
	return domain.WallView{Walled: true}, nil
}

// TestMatterKeysFollowReload: a reload (Durable.load adopting a fresh Memory) replaces
// every matter, so a key list built before it must not be served after it.
func TestMatterKeysFollowReload(t *testing.T) {
	ctx := context.Background()
	m, fresh := NewMemory(), NewMemory()
	if _, err := m.InsertMatter(ctx, domain.Matter{Meta: domain.Meta{ID: tid(1)}, Number: "M-1", Status: StatusOpen}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.InsertMatter(ctx, domain.Matter{Meta: domain.Meta{ID: tid(2)}, Number: "M-2", Status: "closed"}, nil); err != nil {
		t.Fatal(err)
	}
	if k := m.MatterKeys(); len(k) != 1 || k[0].ID != tid(1) {
		t.Fatalf("keys before reload: %+v", k)
	}
	m.adopt(fresh)
	if k := m.MatterKeys(); len(k) != 1 || k[0].ID != tid(2) || k[0].Status != "closed" {
		t.Fatalf("keys after reload: %+v", k)
	}
}

// TestMatterPicksByNumberFollowWrites: the shared sorted pick list is ordered by number,
// then id, and an insert, a renumbering update or a reload shows in the next read.
func TestMatterPicksByNumberFollowWrites(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	put := func(id domain.ID, number string) domain.Matter {
		t.Helper()
		row, err := m.InsertMatter(ctx, domain.Matter{Meta: domain.Meta{ID: id}, Number: number, Name: "N " + number, Status: StatusOpen}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	numbers := func() []string {
		var out []string
		for _, p := range m.MatterPicksByNumber() {
			out = append(out, p.Number+"/"+p.Name)
		}
		return out
	}
	same := func(when string, got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: %v, want %v", when, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: %v, want %v", when, got, want)
			}
		}
	}
	put(tid(3), "M-3")
	b := put(tid(1), "M-2")
	same("first read", numbers(), "M-2/N M-2", "M-3/N M-3")
	put(tid(2), "M-1")
	same("after insert", numbers(), "M-1/N M-1", "M-2/N M-2", "M-3/N M-3")
	b.Number, b.Name = "M-9", "Renamed"
	if err := m.UpdateMatter(ctx, b); err != nil {
		t.Fatal(err)
	}
	same("after update", numbers(), "M-1/N M-1", "M-3/N M-3", "M-9/Renamed")
	fresh := NewMemory()
	if _, err := fresh.InsertMatter(ctx, domain.Matter{Meta: domain.Meta{ID: tid(7)}, Number: "R-1", Name: "Reloaded"}, nil); err != nil {
		t.Fatal(err)
	}
	m.adopt(fresh)
	same("after reload", numbers(), "R-1/Reloaded")
}

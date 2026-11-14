package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestWritableMattersFromPicks: the time and expense pickers read the practice store's
// light pick list (AC-017) but still list only open matters the caller may write, in
// matter-number order, and keep a walled matter from anyone outside the wall.
func TestWritableMattersFromPicks(t *testing.T) {
	f := newFx(t)
	h := &Handler{Svc: f.svc}
	a, _ := f.matter("Alpha", "hourly", 0, 0)
	b, _ := f.matter("Bravo", "hourly", 0, 0)
	walled, _ := f.matter("Walled", "hourly", 0, 0)
	closed, _ := f.matter("Closed", "hourly", 0, 0)
	closed.Status = matterClosed
	if err := f.pm.UpdateMatter(f.ctx, closed); err != nil {
		t.Fatal(err)
	}
	if err := f.ps.SetWall(f.ctx, f.admin, walled.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	ids := func(p domain.Principal, res domain.Resource) []domain.ID {
		var out []domain.ID
		for _, m := range h.writableMatters(f.ctx, p, res) {
			if m.Name == "" || m.Number == "" {
				t.Fatalf("pick without number or name: %+v", m)
			}
			out = append(out, m.ID)
		}
		return out
	}
	same := func(got, want []domain.ID) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	for _, res := range []domain.Resource{domain.ResTime, domain.ResExpense} {
		if got := ids(f.staff, res); !same(got, []domain.ID{a.ID, b.ID}) {
			t.Fatalf("staff %s picks %v, want %v (walled and closed omitted)", res, got, []domain.ID{a.ID, b.ID})
		}
		if got := ids(f.attorney, res); !same(got, []domain.ID{a.ID, b.ID, walled.ID}) {
			t.Fatalf("wall member %s picks %v", res, got)
		}
	}
	// The edit form keeps the record's own closed matter.
	if got := h.editMatters(f.ctx, f.staff, domain.ResTime, closed.ID); len(got) != 3 || got[0].ID != closed.ID {
		t.Fatalf("edit picks %v", got)
	}
}

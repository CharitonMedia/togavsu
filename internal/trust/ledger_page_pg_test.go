package trust

import (
	"testing"
)

// TestPGLedgerPageMatchesLedger: the ledger screen's newest-first batches return, page by
// page, exactly the visible ledger in reverse posting order; a walled matter's entries
// are on no page for a user outside the wall, and an offset past the end starts over.
func TestPGLedgerPageMatchesLedger(t *testing.T) {
	e := newEnv(t)
	m2 := e.newMatter("2026-0002", "Walled Synthetic", "hourly", 0, e.clientID)
	e.wall(m2.ID, e.other.UserID, e.admin.UserID)
	for i := 0; i < 7; i++ {
		in := e.in(TypeDeposit, int64(1000+i))
		if i%3 == 1 {
			in.MatterID = m2.ID
			e.post(e.other, in)
			continue
		}
		e.post(e.attorney, in)
	}
	for _, p := range []struct {
		name  string
		walls bool
	}{{"staff", false}, {"other", true}} {
		who := e.staff
		if p.walls {
			who = e.other
		}
		all, err := e.svc.visibleEntries(e.ctx, who, EntryFilter{})
		if err != nil {
			t.Fatal(err)
		}
		want := newestFirst(all)
		var got []Entry
		for from := 0; ; from += 2 {
			page, more, err := e.svc.ledgerPage(e.ctx, who, EntryFilter{}, from, 2)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, page...)
			if !more {
				break
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: %d entries over the pages, ledger has %d", p.name, len(got), len(want))
		}
		for i := range want {
			if got[i].ID != want[i].ID {
				t.Fatalf("%s: entry %d is %s, want %s", p.name, i, got[i].ID, want[i].ID)
			}
			if !p.walls && got[i].MatterID == m2.ID {
				t.Fatalf("%s sees a walled entry", p.name)
			}
		}
		past, _, err := e.svc.ledgerPage(e.ctx, who, EntryFilter{}, len(want)+5, 2)
		if err != nil || len(past) != min(2, len(want)) || past[0].ID != want[0].ID {
			t.Fatalf("%s: an offset past the end gave %d entries, %v", p.name, len(past), err)
		}
	}
}

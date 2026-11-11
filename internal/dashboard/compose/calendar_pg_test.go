package compose

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestUICalendarIsCoreCalendar is the composed site's half of AC-011/AC-027: the ui
// mounts no calendar of its own. A deadline made through the composed handler is the
// core's Postgres row, the home screen widget reads it, and it survives a core restart;
// a walled deadline stays hidden from staff before and after.
func TestUICalendarIsCoreCalendar(t *testing.T) {
	e := newEnv(t)
	if e.ui.Calendar != e.app.Calendar {
		t.Fatal("compose mounted its own calendar service instead of the core's")
	}
	open, _, _ := e.matter("Estate of Okafor", nil)
	walled, _, _ := e.matter("Walled Estate of Varga", nil)
	e.wall(walled, e.atty.id, e.admin.id)
	due := time.Now().AddDate(0, 0, 4).Format("2006-01-02")
	d1 := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": open, "title": "Okafor inventory due", "due_on": due, "responsible_user_id": e.staff.id}).json, "id")
	d2 := str(t, e.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": walled, "title": "Varga accounting due", "due_on": due, "responsible_user_id": e.atty.id}).json, "id")

	var n int
	if err := e.app.Pool.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM deadlines WHERE id IN ($1, $2)`, d1, d2).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("deadlines made through the composed handler: %d rows in the core's deadlines table, want 2", n)
	}

	from, to := time.Now().Format("2006-01-02"), time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	check := func(when string) {
		t.Helper()
		w := e.staff.must(http.StatusOK, "GET", "/api/v1/widgets/upcoming-deadlines?from_date="+from+"&to_date="+to, nil)
		if !strings.Contains(w.body, d1) || strings.Contains(w.body, d2) {
			t.Fatalf("%s: staff widget: %s", when, w.body)
		}
		if g := e.staff.must(http.StatusOK, "GET", "/api/v1/deadlines/"+d1, nil); str(t, g.json, "title") != "Okafor inventory due" {
			t.Fatalf("%s: deadline: %s", when, g.body)
		}
		if r := e.staff.do("GET", "/api/v1/deadlines/"+d2, nil); r.code != http.StatusNotFound {
			t.Fatalf("%s: staff read the walled deadline: %d", when, r.code)
		}
		if r := e.staff.do("GET", "/calendar/deadlines/"+d2, nil); r.code != http.StatusNotFound {
			t.Fatalf("%s: staff opened the walled deadline page: %d", when, r.code)
		}
		if p := e.staff.must(http.StatusOK, "GET", "/calendar/deadlines/"+d1, nil); !strings.Contains(p.body, "Okafor inventory due") {
			t.Fatalf("%s: deadline page lacks the deadline", when)
		}
		if w := e.atty.must(http.StatusOK, "GET", "/api/v1/widgets/upcoming-deadlines?from_date="+from+"&to_date="+to, nil); !strings.Contains(w.body, d2) {
			t.Fatalf("%s: attorney inside the wall lacks the deadline: %s", when, w.body)
		}
	}
	check("before restart")
	e.restart(e.admin, e.atty, e.staff, e.acct)
	check("after restart")
}

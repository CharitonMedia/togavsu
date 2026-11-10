package calendar

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// txAudit is an audit store with a transaction like Postgres: rows appended inside
// Atomic are kept only when fn returns nil. failOn makes the nth Append from now fail.
type txAudit struct {
	*audit.Memory
	mu        sync.Mutex
	failAt    int
	committed int
}

type txRows struct{ rows []domain.AuditEntry }

type txKey struct{}

func (a *txAudit) failOn(n int) { a.mu.Lock(); a.failAt = n; a.mu.Unlock() }

func (a *txAudit) rows() int { a.mu.Lock(); defer a.mu.Unlock(); return a.committed }

func (a *txAudit) Append(ctx context.Context, row domain.AuditEntry) error {
	a.mu.Lock()
	if a.failAt > 0 {
		a.failAt--
		if a.failAt == 0 {
			a.mu.Unlock()
			return errors.New("audit store down")
		}
	}
	a.mu.Unlock()
	if tx, ok := ctx.Value(txKey{}).(*txRows); ok {
		tx.rows = append(tx.rows, row)
		return nil
	}
	return a.commit(ctx, []domain.AuditEntry{row})
}

func (a *txAudit) commit(ctx context.Context, rows []domain.AuditEntry) error {
	for _, r := range rows {
		if err := a.Memory.Append(ctx, r); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.committed += len(rows)
	a.mu.Unlock()
	return nil
}

func (a *txAudit) Atomic(ctx context.Context, fn func(ctx context.Context) error) error {
	tx := &txRows{}
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return a.commit(ctx, tx.rows)
}

// TestCalendarWritesRollBackWhenAuditFails: when an audit row fails after the store write,
// every calendar mutation returns the error and leaves the store, the audit log and the
// activity stream as they were, so a retry creates one event, not two.
func TestCalendarWritesRollBackWhenAuditFails(t *testing.T) {
	f := newFixture(t)
	ta := &txAudit{Memory: audit.NewMemory()}
	aud, err := audit.New(ta, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	d := f.svc.d
	d.Audit = aud
	svc, err := New(d)
	if err != nil {
		t.Fatal(err)
	}
	m := f.matter("Rollback v. Audit")
	sam := f.staff.UserID
	in := EventInput{Title: "Hearing", Kind: KindCourtDate, MatterID: m.ID, StartsAt: at(t, "2026-11-20 09:00"), EndsAt: at(t, "2026-11-20 10:00"), Attendees: []domain.ID{sam}}

	unchanged := func(step string, rows, stream int) {
		t.Helper()
		if got := ta.rows(); got != rows {
			t.Fatalf("%s: %d audit rows committed, want %d", step, got, rows)
		}
		if got := len(f.store.Stream()); got != stream {
			t.Fatalf("%s: %d activity rows, want %d", step, got, stream)
		}
	}
	rows, stream := ta.rows(), len(f.store.Stream())

	ta.failOn(2) // the second audit row of the create fails
	if _, err := svc.CreateEvent(f.ctx, f.staff, in); err == nil {
		t.Fatal("create event succeeded with a failing audit row")
	}
	if evs, _ := f.store.Events(f.ctx); len(evs) != 0 {
		t.Fatalf("failed create left %d events", len(evs))
	}
	unchanged("create event", rows, stream)

	e, err := svc.CreateEvent(f.ctx, f.staff, in)
	if err != nil {
		t.Fatal(err)
	}
	if evs, _ := f.store.Events(f.ctx); len(evs) != 1 {
		t.Fatalf("retry left %d events, want 1", len(evs))
	}
	rows, stream = ta.rows(), len(f.store.Stream())
	if rows == 0 || stream == 0 {
		t.Fatalf("created event wrote %d audit and %d activity rows", rows, stream)
	}

	ta.failOn(1)
	title := "Moved hearing"
	if _, err := svc.UpdateEvent(f.ctx, f.staff, e.ID, EventPatch{Title: &title}); err == nil {
		t.Fatal("update event succeeded with a failing audit row")
	}
	if got, _ := f.store.Event(f.ctx, e.ID); got.Title != "Hearing" {
		t.Fatalf("failed update left title %q", got.Title)
	}
	unchanged("update event", rows, stream)

	ta.failOn(1)
	if err := svc.DeleteEvent(f.ctx, f.staff, e.ID); err == nil {
		t.Fatal("delete event succeeded with a failing audit row")
	}
	if got, err := f.store.Event(f.ctx, e.ID); err != nil || got.Title != "Hearing" || len(got.Attendees) != 1 {
		t.Fatalf("failed delete did not restore the event: %+v, %v", got, err)
	}
	unchanged("delete event", rows, stream)

	dl := DeadlineInput{MatterID: m.ID, Title: "Answer", DueOn: "2026-11-24", ResponsibleUserID: sam}
	ta.failOn(1)
	if _, err := svc.CreateDeadline(f.ctx, f.staff, dl); err == nil {
		t.Fatal("create deadline succeeded with a failing audit row")
	}
	if got, _ := f.store.Deadlines(f.ctx); len(got) != 0 {
		t.Fatalf("failed create left %d deadlines", len(got))
	}
	unchanged("create deadline", rows, stream)
	made, err := svc.CreateDeadline(f.ctx, f.staff, dl)
	if err != nil {
		t.Fatal(err)
	}
	rows, stream = ta.rows(), len(f.store.Stream())
	ta.failOn(1)
	renamed := "Amended answer"
	if _, err := svc.UpdateDeadline(f.ctx, f.staff, made.ID, DeadlinePatch{Title: &renamed}); err == nil {
		t.Fatal("update deadline succeeded with a failing audit row")
	}
	if got, _ := f.store.Deadline(f.ctx, made.ID); got.Title != "Answer" {
		t.Fatalf("failed update left deadline title %q", got.Title)
	}
	unchanged("update deadline", rows, stream)

	ta.failOn(1)
	if _, _, err := svc.CreateFeed(f.ctx, f.staff, sam); err == nil {
		t.Fatal("create feed succeeded with a failing audit row")
	}
	if got, _ := f.store.Feeds(f.ctx); len(got) != 0 {
		t.Fatalf("failed create left %d feeds", len(got))
	}
	unchanged("create feed", rows, stream)
	feed, _, err := svc.CreateFeed(f.ctx, f.staff, sam)
	if err != nil {
		t.Fatal(err)
	}
	rows, stream = ta.rows(), len(f.store.Stream())
	ta.failOn(1)
	if _, err := svc.RevokeFeed(f.ctx, f.staff, feed.ID); err == nil {
		t.Fatal("revoke feed succeeded with a failing audit row")
	}
	if got, _ := f.store.Feed(f.ctx, feed.ID); got.Revoked {
		t.Fatal("failed revoke left the feed revoked")
	}
	unchanged("revoke feed", rows, stream)
}

func TestMonthStepNeverSkipsAMonth(t *testing.T) {
	for _, c := range []struct {
		y         int
		mo        time.Month
		dd, delta int
		want      string
	}{
		{2026, time.January, 31, 1, "2026-02-28"},
		{2026, time.March, 31, -1, "2026-02-28"},
		{2028, time.January, 31, 1, "2028-02-29"},
		{2026, time.December, 31, 1, "2027-01-31"},
		{2026, time.January, 15, -1, "2025-12-15"},
	} {
		if got := monthStep(c.y, c.mo, c.dd, c.delta).Format(dateLayout); got != c.want {
			t.Errorf("monthStep(%d-%02d-%02d, %+d) = %s, want %s", c.y, c.mo, c.dd, c.delta, got, c.want)
		}
	}
}

func TestMonthPageLinksFromMonthEnd(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	status, body := get(t, srv.URL, f.staff, "/calendar?view=month&date=2026-01-31")
	if status != http.StatusOK {
		t.Fatalf("month page: %d", status)
	}
	if !strings.Contains(body, "date=2026-02-28") || !strings.Contains(body, "date=2025-12-31") {
		t.Fatal("month page from January 31 does not link to February 28 and December 31")
	}
	status, body = get(t, srv.URL, f.staff, "/calendar?view=month&date=2026-03-31")
	if status != http.StatusOK {
		t.Fatalf("month page: %d", status)
	}
	if !strings.Contains(body, "date=2026-02-28") || !strings.Contains(body, "date=2026-04-30") {
		t.Fatal("month page from March 31 does not link to February 28 and April 30")
	}
}

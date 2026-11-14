package billing

import (
	"context"
	"fmt"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// findRows writes time entries over two matters and two users, a third of them billed.
func findRows(t *testing.T, st Store) []TimeEntry {
	t.Helper()
	ctx := context.Background()
	var rows []TimeEntry
	for i := 0; i < 30; i++ {
		var e TimeEntry
		e.ID = domain.ID(fmt.Sprintf("0193f000-0000-7000-8000-%012d", i))
		e.MatterID = domain.ID([]string{"m-a", "m-b"}[i%2])
		e.UserID = domain.ID([]string{"u-1", "u-2", "u-3"}[i%3])
		e.WorkedOn = fmt.Sprintf("2026-01-%02d", 1+i%10)
		e.Billable = true
		if i%3 == 0 {
			e.InvoiceID = "inv-1"
		}
		if err := st.Put(ctx, kindTime, string(e.ID), e); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, e)
	}
	// Another kind with the same matter must not leak into a time entry query.
	if err := st.Put(ctx, kindExpense, "x-1", Expense{Expense: domain.Expense{MatterID: "m-a"}}); err != nil {
		t.Fatal(err)
	}
	return rows
}

func checkFind(t *testing.T, st Store, rows []TimeEntry) {
	t.Helper()
	ctx := context.Background()
	want := func(w Where) []domain.ID {
		var out []domain.ID
		for _, r := range rows {
			if w.match(indexOf(r)) {
				out = append(out, r.ID)
			}
		}
		return out
	}
	for name, w := range map[string]Where{
		"all": {}, "matter": {MatterID: "m-a"}, "user and dates": {UserID: "u-2", FromDate: "2026-01-03", ToDate: "2026-01-07"},
		"unbilled": {Unbilled: true}, "invoice": {InvoiceID: "inv-1"}, "nothing": {MatterID: "m-none"},
	} {
		got, _, err := findOf[TimeEntry](ctx, st, kindTime, w)
		if err != nil {
			t.Fatal(err)
		}
		exp := want(w)
		if len(got) != len(exp) {
			t.Fatalf("%s: got %d rows, want %d", name, len(got), len(exp))
		}
		for i := range got {
			if got[i].ID != exp[i] {
				t.Fatalf("%s: row %d is %s, want %s (id order)", name, i, got[i].ID, exp[i])
			}
		}
	}
	// Batches of 4 through the cursor return every row once, newest date first, ids
	// ascending within a date, the order the time screen shows.
	defer func(n int) { findBatch = n }(findBatch)
	findBatch = 4
	var seen []TimeEntry
	if err := scanFind(ctx, st, kindTime, Where{Newest: true}, 1000, func(r TimeEntry) (bool, error) {
		seen = append(seen, r)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(rows) {
		t.Fatalf("newest scan saw %d of %d rows", len(seen), len(rows))
	}
	for i := 1; i < len(seen); i++ {
		a, b := seen[i-1], seen[i]
		if a.WorkedOn < b.WorkedOn || a.WorkedOn == b.WorkedOn && a.ID >= b.ID {
			t.Fatalf("newest order broken at %d: %s %s then %s %s", i, a.WorkedOn, a.ID, b.WorkedOn, b.ID)
		}
	}
	// max stops the scan once enough rows are kept; rejected rows do not count.
	var kept int
	if err := scanFind(ctx, st, kindTime, Where{}, 5, func(r TimeEntry) (bool, error) {
		if r.UserID != "u-1" {
			return false, nil
		}
		kept++
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if kept != 5 {
		t.Fatalf("scan kept %d rows, want 5", kept)
	}
}

func TestFindMemory(t *testing.T) {
	st := NewMemory()
	checkFind(t, st, findRows(t, st))
	// A rolled-back Atomic restores the indexed columns with the rows.
	ctx := context.Background()
	_ = st.Atomic(ctx, func(ctx context.Context) error {
		var e TimeEntry
		if err := st.Get(ctx, kindTime, "0193f000-0000-7000-8000-000000000001", &e); err != nil {
			t.Fatal(err)
		}
		e.InvoiceID = "inv-2"
		if err := st.Put(ctx, kindTime, string(e.ID), e); err != nil {
			t.Fatal(err)
		}
		return fmt.Errorf("roll back")
	})
	got, _, err := st.Find(ctx, kindTime, Where{InvoiceID: "inv-2"})
	if err != nil || len(got) != 0 {
		t.Fatalf("rolled-back write still indexed: %d %v", len(got), err)
	}
}

func TestFindPostgres(t *testing.T) {
	pool := billingDB(t)
	st := NewPostgres(pool)
	rows := findRows(t, st)
	checkFind(t, st, rows)
	// Rows stored before migration 1200 have no columns; IndexRows fills them.
	ctx := context.Background()
	if _, err := pool.ExecContext(ctx, `UPDATE billing_records SET matter_id = '', user_id = '', on_date = '', invoice_id = '', indexed = 0`); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := st.Find(ctx, kindTime, Where{MatterID: "m-a"}); len(got) != 0 {
		t.Fatalf("unindexed rows matched a matter: %d", len(got))
	}
	if err := st.IndexRows(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_records WHERE indexed = 0 AND kind IN ('time_entry', 'expense')`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d rows left unindexed", left)
	}
	checkFind(t, st, rows)
}

// TestInvoiceHeadPostgres: invoice lists read the stored header without lines, the
// whole invoice still reads with them, and IndexRows fills headers stored before 1201.
func TestInvoiceHeadPostgres(t *testing.T) {
	pool := billingDB(t)
	st := NewPostgres(pool)
	ctx := context.Background()
	inv := Invoice{Lines: []Line{{Kind: LineTime, Description: "work", AmountCents: 100}}, DueOn: "2026-02-01"}
	inv.ID, inv.MatterID, inv.Number, inv.IssuedOn, inv.Status = "inv-h", "m-a", "S-1", "2026-01-01", StatusSent
	if err := st.Put(ctx, kindInvoice, string(inv.ID), inv); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		heads, err := invoiceHeads(ctx, st, Where{MatterID: "m-a"})
		if err != nil || len(heads) != 1 || heads[0].Number != "S-1" || heads[0].DueOn != "2026-02-01" || len(heads[0].Lines) != 0 {
			t.Fatalf("heads: %+v %v", heads, err)
		}
		full, _, err := findOf[Invoice](ctx, st, kindInvoice, Where{MatterID: "m-a"})
		if err != nil || len(full) != 1 || len(full[0].Lines) != 1 {
			t.Fatalf("full: %+v %v", full, err)
		}
	}
	check()
	if _, err := pool.ExecContext(ctx, `UPDATE billing_records SET head = ''`); err != nil {
		t.Fatal(err)
	}
	check() // falls back to the body
	if err := st.IndexRows(ctx); err != nil {
		t.Fatal(err)
	}
	var head string
	if err := pool.QueryRowContext(ctx, `SELECT head FROM billing_records WHERE id = 'inv-h'`).Scan(&head); err != nil || head == "" {
		t.Fatalf("head not filled: %q %v", head, err)
	}
	check()
}

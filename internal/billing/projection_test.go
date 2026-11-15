package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// projectionScenario writes the same billing history through the service on any store:
// billable and non-billable time, expenses with markup, a sent invoice that bills some of
// it, later unbilled work, a partial and a reversed payment, a write-off, a voided invoice
// and a walled matter. Descriptions carry characters JSON escapes.
func projectionScenario(f *fx) {
	t := f.t
	t.Helper()
	m1, c1 := f.matter("Alpha", "hourly", 0, 0)
	m2, c2 := f.matter("Beta", "hourly", 0, 0)
	m3, _ := f.matter("Gamma", "hourly", 0, 0)
	if err := f.ps.SetWall(f.ctx, f.admin, m3.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	rate := domain.Cents(210_00)
	f.logTime(f.attorney, m1.ID, "2026-05-01", 7, &rate)
	f.logTime(f.staff, m1.ID, "2026-05-02", 3, nil)
	f.expense(m1.ID, 12_50, 10)
	inv1 := f.sent(m1.ID) // bills the work so far on m1
	late := f.logTime(f.attorney, m1.ID, "2026-06-03", 4, &rate)
	desc := `Quote "q" <b>&</b> café`
	if _, err := f.svc.UpdateTime(f.ctx, f.attorney, late.ID, TimeInput{Description: &desc}); err != nil {
		t.Fatal(err)
	}
	off := f.logTime(f.staff, m2.ID, "2026-05-05", 2, nil)
	no := false
	if _, err := f.svc.UpdateTime(f.ctx, f.staff, off.ID, TimeInput{Billable: &no}); err != nil {
		t.Fatal(err)
	}
	f.logTime(f.staff, m2.ID, "2026-05-06", 5, nil)
	f.expense(m2.ID, 40_00, 0)
	inv2 := f.sent(m2.ID)
	f.logTime(f.attorney, m3.ID, "2026-05-07", 6, &rate)
	// Staff is walled out of m3, so its attorney bills it.
	drafts, err := f.svc.DraftInvoices(f.ctx, f.attorney, DraftRequest{MatterID: m3.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, drafts[0].ID); err != nil {
		t.Fatal(err)
	}
	inv3, err := f.svc.SendInvoice(f.ctx, f.attorney, drafts[0].ID, "file")
	if err != nil {
		t.Fatal(err)
	}
	f.logTime(f.attorney, m3.ID, "2026-06-01", 1, &rate)
	f.walled = m3.ID
	var pay Payment
	pay, err = f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: c1, MatterID: m1.ID, AmountCents: 100_00,
		Destination: DestOperating, Method: "check", ReferenceNumber: "2001", InvoiceID: inv1.ID})
	if err != nil {
		t.Fatal(err)
	}
	back, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: c2, MatterID: m2.ID, AmountCents: 50_00,
		Destination: DestOperating, Method: "check", ReferenceNumber: "2002", InvoiceID: inv2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReversePayment(f.ctx, f.attorney, back.ID, "bounced"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.WriteOff(f.ctx, f.attorney, inv2.ID, 10_00, "courtesy"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.VoidInvoice(f.ctx, f.attorney, inv3.ID, "wrong matter"); err != nil {
		t.Fatal(err)
	}
	_ = pay
}

// projectionReports is every report the projections feed, per viewer.
func projectionReports(f *fx) map[string]any {
	t := f.t
	t.Helper()
	out := map[string]any{"walled": f.walled}
	for name, p := range map[string]domain.Principal{"admin": f.admin, "attorney": f.attorney, "staff": f.staff, "accountant": f.accountant} {
		for _, rf := range []ReportFilter{{}, {UserID: f.staff.UserID}, {FromDate: "2026-05-02", ToDate: "2026-06-02"}} {
			rep, err := f.svc.UnbilledReport(f.ctx, p, rf)
			if err != nil {
				t.Fatal(err)
			}
			key := name + " unbilled " + rf.UserID.String() + rf.FromDate
			out[key] = rep.Rows
			// The streamed JSON is byte for byte the encoded report.
			var want bytes.Buffer
			if err := json.NewEncoder(&want).Encode(rep); err != nil {
				t.Fatal(err)
			}
			var gb bytes.Buffer
			if _, err := f.svc.UnbilledJSON(f.ctx, p, rf, func() io.Writer { return &gb }); err != nil {
				t.Fatal(err)
			}
			if gb.String() != want.String() {
				t.Fatalf("%s: UnbilledJSON\n%s\nwant\n%s", key, gb.String(), want.String())
			}
			sum, err := f.svc.UnbilledSummary(f.ctx, p, rf)
			if err != nil {
				t.Fatal(err)
			}
			var items int
			var cents domain.Cents
			for _, s := range sum {
				items, cents = items+s.Items, cents+s.Cents
			}
			var rows domain.Cents
			for _, r := range rep.Rows {
				rows += domain.Cents(r["amount_cents"].(int64))
			}
			if items != len(rep.Rows) || cents != rows {
				t.Fatalf("%s: summary %d items %d cents, report %d rows %d cents", key, items, cents, len(rep.Rows), rows)
			}
			// The screen's page is the report's first rows, with the report's row count.
			for _, max := range []int{1, 2, 0} {
				pg, err := f.svc.unbilledPage(f.ctx, p, rf, max)
				if err != nil {
					t.Fatal(err)
				}
				want := rep.Rows
				if max > 0 && len(want) > max {
					want = want[:max]
				}
				if !reflect.DeepEqual(pg.Rows, want) || len(rep.Rows) > len(pg.Rows) && pg.total != len(rep.Rows) {
					t.Fatalf("%s: page of %d: %d rows, total %d; report %d rows", key, max, len(pg.Rows), pg.total, len(rep.Rows))
				}
			}
		}
		for _, asOf := range []string{"", "2026-06-15", "2026-08-01", "2026-12-31"} {
			rep, err := f.svc.ARAgingReport(f.ctx, p, ReportFilter{AsOf: asOf})
			if err != nil {
				t.Fatal(err)
			}
			out[name+" aging "+asOf] = rep.Rows
			// The widget's summary is the report's bucket totals and first rows.
			buckets, top, err := f.svc.ARAgingSummary(f.ctx, p, ReportFilter{AsOf: asOf}, 2)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]domain.Cents{}
			for _, r := range rep.Rows {
				want[r["bucket"].(string)] += domain.Cents(r["balance_cents"].(int64))
			}
			if !reflect.DeepEqual(buckets, want) || len(top) != min(2, len(rep.Rows)) {
				t.Fatalf("%s aging %s: summary %v, %d rows; report %v", name, asOf, buckets, len(top), want)
			}
			for i, l := range top {
				if l.InvoiceID != rep.Rows[i]["invoice_id"] || int64(l.BalanceCents) != rep.Rows[i]["balance_cents"] {
					t.Fatalf("%s aging %s: summary row %d differs", name, asOf, i)
				}
			}
		}
	}
	return out
}

var uuidRE = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// normalized is each report as JSON with ids masked and rows sorted by content: the two
// stores' services mint different ids for the same history, and Postgres orders ids by
// the database collation where the memory store orders by bytes (as before these tables).
// Every row still carries its matter number, user, dates and amounts.
func normalized(reports map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range reports {
		rows, ok := v.([]map[string]any)
		if !ok {
			continue
		}
		lines := make([]string, len(rows))
		for i, r := range rows {
			b, _ := json.Marshal(r)
			lines[i] = uuidRE.ReplaceAllString(string(b), "#")
		}
		sort.Strings(lines)
		out[k] = strings.Join(lines, "\n")
	}
	return out
}

func checkReports(t *testing.T, label string, got, want map[string]any) {
	t.Helper()
	g, w := normalized(got), normalized(want)
	for k := range w {
		if g[k] != w[k] {
			t.Fatalf("%s: %s\ngot  %s\nwant %s", label, k, g[k], w[k])
		}
	}
	if len(g) != len(w) {
		t.Fatalf("%s: %d reports, want %d", label, len(g), len(w))
	}
}

// TestReportProjectionsPostgres: the unbilled and AR aging reports read from the narrow
// tables of migrations 1261 and 1262 return exactly what the memory store returns by
// decoding every document, for every role (walls included). The same holds after
// IndexRows rebuilds the tables, and after the migrations' own backfill statements
// rebuild them from stored documents.
func TestReportProjectionsPostgres(t *testing.T) {
	mem := newFx(t)
	projectionScenario(mem)
	want := projectionReports(mem)
	if rows := want["admin unbilled "].([]map[string]any); len(rows) == 0 {
		t.Fatal("scenario has no unbilled rows")
	}
	if rows := want["admin aging 2026-12-31"].([]map[string]any); len(rows) == 0 {
		t.Fatal("scenario has no aging rows")
	}

	f, pool := pgFx(t)
	projectionScenario(f)
	checkReports(t, "written through Put", projectionReports(f), want)

	// The walled matter's work reaches its attorney and no staff user.
	walled := func(who string) int {
		n := 0
		for _, r := range want[who+" unbilled "].([]map[string]any) {
			if r["matter_id"] == want["walled"] {
				n++
			}
		}
		return n
	}
	if walled("attorney") == 0 || walled("staff") != 0 {
		t.Fatalf("walled rows: attorney %d, staff %d", walled("attorney"), walled("staff"))
	}

	ctx := f.ctx
	for _, q := range []string{`DELETE FROM billing_unbilled`, `DELETE FROM billing_aging`, `UPDATE billing_records SET indexed = 0`} {
		if _, err := pool.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if got := normalized(projectionReports(f)); reflect.DeepEqual(got, normalized(want)) {
		t.Fatal("emptied projections still produced the reports")
	}
	if err := f.st.(*Postgres).IndexRows(ctx); err != nil {
		t.Fatal(err)
	}
	checkReports(t, "rebuilt by IndexRows", projectionReports(f), want)

	migs, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM billing_unbilled`, `DELETE FROM billing_aging`} {
		if _, err := pool.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ran := 0
	for _, m := range migs {
		if m.Version != 1261 && m.Version != 1262 {
			continue
		}
		up := m.SQL[:strings.Index(m.SQL, "-- +goose Down")]
		ins := up[strings.Index(up, "INSERT INTO"):]
		if _, err := pool.ExecContext(ctx, ins); err != nil {
			t.Fatalf("migration %d backfill: %v", m.Version, err)
		}
		ran++
	}
	if ran != 2 {
		t.Fatalf("ran %d backfills", ran)
	}
	checkReports(t, "rebuilt by the migration backfill", projectionReports(f), want)

	// Deleting a document drops its projected row (foreign key cascade).
	var id string
	if err := pool.QueryRowContext(ctx, `SELECT id FROM billing_unbilled WHERE kind = 'time_entry' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := f.st.Delete(ctx, kindTime, id); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_unbilled WHERE id = $1`, id).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleted entry still projected: %d %v", left, err)
	}
	// The kept snapshot follows every committed change: the cascade above, and a new entry
	// written after a read.
	has := func(id string) bool {
		rep, err := f.svc.UnbilledReport(ctx, f.attorney, ReportFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rep.Rows {
			if string(r["id"].(domain.ID)) == id {
				return true
			}
		}
		return false
	}
	if has(id) {
		t.Fatal("report still lists the deleted entry")
	}
	rate := domain.Cents(150_00)
	added := f.logTime(f.attorney, f.walled, "2026-06-10", 2, &rate)
	if !has(string(added.ID)) {
		t.Fatal("report misses an entry written after the last read")
	}
	var token1, token2 string
	_ = pool.QueryRowContext(ctx, `SELECT token FROM billing_projection_versions WHERE name = 'billing_unbilled'`).Scan(&token1)
	if _, err := f.svc.UpdateTime(ctx, f.attorney, added.ID, TimeInput{Billable: new(bool)}); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRowContext(ctx, `SELECT token FROM billing_projection_versions WHERE name = 'billing_unbilled'`).Scan(&token2)
	if token1 == token2 || has(string(added.ID)) {
		t.Fatalf("a non-billable edit kept the entry: tokens %q %q", token1, token2)
	}
	// A rolled-back write leaves the snapshot alone.
	if err := f.st.Atomic(ctx, func(ctx context.Context) error {
		e := added
		e.Billable = true
		if err := f.st.Put(ctx, kindTime, string(e.ID), e); err != nil {
			return err
		}
		return errors.New("roll back")
	}); err == nil {
		t.Fatal("want the rollback error")
	}
	if has(string(added.ID)) {
		t.Fatal("a rolled-back write reached the report")
	}
}

// TestUnbilledBatchesPostgres: the keyset batches of Postgres.Unbilled return every
// unbilled row once, in id order, across batch boundaries, both through pgx outside a
// transaction and through database/sql inside one.
func TestUnbilledBatchesPostgres(t *testing.T) {
	pool := billingDB(t)
	st := NewPostgres(pool)
	ctx := context.Background()
	n, want := unbilledBatch*2+7, 0
	for i := 0; i < n; i++ {
		e := TimeEntry{IncrementMinutes: 6}
		e.ID, e.MatterID, e.UserID = domain.ID(fmt.Sprintf("t%06d", i)), "m-a", "u-a"
		e.WorkedOn, e.Increments, e.Billable, e.RateCents, e.Description = "2026-01-02", 1+i%5, i%3 != 0, 100_00, "w"
		if e.Billable {
			want++
		}
		if err := st.Put(ctx, kindTime, string(e.ID), e); err != nil {
			t.Fatal(err)
		}
	}
	read := func(ctx context.Context) []string {
		var ids []string
		if err := st.Unbilled(ctx, kindTime, Where{}, func(r *unbilledRow) error {
			ids = append(ids, string(r.ID))
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	var inTx []string
	if err := st.Atomic(ctx, func(ctx context.Context) error { inTx = read(ctx); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{read(ctx), inTx} {
		if len(ids) != want {
			t.Fatalf("read %d rows, want %d", len(ids), want)
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("rows out of order at %d: %s then %s", i, ids[i-1], ids[i])
			}
		}
	}
}

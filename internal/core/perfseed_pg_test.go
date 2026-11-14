package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// AC-017 seed volume: 50 users, 20,000 matters, 1,000,000 time entries. Everything is
// synthetic. The seed builds real domain and billing values and marshals them the way
// the stores do, so the rows are what the services themselves would have written.
const (
	perfUsers        = 50
	perfMatters      = 20000
	perfTimePer      = 50 // 20,000 x 50 = 1,000,000 time entries
	perfWalled       = 400
	perfTrustMatters = 5000
	perfSeedVersion  = "ac017-v3"
)

// perfAccount is a signed-in harness user whose secrets live in perf.harness, so a
// reused seed database can sign in again.
type perfAccount struct {
	Email, Password, Secret string
	ID                      domain.ID
}

type perfSeed struct {
	Admin, Staff perfAccount
	// Probe ids: a matter the staff user may see, its client phone, an invoice number.
	Matter, Phone, Invoice, MatterNumber string
	StaffUser                            string
}

// seedID is a deterministic UUIDv7 in a namespace per record kind.
func seedID(ns uint16, n uint64) domain.ID {
	at := uint64(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()) + n/1000
	id, err := domain.ComposeV7(at, ns, n)
	if err != nil {
		panic(err)
	}
	return id
}

func copyRows(ctx context.Context, pool *sql.DB, table string, cols []string, rows [][]any) error {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		pc := dc.(*stdlib.Conn).Conn()
		_, err := pc.CopyFrom(ctx, pgx.Identifier{"core", table}, cols, pgx.CopyFromRows(rows))
		return err
	})
}

// seedPerf fills a fresh core database through the running app: first-run, users through
// the identity service, reference rows through the API, then bulk practice and billing
// documents through COPY, and trust, calendar and deadline rows set-based. The caller
// restarts the core afterwards so practice reloads its records and trust rebuilds balances.
func seedPerf(t *testing.T, app *App, base string) perfSeed {
	t.Helper()
	ctx := context.Background()
	admin := firstRun(t, base)
	var seed perfSeed
	// firstRun's admin has no stored secret; the harness enrolls its own admin and staff.
	u, _ := url.Parse(base)
	var session string
	for _, c := range admin.http.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	caller, err := app.Identity.Authenticate(ctx, identity.Presented{SessionToken: session})
	if err != nil {
		t.Fatal(err)
	}
	// Rate limits would answer 429 before the handler runs and hide latency.
	admin.must(http.StatusOK, "PATCH", "/api/v1/settings/security.api_rate_per_minute", map[string]any{"value": 1000000})
	area := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Litigation", "stages": []string{"Intake", "Discovery", "Trial"}}).json, "id")
	account := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/trust-accounts", map[string]any{
		"bank": "Synthetic Bank", "name": "IOLTA", "iolta": true, "last_four": "4242"}).json, "id")
	var activities []string
	for _, n := range []string{"Drafting", "Research", "Court", "Call", "Review"} {
		activities = append(activities, str(t, admin.must(http.StatusCreated, "POST", "/api/v1/activity-types", map[string]any{"name": n}).json, "id"))
	}
	expType := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Filing fee"}).json, "id")

	// 49 more users: 2 admins, 15 attorneys, 27 staff, 5 accountants; with the first-run
	// admin that is 50.
	var attorneys, workers []domain.ID
	adminID := domain.ID(str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id"))
	attorneys = append(attorneys, adminID)
	mk := func(i int, roles ...domain.Role) domain.ID {
		usr, err := app.Identity.CreateUser(ctx, caller, identity.NewUser{Name: fmt.Sprintf("Perf User %02d", i),
			Email: fmt.Sprintf("perf%02d@firm.example", i), Password: fmt.Sprintf("synthetic-perf-password-%02d", i), Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		return usr.ID
	}
	enroll := func(name, email string, roles ...domain.Role) perfAccount {
		acct := perfAccount{Email: email, Password: "synthetic-password-" + name}
		c, id := enrollUserSecret(t, app, base, admin, name, email, &acct.Secret, roles...)
		_ = c
		acct.ID = id
		return acct
	}
	seed.Admin = enroll("Pat PerfAdmin", "pat.admin@firm.example", domain.RoleAdmin, domain.RoleAttorney)
	seed.Staff = enroll("Sid PerfStaff", "sid.staff@firm.example", domain.RoleStaff)
	attorneys = append(attorneys, seed.Admin.ID)
	workers = append(workers, seed.Staff.ID)
	n := 1
	for ; n <= 1; n++ {
		mk(n, domain.RoleAdmin)
	}
	for ; n <= 16; n++ {
		attorneys = append(attorneys, mk(n, domain.RoleAttorney))
	}
	for ; n <= 42; n++ {
		workers = append(workers, mk(n, domain.RoleStaff))
	}
	for ; n <= 47; n++ {
		mk(n, domain.RoleAccountant)
	}
	var users int
	if err := app.Pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id <> $1`, string(domain.SystemUserID)).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != perfUsers {
		t.Fatalf("seed has %d users, want %d", users, perfUsers)
	}

	// Templates come from the API so the bulk documents carry exactly the shape the
	// practice service writes.
	tplMatter := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Template v. Seed", "practice_area_id": area,
		"billing_type": "hourly", "responsible_attorney_user_id": string(attorneys[0])}).json, "id")
	tplContact := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Template", "family_name": "Client"}).json, "id")
	admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": tplMatter, "contact_id": tplContact, "role": "client", "primary": true})
	var mt domain.Matter
	for _, m := range app.Store.Matters() {
		if string(m.ID) == tplMatter {
			mt = m
		}
	}
	var ct domain.Contact
	for _, c := range app.Store.Contacts() {
		if string(c.ID) == tplContact {
			ct = c
		}
	}
	pt := app.Store.Parties(domain.ID(tplMatter), "")[0]
	if mt.ID == "" || ct.ID == "" {
		t.Fatal("template matter or contact missing")
	}
	var seq int64
	if err := app.Pool.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM practice_records`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC)
	meta := func(id domain.ID, by domain.ID) domain.Meta {
		return domain.Meta{ID: id, CreatedAt: at, UpdatedAt: at, CreatedBy: by}
	}
	var practice [][]any
	add := func(kind string, id domain.ID, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		seq++
		practice = append(practice, []any{kind, string(id), seq, string(b)})
	}
	family := []string{"Garcia", "Nguyen", "Okafor", "Smith", "Kowalski", "Haddad", "Ivanova", "Moreau", "Tanaka", "Lindqvist", "Mensah", "Reyes"}
	given := []string{"Alex", "Blair", "Casey", "Drew", "Emery", "Finley", "Gray", "Harper", "Indy", "Jordan", "Kai", "Logan", "Morgan"}
	matters := make([]domain.ID, perfMatters)
	clients := make([]domain.ID, perfMatters)
	staffSees := func(i int) bool { return i%5 == 0 } // the staff user is assigned on every fifth matter
	for i := 0; i < perfMatters; i++ {
		mid, cid := seedID(1, uint64(i)), seedID(2, uint64(i))
		matters[i], clients[i] = mid, cid
		c := ct
		c.Meta = meta(cid, adminID)
		c.GivenName, c.FamilyName = given[i%len(given)], fmt.Sprintf("%s%d", family[i%len(family)], i)
		c.FullName = c.GivenName + " " + c.FamilyName
		c.PreferredName = c.GivenName
		add("contact", cid, c)
		add("phone", seedID(3, uint64(i)), domain.ContactPhone{Meta: meta(seedID(3, uint64(i)), adminID), ContactID: cid, Label: "mobile", Primary: true,
			PhoneE164: fmt.Sprintf("+1555%07d", i), EffectiveFrom: "2025-01-01"})
		add("email", seedID(4, uint64(i)), domain.ContactEmail{Meta: meta(seedID(4, uint64(i)), adminID), ContactID: cid, Label: "home", Primary: true,
			Email: fmt.Sprintf("client%05d@example.com", i), EffectiveFrom: "2025-01-01"})
		m := mt
		m.Meta = meta(mid, adminID)
		m.Number = fmt.Sprintf("2025-%05d", i+1)
		m.Name = fmt.Sprintf("%s v. %s Holdings", c.FamilyName, family[(i+3)%len(family)])
		m.OpenedOn = fmt.Sprintf("20%02d-%02d-%02d", 22+i%4, 1+i%12, 1+i%28)
		m.ResponsibleAttorneyID = attorneys[i%len(attorneys)]
		m.AssignedStaffID = workers[i%len(workers)]
		if staffSees(i) {
			m.AssignedStaffID = seed.Staff.ID
		}
		m.MatterRateCents = domain.Cents(20000 + 500*int64(i%20))
		m.CaseNumber = fmt.Sprintf("25AR-CV%05d", i)
		if i%10 >= 7 {
			m.Status, m.ClosedOn, m.CloseReason = "closed", "2026-06-30", "Resolved"
		}
		add("matter", mid, m)
		p := pt
		p.Meta = meta(seedID(5, uint64(i)), adminID)
		p.MatterID, p.ContactID = mid, cid
		add("party", p.ID, p)
		add("note", seedID(6, uint64(i)), domain.Note{Meta: meta(seedID(6, uint64(i)), adminID), MatterID: mid, ContactID: cid, AuthorID: m.ResponsibleAttorneyID,
			Title: "Intake call", Body: "Synthetic intake notes for " + m.Name, Status: "active", Pinned: i%7 == 0})
		// Activity spreads over the last 60 days, so the 30-day feed window holds about
		// 10,000 events.
		evAt := time.Now().UTC().Add(-time.Duration(i%60) * 24 * time.Hour)
		add("event", seedID(7, uint64(i)), domain.Event{Meta: domain.Meta{ID: seedID(7, uint64(i)), CreatedAt: evAt, UpdatedAt: evAt, CreatedBy: adminID}, ActorKind: "staff", ActorID: adminID,
			Action: "matter.created", RecordType: "matter", RecordID: mid, MatterID: mid, Summary: "Matter opened: " + m.Name})
		// Walls: every 50th matter is walled to its attorney and the first-run admin.
		// Some walled matters include the staff user, most do not.
		if i%(perfMatters/perfWalled) == 1 {
			who := []domain.ID{m.ResponsibleAttorneyID, adminID}
			if i%200 == 1 {
				who = append(who, seed.Staff.ID)
			}
			for k, uid := range who {
				wid := seedID(8, uint64(i*4+k))
				add("wall", wid, domain.MatterWall{Meta: meta(wid, adminID), MatterID: mid, UserID: uid})
			}
		}
	}
	start := time.Now()
	if err := copyRows(ctx, app.Pool, "practice_records", []string{"kind", "id", "seq", "body"}, practice); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed: %d practice records in %s", len(practice), time.Since(start).Round(time.Millisecond))
	practice = nil

	// Billing documents: time entries, invoices billing the older half of every other
	// matter's time, payments on a third of those invoices, two expenses per matter.
	timekeepers := append(append([]domain.ID{}, attorneys...), workers...)
	day0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var bill [][]any
	put := func(kind string, id domain.ID, v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		bill = append(bill, []any{kind, string(id), string(b)})
	}
	flush := func() {
		if err := copyRows(ctx, app.Pool, "billing_records", []string{"kind", "id", "body"}, bill); err != nil {
			t.Fatal(err)
		}
		bill = bill[:0]
	}
	start = time.Now()
	var invoices int
	for i := 0; i < perfMatters; i++ {
		mid := matters[i]
		// Every matter has one invoice billing its billable time up to a cutoff: paid
		// invoices (every third matter) bill through 2026-09-30, open ones lag up to 120
		// days so AR aging has every bucket. Later time is unbilled.
		billed := true
		lag := 0
		if i%3 != 0 {
			lag = i % 120
		}
		cutoff := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -lag)
		invID := seedID(20, uint64(i))
		var lines []billing.Line
		var subtotal domain.Cents
		rate := domain.Cents(20000 + 500*int64(i%20))
		for k := 0; k < perfTimePer; k++ {
			n := uint64(i*perfTimePer + k)
			day := day0.AddDate(0, 0, (k*20+i)%1035)
			te := billing.TimeEntry{IncrementMinutes: 6}
			te.Meta = meta(seedID(10, n), adminID)
			te.CreatedAt, te.UpdatedAt = day.Add(17*time.Hour), day.Add(17*time.Hour)
			te.MatterID = mid
			te.UserID = timekeepers[(i+k)%len(timekeepers)]
			if staffSees(i) && k%3 == 0 {
				te.UserID = seed.Staff.ID
			}
			te.WorkedOn = day.Format("2006-01-02")
			te.Increments = 1 + (k*7+i)%20
			te.Description = fmt.Sprintf("Synthetic work item %d on %s", k, mid)
			te.ActivityType = activities[k%len(activities)]
			te.Billable = k%10 != 9
			te.RateCents = rate
			te.Source = "manual"
			if billed && !day.After(cutoff) && te.Billable {
				te.InvoiceID = invID
				amt := domain.Cents(int64(rate) * int64(te.Increments) * 6 / 60)
				subtotal += amt
				lines = append(lines, billing.Line{Kind: billing.LineTime, SourceID: te.ID, Description: te.Description, AmountCents: amt})
			}
			put("time_entry", te.ID, te)
		}
		for k := 0; k < 2; k++ {
			ex := billing.Expense{Source: "manual"}
			ex.Meta = meta(seedID(11, uint64(i*2+k)), adminID)
			ex.MatterID, ex.IncurredOn, ex.Description = mid, day0.AddDate(0, 0, (i+k*200)%1000).Format("2006-01-02"), "Synthetic filing fee"
			ex.AmountCents, ex.ExpenseType, ex.Billable = domain.Cents(1500+int64(k)*2500), expType, true
			put("expense", ex.ID, ex)
		}
		if billed {
			invoices++
			inv := billing.Invoice{Lines: lines, MatterNum: fmt.Sprintf("2025-%05d", i+1), FromDate: "2024-01-01", ToDate: cutoff.Format("2006-01-02")}
			inv.Meta = meta(invID, adminID)
			inv.MatterID = mid
			inv.Number = fmt.Sprintf("S-%06d", i+1)
			issued := cutoff.AddDate(0, 0, 3)
			inv.IssuedOn, inv.DueOn = issued.Format("2006-01-02"), issued.AddDate(0, 0, 30).Format("2006-01-02")
			inv.SubtotalCents, inv.TotalCents = subtotal, subtotal
			sent := issued.Add(18 * time.Hour)
			inv.Status, inv.SentAt, inv.ApprovedBy, inv.ApprovedAt = billing.StatusSent, sent.UnixMicro(), adminID, &sent
			inv.BalanceDueCents = subtotal
			if i%3 == 0 {
				inv.Status, inv.AmountPaidCents, inv.BalanceDueCents, inv.PaidAt = billing.StatusPaid, subtotal, 0, sent.AddDate(0, 0, 20).UnixMicro()
				pay := billing.Payment{ReceivedOn: sent.AddDate(0, 0, 20).Format("2006-01-02")}
				pay.Meta = meta(seedID(21, uint64(i)), adminID)
				pay.ContactID, pay.MatterID, pay.AmountCents = clients[i], mid, subtotal
				pay.Destination, pay.Method, pay.ReferenceNumber = billing.DestOperating, "check", fmt.Sprintf("%06d", i)
				pay.Status, pay.InvoiceID, pay.Source = billing.PayCompleted, invID, "manual"
				put("payment", pay.ID, pay)
			}
			inv.MatterName = fmt.Sprintf("matter %d", i)
			put("invoice", invID, inv)
		}
		if len(bill) >= 50000 {
			flush()
		}
	}
	flush()
	t.Logf("seed: %d time entries, %d invoices in %s", perfMatters*perfTimePer, invoices, time.Since(start).Round(time.Millisecond))

	// The section 5 tables trust and calendar reference by foreign key are written by the
	// practice projection at the next start, so trust, calendar and deadline rows go in
	// through the restarted core in seedPerfTables.
	seed.Matter = string(matters[5])
	seed.Phone = fmt.Sprintf("+1555%07d", 5)
	seed.Invoice = "S-000011"
	seed.MatterNumber = "2025-00006"
	seed.StaffUser = string(seed.Staff.ID)
	_ = account
	return seed
}

// seedPerfTables writes the relational trust, calendar and deadline rows once the
// projection has put matters and contacts in place. Set-based INSERT ... SELECT.
func seedPerfTables(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 1, 2, 15, 0, 0, 0, time.UTC).UnixMicro()
	var account, admin string
	if err := pool.QueryRowContext(ctx, `SELECT id FROM trust_accounts ORDER BY id LIMIT 1`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRowContext(ctx, `SELECT id FROM users WHERE email = 'pat.admin@firm.example'`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	stmts := []string{
		// A deposit on every fourth matter and a partial withdrawal on half of those.
		`INSERT INTO trust_ledger (id, created_at, updated_at, created_by, trust_account_id, client_id, matter_id, entry_type, amount_cents, method, description, reverses_id)
		 SELECT 'perf-td-' || m.id, $1, $1, $2, $3, p.contact_id, m.id, 'deposit', 250000 + (m.n % 7) * 10000, 'check', 'Synthetic retainer', NULL
		 FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY id) AS n FROM matters) m JOIN practice_party_seed p ON p.matter_id = m.id WHERE m.n % 4 = 0`,
		`INSERT INTO trust_ledger (id, created_at, updated_at, created_by, trust_account_id, client_id, matter_id, entry_type, amount_cents, method, description, reverses_id)
		 SELECT 'perf-tw-' || matter_id, CAST($1 AS BIGINT) + 1000000, CAST($1 AS BIGINT) + 1000000, $2, $3, client_id, matter_id, 'transfer_to_operating', -50000, 'internal_transfer', 'Synthetic fee transfer', NULL
		 FROM trust_ledger WHERE id LIKE 'perf-td-%' AND amount_cents % 20000 = 10000`,
		`INSERT INTO calendar_events (id, created_at, updated_at, created_by, title, kind, starts_at, ends_at, all_day, matter_id, location, travel_minutes, notes)
		 SELECT 'perf-ce-' || m.id, $1, $1, $2, 'Hearing ' || m.number, 'court_date', $3 + (m.n % 730) * 86400000000 + 14 * 3600000000,
		 $3 + (m.n % 730) * 86400000000 + 15 * 3600000000, 0, m.id, 'Circuit Court', 0, ''
		 FROM (SELECT id, number, ROW_NUMBER() OVER (ORDER BY id) AS n FROM matters) m`,
		`INSERT INTO calendar_event_attendees (id, created_at, updated_at, created_by, event_id, user_id)
		 SELECT 'perf-ca-' || e.id, $1, $1, $2, e.id, m.responsible_attorney_id FROM calendar_events e JOIN matters m ON m.id = e.matter_id
		 WHERE e.id LIKE 'perf-ce-%' AND m.responsible_attorney_id IS NOT NULL`,
		`INSERT INTO deadlines (id, created_at, updated_at, created_by, matter_id, title, due_on, responsible_user_id, status, reminder_offsets, done_by, done_at)
		 SELECT 'perf-dl-' || m.id, $1, $1, $2, m.id, 'Answer due', TO_CHAR(DATE '2026-01-01' + (m.n % 500)::int, 'YYYY-MM-DD'), COALESCE(m.responsible_attorney_id, $2),
		 CASE WHEN m.n % 3 = 0 THEN 'done' ELSE 'open' END, '[7,1]', NULL, 0
		 FROM (SELECT id, responsible_attorney_id, ROW_NUMBER() OVER (ORDER BY id) AS n FROM matters) m`,
	}
	day0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
	tx, err := pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// The client party of each matter, from the practice documents the projection does not
	// copy. A temporary table keeps the trust insert set-based.
	if _, err := tx.ExecContext(ctx, `CREATE TEMPORARY TABLE practice_party_seed (matter_id TEXT, contact_id TEXT) ON COMMIT DROP`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT body FROM practice_records WHERE kind = 'party'`)
	if err != nil {
		t.Fatal(err)
	}
	var pairs [][2]string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		var p domain.MatterParty
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		if p.Role == "client" {
			pairs = append(pairs, [2]string{string(p.MatterID), string(p.ContactID)})
		}
	}
	rows.Close()
	for i := 0; i < len(pairs); i += 1000 {
		end := min(i+1000, len(pairs))
		var sb strings.Builder
		args := []any{}
		for k, p := range pairs[i:end] {
			if k > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, "($%d,$%d)", 2*k+1, 2*k+2)
			args = append(args, p[0], p[1])
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO practice_party_seed VALUES `+sb.String(), args...); err != nil {
			t.Fatal(err)
		}
	}
	for i, q := range stmts {
		args := []any{at, admin}
		switch {
		case strings.Contains(q, "INTO trust_ledger"):
			args = append(args, account)
		case strings.Contains(q, "INTO calendar_events"):
			args = append(args, day0)
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed statement %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("seed: trust, calendar and deadline rows in %s", time.Since(start).Round(time.Millisecond))
}

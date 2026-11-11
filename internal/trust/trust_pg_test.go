package trust

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGPostKeepsBalancesAndCache: deposits and withdrawals move the matter, client and
// account balances, the cache matches the ledger, and the practice sink sees the row.
func TestPGPostKeepsBalancesAndCache(t *testing.T) {
	e := newEnv(t)
	e.post(e.staff, e.in(TypeDeposit, 100000))
	r := e.post(e.attorney, e.in(TypeWithdrawal, 30000))
	if r.Balances != (Balances{MatterCents: 70000, ClientCents: 70000, AccountCents: 70000}) {
		t.Fatalf("balances %+v", r.Balances)
	}
	if len(r.Flags) != 0 {
		t.Fatalf("a non-negative result raised flags: %+v", r.Flags)
	}
	if r.Entry.AmountCents != 30000 || r.Entry.Signed != -30000 || r.Entry.EntryType != TypeWithdrawal {
		t.Fatalf("entry %+v", r.Entry)
	}
	adj := e.in(TypeAdjustment, -500)
	adj.AdjustmentDirection = Debit
	r = e.post(e.attorney, adj)
	if r.Balances.MatterCents != 69500 || r.Entry.AdjustmentDirection != Debit || r.Entry.AmountCents != -500 {
		t.Fatalf("adjustment %+v %+v", r.Entry, r.Balances)
	}
	if err := e.svc.VerifyBalances(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.sunk) != 3 || e.sunk[2].BalanceCents != 69500 {
		t.Fatalf("sink %+v", e.sunk)
	}
	// The cache can be corrupted by hand; VerifyBalances notices and a rebuild repairs it.
	if _, err := e.db.ExecContext(e.ctx, `UPDATE trust_balance_cache SET balance_cents = 1`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.VerifyBalances(e.ctx); err == nil {
		t.Fatal("a wrong cache passed verification")
	}
	if err := e.svc.RebuildBalances(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.VerifyBalances(e.ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := e.svc.Balances(e.ctx, e.accountant, EntryFilter{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("balances rows %+v %v", rows, err)
	}
}

// TestPGBelowZeroPostsFlagsNotifiesAndEmails is AC-005 and AC-021.
func TestPGBelowZeroPostsFlagsNotifiesAndEmails(t *testing.T) {
	e := newEnv(t)
	if _, err := e.reg.Set(e.ctx, "trust.flag_email_extra", "bookkeeper@example.test, AVERY.ATTORNEY@example.test", e.admin); err != nil {
		t.Fatal(err)
	}
	e.post(e.staff, e.in(TypeDeposit, 10000))
	e.events.reset()
	if n, _ := e.svc.UnreadCount(e.ctx, e.attorney); n != 0 {
		t.Fatalf("unread before %d", n)
	}
	r, err := e.svc.Post(e.ctx, e.attorney, e.in(TypeWithdrawal, 25000), "")
	if err != nil {
		t.Fatalf("a below-zero withdrawal must post: %v", err)
	}
	if r.Balances.MatterCents != -15000 || r.Balances.ClientCents != -15000 || r.Balances.AccountCents != -15000 {
		t.Fatalf("balances %+v", r.Balances)
	}
	if len(r.Flags) != 3 || r.Flags[0].Scope != ScopeMatter || r.Flags[1].Scope != ScopeClient || r.Flags[2].Scope != ScopeAccount {
		t.Fatalf("flags %+v", r.Flags)
	}
	if e.count("trust_ledger") != 2 {
		t.Fatal("the entry did not post")
	}
	names := strings.Join(e.events.names(), ",")
	if names != "trust.entry_posted,trust.flag_raised,trust.flag_raised,trust.flag_raised" {
		t.Fatalf("events %s", names)
	}
	// Email: the admin, the responsible attorney and the extra recipient, once each.
	mail, err := e.svc.Outbox(e.ctx, r.Flags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var to []string
	for _, m := range mail {
		to = append(to, strings.ToLower(m.To))
		if !strings.Contains(m.Body, "2026-0001") || !strings.Contains(m.Body, "not blocked") {
			t.Fatalf("body %q", m.Body)
		}
	}
	sort.Strings(to)
	if strings.Join(to, ",") != "ada.admin@example.test,avery.attorney@example.test,bookkeeper@example.test" {
		t.Fatalf("recipients %v", to)
	}
	// Notifications: the responsible attorney and the admin, not staff or another attorney.
	for who, want := range map[*domain.Principal]int{&e.attorney: 1, &e.admin: 1, &e.staff: 0, &e.other: 0} {
		if n, err := e.svc.UnreadCount(e.ctx, *who); err != nil || n != want {
			t.Fatalf("unread for %v: %d %v", who.Roles, n, err)
		}
	}
	page, err := e.svc.Notifications(e.ctx, e.attorney, nil, "", 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].Kind != NotifyTrustFlag || page.Items[0].Link != "/trust/flags/"+string(r.Flags[0].ID) {
		t.Fatalf("notifications %+v %v", page.Items, err)
	}
	// Another user cannot mark it read.
	if _, err := e.svc.MarkRead(e.ctx, e.other, page.Items[0].ID); code(err) != domain.CodeNotFound {
		t.Fatalf("mark another user's notification: %v", err)
	}
	if n, err := e.svc.MarkAllRead(e.ctx, e.attorney); err != nil || n != 1 {
		t.Fatalf("mark all read %d %v", n, err)
	}
	if n, _ := e.svc.UnreadCount(e.ctx, e.attorney); n != 0 {
		t.Fatalf("unread after mark all read %d", n)
	}
	if n, _ := e.svc.UnreadCount(e.ctx, e.admin); n != 1 {
		t.Fatalf("mark all read touched another user: %d", n)
	}
	// The flag shows until an attorney or admin resolves it with a note.
	w, err := e.svc.TrustAlertsWidget(e.ctx, e.accountant, "")
	if err != nil || len(w.Rows) != 3 || w.Target.Path != "/trust/flags" {
		t.Fatalf("widget %+v %v", w, err)
	}
	if _, err := e.svc.ResolveFlag(e.ctx, e.staff, r.Flags[0].ID, "fixed"); code(err) != domain.CodeForbidden {
		t.Fatalf("staff resolve: %v", err)
	}
	if _, err := e.svc.ResolveFlag(e.ctx, e.attorney, r.Flags[0].ID, "  "); code(err) != domain.CodeValidation {
		t.Fatalf("empty note: %v", err)
	}
	f, err := e.svc.ResolveFlag(e.ctx, e.attorney, r.Flags[0].ID, "Client check deposited on paper first")
	if err != nil || f.Status != "resolved" || f.ResolvedBy == nil || *f.ResolvedBy != e.attorney.UserID {
		t.Fatalf("resolve %+v %v", f, err)
	}
	if _, err := e.svc.ResolveFlag(e.ctx, e.admin, r.Flags[0].ID, "again"); code(err) != domain.CodeConflict {
		t.Fatalf("resolve twice: %v", err)
	}
	if w, _ = e.svc.TrustAlertsWidget(e.ctx, e.attorney, ""); len(w.Rows) != 2 {
		t.Fatalf("resolved flag still on widget: %+v", w.Rows)
	}
	// Delivery: a failing server keeps mail queued with its error; a working one sends it.
	if n, err := e.svc.DeliverMail(e.ctx, failSender{}); err != nil || n != 0 {
		t.Fatalf("fail delivery %d %v", n, err)
	}
	mail, _ = e.svc.Outbox(e.ctx, r.Flags[0].ID)
	if mail[0].SentAt != nil || mail[0].Attempts != 1 || mail[0].Error == "" {
		t.Fatalf("failed mail %+v", mail[0])
	}
	got := &okSender{}
	if n, err := e.svc.DeliverMail(e.ctx, got); err != nil || n != 3 || len(got.to) != 3 {
		t.Fatalf("delivery %d %v %v", n, err, got.to)
	}
	if n, _ := e.svc.DeliverMail(e.ctx, got); n != 0 {
		t.Fatal("mail sent twice")
	}
}

type failSender struct{}

func (failSender) Send(context.Context, string, string, string) error {
	return errors.New("synthetic smtp outage")
}

type okSender struct{ to []string }

func (o *okSender) Send(_ context.Context, to, _, _ string) error {
	o.to = append(o.to, to)
	return nil
}

// TestPGNoFlagWithoutDebit: a deposit into a negative balance does not raise a new flag;
// a debit that stays at or above zero does not either.
func TestPGNoFlagWithoutDebit(t *testing.T) {
	e := newEnv(t)
	e.post(e.staff, e.in(TypeDeposit, 1000))
	if r := e.post(e.attorney, e.in(TypeRefund, 1000)); len(r.Flags) != 0 || r.Balances.MatterCents != 0 {
		t.Fatalf("zero result flagged: %+v", r)
	}
	e.post(e.attorney, e.in(TypeWithdrawal, 500))
	if r := e.post(e.staff, e.in(TypeDeposit, 100)); len(r.Flags) != 0 {
		t.Fatalf("a deposit raised flags: %+v", r.Flags)
	}
	if n := e.count("trust_flags"); n != 3 {
		t.Fatalf("%d flags", n)
	}
}

// TestPGLedgerAppendOnlyAndReversals is AC-006.
func TestPGLedgerAppendOnlyAndReversals(t *testing.T) {
	e := newEnv(t)
	dep := e.post(e.staff, e.in(TypeDeposit, 50000)).Entry
	wd := e.post(e.attorney, e.in(TypeWithdrawal, 20000)).Entry
	for _, stmt := range []string{
		"UPDATE trust_ledger SET amount_cents = 1 WHERE id = '" + string(dep.ID) + "'",
		"UPDATE trust_ledger SET description = 'x'",
		"UPDATE trust_ledger SET description = description WHERE false",
		"DELETE FROM trust_ledger WHERE id = '" + string(wd.ID) + "'",
		"DELETE FROM trust_ledger",
		"TRUNCATE trust_ledger CASCADE",
	} {
		_, err := e.db.ExecContext(e.ctx, stmt)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := e.db.ExecContext(e.ctx, `UPDATE trust_ledger SET row_guard = 1`); err == nil || !strings.Contains(err.Error(), "check constraint") {
		t.Fatalf("row_guard changed: %v", err)
	}
	got, err := e.svc.Entry(e.ctx, e.accountant, dep.ID)
	if err != nil || got.AmountCents != 50000 || got.Description != dep.Description {
		t.Fatalf("row changed: %+v %v", got, err)
	}
	// A reversal is a new row; the original stays.
	if _, err := e.svc.Reverse(e.ctx, e.staff, wd.ID, ReverseInput{Reason: "keyed twice"}, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("staff reversal: %v", err)
	}
	if _, err := e.svc.Reverse(e.ctx, e.attorney, wd.ID, ReverseInput{}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("reversal without a reason: %v", err)
	}
	rv, err := e.svc.Reverse(e.ctx, e.attorney, wd.ID, ReverseInput{Reason: "keyed twice"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if rv.Entry.EntryType != TypeReversal || rv.Entry.Direction != Credit || rv.Entry.AmountCents != 20000 || *rv.Entry.ReversesEntryID != wd.ID || rv.Balances.MatterCents != 50000 {
		t.Fatalf("reversal %+v %+v", rv.Entry, rv.Balances)
	}
	if _, err := e.svc.Reverse(e.ctx, e.attorney, wd.ID, ReverseInput{Reason: "again"}, ""); code(err) != domain.CodeConflict {
		t.Fatalf("second reversal: %v", err)
	}
	if _, err := e.svc.Reverse(e.ctx, e.attorney, rv.Entry.ID, ReverseInput{Reason: "undo undo"}, ""); code(err) != domain.CodeConflict {
		t.Fatalf("reversal of a reversal: %v", err)
	}
	orig, _ := e.svc.Entry(e.ctx, e.admin, wd.ID)
	if orig.AmountCents != 20000 || orig.EntryType != TypeWithdrawal || orig.ReversesEntryID != nil {
		t.Fatalf("original changed: %+v", orig)
	}
	// Reversing the deposit after money went out takes the balance below zero: it posts
	// and flags.
	e.post(e.attorney, e.in(TypeWithdrawal, 30000))
	rd, err := e.svc.Reverse(e.ctx, e.admin, dep.ID, ReverseInput{Reason: "bounced check"}, "")
	if err != nil || rd.Balances.MatterCents != -30000 || len(rd.Flags) != 3 || rd.Entry.Direction != Debit || rd.Entry.AmountCents != -50000 {
		t.Fatalf("deposit reversal %+v %v", rd, err)
	}
	if n := e.count("trust_ledger"); n != 5 {
		t.Fatalf("%d ledger rows", n)
	}
	// The audit row for each post is in the append-only audit log.
	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'trust_entry'`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("audit rows %d %v", n, err)
	}
	// Ceiling recorded in PONYTAIL-DEBT.md: a superuser is not stopped by privileges.
	if _, err := e.super.ExecContext(e.ctx, `UPDATE trust_ledger SET description = description WHERE false`); err != nil {
		t.Fatalf("superuser ceiling changed: %v", err)
	}
}

// TestPGRolesAndWalls is AC-004 for trust plus the matter wall in the service layer.
func TestPGRolesAndWalls(t *testing.T) {
	e := newEnv(t)
	dep := e.post(e.staff, e.in(TypeDeposit, 1000))
	before := map[string]int{}
	tables := []string{"trust_ledger", "trust_flags", "trust_replenishment_requests", "trust_reconciliations", "trust_accounts", "audit_log", "trust_evergreen"}
	for _, tb := range tables {
		before[tb] = e.count(tb)
	}
	acc := e.accountant
	stmt := int64(1000)
	amount := int64(100)
	adj := e.in(TypeAdjustment, 10)
	adj.AdjustmentDirection = Credit
	writes := map[string]func() error{
		"deposit":    func() error { _, err := e.svc.Post(e.ctx, acc, e.in(TypeDeposit, 10), ""); return err },
		"withdrawal": func() error { _, err := e.svc.Post(e.ctx, acc, e.in(TypeWithdrawal, 10), ""); return err },
		"adjustment": func() error { _, err := e.svc.Post(e.ctx, acc, adj, ""); return err },
		"bulk":       func() error { _, err := e.svc.PostBulk(e.ctx, acc, []PostInput{e.in(TypeDeposit, 10)}, ""); return err },
		"reverse": func() error {
			_, err := e.svc.Reverse(e.ctx, acc, dep.Entry.ID, ReverseInput{Reason: "x"}, "")
			return err
		},
		"reconcile": func() error {
			_, err := e.svc.Reconcile(e.ctx, acc, ReconcileInput{TrustAccountID: e.account, StatementOn: "2026-10-31", StatementBalanceCents: &stmt}, "")
			return err
		},
		"replenish": func() error {
			_, err := e.svc.CreateReplenishment(e.ctx, acc, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount}, "")
			return err
		},
		"account": func() error {
			_, err := e.svc.CreateAccount(e.ctx, acc, AccountInput{Bank: "B", Name: "N", LastFour: "1111"}, "")
			return err
		},
		"evergreen": func() error { return e.svc.SetEvergreenMinimum(e.ctx, acc, e.matter.ID, 100) },
		"flat_fee": func() error {
			_, err := e.svc.ApplyFlatFee(e.ctx, acc, ApplyFlatFeeInput{MatterID: e.matter.ID, AmountCents: &amount}, "")
			return err
		},
	}
	for name, fn := range writes {
		if err := fn(); code(err) != domain.CodeForbidden {
			t.Fatalf("accountant %s: %v", name, err)
		}
	}
	for _, tb := range tables {
		if e.count(tb) != before[tb] {
			t.Fatalf("a denied write changed %s", tb)
		}
	}
	// The accountant reads everything trust.
	if p, err := e.svc.Entries(e.ctx, acc, EntryFilter{}); err != nil || len(p.Items) != 1 {
		t.Fatalf("accountant entries %v %v", p, err)
	}
	if _, err := e.svc.BalancesReport(e.ctx, acc, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reconciliations(e.ctx, acc, "", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Accounts(e.ctx, acc, nil, "", 0); err != nil {
		t.Fatal(err)
	}
	// Staff: deposits yes, withdrawals and adjustments no.
	for _, typ := range []string{TypeWithdrawal, TypeTransfer, TypeRefund} {
		if _, err := e.svc.Post(e.ctx, e.staff, e.in(typ, 10), ""); code(err) != domain.CodeForbidden {
			t.Fatalf("staff %s: %v", typ, err)
		}
	}
	if _, err := e.svc.Post(e.ctx, e.staff, adj, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("staff adjustment: %v", err)
	}
	if _, err := e.svc.CreateAccount(e.ctx, e.attorney, AccountInput{Bank: "B", Name: "N", LastFour: "1111"}, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("attorney account: %v", err)
	}
	// A client has no trust access through this service.
	if _, err := e.svc.Entries(e.ctx, e.client, EntryFilter{}); err == nil {
		t.Fatal("client read the ledger")
	}
	if _, err := e.svc.Entries(e.ctx, domain.Principal{}, EntryFilter{}); code(err) != domain.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
	// A wall hides the matter's trust rows from everyone not on it, the admin included.
	if err := e.practice.ReplaceWalls(e.ctx, e.matter.ID, []domain.MatterWall{{Meta: domain.Meta{ID: mustID(t)}, MatterID: e.matter.ID, UserID: e.attorney.UserID}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.Entries(e.ctx, e.admin, EntryFilter{}); len(p.Items) != 0 {
		t.Fatalf("walled rows listed: %+v", p.Items)
	}
	if _, err := e.svc.Entry(e.ctx, e.admin, dep.Entry.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("walled get: %v", err)
	}
	if _, err := e.svc.Post(e.ctx, e.admin, e.in(TypeDeposit, 10), ""); code(err) != domain.CodeNotFound {
		t.Fatalf("walled post: %v", err)
	}
	if rows, _ := e.svc.Balances(e.ctx, e.accountant, EntryFilter{}); len(rows) != 0 {
		t.Fatalf("walled balances: %+v", rows)
	}
	if p, err := e.svc.Entries(e.ctx, e.attorney, EntryFilter{}); err != nil || len(p.Items) != 1 {
		t.Fatalf("wall member: %v %v", p, err)
	}
}

// TestPGReconciliation is AC-007.
func TestPGReconciliation(t *testing.T) {
	e := newEnv(t)
	second := e.contact("Morgan Second")
	m2 := e.newMatter("2026-0002", "Second matter", "hourly", 0, second)
	e.post(e.staff, e.in(TypeDeposit, 250000))
	e.post(e.attorney, e.in(TypeWithdrawal, 40000))
	in2 := PostInput{TrustAccountID: e.account, ClientID: second, MatterID: m2.ID, EntryType: TypeDeposit, AmountCents: 12345, Method: "ach", Description: "retainer"}
	e.post(e.staff, in2)
	today := e.svc.now().Format("2006-01-02")
	right := int64(250000 - 40000 + 12345)
	r, err := e.svc.Reconcile(e.ctx, e.staff, ReconcileInput{TrustAccountID: e.account, StatementOn: today, StatementBalanceCents: &right}, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.LedgerTotalCents != right || r.ClientBalancesSumCents != right || r.StatementDifferenceCents != 0 || r.ClientSumDifferenceCents != 0 {
		t.Fatalf("zero case %+v", r)
	}
	off := right - 1234
	r2, err := e.svc.Reconcile(e.ctx, e.attorney, ReconcileInput{TrustAccountID: e.account, StatementOn: today, StatementBalanceCents: &off}, "")
	if err != nil || r2.StatementDifferenceCents != -1234 {
		t.Fatalf("off case %+v %v", r2, err)
	}
	stored, err := e.svc.Reconciliation(e.ctx, e.accountant, r2.ID)
	if err != nil || stored.StatementDifferenceCents != -1234 || stored.LedgerTotalCents != right {
		t.Fatalf("stored %+v %v", stored, err)
	}
	// A statement dated before the entries counts none of them.
	zero := int64(0)
	past, err := e.svc.Reconcile(e.ctx, e.admin, ReconcileInput{TrustAccountID: e.account, StatementOn: "2020-01-31", StatementBalanceCents: &zero}, "")
	if err != nil || past.LedgerTotalCents != 0 || past.StatementDifferenceCents != 0 {
		t.Fatalf("past %+v %v", past, err)
	}
	// A wall does not change the bank-account totals for a reader on every wall. A reader
	// outside the wall cannot reconcile the account (TestPGReconciliationUnderWall).
	if err := e.practice.ReplaceWalls(e.ctx, m2.ID, []domain.MatterWall{{Meta: domain.Meta{ID: mustID(t)}, MatterID: m2.ID, UserID: e.attorney.UserID}}); err != nil {
		t.Fatal(err)
	}
	r3, err := e.svc.Reconcile(e.ctx, e.attorney, ReconcileInput{TrustAccountID: e.account, StatementOn: today, StatementBalanceCents: &right}, "")
	if err != nil || r3.StatementDifferenceCents != 0 {
		t.Fatalf("walled %+v %v", r3, err)
	}
	if _, err := e.svc.Reconcile(e.ctx, e.staff, ReconcileInput{TrustAccountID: e.account, StatementOn: "10/31/2026", StatementBalanceCents: &right}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("bad date: %v", err)
	}
	if _, err := e.svc.Reconcile(e.ctx, e.staff, ReconcileInput{TrustAccountID: e.account, StatementOn: today}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("missing balance: %v", err)
	}
	if _, err := e.svc.Reconcile(e.ctx, e.staff, ReconcileInput{TrustAccountID: mustID(t), StatementOn: today, StatementBalanceCents: &right}, ""); code(err) != domain.CodeNotFound {
		t.Fatalf("unknown account: %v", err)
	}
	rep, err := e.svc.ReconciliationReport(e.ctx, e.attorney, e.account)
	if err != nil || len(rep.Rows) != 4 || rep.Rows[1]["statement_difference"] != "-12.34" {
		t.Fatalf("history %+v %v", rep, err)
	}
	// The accountant is outside the new wall: only the statement dated before M2's rows.
	rep, err = e.svc.ReconciliationReport(e.ctx, e.accountant, e.account)
	if err != nil || len(rep.Rows) != 1 || rep.Rows[0]["statement_on"] != "2020-01-31" {
		t.Fatalf("walled history %+v %v", rep, err)
	}
}

// TestPGEvergreenReplenishment: a debit under the evergreen minimum opens one request
// and notifies the responsible attorney; a deposit back over it satisfies the request.
func TestPGEvergreenReplenishment(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetEvergreenMinimum(e.ctx, e.staff, e.matter.ID, 50000); err != nil {
		t.Fatal(err)
	}
	e.post(e.staff, e.in(TypeDeposit, 60000))
	if n := e.count("trust_replenishment_requests"); n != 0 {
		t.Fatalf("%d requests above the minimum", n)
	}
	e.events.reset()
	e.post(e.attorney, e.in(TypeWithdrawal, 20000))
	e.post(e.attorney, e.in(TypeWithdrawal, 5000))
	page, err := e.svc.Replenishments(e.ctx, e.accountant, e.matter.ID, ReplenishActive, "", 0)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("requests %+v %v", page.Items, err)
	}
	req := page.Items[0]
	if req.AmountCents != 10000 || req.ThresholdCents == nil || *req.ThresholdCents != 50000 {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(strings.Join(e.events.names(), ","), "trust.replenishment_requested") {
		t.Fatalf("events %v", e.events.names())
	}
	n, err := e.svc.Notifications(e.ctx, e.attorney, nil, "", 0)
	if err != nil || len(n.Items) != 1 || n.Items[0].Kind != NotifyReplenish {
		t.Fatalf("attorney notifications %+v %v", n.Items, err)
	}
	w, err := e.svc.ReplenishmentWidget(e.ctx, e.staff, "")
	if err != nil || len(w.Rows) != 1 {
		t.Fatalf("widget %+v %v", w, err)
	}
	e.post(e.staff, e.in(TypeDeposit, 30000))
	if got, _ := e.svc.Replenishment(e.ctx, e.staff, req.ID); got.Status != ReplenishSatisfied {
		t.Fatalf("not satisfied: %+v", got)
	}
	// Manual request from collections, then cancel; a closed request stays closed.
	amount := int64(7500)
	inv := mustID(t)
	m, err := e.svc.CreateReplenishment(e.ctx, e.staff, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount, InvoiceID: inv}, "")
	if err != nil || m.ThresholdCents != nil || m.InvoiceID == nil || *m.InvoiceID != inv {
		t.Fatalf("manual %+v %v", m, err)
	}
	if _, err := e.svc.CreateReplenishment(e.ctx, e.staff, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("no amount: %v", err)
	}
	if _, err := e.svc.CancelReplenishment(e.ctx, e.staff, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SatisfyReplenishment(e.ctx, e.staff, m.ID); code(err) != domain.CodeConflict {
		t.Fatalf("satisfy cancelled: %v", err)
	}
	// The firm default applies to a matter without its own minimum.
	if _, err := e.reg.Set(e.ctx, "trust.evergreen_minimum_cents", "100.00", e.admin); err != nil {
		t.Fatal(err)
	}
	other := e.newMatter("2026-0003", "Default minimum", "hourly", 0, e.clientID)
	if v, ok, err := e.svc.EvergreenMinimum(e.ctx, e.staff, other.ID); err != nil || !ok || v != 10000 {
		t.Fatalf("default minimum %d %v %v", v, ok, err)
	}
}

// TestPGFlatFeeReadyToApplyCollected is the trust half of AC-026.
func TestPGFlatFeeReadyToApplyCollected(t *testing.T) {
	e := newEnv(t)
	fee := e.newMatter("2026-0010", "Flat fee estate plan", "flat", 150000, e.clientID)
	none := e.newMatter("2026-0011", "Flat fee, no amount", "flat", 0, e.clientID)
	st, err := e.svc.FlatFeeStatus(e.ctx, e.accountant, none.ID)
	if err != nil || st.Status != FeeNeedsAmount {
		t.Fatalf("needs amount %+v %v", st, err)
	}
	dep := e.in(TypeDeposit, 100000)
	dep.MatterID = fee.ID
	e.post(e.staff, dep)
	if st, _ = e.svc.FlatFeeStatus(e.ctx, e.staff, fee.ID); st.Status != FeeAwaitingFunds || st.TrustCents != 100000 {
		t.Fatalf("awaiting %+v", st)
	}
	dep.AmountCents = 50000
	e.post(e.staff, dep)
	if st, _ = e.svc.FlatFeeStatus(e.ctx, e.staff, fee.ID); st.Status != FeeReadyToApply {
		t.Fatalf("ready %+v", st)
	}
	amount := int64(150000)
	in := ApplyFlatFeeInput{MatterID: fee.ID, AmountCents: &amount}
	if _, err := e.svc.ApplyFlatFee(e.ctx, e.staff, in, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("staff apply: %v", err)
	}
	if _, err := e.svc.ApplyFlatFee(e.ctx, e.attorney, ApplyFlatFeeInput{MatterID: fee.ID}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("apply without amount: %v", err)
	}
	r, err := e.svc.ApplyFlatFee(e.ctx, e.attorney, in, "apply-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Entry.EntryType != TypeTransfer || r.Entry.AmountCents != 150000 || r.Entry.Method != "internal_transfer" || r.Balances.MatterCents != 0 || len(r.Flags) != 0 {
		t.Fatalf("transfer %+v %+v", r.Entry, r.Balances)
	}
	if st, _ = e.svc.FlatFeeStatus(e.ctx, e.accountant, fee.ID); st.Status != FeeCollected || st.CollectedCents != 150000 {
		t.Fatalf("collected %+v", st)
	}
	// The same key replays the first result; a new key on a collected fee is a conflict.
	again, err := e.svc.ApplyFlatFee(e.ctx, e.attorney, in, "apply-1")
	if err != nil || again.Entry.ID != r.Entry.ID {
		t.Fatalf("replay %+v %v", again.Entry, err)
	}
	if _, err := e.svc.ApplyFlatFee(e.ctx, e.attorney, in, "apply-2"); code(err) != domain.CodeConflict {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := e.svc.ApplyFlatFee(e.ctx, e.attorney, ApplyFlatFeeInput{MatterID: e.matter.ID, AmountCents: &amount}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("hourly matter: %v", err)
	}
	// Reversing the transfer puts the money back and the row is Ready again.
	if _, err := e.svc.Reverse(e.ctx, e.attorney, r.Entry.ID, ReverseInput{Reason: "applied early"}, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ = e.svc.FlatFeeStatus(e.ctx, e.staff, fee.ID); st.Status != FeeReadyToApply || st.CollectedCents != 0 {
		t.Fatalf("after reversal %+v", st)
	}
}

// TestPGIdempotencyAndBulk: replays write nothing new; a bad bulk item posts nothing; a
// good bulk applies in order and flags the item that went below zero.
func TestPGIdempotencyAndBulk(t *testing.T) {
	e := newEnv(t)
	in := e.in(TypeDeposit, 2500)
	a, err := e.svc.Post(e.ctx, e.staff, in, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	e.events.reset()
	b, err := e.svc.Post(e.ctx, e.staff, in, "key-1")
	if err != nil || b.Entry.ID != a.Entry.ID || e.count("trust_ledger") != 1 {
		t.Fatalf("replay %v %v", b.Entry.ID, err)
	}
	if len(e.events.names()) != 0 {
		t.Fatalf("a replay emitted %v", e.events.names())
	}
	in.AmountCents = 2600
	if _, err := e.svc.Post(e.ctx, e.staff, in, "key-1"); code(err) != domain.CodeConflict {
		t.Fatalf("different body: %v", err)
	}
	bad := e.in(TypeWithdrawal, 100)
	bad.Method = "bitcoin"
	if _, err := e.svc.PostBulk(e.ctx, e.attorney, []PostInput{e.in(TypeDeposit, 100), bad}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("bad bulk: %v", err)
	} else if api := err.(*domain.APIError); api.Field != "items[1].method" {
		t.Fatalf("field %q", api.Field)
	}
	if _, err := e.svc.PostBulk(e.ctx, e.staff, []PostInput{e.in(TypeDeposit, 100), e.in(TypeWithdrawal, 100)}, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("staff bulk withdrawal: %v", err)
	}
	if e.count("trust_ledger") != 1 {
		t.Fatal("a rejected batch posted")
	}
	out, err := e.svc.PostBulk(e.ctx, e.attorney, []PostInput{e.in(TypeWithdrawal, 2000), e.in(TypeWithdrawal, 1000), e.in(TypeDeposit, 500)}, "")
	if err != nil || len(out) != 3 {
		t.Fatal(err)
	}
	if len(out[0].Flags) != 0 || len(out[1].Flags) != 3 || out[1].Balances.MatterCents != -500 || out[2].Balances.MatterCents != 0 {
		t.Fatalf("bulk %+v", out)
	}
	// Validation rejects malformed bodies before anything is written.
	cases := map[string]PostInput{
		"amount_cents":         e.in(TypeDeposit, 0),
		"entry_type":           e.in("transfer", 10),
		"adjustment_direction": func() PostInput { p := e.in(TypeAdjustment, 10); p.AdjustmentDirection = Debit; return p }(),
		"description":          func() PostInput { p := e.in(TypeDeposit, 10); p.Description = " "; return p }(),
		"client_id":            func() PostInput { p := e.in(TypeDeposit, 10); p.ClientID = e.contact("Not a party"); return p }(),
	}
	for field, p := range cases {
		_, err := e.svc.Post(e.ctx, e.attorney, p, "")
		if api, ok := err.(*domain.APIError); !ok || api.Field != field {
			t.Fatalf("%s: %v", field, err)
		}
	}
	neg := e.in(TypeWithdrawal, -10)
	if _, err := e.svc.Post(e.ctx, e.attorney, neg, ""); code(err) != domain.CodeValidation {
		t.Fatalf("negative withdrawal: %v", err)
	}
	// An inactive account rolls the whole post back: no row, no event.
	off := false
	if _, err := e.svc.UpdateAccount(e.ctx, e.admin, e.account, AccountPatch{Active: &off}); err != nil {
		t.Fatal(err)
	}
	n := e.count("trust_ledger")
	e.events.reset()
	if _, err := e.svc.Post(e.ctx, e.staff, e.in(TypeDeposit, 10), ""); code(err) != domain.CodeValidation {
		t.Fatalf("inactive account: %v", err)
	}
	if e.count("trust_ledger") != n || len(e.events.names()) != 0 {
		t.Fatal("a rolled-back post left a row or an event")
	}
}

// TestPGConcurrentWithdrawalsSerialize: two withdrawals racing on one balance cannot
// both read it before the other posts, so exactly one of them flags.
func TestPGConcurrentWithdrawalsSerialize(t *testing.T) {
	e := newEnv(t)
	for round := 0; round < 5; round++ {
		e.post(e.staff, e.in(TypeDeposit, 1000))
		var wg sync.WaitGroup
		results := make([]PostResult, 2)
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = e.svc.Post(e.ctx, e.attorney, e.in(TypeWithdrawal, 600), "")
			}(i)
		}
		wg.Wait()
		flagged := 0
		for i := range results {
			if errs[i] != nil {
				t.Fatal(errs[i])
			}
			if len(results[i].Flags) > 0 {
				flagged++
			}
		}
		if flagged != 1 {
			t.Fatalf("round %d: %d flagged: %+v", round, flagged, results)
		}
		// Bring the balance back to zero for the next round.
		e.post(e.staff, e.in(TypeDeposit, 200))
	}
	if err := e.svc.VerifyBalances(e.ctx); err != nil {
		t.Fatal(err)
	}
}

// TestPGReports: client and account ledgers carry running balances and CSV renders the
// same rows with formula text neutralised.
func TestPGReports(t *testing.T) {
	e := newEnv(t)
	e.post(e.staff, e.in(TypeDeposit, 10000))
	w := e.in(TypeWithdrawal, 2500)
	w.Description = "=HYPERLINK(\"http://example.test\")"
	e.post(e.attorney, w)
	rep, err := e.svc.ClientLedger(e.ctx, e.accountant, e.clientID, "")
	if err != nil || len(rep.Rows) != 2 || rep.Rows[1]["balance"] != "75.00" || rep.Rows[1]["amount"] != "-25.00" {
		t.Fatalf("client ledger %+v %v", rep, err)
	}
	csv, err := rep.CSV()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(csv), "'=HYPERLINK") || !strings.Contains(string(csv), ",-25.00,") {
		t.Fatalf("csv %s", csv)
	}
	today := e.svc.now().Format("2006-01-02")
	acct, err := e.svc.AccountLedger(e.ctx, e.accountant, e.account, today, today)
	if err != nil || len(acct.Rows) != 3 || acct.Rows[0]["entry_type"] != "opening_balance" || acct.Rows[2]["balance"] != "75.00" {
		t.Fatalf("account ledger %+v %v", acct, err)
	}
	bal, err := e.svc.BalancesReport(e.ctx, e.accountant, e.account)
	if err != nil || len(bal.Rows) != 1 || bal.Rows[0]["client"] != "Casey Client" || bal.Rows[0]["balance"] != "75.00" {
		t.Fatalf("balances %+v %v", bal, err)
	}
	if _, err := e.svc.ClientLedger(e.ctx, e.accountant, "", ""); code(err) != domain.CodeValidation {
		t.Fatalf("missing client: %v", err)
	}
}

// TestPGReconciliationReminder notifies admins once a month after the reminder day.
func TestPGReconciliationReminder(t *testing.T) {
	e := newEnv(t)
	now := e.svc.now()
	day := now.AddDate(0, 0, -now.Day()+10) // the 10th of this month
	n, err := e.svc.RemindReconciliations(e.ctx, day)
	if err != nil || n != 1 {
		t.Fatalf("reminders %d %v", n, err)
	}
	if n, _ := e.svc.RemindReconciliations(e.ctx, day); n != 0 {
		t.Fatalf("reminded twice: %d", n)
	}
	early := day.AddDate(0, 1, -8) // the 2nd of next month
	if n, _ := e.svc.RemindReconciliations(e.ctx, early); n != 0 {
		t.Fatalf("reminded before the day: %d", n)
	}
	if c, _ := e.svc.UnreadCount(e.ctx, e.admin); c != 1 {
		t.Fatalf("admin unread %d", c)
	}
}

// cancelSender cancels the delivery's context on its first send, as App.Close does
// mid-run, and fails that send.
type cancelSender struct {
	cancel context.CancelFunc
	tried  []string
}

func (c *cancelSender) Send(_ context.Context, to, _, _ string) error {
	c.tried = append(c.tried, to)
	c.cancel()
	return context.Canceled
}

// acceptThenCancelSender has the server take the message, then cancels the delivery's
// context, as App.Close does when it lands between DATA and the outbox update.
type acceptThenCancelSender struct {
	cancel context.CancelFunc
	tried  []string
}

func (c *acceptThenCancelSender) Send(_ context.Context, to, _, _ string) error {
	c.tried = append(c.tried, to)
	c.cancel()
	return nil
}

// Opus checkpoint P2: a message the server took is marked sent even when shutdown
// cancels the delivery right after, so the next start does not send it again.
func TestPGDeliverMailMarksAcceptedMailSentWhenCancelled(t *testing.T) {
	e := newEnv(t)
	for k, to := range []string{"one@example.test", "two@example.test"} {
		if _, err := e.db.ExecContext(e.ctx, `INSERT INTO mail_outbox (id, created_at, updated_at, created_by, kind, record_id, to_address, subject, body)
VALUES ($1,$2,$2,'system','trust_flag','synthetic',$3,'Synthetic','Synthetic')`, fmt.Sprintf("mail-%d", k), k, to); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	s := &acceptThenCancelSender{cancel: cancel}
	sent, err := e.svc.DeliverMail(ctx, s)
	if !errors.Is(err, context.Canceled) || sent != 1 || len(s.tried) != 1 {
		t.Fatalf("DeliverMail: sent %d err %v tried %v, want one sent, then context.Canceled", sent, err, s.tried)
	}
	mail, err := e.svc.Outbox(e.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mail {
		if m.To == s.tried[0] && (m.SentAt == nil || m.Attempts != 1 || m.Error != "") {
			t.Fatalf("the accepted row is not marked sent: %+v", m)
		}
		if m.To != s.tried[0] && (m.SentAt != nil || m.Attempts != 0) {
			t.Fatalf("an untried row changed: %+v", m)
		}
	}
}

// Opus checkpoint P2: a delivery cancelled mid-run stops before the next send. Rows it
// never tried keep no attempt and no error.
func TestPGDeliverMailStopsWhenCancelled(t *testing.T) {
	e := newEnv(t)
	for k, to := range []string{"one@example.test", "two@example.test", "three@example.test"} {
		if _, err := e.db.ExecContext(e.ctx, `INSERT INTO mail_outbox (id, created_at, updated_at, created_by, kind, record_id, to_address, subject, body)
VALUES ($1,$2,$2,'system','trust_flag','synthetic',$3,'Synthetic','Synthetic')`, fmt.Sprintf("mail-%d", k), k, to); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	s := &cancelSender{cancel: cancel}
	if _, err := e.svc.DeliverMail(ctx, s); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeliverMail after cancel: %v, want context.Canceled", err)
	}
	if len(s.tried) != 1 {
		t.Fatalf("sends after cancel: %v", s.tried)
	}
	mail, err := e.svc.Outbox(e.ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mail {
		tried := m.To == s.tried[0]
		if tried && (m.Attempts != 1 || m.Error == "") {
			t.Fatalf("the tried row %+v lacks its attempt", m)
		}
		if !tried && (m.Attempts != 0 || m.Error != "" || m.SentAt != nil) {
			t.Fatalf("an untried row changed: %+v", m)
		}
	}
}

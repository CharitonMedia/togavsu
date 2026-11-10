package trust

import (
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// lastSunk is the last balance the sink got for the env's main owner.
func (e *env) lastSunk() (domain.Cents, int) {
	e.sunkMu.Lock()
	defer e.sunkMu.Unlock()
	var last domain.Cents
	n := 0
	for _, row := range e.sunk {
		if row.TrustAccountID == e.account && row.ClientID == e.clientID && row.MatterID == e.matter.ID {
			last, n = row.BalanceCents, n+1
		}
	}
	return last, n
}

// Codex checkpoint 2, P2 post.go: after-commit sink calls that run out of order still
// leave the practice copy at the newest balance, and a failed sink is repaired by
// RebuildBalances.
func TestPGBalanceSinkOutOfOrderKeepsNewest(t *testing.T) {
	e := newEnv(t)
	a := e.post(e.staff, e.in(TypeDeposit, 10000))
	b := e.post(e.staff, e.in(TypeDeposit, 10000))
	if b.Balances.MatterCents != 20000 {
		t.Fatalf("balances %+v", b.Balances)
	}
	// A's after-commit effect runs late, after B's: the sink must not go back to 100.00.
	e.svc.Committed(e.ctx, a)
	if last, _ := e.lastSunk(); last != 20000 {
		t.Fatalf("a late sink call left the practice copy at %s", last)
	}
	// A failed sink leaves the copy stale; RebuildBalances repushes the ledger value.
	e.sinkFail = true
	e.post(e.attorney, e.in(TypeWithdrawal, 5000))
	e.sinkFail = false
	if last, _ := e.lastSunk(); last != 20000 {
		t.Fatalf("failed sink recorded %s", last)
	}
	if err := e.svc.RebuildBalances(e.ctx); err != nil {
		t.Fatal(err)
	}
	if last, _ := e.lastSunk(); last != 15000 {
		t.Fatalf("rebuild did not repair the practice copy: %s", last)
	}
}

// Concurrent posts on one owner: whatever order the goroutines' after-commit effects run
// in, the last sink value equals the ledger balance.
func TestPGBalanceSinkConcurrentPostsEndAtLedger(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			typ, amt := TypeDeposit, int64(1000+i)
			if i%3 == 0 {
				typ, amt = TypeWithdrawal, 700
			}
			_, err := e.svc.Post(e.ctx, e.admin, e.in(typ, amt), "")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want, err := e.svc.clientMatterCents(e.ctx, e.clientID, e.matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	last, n := e.lastSunk()
	if n != 12 || int64(last) != want {
		t.Fatalf("last sink %s after %d calls, ledger %d", last, n, want)
	}
}

func (e *env) wall(matter domain.ID, users ...domain.ID) {
	e.t.Helper()
	var rows []domain.MatterWall
	for _, u := range users {
		rows = append(rows, domain.MatterWall{Meta: domain.Meta{ID: mustID(e.t)}, MatterID: matter, UserID: u})
	}
	if err := e.practice.ReplaceWalls(e.ctx, matter, rows); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) notificationRows(user domain.ID, kind string) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND kind = $2`, string(user), kind).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// Codex checkpoint 2, P2 notify.go: a below-zero post on a walled matter still emails a
// walled-out admin (PRD 8.4 and 8.17: the email cannot be turned off) but without the
// matter's number, name, amounts or flag link, and gives them no in-app notification.
// The extra address is not on any wall and gets the same redacted notice.
func TestPGWalledFlagEmailOmitsMatter(t *testing.T) {
	e := newEnv(t)
	if _, err := e.reg.Set(e.ctx, "trust.flag_email_extra", "bookkeeper@example.test", e.admin); err != nil {
		t.Fatal(err)
	}
	e.wall(e.matter.ID, e.attorney.UserID)
	r := e.post(e.attorney, e.in(TypeWithdrawal, 2500))
	if len(r.Flags) != 3 {
		t.Fatalf("flags %+v", r.Flags)
	}
	mail, err := e.svc.Outbox(e.ctx, r.Flags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Mail{}
	for _, m := range mail {
		got[strings.ToLower(m.To)] = m
	}
	if len(got) != 3 {
		t.Fatalf("mail %+v", mail)
	}
	full := got["avery.attorney@example.test"]
	if !strings.Contains(full.Body, "2026-0001") || !strings.Contains(full.Subject, "Client v. Synthetic") {
		t.Fatalf("the attorney on the wall lost the details: %+v", full)
	}
	for _, addr := range []string{"ada.admin@example.test", "bookkeeper@example.test"} {
		m, ok := got[addr]
		if !ok {
			t.Fatalf("%s got no below-zero email", addr)
		}
		for _, leak := range []string{"2026-0001", "Client v. Synthetic", "25.00", "/trust/flags/", string(e.matter.ID)} {
			if strings.Contains(m.Subject+m.Body, leak) {
				t.Fatalf("%s email leaks %q: %+v", addr, leak, m)
			}
		}
		if !strings.Contains(m.Body, "not blocked") {
			t.Fatalf("redacted body %q", m.Body)
		}
	}
	if n := e.notificationRows(e.admin.UserID, NotifyTrustFlag); n != 0 {
		t.Fatalf("walled-out admin got %d notification rows", n)
	}
	if n, _ := e.svc.UnreadCount(e.ctx, e.attorney); n != 1 {
		t.Fatalf("attorney unread %d", n)
	}
	// An admin on the wall gets the details and the notification again.
	e.wall(e.matter.ID, e.attorney.UserID, e.admin.UserID)
	r = e.post(e.attorney, e.in(TypeWithdrawal, 100))
	mail, _ = e.svc.Outbox(e.ctx, r.Flags[0].ID)
	for _, m := range mail {
		if strings.EqualFold(m.To, "ada.admin@example.test") && !strings.Contains(m.Body, "2026-0001") {
			t.Fatalf("admin on the wall got the redacted email: %+v", m)
		}
		if strings.EqualFold(m.To, "bookkeeper@example.test") && strings.Contains(m.Body, "2026-0001") {
			t.Fatalf("extra address got a walled matter's details: %+v", m)
		}
	}
	if n := e.notificationRows(e.admin.UserID, NotifyTrustFlag); n != 1 {
		t.Fatalf("admin on the wall notification rows %d", n)
	}
}

// A responsible attorney walled off their own matter gets the redacted email and no
// notification; the attorney on the wall who posted is not a flag recipient.
func TestPGWalledOutResponsibleAttorneyRedacted(t *testing.T) {
	e := newEnv(t)
	e.wall(e.matter.ID, e.other.UserID)
	r := e.post(e.other, e.in(TypeWithdrawal, 100))
	mail, err := e.svc.Outbox(e.ctx, r.Flags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawAttorney bool
	for _, m := range mail {
		if strings.Contains(m.Body, "2026-0001") {
			t.Fatalf("walled-out recipient %s got details", m.To)
		}
		sawAttorney = sawAttorney || strings.EqualFold(m.To, "avery.attorney@example.test")
	}
	if !sawAttorney || len(mail) != 2 {
		t.Fatalf("mail %+v", mail)
	}
	if n := e.notificationRows(e.attorney.UserID, NotifyTrustFlag); n != 0 {
		t.Fatalf("walled-out attorney notification rows %d", n)
	}
}

// Codex checkpoint 2, P2 replenish.go: a replenishment request (evergreen or manual) does
// not notify a deactivated responsible attorney or one walled off the matter; the request
// still opens.
func TestPGReplenishmentSkipsInactiveOrWalledAttorney(t *testing.T) {
	e := newEnv(t)
	u, err := e.users.Get(e.ctx, e.attorney.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Active = false
	if err := e.users.Update(e.ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetEvergreenMinimum(e.ctx, e.staff, e.matter.ID, 50000); err != nil {
		t.Fatal(err)
	}
	e.post(e.staff, e.in(TypeDeposit, 60000))
	e.post(e.admin, e.in(TypeWithdrawal, 20000))
	amount := int64(500)
	if _, err := e.svc.CreateReplenishment(e.ctx, e.staff, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount}, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.count("trust_replenishment_requests"); n != 2 {
		t.Fatalf("%d requests", n)
	}
	if n := e.notificationRows(e.attorney.UserID, NotifyReplenish); n != 0 {
		t.Fatalf("deactivated attorney got %d replenishment notifications", n)
	}
	// Active again but walled off the matter: still no notification row.
	u.Active = true
	if err := e.users.Update(e.ctx, u); err != nil {
		t.Fatal(err)
	}
	e.wall(e.matter.ID, e.staff.UserID)
	if _, err := e.svc.CreateReplenishment(e.ctx, e.staff, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount}, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.notificationRows(e.attorney.UserID, NotifyReplenish); n != 0 {
		t.Fatalf("walled-out attorney got %d replenishment notifications", n)
	}
	// Positive path: on the wall and active, the attorney is told.
	e.wall(e.matter.ID, e.staff.UserID, e.attorney.UserID)
	if _, err := e.svc.CreateReplenishment(e.ctx, e.staff, ReplenishmentInput{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount}, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.notificationRows(e.attorney.UserID, NotifyReplenish); n != 1 {
		t.Fatalf("visible attorney notifications %d", n)
	}
}

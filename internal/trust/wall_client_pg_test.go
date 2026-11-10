package trust

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Codex checkpoint 3, P1 post.go: a client's second matter is walled from the posting
// users. Its trust money must not show through the client or account totals on the post
// result (single, bulk and reversal), the flag JSON, the flag pages, the trust-alerts
// widget, the below-zero email or the flag event. The overall total still raises the
// client and account flags (PRD 8.4), and a reader on every wall sees the overall total.
func TestPGWalledSecondMatterHiddenFromClientTotals(t *testing.T) {
	e := newEnv(t)
	if _, err := e.reg.Set(e.ctx, "trust.flag_email_extra", "bookkeeper@example.test", e.admin); err != nil {
		t.Fatal(err)
	}
	// M2 belongs to the same client and is walled to Oren and the admin only.
	m2 := e.newMatter("2026-0002", "Walled Synthetic", "hourly", 0, e.clientID)
	e.wall(m2.ID, e.other.UserID, e.admin.UserID)
	onM2 := func(typ string, cents int64) PostInput {
		in := e.in(typ, cents)
		in.MatterID = m2.ID
		return in
	}
	e.post(e.other, onM2(TypeDeposit, 512347))

	srv := e.server(t)
	staff, attorney, admin := e.as(t, srv, "staff"), e.as(t, srv, "attorney"), e.as(t, srv, "admin")
	leaks := func(where string, body string, bad ...string) {
		t.Helper()
		for _, b := range bad {
			if strings.Contains(body, b) {
				t.Fatalf("%s leaks the walled matter (%q): %s", where, b, body)
			}
		}
	}

	// A deposit on M1 returns client and account totals over M1 only.
	code, body, _ := staff.do("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 100))
	var dep PostResult
	if code != http.StatusCreated || json.Unmarshal(body, &dep) != nil {
		t.Fatalf("deposit %d %s", code, body)
	}
	if dep.Balances != (Balances{MatterCents: 100, ClientCents: 100, AccountCents: 100}) {
		t.Fatalf("staff deposit balances %+v include the walled matter", dep.Balances)
	}
	leaks("deposit result", string(body), "512447", "5124.47", "512347")
	// Oren, on M2's wall, sees the whole client.
	if r := e.post(e.other, onM2(TypeWithdrawal, 900000)); r.Balances.ClientCents != 100-387653 {
		t.Fatalf("walled reader client total %+v", r.Balances)
	}

	// M2 is now -3876.53. M1 goes to 500.00 and the client overall to -3376.53, which
	// raises client and account flags on the M1 entry.
	e.post(e.staff, e.in(TypeDeposit, 99900))
	e.events.reset()
	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []PostInput{e.in(TypeWithdrawal, 50000)}})
	var bulk struct {
		Data []PostResult `json:"data"`
	}
	if code != http.StatusCreated || json.Unmarshal(body, &bulk) != nil || len(bulk.Data) != 1 {
		t.Fatalf("bulk %d %s", code, body)
	}
	res := bulk.Data[0]
	hidden := []string{"337653", "3376.53", "387653", "3876.53"}
	leaks("bulk result", string(body), hidden...)
	if res.Balances != (Balances{MatterCents: 50000, ClientCents: 50000, AccountCents: 50000}) {
		t.Fatalf("bulk balances %+v", res.Balances)
	}
	scopes := map[string]Flag{}
	for _, f := range res.Flags {
		scopes[f.Scope] = f
	}
	if len(res.Flags) != 2 || scopes[ScopeClient].BalanceCents != 50000 || scopes[ScopeAccount].BalanceCents != 50000 {
		t.Fatalf("the overall client total must raise client and account flags shown as the visible total: %+v", res.Flags)
	}
	for _, ev := range e.events.events {
		if ev.Name == "trust.flag_raised" && ev.Data["balance_scope"] != ScopeMatter && ev.Data["balance_cents"] != "500.00" {
			t.Fatalf("flag event carries the walled-inclusive total: %+v", ev)
		}
	}
	clientFlag := string(scopes[ScopeClient].ID)

	// Every flag read by a user outside M2's wall shows the visible total.
	for _, path := range []string{"/api/v1/trust-flags", "/api/v1/trust-flags/" + clientFlag, "/api/v1/widgets/trust-alerts", "/trust/flags", "/trust/flags/" + clientFlag} {
		for _, c := range []caller{staff, attorney} {
			code, body, _ := c.do("GET", path, nil)
			if code != http.StatusOK {
				t.Fatalf("%s as %s: %d %s", path, c.as, code, body)
			}
			leaks(path+" as "+c.as, string(body), hidden...)
		}
	}
	code, body, _ = staff.do("GET", "/api/v1/trust-flags/"+clientFlag, nil)
	var shown Flag
	if code != http.StatusOK || json.Unmarshal(body, &shown) != nil || shown.BalanceCents != 50000 {
		t.Fatalf("staff client flag %d %s", code, body)
	}
	// The admin is on every wall and sees the overall total that raised the flag.
	code, body, _ = admin.do("GET", "/api/v1/trust-flags/"+clientFlag, nil)
	if code != http.StatusOK || json.Unmarshal(body, &shown) != nil || shown.BalanceCents != -337653 {
		t.Fatalf("admin client flag %d %s", code, body)
	}
	if code, body, _ := admin.do("GET", "/trust/flags/"+clientFlag, nil); code != http.StatusOK || !strings.Contains(string(body), "-3376.53") {
		t.Fatalf("admin flag page %d %s", code, body)
	}

	// The email: the attorney and the extra address are shown M1 only, the admin all.
	mail, err := e.svc.Outbox(e.ctx, res.Flags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Mail{}
	for _, m := range mail {
		got[strings.ToLower(m.To)] = m
	}
	for _, addr := range []string{"avery.attorney@example.test", "bookkeeper@example.test"} {
		m, ok := got[addr]
		if !ok {
			t.Fatalf("%s got no below-zero email: %+v", addr, mail)
		}
		leaks(addr+" email", m.Subject+m.Body, hidden...)
		if !strings.Contains(m.Body, "client balance: 500.00") || !strings.Contains(m.Body, "2026-0001") {
			t.Fatalf("%s email lost the visible details: %s", addr, m.Body)
		}
	}
	if m := got["ada.admin@example.test"]; !strings.Contains(m.Body, "client balance: -3376.53") {
		t.Fatalf("admin on every wall email %s", m.Body)
	}

	// Resolving returns the visible total too.
	code, body, _ = attorney.do("POST", "/api/v1/trust-flags/"+clientFlag+"/resolve", map[string]string{"note": "client funds wired"})
	if code != http.StatusOK {
		t.Fatalf("resolve %d %s", code, body)
	}
	leaks("resolve result", string(body), hidden...)

	// A reversal of the M1 deposit: M1 -499.00, client -4375.53 overall.
	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger/"+string(dep.Entry.ID)+"/reversals", map[string]string{"reason": "bounced"})
	var rev PostResult
	if code != http.StatusCreated || json.Unmarshal(body, &rev) != nil {
		t.Fatalf("reversal %d %s", code, body)
	}
	if rev.Balances != (Balances{MatterCents: 49900, ClientCents: 49900, AccountCents: 49900}) {
		t.Fatalf("reversal balances %+v", rev.Balances)
	}
	leaks("reversal result", string(body), "337753", "3377.53")

	// GET /api/v1/trust/balances already hid M2; it agrees with the post results.
	code, body, _ = staff.do("GET", "/api/v1/trust/balances", nil)
	leaks("balances", string(body), "337753", "387653")
	if code != http.StatusOK {
		t.Fatalf("balances %d %s", code, body)
	}
}

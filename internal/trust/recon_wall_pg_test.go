package trust

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Opus checkpoint, P1 reconcile.go: a reconciliation's ledger and client totals count
// every matter in the account. A walled matter's money must not show through them, so a
// caller outside that wall cannot create, get, list, report or page a reconciliation
// whose totals count it. A reader on every wall still sees the full bank-account totals,
// and an account with no walled rows stays open to every trust reader.
func TestPGReconciliationUnderWall(t *testing.T) {
	e := newEnv(t)
	m2 := e.newMatter("2026-0002", "Walled Synthetic", "hourly", 0, e.clientID)
	e.wall(m2.ID, e.other.UserID, e.admin.UserID)
	e.post(e.staff, e.in(TypeDeposit, 10000))
	walled := e.in(TypeDeposit, 512347)
	walled.MatterID = m2.ID
	e.post(e.other, walled)

	// A second account holds only the open matter's money.
	open, err := e.svc.CreateAccount(e.ctx, e.admin, AccountInput{Bank: "Synthetic Bank", Name: "Second IOLTA", IOLTA: true, LastFour: "5151"}, "")
	if err != nil {
		t.Fatal(err)
	}
	onOpen := e.in(TypeDeposit, 777)
	onOpen.TrustAccountID = open.ID
	e.post(e.staff, onOpen)

	today := e.svc.now().Format("2006-01-02")
	srv := e.server(t)
	staff, attorney, accountant, admin := e.as(t, srv, "staff"), e.as(t, srv, "attorney"), e.as(t, srv, "accountant"), e.as(t, srv, "admin")
	outside := []caller{staff, attorney, accountant}
	hidden := []string{"522347", "5223.47", "512347", "5123.47"}
	leaks := func(where, body string) {
		t.Helper()
		for _, b := range hidden {
			if strings.Contains(body, b) {
				t.Fatalf("%s leaks the walled matter (%q): %s", where, b, body)
			}
		}
	}
	recon := func(account string, bal int64) map[string]any {
		return map[string]any{"trust_account_id": account, "statement_on": today, "statement_balance_cents": bal}
	}

	// Create: outside the wall is not_found, single and bulk, and stores nothing.
	before := e.count("trust_reconciliations")
	for _, c := range []caller{staff, attorney} {
		if code, body, _ := c.do("POST", "/api/v1/trust-reconciliations", recon(string(e.account), 0)); code != http.StatusNotFound {
			t.Fatalf("%s create on the walled account: %d %s", c.as, code, body)
		} else {
			leaks(c.as+" create", string(body))
		}
		if code, body, _ := c.do("POST", "/api/v1/trust-reconciliations:bulk", map[string]any{"items": []any{recon(string(e.account), 0)}}); code != http.StatusNotFound {
			t.Fatalf("%s bulk create on the walled account: %d %s", c.as, code, body)
		}
	}
	if e.count("trust_reconciliations") != before {
		t.Fatal("a refused reconciliation was stored")
	}

	// The admin is on every wall and gets the full bank-account totals.
	code, body, _ := admin.do("POST", "/api/v1/trust-reconciliations", recon(string(e.account), 522347))
	var full Reconciliation
	if code != http.StatusCreated || json.Unmarshal(body, &full) != nil || full.LedgerTotalCents != 522347 || full.StatementDifferenceCents != 0 {
		t.Fatalf("admin create %d %s", code, body)
	}
	// Staff may still reconcile the account with no walled rows.
	code, body, _ = staff.do("POST", "/api/v1/trust-reconciliations", recon(string(open.ID), 777))
	var mine Reconciliation
	if code != http.StatusCreated || json.Unmarshal(body, &mine) != nil || mine.LedgerTotalCents != 777 {
		t.Fatalf("staff create on the open account %d %s", code, body)
	}

	for _, c := range outside {
		if code, body, _ := c.do("GET", "/api/v1/trust-reconciliations/"+string(full.ID), nil); code != http.StatusNotFound {
			t.Fatalf("%s get: %d %s", c.as, code, body)
		}
		for _, path := range []string{"/api/v1/trust-reconciliations", "/api/v1/reports/reconciliations", "/trust/reconciliation"} {
			code, body, _ := c.do("GET", path, nil)
			if code != http.StatusOK {
				t.Fatalf("%s as %s: %d %s", path, c.as, code, body)
			}
			leaks(path+" as "+c.as, string(body))
			if !strings.Contains(string(body), string(mine.ID)) && path != "/trust/reconciliation" {
				t.Fatalf("%s as %s lost the open account's reconciliation: %s", path, c.as, body)
			}
			if strings.Contains(string(body), string(full.ID)) {
				t.Fatalf("%s as %s lists the walled account's reconciliation: %s", path, c.as, body)
			}
		}
	}
	if code, body, _ := staff.do("GET", "/trust/reconciliation", nil); !strings.Contains(string(body), "7.77") {
		t.Fatalf("staff page lost the open account's reconciliation: %d %s", code, body)
	}
	if code, body, _ := admin.do("GET", "/api/v1/trust-reconciliations/"+string(full.ID), nil); code != http.StatusOK || !strings.Contains(string(body), "522347") {
		t.Fatalf("admin get %d %s", code, body)
	}
	if code, body, _ := admin.do("GET", "/trust/reconciliation", nil); code != http.StatusOK || !strings.Contains(string(body), "5223.47") {
		t.Fatalf("admin page %d %s", code, body)
	}
}

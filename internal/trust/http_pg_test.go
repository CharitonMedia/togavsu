package trust

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (e *env) server(t *testing.T) *httptest.Server {
	t.Helper()
	people := map[string]domain.Principal{"admin": e.admin, "attorney": e.attorney, "staff": e.staff, "accountant": e.accountant, "client": e.client, "other": e.other}
	h, err := NewHandler(e.svc, os.DirFS("../../web/templates/trust"), func(r *http.Request) (domain.Principal, error) {
		p, ok := people[r.Header.Get("X-Test-As")]
		if !ok {
			return domain.Principal{}, domain.Unauthenticated("sign in is required")
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

type caller struct {
	t   *testing.T
	srv *httptest.Server
	as  string
	c   *http.Client
}

func (e *env) as(t *testing.T, srv *httptest.Server, who string) caller {
	jar, _ := cookiejar.New(nil)
	return caller{t: t, srv: srv, as: who, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c caller) do(method, path string, body any, hdr ...string) (int, []byte, http.Header) {
	c.t.Helper()
	var rd io.Reader
	ctype := "application/json"
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.srv.URL+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("X-Test-As", c.as)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out, res.Header
}

// TestPGHTTPContractRoutes drives the JSON routes the way the API and MCP do.
func TestPGHTTPContractRoutes(t *testing.T) {
	e := newEnv(t)
	srv := e.server(t)
	staff, attorney, accountant := e.as(t, srv, "staff"), e.as(t, srv, "attorney"), e.as(t, srv, "accountant")

	code, body, _ := staff.do("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 5000), "Idempotency-Key", "dep-1")
	if code != http.StatusCreated {
		t.Fatalf("deposit %d %s", code, body)
	}
	var dep PostResult
	_ = json.Unmarshal(body, &dep)
	code, again, _ := staff.do("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 5000), "Idempotency-Key", "dep-1")
	if code != http.StatusCreated || !bytes.Equal(again, body) {
		t.Fatalf("replay %d %s", code, again)
	}
	code, body, _ = staff.do("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 100))
	if code != http.StatusForbidden {
		t.Fatalf("staff withdrawal %d %s", code, body)
	}
	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 7500))
	if code != http.StatusCreated {
		t.Fatalf("below zero %d %s", code, body)
	}
	var wd struct {
		Entry    map[string]any `json:"entry"`
		Balances Balances       `json:"balances"`
		Flags    []map[string]any
	}
	if err := json.Unmarshal(body, &wd); err != nil || wd.Balances.MatterCents != -2500 || len(wd.Flags) != 3 || wd.Entry["amount_cents"].(float64) != 7500 {
		t.Fatalf("below zero body %s", body)
	}
	if wd.Flags[0]["scope"] != "matter" || wd.Flags[0]["status"] != "open" || wd.Flags[0]["resolved_at"] != nil {
		t.Fatalf("flag json %v", wd.Flags[0])
	}
	// No route edits or deletes a ledger row.
	id := wd.Entry["id"].(string)
	for _, m := range []string{"PUT", "PATCH", "DELETE"} {
		if code, _, _ := attorney.do(m, "/api/v1/trust-ledger/"+id, map[string]any{"amount_cents": 1}); code != http.StatusMethodNotAllowed {
			t.Fatalf("%s ledger row: %d", m, code)
		}
	}
	if code, _, _ := attorney.do("DELETE", "/api/v1/trust-flags/"+wd.Flags[0]["id"].(string), nil); code != http.StatusMethodNotAllowed {
		t.Fatalf("delete flag: %d", code)
	}
	// Unknown fields are rejected, as the contract's additionalProperties false says.
	if code, body, _ := attorney.do("POST", "/api/v1/trust-ledger", map[string]any{"entry_type": "deposit", "balance_override": 1}); code != http.StatusBadRequest {
		t.Fatalf("unknown field %d %s", code, body)
	}
	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger/"+id+"/reversals", map[string]string{"reason": "wrong matter"})
	if code != http.StatusCreated || !strings.Contains(string(body), `"entry_type":"reversal"`) || !strings.Contains(string(body), `"reverses_entry_id":"`+id+`"`) {
		t.Fatalf("reversal %d %s", code, body)
	}
	if code, _, _ := attorney.do("POST", "/api/v1/trust-ledger/"+id+"/reversals", map[string]string{"reason": "again"}); code != http.StatusConflict {
		t.Fatalf("second reversal %d", code)
	}
	// The accountant reads everything and writes nothing.
	code, body, _ = accountant.do("GET", "/api/v1/trust-ledger?limit=2", nil)
	var page struct {
		Data []Entry `json:"data"`
		Page struct {
			Limit      int     `json:"limit"`
			NextCursor *string `json:"next_cursor"`
		} `json:"page"`
	}
	if err := json.Unmarshal(body, &page); code != 200 || err != nil || len(page.Data) != 2 || page.Page.NextCursor == nil {
		t.Fatalf("accountant list %d %s", code, body)
	}
	code, body, _ = accountant.do("GET", "/api/v1/trust-ledger?cursor="+*page.Page.NextCursor, nil)
	if err := json.Unmarshal(body, &page); code != 200 || err != nil || len(page.Data) != 1 || page.Page.NextCursor != nil {
		t.Fatalf("second page %d %s", code, body)
	}
	if code, _, _ := accountant.do("GET", "/api/v1/trust-ledger?limit=201", nil); code != http.StatusBadRequest {
		t.Fatalf("limit above api.max_page_size: %d", code)
	}
	for _, path := range []string{"/api/v1/trust/balances", "/api/v1/trust-flags?status=open", "/api/v1/trust-accounts", "/api/v1/trust-reconciliations",
		"/api/v1/trust-replenishment-requests", "/api/v1/widgets/trust-alerts", "/api/v1/widgets/replenishment-requests",
		"/api/v1/reports/trust-balances", "/api/v1/reports/reconciliations", "/api/v1/reports/trust-client-ledger?client_id=" + string(e.clientID),
		"/api/v1/reports/trust-account-ledger?trust_account_id=" + string(e.account)} {
		if code, body, _ := accountant.do("GET", path, nil); code != http.StatusOK {
			t.Fatalf("accountant GET %s: %d %s", path, code, body)
		}
	}
	bal := int64(0)
	for path, in := range map[string]any{
		"/api/v1/trust-ledger":                                           e.in(TypeDeposit, 1),
		"/api/v1/trust-ledger:bulk":                                      map[string]any{"items": []PostInput{e.in(TypeDeposit, 1)}},
		"/api/v1/trust-ledger/" + id + "/reversals":                      map[string]string{"reason": "x"},
		"/api/v1/trust-reconciliations":                                  ReconcileInput{TrustAccountID: e.account, StatementOn: "2026-10-31", StatementBalanceCents: &bal},
		"/api/v1/trust-accounts":                                         AccountInput{Bank: "B", Name: "N", LastFour: "1234"},
		"/api/v1/trust-flags/" + wd.Flags[1]["id"].(string) + "/resolve": map[string]string{"note": "x"},
	} {
		if code, body, _ := accountant.do("POST", path, in); code != http.StatusForbidden {
			t.Fatalf("accountant POST %s: %d %s", path, code, body)
		}
	}
	code, body, h := accountant.do("GET", "/api/v1/reports/trust-client-ledger?format=csv&client_id="+string(e.clientID), nil)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || !strings.HasPrefix(string(body), "date,entry_id,") {
		t.Fatalf("csv %d %s %s", code, h.Get("Content-Type"), body)
	}
	// Notifications: the attorney's bell, then Mark all read.
	code, body, _ = attorney.do("GET", "/api/v1/notifications?read=false", nil)
	if code != 200 || strings.Count(string(body), `"kind":"trust_flag"`) != 1 {
		t.Fatalf("attorney unread %d %s", code, body)
	}
	if code, _, _ := attorney.do("POST", "/api/v1/notifications:read-all", nil); code != http.StatusNoContent {
		t.Fatalf("read-all %d", code)
	}
	code, body, _ = attorney.do("GET", "/api/v1/notifications?read=false", nil)
	if code != 200 || !strings.Contains(string(body), `"data":[]`) {
		t.Fatalf("after read-all %d %s", code, body)
	}
	if code, _, _ := e.as(t, srv, "nobody").do("GET", "/api/v1/trust-ledger", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous %d", code)
	}
	if code, _, _ := e.as(t, srv, "client").do("GET", "/api/v1/trust-ledger", nil); code != http.StatusNotFound {
		t.Fatalf("client %d", code)
	}
}

// TestPGPagesAndForms renders every trust page and drives the forms.
func TestPGPagesAndForms(t *testing.T) {
	e := newEnv(t)
	srv := e.server(t)
	fee := e.newMatter("2026-0020", "Flat fee will", "flat", 90000, e.clientID)
	admin, staff, attorney := e.as(t, srv, "admin"), e.as(t, srv, "staff"), e.as(t, srv, "attorney")
	form := url.Values{"trust_account_id": {string(e.account)}, "matter_id": {string(fee.ID)}, "entry_type": {"deposit"}, "amount": {"900.00"}, "method": {"check"}, "description": {"Retainer for will"}}
	code, _, h := staff.do("POST", "/trust/ledger", form)
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "notice=Posted") {
		t.Fatalf("deposit form %d %v", code, h)
	}
	code, body, _ := attorney.do("GET", "/trust/matters/"+string(fee.ID), nil)
	if code != 200 || !strings.Contains(string(body), ">Ready to apply<") || !strings.Contains(string(body), "Apply flat fee") || !strings.Contains(string(body), `value="900.00"`) {
		t.Fatalf("matter page %d %s", code, body)
	}
	code, _, h = staff.do("POST", "/trust/matters/"+string(fee.ID)+"/flat-fee", url.Values{"amount": {"900.00"}})
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "error=") {
		t.Fatalf("staff flat fee %d %v", code, h)
	}
	code, _, h = attorney.do("POST", "/trust/matters/"+string(fee.ID)+"/flat-fee", url.Values{"amount": {"900.00"}})
	if code != http.StatusSeeOther || strings.Contains(h.Get("Location"), "error=") {
		t.Fatalf("attorney flat fee %d %v", code, h)
	}
	if _, body, _ = attorney.do("GET", "/trust/matters/"+string(fee.ID), nil); !strings.Contains(string(body), ">Collected<") {
		t.Fatalf("not collected: %s", body)
	}
	// Below zero from the form: posts with the warning.
	form.Set("entry_type", "withdrawal")
	form.Set("amount", "10.00")
	code, _, h = attorney.do("POST", "/trust/ledger", form)
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "below+zero") {
		t.Fatalf("below zero form %d %v", code, h)
	}
	code, _, h = attorney.do("POST", "/trust/reconciliation", url.Values{"trust_account_id": {string(e.account)}, "statement_on": {e.svc.now().Format("2006-01-02")}, "statement_balance": {"0.00"}})
	if code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "Difference") || !strings.Contains(h.Get("Location"), "10.00") {
		t.Fatalf("reconcile form %d %v", code, h)
	}
	if code, _, _ := staff.do("POST", "/trust/matters/"+string(e.matter.ID)+"/evergreen", url.Values{"minimum": {"250.00"}}); code != http.StatusSeeOther {
		t.Fatalf("evergreen %d", code)
	}
	for path, want := range map[string]string{
		"/trust/ledger":                         "Post an entry",
		"/trust/balances":                       "Casey Client",
		"/trust/flags?status=open":              "2026-0020",
		"/trust/replenishment":                  "Trust Replenishment",
		"/trust/reconciliation":                 "-10.00",
		"/trust/matters/" + string(e.matter.ID): "250.00",
		"/trust/notifications":                  "Trust below zero",
	} {
		code, body, _ := admin.do("GET", path, nil)
		if code != 200 || !strings.Contains(string(body), want) {
			t.Fatalf("GET %s %d: missing %q in %s", path, code, want, body)
		}
	}
	// The flag page resolves with a note; opening a notification marks it read.
	flags, _ := e.svc.Flags(e.ctx, e.admin, FlagFilter{Status: "open"})
	fid := string(flags.Items[0].ID)
	if code, body, _ := admin.do("GET", "/trust/flags/"+fid, nil); code != 200 || !strings.Contains(string(body), "Resolve flag") {
		t.Fatalf("flag page %d %s", code, body)
	}
	if code, _, _ := admin.do("POST", "/trust/flags/"+fid+"/resolve", url.Values{"note": {"paper deposit"}}); code != http.StatusSeeOther {
		t.Fatalf("resolve form %d", code)
	}
	notes, _ := e.svc.Notifications(e.ctx, e.attorney, nil, "", 0)
	code, _, h = attorney.do("POST", "/trust/notifications/"+string(notes.Items[0].ID)+"/open", nil)
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/trust/flags/") {
		t.Fatalf("open %d %v", code, h)
	}
	if code, _, _ := attorney.do("POST", "/trust/notifications/read-all", nil); code != http.StatusSeeOther {
		t.Fatalf("read all form %d", code)
	}
	if _, body, _ := attorney.do("GET", "/trust/ledger", nil); !strings.Contains(string(body), `Notifications, 0 unread`) || strings.Contains(string(body), `class="count"`) {
		t.Fatalf("bell not cleared: %s", body)
	}
	// A client and an anonymous caller get the error page, not trust data.
	if code, body, _ := e.as(t, srv, "client").do("GET", "/trust/ledger", nil); code != http.StatusNotFound || strings.Contains(string(body), "Casey") {
		t.Fatalf("client page %d", code)
	}
	if code, _, _ := e.as(t, srv, "nobody").do("GET", "/trust/balances", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous page %d", code)
	}
	if code, _, _ := e.as(t, srv, "nobody").do("GET", "/trust/static/trust.css", nil); code != 200 {
		t.Fatalf("css %d", code)
	}
}

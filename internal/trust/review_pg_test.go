package trust

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// flagShapes reads the contract's per-scope TrustFlag variants: scope -> allowed
// properties and required properties.
func flagShapes(t *testing.T) map[string][2]map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas struct {
				TrustFlag struct {
					OneOf []struct {
						AdditionalProperties *bool `json:"additionalProperties"`
						Properties           map[string]struct {
							Const string `json:"const"`
						} `json:"properties"`
						Required []string `json:"required"`
					} `json:"oneOf"`
				} `json:"TrustFlag"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][2]map[string]bool{}
	for _, v := range doc.Components.Schemas.TrustFlag.OneOf {
		if v.AdditionalProperties == nil || *v.AdditionalProperties {
			t.Fatal("contract TrustFlag variant is not additionalProperties false")
		}
		allowed, required := map[string]bool{}, map[string]bool{}
		for k := range v.Properties {
			allowed[k] = true
		}
		for _, k := range v.Required {
			required[k] = true
		}
		out[v.Properties["scope"].Const] = [2]map[string]bool{allowed, required}
	}
	if len(out) != 3 || out[ScopeMatter][0] == nil || out[ScopeClient][0] == nil || out[ScopeAccount][0] == nil {
		t.Fatalf("contract TrustFlag scopes %v", out)
	}
	return out
}

// checkFlag fails unless one flag's JSON object matches the contract variant for its scope.
func checkFlag(t *testing.T, where string, shapes map[string][2]map[string]bool, f map[string]any) {
	t.Helper()
	scope, _ := f["scope"].(string)
	shape, ok := shapes[scope]
	if !ok {
		t.Fatalf("%s: flag scope %q not in contract: %v", where, scope, f)
	}
	for k := range f {
		if !shape[0][k] {
			t.Fatalf("%s: %s-scope flag has %q, which the contract does not allow: %v", where, scope, k, f)
		}
	}
	for k := range shape[1] {
		if _, ok := f[k]; !ok {
			t.Fatalf("%s: %s-scope flag lacks required %q: %v", where, scope, k, f)
		}
	}
}

// Codex P1: a matter-scope flag must not serialize trust_account_id, on the post result,
// the bulk and reversal results, the flag list and the flag detail. The account filter
// still finds every scope's flag, because the account id is kept internally.
func TestPGFlagJSONMatchesContractShapes(t *testing.T) {
	e := newEnv(t)
	shapes := flagShapes(t)
	srv := e.server(t)
	attorney := e.as(t, srv, "attorney")

	code, body, _ := attorney.do("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 700))
	if code != http.StatusCreated {
		t.Fatalf("post %d %s", code, body)
	}
	var post struct {
		Entry map[string]any   `json:"entry"`
		Flags []map[string]any `json:"flags"`
	}
	if err := json.Unmarshal(body, &post); err != nil || len(post.Flags) != 3 {
		t.Fatalf("post body %s", body)
	}
	seen := map[string]bool{}
	for _, f := range post.Flags {
		checkFlag(t, "post", shapes, f)
		seen[f["scope"].(string)] = true
	}
	if !seen[ScopeMatter] || !seen[ScopeClient] || !seen[ScopeAccount] {
		t.Fatalf("scopes %v", seen)
	}
	for _, f := range post.Flags {
		_, hasAcct := f["trust_account_id"]
		if hasAcct != (f["scope"] == ScopeAccount) {
			t.Fatalf("trust_account_id on %s flag: %v", f["scope"], f)
		}
	}

	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []PostInput{e.in(TypeWithdrawal, 1)}})
	var bulk struct {
		Data []struct {
			Flags []map[string]any `json:"flags"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &bulk); code != http.StatusCreated || err != nil || len(bulk.Data) != 1 || len(bulk.Data[0].Flags) != 3 {
		t.Fatalf("bulk %d %s", code, body)
	}
	for _, f := range bulk.Data[0].Flags {
		checkFlag(t, "bulk", shapes, f)
	}

	// A deposit, then its reversal: the reversal leaves the balance below zero again.
	dep := e.post(e.attorney, e.in(TypeDeposit, 2000))
	code, body, _ = attorney.do("POST", "/api/v1/trust-ledger/"+string(dep.Entry.ID)+"/reversals", map[string]string{"reason": "bounced"})
	var rev struct {
		Flags []map[string]any `json:"flags"`
	}
	if err := json.Unmarshal(body, &rev); code != http.StatusCreated || err != nil || len(rev.Flags) != 3 {
		t.Fatalf("reversal %d %s", code, body)
	}
	for _, f := range rev.Flags {
		checkFlag(t, "reversal", shapes, f)
	}

	code, body, _ = attorney.do("GET", "/api/v1/trust-flags", nil)
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || len(list.Data) != 9 {
		t.Fatalf("flag list %d %s", code, body)
	}
	for _, f := range list.Data {
		checkFlag(t, "list", shapes, f)
		code, one, _ := attorney.do("GET", "/api/v1/trust-flags/"+f["id"].(string), nil)
		var got map[string]any
		if err := json.Unmarshal(one, &got); code != 200 || err != nil {
			t.Fatalf("flag detail %d %s", code, one)
		}
		checkFlag(t, "detail", shapes, got)
	}

	// The trust_account_id filter still selects matter and client flags by their posting.
	code, body, _ = attorney.do("GET", "/api/v1/trust-flags?trust_account_id="+string(e.account), nil)
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || len(list.Data) != 9 {
		t.Fatalf("account filter %d %s", code, body)
	}
	other, err := e.svc.CreateAccount(e.ctx, e.admin, AccountInput{Bank: "Synthetic Bank", Name: "Second", LastFour: "1111"}, "")
	if err != nil {
		t.Fatal(err)
	}
	code, body, _ = attorney.do("GET", "/api/v1/trust-flags?trust_account_id="+string(other.ID), nil)
	if err := json.Unmarshal(body, &list); code != 200 || err != nil || len(list.Data) != 0 {
		t.Fatalf("other account filter %d %s", code, body)
	}
	// The stored row still carries the account for every scope.
	var withAcct int
	if err := e.db.QueryRowContext(e.ctx, `SELECT COUNT(*) FROM trust_flags WHERE trust_account_id = $1`, string(e.account)).Scan(&withAcct); err != nil || withAcct != 9 {
		t.Fatalf("stored account on flags %d %v", withAcct, err)
	}
}

// Codex P2: TrustEntryWrite allows reverses_entry_id as null or a uuid. Null and absent
// post; a uuid is a validation error naming the reversals route, and posts nothing.
func TestPGPostAcceptsNullReversesEntryID(t *testing.T) {
	e := newEnv(t)
	srv := e.server(t)
	staff := e.as(t, srv, "staff")
	body := func(rev any) map[string]any {
		return map[string]any{"trust_account_id": e.account, "client_id": e.clientID, "matter_id": e.matter.ID, "entry_type": "deposit",
			"amount_cents": 100, "method": "check", "description": "synthetic", "reverses_entry_id": rev}
	}
	if code, out, _ := staff.do("POST", "/api/v1/trust-ledger", body(nil)); code != http.StatusCreated || !strings.Contains(string(out), `"reverses_entry_id":null`) {
		t.Fatalf("null reverses_entry_id %d %s", code, out)
	}
	absent := body(nil)
	delete(absent, "reverses_entry_id")
	if code, out, _ := staff.do("POST", "/api/v1/trust-ledger", absent); code != http.StatusCreated {
		t.Fatalf("absent reverses_entry_id %d %s", code, out)
	}
	if code, out, _ := staff.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []any{body(nil)}}); code != http.StatusCreated {
		t.Fatalf("bulk null reverses_entry_id %d %s", code, out)
	}
	before := e.count("trust_ledger")
	status, out, _ := staff.do("POST", "/api/v1/trust-ledger", body(string(mustID(t))))
	var api domain.APIError
	if err := json.Unmarshal(out, &api); status != http.StatusBadRequest || err != nil || api.Field != "reverses_entry_id" || !strings.Contains(api.Message, "/reversals") {
		t.Fatalf("uuid reverses_entry_id %d %s", status, out)
	}
	if code, out, _ := staff.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []any{body(nil), body(string(mustID(t)))}}); code != http.StatusBadRequest {
		t.Fatalf("bulk uuid reverses_entry_id %d %s", code, out)
	}
	if e.count("trust_ledger") != before {
		t.Fatal("a rejected reverses_entry_id posted a row")
	}
	if _, err := e.svc.Post(e.ctx, e.staff, PostInput{TrustAccountID: e.account, ClientID: e.clientID, MatterID: e.matter.ID, EntryType: TypeDeposit,
		AmountCents: 1, Method: "check", Description: "x", ReversesEntryID: &e.account}, ""); code(err) != domain.CodeValidation {
		t.Fatalf("service reverses_entry_id: %v", err)
	}
}

// Codex P2: postTrustEntry and its bulk form check token scopes per entry_type through
// Handler.Scopes. The hook here behaves as identity.Caller.RequireScopes does: a session
// passes, a token needs every scope named and is scope_denied otherwise.
func TestPGEntryTypeScopes(t *testing.T) {
	e := newEnv(t)
	people := map[string]domain.Principal{"attorney": e.attorney, "staff": e.staff}
	h, err := NewHandler(e.svc, nil, func(r *http.Request) (domain.Principal, error) {
		p, ok := people[r.Header.Get("X-Test-As")]
		if !ok {
			return domain.Principal{}, domain.Unauthenticated("sign in is required")
		}
		if r.Header.Get("X-Test-Scopes") != "" {
			p.Source = domain.SourceAPIToken
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var asked [][]string
	h.Scopes = func(r *http.Request, need ...string) error {
		asked = append(asked, need)
		raw := r.Header.Get("X-Test-Scopes")
		if raw == "" {
			return nil
		}
		have := map[string]bool{}
		for _, s := range strings.Split(raw, " ") {
			have[s] = true
		}
		for _, s := range need {
			if !have[s] {
				return &domain.APIError{Code: "scope_denied", Message: "token is missing a scope"}
			}
		}
		return nil
	}
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	tok := func(who, scopes string) func(string, string, any) (int, []byte) {
		c := e.as(t, srv, who)
		return func(m, path string, body any) (int, []byte) {
			code, out, _ := c.do(m, path, body, "X-Test-Scopes", scopes)
			return code, out
		}
	}

	write := tok("attorney", "trust:write")
	if code, out := write("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 1000)); code != http.StatusCreated {
		t.Fatalf("trust:write deposit %d %s", code, out)
	}
	for _, typ := range []string{TypeWithdrawal, TypeTransfer, TypeRefund, TypeAdjustment} {
		in := e.in(typ, 10)
		if typ == TypeAdjustment {
			in.AdjustmentDirection = "credit"
		}
		code, out := write("POST", "/api/v1/trust-ledger", in)
		if code != http.StatusForbidden || !strings.Contains(string(out), "scope") {
			t.Fatalf("trust:write %s %d %s", typ, code, out)
		}
	}
	before := e.count("trust_ledger")
	if code, out := write("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []PostInput{e.in(TypeDeposit, 1), e.in(TypeWithdrawal, 1)}}); code != http.StatusForbidden {
		t.Fatalf("trust:write bulk with a withdrawal %d %s", code, out)
	}
	if e.count("trust_ledger") != before {
		t.Fatal("a scope-denied batch posted")
	}
	// The scope check comes before the post, so a denied token raises no event or flag.
	e.events.reset()
	if code, _ := write("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 99999)); code != http.StatusForbidden || len(e.events.names()) != 0 {
		t.Fatalf("denied post emitted %v", e.events.names())
	}

	withdraw := tok("attorney", "trust:withdraw")
	if code, out := withdraw("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 10)); code != http.StatusCreated {
		t.Fatalf("trust:withdraw withdrawal %d %s", code, out)
	}
	if code, out := withdraw("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 10)); code != http.StatusForbidden {
		t.Fatalf("trust:withdraw-only deposit %d %s", code, out)
	}
	both := tok("attorney", "trust:write trust:withdraw")
	if code, out := both("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []PostInput{e.in(TypeDeposit, 5), e.in(TypeWithdrawal, 5)}}); code != http.StatusCreated {
		t.Fatalf("both scopes bulk %d %s", code, out)
	}
	// A staff token with trust:write posts a deposit (staff role defaults lack trust:withdraw).
	if code, out := tok("staff", "trust:write")("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 10)); code != http.StatusCreated {
		t.Fatalf("staff trust:write deposit %d %s", code, out)
	}
	// Scopes do not replace roles: staff holding trust:withdraw is still forbidden by role.
	if code, out := tok("staff", "trust:write trust:withdraw")("POST", "/api/v1/trust-ledger", e.in(TypeWithdrawal, 1)); code != http.StatusForbidden || strings.Contains(string(out), "scope") {
		t.Fatalf("staff withdrawal by role %d %s", code, out)
	}
	// A session caller passes the hook; the hook is asked for the deposit's scope only.
	asked = nil
	if code, out := tok("staff", "")("POST", "/api/v1/trust-ledger", e.in(TypeDeposit, 10)); code != http.StatusCreated || len(asked) != 1 || len(asked[0]) != 1 || asked[0][0] != "trust:write" {
		t.Fatalf("session deposit %d %s asked %v", code, out, asked)
	}
	// An unknown entry_type asks for no scope and is the service's validation error.
	asked = nil
	if code, out := write("POST", "/api/v1/trust-ledger", e.in("bogus", 1)); code != http.StatusBadRequest || len(asked) != 0 {
		t.Fatalf("unknown entry type %d %s asked %v", code, out, asked)
	}
}

// Codex P2: a deactivated responsible attorney gets neither the in-app notification nor
// the email; the active admin still gets both.
func TestPGInactiveAttorneyNotNotified(t *testing.T) {
	e := newEnv(t)
	u, err := e.users.Get(e.ctx, e.attorney.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Active = false
	if err := e.users.Update(e.ctx, u); err != nil {
		t.Fatal(err)
	}
	r := e.post(e.admin, e.in(TypeWithdrawal, 500))
	if len(r.Flags) != 3 {
		t.Fatalf("flags %+v", r.Flags)
	}
	mail, err := e.svc.Outbox(e.ctx, r.Flags[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(mail) != 1 || !strings.EqualFold(mail[0].To, "ada.admin@example.test") {
		t.Fatalf("mail %+v", mail)
	}
	if n, err := e.svc.UnreadCount(e.ctx, e.attorney); err != nil || n != 0 {
		t.Fatalf("inactive attorney unread %d %v", n, err)
	}
	if n, err := e.svc.UnreadCount(e.ctx, e.admin); err != nil || n != 1 {
		t.Fatalf("admin unread %d %v", n, err)
	}
}

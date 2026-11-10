package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

type api struct {
	f   *fx
	srv http.Handler
	who map[domain.ID]domain.Principal
}

func newAPI(t *testing.T, f *fx) *api {
	t.Helper()
	a := &api{f: f, who: map[domain.ID]domain.Principal{}}
	for _, p := range []domain.Principal{f.admin, f.attorney, f.staff, f.staff2, f.accountant} {
		a.who[p.UserID] = p
	}
	h, err := NewHandler(f.svc, os.DirFS("../../web/templates/billing"), func(r *http.Request) (domain.Principal, error) {
		p, ok := a.who[domain.ID(r.Header.Get("X-Test-User"))]
		if !ok {
			return domain.Principal{}, domain.Unauthenticated("sign in required")
		}
		return p, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.srv = h.Routes()
	return a
}

const csrfToken = "0123456789abcdef0123456789abcdef0123"

func (a *api) do(p domain.Principal, method, path string, body any, hdr ...string) (int, []byte, http.Header) {
	a.f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			a.f.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Test-User", string(p.UserID))
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes(), rec.Header()
}

func (a *api) form(p domain.Principal, path string, v url.Values, withCSRF bool) (int, string) {
	if withCSRF {
		v.Set("csrf", csrfToken)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Test-User", string(p.UserID))
	req.AddCookie(&http.Cookie{Name: practice.CSRFCookie, Value: csrfToken})
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location")
}

func (a *api) page(p domain.Principal, path string) (int, string) {
	code, body, _ := a.do(p, http.MethodGet, path, nil)
	return code, string(body)
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

// contract checks a JSON value against a component schema of the published OpenAPI document:
// types, enums, const, required keys, and no keys outside a closed object.
type contract struct{ schemas map[string]any }

func loadContract(t *testing.T) contract {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return contract{doc.Components.Schemas}
}

func (c contract) check(t *testing.T, name string, v any) {
	t.Helper()
	if err := c.valid(c.schemas[name], v, name); err != nil {
		b, _ := json.Marshal(v)
		t.Fatalf("%s does not match the contract: %v\n%s", name, err, b)
	}
}

func (c contract) valid(s any, v any, path string) error {
	sch, ok := s.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := sch["$ref"].(string); ok {
		return c.valid(c.schemas[strings.TrimPrefix(ref, "#/components/schemas/")], v, path)
	}
	for _, k := range []string{"anyOf", "oneOf"} {
		if alts, ok := sch[k].([]any); ok {
			var errs []string
			for _, alt := range alts {
				if err := c.valid(alt, v, path); err == nil {
					return nil
				} else {
					errs = append(errs, err.Error())
				}
			}
			return fmt.Errorf("%s matches no %s: %s", path, k, strings.Join(errs, "; "))
		}
	}
	if cv, ok := sch["const"]; ok && cv != v {
		return fmt.Errorf("%s: want const %v got %v", path, cv, v)
	}
	if enum, ok := sch["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || e == v
		}
		if !found {
			return fmt.Errorf("%s: %v not in %v", path, v, enum)
		}
	}
	switch sch["type"] {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: want object", path)
		}
		props, _ := sch["properties"].(map[string]any)
		for _, r := range asList(sch["required"]) {
			if _, ok := m[r.(string)]; !ok {
				return fmt.Errorf("%s: missing %s", path, r)
			}
		}
		for k, val := range m {
			ps, ok := props[k]
			if !ok {
				if sch["additionalProperties"] == false {
					return fmt.Errorf("%s: unexpected %s", path, k)
				}
				continue
			}
			if err := c.valid(ps, val, path+"."+k); err != nil {
				return err
			}
		}
	case "array":
		l, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: want array", path)
		}
		for i, it := range l {
			if err := c.valid(sch["items"], it, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s: want string got %v", path, v)
		}
	case "integer":
		n, ok := v.(float64)
		if !ok || n != math.Trunc(n) {
			return fmt.Errorf("%s: want integer got %v", path, v)
		}
		if min, ok := sch["minimum"].(float64); ok && n < min {
			return fmt.Errorf("%s: %v below minimum", path, n)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: want boolean", path)
		}
	case "null":
		if v != nil {
			return fmt.Errorf("%s: want null", path)
		}
	}
	return nil
}

func asList(v any) []any { l, _ := v.([]any); return l }

func TestAPIInvoiceFlowMatchesContract(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	c := loadContract(t)
	m, client := f.matter("API matter", "hourly", 0, 0)

	code, body, _ := a.do(f.staff, "POST", "/api/v1/time-entries", map[string]any{
		"matter_id": m.ID, "worked_on": "2026-06-01", "increments": 5, "description": "Research", "activity_type_id": f.activity})
	if code != 201 {
		t.Fatalf("create time %d %s", code, body)
	}
	te := decodeMap(t, body)
	c.check(t, "TimeEntry", te)
	if te["rate_cents"] != float64(90_00) || te["invoice_id"] != nil {
		t.Fatalf("time entry: %v", te)
	}
	code, body, _ = a.do(f.staff, "POST", "/api/v1/time-entries", map[string]any{"matter_id": m.ID, "unknown": 1})
	if code != 400 {
		t.Fatalf("unknown field must be refused: %d", code)
	}
	code, body, _ = a.do(f.staff, "POST", "/api/v1/expenses", map[string]any{
		"matter_id": m.ID, "incurred_on": "2026-06-02", "description": "Copies", "amount_cents": 12_34, "expense_type_id": f.expType})
	if code != 201 {
		t.Fatalf("expense %d %s", code, body)
	}
	c.check(t, "Expense", decodeMap(t, body))

	code, body, _ = a.do(f.staff, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": m.ID, "from_date": "2026-01-01", "to_date": "2026-12-31"})
	if code != 400 {
		t.Fatalf("two selectors must be refused: %d %s", code, body)
	}
	code, body, _ = a.do(f.staff, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": m.ID})
	if code != 201 {
		t.Fatalf("draft %d %s", code, body)
	}
	data := decodeMap(t, body)["data"].([]any)
	inv := data[0].(map[string]any)
	c.check(t, "Invoice", inv)
	id := inv["id"].(string)
	if inv["subtotal_cents"] != float64(45_00+12_34) {
		t.Fatalf("subtotal %v", inv["subtotal_cents"])
	}
	if code, _, _ := a.do(f.staff, "POST", "/api/v1/invoices/"+id+"/approve", nil); code != 403 {
		t.Fatalf("staff approve: %d", code)
	}
	if code, body, _ := a.do(f.attorney, "POST", "/api/v1/invoices/"+id+"/approve", nil); code != 200 {
		t.Fatalf("approve %d %s", code, body)
	}
	code, body, _ = a.do(f.attorney, "POST", "/api/v1/invoices/"+id+"/send", map[string]any{"deliver": "email"})
	if code != 200 || decodeMap(t, body)["status"] != "sent" {
		t.Fatalf("send %d %s", code, body)
	}
	code, body, hdr := a.do(f.accountant, "GET", "/api/v1/invoices/"+id+"/pdf", nil)
	if code != 200 || hdr.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF")) {
		t.Fatalf("pdf %d", code)
	}
	code, body, _ = a.do(f.staff, "POST", "/api/v1/payments", map[string]any{"contact_id": client, "matter_id": m.ID, "amount_cents": 57_34,
		"destination": "operating", "method": "check", "reference_number": "552", "invoice_id": id})
	if code != 201 {
		t.Fatalf("payment %d %s", code, body)
	}
	pay := decodeMap(t, body)
	c.check(t, "Payment", pay)
	code, body, _ = a.do(f.accountant, "GET", "/api/v1/invoices/"+id, nil)
	got := decodeMap(t, body)
	c.check(t, "Invoice", got)
	if got["status"] != "paid" || got["balance_due_cents"] != float64(0) || got["paid_at"] == nil {
		t.Fatalf("paid invoice %v", got)
	}
	if code, body, _ := a.do(f.attorney, "POST", "/api/v1/payments/"+pay["id"].(string)+"/reverse", map[string]any{"reason": "bounced"}); code != 200 {
		t.Fatalf("reverse %d %s", code, body)
	}
	code, body, _ = a.do(f.attorney, "POST", "/api/v1/invoices/"+id+"/void", map[string]any{"reason": "redo"})
	if code != 200 || decodeMap(t, body)["status"] != "void" {
		t.Fatalf("void %d %s", code, body)
	}
	code, body, _ = a.do(f.accountant, "GET", "/api/v1/time-entries?invoice_id="+id, nil)
	if code != 200 || len(decodeMap(t, body)["data"].([]any)) != 0 {
		t.Fatalf("void must release time: %s", body)
	}
	code, body, _ = a.do(f.accountant, "GET", "/api/v1/invoices/"+id+"/history", nil)
	if code != 200 {
		t.Fatalf("history %d", code)
	}
	for _, row := range decodeMap(t, body)["data"].([]any) {
		c.check(t, "AuditEntry", row)
	}
	code, body, _ = a.do(f.accountant, "GET", "/api/v1/billing/worklist", nil)
	if code != 200 {
		t.Fatalf("worklist %d", code)
	}
	c.check(t, "BillingWorklist", decodeMap(t, body))
	for _, rep := range reportNames {
		code, body, _ := a.do(f.accountant, "GET", "/api/v1/reports/"+rep, nil)
		if code != 200 {
			t.Fatalf("report %s %d", rep, code)
		}
		c.check(t, "ReportTable", decodeMap(t, body))
		code, body, hdr := a.do(f.accountant, "GET", "/api/v1/reports/"+rep+"?format=csv", nil)
		if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") || !bytes.Contains(body, []byte(",")) {
			t.Fatalf("csv %s %d %s", rep, code, body)
		}
	}
}

func TestAPITimersAndFeeItemsMatchContract(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	c := loadContract(t)
	m, _ := f.matter("Timer matter", "hourly", 0, 0)
	code, body, _ := a.do(f.staff, "POST", "/api/v1/timers", map[string]any{"matter_id": m.ID})
	if code != 201 {
		t.Fatalf("timer %d %s", code, body)
	}
	tm := decodeMap(t, body)
	c.check(t, "Timer", tm)
	id := tm["id"].(string)
	code, body, _ = a.do(f.staff, "POST", "/api/v1/timers/"+id+"/pause", nil)
	c.check(t, "Timer", decodeMap(t, body))
	code, body, _ = a.do(f.staff, "POST", "/api/v1/timers", map[string]any{"matter_id": m.ID})
	if code != 200 || decodeMap(t, body)["id"] != id {
		t.Fatalf("start on paused resumes: %d %s", code, body)
	}
	code, body, _ = a.do(f.staff, "GET", "/api/v1/widgets/running-timer", nil)
	c.check(t, "Widget", decodeMap(t, body))
	code, body, _ = a.do(f.staff, "POST", "/api/v1/timers/"+id+"/stop", nil)
	if code != 200 {
		t.Fatalf("stop %d %s", code, body)
	}
	c.check(t, "TimerStopResult", decodeMap(t, body))
	if code, _, _ := a.do(f.staff, "GET", "/api/v1/timers/"+id, nil); code != 404 {
		t.Fatal("a stopped timer is not returned as active")
	}

	code, body, _ = a.do(f.admin, "POST", "/api/v1/fee-items", map[string]any{"name": "Simple will", "default_amount_cents": 750_00})
	if code != 201 {
		t.Fatalf("fee item %d %s", code, body)
	}
	c.check(t, "FeeItem", decodeMap(t, body))
	if code, _, _ := a.do(f.attorney, "POST", "/api/v1/fee-items", map[string]any{"name": "x", "default_amount_cents": 1}); code != 403 {
		t.Fatal("only admin writes fee items")
	}
	code, body, _ = a.do(f.staff, "GET", "/api/v1/activity-types", nil)
	for _, row := range decodeMap(t, body)["data"].([]any) {
		c.check(t, "ActivityType", row)
	}
}

// AC-004 over HTTP: every billing write route refuses an accountant and writes nothing.
func TestAPIAccountantWritesRefused(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, client := f.matter("Read only", "flat", 100_00, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 1, nil)
	inv := f.sent(m.ID)
	te := f.logTime(f.attorney, m.ID, "2026-06-02", 1, nil)
	pay := f.deposit(m.ID, client, 50_00)
	tm, _, _ := f.svc.StartTimer(f.ctx, f.staff, m.ID)
	before := f.snapshot()
	iv, mid := string(inv.ID), string(m.ID)
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/time-entries"}, {"POST", "/api/v1/time-entries:bulk"}, {"PATCH", "/api/v1/time-entries/" + string(te.ID)},
		{"POST", "/api/v1/expenses"}, {"POST", "/api/v1/expenses:bulk"},
		{"POST", "/api/v1/timers"}, {"POST", "/api/v1/timers/" + string(tm.ID) + "/stop"},
		{"POST", "/api/v1/invoice-drafts"}, {"PATCH", "/api/v1/invoices/" + iv}, {"PUT", "/api/v1/invoices/" + iv + "/draft"},
		{"POST", "/api/v1/invoices/" + iv + "/approve"}, {"POST", "/api/v1/invoices/" + iv + "/send"}, {"POST", "/api/v1/invoices/" + iv + "/void"},
		{"POST", "/api/v1/invoices/" + iv + "/apply-trust"}, {"POST", "/api/v1/invoices/" + iv + "/write-off"},
		{"POST", "/api/v1/invoices/" + iv + "/replenishment-requests"}, {"POST", "/api/v1/payments"}, {"POST", "/api/v1/payments:bulk"},
		{"POST", "/api/v1/payments/" + string(pay.ID) + "/status"}, {"POST", "/api/v1/payments/" + string(pay.ID) + "/reverse"},
		{"POST", "/api/v1/billing/flat-fees/" + mid + "/apply"}, {"POST", "/api/v1/fee-items"}, {"POST", "/api/v1/activity-types"}, {"POST", "/api/v1/expense-types"},
	}
	bodies := map[string]any{
		"/api/v1/time-entries":      map[string]any{"matter_id": mid, "worked_on": "2026-06-01", "increments": 1, "description": "x", "activity_type_id": f.activity},
		"/api/v1/time-entries:bulk": map[string]any{"items": []any{map[string]any{"matter_id": mid, "worked_on": "2026-06-01", "increments": 1, "description": "x", "activity_type_id": f.activity}}},
		"/api/v1/expenses":          map[string]any{"matter_id": mid, "incurred_on": "2026-06-01", "description": "x", "amount_cents": 1, "expense_type_id": f.expType},
		"/api/v1/expenses:bulk":     map[string]any{"items": []any{map[string]any{"matter_id": mid, "incurred_on": "2026-06-01", "description": "x", "amount_cents": 1, "expense_type_id": f.expType}}},
		"/api/v1/timers":            map[string]any{"matter_id": mid},
		"/api/v1/invoice-drafts":    map[string]any{"matter_id": mid},
		"/api/v1/payments":          map[string]any{"contact_id": client, "matter_id": mid, "amount_cents": 1, "destination": "operating", "method": "cash"},
		"/api/v1/payments:bulk":     map[string]any{"items": []any{map[string]any{"contact_id": client, "matter_id": mid, "amount_cents": 1, "destination": "operating", "method": "cash"}}},
		"/api/v1/fee-items":         map[string]any{"name": "x", "default_amount_cents": 1},
		"/api/v1/activity-types":    map[string]any{"name": "x"}, "/api/v1/expense-types": map[string]any{"name": "x"},
	}
	for _, rt := range routes {
		body, ok := bodies[rt.path]
		if !ok {
			switch {
			case strings.HasSuffix(rt.path, "/send"):
				body = map[string]any{"deliver": "email"}
			case strings.HasSuffix(rt.path, "/void"), strings.HasSuffix(rt.path, "/reverse"):
				body = map[string]any{"reason": "x"}
			case strings.HasSuffix(rt.path, "/apply-trust"), strings.HasSuffix(rt.path, "/apply"):
				body = map[string]any{"client_id": client, "trust_account_id": f.account, "amount_cents": 1}
			case strings.HasSuffix(rt.path, "/write-off"):
				body = map[string]any{"amount_cents": 1, "reason": "x"}
			case strings.HasSuffix(rt.path, "/replenishment-requests"):
				body = map[string]any{"matter_id": mid, "client_id": client, "amount_requested_cents": 1}
			case strings.HasSuffix(rt.path, "/status"):
				body = map[string]any{"status": "failed"}
			case strings.HasSuffix(rt.path, "/draft"):
				body = map[string]any{"lines": []any{}}
			default:
				body = map[string]any{}
			}
		}
		code, resp, _ := a.do(f.accountant, rt.method, rt.path, body)
		if code != 403 && code != 404 {
			t.Fatalf("%s %s: accountant got %d %s", rt.method, rt.path, code, resp)
		}
	}
	if code, resp, _ := a.do(f.accountant, "POST", "/api/v1/invoice-drafts", map[string]any{"from_date": "2026-01-01", "to_date": "2026-12-31"}); code != 403 {
		t.Fatalf("POST /api/v1/invoice-drafts by date range: accountant got %d %s", code, resp)
	}
	if f.snapshot() != before {
		t.Fatal("an accountant API write changed rows")
	}
	for _, path := range []string{"/api/v1/invoices", "/api/v1/payments", "/api/v1/time-entries", "/api/v1/expenses", "/api/v1/billing/worklist"} {
		if code, _, _ := a.do(f.accountant, "GET", path, nil); code != 200 {
			t.Fatalf("accountant read %s: %d", path, code)
		}
	}
}

func TestAPIIdempotencyPagingAndCSV(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Paging", "hourly", 0, 0)
	body := map[string]any{"matter_id": m.ID, "worked_on": "2026-06-01", "increments": 1, "description": "=SUM(A1)", "activity_type_id": f.activity}
	c1, b1, _ := a.do(f.staff, "POST", "/api/v1/time-entries", body, "Idempotency-Key", "k-1")
	c2, b2, h2 := a.do(f.staff, "POST", "/api/v1/time-entries", body, "Idempotency-Key", "k-1")
	if c1 != 201 || c2 != 201 || !bytes.Equal(bytes.TrimSpace(b1), bytes.TrimSpace(b2)) || h2.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay %d %d", c1, c2)
	}
	body["description"] = "other"
	if c, b, _ := a.do(f.staff, "POST", "/api/v1/time-entries", body, "Idempotency-Key", "k-1"); c != 409 || !bytes.Contains(b, []byte("idempotency_conflict")) {
		t.Fatalf("conflict %d %s", c, b)
	}
	if rows, _ := f.svc.TimeEntries(f.ctx, f.staff, TimeFilter{}); len(rows) != 1 {
		t.Fatalf("idempotent replay wrote %d rows", len(rows))
	}
	for i := 0; i < 4; i++ {
		f.logTime(f.staff, m.ID, "2026-06-03", 1, nil)
	}
	_, b, _ := a.do(f.staff, "GET", "/api/v1/time-entries?limit=2", nil)
	pg := decodeMap(t, b)
	next, _ := pg["page"].(map[string]any)["next_cursor"].(string)
	if len(pg["data"].([]any)) != 2 || next == "" {
		t.Fatalf("page 1: %s", b)
	}
	seen := 2
	for next != "" {
		_, b, _ = a.do(f.staff, "GET", "/api/v1/time-entries?limit=2&cursor="+next, nil)
		pg = decodeMap(t, b)
		seen += len(pg["data"].([]any))
		next, _ = pg["page"].(map[string]any)["next_cursor"].(string)
	}
	if seen != 5 {
		t.Fatalf("paged %d of 5", seen)
	}
	if c, _, _ := a.do(f.staff, "GET", "/api/v1/time-entries?limit=201", nil); c != 400 {
		t.Fatal("a limit above api.max_page_size must be refused")
	}
	if c, _, _ := a.do(f.staff, "GET", "/api/v1/time-entries?cursor=bogus", nil); c != 400 {
		t.Fatal("a foreign cursor must be refused")
	}
	_, csv, _ := a.do(f.staff, "GET", "/api/v1/time-entries?format=csv", nil)
	if !bytes.Contains(csv, []byte("'=SUM(A1)")) || bytes.Count(csv, []byte("\n")) != 6 {
		t.Fatalf("csv: %s", csv)
	}
	if c, _, _ := a.do(domain.Principal{UserID: "nobody"}, "GET", "/api/v1/time-entries", nil); c != 401 {
		t.Fatal("no principal must be 401")
	}
}

func TestAPIClientProjection(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	c := loadContract(t)
	m, contact := f.matter("Client view", "hourly", 0, 0)
	cl := person(f.id(), domain.RoleClient)
	enc, _ := domain.MarshalRoles(cl.Roles)
	f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: cl.UserID}, Name: "Cora", Roles: enc, Active: true, ContactID: contact})
	a.who[cl.UserID] = cl
	f.logTime(f.attorney, m.ID, "2026-06-01", 1, nil)
	inv := f.sent(m.ID)
	f.svc.UpdateInvoice(f.ctx, f.staff, inv.ID, InvoicePatch{InternalNotes: strPtr("slow payer")})
	code, body, _ := a.do(cl, "GET", "/api/v1/invoices/"+string(inv.ID), nil)
	if code != 200 || bytes.Contains(body, []byte("slow payer")) || bytes.Contains(body, []byte("internal_notes")) {
		t.Fatalf("client projection leaked: %d %s", code, body)
	}
	c.check(t, "InvoiceClient", decodeMap(t, body))
	code, body, _ = a.do(cl, "GET", "/api/v1/invoices", nil)
	for _, row := range decodeMap(t, body)["data"].([]any) {
		c.check(t, "InvoiceClient", row)
	}
	if code, _, _ := a.do(cl, "GET", "/api/v1/payments", nil); code != 404 && code != 403 {
		t.Fatalf("client payments: %d", code)
	}
}

func TestPagesRenderAndPost(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, client := f.matter("Page will", "flat", 800_00, 0)
	h, _ := f.matter("Hourly page", "hourly", 0, 0)
	walled, _ := f.matter("Hidden page", "hourly", 0, 0)
	f.logTime(f.attorney, h.ID, "2026-06-01", 3, nil)
	f.logTime(f.attorney, walled.ID, "2026-06-01", 3, nil)
	if err := f.ps.SetWall(f.ctx, f.admin, walled.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	f.deposit(m.ID, client, 800_00)
	code, body := a.page(f.staff, "/billing")
	if code != 200 || !strings.Contains(body, "Ready to apply") || !strings.Contains(body, "Generate draft invoice") || strings.Contains(body, "Hidden page") {
		t.Fatalf("worklist page %d", code)
	}
	if strings.Contains(body, "Apply flat fee</button>") {
		t.Fatal("staff must not see Apply flat fee")
	}
	_, body = a.page(f.attorney, "/billing")
	if !strings.Contains(body, "Apply flat fee</button>") || !strings.Contains(body, `value="800.00"`) {
		t.Fatal("attorney sees a prefilled Apply flat fee")
	}
	if code, _ := a.form(f.attorney, "/billing/flat-fees/"+string(m.ID)+"/apply", url.Values{"client_id": {string(client)}, "trust_account_id": {string(f.account)}, "amount": {"800.00"}}, false); code != 403 {
		t.Fatalf("missing csrf must be refused: %d", code)
	}
	code, loc := a.form(f.attorney, "/billing/flat-fees/"+string(m.ID)+"/apply", url.Values{"client_id": {string(client)}, "trust_account_id": {string(f.account)}, "amount": {"800.00"}}, true)
	if code != 303 || !strings.Contains(loc, "Collected") {
		t.Fatalf("apply %d %s", code, loc)
	}
	_, body = a.page(f.staff, "/billing")
	if !strings.Contains(body, "tag-collected") {
		t.Fatal("collected status missing")
	}
	code, loc = a.form(f.staff, "/billing/generate", url.Values{"one": {string(h.ID)}}, true)
	if code != 303 || !strings.HasPrefix(loc, "/billing/invoices/") {
		t.Fatalf("generate %d %s", code, loc)
	}
	invPath := strings.SplitN(loc, "?", 2)[0]
	code, body = a.page(f.attorney, invPath)
	if code != 200 || !strings.Contains(body, "Approve") || !strings.Contains(body, "Edit history") {
		t.Fatalf("invoice page %d", code)
	}
	a.form(f.attorney, invPath+"/approve", url.Values{}, true)
	code, loc = a.form(f.attorney, invPath+"/send", url.Values{"deliver": {"email"}}, true)
	if code != 303 || !strings.Contains(loc, "cora%40example.test") {
		t.Fatalf("send page %d %s", code, loc)
	}
	_, body = a.page(f.attorney, invPath)
	if !strings.Contains(body, "Collections") || !strings.Contains(body, "Record outside payment") || !strings.Contains(body, "Request replenishment") {
		t.Fatal("collections panel missing on a sent invoice")
	}
	code, loc = a.form(f.staff, invPath+"/payment", url.Values{"contact_id": {string(client)}, "amount": {"0"}, "method": {"check"}}, true)
	if code != 303 || !strings.Contains(loc, "error=") {
		t.Fatalf("zero payment must show an error: %s", loc)
	}
	for _, p := range []string{"/billing/invoices", "/billing/time", "/billing/payments", "/billing/reports/ar-aging", "/billing/reports/unbilled-time"} {
		code, body := a.page(f.staff, p)
		if code != 200 || strings.Contains(body, "Hidden page") {
			t.Fatalf("%s: %d or leaked the wall", p, code)
		}
	}
	_, body = a.page(f.accountant, "/billing/time")
	if strings.Contains(body, "Log time</button>") {
		t.Fatal("accountant must not get the time form")
	}
	code, loc = a.form(f.staff, "/billing/time", url.Values{"matter_id": {string(h.ID)}, "worked_on": {"2026-06-05"}, "minutes": {"13"}, "activity_type_id": {string(f.activity)}, "description": {"Call"}, "billable": {"1"}}, true)
	if code != 303 || !strings.Contains(loc, "notice") {
		t.Fatalf("log time %d %s", code, loc)
	}
	rows, _ := f.svc.TimeEntries(f.ctx, f.staff, TimeFilter{FromDate: "2026-06-05"})
	if len(rows) != 1 || rows[0].Increments != 3 {
		t.Fatal("13 minutes rounds up to three 6-minute increments")
	}
	if code, _ := a.page(f.staff, "/billing/reports/nope"); code != 404 {
		t.Fatal("unknown report")
	}
}

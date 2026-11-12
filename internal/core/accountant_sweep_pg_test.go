package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/api"
)

// accountantSelfService are the write operations the contract opens to the accountant
// role. Each acts only on the caller's own account, session, layouts, notifications or
// problem report, builds a download, or mints a module frame's context token; none writes
// a firm record. TestReleaseAccountantWritesNothing fails if the contract opens any other
// write to accountants.
var accountantSelfService = map[string]bool{
	"POST /api/v1/auth/logout": true, "PATCH /api/v1/me": true, "POST /api/v1/me/email-change": true,
	"POST /api/v1/me/email-change/confirm": true, "POST /api/v1/me/password": true, "POST /api/v1/me/totp/reset": true,
	"POST /api/v1/me/totp/reset/confirm": true, "POST /api/v1/me/recovery-codes": true, "PATCH /api/v1/me/settings": true,
	"PUT /api/v1/me/notification-preferences": true, "PUT /api/v1/me/sidebar": true, "DELETE /api/v1/sessions/{session_id}": true,
	"DELETE /api/v1/api-tokens/{token_id}": true, "POST /api/v1/file-archives": true,
	"POST /api/v1/notifications/{notification_id}/read": true, "POST /api/v1/notifications:read-all": true,
	"PUT /api/v1/me/layout": true, "POST /api/v1/me/layouts": true, "PATCH /api/v1/me/layouts/{layout_id}": true,
	"DELETE /api/v1/me/layouts/{layout_id}": true, "POST /api/v1/me/layouts/{layout_id}/default": true,
	"POST /api/v1/me/layouts/{layout_id}/apply": true, "POST /api/v1/problem-reports": true,
	"POST /api/v1/modules/{module_id}/context-tokens": true,
}

// TestReleaseAccountantWritesNothing is AC-004 across the whole contract: every mounted
// write operation other than the accountant's own self-service, sent by an accountant
// with real record ids in its path and a body built from the contract's request schema,
// is refused with a 4xx, and no row of any core table changes.
func TestReleaseAccountantWritesNothing(t *testing.T) {
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	acct, acctID := enrollUser(t, app, srv.URL, admin, "Abe", "abe@firm.example", "accountant")
	_, attyID := enrollUser(t, app, srv.URL, admin, "Ann", "ann@firm.example", "attorney")

	// Real records, so a write cannot fail only because its target is missing.
	ids := map[string]string{"user_id": string(attyID), "role": "staff", "module_id": "matter-clock"}
	ids["practice_area_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Family", "stages": []string{"Intake"}}).json, "id")
	ids["matter_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Sweep matter", "practice_area_id": ids["practice_area_id"], "billing_type": "hourly", "responsible_attorney_user_id": attyID}).json, "id")
	ids["contact_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Sweep", "family_name": "Client"}).json, "id")
	ids["client_id"] = ids["contact_id"]
	admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": ids["matter_id"], "contact_id": ids["contact_id"], "role": "client", "primary": true})
	ids["note_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/notes", map[string]any{"matter_id": ids["matter_id"], "title": "Sweep note", "body": "x"}).json, "id")
	ids["trust_account_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/trust-accounts", map[string]any{"bank": "Synthetic Bank", "name": "IOLTA", "iolta": true, "last_four": "4242"}).json, "id")
	ids["activity_type_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/activity-types", map[string]any{"name": "Drafting"}).json, "id")
	ids["time_entry_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{"matter_id": ids["matter_id"], "worked_on": time.Now().UTC().Format("2006-01-02"), "increments": 5, "description": "Sweep", "activity_type_id": ids["activity_type_id"], "billable": true, "rate_cents": 10000}).json, "id")
	drafts := admin.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": ids["matter_id"]})
	if list, _ := drafts.json["data"].([]any); len(list) > 0 {
		ids["invoice_id"] = list[0].(map[string]any)["id"].(string)
	}
	ids["calendar_event_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Sweep hearing", "kind": "court_date", "starts_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339), "ends_at": time.Now().Add(49 * time.Hour).UTC().Format(time.RFC3339), "matter_id": ids["matter_id"]}).json, "id")
	ids["event_id"] = ids["calendar_event_id"]
	ids["deadline_id"] = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": ids["matter_id"], "title": "Sweep deadline", "due_on": time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02"), "responsible_user_id": attyID}).json, "id")

	var spec map[string]any
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	before := coreFingerprint(t, app)
	checked, byCode := 0, map[int]int{}
	for _, tl := range app.tools {
		key := tl.Method + " " + tl.Path
		if tl.Method == "GET" || tl.Path == "/mcp" || !app.mounted(tl) {
			continue
		}
		// Public operations (setup, sign-in, password-reset requests) are anyone's, not
		// an accountant's; their own tests cover them.
		if len(tl.Roles) == 0 && len(tl.Scopes) == 0 {
			continue
		}
		for _, r := range tl.Roles {
			if r == "accountant" && !accountantSelfService[key] {
				t.Errorf("the contract opens %s to accountants and it is not self-service", key)
			}
		}
		if accountantSelfService[key] {
			continue
		}
		path := tl.Path
		for _, s := range tl.segs {
			if strings.HasPrefix(s, "{") {
				name := strings.Trim(s, "{}")
				v := ids[name]
				if v == "" {
					v = "01a1c38b-0000-7000-8000-000000000000"
				}
				path = strings.ReplaceAll(path, s, v)
			}
		}
		var body any = map[string]any{}
		if schema := requestSchema(spec, tl.Method, tl.Path); schema != nil {
			body = sample(spec, schema, "", ids, 0)
		}
		r := acct.do(tl.Method, path, body)
		checked++
		byCode[r.code]++
		if r.code < 400 || r.code >= 500 {
			t.Errorf("accountant %s answered %d: %.300s", key, r.code, r.body)
		}
		// The fingerprint after every request names the operation that changed a row.
		after := coreFingerprint(t, app)
		for table, sum := range after {
			if before[table] != sum {
				t.Errorf("accountant %s changed core.%s", key, table)
			}
		}
		before = after
	}
	if checked < 150 {
		t.Fatalf("only %d write operations were swept", checked)
	}
	t.Logf("%d write operations refused to the accountant %s (by status %v); no core table changed", checked, acctID, byCode)
}

// coreFingerprint is an md5 of every row of every core table, except the sessions and
// sign-in bookkeeping that any authenticated request touches.
func coreFingerprint(t *testing.T, app *App) map[string]string {
	t.Helper()
	ctx := context.Background()
	rows, err := app.Pool.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'core' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	sort.Strings(tables)
	// module_event_cursor is the event pump's time horizon, which its background loop advances.
	skip := map[string]bool{"sessions": true, "login_attempts": true, "rate_limits": true, "goose_db_version": true, "module_event_cursor": true}
	out := map[string]string{}
	for _, n := range tables {
		if skip[n] {
			continue
		}
		var sum string
		q := `SELECT coalesce(md5(string_agg(t::text, ',' ORDER BY t::text)), '') FROM core."` + n + `" t`
		if err := app.Pool.QueryRowContext(ctx, q).Scan(&sum); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		out[n] = sum
	}
	return out
}

// requestSchema is the JSON request body schema of one contract operation, or nil.
func requestSchema(spec map[string]any, method, path string) map[string]any {
	paths, _ := spec["paths"].(map[string]any)
	ops, _ := paths[path].(map[string]any)
	op, _ := ops[strings.ToLower(method)].(map[string]any)
	rb, _ := resolve(spec, op["requestBody"]).(map[string]any)
	content, _ := rb["content"].(map[string]any)
	js, _ := content["application/json"].(map[string]any)
	s, _ := resolve(spec, js["schema"]).(map[string]any)
	return s
}

func resolve(spec map[string]any, v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	ref, ok := m["$ref"].(string)
	if !ok {
		return m
	}
	var cur any = spec
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = cur.(map[string]any)[part]
	}
	return resolve(spec, cur)
}

// sample builds a value that satisfies schema's required shape, using real ids for
// properties named like a record id.
func sample(spec map[string]any, schema map[string]any, name string, ids map[string]string, depth int) any {
	if depth > 6 {
		return nil
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf"} {
		if alts, ok := schema[k].([]any); ok {
			for _, a := range alts {
				if s, ok := resolve(spec, a).(map[string]any); ok && s["type"] != "null" {
					return sample(spec, s, name, ids, depth+1)
				}
			}
		}
	}
	if c, ok := schema["const"]; ok {
		return c
	}
	if e, ok := schema["enum"].([]any); ok && len(e) > 0 {
		return e[0]
	}
	switch schema["type"] {
	case "object", nil:
		out := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		req, _ := schema["required"].([]any)
		for _, r := range req {
			k := r.(string)
			if p, ok := resolve(spec, props[k]).(map[string]any); ok {
				out[k] = sample(spec, p, k, ids, depth+1)
			} else {
				out[k] = "synthetic"
			}
		}
		return out
	case "array":
		items, _ := resolve(spec, schema["items"]).(map[string]any)
		if min, _ := schema["minItems"].(float64); min > 0 && items != nil {
			return []any{sample(spec, items, strings.TrimSuffix(name, "s"), ids, depth+1)}
		}
		return []any{}
	case "integer", "number":
		if min, ok := schema["minimum"].(float64); ok && min > 1 {
			return min
		}
		return 1
	case "boolean":
		return true
	}
	switch schema["format"] {
	case "uuid":
		if v := ids[name]; v != "" {
			return v
		}
		if strings.HasSuffix(name, "_user_id") || name == "user_ids" {
			return ids["user_id"]
		}
		return "01a1c38b-0000-7000-8000-000000000000"
	case "date":
		return time.Now().UTC().Format("2006-01-02")
	case "date-time":
		return time.Now().UTC().Format(time.RFC3339)
	case "email":
		return "sweep@firm.example"
	}
	return "Synthetic sweep value"
}

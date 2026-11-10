package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPracticeRecordsOnCore covers AC-009, AC-020, AC-023 and AC-025 through the composed
// core over Postgres. Billing and trust have no API yet, so the unpaid invoice is written
// straight into the durable practice store as the billing wave's stand-in.
func TestPracticeRecordsOnCore(t *testing.T) {
	ctx := context.Background()
	app, srv := start(t, testDSN(t))
	admin := firstRun(t, srv.URL)
	probate := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}}).json, "id")
	family := str(t, admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Family", "stages": []string{"Intake"}}).json, "id")
	estate := admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Estate of Lind", "practice_area_id": probate, "billing_type": "hourly"})
	divorce := admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{"name": "Marriage of Holt", "practice_area_id": family, "billing_type": "hourly"})
	estateID, divorceID := str(t, estate.json, "id"), str(t, divorce.json, "id")
	number := str(t, estate.json, "number")

	// AC-025: a field limited to Probate shows only there and saves through the API and UI.
	def := admin.must(http.StatusCreated, "POST", "/api/v1/custom-field-definitions", map[string]any{
		"record_type": "matter", "key": "date_of_death", "label": "Date of death", "field_type": "text", "practice_area_ids": []string{probate},
	})
	defID := str(t, def.json, "id")
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+divorceID, nil); strings.Contains(r.body, "date_of_death") {
		t.Fatal("probate field shows on a family matter")
	}
	if r := admin.do("PUT", "/api/v1/matters/"+divorceID+"/custom-fields", map[string]any{"values": []map[string]any{{"definition_id": defID, "text": "2026-01-02"}}}); r.code < 400 {
		t.Fatalf("probate field saved on a family matter: %d", r.code)
	}
	admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+estateID+"/custom-fields", map[string]any{"values": []map[string]any{{"definition_id": defID, "text": "2026-01-02"}}})
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+estateID, nil); !strings.Contains(r.body, "2026-01-02") {
		t.Fatal("API save not shown", r.body)
	}
	page := admin.must(http.StatusOK, "GET", "/practice/matters/"+estateID, nil)
	if !strings.Contains(page.body, "Date of death") {
		t.Fatal("matter screen lacks the custom field")
	}
	ui := admin.do("POST", "/practice/matters/"+estateID+"/custom-fields", url.Values{"csrf": {csrfFrom(t, admin)}, "cf_" + defID: {"2026-02-03"}}, "X-CSRF-Token", "")
	if ui.code != http.StatusSeeOther {
		t.Fatalf("UI save: %d %s", ui.code, ui.body)
	}
	csv := admin.must(http.StatusOK, "GET", "/api/v1/matters?format=csv", nil)
	if !strings.Contains(csv.body, "Date of death") || !strings.Contains(csv.body, "2026-02-03") {
		t.Fatalf("CSV lacks the UI-saved value: %s", csv.body)
	}

	// AC-020: matter by number, person by phone, invoice by number, in scope and in All.
	contact := admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Nora", "family_name": "Lind"})
	contactID := str(t, contact.json, "id")
	admin.must(http.StatusCreated, "POST", "/api/v1/contact-phones", map[string]any{"contact_id": contactID, "label": "mobile", "phone": "+13125550187", "e164": "+13125550187", "primary": true})
	now := time.Now().UTC()
	inv := domain.Invoice{Meta: domain.Meta{ID: mustID(t), CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID},
		MatterID: domain.ID(estateID), Number: "INV-7731", Status: "sent", BalanceDueCents: 12500, IssuedOn: "2026-10-01"}
	if err := app.Store.PutInvoice(ctx, inv); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ q, scope, want string }{
		{number, "matters", estateID}, {number, "all", estateID},
		{"3125550187", "people", contactID}, {"3125550187", "all", contactID},
		{"INV-7731", "invoices", string(inv.ID)}, {"INV-7731", "all", string(inv.ID)},
	} {
		r := admin.must(http.StatusOK, "GET", "/api/v1/search?q="+url.QueryEscape(c.q)+"&scope="+c.scope, nil)
		if !strings.Contains(r.body, c.want) {
			t.Fatalf("search %q in %s missed %s: %s", c.q, c.scope, c.want, r.body)
		}
	}

	// AC-009: the unpaid invoice warns, and the matter still closes once acknowledged.
	preview := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+estateID+"/close-preview", nil)
	if !strings.Contains(preview.body, "unpaid") {
		t.Fatalf("no unpaid-invoice warning: %s", preview.body)
	}
	closed := admin.must(http.StatusOK, "POST", "/api/v1/matters/"+estateID+"/close", map[string]any{"reason": "estate settled", "acknowledge": true})
	if !strings.Contains(closed.body, `"closed"`) {
		t.Fatalf("close: %s", closed.body)
	}

	// AC-023: reopening needs a reason, restores the earlier status and is on the timeline.
	if r := admin.do("POST", "/api/v1/matters/"+estateID+"/reopen", map[string]any{"reason": ""}); r.code != http.StatusBadRequest {
		t.Fatalf("reopen without a reason: %d %s", r.code, r.body)
	}
	reopened := admin.must(http.StatusOK, "POST", "/api/v1/matters/"+estateID+"/reopen", map[string]any{"reason": "new asset found"})
	if strings.Contains(reopened.body, `"status":"closed"`) {
		t.Fatalf("reopen kept closed: %s", reopened.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+estateID, nil); !strings.Contains(r.body, `"status":"open"`) {
		t.Fatalf("earlier status not restored: %s", r.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/matters/"+estateID+"/timeline", nil); !strings.Contains(r.body, "new asset found") {
		t.Fatalf("reopen missing from the timeline: %s", r.body)
	}
}

func mustID(t *testing.T) domain.ID {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestFreshInstallSchemaAndSeed is AC-035 on the core: Open on an empty database creates
// every section 5 table, and the synthetic seed fills each one.
func TestFreshInstallSchemaAndSeed(t *testing.T) {
	ctx := context.Background()
	app, _ := start(t, testDSN(t))
	if err := db.ApplySynthetic(ctx, app.Pool, "postgres"); err != nil {
		t.Fatal(err)
	}
	for _, rec := range domain.RecordTypes() {
		var n int
		if err := app.Pool.QueryRowContext(ctx, "SELECT count(*) FROM "+rec.TableName()).Scan(&n); err != nil {
			t.Fatalf("%s: %v", rec.TableName(), err)
		}
		if n == 0 {
			t.Errorf("seed left %s empty", rec.TableName())
		}
	}
	if len(domain.RecordTypes()) != 54 {
		t.Fatal(len(domain.RecordTypes()))
	}
}

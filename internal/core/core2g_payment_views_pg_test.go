package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Exercise Include directly as well as HTTP: a payment endpoint can already filter
// its parent row before the include middleware reaches the vulnerable service check.
func TestCoreUpdate2GContactPaymentExtensionWalls(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	x.trustKey("Synthetic payment views", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(files map[string][]byte) {
		files[modules.ManifestFile] = []byte(strings.NewReplacer("record: matter", "record: payment", "matter_id", "payment_id").Replace(string(files[modules.ManifestFile])))
		path := "migrations/0001_matter_targets.sql"
		files[path] = []byte(strings.NewReplacer("core.matters", "core.payments", "matter_id", "payment_id").Replace(string(files[path])))
	})
	if r := x.admin.upload("/api/v1/modules", nil, pkg); r.code != 202 {
		t.Fatalf("install payment extensions: %d %s", r.code, r.body)
	}
	x.approveOnTab("matter-clock")
	contact := x.admin.must(201, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Synthetic", "family_name": "Receipt"})
	contactID := domain.ID(str(t, contact.json, "id"))
	// This is the same billing entry point the approved processor-receipt binding uses.
	raw, err := x.app.Billing.RecordApprovedOperatingPayment(ctx, domain.Principal{Source: domain.SourceService, ModuleID: "synthetic-intake", Roles: []domain.Role{domain.RoleStaff}}, billing.PaymentInput{ContactID: contactID, AmountCents: 2500, Destination: billing.DestOperating, Method: "card", Status: billing.PayCompleted, ReferenceNumber: "synthetic-receipt", Source: "synthetic-processor"})
	if err != nil {
		t.Fatal(err)
	}
	var payment map[string]any
	if err := json.Unmarshal(raw, &payment); err != nil {
		t.Fatal(err)
	}
	id := str(t, payment, "id")
	dep := x.rt.Last("matter-clock")
	db := x.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO mod_matter_clock.matter_targets (payment_id, monthly_target_tenths, updated_at) VALUES ($1,37,0)`, id); err != nil {
		t.Fatal(err)
	}
	matter, _ := x.matter("Synthetic later placement", "", nil)
	x.admin.must(201, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": matter, "contact_id": contactID, "role": "client"})
	x.admin.must(200, "PUT", "/api/v1/matters/"+matter+"/wall", map[string]any{"user_ids": []string{x.attyID}})
	scope := []string{modules.ViewScope("matter-clock", "monthly_targets")}
	for _, lifted := range []bool{false, true} {
		if lifted {
			x.admin.must(200, "PUT", "/api/v1/matters/"+matter+"/wall", map[string]any{"user_ids": []string{}})
		}
		for _, who := range []*client{x.staff, x.atty} {
			want := lifted || who == x.atty
			p := x.principal(who)
			inc, err := x.app.Modules.Include(ctx, p, nil, "payment", []domain.ID{domain.ID(id)}, []string{"matter-clock.targets"})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(inc[domain.ID(id)]["matter-clock.targets"]); (got == 1) != want {
				t.Errorf("lifted=%v user=%s Include rows=%d want visible=%v", lifted, p.UserID, got, want)
			}
			// Include of a published view also checks the approved view scope.
			inc, err = x.app.Modules.Include(ctx, p, scope, "payment", []domain.ID{domain.ID(id)}, []string{"matter-clock.monthly_targets"})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(inc[domain.ID(id)]["matter-clock.monthly_targets"]); (got == 1) != want {
				t.Errorf("lifted=%v user=%s published Include rows=%d want visible=%v", lifted, p.UserID, got, want)
			}
			rows, err := x.app.Modules.ReadView(ctx, p, scope, true, "matter-clock", "monthly_targets", []domain.ID{domain.ID(id)})
			if err != nil {
				t.Fatal(err)
			}
			if (len(rows) == 1) != want {
				t.Errorf("lifted=%v user=%s scoped view rows=%d want visible=%v", lifted, p.UserID, len(rows), want)
			}
			view := who.must(200, "GET", "/api/v1/modules/matter-clock/views/monthly_targets?record_id="+id, nil)
			if strings.Contains(view.body, `"monthly_target_tenths":37`) != want {
				t.Errorf("lifted=%v HTTP view visible want=%v: %s", lifted, want, view.body)
			}
			code := 404
			if want {
				code = 200
			}
			who.must(code, "GET", "/api/v1/payments/"+id+"?include=matter-clock.targets", nil)
		}
	}
}

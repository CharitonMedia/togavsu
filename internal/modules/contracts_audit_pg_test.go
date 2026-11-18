package modules_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// failContractCallStore refuses Append for module_contract_call rows so CallContract
// can prove it fails closed when the audit write fails (Codex P2 on CORE-31).
type failContractCallStore struct {
	fail bool
}

func (f *failContractCallStore) Append(_ context.Context, row domain.AuditEntry) error {
	if f.fail && row.Entity == "module_contract_call" {
		return errors.New("audit down")
	}
	return nil
}

func (f *failContractCallStore) Get(context.Context, domain.ID) (domain.AuditEntry, error) {
	return domain.AuditEntry{}, repository.ErrNotFound
}

func (f *failContractCallStore) List(context.Context, repository.Filter) (repository.Page[domain.AuditEntry], error) {
	return repository.Page[domain.AuditEntry]{}, nil
}

func (f *failContractCallStore) Rows(context.Context, audit.RowQuery) ([]domain.AuditEntry, error) {
	return nil, nil
}

func patchContractManifest(raw []byte, id, name, extraPerms, tail string) []byte {
	m := string(raw)
	m = strings.Replace(m, "id: matter-clock", "id: "+id, 1)
	m = strings.Replace(m, "name: Matter Clock", "name: "+name, 1)
	if extraPerms != "" {
		m = strings.Replace(m, "permissions:\n", "permissions:\n"+extraPerms, 1)
	}
	if tail != "" {
		if !strings.HasSuffix(m, "\n") {
			m += "\n"
		}
		m += tail
	}
	return []byte(m)
}

// TestCallContractFailsClosedWhenAuditFails: a successful provider answer is not
// returned to the consumer when Audit.Record fails (CORE-CHANGES-2 §4.1).
func TestCallContractFailsClosedWhenAuditFails(t *testing.T) {
	e := newEnv(t)
	const contract = "phone.provider.v1"
	var providerHits int
	e.rt.Serve("phone-provider", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			providerHits++
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"secret":"must-not-leak-without-audit"}`))
		})
	})
	e.rt.Serve("phone-consumer", func(m testkit.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusNoContent)
		})
	})

	prov := e.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchContractManifest(f[modules.ManifestFile], "phone-provider", "Phone Provider",
			"  - scope: contracts:provide\n    reason: Provide contract.\n",
			"provides:\n  - contract: "+contract+"\n    operations:\n      - name: echo\n        path: /contracts/echo\n")
	})
	cons := e.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = patchContractManifest(f[modules.ManifestFile], "phone-consumer", "Phone Consumer",
			"  - scope: contracts:call\n    reason: Call contract.\n",
			"consumes:\n  - contract: "+contract+"\n")
	})
	for _, pkg := range [][]byte{prov, cons} {
		r := e.do("POST", "/api/v1/modules", pkg, e.as("admin")...)
		if r.code != http.StatusAccepted {
			t.Fatalf("install: %d %s", r.code, r.body)
		}
	}
	e.approveAll("phone-provider")
	e.approveAll("phone-consumer")
	if _, err := e.svc.LinkContract(e.ctx, e.admin, modules.ContractLinkWrite{
		Contract: contract, ConsumerID: "phone-consumer", ProviderID: "phone-provider",
	}); err != nil {
		t.Fatal(err)
	}

	failStore := &failContractCallStore{fail: true}
	failAudit, err := audit.New(failStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := modules.New(modules.Deps{
		DB: e.app.Pool, Key: testKey, Audit: failAudit, Settings: e.app.Settings,
		Walls: e.app.Practice.Policy.Walls, Contacts: e.app.Practice, Signing: e.market,
		Users: e.app.Users, Tokens: modules.IdentityTokens{Svc: e.app.Identity},
		Runtime: e.rt, HTTP: http.DefaultClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller := domain.Principal{Source: domain.SourceModule, ModuleID: "phone-consumer"}
	status, hdr, body, err := svc.CallContract(e.ctx, caller, contract, "echo", []byte(`{"n":1}`), "", "")
	if err == nil {
		t.Fatalf("expected audit failure, got status=%d body=%s headers=%v", status, body, hdr)
	}
	if !strings.Contains(err.Error(), "audit down") {
		t.Fatalf("want audit down error, got %v", err)
	}
	if status != 0 || len(body) != 0 || hdr != nil {
		t.Fatalf("provider response must not be returned without audit: status=%d body=%q hdr=%v", status, body, hdr)
	}
	if providerHits != 1 {
		t.Fatalf("provider should have been reached once before audit fail-closed, hits=%d", providerHits)
	}

	okAudit, err := audit.New(&failContractCallStore{fail: false}, nil)
	if err != nil {
		t.Fatal(err)
	}
	okSvc, err := modules.New(modules.Deps{
		DB: e.app.Pool, Key: testKey, Audit: okAudit, Settings: e.app.Settings,
		Walls: e.app.Practice.Policy.Walls, Contacts: e.app.Practice, Signing: e.market,
		Users: e.app.Users, Tokens: modules.IdentityTokens{Svc: e.app.Identity},
		Runtime: e.rt, HTTP: http.DefaultClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _, body, err = okSvc.CallContract(e.ctx, caller, contract, "echo", []byte(`{"n":2}`), "", "")
	if err != nil || status != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("healthy audit path: status=%d body=%s err=%v", status, body, err)
	}
}

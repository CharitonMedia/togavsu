package coretest_test

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2FFixReservedSources(t *testing.T) {
	c := coretest.New(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.TrustKey("synthetic-core-sources", pub)
	for _, id := range []string{"core", "upload", "client_upload", "editor", "billing", "drop_folder", "ui", "api_token", "module", "service", "system", "api", "manual", "timer", "import"} {
		t.Run(id, func(t *testing.T) {
			files, err := matterclock.Files()
			if err != nil {
				t.Fatal(err)
			}
			files[modulekit.ManifestFile] = bytes.Replace(files[modulekit.ManifestFile], []byte("id: matter-clock"), []byte("id: "+id), 1)
			if _, err := modulekit.ParseManifest(files[modulekit.ManifestFile]); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Errorf("manifest: %v", err)
			}
			if _, _, err := modulekit.Check(files); err == nil || !strings.Contains(err.Error(), "reserved") {
				t.Errorf("author tooling: %v", err)
			}
			raw, err := modulekit.Archive(files)
			if err != nil {
				t.Fatal(err)
			}
			signed, err := json.Marshal(modulekit.SignedPackage{ListingID: id, Version: "1.0.0", Digest: modulekit.Digest(raw), Signature: modulekit.Sign(priv, raw), KeyID: modulekit.KeyID(pub), PackageBase64: base64.StdEncoding.EncodeToString(raw)})
			if err != nil {
				t.Fatal(err)
			}
			if r := c.TryInstall(signed); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "reserved") {
				t.Errorf("install: %d %s", r.Code, r.Body)
			}
			if r := c.Admin.Do("POST", "/api/v1/modules", map[string]any{"source": "marketplace", "listing_id": id, "version": "1.0.0"}); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "reserved") {
				t.Errorf("catalog install: %d %s", r.Code, r.Body)
			}
			if h := modulekit.SignWebhookSenderHeaders("synthetic-secret", time.Now(), id, []byte(`{}`)); h != nil {
				t.Error("reserved sender signed")
			}
		})
	}
}

func TestCoreUpdate2FFixPublicationCallerFirst(t *testing.T) {
	c := coretest.New(t)
	const module = "publication-guard"
	c.Handle(module, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, module, []string{"files:read", "files:write"}, "")
	bot := c.ModuleClient(module)
	up := stage2f(t, bot, c.Open, "author-order.txt")
	var want string
	for _, matter := range []string{c.Walled, "00000000-0000-0000-0000-000000000000"} {
		for _, author := range []string{"", c.Attorney.ID, c.Staff.ID, "00000000-0000-0000-0000-000000000000"} {
			r := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "matter_id": matter, "on_behalf_of_user_id": author})
			if r.Code != http.StatusNotFound || r.JSON["code"] != "not_found" {
				t.Errorf("caller denied: %d %s", r.Code, r.Body)
			}
			if want == "" {
				want = r.Body
			}
			if r.Body != want {
				t.Errorf("author/matter disclosed through different answer: %s vs %s", r.Body, want)
			}
		}
	}
	bot.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up, "on_behalf_of_user_id": c.Attorney.ID})
}

func TestCoreUpdate2FFixPublicationAuthorPlacement(t *testing.T) {
	c := coretest.New(t)
	const module = "publication-placement"
	c.Handle(module, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, module, []string{"files:read", "files:write"}, "")
	bot := c.ModuleClient(module)
	viewer := c.ContextFor(coretest.Attorney, module, c.Open)
	// The attorney viewer can publish, but cannot lend that access to a staff author.
	up := stage2f(t, bot, c.Open, "attorney-author.txt", coretest.ContextHeader, viewer)
	var want string
	for _, author := range []string{c.Staff.ID, c.Client.ID, c.Accountant.ID, "00000000-0000-0000-0000-000000000000"} {
		r := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "attorney_only": true, "on_behalf_of_user_id": author}, coretest.ContextHeader, viewer)
		if r.Code != http.StatusBadRequest {
			t.Errorf("invalid author: %d %s", r.Code, r.Body)
		}
		if want == "" {
			want = r.Body
		}
		if r.Body != want {
			t.Errorf("author failure differs: %s vs %s", r.Body, want)
		}
	}
	if r := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "attorney_only": true, "on_behalf_of_user_id": c.Attorney.ID}, coretest.ContextHeader, viewer); r.Code != http.StatusCreated {
		t.Fatalf("valid attorney: %d %s", r.Code, r.Body)
	}
	contact := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "Synthetic", "family_name": "Walled Author"})
	contactID := str(t, contact.JSON, "id")
	c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": c.Walled, "contact_id": contactID, "role": "client"})
	// A service outside the contact's wall gets the same error for every author.
	serviceUp := stage2f(t, bot, c.Open, "hidden-contact.txt")
	want = ""
	for _, author := range []string{"", c.Attorney.ID, c.Staff.ID, "00000000-0000-0000-0000-000000000000"} {
		r := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": serviceUp, "contact_id": contactID, "on_behalf_of_user_id": author})
		if r.Code != http.StatusNotFound {
			t.Errorf("hidden caller contact: %d %s", r.Code, r.Body)
		}
		if want == "" {
			want = r.Body
		}
		if r.Body != want {
			t.Errorf("contact author oracle: %s vs %s", r.Body, want)
		}
	}
	for _, placement := range []string{"matter", "contact"} {
		up := stage2f(t, bot, c.Open, "contact-author.txt", coretest.ContextHeader, viewer)
		body := map[string]any{"upload_id": up, "contact_id": contactID, "on_behalf_of_user_id": c.Staff.ID}
		if placement == "contact" {
			body["matter_id"] = ""
		}
		if r := bot.Do("POST", "/api/v1/files", body, coretest.ContextHeader, viewer); r.Code != http.StatusBadRequest {
			t.Errorf("hidden contact author: %d %s", r.Code, r.Body)
		}
		body["on_behalf_of_user_id"] = c.Attorney.ID
		if r := bot.Do("POST", "/api/v1/files", body, coretest.ContextHeader, viewer); r.Code != http.StatusCreated {
			t.Fatalf("valid contact author: %d %s", r.Code, r.Body)
		}
	}
	// A library publication still validates its named author and attorney-only flag.
	up = stage2f(t, bot, c.Open, "library-author.txt", coretest.ContextHeader, viewer)
	library := map[string]any{"upload_id": up, "matter_id": "", "library": true, "attorney_only": true, "on_behalf_of_user_id": c.Staff.ID}
	if r := bot.Do("POST", "/api/v1/files", library, coretest.ContextHeader, viewer); r.Code != http.StatusBadRequest {
		t.Errorf("staff library author: %d %s", r.Code, r.Body)
	}
	library["on_behalf_of_user_id"] = c.Attorney.ID
	if r := bot.Do("POST", "/api/v1/files", library, coretest.ContextHeader, viewer); r.Code != http.StatusCreated {
		t.Fatalf("valid library author: %d %s", r.Code, r.Body)
	}
}

func TestCoreUpdate2FFixFileEventVisibility(t *testing.T) {
	c := coretest.New(t)
	const module = "file-visibility"
	c.Handle(module, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	install2d(t, c, module, []string{"files:read"}, "")
	for _, test := range []struct {
		name, matter string
		attorneyOnly bool
		visible      bool
	}{
		{"walled", c.Walled, false, false}, {"attorney", c.Open, true, false}, {"open", c.Open, false, true},
	} {
		up := stage2f(t, c.Attorney.Client, test.matter, test.name+".txt")
		f := c.Attorney.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up, "attorney_only": test.attorneyOnly})
		id := str(t, f.JSON, "id")
		c.Attorney.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/status", map[string]any{"status": "final"})
		c.Attorney.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/trash", map[string]any{})
		c.DeliverEvents()
		for _, action := range []string{"file.created", "file.version_created", "file.updated", "file.trashed"} {
			n := c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id=$1 AND action=$2 AND payload LIKE $3`, module, action, "%"+id+"%")
			if (n > 0) != test.visible {
				t.Errorf("%s %s deliveries=%d visible=%v", test.name, action, n, test.visible)
			}
		}
	}
	// Visibility is evaluated when queued, after the wall changes, even for older events.
	up := stage2f(t, c.Admin.Client, c.Open, "late-wall.txt")
	f := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up})
	c.Wall(c.Open, c.Attorney)
	c.DeliverEvents()
	if c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id=$1 AND payload LIKE $2`, module, "%"+str(t, f.JSON, "id")+"%") != 0 {
		t.Error("event crossed wall erected before enqueue")
	}
}

func TestCoreUpdate2FFixConflictNameLimits(t *testing.T) {
	c := coretest.New(t)
	path := "/api/v1/matters/" + c.Open + "/conflict-checks"
	for _, names := range [][]string{strings.Fields(strings.Repeat("Synthetic ", 21)), {strings.Repeat("界", 201)}} {
		if r := c.Staff.Do("POST", path, map[string]any{"names": names}); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "names") {
			t.Errorf("unbounded names: %d %s", r.Code, r.Body)
		}
	}
	c.Staff.Must(http.StatusCreated, "POST", path, map[string]any{"names": []string{strings.Repeat("界", 200)}})
	for key, value := range map[string]string{"practice.conflict_additional_names_max": "2", "practice.conflict_name_max_chars": "3"} {
		c.Admin.Must(http.StatusOK, "PATCH", "/api/v1/settings/"+key, map[string]any{"value": value})
	}
	for _, names := range [][]string{{"A", "B", "C"}, {"四字姓名"}} {
		if r := c.Staff.Do("POST", path, map[string]any{"names": names}); r.Code != http.StatusBadRequest {
			t.Errorf("live names limit: %d %s", r.Code, r.Body)
		}
	}
	c.Staff.Must(http.StatusCreated, "POST", path, map[string]any{"names": []string{"甲乙丙", "ABC"}})
	before := c.Count(`SELECT COUNT(*) FROM core.practice_records WHERE kind='run'`)
	args := map[string]any{"path": map[string]any{"matter_id": c.Open}, "body": map[string]any{"names": []string{"Long"}}}
	if result := toolResult2d(t, c.Staff.Client, "runConflictCheck", args, true); result["code"] != "validation" || result["field"] != "names" {
		t.Errorf("MCP names limit: %v", result)
	}
	if c.Count(`SELECT COUNT(*) FROM core.practice_records WHERE kind='run'`) != before {
		t.Error("rejected MCP check persisted")
	}
}

func TestCoreUpdate2FFixProcessorAuditIdentity(t *testing.T) {
	c, processor, consumer, _ := processorFixture(t)
	path := "/api/v1/modules/contracts/" + modulekit.ProcessorPaymentContract + "/"
	for _, intent := range []string{"audit-one", "audit-two"} {
		processor.LoseNextApprovalResponse()
		consumer.Must(http.StatusAccepted, "POST", path+"authorize", processorRequest(intent))
		consumer.Must(http.StatusOK, "POST", path+"recover", map[string]string{"intent_id": intent})
		if c.Count(`SELECT COUNT(DISTINCT entity_id) FROM core.audit_log WHERE entity='module_processor_intent' AND entity_id=$1`, "module-processor/synthetic-intake/"+intent) != 1 {
			t.Errorf("intent %s has no stable audit identity", intent)
		}
		if c.Count(`SELECT COUNT(*) FROM core.audit_log WHERE entity='module_processor_intent' AND entity_id=$1 AND field='status'`, "module-processor/synthetic-intake/"+intent) < 2 {
			t.Errorf("intent %s transitions not linked", intent)
		}
	}
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var leaked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM core.audit_log WHERE entity='module_processor_intent' AND (new_value LIKE '%synthetic-hosted%' OR new_value LIKE '%2500%')`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("processor audit includes token or amount")
	}
}

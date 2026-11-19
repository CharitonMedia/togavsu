package coretest_test

import (
	"bytes"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestCoreUpdate2ESenderPersistsAcrossUnlink(t *testing.T) {
	c := coretest.New(t)
	const contract = "phone.provider.v1"
	const consumer = "phone-consumer"
	const zoom = "phone-zoom"
	const google = "phone-google"
	type delivery struct {
		sender string
		body   []byte
	}
	var mu sync.Mutex
	var events, calls, coreEvents []delivery
	handler := func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, body, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Errorf("sender verification: %v", err)
				w.WriteHeader(401)
				return
			}
			if err := modulekit.VerifyWebhookHeaders(m.WebhookSecret, r.Header, body, time.Now(), 0); err != nil {
				t.Errorf("2d verification: %v", err)
			}
			// An intermediary changing only identity must fail despite the valid v1 MAC.
			tampered := r.Header.Clone()
			tampered.Set(modulekit.SenderHeader, "forged-module")
			if got, err := modulekit.VerifyWebhookSender(m.WebhookSecret, tampered, body, time.Now(), 0); err == nil || got != "" {
				t.Error("sender tampering accepted")
			}
			var envelope map[string]any
			if err := json.Unmarshal(body, &envelope); err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			d := delivery{sender, body}
			if r.URL.Path == "/call" {
				calls = append(calls, d)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			} else if envelope["contract"] == contract {
				events = append(events, d)
				// Refuse the first attempt so the persisted queue must deliver a retry.
				if len(events) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			} else {
				coreEvents = append(coreEvents, d)
				w.WriteHeader(http.StatusNoContent)
			}
		})
	}
	for _, id := range []string{consumer, zoom, google} {
		c.Serve(id, handler)
	}
	install2d(t, c, consumer, []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	for _, id := range []string{zoom, google} {
		install2d(t, c, id, []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    many: true\n    operations:\n      - name: call\n        path: /call\n")
	}
	links := map[string]string{}
	for _, id := range []string{zoom, google} {
		r := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "consumer_id": consumer, "provider_id": id})
		links[id] = str(t, r.JSON, "id")
	}
	if r := c.ModuleClient(consumer).Do("POST", "/api/v1/modules/contracts/"+contract+"/call", map[string]any{"sender": google}, "X-Togavsu-Contract-Provider", zoom); r.Code != http.StatusOK {
		t.Fatalf("contract call: %d %s", r.Code, r.Body)
	}
	envelope := map[string]any{"id": "synthetic-spoofed-provider-event", "contract": contract, "consumer_id": consumer, "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "action": contract + ".recording", "payload": map[string]any{"provider_id": google, "sender": google}}
	c.ModuleClient(zoom).Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	// Duplicate ids cannot replace the original pusher or payload, even from another
	// linked provider. The event remains the first accepted immutable delivery.
	c.ModuleClient(google).Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
	if c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE event_id=$1 AND created_by=$2`, envelope["id"], zoom) != 1 {
		t.Fatal("pusher was not persisted")
	}
	c.DeliverEvents()
	mu.Lock()
	if len(events) != 1 || events[0].sender != zoom {
		t.Fatalf("first event sender: %+v", events)
	}
	if len(calls) != 1 || calls[0].sender != consumer {
		t.Fatalf("contract call sender: %+v", calls)
	}
	mu.Unlock()
	if c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE event_id=$1 AND status='pending' AND attempts=1`, envelope["id"]) != 1 {
		t.Fatal("refused event was not queued for retry")
	}
	c.Admin.Must(http.StatusNoContent, "DELETE", "/api/v1/modules/contract-links/"+links[zoom], nil)
	// Advance only this persisted delivery to due; no clock sleeps or retry-policy changes.
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.module_deliveries SET next_attempt_at=0 WHERE event_id=$1`, envelope["id"]); err != nil {
		t.Fatal(err)
	}
	c.DeliverEvents()
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 || events[1].sender != zoom || !bytes.Equal(events[0].body, events[1].body) {
		t.Fatalf("retried event changed: %+v", events)
	}
	var got map[string]any
	if err := json.Unmarshal(events[1].body, &got); err != nil {
		t.Fatal(err)
	}
	if got["payload"].(map[string]any)["provider_id"] != google {
		t.Fatal("provider payload was rewritten")
	}
	if len(coreEvents) == 0 {
		t.Fatal("no core events received")
	}
	for _, d := range coreEvents {
		if d.sender != "core" {
			t.Fatalf("core event sender %q", d.sender)
		}
	}
	if c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE event_id=$1 AND status='delivered' AND attempts=2 AND created_by=$2`, envelope["id"], zoom) != 1 {
		t.Fatal("retry lost persisted sender")
	}
	// The unlinked provider cannot queue a new event after unlink, while the old
	// delivery's authenticated sender still identifies that provider.
	envelope["id"] = "synthetic-after-unlink"
	c.ModuleClient(zoom).Must(http.StatusForbidden, "POST", "/api/v1/modules/contracts/"+contract+"/events", envelope)
}

func TestCoreUpdate2EReservedSender(t *testing.T) {
	c := coretest.New(t)
	files, err := matterclock.Files()
	if err != nil {
		t.Fatal(err)
	}
	files[modulekit.ManifestFile] = bytes.Replace(files[modulekit.ManifestFile], []byte("id: matter-clock"), []byte("id: core"), 1)
	if _, err := modulekit.ParseManifest(files[modulekit.ManifestFile]); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("runtime manifest must reject reserved id: %v", err)
	}
	if _, _, err := modulekit.Check(files); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("kit install checks must reject reserved id: %v", err)
	}
	// Bypass author-tool validation to exercise an actually signed malicious package.
	raw, err := modulekit.Archive(files)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.TrustKey("synthetic-reserved-id", pub)
	signed, err := json.Marshal(modulekit.SignedPackage{ListingID: "core", Version: "1.0.0", Digest: modulekit.Digest(raw), Signature: modulekit.Sign(priv, raw), KeyID: modulekit.KeyID(pub), PackageBase64: base64.StdEncoding.EncodeToString(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if r := c.TryInstall(signed); r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "reserved") {
		t.Fatalf("reserved install: %d %s", r.Code, r.Body)
	}
	if c.Count(`SELECT COUNT(*) FROM core.modules WHERE id='core'`) != 0 {
		t.Fatal("reserved module was persisted")
	}
	const contract = "phone.provider.v1"
	for _, link := range []map[string]any{
		{"contract": contract, "consumer_id": "core", "provider_id": "phone-zoom"},
		{"contract": contract, "consumer_id": "phone-consumer", "provider_id": "core"},
	} {
		r := c.Admin.Do("POST", "/api/v1/modules/contract-links", link)
		if r.Code != http.StatusBadRequest || !strings.Contains(r.Body, "reserved") {
			t.Fatalf("reserved link: %d %s", r.Code, r.Body)
		}
	}
	// Model a delivery left by an older installation: nonempty created_by=core
	// must never be upgraded into a valid core signature by the production pump.
	var delivered atomic.Bool
	c.Serve("phone-consumer", func(m coretest.Module) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sender, body, err := coretest.ReadDelivery(m, r)
			if err != nil {
				t.Error(err)
			}
			var event map[string]any
			if err := json.Unmarshal(body, &event); err != nil {
				t.Error(err)
			}
			if event["id"] == "synthetic-reserved-sender" {
				delivered.Store(true)
			} else if sender != "core" {
				t.Errorf("core emission has sender %q", sender)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	install2d(t, c, "phone-consumer", []string{"contracts:call"}, "consumes:\n  - contract: "+contract+"\n")
	install2d(t, c, "phone-zoom", []string{"contracts:provide"}, "provides:\n  - contract: "+contract+"\n    operations:\n      - name: call\n        path: /call\n")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/modules/contract-links", map[string]any{"contract": contract, "consumer_id": "phone-consumer", "provider_id": "phone-zoom"})
	c.ModuleClient("phone-zoom").Must(http.StatusNoContent, "POST", "/api/v1/modules/contracts/"+contract+"/events", map[string]any{"id": "synthetic-reserved-sender", "contract": contract, "consumer_id": "phone-consumer", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano), "action": contract + ".recording", "payload": map[string]any{"sender": "core"}})
	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE core.module_deliveries SET created_by='core' WHERE event_id='synthetic-reserved-sender'`); err != nil {
		t.Fatal(err)
	}
	c.DeliverEvents()
	if delivered.Load() || c.Count(`SELECT COUNT(*) FROM core.module_deliveries WHERE event_id='synthetic-reserved-sender' AND status='pending' AND attempts=1 AND last_error LIKE '%reserved%'`) != 1 {
		t.Fatal("module-originated reserved sender reached the receiver or was not refused")
	}
}

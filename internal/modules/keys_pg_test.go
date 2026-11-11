package modules_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// AC-013 over the contract's trusted-signing-keys API (Codex checkpoint 2 on PR #38):
// an admin adds a PEM key, a package signed by it installs, and once the key is deleted
// a package signed by it is refused as unsigned. Every response matches TrustedKey.
func TestPGTrustedSigningKeysAPI(t *testing.T) {
	e := newEnv(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := modules.PublicKeyPEM(pub)
	body := map[string]any{"name": "Firm key", "public_key_pem": pemKey}
	if r := e.do("POST", "/api/v1/trusted-signing-keys", body, e.as("staff")...); r.code != http.StatusForbidden {
		t.Fatalf("staff added a key: %d", r.code)
	}
	// The old base64 field and a non-ed25519 or garbled PEM are refused.
	if r := e.do("POST", "/api/v1/trusted-signing-keys", map[string]any{"name": "x", "public_key": base64.StdEncoding.EncodeToString(pub)}, e.as("admin")...); r.code != http.StatusBadRequest {
		t.Fatalf("base64 public_key accepted: %d %s", r.code, r.body)
	}
	for _, bad := range []string{base64.StdEncoding.EncodeToString(pub), "-----BEGIN PUBLIC KEY-----\nnope\n-----END PUBLIC KEY-----\n", pemKey + pemKey} {
		if r := e.do("POST", "/api/v1/trusted-signing-keys", map[string]any{"name": "x", "public_key_pem": bad}, e.as("admin")...); r.code != http.StatusBadRequest || r.json["field"] != "public_key_pem" {
			t.Fatalf("bad PEM accepted: %d %s", r.code, r.body)
		}
	}
	r := e.do("POST", "/api/v1/trusted-signing-keys", body, e.as("admin")...)
	if r.code != http.StatusCreated || r.json["public_key_pem"] != pemKey || r.json["name"] != "Firm key" {
		t.Fatalf("create: %d %s", r.code, r.body)
	}
	if err := shapeOf(t, "TrustedKey", r.json); err != nil {
		t.Fatalf("create response outside the contract: %v (%s)", err, r.body)
	}
	id := r.json["id"].(string)
	if r := e.do("POST", "/api/v1/trusted-signing-keys", body, e.as("admin")...); r.code != http.StatusConflict {
		t.Fatalf("duplicate key: %d %s", r.code, r.body)
	}

	list := e.do("GET", "/api/v1/trusted-signing-keys", nil, e.as("admin")...)
	data, _ := list.json["data"].([]any)
	if list.code != http.StatusOK || len(data) != 2 || list.json["page"] == nil { // the harness key and this one
		t.Fatalf("list: %d %s", list.code, list.body)
	}
	for _, k := range data {
		if err := shapeOf(t, "TrustedKey", k); err != nil {
			t.Fatalf("listed key outside the contract: %v", err)
		}
	}
	if p := e.do("GET", "/api/v1/trusted-signing-keys?limit=1", nil, e.as("admin")...); len(p.json["data"].([]any)) != 1 || p.json["page"].(map[string]any)["next_cursor"] == nil {
		t.Fatalf("page of one: %s", p.body)
	}

	// Patch renames; the key keeps working.
	r = e.do("PATCH", "/api/v1/trusted-signing-keys/"+id, map[string]any{"name": "Renamed"}, e.as("admin")...)
	if r.code != http.StatusOK || r.json["name"] != "Renamed" || r.json["public_key_pem"] != pemKey || shapeOf(t, "TrustedKey", r.json) != nil {
		t.Fatalf("patch: %d %s", r.code, r.body)
	}
	if g := e.do("GET", "/api/v1/trusted-signing-keys/"+id, nil, e.as("admin")...); g.code != http.StatusOK || g.json["name"] != "Renamed" {
		t.Fatalf("get: %d %s", g.code, g.body)
	}
	// Patching to a key already on record is a conflict.
	harness := e.do("GET", "/api/v1/trusted-signing-keys/"+e.keyID, nil, e.as("admin")...)
	if c := e.do("PATCH", "/api/v1/trusted-signing-keys/"+id, map[string]any{"public_key_pem": harness.json["public_key_pem"]}, e.as("admin")...); c.code != http.StatusConflict {
		t.Fatalf("patch onto another key: %d %s", c.code, c.body)
	}

	// Delete the harness key so only the API-added key verifies.
	if d := e.do("DELETE", "/api/v1/trusted-signing-keys/"+e.keyID, nil, e.as("admin")...); d.code != http.StatusNoContent {
		t.Fatalf("delete harness key: %d %s", d.code, d.body)
	}
	signed, err := matterclock.Build(priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/modules", signed, e.as("admin")...); r.code != http.StatusAccepted {
		t.Fatalf("install signed by the API-added key: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/api/v1/modules", e.build(nil), e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("a deleted key still verifies: %d %s", r.code, r.body)
	}
	if d := e.do("DELETE", "/api/v1/trusted-signing-keys/"+id, nil, e.as("admin")...); d.code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", d.code, d.body)
	}
	if g := e.do("GET", "/api/v1/trusted-signing-keys/"+id, nil, e.as("admin")...); g.code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", g.code)
	}
	if d := e.do("DELETE", "/api/v1/trusted-signing-keys/"+id, nil, e.as("admin")...); d.code != http.StatusNotFound {
		t.Fatalf("second delete: %d", d.code)
	}
	// Adding the deleted key again restores it under its id.
	if r := e.do("POST", "/api/v1/trusted-signing-keys", body, e.as("admin")...); r.code != http.StatusCreated || r.json["id"] != id {
		t.Fatalf("re-add: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.audit_log WHERE field LIKE 'trusted_key:%' AND entity = 'module:keys'`) < 4 {
		t.Fatal("key changes aren't audited")
	}
	if strings.Contains(list.body, "\"key_id\"") {
		t.Fatal("the list carries fields outside the contract")
	}
}

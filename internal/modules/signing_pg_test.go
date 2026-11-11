package modules_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	service "github.com/CharitonMedia/togavsu/marketplace"
)

// The marketplace component owns the trust root (Codex checkpoint on 4c8b375): a key
// the admin adds through the one mounted trusted-signing-keys API lets a package signed
// with it install, and removing it there stops the next install.
func TestPGTrustedKeyAPILetsPackageInstall(t *testing.T) {
	e := newEnv(t)
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := matterclock.Build(priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/api/v1/modules", signed, e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("before the key is trusted: %d %s", r.code, r.body)
	}
	add := e.do("POST", "/api/v1/trusted-signing-keys", map[string]any{"name": "Firm build key", "public_key_pem": marketplace.PublicKeyPEM(pub)}, e.as("admin")...)
	if add.code != http.StatusCreated || add.json["key_id"] != marketplace.KeyID(pub) {
		t.Fatalf("add key: %d %s", add.code, add.body)
	}
	if r := e.do("POST", "/api/v1/modules", signed, e.as("admin")...); r.code != http.StatusAccepted || r.json["status"] != modules.StatusAwaiting {
		t.Fatalf("install with the trusted key: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_packages WHERE key_id = $1`, marketplace.KeyID(pub)) != 1 {
		t.Fatal("the package row doesn't name the signing key")
	}
	if r := e.do("DELETE", "/api/v1/trusted-signing-keys/"+add.json["id"].(string), nil, e.as("admin")...); r.code != http.StatusNoContent {
		t.Fatalf("remove key: %d %s", r.code, r.body)
	}
	next, _ := matterclock.Build(priv, func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "1.0.1"`), 1)
	})
	if r := e.do("POST", "/api/v1/modules/matter-clock/update", next, e.as("admin")...); r.code != http.StatusUnprocessableEntity || r.json["code"] != modules.CodeUnsigned {
		t.Fatalf("update after the key was removed: %d %s", r.code, r.body)
	}
}

// A package signed and served the marketplace's way installs from the marketplace, with
// the signer recorded as a marketplace key (PRD 9.3: keys published through it).
func TestPGInstallFromMarketplace(t *testing.T) {
	e := newEnv(t)
	cfg := service.Defaults()
	cfg.SecureCookies = false
	m, err := service.Open(t.TempDir(), cfg.AuthorSession(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := service.NewServer(m, cfg, "synthetic-operator-token-0123456789abcdef", nil)
	if err != nil {
		t.Fatal(err)
	}
	market := httptest.NewServer(srv.Handler())
	t.Cleanup(market.Close)
	call := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, market.URL+path, bytes.NewReader(raw))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	call("POST", "/v1/authors", "", map[string]any{"name": "Synthetic Author", "email": "author@example.test", "password": "synthetic author password"})
	_, b := call("POST", "/v1/sessions", "", map[string]any{"email": "author@example.test", "password": "synthetic author password"})
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, b = call("POST", "/v1/authors/me/keys", tok.Token, map[string]any{"public_key_pem": marketplace.PublicKeyPEM(pub)})
	var k service.KeyView
	_ = json.Unmarshal(b, &k)
	if code, b := call("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok.Token, map[string]any{"signature": marketplace.Sign(priv, marketplace.KeyProofMessage(k.KeyID, k.Challenge))}); code >= 300 {
		t.Fatalf("verify key: %d %s", code, b)
	}
	pkg, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, b := call("POST", "/v1/authors/me/packages", tok.Token, map[string]any{"package_base64": base64.StdEncoding.EncodeToString(pkg), "signature": marketplace.Sign(priv, pkg), "key_id": k.KeyID}); code != http.StatusCreated {
		t.Fatalf("publish the block-YAML sample: %d %s", code, b)
	}
	if _, err := e.app.Settings.Set(e.ctx, "modules.marketplace_address", market.URL, e.admin); err != nil {
		t.Fatal(err)
	}
	r := e.do("POST", "/api/v1/modules", map[string]any{"source": "marketplace", "listing_id": "matter-clock", "version": "1.0.0"}, e.as("admin")...)
	if r.code != http.StatusAccepted || r.json["status"] != modules.StatusAwaiting || e.count(`SELECT COUNT(*) FROM core.modules WHERE id = 'matter-clock' AND source = 'marketplace'`) != 1 {
		t.Fatalf("marketplace install: %d %s", r.code, r.body)
	}
	if e.count(`SELECT COUNT(*) FROM core.module_packages WHERE key_id = $1 AND digest = $2`, k.KeyID, marketplace.Digest(pkg)) != 1 {
		t.Fatal("the package row doesn't carry the marketplace signer and digest")
	}
	if r := e.approveAll("matter-clock"); r.code != http.StatusOK {
		t.Fatalf("approve: %d %s", r.code, r.body)
	}
}

// The marketplace reads the module runtime's block-YAML manifest with the same parser.
func TestMarketplaceReadsSampleManifest(t *testing.T) {
	pkg, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := marketplace.ReadManifest(pkg)
	if err != nil || m.ID != "matter-clock" || m.Version != "1.0.0" || len(m.Permissions) == 0 {
		t.Fatalf("marketplace manifest %+v %v", m, err)
	}
	local, err := modules.ReadPackage(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(local.Manifest.Permissions) != len(m.Permissions) {
		t.Fatalf("the two sides read %d and %d permissions", len(local.Manifest.Permissions), len(m.Permissions))
	}
}

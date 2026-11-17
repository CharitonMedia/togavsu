package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	service "github.com/CharitonMedia/togavsu/marketplace"
)

// TestExtensionsMarketplaceCatalog is the desk-fix catalog on the admin Modules tab:
// browse with an empty search, notice and signing key on every listing, price text and
// Free, flagged warning, install / installed / update / newer-installed states, the
// unreachable message, a failed install's plain sentence, and a successful install
// landing in Installed modules with permissions pending.
func TestExtensionsMarketplaceCatalog(t *testing.T) {
	x := newExt(t)
	addr, call, authorKey, keyID := publishCatalog(t)
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": addr})

	// Marketplace card is first on the Modules tab; empty search browses every listing.
	tab := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	marketAt := strings.Index(tab.body, `id="marketplace"`)
	alertsAt := strings.Index(tab.body, `id="module-alerts"`)
	if marketAt < 0 || alertsAt < 0 || marketAt > alertsAt {
		t.Fatalf("marketplace card must sit above alerts: market=%d alerts=%d", marketAt, alertsAt)
	}
	for _, id := range []string{"listing-matter-clock", "listing-mod-alpha", "listing-mod-beta"} {
		if !strings.Contains(tab.body, `id="`+id+`"`) {
			t.Fatalf("empty browse missing %s", id)
		}
	}
	if !strings.Contains(tab.body, marketplace.Notice) {
		t.Fatal("PRD 10.2 notice missing on the catalog")
	}
	if !strings.Contains(tab.body, "signed by") || !strings.Contains(tab.body, keyID) {
		t.Fatal("signing key missing on listings")
	}
	if !strings.Contains(tab.body, "$5 per month") || !strings.Contains(tab.body, "Free") {
		t.Fatal("price text and Free missing")
	}
	if !strings.Contains(tab.body, "Permissions (") || !strings.Contains(tab.body, "<details") {
		t.Fatal("permissions details missing")
	}
	if !strings.Contains(tab.body, ">Install<") {
		t.Fatal("Install button missing")
	}

	// Unreachable placeholder address.
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": "togavsu-marketplace"})
	down := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(down.body, "The marketplace isn't reachable at togavsu-marketplace.") {
		t.Fatalf("unreachable message: %s", down.body)
	}
	if !strings.Contains(down.body, `href="/admin?tab=modules#s-modules.marketplace_address"`) {
		t.Fatal("settings link missing")
	}
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": addr})

	// Compose http note.
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": "http://togavsu-marketplace:8090"})
	compose := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(compose.body, "only your trusted local keys can approve an install") || !strings.Contains(compose.body, `href="#trusted-keys"`) {
		t.Fatalf("compose http note: %s", compose.body)
	}
	keysHint := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(keysHint.body, "Over https or loopback, keys published through the marketplace") {
		t.Fatal("trusted-keys hint missing")
	}
	x.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/modules.marketplace_address", map[string]any{"value": addr})

	// Successful install lands in Installed modules with permissions pending.
	if _, problem := x.tabForm(x.admin, "install-listing", url.Values{"listing_id": {"matter-clock"}, "version": {"1.0.0"}}, nil); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	pending := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(pending.body, `id="module-matter-clock"`) || !strings.Contains(pending.body, `name="scope:`) {
		t.Fatalf("installed pending permissions: %s", pending.body)
	}
	if !strings.Contains(pending.body, ">Installed<") {
		t.Fatal("Installed state missing after install")
	}
	x.approveOnTab("matter-clock")

	// Listed version newer → Update to X.Y.Z (mod-alpha stays at Install).
	publishMatterClockVersion(t, call, authorKey, keyID, "1.1.0")
	upd := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(upd.body, "Update to 1.1.0") {
		t.Fatalf("update button: %s", upd.body)
	}
	if _, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"matter-clock"}, "version": {"1.1.0"}}, nil); problem != "" {
		t.Fatalf("update from catalog: %s", problem)
	}

	// Installed newer than listed → Installed X.Y.Z, never a downgrade.
	x.trustKey("Catalog author", authorKey.Public().(ed25519.PublicKey))
	newer, err := matterclock.Build(authorKey, func(f map[string][]byte) {
		man := string(f["togavsu-module.yaml"])
		man = strings.Replace(man, `version: "1.0.0"`, `version: "2.0.0"`, 1)
		man = strings.Replace(man, "version: 1.0.0", "version: 2.0.0", 1)
		f["togavsu-module.yaml"] = []byte(man)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, problem := x.tabForm(x.admin, "update", url.Values{"module_id": {"matter-clock"}}, newer); problem != "" {
		t.Fatalf("local newer update: %s", problem)
	}
	ctx := context.Background()
	v, err := x.app.Modules.Get(ctx, x.adminP(), "matter-clock")
	if err != nil || v.Version != "2.0.0" {
		t.Fatalf("installed version %+v %v", v, err)
	}
	ahead := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(ahead.body, "Installed 2.0.0") {
		t.Fatalf("installed-newer label: %s", ahead.body)
	}
	if strings.Contains(ahead.body, "Update to 1.1.0") {
		t.Fatal("downgrade offered")
	}

	// Flagged warning on the listing.
	if code, b := call("POST", "/v1/operator/listings/mod-alpha/flag", extOperatorToken, map[string]any{"note": "Synthetic catalog flag."}); code != http.StatusOK {
		t.Fatalf("flag: %d %s", code, b)
	}
	flagged := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(flagged.body, html.EscapeString(marketplace.FlagWarning)) || !strings.Contains(flagged.body, "Synthetic catalog flag.") {
		t.Fatalf("flagged warning on listing: %s", flagged.body)
	}

	// Failed install shows a plain sentence (pulled module).
	if code, b := call("POST", "/v1/operator/listings/mod-beta/pull", extOperatorToken, map[string]any{"note": "Synthetic pull."}); code != http.StatusOK {
		t.Fatalf("pull: %d %s", code, b)
	}
	_, problem := x.tabForm(x.admin, "install-listing", url.Values{"listing_id": {"mod-beta"}, "version": {"1.0.0"}}, nil)
	if problem == "" || strings.Contains(problem, "yanked:") || strings.Contains(problem, ": this module was pulled") {
		t.Fatalf("plain install error: %q", problem)
	}
	if !strings.Contains(problem, "pulled") && !strings.Contains(problem, "can't be installed") {
		t.Fatalf("expected plain pulled sentence, got %q", problem)
	}
}

// publishCatalog publishes matter-clock (priced) plus two free modules.
func publishCatalog(t *testing.T) (string, func(method, path, token string, body any) (int, []byte), ed25519.PrivateKey, string) {
	t.Helper()
	cfg := service.Defaults()
	cfg.SecureCookies = false
	m, err := service.Open(t.TempDir(), cfg.AuthorSession(), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := service.NewServer(m, cfg, extOperatorToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	market := httptest.NewServer(srv.Handler())
	t.Cleanup(market.Close)
	call := func(method, path, token string, body any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, market.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
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
	price := "$5 per month"
	pkg, err := matterclock.Archive(nil)
	if err != nil {
		t.Fatal(err)
	}
	if code, b := call("POST", "/v1/authors/me/packages", tok.Token, map[string]any{
		"package_base64": base64.StdEncoding.EncodeToString(pkg), "signature": marketplace.Sign(priv, pkg), "key_id": k.KeyID, "price_note": price,
	}); code != http.StatusCreated {
		t.Fatalf("publish matter-clock: %d %s", code, b)
	}
	for _, id := range []string{"mod-alpha", "mod-beta"} {
		publishCatalogVersion(t, call, priv, k.KeyID, id, "1.0.0", &tok.Token)
	}
	return market.URL, call, priv, k.KeyID
}

func publishCatalogVersion(t *testing.T, call func(method, path, token string, body any) (int, []byte), priv ed25519.PrivateKey, keyID, id, version string, token *string) {
	t.Helper()
	man, _ := json.Marshal(map[string]any{"id": id, "name": "Module " + id, "version": version, "permissions": []string{"matters:read"}})
	pkg, err := marketplace.BuildPackage(man, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := authorToken(t, call, token)
	if code, b := call("POST", "/v1/authors/me/packages", auth, map[string]any{
		"package_base64": base64.StdEncoding.EncodeToString(pkg), "signature": marketplace.Sign(priv, pkg), "key_id": keyID,
	}); code != http.StatusCreated {
		t.Fatalf("publish %s %s: %d %s", id, version, code, b)
	}
}

func publishMatterClockVersion(t *testing.T, call func(method, path, token string, body any) (int, []byte), priv ed25519.PrivateKey, keyID, version string) {
	t.Helper()
	pkg, err := matterclock.Archive(func(f map[string][]byte) {
		man := string(f["togavsu-module.yaml"])
		man = strings.Replace(man, `version: "1.0.0"`, `version: "`+version+`"`, 1)
		man = strings.Replace(man, "version: 1.0.0", "version: "+version, 1)
		f["togavsu-module.yaml"] = []byte(man)
	})
	if err != nil {
		t.Fatal(err)
	}
	auth := authorToken(t, call, nil)
	if code, b := call("POST", "/v1/authors/me/packages", auth, map[string]any{
		"package_base64": base64.StdEncoding.EncodeToString(pkg), "signature": marketplace.Sign(priv, pkg), "key_id": keyID,
	}); code != http.StatusCreated {
		t.Fatalf("publish matter-clock %s: %d %s", version, code, b)
	}
}

func authorToken(t *testing.T, call func(method, path, token string, body any) (int, []byte), token *string) string {
	t.Helper()
	if token != nil {
		return *token
	}
	_, b := call("POST", "/v1/sessions", "", map[string]any{"email": "author@example.test", "password": "synthetic author password"})
	var tok struct{ Token string }
	_ = json.Unmarshal(b, &tok)
	return tok.Token
}

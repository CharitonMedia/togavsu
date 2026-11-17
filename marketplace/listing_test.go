package marketplace

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

func pkgWithPrice(t *testing.T, id, version string, price any, perms ...string) []byte {
	t.Helper()
	man := map[string]any{"id": id, "name": id + " Module", "version": version, "permissions": perms}
	if price != nil {
		man["price"] = price
	}
	raw, _ := json.Marshal(man)
	b, err := shared.BuildPackage(raw, map[string][]byte{"migrations/0001_init.sql": []byte("-- synthetic\n")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func signedFile(a author, p []byte) map[string]any {
	dig := shared.Digest(p)
	man, _ := shared.ReadManifest(p)
	var manifest map[string]any
	_ = json.Unmarshal(man.Raw, &manifest)
	return map[string]any{
		"listing_id":     man.ID,
		"version":        man.Version,
		"immutable":      true,
		"manifest":       manifest,
		"digest":         dig,
		"signature":      shared.Sign(a.priv, p),
		"key_id":         a.keyID,
		"package_base64": base64.StdEncoding.EncodeToString(p),
	}
}

func (e *env) publishPriced(a author, id, version string, price any) []byte {
	e.t.Helper()
	pkg := pkgWithPrice(e.t, id, version, price, "matters:read")
	r := e.do("POST", "/v1/authors/me/packages", a.token, signedFile(a, pkg))
	if r.code != http.StatusCreated {
		e.t.Fatalf("publish %d %s", r.code, r.body)
	}
	return pkg
}

// MKT-01: structured price lists at that price; old string still lists; provider disclaimer.
func TestMKT01StructuredPrice(t *testing.T) {
	e := newEnv(t)
	a := e.author("Paid Author", "paid@example.test")
	structured := map[string]any{"amount_cents": 4900, "currency": "USD", "interval": "month", "note": "Missouri estate planning"}
	e.publishPriced(a, "mo-estate-planning", "1.0.0", structured)
	var body map[string]any
	e.do("GET", "/v1/listings/mo-estate-planning", "", nil).json(t, &body)
	if body["price_note"] == "" || !strings.Contains(body["price_note"].(string), "USD") || !strings.Contains(body["price_note"].(string), "49.00") {
		t.Fatalf("structured display: %+v", body["price_note"])
	}
	price, _ := body["price"].(map[string]any)
	if int(price["amount_cents"].(float64)) != 4900 || price["currency"] != "USD" || price["interval"] != "month" {
		t.Fatalf("structured price: %+v", price)
	}
	disc, _ := body["provider_disclaimer"].(string)
	wantDisc := "Sold and licensed by Paid Author. Togavsu doesn't sell or bill this module."
	if disc != wantDisc {
		t.Fatalf("disclaimer: %q", disc)
	}
	page := e.do("GET", "/listings/mo-estate-planning", "", nil)
	if page.code != http.StatusOK || (!strings.Contains(string(page.body), wantDisc) && !strings.Contains(string(page.body), htmlEscape(wantDisc))) {
		t.Fatalf("listing page missing disclaimer: %d %s", page.code, page.body)
	}
	b := e.author("Free Author", "free@example.test")
	e.publishPriced(b, "documents", "1.0.0", "Free")
	e.do("GET", "/v1/listings/documents", "", nil).json(t, &body)
	if body["price_note"] != "Free" {
		t.Fatalf("string price: %+v", body)
	}
	if !strings.Contains(body["provider_disclaimer"].(string), "Free Author") {
		t.Fatalf("string listing disclaimer: %+v", body["provider_disclaimer"])
	}
}

// MKT-02: kit signed file unchanged succeeds; tampered refused.
func TestMKT02SignedUpload(t *testing.T) {
	e := newEnv(t)
	a := e.author("Upload Author", "upload@example.test")
	p := pkgWithPrice(t, "matter-clock", "1.0.0", "Included", "matters:read")
	good := signedFile(a, p)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, good); r.code != http.StatusCreated {
		t.Fatalf("good signed file: %d %s", r.code, r.body)
	}
	tampered := append([]byte{}, p...)
	tampered[len(tampered)-1] ^= 0xff
	bad := signedFile(a, p)
	bad["package_base64"] = base64.StdEncoding.EncodeToString(tampered)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, bad); r.code != http.StatusBadRequest {
		t.Fatalf("tampered digest left: %d %s", r.code, r.body)
	}
	bad2 := signedFile(a, p)
	bad2["digest"] = "sha256:" + strings.Repeat("0", 64)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, bad2); r.code != http.StatusBadRequest {
		t.Fatalf("wrong digest: %d %s", r.code, r.body)
	}
}

// MKT-03: author edits listed price and sales link without a new package; install untouched.
func TestMKT03PriceEdit(t *testing.T) {
	e := newEnv(t)
	a := e.author("Price Author", "price@example.test")
	pkg := e.publishPriced(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 4900, "currency": "USD", "interval": "month",
	})
	before := e.do("GET", "/v1/listings/mo-estate-planning/versions/1.0.0/package", "", nil)
	if before.code != http.StatusOK {
		t.Fatalf("package before: %d %s", before.code, before.body)
	}
	var beforePkg shared.SignedPackage
	before.json(t, &beforePkg)
	r := e.do("PATCH", "/v1/authors/me/listings/mo-estate-planning/price", a.token, map[string]any{
		"amount_cents": 5900, "currency": "USD", "interval": "month", "note": "raised",
		"sales_url": "https://author.example.test/buy/mo-estate-planning",
	})
	if r.code != http.StatusOK {
		t.Fatalf("price edit: %d %s", r.code, r.body)
	}
	var body map[string]any
	r.json(t, &body)
	price, _ := body["price"].(map[string]any)
	if int(price["amount_cents"].(float64)) != 5900 {
		t.Fatalf("new price: %+v", price)
	}
	if body["sales_url"] != "https://author.example.test/buy/mo-estate-planning" {
		t.Fatalf("sales_url: %+v", body["sales_url"])
	}
	if !strings.Contains(body["price_note"].(string), "59.00") {
		t.Fatalf("price_note: %+v", body["price_note"])
	}
	after := e.do("GET", "/v1/listings/mo-estate-planning/versions/1.0.0/package", "", nil)
	if after.code != http.StatusOK {
		t.Fatalf("package after: %d %s", after.code, after.body)
	}
	var afterPkg shared.SignedPackage
	after.json(t, &afterPkg)
	if afterPkg.Digest != beforePkg.Digest || afterPkg.Digest != shared.Digest(pkg) {
		t.Fatalf("price edit must not touch install package: before=%s after=%s", beforePkg.Digest, afterPkg.Digest)
	}
	if afterPkg.PackageBase64 != beforePkg.PackageBase64 {
		t.Fatal("package bytes changed after price edit")
	}
}

// MKT-04: priced module installs from local marketplace with no key and no payment step.
func TestMKT04KeylessInstall(t *testing.T) {
	e := newEnv(t)
	a := e.author("Install Author", "install@example.test")
	pkg := e.publishPriced(a, "mo-estate-planning", "2.0.0", map[string]any{
		"amount_cents": 9900, "currency": "USD", "interval": "year",
	})
	r := e.do("GET", "/v1/listings/mo-estate-planning/versions/2.0.0/package", "", nil)
	if r.code != http.StatusOK {
		t.Fatalf("keyless package: %d %s", r.code, r.body)
	}
	lowBody := strings.ToLower(string(r.body))
	if strings.Contains(lowBody, "license") || strings.Contains(lowBody, "checkout") ||
		strings.Contains(lowBody, "payment") || strings.Contains(lowBody, "subscribe") {
		t.Fatalf("package response mentions billing: %s", r.body)
	}
	var sp shared.SignedPackage
	r.json(t, &sp)
	got, err := base64.StdEncoding.DecodeString(sp.PackageBase64)
	if err != nil || shared.Digest(got) != shared.Digest(pkg) {
		t.Fatalf("install package mismatch: %v", err)
	}
}

// MKT-05: no checkout, subscription, renewal, refund, payment-service, platform-fee or license-key
// route, setting or state record.
func TestMKT05NoBilling(t *testing.T) {
	e := newEnv(t)
	forbiddenPaths := []string{
		"/v1/listings/x/checkout",
		"/v1/stripe/checkout/complete",
		"/v1/stripe/webhook",
		"/v1/subscriptions/cancel",
		"/v1/subscriptions/renew",
		"/v1/licenses/renew",
		"/v1/license-public-key",
		"/v1/authors/me/stripe/connect",
		"/v1/operator/settings",
		"/v1/operator/mail/retry",
		"/v1/refunds",
		"/v1/payments",
	}
	for _, path := range forbiddenPaths {
		for _, method := range []string{"GET", "POST", "PATCH", "PUT", "DELETE"} {
			r := e.do(method, path, "", map[string]any{})
			if r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s must not exist: %d %s", method, path, r.code, r.body)
			}
		}
	}
	cfg := Defaults()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(raw))
	for _, bad := range []string{"stripe", "license", "platform_fee", "smtp", "checkout", "subscription", "webhook", "refund"} {
		if strings.Contains(low, bad) {
			t.Fatalf("config defaults mention %q: %s", bad, raw)
		}
	}
	st := reflect.TypeOf(State{})
	for i := 0; i < st.NumField(); i++ {
		name := strings.ToLower(st.Field(i).Name)
		tag := strings.ToLower(st.Field(i).Tag.Get("json"))
		for _, bad := range []string{"subscription", "license", "stripe", "mail", "payment", "fee", "refund", "invoice"} {
			if strings.Contains(name, bad) || strings.Contains(tag, bad) {
				t.Fatalf("state field %s looks like billing", st.Field(i).Name)
			}
		}
	}
	aType := reflect.TypeOf(Author{})
	for i := 0; i < aType.NumField(); i++ {
		name := strings.ToLower(aType.Field(i).Name)
		if strings.Contains(name, "stripe") {
			t.Fatalf("author field %s is Stripe-related", aType.Field(i).Name)
		}
	}
	for _, name := range []string{"stripe.go", "license.go", "mail.go", "stripe_fake_test.go", "mail_fake_test.go"} {
		if _, err := os.Stat(filepath.Join(".", name)); err == nil {
			t.Fatalf("%s must not exist after the provider-owned selling rebuild", name)
		}
	}
}

// MKT-06: no payment or license-key data in marketplace state or listing responses.
func TestMKT06NoPaymentOrKeyData(t *testing.T) {
	e := newEnv(t)
	a := e.author("Clean Author", "clean@example.test")
	e.publishPriced(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	e.do("PATCH", "/v1/authors/me/listings/mo-estate-planning/price", a.token, map[string]any{
		"amount_cents": 1200, "currency": "USD", "interval": "month",
		"sales_url": "https://clean.example.test/buy",
	})
	statePath := filepath.Join(e.dir, "state.json")
	b, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(b))
	for _, bad := range []string{
		"subscription", "license_key", "license", "stripe", "card", "sk_live", "sk_test",
		"whsec_", "payment", "checkout", "platform_fee", "paid_through", "refund",
	} {
		if strings.Contains(low, bad) {
			t.Fatalf("state contains %q: %s", bad, b)
		}
	}
	r := e.do("GET", "/v1/listings/mo-estate-planning", "", nil)
	low = strings.ToLower(string(r.body))
	for _, bad := range []string{"license_key", "stripe", "checkout", "subscription", "card_number", "sk_"} {
		if strings.Contains(low, bad) {
			t.Fatalf("listing response contains %q: %s", bad, r.body)
		}
	}
}

// MKT-07: test helpers refuse any outside host.
func TestMKT07NoOutsideHost(t *testing.T) {
	if err := RequireLocalBaseURL("http://127.0.0.1:9"); err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	if err := RequireLocalBaseURL("http://localhost:9"); err != nil {
		t.Fatalf("localhost refused: %v", err)
	}
	for _, bad := range []string{
		"https://api.stripe.com",
		"https://checkout.stripe.com",
		"https://example.com",
		"http://evil.example",
		"not-a-url",
		"",
	} {
		if err := RequireLocalBaseURL(bad); err == nil {
			t.Fatalf("outside host accepted: %q", bad)
		}
	}
}

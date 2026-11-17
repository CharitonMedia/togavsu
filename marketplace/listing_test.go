package marketplace

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
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

// kitSignedFile is the kit's SignFile output as a JSON object (immutable:false, manifest:null).
func kitSignedFile(t *testing.T, a author, p []byte) map[string]any {
	t.Helper()
	raw, err := modulekit.SignFile(a.priv, p)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func (e *env) publishPriced(a author, id, version string, price any) []byte {
	e.t.Helper()
	pkg := pkgWithPrice(e.t, id, version, price, "matters:read")
	r := e.do("POST", "/v1/authors/me/packages", a.token, kitSignedFile(e.t, a, pkg))
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

// MKT-02: kit SignFile body unchanged succeeds; a tampered byte is refused; metadata
// mismatches on listing_id, version, immutable and manifest are refused.
func TestMKT02SignedUpload(t *testing.T) {
	e := newEnv(t)
	a := e.author("Upload Author", "upload@example.test")
	p := pkgWithPrice(t, "matter-clock", "1.0.0", "Included", "matters:read")
	raw, err := modulekit.SignFile(a.priv, p)
	if err != nil {
		t.Fatal(err)
	}
	var good map[string]any
	if err := json.Unmarshal(raw, &good); err != nil {
		t.Fatal(err)
	}
	if r := e.do("POST", "/v1/authors/me/packages", a.token, good); r.code != http.StatusCreated {
		t.Fatalf("kit SignFile upload: %d %s", r.code, r.body)
	}

	// Tamper one byte of the kit file and refuse under DisallowUnknownFields + digest check.
	tamperedFile := append([]byte{}, raw...)
	// Flip a byte inside package_base64 (after the JSON keys) so the body still parses.
	idx := bytes.Index(tamperedFile, []byte(`"package_base64"`))
	if idx < 0 {
		t.Fatal("signed file missing package_base64")
	}
	for i := idx; i < len(tamperedFile); i++ {
		if tamperedFile[i] >= 'A' && tamperedFile[i] <= 'Z' {
			tamperedFile[i] ^= 0x01
			break
		}
	}
	var badFile map[string]any
	if err := json.Unmarshal(tamperedFile, &badFile); err != nil {
		t.Fatalf("tampered kit file must still be JSON: %v", err)
	}
	if r := e.do("POST", "/v1/authors/me/packages", a.token, badFile); r.code != http.StatusBadRequest {
		t.Fatalf("tampered kit file: %d %s", r.code, r.body)
	}

	p2 := pkgWithPrice(t, "matter-clock", "1.0.1", "Included", "matters:read")
	for _, tc := range []struct {
		field string
		patch func(map[string]any)
	}{
		{"listing_id", func(m map[string]any) { m["listing_id"] = "other-module" }},
		{"version", func(m map[string]any) { m["version"] = "9.9.9" }},
		{"immutable", func(m map[string]any) { m["immutable"] = true }},
		{"manifest", func(m map[string]any) {
			m["manifest"] = map[string]any{"id": "matter-clock", "name": "wrong", "version": "1.0.1", "permissions": []any{"matters:read"}}
		}},
	} {
		body := kitSignedFile(t, a, p2)
		tc.patch(body)
		if r := e.do("POST", "/v1/authors/me/packages", a.token, body); r.code != http.StatusBadRequest {
			t.Fatalf("tampered %s: %d %s", tc.field, r.code, r.body)
		}
	}
	// Correct marketplace-shaped metadata (immutable:true + matching manifest) still uploads.
	okMeta := kitSignedFile(t, a, p2)
	man, _ := shared.ReadManifest(p2)
	var pkgMan map[string]any
	_ = json.Unmarshal(man.Raw, &pkgMan)
	okMeta["immutable"] = true
	okMeta["manifest"] = pkgMan
	if r := e.do("POST", "/v1/authors/me/packages", a.token, okMeta); r.code != http.StatusCreated {
		t.Fatalf("matching immutable+manifest: %d %s", r.code, r.body)
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

	// §17.1: author may change a paid structured price to free (string / note).
	free := e.do("PATCH", "/v1/authors/me/listings/mo-estate-planning/price", a.token, map[string]any{
		"note": "Free",
	})
	if free.code != http.StatusOK {
		t.Fatalf("paid to free: %d %s", free.code, free.body)
	}
	free.json(t, &body)
	if body["price_note"] != "Free" {
		t.Fatalf("free price_note: %+v", body["price_note"])
	}
	price, _ = body["price"].(map[string]any)
	if price["amount_cents"] != nil && int(price["amount_cents"].(float64)) != 0 {
		t.Fatalf("free must clear amount: %+v", price)
	}

	// Currency / interval edits are normalised (trim + uppercase).
	norm := e.do("PATCH", "/v1/authors/me/listings/mo-estate-planning/price", a.token, map[string]any{
		"amount_cents": 100, "currency": "usd ", "interval": " month ",
	})
	if norm.code != http.StatusOK {
		t.Fatalf("normalise edit: %d %s", norm.code, norm.body)
	}
	norm.json(t, &body)
	price, _ = body["price"].(map[string]any)
	if price["currency"] != "USD" || price["interval"] != "month" {
		t.Fatalf("normalised price: %+v", price)
	}
	if !strings.Contains(body["price_note"].(string), "USD") || strings.Contains(body["price_note"].(string), "USD  ") {
		t.Fatalf("display after normalise: %+v", body["price_note"])
	}
}

func TestFractionalAmountCentsRefused(t *testing.T) {
	e := newEnv(t)
	a := e.author("Frac Author", "frac@example.test")
	man := map[string]any{
		"id": "frac-mod", "name": "Frac", "version": "1.0.0", "permissions": []string{"matters:read"},
		"price": map[string]any{"amount_cents": 49.99, "currency": "USD", "interval": "month"},
	}
	raw, _ := json.Marshal(man)
	pkg, err := shared.BuildPackage(raw, map[string][]byte{"migrations/0001_init.sql": []byte("-- synthetic\n")})
	if err != nil {
		t.Fatal(err)
	}
	r := e.do("POST", "/v1/authors/me/packages", a.token, kitSignedFile(t, a, pkg))
	if r.code != http.StatusBadRequest {
		t.Fatalf("fractional amount_cents: %d %s", r.code, r.body)
	}
	if !strings.Contains(strings.ToLower(string(r.body)), "amount_cents") {
		t.Fatalf("expected amount_cents error: %s", r.body)
	}
}

func TestConfigOverridesPriceAndSalesURL(t *testing.T) {
	e := newEnv(t)
	// Load config.example.json and override display / sales-url tunables.
	example, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	dec := json.NewDecoder(bytes.NewReader(example))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SecureCookies = false
	cfg.CentsPerMajorUnit = 10 // example default is 100; 100 cents → "10.00" with unit 10
	cfg.MaxSalesURLRunes = 40
	cfg.CurrencyCodeLength = 3
	if err := e.m.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := e.author("Config Author", "config@example.test")
	e.publishPriced(a, "config-mod", "1.0.0", map[string]any{
		"amount_cents": 100, "currency": "USD", "interval": "month",
	})
	var body map[string]any
	e.do("GET", "/v1/listings/config-mod", "", nil).json(t, &body)
	note, _ := body["price_note"].(string)
	if !strings.Contains(note, "10.00") || strings.Contains(note, "1.00") {
		t.Fatalf("ApplyConfig cents_per_major_unit must change display: %q", note)
	}
	long := "https://author.example.test/" + strings.Repeat("x", 40)
	r := e.do("PATCH", "/v1/authors/me/listings/config-mod/price", a.token, map[string]any{
		"sales_url": long,
	})
	if r.code != http.StatusBadRequest {
		t.Fatalf("max_sales_url_runes from config must bind: %d %s", r.code, r.body)
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

// MKT-07: test helpers refuse any outside host (wired into tryURL / do).
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
	e := newEnv(t)
	_, err := e.tryURL("GET", "https://api.stripe.com/v1/charges", "", nil)
	if !errors.Is(err, ErrOutsideHostRefused) {
		t.Fatalf("helper must refuse outside host before dialing: %v", err)
	}
	if r := e.do("GET", "/healthz", "", nil); r.code != http.StatusNoContent {
		t.Fatalf("local helper still works: %d %s", r.code, r.body)
	}
}

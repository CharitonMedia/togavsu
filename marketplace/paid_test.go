package marketplace

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

type paidEnv struct {
	*env
	fake   *FakeStripe
	mailer *RecordingMailer
}

func newPaidEnv(t *testing.T) *paidEnv {
	t.Helper()
	e := newEnv(t)
	e.m.AllowTestLicenseKeys()
	if err := e.m.EnsureLicenseKey(); err != nil {
		t.Fatal(err)
	}
	fake := StartFakeStripe()
	t.Cleanup(fake.Close)
	if err := RequireLocalStripeBase(fake.BaseURL); err != nil {
		t.Fatal(err)
	}
	stripe, err := NewStripe(fake.BaseURL, "sk_test_synthetic_not_a_real_secret")
	if err != nil {
		t.Fatal(err)
	}
	e.m.SetStripe(stripe)
	e.m.SetNoticeDays(30)
	e.m.SetWebhookSecret("whsec_test_synthetic_webhook")
	mailer := NewRecordingMailer(e.clock)
	e.m.SetMailer(mailer)
	return &paidEnv{env: e, fake: fake, mailer: mailer}
}

func (e *paidEnv) publishPaid(a author, id, version string, price any) {
	e.t.Helper()
	r := e.do("POST", "/v1/authors/me/packages", a.token, signedFile(a, pkgWithPrice(e.t, id, version, price, "matters:read")))
	if r.code != http.StatusCreated {
		e.t.Fatalf("publish %d %s", r.code, r.body)
	}
}

func (e *paidEnv) connect(a author) {
	e.t.Helper()
	r := e.do("POST", "/v1/authors/me/stripe/connect", a.token, map[string]any{})
	if r.code != http.StatusOK {
		e.t.Fatalf("connect %d %s", r.code, r.body)
	}
}

func (e *paidEnv) subscribe(listingID, installID, adminEmail string, installPriv ed25519.PrivateKey) (licenseKey string, sessionID string) {
	e.t.Helper()
	pub := installPriv.Public().(ed25519.PublicKey)
	r := e.do("POST", "/v1/listings/"+listingID+"/checkout", "", map[string]any{
		"install_id": installID, "admin_email": adminEmail,
		"install_public_key_pem": shared.PublicKeyPEM(pub),
		"success_url":            "https://example.test/ok",
		"cancel_url":             "https://example.test/no",
	})
	if r.code != http.StatusCreated {
		e.t.Fatalf("checkout %d %s", r.code, r.body)
	}
	var ck struct {
		SessionID string `json:"session_id"`
	}
	r.json(e.t, &ck)
	subID, _, _, stripeSub, err := e.fake.CompleteCheckout(ck.SessionID)
	if err != nil || subID == "" || stripeSub == "" {
		e.t.Fatalf("complete fake: %v %q %q", err, subID, stripeSub)
	}
	r = e.do("POST", "/v1/stripe/checkout/complete", "", map[string]any{
		"session_id": ck.SessionID,
	})
	if r.code != http.StatusOK {
		e.t.Fatalf("complete %d %s", r.code, r.body)
	}
	var out struct {
		LicenseKey string `json:"license_key"`
	}
	r.json(e.t, &out)
	if out.LicenseKey == "" {
		e.t.Fatal("empty license")
	}
	return out.LicenseKey, ck.SessionID
}

// MKT-01: structured price lists at that price; old string still lists.
func TestMKT01StructuredPrice(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Paid Author", "paid@example.test")
	structured := map[string]any{"amount_cents": 4900, "currency": "USD", "interval": "month", "note": "Missouri estate planning"}
	e.publishPaid(a, "mo-estate-planning", "1.0.0", structured)
	var body map[string]any
	e.do("GET", "/v1/listings/mo-estate-planning", "", nil).json(t, &body)
	if body["price_note"] == "" || !strings.Contains(body["price_note"].(string), "USD") || !strings.Contains(body["price_note"].(string), "49.00") {
		t.Fatalf("structured display: %+v", body["price_note"])
	}
	price, _ := body["price"].(map[string]any)
	if int(price["amount_cents"].(float64)) != 4900 || price["currency"] != "USD" || price["interval"] != "month" {
		t.Fatalf("structured price: %+v", price)
	}
	b := e.author("Free Author", "free@example.test")
	e.publishPaid(b, "documents", "1.0.0", "Free")
	e.do("GET", "/v1/listings/documents", "", nil).json(t, &body)
	if body["price_note"] != "Free" {
		t.Fatalf("string price: %+v", body)
	}
}

// MKT-02: kit signed file unchanged succeeds; tampered refused.
func TestMKT02SignedUpload(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Upload Author", "upload@example.test")
	p := pkgWithPrice(t, "matter-clock", "1.0.0", "Included", "matters:read")
	good := signedFile(a, p)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, good); r.code != http.StatusCreated {
		t.Fatalf("good signed file: %d %s", r.code, r.body)
	}
	// Tampered bytes with the digest left as signed.
	tampered := append([]byte{}, p...)
	tampered[len(tampered)-1] ^= 0xff
	bad := signedFile(a, p)
	bad["package_base64"] = base64.StdEncoding.EncodeToString(tampered)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, bad); r.code != http.StatusBadRequest {
		t.Fatalf("tampered digest left: %d %s", r.code, r.body)
	}
	// Tampered digest field.
	bad2 := signedFile(a, p)
	bad2["digest"] = "sha256:" + strings.Repeat("0", 64)
	if r := e.do("POST", "/v1/authors/me/packages", a.token, bad2); r.code != http.StatusBadRequest {
		t.Fatalf("wrong digest: %d %s", r.code, r.body)
	}
}

// MKT-03: subscribe via fake Stripe, key bound to install; other install refused.
func TestMKT03SubscribeLicense(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Estate Author", "estate@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 9900, "currency": "USD", "interval": "month", "note": "paid module",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := e.subscribe("mo-estate-planning", "install-firm-a", "admin@firm-a.test", installPriv)
	pubPEM := e.do("GET", "/v1/license-public-key", "", nil)
	var pk struct {
		PublicKeyPEM string `json:"public_key_pem"`
	}
	pubPEM.json(t, &pk)
	pub, err := shared.ParsePublicKeyPEM(pk.PublicKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckLicense(key, pub, "install-firm-a", "mo-estate-planning", e.m.now()); err != nil {
		t.Fatalf("own install refused: %v", err)
	}
	if err := CheckLicense(key, pub, "install-firm-b", "mo-estate-planning", e.m.now()); err == nil {
		t.Fatal("other install accepted")
	}
	if err := CheckLicense(key, pub, "install-firm-a", "other-module", e.m.now()); err == nil {
		t.Fatal("other module accepted")
	}
}

// MKT-04: price change emails subscribers; takes effect after notice at next Stripe-paid renewal.
func TestMKT04PriceChangeNotice(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Price Author", "price@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 5000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	e.subscribe("mo-estate-planning", "install-1", "admin@firm.test", installPriv)
	newPrice := ListingPrice{AmountCents: 7500, Currency: "USD", Interval: "month", Note: "raised"}
	r := e.do("PATCH", "/v1/authors/me/listings/mo-estate-planning/price", a.token, newPrice)
	if r.code != http.StatusOK {
		t.Fatalf("set price %d %s", r.code, r.body)
	}
	mails := e.m.Mails()
	if len(mails) != 1 || mails[0].To != "admin@firm.test" || !strings.Contains(mails[0].Body, "75.00") {
		t.Fatalf("notice mail: %+v", mails)
	}
	delivered := e.mailer.Delivered()
	if len(delivered) != 1 || delivered[0].To != "admin@firm.test" || !strings.Contains(delivered[0].Body, "75.00") {
		t.Fatalf("notice not delivered: %+v", delivered)
	}
	sub, _ := e.m.Subscription("install-1", "mo-estate-planning")
	if sub.Price.AmountCents != 5000 || sub.PendingPrice == nil || sub.PendingPrice.Price.AmountCents != 7500 {
		t.Fatalf("pending not set: %+v", sub)
	}
	throughBefore := sub.PaidThrough
	// Daily renew only re-serves the key; it must not extend paid_through or apply price.
	sig := shared.Sign(installPriv, RenewProofMessage("install-1", "mo-estate-planning"))
	key, through, err := e.m.RenewLicense("install-1", "mo-estate-planning", sig)
	if err != nil || key == "" {
		t.Fatalf("renew before notice: %v", err)
	}
	if !through.Equal(throughBefore) {
		t.Fatalf("daily renew extended paid_through: %v -> %v", throughBefore, through)
	}
	sub, _ = e.m.Subscription("install-1", "mo-estate-planning")
	if sub.Price.AmountCents != 5000 {
		t.Fatalf("price applied early: %+v", sub)
	}
	// Before notice ends, invoice.paid extends term but keeps the old price.
	if _, err := e.m.ApplyInvoicePaid(sub.StripeSubscription); err != nil {
		t.Fatalf("invoice before notice: %v", err)
	}
	sub, _ = e.m.Subscription("install-1", "mo-estate-planning")
	if sub.Price.AmountCents != 5000 || sub.PendingPrice == nil {
		t.Fatalf("price applied before notice via invoice: %+v", sub)
	}
	if !sub.PaidThrough.After(throughBefore) {
		t.Fatalf("invoice did not extend paid_through: %v", sub.PaidThrough)
	}
	if e.fake.SubscriptionAmount(sub.StripeSubscription) != 5000 {
		t.Fatalf("stripe amount changed early: %d", e.fake.SubscriptionAmount(sub.StripeSubscription))
	}
	// After notice period, next invoice.paid applies the new price and updates Stripe.
	e.now = e.now.Add(31 * 24 * time.Hour)
	payload := InvoicePaidEvent(sub.StripeSubscription)
	sigHeader := SignWebhook(payload, e.m.WebhookSecret(), e.m.now())
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/stripe/webhook", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", sigHeader)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("webhook %d", res.StatusCode)
	}
	sub, _ = e.m.Subscription("install-1", "mo-estate-planning")
	if sub.Price.AmountCents != 7500 || sub.PendingPrice != nil {
		t.Fatalf("price not applied: %+v", sub)
	}
	if e.fake.SubscriptionAmount(sub.StripeSubscription) != 7500 {
		t.Fatalf("stripe amount not updated: %d", e.fake.SubscriptionAmount(sub.StripeSubscription))
	}
	p, _ := e.m.ListingPriceOf("mo-estate-planning")
	if p.AmountCents != 7500 {
		t.Fatalf("listing price %+v", p)
	}
}

// MKT-05: cancel stops renewal; key runs to paid-through.
func TestMKT05Cancel(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Cancel Author", "cancel@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := e.subscribe("mo-estate-planning", "install-c", "admin@c.test", installPriv)
	pub, _ := shared.ParsePublicKeyPEM(e.m.LicensePublicKeyPEM())
	if err := CheckLicense(key, pub, "install-c", "mo-estate-planning", e.m.now()); err != nil {
		t.Fatal(err)
	}
	cancelSig := shared.Sign(installPriv, CancelProofMessage("install-c", "mo-estate-planning"))
	r := e.do("POST", "/v1/subscriptions/cancel", "", map[string]any{
		"install_id": "install-c", "module_id": "mo-estate-planning", "signature": cancelSig,
	})
	if r.code != http.StatusOK {
		t.Fatalf("cancel %d %s", r.code, r.body)
	}
	sub, _ := e.m.Subscription("install-c", "mo-estate-planning")
	if !sub.CancelAtPeriodEnd || sub.Status != subCanceled {
		t.Fatalf("cancel state %+v", sub)
	}
	if !e.fake.SubscriptionCanceled(sub.StripeSubscription) {
		t.Fatal("stripe sub not cancel_at_period_end")
	}
	sig := shared.Sign(installPriv, RenewProofMessage("install-c", "mo-estate-planning"))
	// Daily renew still works until paid-through; invoice.paid after cancel does not extend.
	renewed, _, err := e.m.RenewLicense("install-c", "mo-estate-planning", sig)
	if err != nil {
		t.Fatalf("renew while paid-through: %v", err)
	}
	if err := CheckLicense(renewed, pub, "install-c", "mo-estate-planning", e.m.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.ApplyInvoicePaid(sub.StripeSubscription); err != nil {
		t.Fatalf("invoice after cancel: %v", err)
	}
	sub2, _ := e.m.Subscription("install-c", "mo-estate-planning")
	if !sub2.PaidThrough.Equal(sub.PaidThrough) {
		t.Fatalf("paid_through extended after cancel: %v -> %v", sub.PaidThrough, sub2.PaidThrough)
	}
	// After paid-through, renew and check fail.
	e.now = sub.PaidThrough.Add(48 * time.Hour)
	if _, _, err := e.m.RenewLicense("install-c", "mo-estate-planning", sig); err == nil {
		t.Fatal("renew after paid-through")
	}
	if err := CheckLicense(renewed, pub, "install-c", "mo-estate-planning", e.m.now()); err == nil {
		t.Fatal("check after paid-through")
	}
}

// MKT-06: no card number or Stripe secret in state, logs or responses.
func TestMKT06NoCardOrSecret(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Secret Author", "secret@example.test")
	connect := e.do("POST", "/v1/authors/me/stripe/connect", a.token, map[string]any{})
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 2000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	pub := installPriv.Public().(ed25519.PublicKey)
	checkout := e.do("POST", "/v1/listings/mo-estate-planning/checkout", "", map[string]any{
		"install_id": "install-s", "admin_email": "admin@s.test",
		"install_public_key_pem": shared.PublicKeyPEM(pub),
	})
	var ck struct {
		SessionID string `json:"session_id"`
	}
	checkout.json(t, &ck)
	_, _, _, _, _ = e.fake.CompleteCheckout(ck.SessionID)
	complete := e.do("POST", "/v1/stripe/checkout/complete", "", map[string]any{"session_id": ck.SessionID})
	var out struct {
		LicenseKey string `json:"license_key"`
	}
	complete.json(t, &out)
	key := out.LicenseKey
	r := e.do("GET", "/v1/listings/mo-estate-planning", "", nil)
	statePath := filepath.Join(e.dir, stateName)
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	logSample := truncateForLog(`error sk_test_abc123xyz card 4242424242424242`)
	haystacks := []string{
		string(stateBytes), string(r.body), key, e.m.LicensePublicKeyPEM(),
		string(connect.body), string(checkout.body), string(complete.body), logSample,
	}
	for _, h := range haystacks {
		for _, bad := range []string{"sk_test_", "sk_live_", "whsec_", "rk_test_", "4242424242424242", "4111111111111111"} {
			if strings.Contains(h, bad) {
				t.Fatalf("secret or card material %q found in output", bad)
			}
		}
	}
	sample := redactSecrets(`card 4242-4242-4242-4242 and sk_test_abc123xyz and ok`)
	if strings.Contains(sample, "4242-4242") || strings.Contains(sample, "sk_test_abc") {
		t.Fatalf("redact failed: %q", sample)
	}
}

// MKT-07: no test reaches Stripe; helpers refuse non-local hosts.
func TestMKT07StripeFaked(t *testing.T) {
	if err := RequireLocalStripeBase("https://api.stripe.com"); err == nil {
		t.Fatal("api.stripe.com allowed")
	}
	if err := RequireLocalStripeBase("https://checkout.stripe.com"); err == nil {
		t.Fatal("checkout.stripe.com allowed")
	}
	fake := StartFakeStripe()
	t.Cleanup(fake.Close)
	if err := RequireLocalStripeBase(fake.BaseURL); err != nil {
		t.Fatal(err)
	}
	// Exercising the paid path only talks to the fake.
	e := newPaidEnv(t)
	a := e.author("Fake Author", "fake@example.test")
	e.connect(a)
	var acct string
	e.m.read(func(st *State) {
		for _, x := range st.Authors {
			if x.Email == "fake@example.test" {
				acct = x.StripeAccountID
			}
		}
	})
	if !strings.HasPrefix(acct, "acct_fake_") {
		t.Fatalf("expected fake account, got %q", acct)
	}
	if !strings.HasPrefix(e.fake.BaseURL, "http://127.0.0.1") && !strings.HasPrefix(e.fake.BaseURL, "http://localhost") {
		t.Fatalf("fake not local: %s", e.fake.BaseURL)
	}
	_ = fake
}

func TestLicenseEncodingRoundTrip(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub := priv.Public().(ed25519.PublicKey)
	through := time.Date(2027, 1, 15, 12, 0, 0, 0, time.UTC)
	key, err := IssueLicense(priv, "inst-1", "mo-estate-planning", through)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, licensePrefix+".") {
		t.Fatalf("prefix: %s", key)
	}
	st, err := ParseLicenseKey(key, pub)
	if err != nil {
		t.Fatal(err)
	}
	if st.InstallID != "inst-1" || st.ModuleID != "mo-estate-planning" || st.PaidThrough.Format("2006-01-02") != "2027-01-15" {
		t.Fatalf("%+v", st)
	}
	want := FormatLicenseStatement("inst-1", "mo-estate-planning", through)
	if !bytes.Equal(st.Payload, want) {
		t.Fatalf("payload\n got %q\nwant %q", st.Payload, want)
	}
}

func TestPlatformFeeDefaultZero(t *testing.T) {
	e := newPaidEnv(t)
	if e.m.PlatformFeePercent() != 0 {
		t.Fatalf("default fee %d", e.m.PlatformFeePercent())
	}
	r := e.do("GET", "/v1/operator/settings", opToken, nil)
	if r.code != http.StatusOK {
		t.Fatalf("%d %s", r.code, r.body)
	}
	var body map[string]any
	r.json(t, &body)
	if int(body["platform_fee_percent"].(float64)) != 0 {
		t.Fatalf("%+v", body)
	}
}

func TestUnpaidCheckoutRefused(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Unpaid Author", "unpaid@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	pub := installPriv.Public().(ed25519.PublicKey)
	r := e.do("POST", "/v1/listings/mo-estate-planning/checkout", "", map[string]any{
		"install_id": "install-u", "admin_email": "admin@u.test",
		"install_public_key_pem": shared.PublicKeyPEM(pub),
	})
	if r.code != http.StatusCreated {
		t.Fatalf("checkout %d %s", r.code, r.body)
	}
	var ck struct {
		SessionID string `json:"session_id"`
	}
	r.json(t, &ck)
	// Caller-supplied stripe_subscription_id is refused (unknown field) and unpaid session fails.
	if r := e.do("POST", "/v1/stripe/checkout/complete", "", map[string]any{
		"session_id": ck.SessionID, "stripe_subscription_id": "sub_forged",
	}); r.code != http.StatusBadRequest {
		t.Fatalf("forged stripe sub id: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/v1/stripe/checkout/complete", "", map[string]any{
		"session_id": ck.SessionID,
	}); r.code != http.StatusBadRequest {
		t.Fatalf("unpaid complete: %d %s", r.code, r.body)
	}
}

func TestClientExtendRefused(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Extend Author", "extend@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	e.subscribe("mo-estate-planning", "install-e", "admin@e.test", installPriv)
	sub, _ := e.m.Subscription("install-e", "mo-estate-planning")
	through := sub.PaidThrough
	sig := shared.Sign(installPriv, RenewProofMessage("install-e", "mo-estate-planning"))
	r := e.do("POST", "/v1/licenses/renew", "", map[string]any{
		"install_id": "install-e", "module_id": "mo-estate-planning",
		"signature": sig, "extend": true,
	})
	if r.code != http.StatusBadRequest {
		t.Fatalf("extend true: %d %s", r.code, r.body)
	}
	r = e.do("POST", "/v1/licenses/renew", "", map[string]any{
		"install_id": "install-e", "module_id": "mo-estate-planning", "signature": sig,
	})
	if r.code != http.StatusOK {
		t.Fatalf("renew: %d %s", r.code, r.body)
	}
	sub, _ = e.m.Subscription("install-e", "mo-estate-planning")
	if !sub.PaidThrough.Equal(through) {
		t.Fatalf("paid_through moved without invoice: %v -> %v", through, sub.PaidThrough)
	}
}

func TestForgedCancelRefused(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Forged Cancel", "fcancel@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	e.subscribe("mo-estate-planning", "install-f", "admin@f.test", installPriv)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	bad := shared.Sign(otherPriv, CancelProofMessage("install-f", "mo-estate-planning"))
	if r := e.do("POST", "/v1/subscriptions/cancel", "", map[string]any{
		"install_id": "install-f", "module_id": "mo-estate-planning", "signature": bad,
	}); r.code != http.StatusBadRequest {
		t.Fatalf("forged cancel: %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/v1/subscriptions/cancel", "", map[string]any{
		"install_id": "install-f", "module_id": "mo-estate-planning",
	}); r.code != http.StatusBadRequest {
		t.Fatalf("missing cancel signature: %d %s", r.code, r.body)
	}
	sub, _ := e.m.Subscription("install-f", "mo-estate-planning")
	if sub.Status != subActive {
		t.Fatalf("canceled without proof: %+v", sub)
	}
}

func TestForgedRenewSignatureRefused(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Forged Renew", "frenew@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	e.subscribe("mo-estate-planning", "install-r", "admin@r.test", installPriv)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	bad := shared.Sign(otherPriv, RenewProofMessage("install-r", "mo-estate-planning"))
	if r := e.do("POST", "/v1/licenses/renew", "", map[string]any{
		"install_id": "install-r", "module_id": "mo-estate-planning", "signature": bad,
	}); r.code != http.StatusBadRequest {
		t.Fatalf("forged renew: %d %s", r.code, r.body)
	}
}

func TestPulledAndFreeCheckoutRefused(t *testing.T) {
	e := newPaidEnv(t)
	a := e.author("Pull Author", "pullpaid@example.test")
	e.connect(a)
	e.publishPaid(a, "mo-estate-planning", "1.0.0", map[string]any{
		"amount_cents": 1000, "currency": "USD", "interval": "month",
	})
	if _, err := e.m.Pull("mo-estate-planning", "security review"); err != nil {
		t.Fatal(err)
	}
	_, installPriv, _ := ed25519.GenerateKey(rand.Reader)
	pub := installPriv.Public().(ed25519.PublicKey)
	if r := e.do("POST", "/v1/listings/mo-estate-planning/checkout", "", map[string]any{
		"install_id": "install-p", "admin_email": "admin@p.test",
		"install_public_key_pem": shared.PublicKeyPEM(pub),
	}); r.code != http.StatusGone {
		t.Fatalf("pulled checkout: %d %s", r.code, r.body)
	}
	b := e.author("Free Lister", "freelist@example.test")
	e.publishPaid(b, "documents", "1.0.0", "Free")
	if r := e.do("POST", "/v1/listings/documents/checkout", "", map[string]any{
		"install_id": "install-free", "admin_email": "admin@free.test",
		"install_public_key_pem": shared.PublicKeyPEM(pub),
	}); r.code != http.StatusBadRequest {
		t.Fatalf("free checkout: %d %s", r.code, r.body)
	}
}

func TestMissingLicenseSeedFailsClosed(t *testing.T) {
	dir := t.TempDir()
	m, err := Open(dir, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.HasLicenseKey() {
		t.Fatal("Open must not mint a random license key")
	}
	if err := m.EnsureLicenseKey(); err == nil {
		t.Fatal("EnsureLicenseKey without AllowTestLicenseKeys must fail")
	}
	fake := StartFakeStripe()
	t.Cleanup(fake.Close)
	stripe, err := NewStripe(fake.BaseURL, "sk_test_synthetic")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.StripeAPIBase = fake.BaseURL
	cfg.LicenseSeedFile = ""
	// SetStripe via ApplyConfig refuses stripe without a seed.
	seedPath := filepath.Join(dir, "missing-seed-should-not-exist")
	cfg.LicenseSeedFile = seedPath
	if err := m.ApplyConfig(cfg); err == nil {
		t.Fatal("ApplyConfig with missing seed file must fail")
	}
	cfg.LicenseSeedFile = ""
	m.SetStripe(stripe) // direct attach still leaves license unset
	if _, _, err := m.CompleteCheckout("cs_missing"); err == nil {
		t.Fatal("CompleteCheckout without license key")
	}
	if pem := m.LicensePublicKeyPEM(); pem != "" {
		t.Fatalf("public key without seed: %q", pem)
	}
	// Seed file restores a stable key.
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}
	seedFile := filepath.Join(dir, "license.seed")
	if err := os.WriteFile(seedFile, seed, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.LicenseSeedFile = seedFile
	cfg.StripeAPIBase = ""
	if err := m.ApplyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if !m.HasLicenseKey() {
		t.Fatal("seed did not install license key")
	}
}

func TestSMTPMailDelivery(t *testing.T) {
	fake, err := StartFakeSMTP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fake.Close() })
	mailer := SMTPMailer{Addr: fake.Addr, From: "marketplace@example.test"}
	if err := mailer.Send("admin@firm.test", "Price change notice", "new price 75.00"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.Messages)
		fake.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.Messages) == 0 || !strings.Contains(fake.Messages[0], "75.00") {
		t.Fatalf("smtp messages: %+v", fake.Messages)
	}
}

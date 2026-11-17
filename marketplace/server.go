package marketplace

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

//go:embed templates/*.html static/*.css
var assets embed.FS

// Server is the marketplace's HTTP surface: the read API the core uses, the author API,
// the operator API, and the listing, search and operator pages.
type Server struct {
	M             *Market
	operatorHash  [sha256.Size]byte
	secureCookies bool
	opLife        time.Duration
	maxJSON       int64
	pages         *template.Template
	now           func() time.Time

	mu       sync.Mutex
	sessions map[string]opSession
}

type opSession struct {
	csrf    string
	expires time.Time
}

// NewServer returns the server. operatorToken is the operator's secret.
func NewServer(m *Market, cfg Config, operatorToken string, now func() time.Time) (*Server, error) {
	if len(operatorToken) < cfg.MinOperatorTokenBytes {
		return nil, errors.New("marketplace: operator token is too short")
	}
	if now == nil {
		now = time.Now
	}
	t, err := template.New("pages").Funcs(template.FuncMap{
		"date": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{M: m, operatorHash: sha256.Sum256([]byte(operatorToken)), secureCookies: cfg.SecureCookies,
		opLife: cfg.OperatorSession(), maxJSON: cfg.MaxJSONBytes, pages: t, now: now, sessions: map[string]opSession{}}, nil
}

// Handler routes every request.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	// Read API (the core).
	m.HandleFunc("GET /v1/listings", s.apiSearch)
	m.HandleFunc("GET /v1/listings/{listing_id}", s.apiListing)
	m.HandleFunc("GET /v1/listings/{listing_id}/versions/{version}/package", s.apiPackage)
	m.HandleFunc("GET /v1/alerts", s.apiAlerts)
	m.HandleFunc("GET /v1/signing-keys", s.apiSigningKeys)
	// Authors.
	m.HandleFunc("POST /v1/authors", s.apiRegister)
	m.HandleFunc("POST /v1/sessions", s.apiLogin)
	m.HandleFunc("GET /v1/authors/me/keys", s.apiKeys)
	m.HandleFunc("POST /v1/authors/me/keys", s.apiAddKey)
	m.HandleFunc("POST /v1/authors/me/keys/{key_id}/verify", s.apiVerifyKey)
	m.HandleFunc("POST /v1/authors/me/packages", s.apiUpload)
	m.HandleFunc("POST /v1/authors/me/stripe/connect", s.apiStripeConnect)
	m.HandleFunc("PATCH /v1/authors/me/listings/{listing_id}/price", s.apiSetPrice)
	// Paid subscriptions and licenses (CORE-CHANGES-2 §17).
	m.HandleFunc("POST /v1/listings/{listing_id}/checkout", s.apiCheckout)
	m.HandleFunc("POST /v1/stripe/checkout/complete", s.apiCompleteCheckout)
	m.HandleFunc("POST /v1/stripe/webhook", s.apiStripeWebhook)
	m.HandleFunc("POST /v1/subscriptions/cancel", s.apiCancelSub)
	m.HandleFunc("POST /v1/licenses/renew", s.apiRenewLicense)
	m.HandleFunc("GET /v1/license-public-key", s.apiLicensePublicKey)
	// Operator.
	m.HandleFunc("POST /v1/operator/listings/{listing_id}/flag", s.apiFlag)
	m.HandleFunc("POST /v1/operator/listings/{listing_id}/pull", s.apiPull)
	m.HandleFunc("GET /v1/operator/settings", s.apiOperatorSettings)
	m.HandleFunc("PATCH /v1/operator/settings", s.apiOperatorSettingsPatch)
	m.HandleFunc("POST /v1/operator/mail/retry", s.apiOperatorMailRetry)
	// Pages.
	m.HandleFunc("GET /{$}", s.pageSearch)
	m.HandleFunc("GET /listings/{listing_id}", s.pageListing)
	m.HandleFunc("GET /operator", s.pageOperator)
	m.HandleFunc("POST /operator/login", s.formLogin)
	m.HandleFunc("POST /operator/logout", s.formLogout)
	m.HandleFunc("POST /operator/listings/{listing_id}/flag", s.formFlag)
	m.HandleFunc("POST /operator/listings/{listing_id}/pull", s.formPull)
	m.HandleFunc("GET /static/site.css", s.css)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return headers(m)
}

// headers sets the security headers on every response.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(shared.Problem{Status: status, Code: code, Detail: detail})
}

func writeErr(w http.ResponseWriter, err error) {
	var inv *InvalidError
	switch {
	case errors.As(err, &inv):
		problem(w, http.StatusBadRequest, "validation", inv.Error())
	case errors.Is(err, ErrNotFound):
		problem(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrPulled):
		problem(w, http.StatusGone, "yanked", "this module was pulled from the marketplace and can't be installed from it; a firm can still install it from a local file")
	case errors.Is(err, ErrUnauthorized):
		problem(w, http.StatusUnauthorized, "unauthenticated", "sign in")
	case errors.Is(err, ErrForbidden):
		problem(w, http.StatusForbidden, "forbidden", "another author publishes this module id")
	case errors.Is(err, ErrConflict):
		problem(w, http.StatusConflict, "conflict", "already exists; listed versions never change")
	case errors.Is(err, ErrLicenseInvalid):
		problem(w, http.StatusForbidden, "license_invalid", "the license is invalid or expired for this install")
	default:
		problem(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func decode(r *http.Request, limit int64, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		return invalid("body", "the request body is too large or unreadable")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("body", "the body must be a JSON object with known fields")
	}
	return nil
}

func bearer(r *http.Request) string {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(t)
}

// --- Read API ---

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	list := s.M.Search(r.URL.Query().Get("q"))
	out := make([]map[string]any, 0, len(list))
	for _, l := range list {
		out = append(out, s.listingBody(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) apiListing(w http.ResponseWriter, r *http.Request) {
	l, err := s.M.Listing(r.PathValue("listing_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.listingBody(l))
}

func (s *Server) listingBody(l shared.Listing) map[string]any {
	body := map[string]any{
		"id": l.ID, "module_id": l.ModuleID, "name": l.Name, "author": l.Author,
		"signing_key": l.SigningKey, "signing_key_pem": l.SigningKeyPEM, "version": l.Version,
		"permissions": l.Permissions, "version_history": l.VersionHistory, "price_note": l.PriceNote,
		"flagged": l.Flagged, "pulled": l.Pulled, "notice": l.Notice,
	}
	if l.FlagNote != "" {
		body["flag_note"] = l.FlagNote
	}
	if l.ListingFlagged {
		body["listing_flagged"] = true
	}
	if p, err := s.M.ListingPriceOf(l.ID); err == nil && !p.empty() {
		body["price"] = p
	}
	return body
}

func (s *Server) apiPackage(w http.ResponseWriter, r *http.Request) {
	p, err := s.M.Package(r.PathValue("listing_id"), r.PathValue("version"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) apiAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			problem(w, http.StatusBadRequest, "validation", "since is an RFC 3339 time")
			return
		}
		since = t
	}
	var ids []string
	for _, v := range q["module_id"] {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.Alerts(ids, since)})
}

func (s *Server) apiSigningKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.SigningKeys()})
}

// --- Author API ---

func (s *Server) apiRegister(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	a, err := s.M.RegisterAuthor(b.Name, b.Email, b.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	tok, err := s.M.Login(b.Email, b.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok})
}

func (s *Server) author(w http.ResponseWriter, r *http.Request) (Author, bool) {
	a, err := s.M.Author(bearer(r))
	if err != nil {
		writeErr(w, err)
		return Author{}, false
	}
	return a, true
}

func (s *Server) apiKeys(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.Keys(a)})
}

func (s *Server) apiAddKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b struct {
		PublicKeyPEM string `json:"public_key_pem"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	k, err := s.M.AddKey(a, b.PublicKeyPEM)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) apiVerifyKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b struct {
		Signature string `json:"signature"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	k, err := s.M.VerifyKey(a, r.PathValue("key_id"), b.Signature)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	// Accept the kit's signed package file unchanged (listing_id, version, immutable,
	// manifest, digest, signature, key_id, package_base64). The older three-field body
	// and optional price_note still work for existing callers.
	var b struct {
		PackageBase64 string         `json:"package_base64"`
		Signature     string         `json:"signature"`
		KeyID         string         `json:"key_id"`
		Digest        string         `json:"digest"`
		ListingID     string         `json:"listing_id"`
		Version       string         `json:"version"`
		Immutable     *bool          `json:"immutable"`
		Manifest      map[string]any `json:"manifest"`
		PriceNote     *string        `json:"price_note"`
	}
	if err := decode(r, shared.MaxPackageBytes()/3*4+s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	pkg, err := base64.StdEncoding.DecodeString(b.PackageBase64)
	if err != nil {
		writeErr(w, invalid("package_base64", "the package must be standard base64"))
		return
	}
	l, err := s.M.Publish(a, Upload{Package: pkg, Signature: b.Signature, KeyID: b.KeyID, Digest: b.Digest, PriceNote: b.PriceNote})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.listingBody(l))
}

func (s *Server) apiStripeConnect(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	id, err := s.M.ConnectStripe(a)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stripe_account_id": id})
}

func (s *Server) apiSetPrice(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b ListingPrice
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	res, err := s.M.SetListingPrice(a, r.PathValue("listing_id"), b)
	if err != nil {
		writeErr(w, err)
		return
	}
	body := s.listingBody(res.Listing)
	notices := make([]map[string]any, 0, len(res.Notices))
	for _, n := range res.Notices {
		notices = append(notices, map[string]any{
			"id": n.ID, "to": n.To, "status": n.Status, "last_error": n.LastErr,
		})
	}
	body["notices"] = notices
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) apiCheckout(w http.ResponseWriter, r *http.Request) {
	var b struct {
		InstallID           string `json:"install_id"`
		AdminEmail          string `json:"admin_email"`
		InstallPublicKeyPEM string `json:"install_public_key_pem"`
		SuccessURL          string `json:"success_url"`
		CancelURL           string `json:"cancel_url"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	b.SuccessURL = strings.TrimSpace(b.SuccessURL)
	b.CancelURL = strings.TrimSpace(b.CancelURL)
	if b.SuccessURL == "" || !strings.Contains(b.SuccessURL, "{CHECKOUT_SESSION_ID}") {
		writeErr(w, invalid("success_url", "success_url is required and must include {CHECKOUT_SESSION_ID}"))
		return
	}
	if b.CancelURL == "" {
		writeErr(w, invalid("cancel_url", "cancel_url is required"))
		return
	}
	sess, subID, err := s.M.StartCheckout(r.PathValue("listing_id"), b.InstallID, b.AdminEmail, b.InstallPublicKeyPEM, b.SuccessURL, b.CancelURL)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"checkout_url": sess.URL, "session_id": sess.ID, "subscription_id": subID,
	})
}

func (s *Server) apiCompleteCheckout(w http.ResponseWriter, r *http.Request) {
	var b struct {
		SessionID string `json:"session_id"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	key, sub, err := s.M.CompleteCheckout(b.SessionID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"license_key":     key,
		"install_id":      sub.InstallID,
		"module_id":       sub.ListingID,
		"paid_through":    sub.PaidThrough.UTC().Format("2006-01-02"),
		"subscription_id": sub.ID,
	})
}

func (s *Server) apiStripeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, s.maxJSON))
	if err != nil {
		writeErr(w, invalid("body", "the request body is too large or unreadable"))
		return
	}
	secret := s.M.WebhookSecret()
	if err := VerifyWebhookSignature(body, r.Header.Get("Stripe-Signature"), secret, s.now(), Defaults().StripeWebhookTolerance()); err != nil {
		writeErr(w, err)
		return
	}
	var evt struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object stripeInvoiceObject `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		writeErr(w, invalid("body", "the body must be a Stripe event JSON object"))
		return
	}
	if evt.Type != "invoice.paid" {
		writeJSON(w, http.StatusOK, map[string]any{"received": true, "ignored": evt.Type})
		return
	}
	subID := evt.Data.Object.stripeSubscriptionID()
	if subID == "" {
		writeErr(w, invalid("subscription", "invoice.paid event is missing subscription"))
		return
	}
	periodEndUnix := evt.Data.Object.paidTermEndUnix()
	var periodEnd time.Time
	if periodEndUnix > int64(0) {
		periodEnd = time.Unix(periodEndUnix, int64(0)).UTC()
	}
	sub, err := s.M.ApplyInvoicePaid(InvoicePaidInfo{
		EventID: evt.ID, InvoiceID: evt.Data.Object.ID, StripeSubscription: subID,
		BillingReason: evt.Data.Object.BillingReason, PeriodEnd: periodEnd,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"received": true, "install_id": sub.InstallID, "module_id": sub.ListingID,
		"paid_through": sub.PaidThrough.UTC().Format("2006-01-02"),
	})
}

// stripeInvoiceObject is the invoice payload on invoice.paid. Newer Stripe API versions
// nest the subscription under parent.subscription_details; older ones use subscription.
type stripeInvoiceObject struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Subscription  string `json:"subscription"`
	Paid          bool   `json:"paid"`
	Status        string `json:"status"`
	BillingReason string `json:"billing_reason"`
	PeriodEnd     int64  `json:"period_end"`
	Lines         struct {
		Data []struct {
			Type   string `json:"type"`
			Period struct {
				End int64 `json:"end"`
			} `json:"period"`
		} `json:"data"`
	} `json:"lines"`
	Parent struct {
		Type                string `json:"type"`
		SubscriptionDetails struct {
			Subscription string `json:"subscription"`
		} `json:"subscription_details"`
	} `json:"parent"`
}

func (o stripeInvoiceObject) stripeSubscriptionID() string {
	if id := strings.TrimSpace(o.Subscription); id != "" {
		return id
	}
	return strings.TrimSpace(o.Parent.SubscriptionDetails.Subscription)
}

// paidTermEndUnix is the end of the newly paid term. On real Stripe invoices the
// top-level period_end is the previous usage period; the new term is only in
// lines.data[].period.end.
func (o stripeInvoiceObject) paidTermEndUnix() int64 {
	for _, line := range o.Lines.Data {
		if line.Period.End > int64(0) {
			return line.Period.End
		}
	}
	return int64(0)
}

func (s *Server) apiCancelSub(w http.ResponseWriter, r *http.Request) {
	var b struct {
		InstallID string `json:"install_id"`
		ModuleID  string `json:"module_id"`
		Signature string `json:"signature"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	sub, err := s.M.CancelSubscription(b.InstallID, b.ModuleID, b.Signature)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": sub.Status, "cancel_at_period_end": sub.CancelAtPeriodEnd,
		"paid_through": sub.PaidThrough.UTC().Format("2006-01-02"),
		"install_id":   sub.InstallID, "module_id": sub.ListingID,
	})
}

func (s *Server) apiRenewLicense(w http.ResponseWriter, r *http.Request) {
	var b struct {
		InstallID string `json:"install_id"`
		ModuleID  string `json:"module_id"`
		Signature string `json:"signature"`
		Extend    *bool  `json:"extend"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	if b.Extend != nil && *b.Extend {
		writeErr(w, invalid("extend", "paid_through extends only after Stripe confirms invoice.paid; daily renew re-serves the current key"))
		return
	}
	key, through, err := s.M.RenewLicense(b.InstallID, b.ModuleID, b.Signature)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"license_key": key, "paid_through": through.UTC().Format("2006-01-02"),
	})
}

func (s *Server) apiLicensePublicKey(w http.ResponseWriter, _ *http.Request) {
	pem := s.M.LicensePublicKeyPEM()
	if pem == "" {
		writeErr(w, errorsLicenseKey())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"public_key_pem": pem})
}

// --- Operator API ---

// operator checks the operator's bearer token in constant time.
func (s *Server) operator(r *http.Request) bool {
	t := bearer(r)
	if t == "" {
		return false
	}
	sum := sha256.Sum256([]byte(t))
	return hmac.Equal(sum[:], s.operatorHash[:])
}

func (s *Server) apiFlag(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	var b struct {
		Note    string `json:"note"`
		Version string `json:"version"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	l, err := s.M.Flag(r.PathValue("listing_id"), strings.TrimSpace(b.Version), b.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) apiPull(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	var b struct {
		Note string `json:"note"`
	}
	if r.ContentLength != int64(0) {
		if err := decode(r, s.maxJSON, &b); err != nil {
			writeErr(w, err)
			return
		}
	}
	l, err := s.M.Pull(r.PathValue("listing_id"), b.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) apiOperatorSettings(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"platform_fee_percent": s.M.PlatformFeePercent()})
}

func (s *Server) apiOperatorMailRetry(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	err := s.M.RetryPendingMail()
	pending, failed, sent := int(0), int(0), int(0)
	for _, m := range s.M.Mails() {
		switch m.Status {
		case mailPending:
			pending++
		case mailFailed:
			failed++
		case mailSent:
			sent++
		}
	}
	body := map[string]any{
		"pending": pending, "failed": failed, "sent": sent,
	}
	if err != nil {
		body["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) apiOperatorSettingsPatch(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	var b struct {
		PlatformFeePercent *int `json:"platform_fee_percent"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	if b.PlatformFeePercent != nil {
		if err := s.M.SetPlatformFeePercent(*b.PlatformFeePercent); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"platform_fee_percent": s.M.PlatformFeePercent()})
}

// --- Pages ---

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var b bytes.Buffer
	if err := s.pages.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	b, err := fs.ReadFile(assets, "static/site.css")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(b)
}

type listingPage struct {
	L       shared.Listing
	Warning string
	Notice  string
}

func (s *Server) pageSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	s.render(w, http.StatusOK, "search.html", map[string]any{"Q": q, "Listings": s.M.Search(q), "Notice": shared.Notice, "FlagWarning": shared.FlagWarning})
}

func (s *Server) pageListing(w http.ResponseWriter, r *http.Request) {
	l, err := s.M.Listing(r.PathValue("listing_id"))
	if err != nil {
		s.render(w, http.StatusNotFound, "notfound.html", nil)
		return
	}
	s.render(w, http.StatusOK, "listing.html", listingPage{L: l, Warning: l.FlagText(), Notice: shared.Notice})
}

const opCookie = "togavsu_marketplace_operator"

// session is the signed-in operator's browser session, if any.
func (s *Server) session(r *http.Request) (string, opSession, bool) {
	c, err := r.Cookie(opCookie)
	if err != nil || c.Value == "" {
		return "", opSession{}, false
	}
	key := hashToken(c.Value)
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.sessions[key]
	if !ok || !s.now().Before(o.expires) {
		delete(s.sessions, key)
		return "", opSession{}, false
	}
	return key, o, true
}

func (s *Server) pageOperator(w http.ResponseWriter, r *http.Request) {
	_, o, ok := s.session(r)
	if !ok {
		s.render(w, http.StatusOK, "operator_login.html", nil)
		return
	}
	s.render(w, http.StatusOK, "operator.html", map[string]any{"Listings": s.M.All(), "CSRF": o.csrf, "Done": r.URL.Query().Get("done")})
}

func (s *Server) formLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSON)
	sum := sha256.Sum256([]byte(strings.TrimSpace(r.PostFormValue("token"))))
	if !hmac.Equal(sum[:], s.operatorHash[:]) {
		s.render(w, http.StatusUnauthorized, "operator_login.html", map[string]any{"Failed": true})
		return
	}
	value, err := randomText()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf, err := randomText()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := s.now()
	s.mu.Lock()
	for k, o := range s.sessions {
		if !now.Before(o.expires) {
			delete(s.sessions, k)
		}
	}
	s.sessions[hashToken(value)] = opSession{csrf: csrf, expires: now.Add(s.opLife)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: opCookie, Value: value, Path: "/operator", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(s.opLife.Seconds())})
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

// formOperator checks the session and the form's CSRF token.
func (s *Server) formOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSON)
	key, o, ok := s.session(r)
	if !ok {
		http.Redirect(w, r, "/operator", http.StatusSeeOther)
		return "", false
	}
	if !hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(o.csrf)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	return key, true
}

func (s *Server) formLogout(w http.ResponseWriter, r *http.Request) {
	key, ok := s.formOperator(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	delete(s.sessions, key)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: opCookie, Value: "", Path: "/operator", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

func (s *Server) formFlag(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.formOperator(w, r); !ok {
		return
	}
	if _, err := s.M.Flag(r.PathValue("listing_id"), strings.TrimSpace(r.PostFormValue("version")), r.PostFormValue("note")); err != nil {
		s.formFailed(w, err)
		return
	}
	http.Redirect(w, r, "/operator?done=flagged", http.StatusSeeOther)
}

func (s *Server) formPull(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.formOperator(w, r); !ok {
		return
	}
	if _, err := s.M.Pull(r.PathValue("listing_id"), r.PostFormValue("note")); err != nil {
		s.formFailed(w, err)
		return
	}
	http.Redirect(w, r, "/operator?done=pulled", http.StatusSeeOther)
}

func (s *Server) formFailed(w http.ResponseWriter, err error) {
	var inv *InvalidError
	msg, status := "That listing or version does not exist.", http.StatusNotFound
	if errors.As(err, &inv) {
		msg, status = inv.Message, http.StatusBadRequest
	} else if !errors.Is(err, ErrNotFound) {
		msg, status = "The change could not be saved.", http.StatusInternalServerError
	}
	s.render(w, status, "operator_error.html", map[string]any{"Message": msg})
}

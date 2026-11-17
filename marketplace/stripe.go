package marketplace

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Stripe talks to Stripe's HTTP API (Connect accounts, Checkout sessions, subscription
// cancel). Secrets stay in memory on this client; they are never written to State.
type Stripe struct {
	base   string
	secret string
	client *http.Client
}

// NewStripe builds a client for base (e.g. https://api.stripe.com or a FakeStripe URL).
// secret is the Stripe secret key; it is held only on this value.
func NewStripe(base, secret string) (*Stripe, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, errors.New("marketplace: stripe api base is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("marketplace: stripe api base: %w", err)
	}
	return &Stripe{base: base, secret: secret, client: http.DefaultClient}, nil
}

func (s *Stripe) doForm(method, path string, form url.Values, dst any) error {
	if s == nil || s.base == "" {
		return errors.New("marketplace: stripe is not configured")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, s.base+path, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if s.secret != "" {
		req.SetBasicAuth(s.secret, "")
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("marketplace: stripe %s: %s", res.Status, truncateForLog(string(b)))
	}
	if dst == nil {
		return nil
	}
	return json.Unmarshal(b, dst)
}

func truncateForLog(s string) string {
	// Never echo card numbers or secret-shaped values into errors that might be logged.
	s = redactSecrets(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func redactSecrets(s string) string {
	for _, p := range []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_", "whsec_"} {
		for {
			i := strings.Index(s, p)
			if i < 0 {
				break
			}
			j := i + len(p)
			for j < len(s) && s[j] != '"' && s[j] != ' ' && s[j] != '&' && s[j] != '\n' && s[j] != '[' {
				j++
			}
			// Replace the whole token so the prefix does not match again.
			s = s[:i] + "[stripe-secret-redacted]" + s[j:]
		}
	}
	// Card-shaped digit runs (13–19 digits), including spaced/dashed groups.
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			digits := 0
			for j < len(s) {
				if s[j] >= '0' && s[j] <= '9' {
					digits++
					j++
					continue
				}
				if (s[j] == ' ' || s[j] == '-') && j+1 < len(s) && s[j+1] >= '0' && s[j+1] <= '9' {
					j++
					continue
				}
				break
			}
			if digits >= 13 && digits <= 19 {
				out.WriteString("[card-redacted]")
				i = j
				continue
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// CreateConnectAccount creates a Stripe Express account for an author.
func (s *Stripe) CreateConnectAccount(email, name string) (string, error) {
	form := url.Values{}
	form.Set("type", "express")
	form.Set("email", email)
	form.Set("business_profile[name]", name)
	form.Set("capabilities[card_payments][requested]", "true")
	form.Set("capabilities[transfers][requested]", "true")
	var out struct {
		ID string `json:"id"`
	}
	if err := s.doForm(http.MethodPost, "/v1/accounts", form, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("marketplace: stripe account id missing")
	}
	return out.ID, nil
}

// CheckoutSession is a Stripe Checkout Session for a subscription.
type CheckoutSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// CreateCheckoutSession starts Checkout for a subscription on the author's Connect account.
func (s *Stripe) CreateCheckoutSession(in CheckoutInput) (CheckoutSession, error) {
	form := url.Values{}
	form.Set("mode", "subscription")
	form.Set("success_url", in.SuccessURL)
	form.Set("cancel_url", in.CancelURL)
	form.Set("client_reference_id", in.InstallID)
	form.Set("metadata[install_id]", in.InstallID)
	form.Set("metadata[module_id]", in.ModuleID)
	form.Set("metadata[subscription_id]", in.SubscriptionID)
	form.Set("line_items[0][price_data][currency]", strings.ToLower(in.Currency))
	form.Set("line_items[0][price_data][unit_amount]", fmt.Sprintf("%d", in.AmountCents))
	form.Set("line_items[0][price_data][recurring][interval]", in.Interval)
	form.Set("line_items[0][price_data][product_data][name]", in.ProductName)
	form.Set("line_items[0][quantity]", "1")
	if in.ConnectedAccountID != "" {
		form.Set("subscription_data[transfer_data][destination]", in.ConnectedAccountID)
	}
	if in.PlatformFeePercent > 0 {
		form.Set("subscription_data[application_fee_percent]", fmt.Sprintf("%d", in.PlatformFeePercent))
	}
	var out CheckoutSession
	if err := s.doForm(http.MethodPost, "/v1/checkout/sessions", form, &out); err != nil {
		return CheckoutSession{}, err
	}
	if out.ID == "" || out.URL == "" {
		return CheckoutSession{}, errors.New("marketplace: stripe checkout session incomplete")
	}
	return out, nil
}

// CheckoutInput is the data for CreateCheckoutSession.
type CheckoutInput struct {
	AmountCents        int64
	Currency           string
	Interval           string
	ProductName        string
	InstallID          string
	ModuleID           string
	SubscriptionID     string
	ConnectedAccountID string
	PlatformFeePercent int
	SuccessURL         string
	CancelURL          string
}

// CancelSubscription marks a Stripe subscription to cancel at period end.
func (s *Stripe) CancelSubscription(stripeSubID string) error {
	form := url.Values{}
	form.Set("cancel_at_period_end", "true")
	return s.doForm(http.MethodPost, "/v1/subscriptions/"+url.PathEscape(stripeSubID), form, &struct{}{})
}

// CheckoutSessionDetail is the Stripe Checkout Session used to confirm payment.
type CheckoutSessionDetail struct {
	ID                string
	Status            string // open, complete, expired
	PaymentStatus     string // paid, unpaid, no_payment_required
	Subscription      string // Stripe subscription id
	ClientReferenceID string
	Metadata          map[string]string
}

// GetCheckoutSession fetches a Checkout Session from Stripe. CompleteCheckout uses this
// so payment status comes from Stripe, never from the caller.
func (s *Stripe) GetCheckoutSession(sessionID string) (CheckoutSessionDetail, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return CheckoutSessionDetail{}, invalid("session_id", "session_id is required")
	}
	var raw struct {
		ID                string            `json:"id"`
		Status            string            `json:"status"`
		PaymentStatus     string            `json:"payment_status"`
		Subscription      string            `json:"subscription"`
		ClientReferenceID string            `json:"client_reference_id"`
		Metadata          map[string]string `json:"metadata"`
	}
	if err := s.doForm(http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(sessionID), nil, &raw); err != nil {
		return CheckoutSessionDetail{}, err
	}
	return CheckoutSessionDetail{
		ID: raw.ID, Status: raw.Status, PaymentStatus: raw.PaymentStatus,
		Subscription: raw.Subscription, ClientReferenceID: raw.ClientReferenceID, Metadata: raw.Metadata,
	}, nil
}

// UpdateSubscriptionPrice changes the recurring amount on a Stripe subscription so the
// billed price stays in step with a marketplace price change after the notice period.
func (s *Stripe) UpdateSubscriptionPrice(stripeSubID string, amountCents int64, currency, interval string) error {
	form := url.Values{}
	form.Set("items[0][price_data][currency]", strings.ToLower(currency))
	form.Set("items[0][price_data][unit_amount]", fmt.Sprintf("%d", amountCents))
	form.Set("items[0][price_data][recurring][interval]", interval)
	form.Set("proration_behavior", "none")
	return s.doForm(http.MethodPost, "/v1/subscriptions/"+url.PathEscape(stripeSubID), form, &struct{}{})
}

// VerifyWebhookSignature checks a Stripe-Signature header (t=…,v1=…) over the raw body.
// secret is the webhook signing secret from the operator's secret file (never stored in state).
func VerifyWebhookSignature(payload []byte, sigHeader, secret string, now time.Time, tolerance time.Duration) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return invalid("stripe_webhook", "webhook signing secret is not configured")
	}
	var ts int64
	var v1 string
	for _, part := range strings.Split(sigHeader, ",") {
		part = strings.TrimSpace(part)
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return invalid("stripe_webhook", "invalid signature timestamp")
			}
			ts = n
		case "v1":
			v1 = v
		}
	}
	if ts == 0 || v1 == "" {
		return invalid("stripe_webhook", "Stripe-Signature must include t and v1")
	}
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	eventTime := time.Unix(ts, 0).UTC()
	if now.Sub(eventTime) > tolerance || eventTime.Sub(now) > tolerance {
		return invalid("stripe_webhook", "signature timestamp is outside the allowed window")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	_, _ = mac.Write(payload)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(v1)) {
		return invalid("stripe_webhook", "signature mismatch")
	}
	return nil
}

// SignWebhook builds a Stripe-Signature header for tests (local fake only).
func SignWebhook(payload []byte, secret string, t time.Time) string {
	ts := t.UTC().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	_, _ = mac.Write(payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

// --- Fake Stripe (tests only) ---

// ErrStripeHostRefused means a test helper was pointed at a non-local Stripe host.
var ErrStripeHostRefused = errors.New("marketplace: stripe test helpers refuse any host that is not local")

// RequireLocalStripeBase refuses any base URL that is not a local test server. Every
// marketplace test that builds a Stripe client must call this (MKT-07).
func RequireLocalStripeBase(base string) error {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ErrStripeHostRefused
	}
	host := u.Hostname()
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		if strings.HasSuffix(host, ".localhost") {
			return nil
		}
		return ErrStripeHostRefused
	}
}

// FakeStripe is a local Stripe stand-in for tests. It never contacts api.stripe.com.
type FakeStripe struct {
	Server  *httptest.Server
	BaseURL string
	mu      sync.Mutex
	seq     atomic.Uint64
	accts   map[string]struct{}
	sess    map[string]fakeSession
	subs    map[string]fakeSub
}

type fakeSession struct {
	ID             string
	URL            string
	SubscriptionID string // marketplace subscription id in metadata
	InstallID      string
	ModuleID       string
	Completed      bool
	PaymentStatus  string // unpaid until CompleteCheckout; then paid
	StripeSubID    string
	AmountCents    int64
	Currency       string
	Interval       string
}

type fakeSub struct {
	ID                string
	CancelAtPeriodEnd bool
	AmountCents       int64
	Currency          string
	Interval          string
	ItemID            string
}

// StartFakeStripe listens on 127.0.0.1 and serves a minimal Stripe API. The returned
// BaseURL always passes RequireLocalStripeBase.
func StartFakeStripe() *FakeStripe {
	f := &FakeStripe{accts: map[string]struct{}{}, sess: map[string]fakeSession{}, subs: map[string]fakeSub{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/accounts", f.createAccount)
	mux.HandleFunc("POST /v1/checkout/sessions", f.createCheckout)
	mux.HandleFunc("POST /v1/subscriptions/{id}", f.updateSub)
	mux.HandleFunc("GET /v1/checkout/sessions/{id}", f.getSession)
	f.Server = httptest.NewServer(mux)
	f.BaseURL = f.Server.URL
	return f
}

// Close shuts the fake server down.
func (f *FakeStripe) Close() { f.Server.Close() }

func (f *FakeStripe) nextID(prefix string) string {
	return fmt.Sprintf("%s_fake_%d", prefix, f.seq.Add(1))
}

func (f *FakeStripe) createAccount(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := f.nextID("acct")
	f.mu.Lock()
	f.accts[id] = struct{}{}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "account", "type": "express"})
}

func (f *FakeStripe) createCheckout(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := f.nextID("cs")
	subMeta := r.Form.Get("metadata[subscription_id]")
	install := r.Form.Get("metadata[install_id]")
	module := r.Form.Get("metadata[module_id]")
	amount, _ := strconv.ParseInt(r.Form.Get("line_items[0][price_data][unit_amount]"), 10, 64)
	currency := r.Form.Get("line_items[0][price_data][currency]")
	interval := r.Form.Get("line_items[0][price_data][recurring][interval]")
	stripeSub := f.nextID("sub")
	itemID := f.nextID("si")
	sess := fakeSession{
		ID: id, URL: f.BaseURL + "/pay/" + id, SubscriptionID: subMeta,
		InstallID: install, ModuleID: module, StripeSubID: stripeSub,
		PaymentStatus: "unpaid", AmountCents: amount, Currency: currency, Interval: interval,
	}
	f.mu.Lock()
	f.sess[id] = sess
	f.subs[stripeSub] = fakeSub{
		ID: stripeSub, AmountCents: amount, Currency: currency, Interval: interval, ItemID: itemID,
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "checkout.session", "url": sess.URL, "status": "open", "payment_status": "unpaid"})
}

func (f *FakeStripe) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	s, ok := f.sess[id]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	status := "open"
	pay := s.PaymentStatus
	if pay == "" {
		pay = "unpaid"
	}
	if s.Completed {
		status = "complete"
		pay = "paid"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "object": "checkout.session", "status": status, "payment_status": pay, "url": s.URL,
		"client_reference_id": s.InstallID,
		"metadata": map[string]string{
			"install_id": s.InstallID, "module_id": s.ModuleID, "subscription_id": s.SubscriptionID,
		},
		"subscription": s.StripeSubID,
	})
}

func (f *FakeStripe) updateSub(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.PathValue("id")
	f.mu.Lock()
	sub, ok := f.subs[id]
	if !ok {
		f.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if r.Form.Get("cancel_at_period_end") == "true" {
		sub.CancelAtPeriodEnd = true
	}
	if v := r.Form.Get("items[0][price_data][unit_amount]"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			sub.AmountCents = n
		}
	}
	if v := r.Form.Get("items[0][price_data][currency]"); v != "" {
		sub.Currency = v
	}
	if v := r.Form.Get("items[0][price_data][recurring][interval]"); v != "" {
		sub.Interval = v
	}
	f.subs[id] = sub
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "object": "subscription", "cancel_at_period_end": sub.CancelAtPeriodEnd,
		"items": map[string]any{"data": []map[string]any{{
			"id": sub.ItemID,
			"price": map[string]any{
				"unit_amount": sub.AmountCents, "currency": sub.Currency,
				"recurring": map[string]any{"interval": sub.Interval},
			},
		}}},
	})
}

// CompleteCheckout marks the session paid and returns the marketplace subscription id,
// install id, module id and Stripe subscription id. Tests call this to simulate Stripe
// Checkout succeeding before the marketplace confirms via GetCheckoutSession.
func (f *FakeStripe) CompleteCheckout(sessionID string) (marketplaceSubID, installID, moduleID, stripeSubID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sess[sessionID]
	if !ok {
		return "", "", "", "", ErrNotFound
	}
	s.Completed = true
	s.PaymentStatus = "paid"
	f.sess[sessionID] = s
	return s.SubscriptionID, s.InstallID, s.ModuleID, s.StripeSubID, nil
}

// SubscriptionCanceled reports whether cancel_at_period_end is set on a fake sub.
func (f *FakeStripe) SubscriptionCanceled(stripeSubID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[stripeSubID].CancelAtPeriodEnd
}

// SubscriptionAmount returns the recurring unit amount stored on a fake subscription.
func (f *FakeStripe) SubscriptionAmount(stripeSubID string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[stripeSubID].AmountCents
}

// InvoicePaidEvent builds a minimal invoice.paid JSON body naming the Stripe subscription.
func InvoicePaidEvent(stripeSubID string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": "evt_fake_invoice_paid", "object": "event", "type": "invoice.paid",
		"data": map[string]any{"object": map[string]any{
			"object": "invoice", "subscription": stripeSubID, "paid": true, "status": "paid",
		}},
	})
	return b
}

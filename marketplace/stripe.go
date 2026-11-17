package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
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
	StripeSubID    string
}

type fakeSub struct {
	ID                string
	CancelAtPeriodEnd bool
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
	stripeSub := f.nextID("sub")
	sess := fakeSession{
		ID: id, URL: f.BaseURL + "/pay/" + id, SubscriptionID: subMeta,
		InstallID: install, ModuleID: module, StripeSubID: stripeSub,
	}
	f.mu.Lock()
	f.sess[id] = sess
	f.subs[stripeSub] = fakeSub{ID: stripeSub}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "checkout.session", "url": sess.URL, "status": "open"})
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
	if s.Completed {
		status = "complete"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "object": "checkout.session", "status": status, "url": s.URL,
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
		f.subs[id] = sub
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "subscription", "cancel_at_period_end": sub.CancelAtPeriodEnd})
}

// CompleteCheckout marks the session paid and returns the marketplace subscription id,
// install id, module id and Stripe subscription id for the webhook handler.
func (f *FakeStripe) CompleteCheckout(sessionID string) (marketplaceSubID, installID, moduleID, stripeSubID string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sess[sessionID]
	if !ok {
		return "", "", "", "", ErrNotFound
	}
	s.Completed = true
	f.sess[sessionID] = s
	return s.SubscriptionID, s.InstallID, s.ModuleID, s.StripeSubID, nil
}

// SubscriptionCanceled reports whether cancel_at_period_end is set on a fake sub.
func (f *FakeStripe) SubscriptionCanceled(stripeSubID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subs[stripeSubID].CancelAtPeriodEnd
}

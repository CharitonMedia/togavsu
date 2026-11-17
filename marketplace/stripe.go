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
	"net/url"
	"strconv"
	"strings"
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
	cfg := Defaults()
	b, _ := io.ReadAll(io.LimitReader(res.Body, cfg.StripeResponseMaxBytes))
	if res.StatusCode >= http.StatusMultipleChoices {
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
	cfg := Defaults()
	if len(s) > cfg.StripeErrorLogRunes {
		return s[:cfg.StripeErrorLogRunes] + "…"
	}
	return s
}

func redactSecrets(s string) string {
	for _, p := range []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_", "whsec_"} {
		for {
			i := strings.Index(s, p)
			if i < int(0) {
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
	// Card-shaped digit runs, including spaced/dashed groups.
	cfg := Defaults()
	var out strings.Builder
	for i := int(0); i < len(s); {
		if s[i] >= '0' && s[i] <= '9' {
			j := i
			digits := int(0)
			for j < len(s) {
				if s[j] >= '0' && s[j] <= '9' {
					digits++
					j++
					continue
				}
				if (s[j] == ' ' || s[j] == '-') && j+len("0") < len(s) && s[j+len("0")] >= '0' && s[j+len("0")] <= '9' {
					j++
					continue
				}
				break
			}
			if digits >= cfg.CardDigitMin && digits <= cfg.CardDigitMax {
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
	form.Set("line_items[0][quantity]", fmt.Sprintf("%d", Defaults().StripeCheckoutQuantity))
	if in.ConnectedAccountID != "" {
		form.Set("subscription_data[transfer_data][destination]", in.ConnectedAccountID)
	}
	if in.PlatformFeePercent > int(0) {
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

// StripeSubscriptionDetail is the fields needed to replace a subscription item's price.
type StripeSubscriptionDetail struct {
	ID      string
	ItemID  string
	Product string
}

// GetSubscription fetches a Stripe subscription's first item id and product id.
func (s *Stripe) GetSubscription(stripeSubID string) (StripeSubscriptionDetail, error) {
	stripeSubID = strings.TrimSpace(stripeSubID)
	if stripeSubID == "" {
		return StripeSubscriptionDetail{}, invalid("subscription", "stripe subscription id is required")
	}
	var raw struct {
		ID    string `json:"id"`
		Items struct {
			Data []struct {
				ID    string `json:"id"`
				Price struct {
					Product string `json:"product"`
				} `json:"price"`
			} `json:"data"`
		} `json:"items"`
	}
	if err := s.doForm(http.MethodGet, "/v1/subscriptions/"+url.PathEscape(stripeSubID), nil, &raw); err != nil {
		return StripeSubscriptionDetail{}, err
	}
	if len(raw.Items.Data) == 0 || raw.Items.Data[int(0)].ID == "" || raw.Items.Data[int(0)].Price.Product == "" {
		return StripeSubscriptionDetail{}, errors.New("marketplace: stripe subscription item or product missing")
	}
	item := raw.Items.Data[int(0)]
	return StripeSubscriptionDetail{ID: raw.ID, ItemID: item.ID, Product: item.Price.Product}, nil
}

// UpdateSubscriptionPrice changes the recurring amount on an existing Stripe subscription
// item. Real Stripe requires the item id and a product (or Price id); the local fake
// enforces the same fields.
func (s *Stripe) UpdateSubscriptionPrice(stripeSubID string, amountCents int64, currency, interval string) error {
	detail, err := s.GetSubscription(stripeSubID)
	if err != nil {
		return err
	}
	form := url.Values{}
	form.Set("items[0][id]", detail.ItemID)
	form.Set("items[0][price_data][currency]", strings.ToLower(currency))
	form.Set("items[0][price_data][unit_amount]", fmt.Sprintf("%d", amountCents))
	form.Set("items[0][price_data][recurring][interval]", interval)
	form.Set("items[0][price_data][product]", detail.Product)
	form.Set("proration_behavior", "none")
	return s.doForm(http.MethodPost, "/v1/subscriptions/"+url.PathEscape(stripeSubID), form, &struct{}{})
}

// VerifyWebhookSignature checks a Stripe-Signature header (t=…,v1=…) over the raw body.
// secret is the webhook signing secret from the operator's secret file (never stored in state).
// tolerance must be positive; callers pass Config.StripeWebhookTolerance().
func VerifyWebhookSignature(payload []byte, sigHeader, secret string, now time.Time, tolerance time.Duration) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return invalid("stripe_webhook", "webhook signing secret is not configured")
	}
	if !(tolerance > time.Duration(int(0))) {
		return invalid("stripe_webhook", "webhook signature tolerance must be positive")
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
	if ts == int64(0) || v1 == "" {
		return invalid("stripe_webhook", "Stripe-Signature must include t and v1")
	}
	eventTime := time.Unix(ts, int64(0)).UTC()
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

// InvoicePaidInfo is the Stripe invoice.paid payload fields the marketplace applies.
type InvoicePaidInfo struct {
	EventID            string
	InvoiceID          string
	StripeSubscription string
	BillingReason      string
	PeriodEnd          time.Time
}

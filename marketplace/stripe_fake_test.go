package marketplace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
)

// FakeStripe is a local Stripe stand-in for tests. It never contacts api.stripe.com.
type FakeStripe struct {
	Server  *httptest.Server
	BaseURL string
	mu      sync.Mutex
	seq     atomic.Uint64
	accts   map[string]struct{}
	sess    map[string]fakeSession
	subs    map[string]fakeSub
	// FailCancel makes the next subscription cancel update return an error.
	FailCancel bool
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
	ProductID         string
}

// StartFakeStripe listens on 127.0.0.1 and serves a minimal Stripe API. The returned
// BaseURL always passes RequireLocalStripeBase.
func StartFakeStripe() *FakeStripe {
	f := &FakeStripe{accts: map[string]struct{}{}, sess: map[string]fakeSession{}, subs: map[string]fakeSub{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/accounts", f.createAccount)
	mux.HandleFunc("POST /v1/checkout/sessions", f.createCheckout)
	mux.HandleFunc("POST /v1/subscriptions/{id}", f.updateSub)
	mux.HandleFunc("GET /v1/subscriptions/{id}", f.getSub)
	mux.HandleFunc("GET /v1/checkout/sessions/{id}", f.getSession)
	f.Server = httptest.NewServer(mux)
	f.BaseURL = f.Server.URL
	return f
}

// Close shuts the fake server down.
func (f *FakeStripe) Close() { f.Server.Close() }

func (f *FakeStripe) nextID(prefix string) string {
	return fmt.Sprintf("%s_fake_%d", prefix, f.seq.Add(uint64(Defaults().StripeCheckoutQuantity)))
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
	productID := f.nextID("prod")
	sess := fakeSession{
		ID: id, URL: f.BaseURL + "/pay/" + id, SubscriptionID: subMeta,
		InstallID: install, ModuleID: module, StripeSubID: stripeSub,
		PaymentStatus: "unpaid", AmountCents: amount, Currency: currency, Interval: interval,
	}
	f.mu.Lock()
	f.sess[id] = sess
	f.subs[stripeSub] = fakeSub{
		ID: stripeSub, AmountCents: amount, Currency: currency, Interval: interval,
		ItemID: itemID, ProductID: productID,
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

func (f *FakeStripe) getSub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	sub, ok := f.subs[id]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "object": "subscription", "cancel_at_period_end": sub.CancelAtPeriodEnd,
		"items": map[string]any{"data": []map[string]any{{
			"id": sub.ItemID,
			"price": map[string]any{
				"unit_amount": sub.AmountCents, "currency": sub.Currency, "product": sub.ProductID,
				"recurring": map[string]any{"interval": sub.Interval},
			},
		}}},
	})
}

func (f *FakeStripe) updateSub(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id := r.PathValue("id")
	f.mu.Lock()
	if f.FailCancel && r.Form.Get("cancel_at_period_end") == "true" {
		f.FailCancel = false
		f.mu.Unlock()
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"message": "synthetic cancel failure"}})
		return
	}
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
		// Real Stripe requires item id and product; refuse updates that omit them.
		if r.Form.Get("items[0][id]") == "" || r.Form.Get("items[0][price_data][product]") == "" {
			f.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{"message": "items[0][id] and items[0][price_data][product] are required"},
			})
			return
		}
		if r.Form.Get("items[0][id]") != sub.ItemID {
			f.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"message": "unknown subscription item"}})
			return
		}
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
				"unit_amount": sub.AmountCents, "currency": sub.Currency, "product": sub.ProductID,
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
	return InvoicePaidEventFull("evt_fake_invoice_paid", "in_fake_1", stripeSubID, "subscription_cycle", 0)
}

// InvoicePaidEventFull builds an invoice.paid event with Stripe's real period shape: the
// top-level period_end is the previous usage period (or zero), and lines.data[0].period.end
// is the newly paid term. Prefer InvoicePaidEventRealShape when both ends matter.
func InvoicePaidEventFull(eventID, invoiceID, stripeSubID, billingReason string, periodEndUnix int64) []byte {
	previous := int64(0)
	if periodEndUnix > int64(0) {
		// Approximate the prior period end so tests that used top-level period_end alone would fail.
		previous = periodEndUnix - int64(30*24*60*60)
	}
	return InvoicePaidEventRealShape(eventID, invoiceID, stripeSubID, billingReason, previous, periodEndUnix)
}

// InvoicePaidEventRealShape builds invoice.paid with explicit previous (top-level) and new
// (line item) period ends, and names the subscription only under parent.subscription_details
// (newer Stripe API shape) so both parsers stay covered.
func InvoicePaidEventRealShape(eventID, invoiceID, stripeSubID, billingReason string, previousPeriodEndUnix, newTermEndUnix int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": eventID, "object": "event", "type": "invoice.paid",
		"data": map[string]any{"object": map[string]any{
			"id": invoiceID, "object": "invoice",
			"paid": true, "status": "paid", "billing_reason": billingReason,
			"period_end": previousPeriodEndUnix,
			"lines": map[string]any{"data": []map[string]any{{
				"type":   "subscription",
				"period": map[string]any{"end": newTermEndUnix},
			}}},
			"parent": map[string]any{
				"type": "subscription_details",
				"subscription_details": map[string]any{
					"subscription": stripeSubID,
				},
			},
		}},
	})
	return b
}

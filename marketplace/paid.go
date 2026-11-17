package marketplace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

const (
	subPending  = "pending"
	subActive   = "active"
	subCanceled = "canceled"
)

// ConnectStripe links the author to a Stripe Connect account. The Stripe account id is
// stored; no Stripe secret is persisted.
func (m *Market) ConnectStripe(a Author) (accountID string, err error) {
	if m.stripe == nil {
		return "", invalid("stripe", "stripe is not configured")
	}
	var existing string
	m.read(func(st *State) {
		for _, x := range st.Authors {
			if x.ID == a.ID {
				existing = x.StripeAccountID
			}
		}
	})
	if existing != "" {
		return existing, nil
	}
	id, err := m.stripe.CreateConnectAccount(a.Email, a.Name)
	if err != nil {
		return "", err
	}
	err = m.commit(func(st *State) error {
		for i := range st.Authors {
			if st.Authors[i].ID == a.ID {
				st.Authors[i].StripeAccountID = id
				return nil
			}
		}
		return ErrNotFound
	})
	return id, err
}

// PriceChangeResult is the listing after a price change plus notice delivery status.
// The change is saved even when some notices fail; failed notices stay queued for
// RetryPendingMail and block ApplyDuePriceChanges until sent.
type PriceChangeResult struct {
	Listing shared.Listing
	Notices []MailRecord
}

// SetListingPrice changes the listing price. New subscriptions use it at once. Existing
// ones keep the old price until the next renewal after the notice period, and each
// subscribing admin is emailed. A paid listing's replacement price must be a complete
// structured price. Notice mail requires a configured mailer; undelivered notices stay
// queued for RetryPendingMail without restarting the notice period.
func (m *Market) SetListingPrice(a Author, listingID string, price ListingPrice) (PriceChangeResult, error) {
	if err := price.validate(); err != nil {
		return PriceChangeResult{}, err
	}
	if price.empty() {
		return PriceChangeResult{}, invalid("price", "a price is required")
	}
	now := m.now()
	days := m.noticeDays
	if days <= int(0) {
		days = Defaults().PriceChangeNoticeDays
	}
	effective := now.Add(dur("h", days*Defaults().HoursPerDay))
	var out ListingRecord
	var pendingMail []MailRecord
	var needMail bool
	m.read(func(st *State) {
		for _, s := range st.Subscriptions {
			if s.ListingID == listingID && s.Status == subActive {
				needMail = true
			}
		}
	})
	if needMail && m.mailer == nil {
		return PriceChangeResult{}, invalid("mail", "a mailer (smtp_addr) is required to notice subscribers of a price change")
	}
	// When replacing a paid structured price, require another complete paid price.
	var wasPaid bool
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.ID == listingID && l.Price.paidStructured() {
				wasPaid = true
			}
		}
	})
	if wasPaid && !price.paidStructured() {
		return PriceChangeResult{}, invalid("price", "a paid listing requires amount_cents, currency and interval")
	}
	err := m.commit(func(st *State) error {
		var l *ListingRecord
		for i := range st.Listings {
			if st.Listings[i].ID == listingID {
				l = &st.Listings[i]
			}
		}
		if l == nil {
			return ErrNotFound
		}
		if l.AuthorID != a.ID {
			return ErrForbidden
		}
		l.Price = price
		l.PriceNote = price.Display()
		out = *l
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.ListingID != listingID || s.Status != subActive {
				continue
			}
			mailID, err := domain.NewID()
			if err != nil {
				return err
			}
			subject := "Price change notice: " + listingID
			body := "The price of " + listingID + " will change to " + price.Display() +
				" for renewals on or after " + effective.UTC().Format("2006-01-02") +
				". New subscriptions use the new price immediately."
			rec := MailRecord{ID: string(mailID), To: s.AdminEmail, Subject: subject, Body: body, Status: mailPending}
			st.Mails = append(st.Mails, rec)
			pendingMail = append(pendingMail, rec)
			s.PendingPrice = &PendingPriceChange{
				Price:        price,
				NoticedAt:    now.Format(time.RFC3339Nano),
				EffectiveAt:  effective.Format(time.RFC3339Nano),
				NoticeMailID: string(mailID),
			}
		}
		return nil
	})
	if err != nil {
		return PriceChangeResult{}, err
	}
	for _, msg := range pendingMail {
		_ = m.deliverMail(msg.ID)
	}
	notices := make([]MailRecord, 0, len(pendingMail))
	m.read(func(st *State) {
		for _, want := range pendingMail {
			for _, x := range st.Mails {
				if x.ID == want.ID {
					notices = append(notices, x)
				}
			}
		}
	})
	return PriceChangeResult{Listing: m.view(out), Notices: notices}, nil
}

func (m *Market) deliverMail(id string) error {
	if m.mailer == nil {
		return invalid("mail", "no mailer is configured")
	}
	var msg MailRecord
	found := false
	m.read(func(st *State) {
		for _, x := range st.Mails {
			if x.ID == id {
				msg, found = x, true
			}
		}
	})
	if !found {
		return ErrNotFound
	}
	if msg.Status == mailSent {
		return nil
	}
	err := m.mailer.Send(msg.To, msg.Subject, msg.Body)
	now := m.now()
	_ = m.commit(func(st *State) error {
		for i := range st.Mails {
			if st.Mails[i].ID != id {
				continue
			}
			if err != nil {
				st.Mails[i].Status = mailFailed
				st.Mails[i].LastErr = err.Error()
				return nil
			}
			st.Mails[i].Status = mailSent
			st.Mails[i].SentAt = now
			st.Mails[i].LastErr = ""
			return nil
		}
		return ErrNotFound
	})
	return err
}

// RetryPendingMail redelivers queued notices that are not yet sent. It does not change
// pending price EffectiveAt values.
func (m *Market) RetryPendingMail() error {
	if m.mailer == nil {
		return invalid("mail", "no mailer is configured")
	}
	var ids []string
	m.read(func(st *State) {
		for _, x := range st.Mails {
			if x.Status == mailPending || x.Status == mailFailed {
				ids = append(ids, x.ID)
			}
		}
	})
	var first error
	for _, id := range ids {
		if err := m.deliverMail(id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// StartCheckout begins Stripe Checkout for a firm subscription. The install id is named
// on the session. The license is issued only after payment completes (CompleteCheckout).
func (m *Market) StartCheckout(listingID, installID, adminEmail, installPubPEM, successURL, cancelURL string) (CheckoutSession, string, error) {
	if m.stripe == nil {
		return CheckoutSession{}, "", invalid("stripe", "stripe is not configured")
	}
	installID = strings.TrimSpace(installID)
	adminEmail = strings.ToLower(strings.TrimSpace(adminEmail))
	if installID == "" || utf8.RuneCountInString(installID) > shared.MaxNoteRunes() {
		return CheckoutSession{}, "", invalid("install_id", "a Togavsu install id is required")
	}
	if a, err := mail.ParseAddress(adminEmail); err != nil || a.Address != adminEmail {
		return CheckoutSession{}, "", invalid("admin_email", "a valid admin email is required")
	}
	pub, err := shared.ParsePublicKeyPEM(installPubPEM)
	if err != nil {
		return CheckoutSession{}, "", invalid("install_public_key_pem", "the install public key must be one Ed25519 PUBLIC KEY block")
	}
	_ = pub
	var listing ListingRecord
	var author Author
	var fee int
	found, conflict := false, false
	m.read(func(st *State) {
		fee = st.Settings.PlatformFeePercent
		for _, l := range st.Listings {
			if l.ID == listingID {
				listing, found = l, true
			}
		}
		if !found {
			return
		}
		for _, a := range st.Authors {
			if a.ID == listing.AuthorID {
				author = a
			}
		}
		for _, s := range st.Subscriptions {
			if s.ListingID == listingID && s.InstallID == installID && s.Status == subActive {
				conflict = true
			}
		}
	})
	if !found {
		return CheckoutSession{}, "", ErrNotFound
	}
	if conflict {
		return CheckoutSession{}, "", ErrConflict
	}
	if listing.Pulled {
		return CheckoutSession{}, "", ErrPulled
	}
	if author.StripeAccountID == "" {
		return CheckoutSession{}, "", invalid("stripe", "the author has not connected Stripe")
	}
	price := listing.Price
	if !price.paidStructured() {
		return CheckoutSession{}, "", invalid("price", "this listing is not sold as a paid subscription")
	}
	subID, err := domain.NewID()
	if err != nil {
		return CheckoutSession{}, "", err
	}
	sess, err := m.stripe.CreateCheckoutSession(CheckoutInput{
		AmountCents: price.AmountCents, Currency: price.Currency, Interval: price.Interval,
		ProductName: listing.ID, InstallID: installID, ModuleID: listing.ID,
		SubscriptionID: string(subID), ConnectedAccountID: author.StripeAccountID,
		PlatformFeePercent: fee, SuccessURL: successURL, CancelURL: cancelURL,
	})
	if err != nil {
		return CheckoutSession{}, "", err
	}
	now := m.now()
	err = m.commit(func(st *State) error {
		st.Subscriptions = append(st.Subscriptions, SubscriptionRecord{
			ID: string(subID), ListingID: listing.ID, InstallID: installID, AdminEmail: adminEmail,
			InstallPublicKey: shared.PublicKeyPEM(pub), StripeSessionID: sess.ID,
			Status: subPending, Price: price, CreatedAt: now,
		})
		return nil
	})
	return sess, string(subID), err
}

// CompleteCheckout activates the subscription only after Stripe reports the Checkout
// Session as complete with payment_status paid. The Stripe subscription id is taken
// from Stripe, never from the caller (§17.3). A session whose subscription has left
// pending (including canceled) or already shown its key is refused.
func (m *Market) CompleteCheckout(sessionID string) (licenseKey string, sub SubscriptionRecord, err error) {
	if m.stripe == nil {
		return "", SubscriptionRecord{}, invalid("stripe", "stripe is not configured")
	}
	if len(m.licensePriv) != ed25519.PrivateKeySize {
		return "", SubscriptionRecord{}, errorsLicenseKey()
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", SubscriptionRecord{}, invalid("session_id", "session_id is required")
	}
	detail, err := m.stripe.GetCheckoutSession(sessionID)
	if err != nil {
		return "", SubscriptionRecord{}, err
	}
	if detail.Status != "complete" || detail.PaymentStatus != "paid" {
		return "", SubscriptionRecord{}, invalid("session", "checkout is not paid")
	}
	stripeSubID := strings.TrimSpace(detail.Subscription)
	if stripeSubID == "" {
		return "", SubscriptionRecord{}, invalid("session", "stripe subscription id missing from checkout session")
	}
	now := m.now()
	err = m.commit(func(st *State) error {
		var s *SubscriptionRecord
		for i := range st.Subscriptions {
			if st.Subscriptions[i].StripeSessionID == sessionID {
				s = &st.Subscriptions[i]
			}
		}
		if s == nil {
			return ErrNotFound
		}
		if meta := detail.Metadata; meta != nil {
			if id := meta["install_id"]; id != "" && id != s.InstallID {
				return invalid("session", "checkout install_id does not match the pending subscription")
			}
			if id := meta["module_id"]; id != "" && id != s.ListingID {
				return invalid("session", "checkout module_id does not match the pending subscription")
			}
			if id := meta["subscription_id"]; id != "" && id != s.ID {
				return invalid("session", "checkout subscription_id does not match the pending subscription")
			}
		}
		if s.Status != subPending || s.LicenseShown {
			sub = *s
			return invalid("session", "checkout is no longer pending")
		}
		paidThrough := addInterval(now, s.Price.Interval)
		key, err := IssueLicense(m.licensePriv, s.InstallID, s.ListingID, paidThrough)
		if err != nil {
			return err
		}
		s.Status = subActive
		s.StripeSubscription = stripeSubID
		s.PaidThrough = paidThrough
		s.LicenseShown = true
		licenseKey = key
		sub = *s
		return nil
	})
	return licenseKey, sub, err
}

func errorsLicenseKey() error {
	return invalid("license", "the marketplace license signing key is not configured")
}

func addInterval(from time.Time, interval string) time.Time {
	d := Defaults()
	switch interval {
	case "year":
		return from.UTC().AddDate(d.IntervalYearStep, int(0), int(0))
	default:
		return from.UTC().AddDate(int(0), d.IntervalMonthStep, int(0))
	}
}

// CancelSubscription stops renewal after the install proves possession of its key; the
// license runs to its paid-through date. Stripe cancel must succeed before local state
// is committed so a failed Stripe call stays retryable.
func (m *Market) CancelSubscription(installID, moduleID, signature string) (SubscriptionRecord, error) {
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
	var stripeSub string
	var found bool
	var verifyErr error
	m.read(func(st *State) {
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.InstallID != installID || s.ListingID != moduleID || s.Status != subActive {
				continue
			}
			found = true
			pub, err := shared.ParsePublicKeyPEM(s.InstallPublicKey)
			if err != nil {
				verifyErr = invalid("install_public_key_pem", "the stored install key is unreadable")
				return
			}
			if shared.VerifySignature(pub, CancelProofMessage(installID, moduleID), signature) != nil {
				verifyErr = invalid("signature", "the signature does not prove this install")
				return
			}
			stripeSub = s.StripeSubscription
			return
		}
	})
	if verifyErr != nil {
		return SubscriptionRecord{}, verifyErr
	}
	if !found {
		return SubscriptionRecord{}, ErrNotFound
	}
	if stripeSub != "" && m.stripe != nil {
		if err := m.stripe.CancelSubscription(stripeSub); err != nil {
			return SubscriptionRecord{}, err
		}
	}
	var out SubscriptionRecord
	err := m.commit(func(st *State) error {
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.InstallID == installID && s.ListingID == moduleID && s.Status == subActive {
				pub, err := shared.ParsePublicKeyPEM(s.InstallPublicKey)
				if err != nil {
					return invalid("install_public_key_pem", "the stored install key is unreadable")
				}
				if shared.VerifySignature(pub, CancelProofMessage(installID, moduleID), signature) != nil {
					return invalid("signature", "the signature does not prove this install")
				}
				s.CancelAtPeriodEnd = true
				s.Status = subCanceled
				out = *s
				return nil
			}
		}
		return ErrNotFound
	})
	return out, err
}

// RenewLicense re-serves the current key when the install proves possession of its key.
// It never extends paid_through: that happens only on a Stripe-verified invoice.paid
// event (ApplyInvoicePaid). Canceled subscriptions still fetch the key until paid_through.
// When a pending price's notice period has elapsed, Stripe is updated before the next bill.
func (m *Market) RenewLicense(installID, moduleID, signature string) (string, time.Time, error) {
	if len(m.licensePriv) != ed25519.PrivateKeySize {
		return "", time.Time{}, errorsLicenseKey()
	}
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
	if _, err := m.ApplyDuePriceChanges(); err != nil {
		return "", time.Time{}, err
	}
	var key string
	var paidThrough time.Time
	now := m.now()
	err := m.commit(func(st *State) error {
		var s *SubscriptionRecord
		for i := range st.Subscriptions {
			x := &st.Subscriptions[i]
			if x.InstallID == installID && x.ListingID == moduleID && (x.Status == subActive || x.Status == subCanceled) {
				s = x
			}
		}
		if s == nil {
			return ErrNotFound
		}
		pub, err := shared.ParsePublicKeyPEM(s.InstallPublicKey)
		if err != nil {
			return invalid("install_public_key_pem", "the stored install key is unreadable")
		}
		if shared.VerifySignature(pub, RenewProofMessage(installID, moduleID), signature) != nil {
			return invalid("signature", "the signature does not prove this install")
		}
		day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), int(0), int(0), int(0), int(0), time.UTC)
		throughDay := time.Date(s.PaidThrough.UTC().Year(), s.PaidThrough.UTC().Month(), s.PaidThrough.UTC().Day(), int(0), int(0), int(0), int(0), time.UTC)
		if day.After(throughDay) {
			return ErrLicenseInvalid
		}
		k, err := IssueLicense(m.licensePriv, s.InstallID, s.ListingID, s.PaidThrough)
		if err != nil {
			return err
		}
		key, paidThrough = k, s.PaidThrough
		return nil
	})
	return key, paidThrough, err
}

// ApplyDuePriceChanges updates Stripe (and local subscription price) for pending price
// changes whose notice period has ended and whose notice mail was delivered, before the
// next renewal invoice is billed.
func (m *Market) ApplyDuePriceChanges() ([]SubscriptionRecord, error) {
	now := m.now()
	type due struct {
		stripeSub string
		price     ListingPrice
		subID     string
	}
	var todos []due
	err := m.commit(func(st *State) error {
		mailStatus := map[string]string{}
		for _, x := range st.Mails {
			mailStatus[x.ID] = x.Status
		}
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.Status != subActive || s.PendingPrice == nil || s.PendingPrice.StripeSynced {
				continue
			}
			if mid := strings.TrimSpace(s.PendingPrice.NoticeMailID); mid != "" {
				if mailStatus[mid] != mailSent {
					continue
				}
			}
			eff, err := time.Parse(time.RFC3339Nano, s.PendingPrice.EffectiveAt)
			if err != nil || now.Before(eff) {
				continue
			}
			p := s.PendingPrice.Price
			if !p.paidStructured() {
				return invalid("price", "pending price is not a complete structured price")
			}
			todos = append(todos, due{stripeSub: s.StripeSubscription, price: p, subID: s.ID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []SubscriptionRecord
	for _, t := range todos {
		if m.stripe != nil && t.stripeSub != "" {
			if err := m.stripe.UpdateSubscriptionPrice(t.stripeSub, t.price.AmountCents, t.price.Currency, t.price.Interval); err != nil {
				return out, err
			}
		}
		err := m.commit(func(st *State) error {
			for i := range st.Subscriptions {
				s := &st.Subscriptions[i]
				if s.ID != t.subID || s.PendingPrice == nil {
					continue
				}
				s.Price = t.price
				s.PendingPrice.StripeSynced = true
				s.PendingPrice = nil
				out = append(out, *s)
				return nil
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// ApplyInvoicePaid sets paid_through from the invoice period end after Stripe reports a
// paid invoice. Event and invoice ids are recorded so redeliveries do not extend again.
// subscription_create invoices do not stack an extra period on top of CompleteCheckout.
func (m *Market) ApplyInvoicePaid(info InvoicePaidInfo) (SubscriptionRecord, error) {
	stripeSubID := strings.TrimSpace(info.StripeSubscription)
	if stripeSubID == "" {
		return SubscriptionRecord{}, invalid("subscription", "stripe subscription id is required")
	}
	eventID := strings.TrimSpace(info.EventID)
	invoiceID := strings.TrimSpace(info.InvoiceID)
	if _, err := m.ApplyDuePriceChanges(); err != nil {
		return SubscriptionRecord{}, err
	}
	var out SubscriptionRecord
	err := m.commit(func(st *State) error {
		if eventID != "" {
			for _, id := range st.ProcessedStripeEvents {
				if id == eventID {
					for i := range st.Subscriptions {
						if st.Subscriptions[i].StripeSubscription == stripeSubID {
							out = st.Subscriptions[i]
							return nil
						}
					}
					return ErrNotFound
				}
			}
		}
		if invoiceID != "" {
			for _, id := range st.ProcessedStripeInvoices {
				if id == invoiceID {
					for i := range st.Subscriptions {
						if st.Subscriptions[i].StripeSubscription == stripeSubID {
							out = st.Subscriptions[i]
							return nil
						}
					}
					return ErrNotFound
				}
			}
		}
		var s *SubscriptionRecord
		for i := range st.Subscriptions {
			x := &st.Subscriptions[i]
			if x.StripeSubscription == stripeSubID {
				s = x
			}
		}
		if s == nil {
			return ErrNotFound
		}
		if eventID != "" {
			st.ProcessedStripeEvents = append(st.ProcessedStripeEvents, eventID)
		}
		if invoiceID != "" {
			st.ProcessedStripeInvoices = append(st.ProcessedStripeInvoices, invoiceID)
		}
		if s.Status != subActive || s.CancelAtPeriodEnd {
			out = *s
			return nil
		}
		// First invoice for a new subscription: CompleteCheckout already set paid_through.
		if info.BillingReason == "subscription_create" {
			if !info.PeriodEnd.IsZero() && info.PeriodEnd.After(s.PaidThrough) {
				s.PaidThrough = info.PeriodEnd.UTC()
			}
			out = *s
			return nil
		}
		if !info.PeriodEnd.IsZero() {
			s.PaidThrough = info.PeriodEnd.UTC()
		} else {
			s.PaidThrough = addInterval(s.PaidThrough, s.Price.Interval)
		}
		out = *s
		return nil
	})
	return out, err
}

// PlatformFeePercent returns the operator platform fee setting (default 0).
func (m *Market) PlatformFeePercent() int {
	var n int
	m.read(func(st *State) { n = st.Settings.PlatformFeePercent })
	return n
}

// SetPlatformFeePercent sets the Connect application fee percent (0–max).
func (m *Market) SetPlatformFeePercent(n int) error {
	max := Defaults().MaxPlatformFeePercent
	if n < int(0) || n > max {
		return invalid("platform_fee_percent", "must be 0 to 100")
	}
	return m.commit(func(st *State) error {
		st.Settings.PlatformFeePercent = n
		return nil
	})
}

// Mails returns queued notice emails (for tests and operator inspection).
func (m *Market) Mails() []MailRecord {
	var out []MailRecord
	m.read(func(st *State) { out = append(out, st.Mails...) })
	return out
}

// Subscription looks up a subscription by install and module.
func (m *Market) Subscription(installID, moduleID string) (SubscriptionRecord, error) {
	var out SubscriptionRecord
	found := false
	m.read(func(st *State) {
		for _, s := range st.Subscriptions {
			if s.InstallID == installID && s.ListingID == moduleID {
				out, found = s, true
			}
		}
	})
	if !found {
		return SubscriptionRecord{}, ErrNotFound
	}
	return out, nil
}

// ListingPriceOf returns the structured price on a listing.
func (m *Market) ListingPriceOf(id string) (ListingPrice, error) {
	var p ListingPrice
	found := false
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.ID == id {
				p, found = l.Price, true
			}
		}
	})
	if !found {
		return ListingPrice{}, ErrNotFound
	}
	return p, nil
}

// LicensePublicKeyPEM returns the marketplace license public key.
func (m *Market) LicensePublicKeyPEM() string {
	if len(m.licensePub) != ed25519.PublicKeySize {
		return ""
	}
	return shared.PublicKeyPEM(m.licensePub)
}

// EnsureLicenseKey installs a random license keypair for tests only. The service binary
// must load a stable seed via ApplyConfig / SetLicenseSeed; Open never generates keys.
func (m *Market) EnsureLicenseKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.licensePriv) == ed25519.PrivateKeySize {
		return nil
	}
	if !m.allowTestKeys {
		return invalid("license_seed_file", "license signing key is not configured; set license_seed_file")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	m.licensePriv = priv
	m.licensePub = priv.Public().(ed25519.PublicKey)
	return nil
}

// AllowTestLicenseKeys permits EnsureLicenseKey to mint a random key (tests only).
func (m *Market) AllowTestLicenseKeys() {
	m.mu.Lock()
	m.allowTestKeys = true
	m.mu.Unlock()
}

// HasLicenseKey reports whether a license signing key is configured.
func (m *Market) HasLicenseKey() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.licensePriv) == ed25519.PrivateKeySize
}

// SetLicenseSeed sets the license key from a 32-byte Ed25519 seed (raw or standard base64).
func (m *Market) SetLicenseSeed(seed []byte) error {
	if len(seed) != ed25519.SeedSize {
		decoded := make([]byte, ed25519.SeedSize)
		n, err := base64.StdEncoding.Decode(decoded, bytesTrim(seed))
		if err != nil || n != ed25519.SeedSize {
			return invalid("license_seed_file", "the license seed must be 32 raw or base64 bytes")
		}
		seed = decoded
	}
	priv := ed25519.NewKeyFromSeed(seed)
	m.mu.Lock()
	m.licensePriv = priv
	m.licensePub = priv.Public().(ed25519.PublicKey)
	m.mu.Unlock()
	return nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// SetStripe attaches a Stripe client (fake in tests).
func (m *Market) SetStripe(s *Stripe) { m.mu.Lock(); m.stripe = s; m.mu.Unlock() }

// SetMailer attaches the outbound mail sender (SMTP in production, RecordingMailer in tests).
func (m *Market) SetMailer(mailer MailSender) { m.mu.Lock(); m.mailer = mailer; m.mu.Unlock() }

// StartMailRetry runs RetryPendingMail on interval until ctx is cancelled. The marketplace
// binary starts this so failed price-change notices are resent without an operator call.
// Caller must pass a positive interval (see Config.MailRetryIntervalSeconds).
func (m *Market) StartMailRetry(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_ = m.RetryPendingMail()
			}
		}
	}()
}

// SetWebhookSecret sets the Stripe webhook signing secret (memory only; never in state).
func (m *Market) SetWebhookSecret(secret string) {
	m.mu.Lock()
	m.webhookSecret = strings.TrimSpace(secret)
	m.mu.Unlock()
}

// WebhookSecret returns the in-memory Stripe webhook signing secret.
func (m *Market) WebhookSecret() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.webhookSecret
}

// SetNoticeDays sets the price-change notice period in days (default from fixed.json).
func (m *Market) SetNoticeDays(days int) {
	if days <= int(0) {
		days = Defaults().PriceChangeNoticeDays
	}
	m.mu.Lock()
	m.noticeDays = days
	m.mu.Unlock()
}

// ApplyConfig attaches paid-listing options from the service config. When Stripe is
// configured, a license seed file is required so restarts keep the same public key.
func (m *Market) ApplyConfig(cfg Config) error {
	m.SetNoticeDays(cfg.PriceChangeNoticeDays)
	if cfg.PlatformFeePercent != int(0) {
		if err := m.SetPlatformFeePercent(cfg.PlatformFeePercent); err != nil {
			return err
		}
	}
	seedPath := strings.TrimSpace(cfg.LicenseSeedFile)
	if seedPath != "" {
		b, err := os.ReadFile(seedPath)
		if err != nil {
			return fmt.Errorf("marketplace: license seed: %w", err)
		}
		if err := m.SetLicenseSeed(b); err != nil {
			return err
		}
	}
	secret, err := cfg.StripeSecret()
	if err != nil {
		return err
	}
	base := strings.TrimSpace(cfg.StripeAPIBase)
	if base != "" {
		if seedPath == "" && !m.HasLicenseKey() {
			return fmt.Errorf("marketplace: license_seed_file is required when stripe_api_base is set")
		}
		s, err := NewStripe(base, secret)
		if err != nil {
			return err
		}
		m.SetStripe(s)
	}
	if wh, err := cfg.StripeWebhookSecret(); err != nil {
		return err
	} else if wh != "" {
		m.SetWebhookSecret(wh)
	}
	if addr := strings.TrimSpace(cfg.SMTPAddr); addr != "" {
		from := strings.TrimSpace(cfg.SMTPFrom)
		if from == "" {
			from = cfg.DefaultSMTPFrom
		}
		m.SetMailer(SMTPMailer{Addr: addr, From: from})
	}
	return nil
}

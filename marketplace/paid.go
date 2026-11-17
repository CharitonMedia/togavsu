package marketplace

import (
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

// SetListingPrice changes the listing price. New subscriptions use it at once. Existing
// ones keep the old price until the next renewal after the notice period, and each
// subscribing admin is emailed.
func (m *Market) SetListingPrice(a Author, listingID string, price ListingPrice) (shared.Listing, error) {
	if err := price.validate(); err != nil {
		return shared.Listing{}, err
	}
	if price.empty() {
		return shared.Listing{}, invalid("price", "a price is required")
	}
	now := m.now()
	effective := now.Add(time.Duration(m.noticeDays) * 24 * time.Hour)
	var out ListingRecord
	var pendingMail []MailRecord
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
		pending := &PendingPriceChange{
			Price:       price,
			NoticedAt:   now.Format(time.RFC3339Nano),
			EffectiveAt: effective.Format(time.RFC3339Nano),
		}
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.ListingID != listingID || s.Status != subActive {
				continue
			}
			s.PendingPrice = pending
			mailID, err := domain.NewID()
			if err != nil {
				return err
			}
			subject := "Price change notice: " + listingID
			body := "The price of " + listingID + " will change to " + price.Display() +
				" for renewals on or after " + effective.UTC().Format("2006-01-02") +
				". New subscriptions use the new price immediately."
			rec := MailRecord{ID: string(mailID), To: s.AdminEmail, Subject: subject, Body: body, SentAt: now}
			st.Mails = append(st.Mails, rec)
			pendingMail = append(pendingMail, rec)
		}
		return nil
	})
	if err != nil {
		return shared.Listing{}, err
	}
	for _, msg := range pendingMail {
		if err := m.sendMail(msg.To, msg.Subject, msg.Body); err != nil {
			return shared.Listing{}, err
		}
	}
	return m.view(out), nil
}

func (m *Market) sendMail(to, subject, body string) error {
	if m.mailer == nil {
		return nil
	}
	return m.mailer.Send(to, subject, body)
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
	if price.empty() || price.AmountCents <= 0 {
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
// from Stripe, never from the caller (§17.3).
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
		if s.Status == subActive && s.LicenseShown {
			sub = *s
			return invalid("session", "the license key was already shown")
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
	switch interval {
	case "year":
		return from.UTC().AddDate(1, 0, 0)
	default:
		return from.UTC().AddDate(0, 1, 0)
	}
}

// CancelSubscription stops renewal after the install proves possession of its key; the
// license runs to its paid-through date. A forged or missing signature is refused.
func (m *Market) CancelSubscription(installID, moduleID, signature string) (SubscriptionRecord, error) {
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
	var out SubscriptionRecord
	var stripeSub string
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
				stripeSub = s.StripeSubscription
				out = *s
				return nil
			}
		}
		return ErrNotFound
	})
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if stripeSub != "" && m.stripe != nil {
		_ = m.stripe.CancelSubscription(stripeSub)
	}
	return out, nil
}

// RenewLicense re-serves the current key when the install proves possession of its key.
// It never extends paid_through: that happens only on a Stripe-verified invoice.paid
// event (ApplyInvoicePaid). Canceled subscriptions still fetch the key until paid_through.
func (m *Market) RenewLicense(installID, moduleID, signature string) (string, time.Time, error) {
	if len(m.licensePriv) != ed25519.PrivateKeySize {
		return "", time.Time{}, errorsLicenseKey()
	}
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
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
		day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
		throughDay := time.Date(s.PaidThrough.UTC().Year(), s.PaidThrough.UTC().Month(), s.PaidThrough.UTC().Day(), 0, 0, 0, 0, time.UTC)
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

// ApplyInvoicePaid extends paid_through after Stripe reports a paid invoice for the
// subscription. Pending price changes whose notice period has elapsed are applied and
// the Stripe subscription amount is updated to match.
func (m *Market) ApplyInvoicePaid(stripeSubID string) (SubscriptionRecord, error) {
	stripeSubID = strings.TrimSpace(stripeSubID)
	if stripeSubID == "" {
		return SubscriptionRecord{}, invalid("subscription", "stripe subscription id is required")
	}
	now := m.now()
	var out SubscriptionRecord
	var priceUpdate *ListingPrice
	err := m.commit(func(st *State) error {
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
		if s.Status != subActive || s.CancelAtPeriodEnd {
			// Canceled / cancel-at-period-end: do not extend; key still runs to paid_through.
			out = *s
			return nil
		}
		if s.PendingPrice != nil {
			eff, err := time.Parse(time.RFC3339Nano, s.PendingPrice.EffectiveAt)
			if err == nil && !now.Before(eff) {
				s.Price = s.PendingPrice.Price
				s.PendingPrice = nil
				p := s.Price
				priceUpdate = &p
			}
		}
		s.PaidThrough = addInterval(s.PaidThrough, s.Price.Interval)
		out = *s
		return nil
	})
	if err != nil {
		return SubscriptionRecord{}, err
	}
	if priceUpdate != nil && m.stripe != nil {
		if err := m.stripe.UpdateSubscriptionPrice(stripeSubID, priceUpdate.AmountCents, priceUpdate.Currency, priceUpdate.Interval); err != nil {
			return out, err
		}
	}
	return out, nil
}

// PlatformFeePercent returns the operator platform fee setting (default 0).
func (m *Market) PlatformFeePercent() int {
	var n int
	m.read(func(st *State) { n = st.Settings.PlatformFeePercent })
	return n
}

// SetPlatformFeePercent sets the Connect application fee percent (0–100).
func (m *Market) SetPlatformFeePercent(n int) error {
	if n < 0 || n > 100 {
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

// SetNoticeDays sets the price-change notice period in days (default 30).
func (m *Market) SetNoticeDays(days int) {
	if days <= 0 {
		days = 30
	}
	m.mu.Lock()
	m.noticeDays = days
	m.mu.Unlock()
}

// ApplyConfig attaches paid-listing options from the service config. When Stripe is
// configured, a license seed file is required so restarts keep the same public key.
func (m *Market) ApplyConfig(cfg Config) error {
	m.SetNoticeDays(cfg.PriceChangeNoticeDays)
	if cfg.PlatformFeePercent != 0 {
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
			from = "marketplace@localhost"
		}
		m.SetMailer(SMTPMailer{Addr: addr, From: from})
	}
	return nil
}

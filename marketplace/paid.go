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
			st.Mails = append(st.Mails, MailRecord{
				ID: string(mailID), To: s.AdminEmail,
				Subject: "Price change notice: " + listingID,
				Body: "The price of " + listingID + " will change to " + price.Display() +
					" for renewals on or after " + effective.UTC().Format("2006-01-02") +
					". New subscriptions use the new price immediately.",
				SentAt: now,
			})
		}
		return nil
	})
	if err != nil {
		return shared.Listing{}, err
	}
	return m.view(out), nil
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

// CompleteCheckout activates the subscription after fake or real Stripe reports payment
// and returns the license key once.
func (m *Market) CompleteCheckout(sessionID, stripeSubID string) (licenseKey string, sub SubscriptionRecord, err error) {
	if len(m.licensePriv) != ed25519.PrivateKeySize {
		return "", SubscriptionRecord{}, errorsLicenseKey()
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

// CancelSubscription stops renewal; the key runs to its paid-through date.
func (m *Market) CancelSubscription(installID, moduleID string) (SubscriptionRecord, error) {
	installID = strings.TrimSpace(installID)
	moduleID = strings.TrimSpace(moduleID)
	var out SubscriptionRecord
	var stripeSub string
	err := m.commit(func(st *State) error {
		for i := range st.Subscriptions {
			s := &st.Subscriptions[i]
			if s.InstallID == installID && s.ListingID == moduleID && s.Status == subActive {
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

// RenewLicense returns the current key when the install proves possession of its key.
// Canceled subscriptions still renew the key until paid_through. After that date the
// call is refused. When a pending price change's notice has elapsed, the new price is
// applied and paid_through is extended (simulating the next successful renewal charge).
func (m *Market) RenewLicense(installID, moduleID, signature string, extend bool) (string, time.Time, error) {
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
		// Apply a pending price after the notice period when this renewal extends the term.
		if extend && s.Status == subActive && !s.CancelAtPeriodEnd && s.PendingPrice != nil {
			eff, err := time.Parse(time.RFC3339Nano, s.PendingPrice.EffectiveAt)
			if err == nil && !now.Before(eff) {
				s.Price = s.PendingPrice.Price
				s.PendingPrice = nil
			}
		}
		if extend && s.Status == subActive && !s.CancelAtPeriodEnd {
			s.PaidThrough = addInterval(s.PaidThrough, s.Price.Interval)
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

// EnsureLicenseKey installs a random license keypair when none is configured (tests and
// local runs). Production should load one from a seed file via SetLicenseSeed.
func (m *Market) EnsureLicenseKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.licensePriv) == ed25519.PrivateKeySize {
		return nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	m.licensePriv = priv
	m.licensePub = priv.Public().(ed25519.PublicKey)
	return nil
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

// SetNoticeDays sets the price-change notice period in days (default 30).
func (m *Market) SetNoticeDays(days int) {
	if days <= 0 {
		days = 30
	}
	m.mu.Lock()
	m.noticeDays = days
	m.mu.Unlock()
}

// ApplyConfig attaches paid-listing options from the service config.
func (m *Market) ApplyConfig(cfg Config) error {
	m.SetNoticeDays(cfg.PriceChangeNoticeDays)
	if cfg.PlatformFeePercent != 0 {
		if err := m.SetPlatformFeePercent(cfg.PlatformFeePercent); err != nil {
			return err
		}
	}
	if path := strings.TrimSpace(cfg.LicenseSeedFile); path != "" {
		b, err := os.ReadFile(path)
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
	if base := strings.TrimSpace(cfg.StripeAPIBase); base != "" {
		s, err := NewStripe(base, secret)
		if err != nil {
			return err
		}
		m.SetStripe(s)
	}
	return nil
}

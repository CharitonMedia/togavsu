package marketplace

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the service's own configuration, read from a JSON file named on the command
// line (config.example.json). Fields left out keep the defaults in fixed.json. The
// operator token is read from a file, a Docker secret, never from the config itself.
type Config struct {
	Listen                   string `json:"listen"`
	DataDir                  string `json:"data_dir"`
	OperatorTokenFile        string `json:"operator_token_file"`
	SecureCookies            bool   `json:"secure_cookies"`
	AuthorSessionHours       int    `json:"author_session_hours"`
	OperatorSessionMinutes   int    `json:"operator_session_minutes"`
	ReadHeaderTimeoutSeconds int    `json:"read_header_timeout_seconds"`
	MaxJSONBytes             int64  `json:"max_json_bytes"`
	MinOperatorTokenBytes    int    `json:"min_operator_token_bytes"`
	// PriceChangeNoticeDays is how long before a price change hits existing subscriptions (default 30).
	PriceChangeNoticeDays int `json:"price_change_notice_days"`
	// PlatformFeePercent is the default Connect application fee when state has none (default 0).
	PlatformFeePercent int `json:"platform_fee_percent"`
	// MaxPlatformFeePercent is the inclusive upper bound for the operator fee setting.
	MaxPlatformFeePercent int `json:"max_platform_fee_percent"`
	// CurrencyCodeLength is the required ISO-4217 alphabetic currency width.
	CurrencyCodeLength int `json:"currency_code_length"`
	// CentsPerMajorUnit converts amount_cents to major currency units for display.
	CentsPerMajorUnit int64 `json:"cents_per_major_unit"`
	// IntervalYearStep / IntervalMonthStep are AddDate steps for paid-through intervals.
	IntervalYearStep  int `json:"interval_year_step"`
	IntervalMonthStep int `json:"interval_month_step"`
	// HoursPerDay converts notice days into a duration.
	HoursPerDay int `json:"hours_per_day"`
	// StripeErrorLogRunes caps redacted Stripe error bodies in returned errors.
	StripeErrorLogRunes int `json:"stripe_error_log_runes"`
	// CardDigitMin / CardDigitMax bound digit runs redacted as card numbers in logs.
	CardDigitMin int `json:"card_digit_min"`
	CardDigitMax int `json:"card_digit_max"`
	// StripeWebhookToleranceSeconds is the allowed clock skew for Stripe-Signature.
	StripeWebhookToleranceSeconds int `json:"stripe_webhook_tolerance_seconds"`
	// StripeResponseMaxBytes caps a single Stripe API response body.
	StripeResponseMaxBytes int64 `json:"stripe_response_max_bytes"`
	// StripeCheckoutQuantity is the Checkout line-item quantity (always 1).
	StripeCheckoutQuantity int `json:"stripe_checkout_quantity"`
	// StripeAPIVersion is sent as the Stripe-Version request header.
	StripeAPIVersion string `json:"stripe_api_version"`
	// MailRetryIntervalSeconds is how often the service retries undelivered notice mail.
	MailRetryIntervalSeconds int `json:"mail_retry_interval_seconds"`
	// SMTPDeadlineSeconds bounds one fake/local SMTP conversation.
	SMTPDeadlineSeconds int `json:"smtp_deadline_seconds"`
	// SMTPReadBufferBytes / SMTPTmpBufferBytes size the SMTP acceptor buffers.
	SMTPReadBufferBytes int `json:"smtp_read_buffer_bytes"`
	SMTPTmpBufferBytes  int `json:"smtp_tmp_buffer_bytes"`
	// DefaultSMTPFrom is used when smtp_from is empty.
	DefaultSMTPFrom string `json:"default_smtp_from"`
	// StripeAPIBase is the Stripe API origin. Tests use a local fake; production uses https://api.stripe.com.
	StripeAPIBase string `json:"stripe_api_base"`
	// StripeSecretFile holds the Stripe secret key at runtime (Docker secret). Never logged or stored in state.
	StripeSecretFile string `json:"stripe_secret_file"`
	// StripeWebhookSecretFile holds the Stripe webhook signing secret (Docker secret). Never stored in state.
	StripeWebhookSecretFile string `json:"stripe_webhook_secret_file"`
	// LicenseSeedFile holds 32 raw or base64 bytes for the marketplace license Ed25519 seed.
	// Required whenever stripe_api_base is set so paid modules keep a stable public key across restarts.
	LicenseSeedFile string `json:"license_seed_file"`
	// SMTPAddr is host:port for price-change notice delivery. Empty refuses price changes that
	// need notice mail unless a MailSender is attached in-process (tests use RecordingMailer).
	SMTPAddr string `json:"smtp_addr"`
	// SMTPFrom is the From address for notice mail.
	SMTPFrom string `json:"smtp_from"`
}

//go:embed fixed.json
var fixedJSON []byte

// Defaults is the configuration with nothing overridden.
func Defaults() Config {
	var c Config
	if err := json.Unmarshal(fixedJSON, &c); err != nil {
		panic(fmt.Sprintf("marketplace: fixed.json: %v", err))
	}
	return c
}

// LoadConfig reads path over the defaults. Unknown fields are refused so a typo is not
// silently ignored.
func LoadConfig(path string) (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("marketplace: %s: %w", path, err)
	}
	if c.DataDir == "" || c.Listen == "" || c.AuthorSessionHours <= int(0) || c.OperatorSessionMinutes <= int(0) {
		return c, errors.New("marketplace: listen, data_dir and positive session lengths are required")
	}
	return c, nil
}

// OperatorToken reads the operator's bearer token from its file. A short token is
// refused.
func (c Config) OperatorToken() (string, error) {
	b, err := os.ReadFile(c.OperatorTokenFile)
	if err != nil {
		return "", fmt.Errorf("marketplace: operator token: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if len(t) < c.MinOperatorTokenBytes {
		return "", fmt.Errorf("marketplace: the operator token must be at least %d bytes", c.MinOperatorTokenBytes)
	}
	return t, nil
}

func dur(unit string, n int) time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%d%s", n, unit))
	return d
}

// AuthorSession is how long an author's bearer token lasts.
func (c Config) AuthorSession() time.Duration { return dur("h", c.AuthorSessionHours) }

// OperatorSession is how long an operator's browser sign-in lasts.
func (c Config) OperatorSession() time.Duration { return dur("m", c.OperatorSessionMinutes) }

// ReadHeaderTimeout bounds a slow client.
func (c Config) ReadHeaderTimeout() time.Duration { return dur("s", c.ReadHeaderTimeoutSeconds) }

// NoticePeriod is the price-change notice window for existing subscriptions.
func (c Config) NoticePeriod() time.Duration {
	return dur("h", c.PriceChangeNoticeDays*c.HoursPerDay)
}

// StripeWebhookTolerance is the allowed Stripe-Signature clock skew.
func (c Config) StripeWebhookTolerance() time.Duration {
	return dur("s", c.StripeWebhookToleranceSeconds)
}

// SMTPDeadline bounds one SMTP conversation.
func (c Config) SMTPDeadline() time.Duration { return dur("s", c.SMTPDeadlineSeconds) }

// MailRetryInterval is how often undelivered price-change notices are retried.
func (c Config) MailRetryInterval() time.Duration {
	return dur("s", c.MailRetryIntervalSeconds)
}

// StripeSecret reads the Stripe secret key from its file. Empty path means unset.
func (c Config) StripeSecret() (string, error) {
	if strings.TrimSpace(c.StripeSecretFile) == "" {
		return "", nil
	}
	b, err := os.ReadFile(c.StripeSecretFile)
	if err != nil {
		return "", fmt.Errorf("marketplace: stripe secret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// StripeWebhookSecret reads the Stripe webhook signing secret from its file.
func (c Config) StripeWebhookSecret() (string, error) {
	if strings.TrimSpace(c.StripeWebhookSecretFile) == "" {
		return "", nil
	}
	b, err := os.ReadFile(c.StripeWebhookSecretFile)
	if err != nil {
		return "", fmt.Errorf("marketplace: stripe webhook secret: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

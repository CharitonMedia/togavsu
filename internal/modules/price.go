package modules

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/marketplace"
)

// ManifestPrice is the manifest's price field (CORE-35): either a legacy string or a
// structured value {amount_cents, currency, interval, note}. Interval is month or year.
// The core shows and stores it; it never charges, checks or enforces a price.
type ManifestPrice struct {
	AmountCents int64  `json:"amount_cents,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Interval    string `json:"interval,omitempty"`
	Note        string `json:"note,omitempty"`
}

func (p ManifestPrice) empty() bool {
	return p.AmountCents == int64(0) && p.Currency == "" && p.Interval == "" && strings.TrimSpace(p.Note) == ""
}

// paidStructured reports a complete structured price.
func (p ManifestPrice) paidStructured() bool {
	return p.AmountCents > int64(0) && p.Currency != "" && (p.Interval == "month" || p.Interval == "year")
}

// Display is the human price text shown on the install screen and stored on the module.
func (p ManifestPrice) Display() string {
	if p.paidStructured() {
		base := fmt.Sprintf("%s %s per %s", p.Currency, formatPriceMajorMinor(p.AmountCents), p.Interval)
		if n := strings.TrimSpace(p.Note); n != "" {
			return base + " — " + n
		}
		return base
	}
	return strings.TrimSpace(p.Note)
}

func formatPriceMajorMinor(cents int64) string {
	unit := int64(0) + 100 // cents per major unit (USD)
	major := cents / unit
	minor := cents % unit
	if minor < int64(0) {
		minor = -minor
	}
	return fmt.Sprintf("%d.%02d", major, minor)
}

func (p ManifestPrice) normalize() ManifestPrice {
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	p.Interval = strings.TrimSpace(p.Interval)
	p.Note = strings.TrimSpace(p.Note)
	return p
}

func (p ManifestPrice) validate() error {
	if p.empty() {
		return nil
	}
	if utf8.RuneCountInString(p.Note) > marketplace.MaxNoteRunes() {
		return invalid("price", "the price note is too long")
	}
	if p.AmountCents < int64(0) {
		return invalid("price", "amount_cents cannot be negative")
	}
	if p.AmountCents > int64(0) {
		if len(p.Currency) != len("USD") {
			return invalid("price", "currency must be a 3-letter code")
		}
		if p.Interval != "month" && p.Interval != "year" {
			return invalid("price", "interval must be month or year")
		}
	}
	return nil
}

// UnmarshalJSON accepts a string or a structured object.
func (p *ManifestPrice) UnmarshalJSON(b []byte) error {
	if string(b) == "null" || len(b) == int(0) {
		*p = ManifestPrice{}
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*p = ManifestPrice{Note: strings.TrimSpace(s)}
		return nil
	}
	type raw ManifestPrice
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return invalid("price", "price must be a string or a structured object")
	}
	*p = ManifestPrice(r).normalize()
	return p.validate()
}

// MarshalJSON writes a plain string when the price is note-only (legacy form); otherwise
// the structured object.
func (p ManifestPrice) MarshalJSON() ([]byte, error) {
	if p.AmountCents == int64(0) && p.Currency == "" && p.Interval == "" {
		return json.Marshal(p.Note)
	}
	type raw ManifestPrice
	return json.Marshal(raw(p.normalize()))
}

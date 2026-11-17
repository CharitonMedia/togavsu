package marketplace

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

// ListingPrice is the marketplace listing price (CORE-CHANGES-2 §17.1 / CORE-35).
// A structured price has amount_cents, currency and interval (month or year). The old
// string form is stored as Note alone with AmountCents zero and Interval empty.
type ListingPrice struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency,omitempty"`
	Interval    string `json:"interval,omitempty"`
	Note        string `json:"note,omitempty"`
}

// PendingPriceChange is an author price change waiting out the notice period for
// existing subscriptions.
type PendingPriceChange struct {
	Price       ListingPrice `json:"price"`
	NoticedAt   string       `json:"noticed_at"`
	EffectiveAt string       `json:"effective_at"`
}

func (p ListingPrice) empty() bool {
	return p.AmountCents == 0 && p.Currency == "" && p.Interval == "" && strings.TrimSpace(p.Note) == ""
}

// Display is the human price text shown on listing pages and in price_note.
func (p ListingPrice) Display() string {
	if p.AmountCents > 0 && p.Currency != "" && (p.Interval == "month" || p.Interval == "year") {
		major := p.AmountCents / 100
		minor := p.AmountCents % 100
		base := fmt.Sprintf("%s %d.%02d per %s", strings.ToUpper(p.Currency), major, minor, p.Interval)
		if n := strings.TrimSpace(p.Note); n != "" {
			return base + " — " + n
		}
		return base
	}
	return strings.TrimSpace(p.Note)
}

func (p ListingPrice) validate() error {
	if p.empty() {
		return nil
	}
	if utf8.RuneCountInString(p.Note) > shared.MaxNoteRunes() {
		return invalid("price", "the price note is too long")
	}
	if p.AmountCents < 0 {
		return invalid("price", "amount_cents cannot be negative")
	}
	if p.AmountCents > 0 {
		cur := strings.ToUpper(strings.TrimSpace(p.Currency))
		if len(cur) != 3 {
			return invalid("price", "currency must be a three-letter code")
		}
		if p.Interval != "month" && p.Interval != "year" {
			return invalid("price", "interval must be month or year")
		}
	}
	return nil
}

// parseManifestPrice reads CORE-35's price field from a parsed manifest map: either a
// string or {amount_cents, currency, interval, note}.
func parseManifestPrice(manifest map[string]any) (ListingPrice, error) {
	raw, ok := manifest["price"]
	if !ok || raw == nil {
		return ListingPrice{}, nil
	}
	switch v := raw.(type) {
	case string:
		return ListingPrice{Note: strings.TrimSpace(v)}, nil
	case map[string]any:
		p := ListingPrice{
			Currency: strings.ToUpper(strings.TrimSpace(asString(v["currency"]))),
			Interval: strings.TrimSpace(asString(v["interval"])),
			Note:     strings.TrimSpace(asString(v["note"])),
		}
		switch n := v["amount_cents"].(type) {
		case float64:
			p.AmountCents = int64(n)
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				return ListingPrice{}, invalid("price", "amount_cents must be an integer")
			}
			p.AmountCents = i
		case int64:
			p.AmountCents = n
		case int:
			p.AmountCents = int64(n)
		case nil:
			// note-only structured object
		default:
			return ListingPrice{}, invalid("price", "amount_cents must be an integer")
		}
		if err := p.validate(); err != nil {
			return ListingPrice{}, err
		}
		return p, nil
	default:
		return ListingPrice{}, invalid("price", "price must be a string or a structured object")
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

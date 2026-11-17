package marketplace

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

// ListingPrice is the marketplace listing price (CORE-CHANGES-2 §17.1 / CORE-35).
// A structured price has amount_cents, currency and interval (month or year). The old
// string form is stored as Note alone with AmountCents zero and Interval empty.
// The marketplace shows this as the provider's information; it never charges it.
type ListingPrice struct {
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency,omitempty"`
	Interval    string `json:"interval,omitempty"`
	Note        string `json:"note,omitempty"`
}

func (p ListingPrice) empty() bool {
	return p.AmountCents == int64(0) && p.Currency == "" && p.Interval == "" && strings.TrimSpace(p.Note) == ""
}

// paidStructured reports a complete structured price (amount, currency, interval).
func (p ListingPrice) paidStructured() bool {
	return p.AmountCents > int64(0) && p.Currency != "" && (p.Interval == "month" || p.Interval == "year")
}

// normalize trims and uppercases currency/interval the same way parseManifestPrice does.
func (p ListingPrice) normalize() ListingPrice {
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	p.Interval = strings.TrimSpace(p.Interval)
	p.Note = strings.TrimSpace(p.Note)
	return p
}

// Display is the human price text shown on listing pages (before the provider disclaimer).
func (p ListingPrice) Display(cfg Config) string {
	return p.display(cfg)
}

func (p ListingPrice) display(cfg Config) string {
	if p.paidStructured() {
		base := fmt.Sprintf("%s %s per %s", strings.ToUpper(p.Currency), formatMajorMinor(p.AmountCents, cfg.CentsPerMajorUnit), p.Interval)
		if n := strings.TrimSpace(p.Note); n != "" {
			return base + " — " + n
		}
		return base
	}
	return strings.TrimSpace(p.Note)
}

// formatMajorMinor renders amount_cents using cents_per_major_unit for the minor width
// (100 → two digits, 1 → no fraction, 10 → one digit).
func formatMajorMinor(amountCents, unit int64) string {
	if unit < int64(1) {
		unit = 1
	}
	major := amountCents / unit
	minor := amountCents % unit
	if unit == int64(1) {
		return strconv.FormatInt(major, 10)
	}
	width := len(strconv.FormatInt(unit-1, 10))
	return fmt.Sprintf("%d.%0*d", major, width, minor)
}

func (p ListingPrice) validateWith(cfg Config) error {
	if p.empty() {
		return nil
	}
	if utf8.RuneCountInString(p.Note) > shared.MaxNoteRunes() {
		return invalid("price", "the price note is too long")
	}
	if p.AmountCents < int64(0) {
		return invalid("price", "amount_cents cannot be negative")
	}
	if p.AmountCents > int64(0) {
		cur := strings.ToUpper(strings.TrimSpace(p.Currency))
		if len(cur) != cfg.CurrencyCodeLength {
			return invalid("price", fmt.Sprintf("currency must be a %d-letter code", cfg.CurrencyCodeLength))
		}
		if p.Interval != "month" && p.Interval != "year" {
			return invalid("price", "interval must be month or year")
		}
	}
	return nil
}

// WirePriceNote is the listing price_note on the published JSON contract: the rendered
// price followed by the provider disclaimer. Structured price and sales_url are not
// wire fields (api/ owns those additions later).
func WirePriceNote(display, author string) string {
	disc := ProviderDisclaimer(author)
	display = strings.TrimSpace(display)
	if display == "" {
		return disc
	}
	if strings.HasSuffix(display, ".") {
		return display + " " + disc
	}
	return display + ". " + disc
}

// parseManifestPriceWith reads CORE-35's price field from a parsed manifest map: either a
// string or {amount_cents, currency, interval, note}.
func parseManifestPriceWith(manifest map[string]any, cfg Config) (ListingPrice, error) {
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
			if n != float64(int64(n)) {
				return ListingPrice{}, invalid("price", "amount_cents must be an integer")
			}
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
		if err := p.validateWith(cfg); err != nil {
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

// ProviderDisclaimer is shown beside every listing price: selling and licensing belong
// to the author, never to Togavsu.
func ProviderDisclaimer(authorName string) string {
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		authorName = "the author"
	}
	return "Sold and licensed by " + authorName + ". Togavsu doesn't sell or bill this module."
}

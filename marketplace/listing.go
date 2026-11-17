package marketplace

import (
	"net/url"
	"strings"
	"unicode/utf8"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

// ListingEdit is an author change to the listed price and/or sales page link.
// It applies at once and never touches package digests or versions (no re-upload).
type ListingEdit struct {
	Price    *ListingPrice `json:"price"`
	SalesURL *string       `json:"sales_url"`
}

// ListingEditResult is the listing after a price/sales-link edit.
type ListingEditResult struct {
	Listing shared.Listing
}

// SetListingPrice updates the listing's displayed price and optional sales URL without
// a new package. Changes show at once. Package digests and versions are untouched so
// an install from the marketplace is unchanged.
func (m *Market) SetListingPrice(a Author, listingID string, edit ListingEdit) (ListingEditResult, error) {
	if edit.Price == nil && edit.SalesURL == nil {
		return ListingEditResult{}, invalid("body", "price or sales_url is required")
	}
	if edit.Price != nil {
		if err := edit.Price.validate(); err != nil {
			return ListingEditResult{}, err
		}
		if edit.Price.empty() {
			return ListingEditResult{}, invalid("price", "a price is required")
		}
	}
	var sales string
	if edit.SalesURL != nil {
		var err error
		sales, err = normalizeSalesURL(*edit.SalesURL)
		if err != nil {
			return ListingEditResult{}, err
		}
	}
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
		if edit.Price != nil {
			// When replacing a structured paid price, require another complete price.
			if l.Price.paidStructured() && !edit.Price.paidStructured() {
				return invalid("price", "a paid listing requires amount_cents, currency and interval")
			}
			l.Price = *edit.Price
			l.PriceNote = edit.Price.Display()
		}
		if edit.SalesURL != nil {
			l.SalesURL = sales
		}
		out = *l
		return nil
	})
	if err != nil {
		return ListingEditResult{}, err
	}
	return ListingEditResult{Listing: m.view(out)}, nil
}

func normalizeSalesURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	cfg := Defaults()
	if utf8.RuneCountInString(raw) > cfg.MaxSalesURLRunes {
		return "", invalid("sales_url", "the sales page link is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", invalid("sales_url", "sales_url must be an absolute http(s) URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", invalid("sales_url", "sales_url must be an http or https URL")
	}
	return u.String(), nil
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

// ListingSalesURL returns the author's sales page link for a listing, if any.
func (m *Market) ListingSalesURL(id string) (string, error) {
	var u string
	found := false
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.ID == id {
				u, found = l.SalesURL, true
			}
		}
	})
	if !found {
		return "", ErrNotFound
	}
	return u, nil
}

// ApplyConfig applies service config. Kept so the binary can call it after Open; there
// is no Stripe, license seed or mailer to attach.
func (m *Market) ApplyConfig(cfg Config) error {
	_ = cfg
	return nil
}

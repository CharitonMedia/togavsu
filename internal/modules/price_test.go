package modules_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestManifestPriceStringAndStructured(t *testing.T) {
	m, err := modules.ParseManifest([]byte(`
id: price-string
name: Price String
version: "1.0.0"
author: Test
license: MIT
price: Free
container:
  image: example/mod
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  port: 8080
permissions:
  - scope: matters:read
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Price.Display() != "Free" || m.Price.AmountCents != 0 {
		t.Fatalf("string price: %+v", m.Price)
	}

	m2, err := modules.ParseManifest([]byte(`
id: price-structured
name: Price Structured
version: "1.0.0"
author: Test
license: MIT
price:
  amount_cents: 4900
  currency: usd
  interval: month
  note: Missouri estate planning
container:
  image: example/mod
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  port: 8080
permissions:
  - scope: matters:read
`))
	if err != nil {
		t.Fatal(err)
	}
	if m2.Price.AmountCents != 4900 || m2.Price.Currency != "USD" || m2.Price.Interval != "month" {
		t.Fatalf("structured: %+v", m2.Price)
	}
	if !strings.Contains(m2.Price.Display(), "USD 49.00 per month") {
		t.Fatalf("display: %q", m2.Price.Display())
	}

	_, err = modules.ParseManifest([]byte(`
id: price-bad
name: Price Bad
version: "1.0.0"
author: Test
license: MIT
price:
  amount_cents: 100
  currency: USD
  interval: week
container:
  image: example/mod
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  port: 8080
permissions:
  - scope: matters:read
`))
	if err == nil || !strings.Contains(err.Error(), "interval") {
		t.Fatalf("bad interval: %v", err)
	}
}

func TestManifestPriceRoundTripJSON(t *testing.T) {
	var p modules.ManifestPrice
	if err := json.Unmarshal([]byte(`"Free"`), &p); err != nil || p.Display() != "Free" {
		t.Fatalf("string: %+v %v", p, err)
	}
	raw, err := json.Marshal(p)
	if err != nil || string(raw) != `"Free"` {
		t.Fatalf("marshal string form: %s %v", raw, err)
	}
	if err := json.Unmarshal([]byte(`{"amount_cents":9900,"currency":"USD","interval":"year"}`), &p); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(p)
	if err != nil || !strings.Contains(string(raw), `"amount_cents":9900`) {
		t.Fatalf("marshal structured: %s %v", raw, err)
	}
}

func TestAcceptsIncludesPriceFields(t *testing.T) {
	a := modules.Accepts()
	want := map[string]bool{"price": false, "price.amount_cents": false, "price.currency": false, "price.interval": false, "price.note": false}
	for _, f := range a.ManifestFields {
		if _, ok := want[f]; ok {
			want[f] = true
		}
	}
	for f, ok := range want {
		if !ok {
			t.Fatalf("Accepts missing %s; fields=%v", f, a.ManifestFields)
		}
	}
	found := false
	for _, item := range a.CoreItems {
		if item == "CORE-35" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CORE-35 missing from core_items: %v", a.CoreItems)
	}
}

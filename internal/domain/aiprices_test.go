package domain

import "testing"

func TestModelPricesCost(t *testing.T) {
	p, err := ParseModelPrices(`{"fast": {"input": 300, "output": 1500}, "free": {"input": 0, "output": 0}}`)
	if err != nil {
		t.Fatal(err)
	}
	// 200,000 in at 300 = 60 cents; 40,000 out at 1500 = 60 cents.
	if got := p.CostCents("fast", 200000, 40000); got != 120 {
		t.Fatalf("fast cost %d, want 120", got)
	}
	// 1,000 in at 300 = 0.3 cents rounds to 0; 2,000 in = 0.6 rounds to 1.
	if got := p.CostCents("fast", 1000, 0); got != 0 {
		t.Fatalf("rounding down: %d", got)
	}
	if got := p.CostCents("fast", 2000, 0); got != 1 {
		t.Fatalf("rounding up: %d", got)
	}
	if got := p.CostCents("unpriced", 1000000, 1000000); got != 0 {
		t.Fatalf("an unpriced model cost %d", got)
	}
	if got := p.CostCents("free", 1000000, 1000000); got != 0 {
		t.Fatalf("a free model cost %d", got)
	}
}

func TestModelPricesRefuseBadValues(t *testing.T) {
	for _, raw := range []string{
		`[]`, `{"m": 3}`, `{"m": {"input": 1}}`, `{"m": {"input": -1, "output": 1}}`,
		`{"m": {"input": 1, "output": 1, "extra": 2}}`, `{"": {"input": 1, "output": 1}}`, `{"m": null}`,
	} {
		if _, err := ParseModelPrices(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if p, err := ParseModelPrices(`{}`); err != nil || len(p) != 0 {
		t.Fatalf("empty prices: %v %v", p, err)
	}
}

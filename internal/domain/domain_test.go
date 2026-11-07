package domain

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestUUIDv7(t *testing.T) {
	a, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected distinct ids")
	}
	parsed, err := ParseID(strings.ToUpper(string(a)))
	if err != nil || parsed != a {
		t.Fatalf("parse %s: %v %s", a, err, parsed)
	}
	got, err := a.Time()
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC {
		t.Fatal(got.Location())
	}
	if delta := time.Since(got); delta < 0 || delta > time.Minute {
		t.Fatal(delta)
	}
	if _, err := ParseID("550e8400-e29b-41d4-a716-446655440000"); err == nil {
		t.Fatal("v4 accepted")
	}
	if _, err := ParseID("not-a-uuid"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := ComposeV7(1<<48, 0, 1); err == nil {
		t.Fatal("overflow ms accepted")
	}
	early, err := ComposeV7(1_000, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	later, err := ComposeV7(1_001, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(early) >= string(later) {
		t.Fatalf("order %s >= %s", early, later)
	}
	if _, err := ParseID(string(SystemUserID)); err != nil {
		t.Fatal(err)
	}
	if SystemUserID.IsZero() {
		t.Fatal("system id")
	}
}

func TestCents(t *testing.T) {
	cases := []struct {
		in   string
		want Cents
		ok   bool
	}{
		{"12.34", 1234, true},
		{"-0.01", -1, true},
		{"2", 200, true},
		{"2.5", 250, true},
		{"0.00", 0, true},
		{"-0.00", 0, true},
		{"1.234", 0, false},
		{"", 0, false},
		{"$1.00", 0, false},
		{"1,000.00", 0, false},
		{"10.", 0, false},
		{"abc", 0, false},
		{"\u0661", 0, false},
		{"\u0661.00", 0, false},
		{"1\u0662.00", 0, false},
		{"\uFF11.00", 0, false},
	}
	for _, tc := range cases {
		got, err := ParseCents(tc.in)
		if tc.ok {
			if err != nil || got != tc.want {
				t.Fatalf("%q got %v err %v", tc.in, got, err)
			}
		} else if err == nil {
			t.Fatalf("%q accepted as %v", tc.in, got)
		}
	}
	if Cents(1234).String() != "12.34" || Cents(-1).String() != "-0.01" || Cents(0).String() != "0.00" || Cents(10).String() != "0.10" {
		t.Fatal("format")
	}
	sum, err := Cents(100).Add(50)
	if err != nil || sum != 150 {
		t.Fatal(sum, err)
	}
	if _, err := Cents(math.MaxInt64).Add(1); err != ErrOverflow {
		t.Fatal(err)
	}
	diff, err := Cents(100).Sub(250)
	if err != nil || diff != -150 {
		t.Fatal(diff, err)
	}
	max := Cents(math.MaxInt64)
	min := Cents(math.MinInt64)
	if max.String() != "92233720368547758.07" || min.String() != "-92233720368547758.08" {
		t.Fatal(max.String(), min.String())
	}
	parsed, err := ParseCents(max.String())
	if err != nil || parsed != max {
		t.Fatal(parsed, err)
	}
	parsed, err = ParseCents(min.String())
	if err != nil || parsed != min {
		t.Fatal(parsed, err)
	}
	if _, err := ParseCents("92233720368547758.08"); err != ErrOverflow {
		t.Fatal(err)
	}
	if _, err := Cents(0).Sub(min); err != ErrOverflow {
		t.Fatal(err)
	}
	if _, err := Cents(1).Sub(min); err != ErrOverflow {
		t.Fatal(err)
	}
	if _, err := max.Sub(min); err != ErrOverflow {
		t.Fatal(err)
	}
	got, err := min.Sub(min)
	if err != nil || got != 0 {
		t.Fatal(got, err)
	}
	got, err = Cents(-1).Sub(min)
	if err != nil || got != max {
		t.Fatalf("(-1) - MinInt64 = %v err %v", got, err)
	}
	got, err = Cents(-2).Sub(min)
	if err != nil || got != max-1 {
		t.Fatalf("(-2) - MinInt64 = %v err %v", got, err)
	}
	got, err = Cents(math.MinInt64 + 1).Sub(min)
	if err != nil || got != 1 {
		t.Fatalf("(MinInt64+1) - MinInt64 = %v err %v", got, err)
	}
	if _, err := min.Add(-1); err != ErrOverflow {
		t.Fatal(err)
	}
}

func TestTimeAndPhone(t *testing.T) {
	now := Now()
	if now.Location() != time.UTC {
		t.Fatal(now.Location())
	}
	text := FormatUTC(now)
	back, err := ParseUTC(text)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("%s -> %s", now, back)
	}
	zoned, err := ParseUTC("2026-11-06T12:00:00-05:00")
	if err != nil || zoned.Hour() != 17 || zoned.Location() != time.UTC {
		t.Fatal(zoned, err)
	}
	if _, err := ParseUTC(""); err == nil {
		t.Fatal("empty accepted")
	}
	loc, err := InZone(zoned, "")
	if err != nil || loc.Location() != time.UTC {
		t.Fatal(loc, err)
	}
	if _, err := InZone(zoned, "Not/AZone"); err == nil {
		t.Fatal("bad zone accepted")
	}
	if _, err := ParseE164("+15555550100"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"5555550100", "+0123", "++1555", ""} {
		if _, err := ParseE164(bad); err == nil {
			t.Fatal(bad)
		}
	}
}

func TestNamingAndCategories(t *testing.T) {
	if err := ValidateNamingPattern(NamingPattern); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "file.pdf", "<status>.<ext>", "<file-status>.<ext>", "<what-it-is>.pdf", "<nope>.<ext>"} {
		if err := ValidateNamingPattern(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	ok, err := MatchCasePattern(DefaultCasePattern, "23AR-CV00825")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = MatchCasePattern(DefaultCasePattern, "hello")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	cats := DefaultDocCategories()
	if len(cats) != 17 {
		t.Fatal(len(cats))
	}
	high := map[string]bool{}
	for _, name := range HighTierCategories() {
		high[name] = true
	}
	if len(high) != 7 {
		t.Fatal(len(high))
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if seen[c.Name] {
			t.Fatal("dup", c.Name)
		}
		seen[c.Name] = true
		wantHigh := high[c.Name]
		if wantHigh && c.Tier != "high" || !wantHigh && c.Tier != "standard" {
			t.Fatalf("%s tier %s", c.Name, c.Tier)
		}
		if c.Folder == "" || c.Description == "" || !c.Active {
			t.Fatal(c)
		}
	}
}

func TestRolesAndAPIErrors(t *testing.T) {
	raw, err := MarshalRoles([]Role{RoleAdmin, RoleAttorney})
	if err != nil || raw != `["admin","attorney"]` {
		t.Fatal(raw, err)
	}
	back, err := ParseRoles(raw)
	if err != nil || len(back) != 2 {
		t.Fatal(back, err)
	}
	if _, err := ParseRoles(`["admin","admin"]`); err == nil {
		t.Fatal("duplicate")
	}
	if _, err := ParseRoles(`["judge"]`); err == nil {
		t.Fatal("unknown")
	}
	if _, err := ParseRoles(`[]`); err == nil {
		t.Fatal("empty")
	}
	e := Invalid("number", "bad")
	if e.HTTPStatus() != 400 || e.Field != "number" {
		t.Fatal(e)
	}
	if Unauthenticated("no").HTTPStatus() != 401 || Forbidden("no").HTTPStatus() != 403 {
		t.Fatal("status")
	}
	if NotFound("matter").HTTPStatus() != 404 || Conflict("x").HTTPStatus() != 409 {
		t.Fatal("status")
	}
	if AppendOnly("trust_ledger").HTTPStatus() != 409 || RateLimited().HTTPStatus() != 429 {
		t.Fatal("status")
	}
	if !strings.Contains(AppendOnly("trust_ledger").Error(), "trust_ledger") {
		t.Fatal(AppendOnly("trust_ledger"))
	}
}

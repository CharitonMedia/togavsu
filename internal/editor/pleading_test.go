package editor

import "testing"

func TestFirmAddressAndPhoneForNewPleading(t *testing.T) {
	cases := []struct{ in, a1, a2, city, state, zip string }{
		{"1 Main St, Kirksville, MO 63501", "1 Main St", "", "Kirksville", "MO", "63501"},
		{"1 Main St, Suite 2\nKirksville, mo. 63501-1234", "1 Main St", "Suite 2", "Kirksville", "MO", "63501-1234"},
	}
	for _, c := range cases {
		a1, a2, city, state, zip, ok := firmAddress(c.in)
		if !ok || a1 != c.a1 || a2 != c.a2 || city != c.city || state != c.state || zip != c.zip {
			t.Errorf("firmAddress(%q) = %q %q %q %q %q %v", c.in, a1, a2, city, state, zip, ok)
		}
	}
	for _, bad := range []string{"", "Somewhere", "1 Main St, Kirksville"} {
		if _, _, _, _, _, ok := firmAddress(bad); ok {
			t.Errorf("firmAddress(%q) parsed", bad)
		}
	}
	if got := usPhone("+16605550100"); got != "(660) 555-0100" {
		t.Errorf("usPhone = %q", got)
	}
	if got := usPhone("+442071234567"); got != "+442071234567" {
		t.Errorf("a non-US number changed: %q", got)
	}
}

func TestCaptionSidesAndCourt(t *testing.T) {
	for pos, want := range map[string][3]any{
		"": {"Petitioner", "Respondent", true}, "Petitioner": {"Petitioner", "Respondent", true},
		"respondent": {"Respondent", "Petitioner", false}, "Defendant": {"Defendant", "Plaintiff", false},
		"plaintiff": {"Plaintiff", "Defendant", true},
	} {
		a, b, first := sides(pos)
		if a != want[0] || b != want[1] || first != want[2] {
			t.Errorf("sides(%q) = %s %s %v", pos, a, b, first)
		}
	}
	for court, want := range map[string]string{"Circuit Court": "CIRCUIT", "The Associate Circuit Court of Adair County": "ASSOCIATE CIRCUIT", "": "CIRCUIT", "Court": "CIRCUIT"} {
		if got := courtType(court); got != want {
			t.Errorf("courtType(%q) = %q", court, got)
		}
	}
	if got := countyName("Adair County"); got != "ADAIR" {
		t.Errorf("countyName = %q", got)
	}
	if got := sanitizeName("Motion: to / Continue\x00"); got != "Motion- to - Continue-" {
		t.Errorf("sanitizeName = %q", got)
	}
}

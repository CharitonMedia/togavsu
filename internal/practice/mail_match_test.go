package practice

import "testing"

func TestCaseNumberInName(t *testing.T) {
	cases := []struct {
		name, caseNum string
		want          bool
	}{
		{"Matter 21-CV-100", "21-CV-100", true},
		{"Matter 21-CV-100-A", "21-CV-100", false}, // following hyphen excluded
		{"x21-CV-100", "21-CV-100", false},         // letter before is not a boundary
		{"(21-CV-100)", "21-CV-100", true},
		{"", "21-CV-100", false},
		{"Matter", "", false},
		{"21-CV-100", "21-CV-100", true},
	}
	for _, c := range cases {
		if got := caseNumberInName(c.name, c.caseNum); got != c.want {
			t.Errorf("caseNumberInName(%q, %q)=%v want %v", c.name, c.caseNum, got, c.want)
		}
	}
}

func TestTrimMailListCap(t *testing.T) {
	in := make([]string, mailMatchCap+1)
	for i := range in {
		in[i] = "x"
	}
	if _, err := trimMailList(in); err == nil {
		t.Fatal("expected cap error")
	}
}

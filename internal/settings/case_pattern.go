package settings

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	keyCaseExamples = "practice.case_number_examples"
	keyCasePatterns = "practice.case_number_patterns"
)

// DeriveCasePattern turns one real case number into the stored regexp form.
// Digit runs become \d{n}, except the last digit run which becomes \d+; letter
// runs become [A-Z]{n}; every other character is matched literally; the result
// is anchored with ^ and $.
func DeriveCasePattern(example string) (string, error) {
	ex := strings.TrimSpace(example)
	if ex == "" {
		return "", fmt.Errorf("%w: empty example", ErrInvalid)
	}
	// Forty characters is a format bound for one example, not a firm setting.
	if len(ex) > 40 {
		return "", fmt.Errorf("%w: example %q is longer than 40 characters", ErrInvalid, ex)
	}
	hasDigit := false
	for _, r := range ex {
		if unicode.IsDigit(r) {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return "", fmt.Errorf("%w: example %q has no digit", ErrInvalid, ex)
	}

	type run struct {
		kind string // "digit", "letter", "other"
		text string
	}
	var runs []run
	for _, r := range ex {
		switch {
		case unicode.IsDigit(r):
			if len(runs) > 0 && runs[len(runs)-1].kind == "digit" {
				runs[len(runs)-1].text += string(r)
			} else {
				runs = append(runs, run{kind: "digit", text: string(r)})
			}
		case unicode.IsLetter(r):
			ch := string(unicode.ToUpper(r))
			if len(runs) > 0 && runs[len(runs)-1].kind == "letter" {
				runs[len(runs)-1].text += ch
			} else {
				runs = append(runs, run{kind: "letter", text: ch})
			}
		default:
			runs = append(runs, run{kind: "other", text: string(r)})
		}
	}

	lastDigit := -1
	for i, r := range runs {
		if r.kind == "digit" {
			lastDigit = i
		}
	}

	var b strings.Builder
	b.WriteByte('^')
	for i, r := range runs {
		switch r.kind {
		case "digit":
			if i == lastDigit {
				b.WriteString(`\d+`)
			} else {
				fmt.Fprintf(&b, `\d{%d}`, len(r.text))
			}
		case "letter":
			fmt.Fprintf(&b, `[A-Z]{%d}`, len(r.text))
		default:
			b.WriteString(regexp.QuoteMeta(r.text))
		}
	}
	b.WriteByte('$')
	return b.String(), nil
}

// DeriveCasePatterns derives one pattern per example. examplesJSON is the stored
// JSON list of example strings.
func DeriveCasePatterns(examplesJSON string) ([]string, error) {
	examples, err := parseStringList(examplesJSON)
	if err != nil || len(examples) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalid, keyCaseExamples)
	}
	out := make([]string, 0, len(examples))
	for _, ex := range examples {
		p, err := DeriveCasePattern(ex)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// PatternsMatchExamples reports whether the stored patterns equal those derived
// from the stored examples.
func PatternsMatchExamples(examplesJSON, patternsJSON string) bool {
	derived, err := DeriveCasePatterns(examplesJSON)
	if err != nil {
		return false
	}
	var patterns []string
	if err := json.Unmarshal([]byte(patternsJSON), &patterns); err != nil {
		return false
	}
	if len(derived) != len(patterns) {
		return false
	}
	for i := range derived {
		if derived[i] != patterns[i] {
			return false
		}
	}
	return true
}

func parseStringList(raw string) ([]string, error) {
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	return list, nil
}

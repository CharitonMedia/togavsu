package forms

import (
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// MaxRepeatingIndex is the highest accepted repeating-row index in a form POST
// (0..MaxRepeatingIndex inclusive). Larger indices are ignored so an anonymous
// public-page POST cannot force a multi-gigabyte allocation.
const MaxRepeatingIndex = domain.FormsMaxRepeatingIndex

// ParseFormAnswers builds typed Answers from a form POST
// (application/x-www-form-urlencoded or multipart) using the form definition.
// Both autosave and full submit use this single parser: numbers become float64,
// booleans bool, multi-selects []string, dates validated strings, addresses
// structs, repeating groups ordered row arrays. Keys ending in ".__unknown"
// mark "I don't know". Address parts use "id.line1" names as RenderScreen emits.
// Repeating rows use "id[N].sub". Non-field keys (csrf) are ignored.
//
// Presence: RenderScreen emits name="__present" value="<field name>" for every
// rendered control. A present field with no value is cleared (empty multi-select,
// all repeating rows removed). A field not listed in __present is omitted so
// MergeFormSubmit keeps other screens' answers. TypeFile is never read from the
// form post (upload endpoint only). Empty masked values are still parsed as ""
// so MergeFormSubmit can keep sealed ciphertext at any nesting depth.
// Repeating rows carry RowIDKey ("__row") for stable merge identity.
func ParseFormAnswers(form Form, values map[string][]string) Answers {
	return parsePostedAnswers(form, values)
}

// parsePostedAnswers is the one schema-driven POST reader (CORE-33).
func parsePostedAnswers(form Form, values map[string][]string) Answers {
	out := Answers{}
	if values == nil {
		return out
	}
	for _, field := range formFields(form) {
		parseFieldInto(out, field, values, "")
	}
	return out
}

func formFields(form Form) []Field {
	var out []Field
	for _, track := range form.Tracks {
		for _, screen := range track.Screens {
			out = append(out, screen.Fields...)
		}
	}
	return out
}

func parseFieldInto(out Answers, field Field, values map[string][]string, namePrefix string) {
	name := namePrefix + field.ID
	if u, ok := unknownPosted(values, name); ok {
		if u {
			out.Set(field.ID, Answer{Unknown: true})
			return
		}
		// Explicit clear: continue so a posted value can fill Answer.Unknown=false.
	}
	switch field.Type {
	case TypeReadOnly:
		return
	case TypeFile:
		// File answers are set only by the upload endpoint; ignore form posts.
		return
	case TypeRepeating:
		if !repeatingPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: []map[string]Answer{}})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: parseRepeating(field, values, name)})
	case TypeAddress:
		if !addressPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: Address{}})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: parseAddress(values, name)})
	case TypeBoolean:
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: false})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: parseBool(lastValue(values[name]))})
	case TypeMultiSelect:
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: []string{}})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: append([]string{}, values[name]...)})
	case TypeNumber:
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: nil})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, parseNumberAnswer(lastValue(values[name])))
	case TypeDate:
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: ""})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, parseDateAnswer(lastValue(values[name])))
	case TypeMasked:
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				// Empty/omitted masked → Answer{Value:""}; MergeFormSubmit keeps sealed.
				out.Set(field.ID, Answer{Value: ""})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: lastValue(values[name])})
	default:
		// text, email, tel, textarea, select, radio, hidden
		if !keyPosted(values, name) {
			if fieldPresent(values, name) {
				out.Set(field.ID, Answer{Value: ""})
				return
			}
			applyUnknownClear(out, field.ID, values, name)
			return
		}
		out.Set(field.ID, Answer{Value: lastValue(values[name])})
	}
	if u, ok := unknownPosted(values, name); ok && !u {
		ans := out.Get(field.ID)
		ans.Unknown = false
		out.Set(field.ID, ans)
	}
}

// fieldPresent reports whether RenderScreen's __present marker listed this control.
func fieldPresent(values map[string][]string, name string) bool {
	for _, v := range values["__present"] {
		if v == name {
			return true
		}
	}
	return false
}

func applyUnknownClear(out Answers, id string, values map[string][]string, name string) {
	if u, ok := unknownPosted(values, name); ok && !u {
		ans := out.Get(id)
		ans.Unknown = false
		out.Set(id, ans)
	}
}

func keyPosted(values map[string][]string, name string) bool {
	_, ok := values[name]
	return ok
}

func unknownPosted(values map[string][]string, name string) (unknown bool, ok bool) {
	vals, ok := values[name+".__unknown"]
	if !ok {
		return false, false
	}
	// Hidden 0 + checkbox 1 posts both; the last value wins (unchecked → "0").
	last := ""
	if len(vals) > 0 {
		last = vals[len(vals)-1]
	}
	return last != "" && last != "0" && last != "false", true
}

func addressPosted(values map[string][]string, name string) bool {
	for _, part := range []string{"line1", "line2", "city", "state", "postal", "country"} {
		if _, ok := values[name+"."+part]; ok {
			return true
		}
	}
	return false
}

func repeatingPosted(values map[string][]string, name string) bool {
	prefix := name + "["
	for k := range values {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

func parseAddress(values map[string][]string, name string) Address {
	return Address{
		Line1:   lastValue(values[name+".line1"]),
		Line2:   lastValue(values[name+".line2"]),
		City:    lastValue(values[name+".city"]),
		State:   lastValue(values[name+".state"]),
		Postal:  lastValue(values[name+".postal"]),
		Country: lastValue(values[name+".country"]),
	}
}

func parseBool(s string) bool {
	return s == "true" || s == "1" || s == "on"
}

func parseNumberAnswer(s string) Answer {
	s = strings.TrimSpace(s)
	if s == "" {
		return Answer{Value: nil}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// Keep the raw string so Validate can report "not a number".
		return Answer{Value: s}
	}
	return Answer{Value: f}
}

func parseDateAnswer(s string) Answer {
	s = strings.TrimSpace(s)
	if s == "" {
		return Answer{Value: ""}
	}
	// Accept date (YYYY-MM-DD) or date-time (RFC3339 / HTML datetime-local); store date fields as YYYY-MM-DD.
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return Answer{Value: s}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return Answer{Value: t.Format("2006-01-02")}
		}
	}
	return Answer{Value: s}
}

func parseRepeating(field Field, values map[string][]string, name string) []map[string]Answer {
	type rowPart struct {
		idx  int
		rest string
		vals []string
	}
	var parts []rowPart
	for key, vals := range values {
		id, idx, rest, ok := splitRepeatingKey(key)
		if !ok || id != name {
			continue
		}
		parts = append(parts, rowPart{idx: idx, rest: rest, vals: vals})
	}
	max := -1
	for _, p := range parts {
		if p.idx > MaxRepeatingIndex {
			continue
		}
		if p.idx > max {
			max = p.idx
		}
	}
	if max < 0 {
		return nil
	}
	rows := make([]map[string]Answer, max+1)
	for i := range rows {
		rows[i] = map[string]Answer{}
	}
	rowForm := Form{Tracks: []Track{{Screens: []Screen{{Fields: field.Fields}}}}}
	rowPrefix := make([]string, max+1)
	for i := 0; i <= max; i++ {
		rowPrefix[i] = name + "[" + strconv.Itoa(i) + "]."
	}
	// Project __present markers into each row so nested clears (empty multi-select,
	// all inner repeating rows removed) are visible to parsePostedAnswers.
	presentByRow := make([][]string, max+1)
	for _, v := range values["__present"] {
		for i := 0; i <= max; i++ {
			if strings.HasPrefix(v, rowPrefix[i]) {
				presentByRow[i] = append(presentByRow[i], v[len(rowPrefix[i]):])
				break
			}
		}
	}
	for i := 0; i <= max; i++ {
		rowVals := map[string][]string{}
		for _, p := range parts {
			if p.idx != i || p.idx > MaxRepeatingIndex {
				continue
			}
			rowVals[p.rest] = append([]string{}, p.vals...)
		}
		if len(presentByRow[i]) > 0 {
			rowVals["__present"] = append([]string{}, presentByRow[i]...)
		}
		// Row identity is not a schema Field; lift it before typed parse.
		if rid := lastValue(rowVals[RowIDKey]); rid != "" {
			rows[i][RowIDKey] = Answer{Value: rid}
		}
		delete(rowVals, RowIDKey)
		parsed := parsePostedAnswers(rowForm, rowVals)
		for k, a := range parsed {
			rows[i][k] = a
		}
	}
	return rows
}

func lastValue(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}

// splitRepeatingKey parses "kids[0].name" into id, index, rest.
func splitRepeatingKey(key string) (id string, idx int, rest string, ok bool) {
	lb := strings.IndexByte(key, '[')
	if lb <= 0 {
		return "", 0, "", false
	}
	rb := strings.IndexByte(key[lb:], ']')
	if rb <= 1 {
		return "", 0, "", false
	}
	rb = lb + rb
	if rb+1 >= len(key) || key[rb+1] != '.' {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(key[lb+1 : rb])
	if err != nil || n < 0 {
		return "", 0, "", false
	}
	return key[:lb], n, key[rb+2:], true
}

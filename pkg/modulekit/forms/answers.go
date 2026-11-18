package forms

import "encoding/json"

// Answer is one field's stored value. Unknown marks an "I don't know" response
// (AllowUnknown on the field). Value shapes by type:
//
//	text/email/tel/textarea/date/select/radio/masked/file/hidden/read-only: string
//	number: float64 (JSON number)
//	boolean: bool
//	multi-select: []string
//	address: Address
//	repeating: []map[string]Answer (row maps keyed by nested field id)
type Answer struct {
	Unknown bool `json:"unknown,omitempty"`
	Value   any  `json:"value,omitempty"`
}

// Answers maps field id → answer. Modules persist this (or an equivalent) in their
// own schema; the kit never writes it.
type Answers map[string]Answer

// Address is the structured value for TypeAddress.
type Address struct {
	Line1   string `json:"line1,omitempty"`
	Line2   string `json:"line2,omitempty"`
	City    string `json:"city,omitempty"`
	State   string `json:"state,omitempty"`
	Postal  string `json:"postal,omitempty"`
	Country string `json:"country,omitempty"`
}

// Get returns the answer for id, or a zero Answer.
func (a Answers) Get(id string) Answer {
	if a == nil {
		return Answer{}
	}
	return a[id]
}

// Set stores ans under id, allocating the map when needed.
func (a *Answers) Set(id string, ans Answer) {
	if *a == nil {
		*a = Answers{}
	}
	(*a)[id] = ans
}

// Merge overlays patch onto a, replacing keys present in patch. Unknown answers
// clear Value. Nil receiver allocates.
func (a *Answers) Merge(patch Answers) {
	if *a == nil {
		*a = Answers{}
	}
	for id, ans := range patch {
		if ans.Unknown {
			ans.Value = nil
		}
		(*a)[id] = ans
	}
}

// ClearMaskedValue is the explicit form marker that clears a stored masked answer.
// An empty posted masked value keeps the existing ciphertext (blank password inputs).
const ClearMaskedValue = "__clear__"

// MergeFormSubmit merges posted form answers into existing. Masked fields keep their
// stored value when the posted string is empty or missing (RevealMasked=false renders
// value=""; OfflineQueueJS omits empty masked controls) at any nesting depth, including
// repeating rows. Post ClearMaskedValue to erase. Empty file posts keep the stored ref.
// Repeating groups merge row-by-row and field-by-field so a sibling edit cannot drop
// nested masked/file answers. Unknown still replaces. Other fields replace as Merge.
func MergeFormSubmit(existing Answers, posted Answers, form Form) Answers {
	out := Answers{}
	for id, ans := range existing {
		out[id] = ans
	}
	fields := formFields(form)
	byID := map[string]Field{}
	for _, f := range fields {
		byID[f.ID] = f
	}
	for id, ans := range posted {
		if ans.Unknown {
			ans.Value = nil
			out[id] = ans
			continue
		}
		if f, ok := byID[id]; ok {
			out[id] = mergePostedField(out[id], ans, f)
			continue
		}
		out[id] = ans
	}
	return out
}

func mergePostedField(existing, posted Answer, field Field) Answer {
	switch field.Type {
	case TypeMasked:
		s, _ := posted.Value.(string)
		if s == "" {
			cur := existing
			cur.Unknown = false
			return cur
		}
		if s == ClearMaskedValue {
			return Answer{Value: ""}
		}
		return posted
	case TypeFile:
		s, _ := posted.Value.(string)
		if s == "" {
			return existing
		}
		return posted
	case TypeRepeating:
		return Answer{Value: mergeRepeatingRows(existing.RowsValue(), posted.RowsValue(), field.Fields)}
	default:
		return posted
	}
}

func mergeRepeatingRows(existing, posted []map[string]Answer, nested []Field) []map[string]Answer {
	if posted == nil {
		return nil
	}
	out := make([]map[string]Answer, len(posted))
	for i, prow := range posted {
		var erow map[string]Answer
		if i < len(existing) {
			erow = existing[i]
		}
		row := map[string]Answer{}
		// Preserve nested masked/file values that the post omitted entirely.
		for _, f := range nested {
			if f.Type != TypeMasked && f.Type != TypeFile && f.Type != TypeRepeating {
				continue
			}
			if _, ok := prow[f.ID]; ok {
				continue
			}
			if erow == nil {
				continue
			}
			if cur, ok := erow[f.ID]; ok {
				row[f.ID] = cur
			}
		}
		for id, pans := range prow {
			var f Field
			for _, nf := range nested {
				if nf.ID == id {
					f = nf
					break
				}
			}
			var eans Answer
			if erow != nil {
				eans = erow[id]
			}
			if f.ID != "" {
				row[id] = mergePostedField(eans, pans, f)
			} else {
				row[id] = pans
			}
		}
		out[i] = row
	}
	return out
}

func maskedFieldIDs(form Form) map[string]bool {
	out := map[string]bool{}
	var walk func(fields []Field)
	walk = func(fields []Field) {
		for _, f := range fields {
			if f.Type == TypeMasked {
				out[f.ID] = true
			}
			if f.Type == TypeRepeating {
				walk(f.Fields)
			}
		}
	}
	for _, track := range form.Tracks {
		for _, screen := range track.Screens {
			walk(screen.Fields)
		}
	}
	return out
}

// StringValue returns a string answer, or "".
func (ans Answer) StringValue() string {
	if ans.Unknown || ans.Value == nil {
		return ""
	}
	s, _ := ans.Value.(string)
	return s
}

// BoolValue returns a boolean answer, or false.
func (ans Answer) BoolValue() bool {
	if ans.Unknown || ans.Value == nil {
		return false
	}
	b, _ := ans.Value.(bool)
	return b
}

// FloatValue returns a number answer. ok is false when missing or not numeric.
func (ans Answer) FloatValue() (float64, bool) {
	if ans.Unknown || ans.Value == nil {
		return 0, false
	}
	switch v := ans.Value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// StringsValue returns a multi-select answer.
func (ans Answer) StringsValue() []string {
	if ans.Unknown || ans.Value == nil {
		return nil
	}
	switch v := ans.Value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// AddressValue returns an address answer.
func (ans Answer) AddressValue() Address {
	if ans.Unknown || ans.Value == nil {
		return Address{}
	}
	switch v := ans.Value.(type) {
	case Address:
		return v
	case map[string]any:
		return Address{
			Line1:   asString(v["line1"]),
			Line2:   asString(v["line2"]),
			City:    asString(v["city"]),
			State:   asString(v["state"]),
			Postal:  asString(v["postal"]),
			Country: asString(v["country"]),
		}
	default:
		return Address{}
	}
}

// RowsValue returns repeating-group rows.
func (ans Answer) RowsValue() []map[string]Answer {
	if ans.Unknown || ans.Value == nil {
		return nil
	}
	switch v := ans.Value.(type) {
	case []map[string]Answer:
		return v
	case []any:
		out := make([]map[string]Answer, 0, len(v))
		for _, row := range v {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			parsed := map[string]Answer{}
			for k, raw := range m {
				parsed[k] = decodeAnswer(raw)
			}
			out = append(out, parsed)
		}
		return out
	default:
		return nil
	}
}

// Empty reports whether the answer has no usable value (and is not Unknown).
func (ans Answer) Empty() bool {
	if ans.Unknown {
		return false
	}
	if ans.Value == nil {
		return true
	}
	switch v := ans.Value.(type) {
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case []any:
		return len(v) == 0
	case []map[string]Answer:
		return len(v) == 0
	case Address:
		return v.Line1 == "" && v.City == "" && v.State == "" && v.Postal == "" && v.Country == "" && v.Line2 == ""
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func decodeAnswer(raw any) Answer {
	switch v := raw.(type) {
	case Answer:
		return v
	case map[string]any:
		ans := Answer{}
		if u, ok := v["unknown"].(bool); ok {
			ans.Unknown = u
		}
		if val, ok := v["value"]; ok {
			ans.Value = val
		}
		return ans
	default:
		return Answer{Value: raw}
	}
}

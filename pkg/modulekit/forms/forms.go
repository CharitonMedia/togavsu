// Package forms is the CORE-33 public forms kit: form definitions (tracks, screens,
// fields), the 16 field types, conditions, validation, an HTMX renderer for staff,
// client and public pages, autosave helpers with an offline queue, "I don't know"
// answers, and masked-field encryption through the calling module's secret store.
//
// Answers stay in the calling module's own schema. This package only provides types
// and helpers; it never opens a database.
package forms

// Audience selects which surfaces a field or render targets (CORE-33).
type Audience string

const (
	AudienceStaff  Audience = "staff"
	AudienceClient Audience = "client"
	AudiencePublic Audience = "public"
)

// FieldType is one of the 16 CORE-33 field types.
type FieldType string

const (
	TypeText        FieldType = "text"
	TypeEmail       FieldType = "email"
	TypeTel         FieldType = "tel"
	TypeTextarea    FieldType = "textarea"
	TypeNumber      FieldType = "number"
	TypeDate        FieldType = "date"
	TypeSelect      FieldType = "select"
	TypeMultiSelect FieldType = "multi-select"
	TypeBoolean     FieldType = "boolean"
	TypeRadio       FieldType = "radio"
	TypeAddress     FieldType = "address"
	TypeMasked      FieldType = "masked"
	TypeReadOnly    FieldType = "read-only"
	TypeHidden      FieldType = "hidden"
	TypeFile        FieldType = "file"
	TypeRepeating   FieldType = "repeating"
)

// AllFieldTypes lists every CORE-33 field type in declaration order.
var AllFieldTypes = []FieldType{
	TypeText, TypeEmail, TypeTel, TypeTextarea, TypeNumber, TypeDate,
	TypeSelect, TypeMultiSelect, TypeBoolean, TypeRadio, TypeAddress,
	TypeMasked, TypeReadOnly, TypeHidden, TypeFile, TypeRepeating,
}

// Form is a multi-track questionnaire definition.
type Form struct {
	ID     string  `json:"id"`
	Title  string  `json:"title,omitempty"`
	Tracks []Track `json:"tracks"`
}

// Track is one path through a form (for example "individual" vs "business").
type Track struct {
	ID      string     `json:"id"`
	Title   string     `json:"title,omitempty"`
	When    *Condition `json:"when,omitempty"`
	Screens []Screen   `json:"screens"`
}

// Screen is one page of fields inside a track.
type Screen struct {
	ID     string     `json:"id"`
	Title  string     `json:"title,omitempty"`
	When   *Condition `json:"when,omitempty"`
	Fields []Field    `json:"fields"`
}

// Option is one choice for select, multi-select or radio.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Field is one input on a screen. Type Repeating nests Fields as the row schema.
type Field struct {
	ID           string     `json:"id"`
	Type         FieldType  `json:"type"`
	Label        string     `json:"label,omitempty"`
	Help         string     `json:"help,omitempty"`
	Placeholder  string     `json:"placeholder,omitempty"`
	Required     bool       `json:"required,omitempty"`
	AllowUnknown bool       `json:"allow_unknown,omitempty"` // "I don't know"
	When         *Condition `json:"when,omitempty"`
	Options      []Option   `json:"options,omitempty"`
	// Audiences limits visibility; empty means every audience.
	Audiences []Audience `json:"audiences,omitempty"`
	// Min / Max bound numbers or repeating row counts.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// MinDate / MaxDate bound TypeDate values as YYYY-MM-DD.
	MinDate string `json:"min_date,omitempty"`
	MaxDate string `json:"max_date,omitempty"`
	// Pattern is a RE2 regular expression for text-like values.
	Pattern string `json:"pattern,omitempty"`
	// SecretKey is the module secret-store name holding the AES key for TypeMasked.
	SecretKey string `json:"secret_key,omitempty"`
	// Value is the fixed display for TypeReadOnly (and the default for TypeHidden).
	Value string `json:"value,omitempty"`
	// Fields is the row schema when Type is TypeRepeating.
	Fields []Field `json:"fields,omitempty"`
}

// TrackByID returns the named track, or false.
func (f Form) TrackByID(id string) (Track, bool) {
	for _, t := range f.Tracks {
		if t.ID == id {
			return t, true
		}
	}
	return Track{}, false
}

// ScreenByID returns the named screen inside a track, or false.
func (t Track) ScreenByID(id string) (Screen, bool) {
	for _, s := range t.Screens {
		if s.ID == id {
			return s, true
		}
	}
	return Screen{}, false
}

// FieldByID walks screens (and repeating schemas) for a field id.
func (f Form) FieldByID(id string) (Field, bool) {
	for _, t := range f.Tracks {
		for _, s := range t.Screens {
			if field, ok := findField(s.Fields, id); ok {
				return field, true
			}
		}
	}
	return Field{}, false
}

func findField(fields []Field, id string) (Field, bool) {
	for _, field := range fields {
		if field.ID == id {
			return field, true
		}
		if field.Type == TypeRepeating {
			if nested, ok := findField(field.Fields, id); ok {
				return nested, true
			}
		}
	}
	return Field{}, false
}

// VisibleTo reports whether the field is shown for audience.
func (f Field) VisibleTo(audience Audience) bool {
	if len(f.Audiences) == 0 {
		return true
	}
	for _, a := range f.Audiences {
		if a == audience {
			return true
		}
	}
	return false
}

// KnownType reports whether t is one of the 16 CORE-33 field types.
func KnownType(t FieldType) bool {
	for _, known := range AllFieldTypes {
		if t == known {
			return true
		}
	}
	return false
}

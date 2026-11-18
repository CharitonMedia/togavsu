package forms

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// Issue is one validation problem on a field (or nested path like "kids[0].name").
type Issue struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (i Issue) Error() string {
	if i.Field == "" {
		return i.Message
	}
	return i.Field + ": " + i.Message
}

// Issues is every problem from one Validate call.
type Issues []Issue

func (is Issues) Error() string {
	if len(is) == 0 {
		return ""
	}
	parts := make([]string, len(is))
	for i, issue := range is {
		parts[i] = issue.Error()
	}
	return strings.Join(parts, "; ")
}

// Validate checks answers against a form definition for audience. Only visible
// tracks, screens and fields are checked. An "I don't know" answer is accepted
// when the field allows it; otherwise it is refused.
func Validate(form Form, answers Answers, audience Audience) Issues {
	var out Issues
	for _, track := range form.Tracks {
		if !Eval(track.When, answers) {
			continue
		}
		for _, screen := range track.Screens {
			if !Eval(screen.When, answers) {
				continue
			}
			for _, field := range VisibleFields(screen, answers, audience) {
				out = append(out, validateField(field, answers.Get(field.ID), answers, "")...)
			}
		}
	}
	return out
}

// ValidateScreen checks one screen only.
func ValidateScreen(screen Screen, answers Answers, audience Audience) Issues {
	var out Issues
	for _, field := range VisibleFields(screen, answers, audience) {
		out = append(out, validateField(field, answers.Get(field.ID), answers, "")...)
	}
	return out
}

func validateField(field Field, ans Answer, answers Answers, path string) Issues {
	id := field.ID
	if path != "" {
		id = path
	}
	if !KnownType(field.Type) {
		return Issues{{Field: id, Code: "unknown_type", Message: "unknown field type " + string(field.Type)}}
	}
	if ans.Unknown {
		if !field.AllowUnknown {
			return Issues{{Field: id, Code: "unknown_not_allowed", Message: "\"I don't know\" is not allowed"}}
		}
		return nil
	}
	if field.Type == TypeReadOnly {
		return nil
	}
	if ans.Empty() {
		if field.Type == TypeRepeating {
			return boundIssues(id, 0, field.Min, field.Max)
		}
		if field.Required {
			return Issues{{Field: id, Code: "required", Message: "required"}}
		}
		return nil
	}
	var out Issues
	switch field.Type {
	case TypeEmail:
		if _, err := mail.ParseAddress(ans.StringValue()); err != nil {
			out = append(out, Issue{Field: id, Code: "email", Message: "not a valid email"})
		}
	case TypeNumber:
		n, ok := ans.FloatValue()
		if !ok {
			out = append(out, Issue{Field: id, Code: "number", Message: "not a number"})
			break
		}
		out = append(out, boundIssues(id, n, field.Min, field.Max)...)
	case TypeDate:
		s := ans.StringValue()
		if _, err := time.Parse("2006-01-02", s); err != nil {
			out = append(out, Issue{Field: id, Code: "date", Message: "use YYYY-MM-DD"})
			break
		}
		if field.MinDate != "" && s < field.MinDate {
			out = append(out, Issue{Field: id, Code: "min", Message: "before minimum date"})
		}
		if field.MaxDate != "" && s > field.MaxDate {
			out = append(out, Issue{Field: id, Code: "max", Message: "after maximum date"})
		}
	case TypeSelect, TypeRadio:
		if !optionHas(field.Options, ans.StringValue()) {
			out = append(out, Issue{Field: id, Code: "option", Message: "not an allowed option"})
		}
	case TypeMultiSelect:
		for _, v := range ans.StringsValue() {
			if !optionHas(field.Options, v) {
				out = append(out, Issue{Field: id, Code: "option", Message: "not an allowed option: " + v})
			}
		}
	case TypeBoolean:
		if _, ok := ans.Value.(bool); !ok {
			out = append(out, Issue{Field: id, Code: "boolean", Message: "not a boolean"})
		}
	case TypeAddress:
		addr := ans.AddressValue()
		if field.Required && (addr.Line1 == "" || addr.City == "" || addr.State == "" || addr.Postal == "") {
			out = append(out, Issue{Field: id, Code: "address", Message: "address needs line1, city, state and postal"})
		}
	case TypeRepeating:
		rows := ans.RowsValue()
		n := float64(len(rows))
		out = append(out, boundIssues(id, n, field.Min, field.Max)...)
		for i, row := range rows {
			rowAnswers := Answers(row)
			for _, nested := range field.Fields {
				nestedPath := fmt.Sprintf("%s[%d].%s", id, i, nested.ID)
				if !Eval(nested.When, rowAnswers) {
					continue
				}
				out = append(out, validateField(nested, rowAnswers.Get(nested.ID), rowAnswers, nestedPath)...)
			}
		}
	case TypeText, TypeTel, TypeTextarea, TypeMasked, TypeHidden, TypeFile:
		// string-ish; pattern applied below
	}
	if field.Pattern != "" && field.Type != TypeRepeating && field.Type != TypeAddress && field.Type != TypeBoolean && field.Type != TypeMultiSelect {
		re, err := regexp.Compile(field.Pattern)
		if err != nil {
			out = append(out, Issue{Field: id, Code: "pattern", Message: "invalid field pattern"})
		} else if !re.MatchString(ans.StringValue()) {
			out = append(out, Issue{Field: id, Code: "pattern", Message: "does not match pattern"})
		}
	}
	_ = answers
	return out
}

func optionHas(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func boundIssues(id string, n float64, min, max *float64) Issues {
	var out Issues
	if min != nil && n < *min {
		out = append(out, Issue{Field: id, Code: "min", Message: fmt.Sprintf("must be at least %v", *min)})
	}
	if max != nil && n > *max {
		out = append(out, Issue{Field: id, Code: "max", Message: fmt.Sprintf("must be at most %v", *max)})
	}
	return out
}

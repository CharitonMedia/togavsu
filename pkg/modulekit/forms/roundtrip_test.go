package forms

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestFieldTypeRoundTripAutosaveAndSubmit is one end-to-end check per CORE-33 field
// type: RenderScreen → post what the browser/HTMX whole-form autosave would send →
// full submit → typed Answers + clean Validate. Includes a repeating group of two
// rows with an edit in row 2.
func TestFieldTypeRoundTripAutosaveAndSubmit(t *testing.T) {
	minOne := 1.0
	cases := []struct {
		name   string
		field  Field
		post   url.Values
		check  func(t *testing.T, a Answer)
		skipRO bool
	}{
		{
			name:  "text",
			field: Field{ID: "f", Type: TypeText, Label: "Text", Required: true},
			post:  url.Values{"f": {"hello"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "hello" {
					t.Fatalf("text = %#v", a)
				}
			},
		},
		{
			name:  "email",
			field: Field{ID: "f", Type: TypeEmail, Label: "Email", Required: true},
			post:  url.Values{"f": {"a@example.com"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "a@example.com" {
					t.Fatalf("email = %#v", a)
				}
			},
		},
		{
			name:  "tel",
			field: Field{ID: "f", Type: TypeTel, Label: "Tel"},
			post:  url.Values{"f": {"555-0100"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "555-0100" {
					t.Fatalf("tel = %#v", a)
				}
			},
		},
		{
			name:  "textarea",
			field: Field{ID: "f", Type: TypeTextarea, Label: "Notes"},
			post:  url.Values{"f": {"line1\nline2"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "line1\nline2" {
					t.Fatalf("textarea = %#v", a)
				}
			},
		},
		{
			name:  "number",
			field: Field{ID: "f", Type: TypeNumber, Label: "Age", Required: true},
			post:  url.Values{"f": {"42"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				n, ok := a.FloatValue()
				if !ok || n != 42 {
					t.Fatalf("number = %#v", a)
				}
			},
		},
		{
			name:  "date",
			field: Field{ID: "f", Type: TypeDate, Label: "When", Required: true},
			post:  url.Values{"f": {"2024-06-15"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "2024-06-15" {
					t.Fatalf("date = %#v", a)
				}
			},
		},
		{
			name:  "select",
			field: Field{ID: "f", Type: TypeSelect, Label: "Role", Options: []Option{{Value: "a"}, {Value: "b"}}, Required: true},
			post:  url.Values{"f": {"b"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "b" {
					t.Fatalf("select = %#v", a)
				}
			},
		},
		{
			name:  "multi-select",
			field: Field{ID: "f", Type: TypeMultiSelect, Label: "Tags", Options: []Option{{Value: "x"}, {Value: "y"}}, Required: true},
			post:  url.Values{"f": {"x"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				s := a.StringsValue()
				if len(s) != 1 || s[0] != "x" {
					t.Fatalf("multi-select single = %#v", a)
				}
			},
		},
		{
			name:  "boolean-true",
			field: Field{ID: "f", Type: TypeBoolean, Label: "OK"},
			post:  url.Values{"f": {"false", "true"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if !a.BoolValue() {
					t.Fatalf("boolean true = %#v", a)
				}
			},
		},
		{
			name:  "boolean-false",
			field: Field{ID: "f", Type: TypeBoolean, Label: "OK"},
			post:  url.Values{"f": {"false"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if _, ok := a.Value.(bool); !ok || a.BoolValue() {
					t.Fatalf("boolean false = %#v", a)
				}
			},
		},
		{
			name:  "radio",
			field: Field{ID: "f", Type: TypeRadio, Options: []Option{{Value: "1"}, {Value: "2"}}, Required: true},
			post:  url.Values{"f": {"2"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "2" {
					t.Fatalf("radio = %#v", a)
				}
			},
		},
		{
			name:  "address",
			field: Field{ID: "f", Type: TypeAddress, Label: "Addr", Required: true},
			post: url.Values{
				"f.line1": {"1 Main"}, "f.city": {"Town"}, "f.state": {"MO"}, "f.postal": {"65101"},
				"csrf": {"tok"},
			},
			check: func(t *testing.T, a Answer) {
				addr := a.AddressValue()
				if addr.Line1 != "1 Main" || addr.City != "Town" || addr.State != "MO" || addr.Postal != "65101" {
					t.Fatalf("address = %#v", addr)
				}
			},
		},
		{
			name:  "masked",
			field: Field{ID: "f", Type: TypeMasked, Label: "SSN", SecretKey: "forms/ssn", Required: true},
			post:  url.Values{"f": {"123-45-6789"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "123-45-6789" {
					t.Fatalf("masked = %#v", a)
				}
			},
		},
		{
			name:   "read-only",
			field:  Field{ID: "f", Type: TypeReadOnly, Value: "fixed"},
			post:   url.Values{"csrf": {"tok"}},
			skipRO: true,
			check: func(t *testing.T, a Answer) {
				// read-only is not posted; Validate skips it
			},
		},
		{
			name:  "hidden",
			field: Field{ID: "f", Type: TypeHidden, Value: "seed"},
			post:  url.Values{"f": {"seed"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "seed" {
					t.Fatalf("hidden = %#v", a)
				}
			},
		},
		{
			name:  "file",
			field: Field{ID: "f", Type: TypeFile, Label: "Doc"},
			post:  url.Values{"f": {"file-ref-1"}, "csrf": {"tok"}},
			check: func(t *testing.T, a Answer) {
				if a.StringValue() != "file-ref-1" {
					t.Fatalf("file = %#v", a)
				}
			},
		},
		{
			name: "repeating",
			field: Field{ID: "kids", Type: TypeRepeating, Label: "Kids", Min: &minOne, Fields: []Field{
				{ID: "name", Type: TypeText, Label: "Name", Required: true},
				{ID: "age", Type: TypeNumber, Label: "Age"},
			}},
			post: url.Values{
				"kids[0].name": {"Ada"}, "kids[0].age": {"10"},
				"kids[1].name": {"Bea"}, "kids[1].age": {"8"},
				"csrf": {"tok"},
			},
			check: func(t *testing.T, a Answer) {
				rows := a.RowsValue()
				if len(rows) != 2 {
					t.Fatalf("rows len = %d %#v", len(rows), a)
				}
				if rows[0]["name"].StringValue() != "Ada" {
					t.Fatalf("row0 name = %#v", rows[0])
				}
				n, ok := rows[1]["age"].FloatValue()
				if !ok || n != 8 || rows[1]["name"].StringValue() != "Bea" {
					t.Fatalf("row1 = %#v", rows[1])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := Form{ID: "rt", Tracks: []Track{{ID: "t", Screens: []Screen{{ID: "s", Fields: []Field{tc.field}}}}}}
			screen := form.Tracks[0].Screens[0]
			var buf strings.Builder
			if err := RenderScreen(&buf, screen, Answers{}, AudiencePublic, RenderOptions{
				AutosaveURL: "/autosave",
				FormAction:  "/submit",
				CSRFToken:   "tok",
			}); err != nil {
				t.Fatal(err)
			}
			html := buf.String()
			if !strings.Contains(html, `hx-post="/autosave"`) || !strings.Contains(html, `hx-include="this"`) {
				t.Fatalf("form must carry whole-form autosave:\n%s", html)
			}
			if strings.Contains(html, `hx-include="closest .tv-field`) {
				t.Fatalf("must not use per-field hx-include:\n%s", html)
			}

			var stored Answers
			h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
				patch.Apply(&stored)
				return stored, nil
			})

			// Autosave path (what HTMX posts from the form).
			req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(tc.post.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			req.Header.Set("X-CSRF-Token", "tok")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("autosave code = %d body=%s", rec.Code, rec.Body.String())
			}
			if !tc.skipRO {
				tc.check(t, stored.Get(tc.field.ID))
			}

			// Full submit path: same parser.
			posted := ParseFormAnswers(form, tc.post)
			stored = MergeFormSubmit(Answers{}, posted, form)
			if !tc.skipRO {
				tc.check(t, stored.Get(tc.field.ID))
			}
			if issues := Validate(form, stored, AudiencePublic); len(issues) != 0 {
				t.Fatalf("Validate: %v (answers=%#v)", issues, stored)
			}
		})
	}
}

// TestRepeatingAutosaveKeepsSiblingRows: editing row 2 of a two-row group via
// whole-form autosave must keep row 0 (Codex P1 / Patrick redesign).
func TestRepeatingAutosaveKeepsSiblingRows(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText},
			{ID: "age", Type: TypeNumber},
		}},
	}}}}}}
	stored := Answers{"kids": {Value: []map[string]Answer{
		{"name": {Value: "Ada"}, "age": {Value: float64(10)}},
		{"name": {Value: "Bea"}, "age": {Value: float64(8)}},
	}}}

	var buf strings.Builder
	if err := RenderScreen(&buf, form.Tracks[0].Screens[0], stored, AudiencePublic, RenderOptions{
		AutosaveURL: "/autosave",
		FormAction:  "/submit",
		CSRFToken:   "tok",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `name="kids[0].name"`) || !strings.Contains(html, `name="kids[1].name"`) {
		t.Fatalf("render missing both rows:\n%s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	// Whole form after editing kids[1].name only (browser still posts every control).
	body := url.Values{
		"kids[0].name": {"Ada"},
		"kids[0].age":  {"10"},
		"kids[1].name": {"Beatrice"},
		"kids[1].age":  {"8"},
		"csrf":         {"tok"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	rows := stored.Get("kids").RowsValue()
	if len(rows) != 2 {
		t.Fatalf("rows wiped: %#v", stored["kids"])
	}
	if rows[0]["name"].StringValue() != "Ada" {
		t.Fatalf("row0 lost: %#v", rows[0])
	}
	n, ok := rows[0]["age"].FloatValue()
	if !ok || n != 10 {
		t.Fatalf("row0 age: %#v", rows[0])
	}
	if rows[1]["name"].StringValue() != "Beatrice" {
		t.Fatalf("row1 name: %#v", rows[1])
	}
}

// TestTextTrueNotBool: a text/select/radio value of "true" must stay a string.
func TestTextTrueNotBool(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "note", Type: TypeText},
		{ID: "pick", Type: TypeSelect, Options: []Option{{Value: "true"}, {Value: "false"}}},
	}}}}}}
	got := ParseFormAnswers(form, map[string][]string{
		"note": {"true"},
		"pick": {"true"},
	})
	if _, isBool := got.Get("note").Value.(bool); isBool {
		t.Fatalf("text true became bool: %#v", got["note"])
	}
	if got.Get("note").StringValue() != "true" || got.Get("pick").StringValue() != "true" {
		t.Fatalf("got %#v", got)
	}
}

package forms

import (
	"strings"
	"testing"
)

func TestRenderScreenAudiencesAndHTMX(t *testing.T) {
	screen := Screen{
		ID:    "s1",
		Title: "About you",
		Fields: []Field{
			{ID: "name", Type: TypeText, Label: "Name", AllowUnknown: true},
			{ID: "email", Type: TypeEmail, Label: "Email"},
			{ID: "secret", Type: TypeMasked, Label: "SSN", SecretKey: "k"},
			{ID: "role", Type: TypeSelect, Label: "Role", Options: []Option{{Value: "a", Label: "A"}}},
			{ID: "staff_note", Type: TypeTextarea, Audiences: []Audience{AudienceStaff}},
			{ID: "hidden", Type: TypeHidden, Value: "x"},
			{ID: "addr", Type: TypeAddress, Label: "Address"},
			{ID: "ok", Type: TypeBoolean, Label: "OK"},
			{ID: "pick", Type: TypeRadio, Options: []Option{{Value: "1", Label: "One"}}},
			{ID: "tags", Type: TypeMultiSelect, Options: []Option{{Value: "x"}}},
			{ID: "ro", Type: TypeReadOnly, Value: "fixed"},
			{ID: "kids", Type: TypeRepeating, Fields: []Field{{ID: "name", Type: TypeText, Label: "Child"}}},
		},
	}
	answers := Answers{
		"name":  {Unknown: true},
		"email": {Value: "a@example.com"},
		"kids":  {Value: []map[string]Answer{{"name": {Value: "Ada"}}}},
		"addr":  {Value: Address{Line1: "1 Main", City: "Town"}},
		"ok":    {Value: true},
		"pick":  {Value: "1"},
		"tags":  {Value: []string{"x"}},
	}
	var buf strings.Builder
	err := RenderScreen(&buf, screen, answers, AudiencePublic, RenderOptions{
		AutosaveURL: "/m/intake/api/autosave",
		FormAction:  "/m/intake/submit",
		CSRFToken:   "tok",
		Errors:      map[string]string{"email": "bad"},
	})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		`data-audience="public"`,
		`hx-post="/m/intake/api/autosave"`,
		`name="csrf" value="tok"`,
		`I don't know`,
		`disabled`,
		`tv-field-error`,
		`type="hidden"`,
		`name="addr.line1"`,
		`tv-readonly`,
		`tv-repeat-row`,
		`name="kids[0].name"`,
		`type="password"`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("render missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "staff_note") {
		t.Fatal("public render should omit staff-only field")
	}

	buf.Reset()
	if err := RenderScreen(&buf, screen, answers, AudienceStaff, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "staff_note") {
		t.Fatal("staff render should include staff-only field")
	}
}

func TestRenderEscapes(t *testing.T) {
	screen := Screen{Fields: []Field{
		{ID: "n", Type: TypeText, Label: `<script>`, Placeholder: `"x"`},
	}}
	var buf strings.Builder
	if err := RenderScreen(&buf, screen, Answers{"n": {Value: `<img>`}}, AudienceClient, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if strings.Contains(html, `<script>`) || strings.Contains(html, `value="<img>"`) {
		t.Fatalf("unescaped HTML: %s", html)
	}
	if !strings.Contains(html, `&lt;script&gt;`) || !strings.Contains(html, `&lt;img&gt;`) {
		t.Fatalf("expected escaped values: %s", html)
	}
}

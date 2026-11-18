package forms

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestRepeatingMaskedSubfieldPreserved: editing any field must not erase sealed
// masked values inside repeating rows (Codex P1 on 630e0f9).
func TestRepeatingMaskedSubfieldPreserved(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText},
			{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn"},
		}},
	}}}}}}
	sealed0 := CipherPrefix + "child0-ssn"
	sealed1 := CipherPrefix + "child1-ssn"
	stored := Answers{"kids": {Value: []map[string]Answer{
		{"name": {Value: "Ada"}, "ssn": {Value: sealed0}},
		{"name": {Value: "Bea"}, "ssn": {Value: sealed1}},
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
	if !strings.Contains(html, `data-autosave-masked="1"`) {
		t.Fatalf("masked sub-field must render empty password: %s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})

	// Autosave: OfflineQueueJS strips empty masked kids[N].ssn; presence + other fields remain.
	auto := url.Values{
		"__present":    {"kids", "kids[0].name", "kids[0].ssn", "kids[1].name", "kids[1].ssn"},
		"kids[0].name": {"Ada"},
		"kids[1].name": {"Beatrice"},
		"csrf":         {"tok"},
	}
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(auto.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("autosave code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows := stored.Get("kids").RowsValue()
	if len(rows) != 2 {
		t.Fatalf("autosave wiped rows: %#v", stored["kids"])
	}
	if rows[0]["ssn"].StringValue() != sealed0 || rows[1]["ssn"].StringValue() != sealed1 {
		t.Fatalf("autosave erased nested masked: %#v", rows)
	}
	if rows[1]["name"].StringValue() != "Beatrice" {
		t.Fatalf("autosave name edit lost: %#v", rows[1])
	}

	// Full submit without OfflineQueueJS: empty kids[N].ssn="" posts.
	full := url.Values{
		"__present":    {"kids", "kids[0].name", "kids[0].ssn", "kids[1].name", "kids[1].ssn"},
		"kids[0].name": {"Ada"},
		"kids[0].ssn":  {""},
		"kids[1].name": {"Beatrice"},
		"kids[1].ssn":  {""},
		"csrf":         {"tok"},
	}
	posted := ParseFormAnswers(form, full)
	stored = MergeFormSubmit(stored, posted, form)
	rows = stored.Get("kids").RowsValue()
	if len(rows) != 2 {
		t.Fatalf("submit wiped rows: %#v", stored["kids"])
	}
	if rows[0]["ssn"].StringValue() != sealed0 || rows[1]["ssn"].StringValue() != sealed1 {
		t.Fatalf("submit erased nested masked: %#v", rows)
	}
}

// TestFileEmptyPostKeepsStoredRef: browser urlencoded posts send file inputs as
// empty; must not wipe the stored upload ref (Codex P2).
func TestFileEmptyPostKeepsStoredRef(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "doc", Type: TypeFile, Label: "Doc"},
		{ID: "name", Type: TypeText, Label: "Name"},
	}}}}}}
	stored := Answers{"doc": {Value: "file-ref-1"}, "name": {Value: "Avery"}}

	var buf strings.Builder
	if err := RenderScreen(&buf, form.Tracks[0].Screens[0], stored, AudiencePublic, RenderOptions{
		AutosaveURL: "/autosave",
		FormAction:  "/submit",
		CSRFToken:   "tok",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, "file-ref-1") {
		t.Fatalf("renderer must show stored file name: %s", html)
	}
	if !strings.Contains(OfflineQueueJS, "type=file") && !strings.Contains(OfflineQueueJS, "type='file'") {
		t.Fatal("OfflineQueueJS must strip file inputs from urlencoded autosave")
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	// What a browser actually sends on whole-form autosave (empty file control).
	body := url.Values{
		"__present": {"doc", "name"},
		"doc":       {""},
		"name":      {"Blake"},
		"csrf":      {"tok"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if stored.Get("doc").StringValue() != "file-ref-1" {
		t.Fatalf("empty file post wiped ref: %#v", stored["doc"])
	}
	if stored.Get("name").StringValue() != "Blake" {
		t.Fatalf("name: %#v", stored["name"])
	}

	// Full submit same shape.
	posted := ParseFormAnswers(form, url.Values{
		"__present": {"doc", "name"},
		"doc":       {""},
		"name":      {"Blake"},
		"csrf":      {"tok"},
	})
	if _, ok := posted["doc"]; ok {
		t.Fatalf("parser must ignore file inputs in form post: %#v", posted["doc"])
	}
	stored = MergeFormSubmit(stored, posted, form)
	if stored.Get("doc").StringValue() != "file-ref-1" {
		t.Fatalf("submit wiped file ref: %#v", stored["doc"])
	}
}

// TestPresenceClearsMultiSelectAndRepeating: empty multi-select and all rows
// removed must clear stored answers when the field was rendered (Codex P2 / follow-on a).
func TestPresenceClearsMultiSelectAndRepeating(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "tags", Type: TypeMultiSelect, Options: []Option{{Value: "x"}, {Value: "y"}}},
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText},
		}},
		{ID: "other_screen", Type: TypeText}, // not rendered below
	}}}}}}
	stored := Answers{
		"tags":         {Value: []string{"x", "y"}},
		"kids":         {Value: []map[string]Answer{{"name": {Value: "Ada"}}}},
		"other_screen": {Value: "keep-me"},
	}

	screen := Screen{ID: "s", Fields: []Field{
		form.Tracks[0].Screens[0].Fields[0],
		form.Tracks[0].Screens[0].Fields[1],
	}}
	var buf strings.Builder
	if err := RenderScreen(&buf, screen, stored, AudiencePublic, RenderOptions{
		AutosaveURL: "/autosave",
		FormAction:  "/submit",
		CSRFToken:   "tok",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `name="__present" value="tags"`) || !strings.Contains(html, `name="__present" value="kids"`) {
		t.Fatalf("render must emit presence markers:\n%s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	// Deselect all tags; remove every repeating row — only presence markers remain.
	body := url.Values{
		"__present": {"tags", "kids"},
		"csrf":      {"tok"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d", rec.Code)
	}
	if got := stored.Get("tags").StringsValue(); len(got) != 0 {
		t.Fatalf("multi-select should clear: %#v", stored["tags"])
	}
	if got := stored.Get("kids").RowsValue(); len(got) != 0 {
		t.Fatalf("repeating should clear: %#v", stored["kids"])
	}
	if stored.Get("other_screen").StringValue() != "keep-me" {
		t.Fatalf("field not on this screen must keep: %#v", stored["other_screen"])
	}

	// Full submit same shape.
	posted := ParseFormAnswers(form, url.Values{"__present": {"tags", "kids"}, "csrf": {"tok"}})
	stored = MergeFormSubmit(Answers{
		"tags":         {Value: []string{"x"}},
		"kids":         {Value: []map[string]Answer{{"name": {Value: "Ada"}}}},
		"other_screen": {Value: "keep-me"},
	}, posted, form)
	if len(stored.Get("tags").StringsValue()) != 0 || len(stored.Get("kids").RowsValue()) != 0 {
		t.Fatalf("submit clear failed: %#v", stored)
	}
	if stored.Get("other_screen").StringValue() != "keep-me" {
		t.Fatalf("other screen: %#v", stored["other_screen"])
	}
}

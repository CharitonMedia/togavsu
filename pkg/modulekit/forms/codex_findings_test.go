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
	id0, id1 := "row-ada", "row-bea"
	stored := Answers{"kids": {Value: []map[string]Answer{
		{RowIDKey: {Value: id0}, "name": {Value: "Ada"}, "ssn": {Value: sealed0}},
		{RowIDKey: {Value: id1}, "name": {Value: "Bea"}, "ssn": {Value: sealed1}},
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
	if !strings.Contains(html, `name="kids[0].`+RowIDKey+`"`) || !strings.Contains(html, id0) {
		t.Fatalf("render must emit stable row ids:\n%s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})

	// Autosave: OfflineQueueJS strips empty masked kids[N].ssn; presence + other fields remain.
	auto := url.Values{
		"__present":           {"kids", "kids[0].name", "kids[0].ssn", "kids[1].name", "kids[1].ssn"},
		"kids[0]." + RowIDKey: {id0},
		"kids[0].name":        {"Ada"},
		"kids[1]." + RowIDKey: {id1},
		"kids[1].name":        {"Beatrice"},
		"csrf":                {"tok"},
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
		"__present":           {"kids", "kids[0].name", "kids[0].ssn", "kids[1].name", "kids[1].ssn"},
		"kids[0]." + RowIDKey: {id0},
		"kids[0].name":        {"Ada"},
		"kids[0].ssn":         {""},
		"kids[1]." + RowIDKey: {id1},
		"kids[1].name":        {"Beatrice"},
		"kids[1].ssn":         {""},
		"csrf":                {"tok"},
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

// TestRepeatingDeleteKeepsMaskedByRowID: removing the first of three rows must
// keep rows 2 and 3's own sealed masked values (Codex P1 on 2b1b58d). Matching
// by array position would give the former row-1 name the former row-0 SSN.
func TestRepeatingDeleteKeepsMaskedByRowID(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText},
			{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn"},
			{ID: "doc", Type: TypeFile},
		}},
	}}}}}}
	idA, idB, idC := "row-a", "row-b", "row-c"
	sealedA := CipherPrefix + "ssn-a"
	sealedB := CipherPrefix + "ssn-b"
	sealedC := CipherPrefix + "ssn-c"
	stored := Answers{"kids": {Value: []map[string]Answer{
		{RowIDKey: {Value: idA}, "name": {Value: "A"}, "ssn": {Value: sealedA}, "doc": {Value: "file-a"}},
		{RowIDKey: {Value: idB}, "name": {Value: "B"}, "ssn": {Value: sealedB}, "doc": {Value: "file-b"}},
		{RowIDKey: {Value: idC}, "name": {Value: "C"}, "ssn": {Value: sealedC}, "doc": {Value: "file-c"}},
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
	if !strings.Contains(RepeatingJS, "data-tv-row-id") || !strings.Contains(RepeatingJS, "newRowId") {
		t.Fatal("RepeatingJS must mint/keep row ids on add/reindex")
	}
	if !strings.Contains(html, idA) || !strings.Contains(html, idB) || !strings.Contains(html, idC) {
		t.Fatalf("render missing row ids:\n%s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})

	// Delete row 0 (A): RepeatingJS reindexes B→[0], C→[1] and keeps their __row values.
	// OfflineQueueJS strips empty masked; file inputs are not in the urlencoded post.
	body := url.Values{
		"__present": {
			"kids",
			"kids[0].name", "kids[0].ssn", "kids[0].doc",
			"kids[1].name", "kids[1].ssn", "kids[1].doc",
		},
		"kids[0]." + RowIDKey: {idB},
		"kids[0].name":        {"B"},
		"kids[1]." + RowIDKey: {idC},
		"kids[1].name":        {"C"},
		"csrf":                {"tok"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
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
		t.Fatalf("want 2 rows after delete, got %#v", stored["kids"])
	}
	if rowIDOf(rows[0]) != idB || rows[0]["name"].StringValue() != "B" {
		t.Fatalf("row0 should be B: %#v", rows[0])
	}
	if rows[0]["ssn"].StringValue() != sealedB {
		t.Fatalf("B must keep own sealed SSN (not A's): %#v", rows[0])
	}
	if rows[0]["doc"].StringValue() != "file-b" {
		t.Fatalf("B must keep own file ref: %#v", rows[0])
	}
	if rowIDOf(rows[1]) != idC || rows[1]["name"].StringValue() != "C" {
		t.Fatalf("row1 should be C: %#v", rows[1])
	}
	if rows[1]["ssn"].StringValue() != sealedC {
		t.Fatalf("C must keep own sealed SSN: %#v", rows[1])
	}
	if rows[1]["doc"].StringValue() != "file-c" {
		t.Fatalf("C must keep own file ref: %#v", rows[1])
	}
	for _, row := range rows {
		if row["ssn"].StringValue() == sealedA || row["doc"].StringValue() == "file-a" || rowIDOf(row) == idA {
			t.Fatalf("deleted row A's sealed value must be gone: %#v", rows)
		}
	}

	// Unknown __row is a new row: no carried masked/file from position 0.
	posted := ParseFormAnswers(form, url.Values{
		"__present":           {"kids", "kids[0].name", "kids[0].ssn"},
		"kids[0]." + RowIDKey: {"brand-new"},
		"kids[0].name":        {"D"},
		"csrf":                {"tok"},
	})
	got := MergeFormSubmit(stored, posted, form).Get("kids").RowsValue()
	if len(got) != 1 || got[0]["name"].StringValue() != "D" {
		t.Fatalf("new row: %#v", got)
	}
	if got[0]["ssn"].StringValue() != "" || got[0]["doc"].StringValue() != "" {
		t.Fatalf("unknown row id must not carry masked/file: %#v", got[0])
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

// TestNestedRepeatingGroupsE2E: repeating-inside-repeating through renderer →
// HTMX autosave shape → parse → merge (Codex P2 on e729d02). Covers add/edit/
// delete at both levels, presence clear of all inner rows, masked keep, and
// file-ref keep. RepeatingJS must reindex only the edited group's own [N].
func TestNestedRepeatingGroupsE2E(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText},
			{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn"},
			{ID: "doc", Type: TypeFile},
			{ID: "pets", Type: TypeRepeating, Fields: []Field{
				{ID: "name", Type: TypeText},
				{ID: "chip", Type: TypeMasked, SecretKey: "forms/chip"},
				{ID: "photo", Type: TypeFile},
			}},
		}},
	}}}}}}

	kidA, kidB := "kid-a", "kid-b"
	petA1, petA2, petB1 := "pet-a1", "pet-a2", "pet-b1"
	ssnA, ssnB := CipherPrefix+"ssn-a", CipherPrefix+"ssn-b"
	chipA1, chipA2, chipB1 := CipherPrefix+"chip-a1", CipherPrefix+"chip-a2", CipherPrefix+"chip-b1"

	stored := Answers{"kids": {Value: []map[string]Answer{
		{
			RowIDKey: {Value: kidA}, "name": {Value: "Ada"}, "ssn": {Value: ssnA}, "doc": {Value: "doc-a"},
			"pets": {Value: []map[string]Answer{
				{RowIDKey: {Value: petA1}, "name": {Value: "Fluffy"}, "chip": {Value: chipA1}, "photo": {Value: "photo-a1"}},
				{RowIDKey: {Value: petA2}, "name": {Value: "Spot"}, "chip": {Value: chipA2}, "photo": {Value: "photo-a2"}},
			}},
		},
		{
			RowIDKey: {Value: kidB}, "name": {Value: "Bea"}, "ssn": {Value: ssnB}, "doc": {Value: "doc-b"},
			"pets": {Value: []map[string]Answer{
				{RowIDKey: {Value: petB1}, "name": {Value: "Rex"}, "chip": {Value: chipB1}, "photo": {Value: "photo-b1"}},
			}},
		},
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
	// Nested group must carry its full name path so RepeatingJS reindexes only that [N].
	if !strings.Contains(html, `data-name="kids"`) {
		t.Fatalf("outer repeating needs data-name:\n%s", html)
	}
	if !strings.Contains(html, `data-name="kids[0].pets"`) || !strings.Contains(html, `data-name="kids[1].pets"`) {
		t.Fatalf("nested repeating needs full-path data-name:\n%s", html)
	}
	if !strings.Contains(html, `name="kids[0].pets[1].`+RowIDKey+`"`) || !strings.Contains(html, petA2) {
		t.Fatalf("nested row ids missing:\n%s", html)
	}
	if !strings.Contains(RepeatingJS, "data-name") {
		t.Fatal("RepeatingJS must reindex by data-name prefix, not first [N]")
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})

	// 1) Edit kid B name + pet under kid A; leave masked/file empty (unchanged).
	// Simulates browser post after correct nested reindex (outer indexes untouched).
	body := url.Values{
		"__present": {
			"kids",
			"kids[0].name", "kids[0].ssn", "kids[0].doc", "kids[0].pets",
			"kids[0].pets[0].name", "kids[0].pets[0].chip", "kids[0].pets[0].photo",
			"kids[0].pets[1].name", "kids[0].pets[1].chip", "kids[0].pets[1].photo",
			"kids[1].name", "kids[1].ssn", "kids[1].doc", "kids[1].pets",
			"kids[1].pets[0].name", "kids[1].pets[0].chip", "kids[1].pets[0].photo",
		},
		"kids[0]." + RowIDKey:         {kidA},
		"kids[0].name":                {"Ada"},
		"kids[0].pets[0]." + RowIDKey: {petA1},
		"kids[0].pets[0].name":        {"Fluffy"},
		"kids[0].pets[1]." + RowIDKey: {petA2},
		"kids[0].pets[1].name":        {"Spotty"}, // edit inner
		"kids[1]." + RowIDKey:         {kidB},
		"kids[1].name":                {"Beatrice"}, // edit outer
		"kids[1].pets[0]." + RowIDKey: {petB1},
		"kids[1].pets[0].name":        {"Rex"},
		"csrf":                        {"tok"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("edit autosave code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows := stored.Get("kids").RowsValue()
	if len(rows) != 2 {
		t.Fatalf("edit wiped kids: %#v", rows)
	}
	if rows[0]["ssn"].StringValue() != ssnA || rows[1]["ssn"].StringValue() != ssnB {
		t.Fatalf("outer masked lost: %#v", rows)
	}
	if rows[0]["doc"].StringValue() != "doc-a" || rows[1]["doc"].StringValue() != "doc-b" {
		t.Fatalf("outer file lost: %#v", rows)
	}
	if rows[1]["name"].StringValue() != "Beatrice" {
		t.Fatalf("outer edit: %#v", rows[1])
	}
	petsA := rows[0]["pets"].RowsValue()
	if len(petsA) != 2 || petsA[1]["name"].StringValue() != "Spotty" {
		t.Fatalf("inner edit: %#v", petsA)
	}
	if petsA[0]["chip"].StringValue() != chipA1 || petsA[1]["chip"].StringValue() != chipA2 {
		t.Fatalf("inner masked lost: %#v", petsA)
	}
	if petsA[0]["photo"].StringValue() != "photo-a1" || petsA[1]["photo"].StringValue() != "photo-a2" {
		t.Fatalf("inner file lost: %#v", petsA)
	}

	// 2) Delete petA1 under kid A (inner delete). Remaining petA2 keeps its own chip/photo.
	// After RepeatingJS reindex of kids[0].pets only: Spotty is pets[0] with petA2 id.
	body = url.Values{
		"__present": {
			"kids",
			"kids[0].name", "kids[0].ssn", "kids[0].doc", "kids[0].pets",
			"kids[0].pets[0].name", "kids[0].pets[0].chip", "kids[0].pets[0].photo",
			"kids[1].name", "kids[1].ssn", "kids[1].doc", "kids[1].pets",
			"kids[1].pets[0].name", "kids[1].pets[0].chip", "kids[1].pets[0].photo",
		},
		"kids[0]." + RowIDKey:         {kidA},
		"kids[0].name":                {"Ada"},
		"kids[0].pets[0]." + RowIDKey: {petA2},
		"kids[0].pets[0].name":        {"Spotty"},
		"kids[1]." + RowIDKey:         {kidB},
		"kids[1].name":                {"Beatrice"},
		"kids[1].pets[0]." + RowIDKey: {petB1},
		"kids[1].pets[0].name":        {"Rex"},
		"csrf":                        {"tok"},
	}.Encode()
	req = httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("inner delete code=%d", rec.Code)
	}
	rows = stored.Get("kids").RowsValue()
	petsA = rows[0]["pets"].RowsValue()
	if len(petsA) != 1 || rowIDOf(petsA[0]) != petA2 {
		t.Fatalf("after inner delete: %#v", petsA)
	}
	if petsA[0]["chip"].StringValue() != chipA2 || petsA[0]["photo"].StringValue() != "photo-a2" {
		t.Fatalf("petA2 must keep own sealed/file (not petA1's): %#v", petsA[0])
	}
	petsB := rows[1]["pets"].RowsValue()
	if len(petsB) != 1 || petsB[0]["chip"].StringValue() != chipB1 {
		t.Fatalf("sibling kid pets untouched: %#v", petsB)
	}

	// 3) Clear every pet under kid A via presence only (no pets[N] keys).
	body = url.Values{
		"__present": {
			"kids",
			"kids[0].name", "kids[0].ssn", "kids[0].doc", "kids[0].pets",
			"kids[1].name", "kids[1].ssn", "kids[1].doc", "kids[1].pets",
			"kids[1].pets[0].name", "kids[1].pets[0].chip", "kids[1].pets[0].photo",
		},
		"kids[0]." + RowIDKey:         {kidA},
		"kids[0].name":                {"Ada"},
		"kids[1]." + RowIDKey:         {kidB},
		"kids[1].name":                {"Beatrice"},
		"kids[1].pets[0]." + RowIDKey: {petB1},
		"kids[1].pets[0].name":        {"Rex"},
		"csrf":                        {"tok"},
	}.Encode()
	req = httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", "tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear inner code=%d", rec.Code)
	}
	rows = stored.Get("kids").RowsValue()
	if got := rows[0]["pets"].RowsValue(); len(got) != 0 {
		t.Fatalf("presence must clear nested repeating: %#v", got)
	}
	if got := rows[1]["pets"].RowsValue(); len(got) != 1 || rowIDOf(got[0]) != petB1 {
		t.Fatalf("other outer row's pets must stay: %#v", rows[1]["pets"])
	}
	if rows[0]["ssn"].StringValue() != ssnA {
		t.Fatalf("outer masked after clear: %#v", rows[0])
	}

	// 4) Add a new inner pet (unknown __row) under kid B + full submit path.
	petNew := "pet-new"
	full := url.Values{
		"__present": {
			"kids",
			"kids[0].name", "kids[0].ssn", "kids[0].doc", "kids[0].pets",
			"kids[1].name", "kids[1].ssn", "kids[1].doc", "kids[1].pets",
			"kids[1].pets[0].name", "kids[1].pets[0].chip", "kids[1].pets[0].photo",
			"kids[1].pets[1].name", "kids[1].pets[1].chip", "kids[1].pets[1].photo",
		},
		"kids[0]." + RowIDKey:         {kidA},
		"kids[0].name":                {"Ada"},
		"kids[0].ssn":                 {""},
		"kids[1]." + RowIDKey:         {kidB},
		"kids[1].name":                {"Beatrice"},
		"kids[1].ssn":                 {""},
		"kids[1].pets[0]." + RowIDKey: {petB1},
		"kids[1].pets[0].name":        {"Rex"},
		"kids[1].pets[0].chip":        {""},
		"kids[1].pets[1]." + RowIDKey: {petNew},
		"kids[1].pets[1].name":        {"Nala"},
		"kids[1].pets[1].chip":        {""},
		"csrf":                        {"tok"},
	}
	posted := ParseFormAnswers(form, full)
	stored = MergeFormSubmit(stored, posted, form)
	rows = stored.Get("kids").RowsValue()
	if len(rows[0]["pets"].RowsValue()) != 0 {
		t.Fatalf("kid A pets still clear: %#v", rows[0]["pets"])
	}
	petsB = rows[1]["pets"].RowsValue()
	if len(petsB) != 2 {
		t.Fatalf("add inner row: %#v", petsB)
	}
	if rowIDOf(petsB[0]) != petB1 || petsB[0]["chip"].StringValue() != chipB1 {
		t.Fatalf("existing pet keep masked: %#v", petsB[0])
	}
	if rowIDOf(petsB[1]) != petNew || petsB[1]["name"].StringValue() != "Nala" {
		t.Fatalf("new pet: %#v", petsB[1])
	}
	if petsB[1]["chip"].StringValue() != "" || petsB[1]["photo"].StringValue() != "" {
		t.Fatalf("new pet must not inherit masked/file: %#v", petsB[1])
	}
	if rows[1]["ssn"].StringValue() != ssnB || rows[1]["doc"].StringValue() != "doc-b" {
		t.Fatalf("outer keep after submit: %#v", rows[1])
	}
}

package forms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAutosavePatchApply(t *testing.T) {
	dst := Answers{"keep": {Value: "1"}}
	trueUnknown := true
	AutosavePatch{Answers: Answers{"a": {Value: "x"}}}.Apply(&dst)
	AutosavePatch{Field: "b", Value: "y"}.Apply(&dst)
	AutosavePatch{Field: "c", Unknown: &trueUnknown}.Apply(&dst)
	if dst.Get("keep").StringValue() != "1" || dst.Get("a").StringValue() != "x" || dst.Get("b").StringValue() != "y" {
		t.Fatalf("dst = %#v", dst)
	}
	if !dst.Get("c").Unknown || dst.Get("c").Value != nil {
		t.Fatalf("unknown answer = %#v", dst["c"])
	}
}

func TestAutosaveHandler(t *testing.T) {
	var stored Answers
	h := AutosaveHandler(func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	body := `{"field":"name","value":"Avery"}`
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if stored.Get("name").StringValue() != "Avery" {
		t.Fatalf("stored = %#v", stored)
	}

	req = httptest.NewRequest(http.MethodPost, "/autosave?echo=1", strings.NewReader(`{"answers":{"email":{"value":"a@example.com"}}}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("echo code = %d", rec.Code)
	}
	var got Answers
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Get("email").StringValue() != "a@example.com" {
		t.Fatalf("echo = %#v", got)
	}

	hFail := AutosaveHandler(func(*http.Request, AutosavePatch) (Answers, error) {
		return nil, errors.New("nope")
	})
	rec = httptest.NewRecorder()
	hFail.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"field":"x","value":"1"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("fail code = %d", rec.Code)
	}
}

func TestParseFormAnswers(t *testing.T) {
	got := ParseFormAnswers(map[string][]string{
		"name":           {"Avery"},
		"name.__unknown": {"1"},
		"addr.line1":     {"1 Main"},
		"addr.city":      {"Town"},
		"addr.state":     {"MO"},
		"addr.postal":    {"65101"},
		"tags":           {"x", "y"},
		"ok":             {"true"},
	})
	if !got.Get("name").Unknown {
		t.Fatalf("name should be unknown: %#v", got["name"])
	}
	addr := got.Get("addr").AddressValue()
	if addr.Line1 != "1 Main" || addr.City != "Town" || addr.State != "MO" || addr.Postal != "65101" {
		t.Fatalf("addr = %#v", addr)
	}
	if len(got.Get("tags").StringsValue()) != 2 {
		t.Fatalf("tags = %#v", got["tags"])
	}
	if !got.Get("ok").BoolValue() {
		t.Fatal("ok should be true")
	}
}

func TestOfflineQueueJSPresent(t *testing.T) {
	if !strings.Contains(OfflineQueueJS, "localStorage") || !strings.Contains(OfflineQueueJS, "TogavsuFormsOffline") {
		t.Fatal("OfflineQueueJS missing expected pieces")
	}
	if !strings.Contains(OfflineQueueJS, "_togavsuBody") || !strings.Contains(OfflineQueueJS, "htmx:configRequest") {
		t.Fatal("OfflineQueueJS must capture the HTMX body for replay")
	}
	if !strings.Contains(OfflineQueueJS, `"answers"`) && !strings.Contains(OfflineQueueJS, "answers:") {
		t.Fatal("OfflineQueueJS must enqueue answers, not a nil raw value")
	}
	if !strings.Contains(OfflineQueueJS, "X-CSRF-Token") || !strings.Contains(OfflineQueueJS, "csrfToken") {
		t.Fatal("OfflineQueueJS must carry X-CSRF-Token on enqueue and flush")
	}
	if !strings.Contains(OfflineQueueJS, "var mem") {
		t.Fatal("OfflineQueueJS must keep a memory fallback when localStorage throws on opaque origin")
	}
}

func TestAutosaveHandlerOpaqueCORS(t *testing.T) {
	h := AutosaveHandler(func(*http.Request, AutosavePatch) (Answers, error) {
		return Answers{}, nil
	})
	req := httptest.NewRequest(http.MethodOptions, "/autosave", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-csrf-token,hx-request")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight code = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Fatalf("ACAO = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if !strings.Contains(strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers")), "x-csrf-token") {
		t.Fatalf("Allow-Headers = %q", rec.Header().Get("Access-Control-Allow-Headers"))
	}
}

// TestOpaqueOriginOfflineQueueJS runs the Node behavioural check that localStorage
// SecurityError (opaque CSP origin) still queues and flushes with the memory fallback.
func TestOpaqueOriginOfflineQueueJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available for opaque-origin offline queue check")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	script := filepath.Join(filepath.Dir(file), "opaque_origin_test.js")
	out, err := exec.Command(node, script).CombinedOutput()
	if err != nil {
		t.Fatalf("opaque-origin offline queue: %v\n%s", err, out)
	}
}

func TestAutosaveHandlerAcceptsFormURLEncoded(t *testing.T) {
	var stored Answers
	h := AutosaveHandler(func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	body := "name=Avery&email=a%40example.com"
	req := httptest.NewRequest(http.MethodPost, "/autosave?echo=1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if stored.Get("name").StringValue() != "Avery" || stored.Get("email").StringValue() != "a@example.com" {
		t.Fatalf("stored = %#v", stored)
	}
}

func TestAutosaveOfflineReplayDoesNotWipe(t *testing.T) {
	dst := Answers{"name": {Value: "Avery"}}
	AutosavePatch{Offline: true, Field: "name"}.Apply(&dst)
	if dst.Get("name").StringValue() != "Avery" {
		t.Fatalf("offline empty patch wiped value: %#v", dst["name"])
	}
	AutosavePatch{Offline: true, Answers: Answers{"name": {Value: "Blake"}}}.Apply(&dst)
	if dst.Get("name").StringValue() != "Blake" {
		t.Fatalf("offline answers not applied: %#v", dst["name"])
	}
}

// TestAutosaveHXTriggerNameDoesNotErase is the Codex P1: HTMX always sends
// HX-Trigger-Name for the named control; that must not clear the just-saved value.
func TestAutosaveHXTriggerNameDoesNotErase(t *testing.T) {
	var stored Answers
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "name", Type: TypeText},
	}}}}}}
	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader("name=Avery&csrf=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Trigger-Name", "name")
	req.Header.Set("X-CSRF-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	if stored.Get("name").StringValue() != "Avery" {
		t.Fatalf("HX-Trigger-Name erased name: %#v", stored)
	}
	// "I don't know" checkbox triggers as name.__unknown — must not invent a junk key.
	req = httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader("name.__unknown=1&csrf=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Trigger-Name", "name.__unknown")
	req.Header.Set("X-CSRF-Token", "tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if _, junk := stored["name.__unknown"]; junk {
		t.Fatalf("junk key from HX-Trigger-Name: %#v", stored)
	}
	if !stored.Get("name").Unknown {
		t.Fatalf("unknown not applied: %#v", stored["name"])
	}
}

// TestAutosaveEndToEndHTMXShape renders a form and posts exactly what HTMX would
// send (headers included) for a normal field, an untouched masked field, and an
// offline replay; checks stored answers after each.
func TestAutosaveEndToEndHTMXShape(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "name", Type: TypeText, Label: "Name"},
		{ID: "ssn", Type: TypeMasked, Label: "SSN", SecretKey: "forms/ssn"},
	}}}}}}
	sealed := CipherPrefix + "already-sealed"
	stored := Answers{"ssn": {Value: sealed}}

	var buf strings.Builder
	if err := RenderScreen(&buf, form.Tracks[0].Screens[0], stored, AudiencePublic, RenderOptions{
		AutosaveURL: "/autosave",
		FormAction:  "/submit",
		CSRFToken:   "csrf-tok",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `hx-post="/autosave"`) || !strings.Contains(html, `data-autosave-name="name"`) {
		t.Fatalf("render missing autosave attrs: %s", html)
	}
	if !strings.Contains(html, `data-autosave-masked="1"`) || !strings.Contains(html, `data-autosave-name="ssn"`) {
		t.Fatalf("masked field must carry data-autosave-masked: %s", html)
	}
	if strings.Contains(html, `name="ssn" value="`) {
		t.Fatalf("unrevealed masked must render empty: %s", html)
	}

	h := AutosaveHandlerForm(form, func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})

	// 1) Normal field: form body + HX-Trigger-Name + X-CSRF-Token (HTMX shape).
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader("name=Avery&csrf=csrf-tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Trigger-Name", "name")
	req.Header.Set("X-CSRF-Token", "csrf-tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || stored.Get("name").StringValue() != "Avery" {
		t.Fatalf("normal field: code=%d stored=%#v", rec.Code, stored)
	}
	if stored.Get("ssn").StringValue() != sealed {
		t.Fatalf("normal field save must not touch ssn: %#v", stored["ssn"])
	}

	// 2) Untouched masked: empty ssn= as blur would post — must keep sealed value.
	req = httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader("ssn=&csrf=csrf-tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Trigger-Name", "ssn")
	req.Header.Set("X-CSRF-Token", "csrf-tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || stored.Get("ssn").StringValue() != sealed {
		t.Fatalf("empty masked wiped ssn: code=%d stored=%#v", rec.Code, stored)
	}

	// 3) Offline replay JSON shape (answers only, offline flag).
	req = httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader(
		`{"offline":true,"answers":{"name":{"value":"Blake"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Togavsu-Forms-Offline", "1")
	req.Header.Set("X-CSRF-Token", "csrf-tok")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || stored.Get("name").StringValue() != "Blake" {
		t.Fatalf("offline replay: code=%d stored=%#v", rec.Code, stored)
	}
	if stored.Get("ssn").StringValue() != sealed {
		t.Fatalf("offline replay must not touch ssn: %#v", stored["ssn"])
	}
}

func TestParseFormAnswersRepeating(t *testing.T) {
	got := ParseFormAnswers(map[string][]string{
		"kids[0].name": {"Ada"},
		"kids[1].name": {"Bea"},
	})
	rows := got.Get("kids").RowsValue()
	if len(rows) != 2 || rows[0]["name"].StringValue() != "Ada" || rows[1]["name"].StringValue() != "Bea" {
		t.Fatalf("repeating rows = %#v", got["kids"])
	}
}

func TestParseFormAnswersRejectsHugeRepeatingIndex(t *testing.T) {
	got := ParseFormAnswers(map[string][]string{
		"kids[0].name":          {"Ada"},
		"kids[2000000000].name": {"Boom"},
		"kids[100].name":        {"TooHigh"}, // MaxRepeatingIndex is 99
	})
	rows := got.Get("kids").RowsValue()
	if len(rows) != 1 || rows[0]["name"].StringValue() != "Ada" {
		t.Fatalf("huge index must not allocate; got %#v", got["kids"])
	}
}

func TestRendererToAutosaveHandler(t *testing.T) {
	screen := Screen{Fields: []Field{
		{ID: "name", Type: TypeText, Label: "Name"},
	}}
	var buf strings.Builder
	if err := RenderScreen(&buf, screen, Answers{}, AudiencePublic, RenderOptions{
		AutosaveURL: "/autosave",
		FormAction:  "/submit",
		CSRFToken:   "tok",
	}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	if !strings.Contains(html, `hx-post="/autosave"`) || !strings.Contains(html, `name="name"`) {
		t.Fatalf("render: %s", html)
	}
	var stored Answers
	h := AutosaveHandler(func(r *http.Request, patch AutosavePatch) (Answers, error) {
		patch.Apply(&stored)
		return stored, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/autosave", strings.NewReader("name=FromRender"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || stored.Get("name").StringValue() != "FromRender" {
		t.Fatalf("renderer→handler: code=%d stored=%#v", rec.Code, stored)
	}
}

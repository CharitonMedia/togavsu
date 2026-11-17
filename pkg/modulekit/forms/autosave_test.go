package forms

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

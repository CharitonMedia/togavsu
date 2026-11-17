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
}

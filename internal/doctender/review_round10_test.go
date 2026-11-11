package doctender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Opus checkpoint P2 (on 4c632e5): when the answer of a compared call fails, the
// fallback is not taken if it is the check model, so a check never compares a model
// with itself. Opus checkpoint P2 (on 0918dca): every other call keeps the PRD 8.7
// fallback to the secondary provider.
func TestFallbackNeverLandsOnTheCheckModel(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		asked = append(asked, body.Model)
		mu.Unlock()
		if body.Model == "gpt-primary" || body.Model == domain.LabelingModel {
			http.Error(w, "injected primary failure", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"answer from `+body.Model+`"}}]}`)
	}))
	defer srv.Close()
	reg, err := settings.New(settings.NewMemory(), quiet{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	admin := newAdmin(t)
	set := func(k, v string) {
		t.Helper()
		if _, err := reg.Set(ctx, k, v, admin); err != nil {
			t.Fatal(err)
		}
	}
	set("ai.openai_api_key", "o")
	set("ai.compatible_api_key", "c")
	set("ai.compatible_base_url", srv.URL)
	set("ai.primary_provider", "openai")
	set("ai.primary_strong_model", "gpt-primary")
	set("ai.primary_fast_model", "gpt-fast")
	set("ai.secondary_provider", "compatible")
	set("ai.secondary_strong_model", "gpt-secondary")
	r := &Router{Settings: reg, BaseURLs: map[string]string{ProviderOpenAI: srv.URL, ProviderCompatible: srv.URL}}

	// The default check model is the secondary's strong model, which is also the
	// fallback: a compared answer fails rather than coming from the check model.
	if resp, err := r.Call(ctx, Request{Task: TaskOCR, Compared: true, System: "read"}); err == nil {
		t.Fatalf("the compared answer fell back onto the check model: %+v", resp)
	}
	if resp, err := r.Call(ctx, Request{Task: TaskOCR, Check: true, Compared: true, System: "read"}); err != nil || resp.Model != "gpt-secondary" {
		t.Fatalf("check call: %+v %v", resp, err)
	}
	// A read that Tesseract checks, and labeling (checked by its citations), are not
	// compared with the check model: they fall back to the secondary provider.
	for _, req := range []Request{{Task: TaskOCR, System: "read"}, {Task: TaskLabeling, System: "label"}} {
		resp, err := r.Call(ctx, req)
		if err != nil || resp.Model != "gpt-secondary" || !resp.FellBack {
			t.Fatalf("%s did not fall back to the secondary provider: %+v %v", req.Task, resp, err)
		}
	}
	// With a separate check model, a compared answer falls back as before.
	set("ai.task_overrides", `{"`+TaskOCR+`":{"check_provider":"openai","check_model":"gpt-fast"}}`)
	resp, err := r.Call(ctx, Request{Task: TaskOCR, Compared: true, System: "read"})
	if err != nil || resp.Model != "gpt-secondary" || !resp.FellBack {
		t.Fatalf("fallback with a separate check model: %+v %v", resp, err)
	}
}

// Opus checkpoint P2 (on 4c632e5): a photo group whose matter closed during the quiet
// time spends nothing, and runs once the matter reopens.
func TestPhotoGroupWaitsWhileItsMatterIsClosed(t *testing.T) {
	e := newEnv(t)
	uploadPhotos(e)
	e.drain()
	e.dir.setStatus(e.open, "closed")
	before := e.ai.callCount("grouping")
	e.clock.advance(10 * minuteUnit)
	e.drain()
	if got := e.ai.callCount("grouping"); got != before {
		t.Fatalf("a closed matter's photo group made %d grouping calls", got-before)
	}
	held, err := e.svc.jobsWhere(e.ctx, `kind = ? AND status = ?`, kindGroup, StatusMatterClosed)
	if err != nil || len(held) == 0 {
		t.Fatalf("no group job is held for the closed matter: %v", err)
	}
	e.dir.setStatus(e.open, "open")
	if err := e.svc.MatterReopened(e.ctx, e.open); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if e.ai.callCount("grouping") == before {
		t.Fatal("reopening the matter did not run the photo group")
	}
}

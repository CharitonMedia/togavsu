package doctender

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
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

// Opus checkpoint P2 (on 8d2551b): with a check model set for the task, both sides of
// a comparison can fail and fall back to the same secondary model. The comparison is
// refused rather than passed by one model agreeing with itself.
func TestBothSidesFallingBackToOneModelIsNotACheck(t *testing.T) {
	e := newEnv(t)
	e.set("ai.compatible_api_key", "test-openai-key")
	e.set("ai.compatible_base_url", e.ai.srv.URL)
	e.set("ai.secondary_provider", "compatible")
	e.set("ai.secondary_strong_model", "gpt-secondary")
	e.set("ai.task_overrides", `{"`+TaskGrouping+`": {"check_provider": "openai", "check_model": "gpt-test-check"}}`)
	e.ai.set(func(f *fakeAI) { f.failGrouping = map[string]bool{"gpt-test-strong": true, "gpt-test-check": true} })
	uploadPhotos(e)
	e.drain()
	e.clock.advance(10 * minuteUnit)
	e.drain()
	groups, err := e.svc.jobsWhere(e.ctx, `kind = ?`, kindGroup)
	if err != nil || len(groups) == 0 {
		t.Fatalf("no group job: %v", err)
	}
	for _, g := range groups {
		if g.Status == StatusPublished {
			t.Fatalf("group job %s was published on one model agreeing with itself", g.ID)
		}
		for _, u := range e.unitsFor(g.ID) {
			if u.Name != "grouping" {
				continue
			}
			if u.Status == unitPassed {
				t.Fatalf("the grouping unit passed with both sides from one model: %+v", u)
			}
			if !strings.Contains(u.LastError, "both came from compatible/gpt-secondary") {
				t.Fatalf("grouping unit error %q, want the self-comparison refusal", u.LastError)
			}
		}
	}
}

// Opus checkpoint P2 (on 3d44fd8): settling a version's last open review item moves
// its job out of review, so the file reads as processed.
func TestSettlingTheLastItemMarksTheFileProcessed(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(fa *fakeAI) {
		lowName(fa)
		fa.categoryConfidence = 0.3
	})
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	items := e.openExceptions(f.ID)
	if len(items) != 1 || items[0].Kind != "low_confidence" {
		t.Fatalf("items %+v, want one low-confidence item", items)
	}
	st, err := e.svc.Status(e.ctx, e.attorney, f.ID)
	if err != nil || st.Label != "Needs review" {
		t.Fatalf("status before review %+v %v, want Needs review", st, err)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job status %q after the last item was settled, want %q", j.Status, StatusPublished)
	}
	st, err = e.svc.Status(e.ctx, e.attorney, f.ID)
	if err != nil || st.Label != "Processed" {
		t.Fatalf("status after review %+v %v, want Processed", st, err)
	}
}

// Opus checkpoint P2 (on 42e2cbf): settling the item of a part that failed its check,
// or of a file stopped at its first check, does not make the file read as processed.
func TestSettlingAnUnresolvedPartLeavesTheFileInReview(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	e.set("doctender.max_attempts", "2")
	e.ai.set(func(f *fakeAI) { f.failOCR["PAGE 2 OF 3"] = 100 })
	e.set("ai.primary_fast_model", "gpt-test-strong-check")
	scan := e.upload(e.staff, e.open, "scan.pdf", "application/pdf", scannedPDF(t, 3))
	broken := e.upload(e.staff, e.open, "damaged.pdf", "application/pdf", damagedPDF())
	e.drain()
	for _, c := range []struct {
		f      files.FileView
		kind   string
		action string
	}{{scan, "extraction_failed", "accept"}, {broken, "damaged", "keep"}} {
		ex := e.openExceptions(c.f.ID)
		if len(ex) != 1 || ex[0].Kind != c.kind {
			t.Fatalf("%s: items %+v, want one %s item", c.f.Name, ex, c.kind)
		}
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, ex[0].ID, Resolve{Action: c.action}); err != nil {
			t.Fatalf("%s: %s: %v", c.f.Name, c.action, err)
		}
		if j := e.jobFor(c.f.ID); j.Status != StatusException {
			t.Fatalf("%s: job status %q after %s, want it left in review", c.f.Name, j.Status, c.action)
		}
		st, err := e.svc.Status(e.ctx, e.attorney, c.f.ID)
		if err != nil || st.Label == "Processed" {
			t.Fatalf("%s: status %+v %v after %s, want it not to read Processed", c.f.Name, st, err, c.action)
		}
	}
	_, md := e.markdown(e.attorney, scan.ID)
	if !strings.Contains(md, "[unresolved: page 2]") {
		t.Fatalf("markdown lost its unresolved page:\n%s", md)
	}
}

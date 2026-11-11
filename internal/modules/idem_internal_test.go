package modules

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// A slow idempotent write blocks only repeats of its own key, not other writes
// (Codex checkpoint finding 4 on PR #38), and a repeat still replays without a second
// effect.
func TestIdempotencyLocksPerKey(t *testing.T) {
	h := &Handler{Auth: func(*http.Request) (Caller, error) {
		return Caller{Principal: domain.Principal{UserID: "u1"}}, nil
	}, idem: map[string]*idemEntry{}}
	release := make(chan struct{})
	started := make(chan struct{})
	var runs atomic.Int32
	slow := h.write("", func(w http.ResponseWriter, r *http.Request, _ Caller) {
		if runs.Add(1) == 1 {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	call := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/modules/x/uninstall", nil)
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		slow(rec, req)
		return rec
	}
	var wg sync.WaitGroup
	var first, repeat *httptest.ResponseRecorder
	wg.Add(1)
	go func() { defer wg.Done(); first = call("k1") }()
	<-started
	wg.Add(1)
	go func() { defer wg.Done(); repeat = call("k1") }()

	other := make(chan *httptest.ResponseRecorder, 1)
	go func() { other <- call("k2") }()
	select {
	case rec := <-other:
		if rec.Code != http.StatusCreated {
			t.Fatalf("other key: %d", rec.Code)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a different key waited on the slow request")
	}
	close(release)
	wg.Wait()
	if first.Code != http.StatusCreated || repeat.Code != http.StatusCreated || repeat.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("first %d, repeat %d replayed=%q", first.Code, repeat.Code, repeat.Header().Get("Idempotent-Replayed"))
	}
	if runs.Load() != 2 {
		t.Fatalf("handler ran %d times for two keys", runs.Load())
	}
}

// A server error isn't kept, so the same key runs again.
func TestIdempotencyRetriesServerError(t *testing.T) {
	h := &Handler{Auth: func(*http.Request) (Caller, error) {
		return Caller{Principal: domain.Principal{UserID: "u1"}}, nil
	}, idem: map[string]*idemEntry{}}
	var runs atomic.Int32
	fn := h.write("", func(w http.ResponseWriter, r *http.Request, _ Caller) {
		if runs.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	for i, want := range []int{http.StatusBadGateway, http.StatusOK, http.StatusOK} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/modules/x/enable", nil)
		req.Header.Set("Idempotency-Key", "same")
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != want {
			t.Fatalf("call %d: %d, want %d", i, rec.Code, want)
		}
	}
	if runs.Load() != 2 {
		t.Fatalf("handler ran %d times; the 502 should be retried once and the 200 replayed", runs.Load())
	}
}

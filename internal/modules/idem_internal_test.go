package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if first.Code != http.StatusCreated || repeat.Code != http.StatusCreated || repeat.Header().Get(IdempotentReplayHeader) != "true" {
		t.Fatalf("first %d, repeat %d replayed=%q", first.Code, repeat.Code, repeat.Header().Get(IdempotentReplayHeader))
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

// Contract idempotencyKey (Codex checkpoint 2 on PR #38): the same key and body replay
// the first response with its own headers; the same key with a different body is a 409
// idempotency_conflict without running the handler.
func TestIdempotencyBodyConflict(t *testing.T) {
	h := &Handler{Auth: func(*http.Request) (Caller, error) {
		return Caller{Principal: domain.Principal{UserID: "u1"}}, nil
	}, idem: map[string]*idemEntry{}}
	var runs atomic.Int32
	var bodies []string
	fn := h.write("", func(w http.ResponseWriter, r *http.Request, _ Caller) {
		runs.Add(1)
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b["data"])
		writeErr(w, invalid("confirmation", "type the module id"))
	})
	call := func(key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/modules/x/uninstall", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		fn(rec, req)
		return rec
	}
	first := call("k", `{"data":"keep"}`)
	if first.Code != http.StatusBadRequest || first.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("first: %d %q", first.Code, first.Header().Get("Content-Type"))
	}
	if len(bodies) != 1 || bodies[0] != "keep" {
		t.Fatalf("the handler didn't see the body after hashing: %v", bodies)
	}
	same := call("k", `{"data":"keep"}`)
	if same.Code != http.StatusBadRequest || same.Header().Get(IdempotentReplayHeader) != "true" ||
		same.Header().Get("Content-Type") != "application/problem+json" || same.Body.String() != first.Body.String() {
		t.Fatalf("replay: %d %v %s", same.Code, same.Header(), same.Body.String())
	}
	diff := call("k", `{"data":"delete"}`)
	var problem domain.APIError
	_ = json.Unmarshal(diff.Body.Bytes(), &problem)
	if diff.Code != http.StatusConflict || problem.Code != "idempotency_conflict" || diff.Header().Get(IdempotentReplayHeader) != "" {
		t.Fatalf("different body: %d %s", diff.Code, diff.Body.String())
	}
	if runs.Load() != 1 {
		t.Fatalf("handler ran %d times; a replay and a conflict must not run it", runs.Load())
	}
	// The same body under another key runs; a key over the contract's 200 characters is refused.
	if other := call("k2", `{"data":"delete"}`); other.Code != http.StatusBadRequest || other.Header().Get(IdempotentReplayHeader) != "" || runs.Load() != 2 {
		t.Fatalf("another key: %d runs=%d", other.Code, runs.Load())
	}
	if long := call(strings.Repeat("k", limits().IdemKeyChars+1), `{}`); long.Code != http.StatusBadRequest || runs.Load() != 2 {
		t.Fatalf("over-long key: %d", long.Code)
	}
}

// A replayed 204 carries no JSON content type and no body.
func TestIdempotencyReplaysNoContent(t *testing.T) {
	h := &Handler{Auth: func(*http.Request) (Caller, error) {
		return Caller{Principal: domain.Principal{UserID: "u1"}}, nil
	}, idem: map[string]*idemEntry{}}
	fn := h.write("", func(w http.ResponseWriter, r *http.Request, _ Caller) { w.WriteHeader(http.StatusNoContent) })
	for i := range 2 {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/trusted-signing-keys/a", nil)
		req.Header.Set("Idempotency-Key", "d")
		rec := httptest.NewRecorder()
		fn(rec, req)
		if rec.Code != http.StatusNoContent || rec.Header().Get("Content-Type") != "" || rec.Body.Len() != 0 {
			t.Fatalf("call %d: %d %q %q", i, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		if (i == 1) != (rec.Header().Get(IdempotentReplayHeader) == "true") {
			t.Fatalf("call %d replay header: %q", i, rec.Header().Get(IdempotentReplayHeader))
		}
	}
}

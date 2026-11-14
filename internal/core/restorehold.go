package core

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// restoreHold keeps users out of a core that may have been started by a restore
// whose file bytes are not written back yet. The supervisor writes those bytes
// through this core once it is healthy, and if that write fails it puts the
// previous database back from the safety backup; anything a user saved in
// between would be lost with it. So a core that talks to a supervisor starts
// held, serves only its health check and the supervisor's file import, and
// opens once the supervisor says no restore holds it (GET /v1/restores/hold).
// It fails closed: until the supervisor answers, the core stays held. A core
// with no supervisor configured never holds.
type restoreHold struct {
	held   atomic.Bool
	b      *backups
	stop   context.CancelFunc
	done   chan struct{}
	closed sync.Once
}

func newRestoreHold(b *backups) *restoreHold {
	h := &restoreHold{b: b}
	h.held.Store(b != nil && b.secret != "")
	return h
}

// watch calls open once the hold is over: at once without a supervisor,
// otherwise when the supervisor first answers that nothing holds the core.
// A held core asks again every poll.
func (h *restoreHold) watch(poll time.Duration, open func()) {
	if !h.held.Load() {
		open()
		return
	}
	// The first answer is awaited here, before the core listens, so a core that
	// nothing holds opens without a held moment.
	hold, err := h.ask(context.Background())
	if err == nil && !hold {
		h.held.Store(false)
		open()
		return
	}
	ctx, stop := context.WithCancel(context.Background())
	h.stop, h.done = stop, make(chan struct{})
	go func() {
		defer close(h.done)
		logged := false
		for {
			if err != nil && !logged {
				log.Printf("core: restore hold: supervisor not answering, users stay out until it does: %v", err)
				logged = true
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(poll):
			}
			hold, err = h.ask(ctx)
			if ctx.Err() != nil {
				return
			}
			if err == nil && !hold {
				h.held.Store(false)
				open()
				return
			}
		}
	}()
}

func (h *restoreHold) ask(ctx context.Context) (bool, error) {
	res, err := h.b.send(ctx, http.MethodGet, "/v1/restores/hold", "", nil)
	if err != nil {
		return true, err
	}
	defer res.Body.Close()
	var body struct {
		Hold *bool `json:"hold"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<10)).Decode(&body); err != nil {
		return true, err
	}
	if body.Hold == nil {
		return true, errHoldUnanswered
	}
	return *body.Hold, nil
}

type holdError string

func (e holdError) Error() string { return string(e) }

const errHoldUnanswered = holdError("the supervisor's hold answer has no hold field")

func (h *restoreHold) close() {
	if h == nil {
		return
	}
	h.closed.Do(func() {
		if h.stop != nil {
			h.stop()
			<-h.done
		}
	})
}

// holdGate answers every request with 503 restore_in_progress while the hold
// stands, except the health check (the supervisor waits on it before writing the
// bytes) and the file import from the supervisor itself.
func (a *App) holdGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.hold == nil || !a.hold.held.Load() {
			next.ServeHTTP(w, r)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/health" || r.URL.Path == "/healthz":
			next.ServeHTTP(w, r)
			return
		case r.URL.Path == fileBytesPath && a.backups.supervisor(r):
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", "5")
		w.Header().Set("Cache-Control", "no-store")
		const msg = "Togavsu is finishing a restore and will open when the restored files are back in place."
		if !strings.Contains(r.Header.Get("Accept"), "text/html") || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "restore_in_progress", "message": msg})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="5"><title>Togavsu</title><p>`+msg+`</p>`)
	})
}

package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const (
	inboundWebhookMaxBytesKey      = "modules.inbound_webhook_max_bytes"
	inboundWebhookRatePerMinuteKey = "modules.inbound_webhook_rate_per_minute"
)

// inboundRate limits POST /hooks/{module}/{hook} to modules.inbound_webhook_rate_per_minute.
// ponytail: ceiling: the counter lives in this process. Upgrade path: store hits in
// Postgres so every core process shares them. Trigger: more than one core process
// serves inbound webhooks.
type inboundRate struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (s *Service) inboundHits() *inboundRate {
	s.inboundOnce.Do(func() {
		s.inbound = &inboundRate{hits: map[string][]time.Time{}}
	})
	return s.inbound
}

func (r *inboundRate) allow(key string, limit int, now time.Time, window time.Duration) (retryAfter time.Duration, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []time.Time
	for _, at := range r.hits[key] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		r.hits[key] = kept
		wait := window - now.Sub(kept[0])
		if wait < time.Second {
			wait = time.Second
		}
		return wait, false
	}
	r.hits[key] = append(kept, now)
	return 0, true
}

func (h *Handler) inboundWebhook(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	hookID := r.PathValue("hook_id")
	in, err := h.Svc.Installed(r.Context(), moduleID)
	if err != nil || !in.Enabled || h.Svc.d.Runtime == nil {
		writeErr(w, domain.NotFound("module"))
		return
	}
	hook, ok := in.Manifest.InboundWebhook(hookID)
	if !ok {
		writeErr(w, domain.NotFound("hook"))
		return
	}

	maxBytes, err := h.Svc.intSetting(r.Context(), inboundWebhookMaxBytesKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if maxBytes <= 0 {
		maxBytes = domain.InboundWebhookMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(&domain.APIError{Code: "payload_too_large", Message: "request body exceeds modules.inbound_webhook_max_bytes"})
			return
		}
		writeErr(w, domain.Invalid("body", "could not read the request body"))
		return
	}

	rate, err := h.Svc.intSetting(r.Context(), inboundWebhookRatePerMinuteKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if rate <= 0 {
		rate = domain.InboundWebhookRatePerMin
	}
	if wait, ok := h.Svc.inboundHits().allow(moduleID+"/"+hookID, rate, h.Svc.now(), time.Minute); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		writeErr(w, domain.RateLimited())
		return
	}

	target, err := url.Parse(h.Svc.d.Runtime.BaseURL(moduleID))
	if err != nil {
		writeErr(w, domain.NotFound("module"))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = hook.Path
			pr.Out.URL.RawPath = ""
			pr.Out.Body = io.NopCloser(bytes.NewReader(body))
			pr.Out.ContentLength = int64(len(body))
			pr.Out.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del(ContextHeader)
			pr.Out.Header.Del("X-Csrf-Token")
			pr.Out.Header.Set("Content-Length", strconv.Itoa(len(body)))
			pr.Out.Header.Set("X-Togavsu-Module", moduleID)
			pr.Out.Header.Set("X-Togavsu-Inbound-Hook", hookID)
		},
		ModifyResponse: func(resp *http.Response) error {
			// Same origin protections as /m/ (CORE-09): a public hook must not plant cookies
			// on the core domain or run module HTML as the core origin in a staff browser.
			resp.Header.Del("Set-Cookie")
			resp.Header.Add("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation")
			if err := h.Svc.audit(r.Context(), domain.SystemPrincipal(), moduleID, "inbound_webhook", "",
				fmt.Sprintf("%s %d", hookID, resp.StatusCode), "inbound webhook delivery"); err != nil {
				log.Printf("modules: inbound webhook audit %s/%s: %v", moduleID, hookID, err)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("modules: inbound webhook to %s/%s: %v", moduleID, hookID, err)
			http.Error(w, "module "+moduleID+" is not answering", http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

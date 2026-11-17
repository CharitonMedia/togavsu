package modules

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const (
	publicPageRateKey     = "modules.public_page_rate_per_minute"
	publicPageMaxBytesKey = "modules.public_page_max_bytes"
)

// publicRate limits /p/{id}/ by IP (CORE-32).
// ponytail: ceiling: counters live in this process. Upgrade path: shared Postgres hits.
// Trigger: more than one core process serves public pages.
type publicRate struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (s *Service) publicHits() *publicRate {
	s.publicOnce.Do(func() {
		s.public = &publicRate{hits: map[string][]time.Time{}}
	})
	return s.public
}

func (r *publicRate) allow(key string, limit int, now time.Time, window time.Duration) (retryAfter time.Duration, ok bool) {
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

// clientIP prefers X-Forwarded-For's first hop, then RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	if host != "" {
		return host
	}
	return r.RemoteAddr
}

// publicPage proxies /p/{module_id}/... with no sign-in (CORE-32).
func (h *Handler) publicPage(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	in, err := h.Svc.Installed(r.Context(), moduleID)
	if err != nil || !in.Enabled || h.Svc.d.Runtime == nil {
		writeErr(w, domain.NotFound("module"))
		return
	}
	if !in.Approved["public_pages:serve"] {
		writeErr(w, &domain.APIError{Code: "scope_denied", Message: "module lacks the public_pages:serve scope"})
		return
	}
	prefix := "/p/" + moduleID
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	if rel == "" {
		rel = "/"
	}
	if !strings.HasPrefix(rel, "/") {
		rel = "/" + rel
	}
	if !in.Manifest.PublicPagePath(rel) {
		writeErr(w, domain.NotFound("page"))
		return
	}

	maxBytes, err := h.Svc.intSetting(r.Context(), publicPageMaxBytesKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if maxBytes <= 0 {
		maxBytes = domain.PublicPageMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"code":"payload_too_large","message":"request body exceeds modules.public_page_max_bytes"}`))
			return
		}
		writeErr(w, domain.Invalid("body", "could not read the request body"))
		return
	}

	rate, err := h.Svc.intSetting(r.Context(), publicPageRateKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if rate <= 0 {
		rate = domain.PublicPageRatePerMin
	}
	ip := clientIP(r)
	if wait, ok := h.Svc.publicHits().allow(ip+"/"+moduleID, rate, h.Svc.now(), time.Minute); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
		writeErr(w, domain.RateLimited())
		return
	}

	// CSRF: set cookie on GET; require match on unsafe methods.
	csrfCookie, _ := r.Cookie(PublicCSRFCookie)
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if csrfCookie == nil || csrfCookie.Value == "" {
			tok, err := IssuePublicCSRF()
			if err != nil {
				writeErr(w, err)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: PublicCSRFCookie, Value: tok, Path: "/p/" + moduleID,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
			})
		}
	default:
		submitted := r.Header.Get(PublicCSRFHeader)
		if submitted == "" {
			submitted = r.FormValue("csrf")
		}
		cookieVal := ""
		if csrfCookie != nil {
			cookieVal = csrfCookie.Value
		}
		if err := CheckPublicCSRF(cookieVal, submitted); err != nil {
			writeErr(w, err)
			return
		}
	}

	row, err := h.Svc.row(r.Context(), moduleID)
	if err != nil {
		writeErr(w, err)
		return
	}
	secret, err := h.Svc.open(row.WebhookSecret)
	if err != nil {
		writeErr(w, err)
		return
	}
	step, err := IssueStepToken([]byte(secret), moduleID, 30*time.Minute, h.Svc.now())
	if err != nil {
		writeErr(w, err)
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
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = rel
			req.Host = target.Host
			req.Header.Del("Cookie")
			req.Header.Del("Authorization")
			req.Header.Set("X-Togavsu-Module", moduleID)
			req.Header.Set(PublicStepHeader, step)
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/html") {
				resp.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox")
			}
			return nil
		},
	}
	proxy.ServeHTTP(w, r)
}

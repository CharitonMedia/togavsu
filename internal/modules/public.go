package modules

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
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
	r.evict(now, window)
	return 0, true
}

// evict drops keys whose hits all fall outside window (caller holds mu).
func (r *publicRate) evict(now time.Time, window time.Duration) {
	if len(r.hits) < 64 {
		return
	}
	for k, times := range r.hits {
		var kept []time.Time
		for _, at := range times {
			if now.Sub(at) < window {
				kept = append(kept, at)
			}
		}
		if len(kept) == 0 {
			delete(r.hits, k)
		} else {
			r.hits[k] = kept
		}
	}
}

// clientIP returns the client address for public-page rate limits (CORE-32).
// RemoteAddr is parsed with SplitHostPort (IPv6-safe). X-Forwarded-For is trusted
// only when the immediate peer looks like an on-host/docker reverse proxy; the
// right-most hop is the one the proxy appended.
func clientIP(r *http.Request) string {
	host := remoteHost(r.RemoteAddr)
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" && proxyPeer(host) {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}
	if host != "" {
		return host
	}
	return r.RemoteAddr
}

func remoteHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(addr)
}

func proxyPeer(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func publicCookieSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// submittedPublicCSRF reads the CSRF token from the header or from a buffered form body
// (the body may already have been consumed for MaxBytesReader).
func submittedPublicCSRF(r *http.Request, body []byte) string {
	if v := r.Header.Get(PublicCSRFHeader); v != "" {
		return v
	}
	ct := r.Header.Get("Content-Type")
	media, params, _ := mime.ParseMediaType(ct)
	switch media {
	case "multipart/form-data":
		mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			name := part.FormName()
			raw, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			_ = part.Close()
			if name == "csrf" {
				return string(raw)
			}
		}
	default:
		// application/x-www-form-urlencoded and unknown text forms.
		vals, err := url.ParseQuery(string(body))
		if err == nil {
			if v := vals.Get("csrf"); v != "" {
				return v
			}
		}
	}
	return ""
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

	// Rate before body read and before OPTIONS preflight (CORE-32 abuse protection).
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

	// Opaque-origin HTMX/fetch preflight (CSP sandbox without allow-same-origin).
	if answerOpaqueOriginCORS(w, r) {
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
	sec := []byte(secret)

	// CSRF: signed token (no allow-same-origin). Cookie is optional defense-in-depth;
	// opaque-origin HTMX may omit it, so MAC verification is authoritative.
	csrfCookie, _ := r.Cookie(PublicCSRFCookie)
	csrfTok := ""
	if csrfCookie != nil {
		csrfTok = csrfCookie.Value
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		if csrfTok == "" || CheckSignedPublicCSRF(sec, moduleID, csrfTok, csrfTok, h.Svc.now()) != nil {
			tok, err := IssueSignedPublicCSRF(sec, moduleID, 0, h.Svc.now())
			if err != nil {
				writeErr(w, err)
				return
			}
			csrfTok = tok
			http.SetCookie(w, &http.Cookie{
				Name: PublicCSRFCookie, Value: tok, Path: "/p/" + moduleID,
				HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: publicCookieSecure(r),
			})
		}
	default:
		submitted := submittedPublicCSRF(r, body)
		if err := CheckSignedPublicCSRF(sec, moduleID, csrfTok, submitted, h.Svc.now()); err != nil {
			writeErr(w, err)
			return
		}
	}

	// Core-only key: modules cannot mint public step credentials (CORE-32).
	step, err := IssueStepToken(h.Svc.PublicStepKey(), moduleID, 30*time.Minute, h.Svc.now())
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
	passCSRF := csrfTok
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
			if passCSRF != "" {
				req.Header.Set(PublicCSRFPassHeader, passCSRF)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			// Always sandbox, including SVG/XML and empty Content-Type (sniffed as HTML).
			// Same rule as /hooks/ and /m/ when strip != "": never allow-same-origin.
			resp.Header.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox")
			applyOpaqueOriginCORS(resp.Header, r)
			return nil
		},
	}
	proxy.ServeHTTP(w, r)
}

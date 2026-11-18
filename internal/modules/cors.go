package modules

import (
	"net/http"
	"strings"
)

// opaqueOriginCORSHeaders are the non-safelisted headers HTMX and the forms kit send
// from CSP-sandboxed /p/ and /m/ pages (Origin: null). Without a preflight answer the
// browser blocks autosave and offline replay (CORE-33).
const opaqueOriginCORSHeaders = "Content-Type, X-CSRF-Token, HX-Request, HX-Current-URL, HX-Trigger, HX-Trigger-Name, HX-Target, HX-Prompt, HX-History-Restore-Request, X-Togavsu-Forms-Offline, X-Togavsu-Public-CSRF, X-Togavsu-Public-Step"

// isOpaqueOrigin reports a browser Origin produced by a CSP sandbox without
// allow-same-origin (the string "null", not an https origin).
func isOpaqueOrigin(origin string) bool {
	return strings.EqualFold(strings.TrimSpace(origin), "null")
}

// applyOpaqueOriginCORS sets ACAO for Origin: null on proxied /p/ and /m/ responses
// so credentialed HTMX/fetch from sandboxed pages can complete.
func applyOpaqueOriginCORS(h http.Header, r *http.Request) {
	if r == nil || !isOpaqueOrigin(r.Header.Get("Origin")) {
		return
	}
	h.Set("Access-Control-Allow-Origin", "null")
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Set("Access-Control-Allow-Headers", opaqueOriginCORSHeaders)
	h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	h.Add("Vary", "Origin")
}

// answerOpaqueOriginCORS handles an OPTIONS preflight from an opaque-origin document.
// Returns true when the request was fully answered.
func answerOpaqueOriginCORS(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodOptions || !isOpaqueOrigin(r.Header.Get("Origin")) {
		return false
	}
	applyOpaqueOriginCORS(w.Header(), r)
	w.WriteHeader(http.StatusNoContent)
	return true
}

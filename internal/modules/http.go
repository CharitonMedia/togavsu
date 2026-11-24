package modules

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/frame"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// Caller is an authenticated request: the principal, the bearer token's scopes (nil for
// a browser session) and whether a bearer token was presented. The core's identity
// layer supplies it, including CSRF for cookie writes.
type Caller struct {
	Principal domain.Principal
	Scopes    []string
	Bearer    bool
}

// Handler serves the module API, the frame pages and the /m/{id}/ proxy.
type Handler struct {
	Svc      *Service
	Auth     func(*http.Request) (Caller, error)
	FirmName func(ctx context.Context) string

	// idemMu guards only the idem map; no handler runs under it.
	idemMu sync.Mutex
	idem   map[string]*idemEntry
}

// idemEntry is one Idempotency-Key: done closes when the first request finishes,
// fingerprint is the SHA-256 of its content type and body, and status, header and body
// are its response.
type idemEntry struct {
	done        chan struct{}
	fingerprint string
	status      int
	header      http.Header
	body        []byte
}

// NewHandler returns the module routes.
func NewHandler(svc *Service, auth func(*http.Request) (Caller, error)) *Handler {
	return &Handler{Svc: svc, Auth: auth, idem: map[string]*idemEntry{}}
}

// Routes is the module mux. The core mounts it beside the other domains and wraps its
// own handler in Svc.Router (domain replacement) and Include (include= reads).
func (h *Handler) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/modules", h.scoped("modules:read", h.list))
	m.HandleFunc("POST /api/v1/modules", h.write("modules:write", h.install))
	m.HandleFunc("GET /api/v1/modules/{module_id}", h.scoped("modules:read", h.get))
	m.HandleFunc("POST /api/v1/modules/{module_id}/permissions", h.write("modules:write", h.permissions))
	m.HandleFunc("POST /api/v1/modules/{module_id}/enable", h.write("modules:write", h.enable))
	m.HandleFunc("POST /api/v1/modules/{module_id}/disable", h.write("modules:write", h.disable))
	m.HandleFunc("POST /api/v1/modules/{module_id}/uninstall", h.write("modules:write", h.uninstall))
	m.HandleFunc("POST /api/v1/modules/{module_id}/update", h.write("modules:write", h.update))
	m.HandleFunc("GET /api/v1/modules/{module_id}/settings", h.scoped("", h.getSettings))
	m.HandleFunc("PUT /api/v1/modules/{module_id}/own-settings", h.write("module_settings:write", h.putSettings))
	m.HandleFunc("PUT /api/v1/modules/{module_id}/settings", h.write("modules:write", h.putSettings))
	// {name...} so names may contain / (qbo/client_secret, account/<id>/kind).
	m.HandleFunc("PUT /api/v1/modules/{module_id}/secrets/{name...}", h.write("module_secrets:write", h.putSecret))
	m.HandleFunc("GET /api/v1/modules/{module_id}/secrets/{name...}", h.scoped("module_secrets:write", h.getSecret))
	m.HandleFunc("DELETE /api/v1/modules/{module_id}/secrets/{name...}", h.write("module_secrets:write", h.deleteSecret))
	m.HandleFunc("POST /api/v1/modules/{module_id}/context-tokens", h.write("", h.contextToken))
	m.HandleFunc("POST /api/v1/modules/{module_id}/context-tokens/verify", h.scoped("", h.verifyContext))
	m.HandleFunc("POST /api/v1/modules/{module_id}/device-context-tokens", h.write("modules:read", h.exchangeDevice))
	m.HandleFunc("GET /api/v1/modules/{module_id}/views/{view}", h.scoped("", h.readView))
	// CORE-31 link admin + CORE-34 banners (literal path segments; safe beside {module_id}).
	m.HandleFunc("GET /api/v1/modules/contract-links", h.scoped("modules:read", h.listContractLinks))
	m.HandleFunc("POST /api/v1/modules/contract-links", h.write("modules:write", h.linkContract))
	m.HandleFunc("DELETE /api/v1/modules/contract-links/{link_id}", h.write("modules:write", h.unlinkContract))
	m.HandleFunc("GET /api/v1/modules/banners", h.scoped("modules:read", h.listBannersJSON))
	m.HandleFunc("PUT /api/v1/modules/banner-order", h.write("modules:write", h.putBannerOrder))
	m.HandleFunc("POST /api/v1/me/module-devices", h.write("", h.createDevice))
	m.HandleFunc("GET /api/v1/me/module-devices", h.scoped("profile:read", h.listDevices))
	m.HandleFunc("DELETE /api/v1/me/module-devices/{device_id}", h.write("profile:write", h.revokeDevice))
	m.HandleFunc("GET /api/v1/domain-handlers", h.scoped("modules:read", h.listDomains))
	m.HandleFunc("PUT /api/v1/domain-handlers/{domain}", h.write("modules:write", h.switchDomain))
	m.HandleFunc("GET /api/v1/webhook-deliveries", h.scoped("webhooks:read", h.deliveries))
	m.HandleFunc("POST /api/v1/ai/calls", h.write("ai:use", h.aiCall))
	m.HandleFunc("GET /api/v1/ai/calls", h.scoped("ai:admin", h.aiCalls))
	m.HandleFunc("GET /modules/{module_id}/navigate", h.scoped("", h.navigate))
	m.HandleFunc("GET /modules/{module_id}/tabs/{tab}", h.scoped("", h.tabPage))
	m.HandleFunc("GET /modules/{module_id}/tabs/{tab}/context-token", h.scoped("", h.staffTabContextToken))
	m.HandleFunc("GET /modules/{module_id}/widgets/{widget}", h.scoped("", h.widgetPage))
	m.HandleFunc("GET /modules/{module_id}/widgets/{widget}/context-token", h.scoped("", h.staffWidgetContextToken))
	m.HandleFunc("GET /modules/{module_id}/contact-phone-items/{item}", h.scoped("", h.phoneItemPage))
	m.HandleFunc("GET /modules/{module_id}/matter-items/{item}", h.scoped("", h.matterItemPage))
	m.HandleFunc("GET /modules/{module_id}/matter-items/{item}/context-token", h.scoped("", h.staffMatterItemContextToken))
	m.HandleFunc("GET /modules/{module_id}/banner", h.scoped("", h.bannerPage))
	m.HandleFunc("GET /modules/{module_id}/device-connect", h.scoped("", h.deviceConnectGet))
	m.HandleFunc("POST /modules/{module_id}/device-connect", h.write("", h.deviceConnectPost))
	m.HandleFunc("/m/{module_id}/", h.frame)
	// CORE-07: public provider webhook intake. POST only; undeclared hooks 404.
	m.HandleFunc("POST /hooks/{module_id}/{hook_id}", h.inboundWebhook)
	// CORE-32: public module pages, no sign-in (trailing slash matches the subtree).
	m.HandleFunc("/p/{module_id}/", h.publicPage)
	m.HandleFunc("/p/{module_id}", h.publicPage)
	// CORE-10
	m.HandleFunc("GET /portal", h.scoped("", h.portalHome))
	m.HandleFunc("GET /portal/{module_id}/{tab_id}", h.scoped("", h.portalTab))
	m.HandleFunc("GET /portal/{module_id}/{tab_id}/context-token", h.scoped("", h.portalContextToken))
	m.HandleFunc("GET /api/v1/nav/client-context", h.scoped("profile:read", h.clientContextJSON))
	return m
}

// ContractRoutes is CORE-31's call and event-push mux. It is mounted ahead of Routes so
// POST /api/v1/modules/contracts/{contract}/… does not conflict with
// POST /api/v1/modules/{module_id}/context-tokens/verify on one ServeMux.
func (h *Handler) ContractRoutes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/modules/contracts/{contract}/providers", h.scoped("contracts:call", h.listContractProviders))
	m.HandleFunc("POST /api/v1/modules/contracts/{contract}/events", h.write("contracts:provide", h.pushContractEvent))
	cachedCall := h.write("contracts:call", h.callContract)
	processorCall := h.scoped("contracts:call", h.callContract)
	m.HandleFunc("POST /api/v1/modules/contracts/{contract}/{operation}", func(w http.ResponseWriter, r *http.Request) {
		// The processor receipt owns financial idempotency. Every retry must
		// recheck current access and return its current result, so a transport
		// cache cannot retain pending after recovery or approval after unlink.
		if strings.TrimSpace(r.PathValue("contract")) == ProcessorPaymentContract {
			processorCall(w, r)
			return
		}
		cachedCall(w, r)
	})
	return m
}

// --- plumbing ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func statusOf(err error) int {
	var me *marketplace.Error
	if errors.As(err, &me) {
		return me.HTTPStatus()
	}
	var api *domain.APIError
	if !errors.As(err, &api) {
		return http.StatusInternalServerError
	}
	switch api.Code {
	case CodeUnsigned, CodeTampered, CodeMigration, CodeDeclined:
		return http.StatusUnprocessableEntity
	case CodeIdemConflict:
		return http.StatusConflict
	case "marketplace_unconfigured":
		return http.StatusConflict
	case "ai_unavailable":
		return http.StatusBadGateway
	case "contract_unlinked":
		return http.StatusConflict
	case "payload_too_large", "too_large":
		return http.StatusRequestEntityTooLarge
	case "scope_denied":
		return http.StatusForbidden
	}
	return api.HTTPStatus()
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	var me *marketplace.Error
	switch {
	case errors.As(err, &me):
		// The marketplace component's verification and download problems keep their
		// contract code and status.
		api = &domain.APIError{Code: me.Code, Message: me.Message}
	case !errors.As(err, &api):
		api = &domain.APIError{Code: "internal", Message: "internal error"}
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(statusOf(err))
	_ = json.NewEncoder(w).Encode(api)
}

func (h *Handler) caller(r *http.Request) (Caller, error) {
	if h.Auth == nil {
		return Caller{}, domain.Unauthenticated("sign in")
	}
	c, err := h.Auth(r)
	if err != nil {
		return Caller{}, err
	}
	// CORE-06 / CORE-28: module secret and device-exchange routes use the module bearer;
	// a context token is ignored.
	if tok := r.Header.Get(ContextHeader); tok != "" && !ignoreModuleContext(r.URL.Path) {
		p, err := h.Svc.Delegate(r.Context(), c.Principal, tok)
		if err != nil {
			return Caller{}, err
		}
		c.Principal = p
	}
	return c, nil
}

// ignoreModuleContext is secrets (CORE-06) and device-context-tokens (CORE-28).
func ignoreModuleContext(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/v1/modules/")
	if !ok {
		return false
	}
	_, afterSecrets, secrets := strings.Cut(rest, "/secrets/")
	if secrets && afterSecrets != "" {
		return true
	}
	_, afterDevice, device := strings.Cut(rest, "/device-context-tokens")
	return device && (afterDevice == "" || afterDevice == "/")
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, c Caller)

type scopesKey struct{}

// CallerScopes returns the bearer scopes of the request being served, for the token
// adapter (a session has none and is bounded by its roles).
func CallerScopes(ctx context.Context) []string {
	s, _ := ctx.Value(scopesKey{}).([]string)
	return s
}

func (h *Handler) scoped(scope string, fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := h.caller(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		// CORE-32: public_operations is gated in moduleCaller; approved install scopes
		// still apply here (CU2-01), including when X-Togavsu-Public-Step is present.
		if scope != "" && c.Bearer && !hasScope(c.Scopes, scope) {
			writeErr(w, &domain.APIError{Code: "scope_denied", Message: "this token lacks the " + scope + " scope"})
			return
		}
		fn(w, r.WithContext(context.WithValue(r.Context(), scopesKey{}, c.Scopes)), c)
	}
}

// IdempotentReplayHeader marks a replayed response, the same header billing sends.
const IdempotentReplayHeader = "Idempotent-Replay"

// write adds Idempotency-Key replay (contract idempotencyKey): the same caller, route,
// key and body get the first response again without a second effect; the same key with
// a different body is idempotency_conflict.
func (h *Handler) write(scope string, fn handlerFunc) http.HandlerFunc {
	return h.scoped(scope, func(w http.ResponseWriter, r *http.Request, c Caller) {
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			fn(w, r, c)
			return
		}
		if len(key) > limits().IdemKeyChars { // the contract's Idempotency-Key maxLength
			writeErr(w, invalid("Idempotency-Key", "an Idempotency-Key is at most 200 characters"))
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, limits().MaxPackageBytes+1))
		if err != nil {
			writeErr(w, invalid("body", "the request body is unreadable"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		// The fingerprint covers the content type too, so a multipart upload's boundary
		// and parts count as its body.
		sum := sha256.Sum256(append([]byte(r.Header.Get("Content-Type")+"\n"), raw...))
		fp := hex.EncodeToString(sum[:])
		id := string(c.Principal.UserID) + "|" + c.Principal.ModuleID + "|" + r.Method + " " + r.URL.Path + "|" + key
		// A repeat of a key still in flight waits for that request only; other keys and
		// callers run concurrently (slow AI calls, backups, contract fetches).
		var mine *idemEntry
		for mine == nil {
			h.idemMu.Lock()
			prev, ok := h.idem[id]
			if !ok {
				mine = &idemEntry{done: make(chan struct{}), fingerprint: fp}
				h.idem[id] = mine
			}
			h.idemMu.Unlock()
			if !ok {
				break
			}
			if prev.fingerprint != fp {
				writeErr(w, &domain.APIError{Code: CodeIdemConflict, Message: "this Idempotency-Key was used with a different body"})
				return
			}
			select {
			case <-prev.done:
			case <-r.Context().Done():
				return
			}
			h.idemMu.Lock()
			kept := h.idem[id] == prev
			h.idemMu.Unlock()
			if kept {
				for k, v := range prev.header {
					w.Header()[k] = v
				}
				w.Header().Set(IdempotentReplayHeader, "true")
				w.WriteHeader(prev.status)
				_, _ = w.Write(prev.body)
				return
			}
			// The first request failed with a server error and wasn't kept: try again.
		}
		rec := httptest.NewRecorder()
		finished := false
		defer func() {
			h.idemMu.Lock()
			if finished && rec.Code < http.StatusInternalServerError {
				// ponytail: ceiling: replay records live in process memory for its lifetime; a
				// restart forgets them. Upgrade path: a shared idempotency table once the API
				// wave lands one. Trigger: the core gains a durable idempotency store.
				mine.status, mine.body, mine.header = rec.Code, rec.Body.Bytes(), rec.Header().Clone()
			} else {
				delete(h.idem, id)
			}
			h.idemMu.Unlock()
			close(mine.done)
		}()
		fn(rec, r, c)
		finished = true
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, limits().MaxPackageBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("body", "the request body is not valid JSON for this operation")
	}
	return nil
}

// packageBody reads a multipart "package" upload, or nil with the JSON body decoded.
func packageBody(r *http.Request, js any) ([]byte, bool, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(limits().MaxPackageBytes); err != nil {
			return nil, false, invalid("package", "the upload is unreadable")
		}
		f, _, err := r.FormFile("package")
		if err != nil {
			return nil, false, invalid("package", "attach the module package as package")
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, limits().MaxPackageBytes+1))
		if err != nil {
			return nil, false, err
		}
		return raw, true, nil
	}
	return nil, false, decode(r, js)
}

// --- module routes ---

func (h *Handler) list(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.List(r.Context(), c.Principal)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": v, "page": map[string]any{"limit": len(v) + 1, "next_cursor": nil}})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.Get(r.Context(), c.Principal, r.PathValue("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) install(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		Source    string `json:"source"`
		ListingID string `json:"listing_id"`
		Version   string `json:"version"`
	}
	raw, upload, err := packageBody(r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	var v View
	switch {
	case upload:
		v, err = h.Svc.Install(r.Context(), c.Principal, raw)
	case body.Source == "marketplace":
		v, err = h.Svc.InstallFromMarketplace(r.Context(), c.Principal, body.ListingID, body.Version)
	default:
		err = invalid("package", "upload the package file, or name a marketplace listing")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

func (h *Handler) permissions(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		Decisions []Decision `json:"decisions"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.DecidePermissions(r.Context(), c.Principal, r.PathValue("module_id"), body.Decisions)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) enable(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.Enable(r.Context(), c.Principal, r.PathValue("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) disable(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.Disable(r.Context(), c.Principal, r.PathValue("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) uninstall(w http.ResponseWriter, r *http.Request, c Caller) {
	var body UninstallInput
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.Uninstall(r.Context(), c.Principal, r.PathValue("module_id"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		Source  string `json:"source"`
		Version string `json:"version"`
	}
	raw, upload, err := packageBody(r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("module_id")
	var res UpdateResult
	switch {
	case upload:
		res, err = h.Svc.Update(r.Context(), c.Principal, id, raw)
	case body.Source == "marketplace":
		res, err = h.Svc.UpdateFromMarketplace(r.Context(), c.Principal, id, body.Version)
	default:
		err = invalid("package", "upload the new package file, or name a marketplace version")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.ModuleSettings(r.Context(), c.Principal, c.Scopes, c.Bearer, r.PathValue("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request, c Caller) {
	scope := "modules:write"
	if c.Principal.ModuleID != "" {
		scope = "module_settings:write"
	}
	if c.Bearer && !hasScope(c.Scopes, scope) {
		writeErr(w, scopeDenied(scope))
		return
	}
	var body map[string]string
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.SetModuleSettings(r.Context(), c.Principal, r.PathValue("module_id"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// createDevice is createModuleDeviceGrant (CORE-28): session only.
func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer {
		writeErr(w, domain.Forbidden("a device grant is created from a signed-in session"))
		return
	}
	var body ModuleDeviceWrite
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.CreateModuleDeviceGrant(r.Context(), c.Principal, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.ListModuleDeviceGrants(r.Context(), c.Principal)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": v})
}

func (h *Handler) revokeDevice(w http.ResponseWriter, r *http.Request, c Caller) {
	if err := h.Svc.RevokeModuleDeviceGrant(r.Context(), c.Principal, domain.ID(r.PathValue("device_id"))); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// exchangeDevice is exchangeModuleDeviceGrant (CORE-28).
func (h *Handler) exchangeDevice(w http.ResponseWriter, r *http.Request, c Caller) {
	var body DeviceContextRequest
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	tok, err := h.Svc.ExchangeModuleDeviceGrant(r.Context(), c.Principal, r.PathValue("module_id"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tok)
}

var deviceConnectPage = template.Must(template.New("device-connect").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Connect device</title>
<style>
body{font-family:system-ui,sans-serif;margin:2rem;max-width:28rem;line-height:1.4}
button{font:inherit;padding:.5rem 1rem;cursor:pointer}
.error{color:#8b0000}
</style></head>
<body>
<h1>Connect a device</h1>
<p>Allow <strong>{{.Name}}</strong> to act as you on this device. You can revoke the grant later from your profile.</p>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<button type="submit">Connect</button>
</form>
</body></html>`))

func (h *Handler) deviceConnectGet(w http.ResponseWriter, r *http.Request, c Caller) {
	h.renderDeviceConnect(w, r, c, "")
}

func (h *Handler) deviceConnectPost(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer {
		writeErr(w, domain.Forbidden("a device grant is created from a signed-in session"))
		return
	}
	module := r.PathValue("module_id")
	_, path, err := h.Svc.DeviceConnectPath(r.Context(), module)
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.CreateModuleDeviceGrant(r.Context(), c.Principal, ModuleDeviceWrite{ModuleID: module})
	if err != nil {
		h.renderDeviceConnect(w, r, c, err.Error())
		return
	}
	http.Redirect(w, r, "/m/"+module+path+"#grant="+url.QueryEscape(v.Secret), http.StatusSeeOther)
}

func (h *Handler) renderDeviceConnect(w http.ResponseWriter, r *http.Request, c Caller, errText string) {
	if ClientOnly(c.Principal) {
		writeErr(w, domain.Forbidden("only firm staff may connect a device"))
		return
	}
	module := r.PathValue("module_id")
	name, _, err := h.Svc.DeviceConnectPath(r.Context(), module)
	if err != nil {
		writeErr(w, err)
		return
	}
	csrf := ""
	if cookie, err := r.Cookie(practice.CSRFCookie); err == nil {
		csrf = cookie.Value
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = deviceConnectPage.Execute(w, map[string]string{"Name": name, "CSRF": csrf, "Error": errText})
}

// putSecret is putModuleSecret (CORE-06). If-None-Match: * creates only when absent
// (409 conflict when the name exists) so module EnsureKey races cannot overwrite keys.
func (h *Handler) putSecret(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		Value string `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	module, name := r.PathValue("module_id"), r.PathValue("name")
	var err error
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == "*" {
		err = h.Svc.PutSecretIfAbsent(r.Context(), c.Principal, module, name, body.Value)
	} else {
		err = h.Svc.PutSecret(r.Context(), c.Principal, module, name, body.Value)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getSecret is getModuleSecret (CORE-06).
func (h *Handler) getSecret(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.GetSecret(r.Context(), c.Principal, r.PathValue("module_id"), r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// deleteSecret is deleteModuleSecret (CORE-06).
func (h *Handler) deleteSecret(w http.ResponseWriter, r *http.Request, c Caller) {
	if err := h.Svc.DeleteSecret(r.Context(), c.Principal, r.PathValue("module_id"), r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) contextToken(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer && c.Principal.ModuleID == "" && !hasScope(c.Scopes, "modules:read") && !c.Principal.HasRole(domain.RoleClient) {
		writeErr(w, domain.Forbidden("this token lacks the modules:read scope"))
		return
	}
	var body struct {
		MatterID *domain.ID `json:"matter_id"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	var matter domain.ID
	if body.MatterID != nil {
		matter = *body.MatterID
	}
	t, err := h.Svc.IssueContext(r.Context(), c.Principal, r.PathValue("module_id"), matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// verifyContext lets a module check a frame's context token with its own token.
func (h *Handler) verifyContext(w http.ResponseWriter, r *http.Request, c Caller) {
	id := r.PathValue("module_id")
	if c.Principal.ModuleID != id {
		writeErr(w, domain.Forbidden("a module verifies only its own context tokens"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"viewer_user_id": c.Principal.UserID, "roles": c.Principal.Roles, "module": id,
		"matter_id": h.matterOf(r)})
}

func (h *Handler) matterOf(r *http.Request) any {
	cl, err := h.Svc.VerifyContext(r.Context(), r.Header.Get(ContextHeader))
	if err != nil || cl.MatterID.IsZero() {
		return nil
	}
	return cl.MatterID
}

func (h *Handler) readView(w http.ResponseWriter, r *http.Request, c Caller) {
	var ids []domain.ID
	for _, v := range r.URL.Query()["record_id"] {
		ids = append(ids, domain.ID(v))
	}
	rows, err := h.Svc.ReadView(r.Context(), c.Principal, c.Scopes, c.Bearer, r.PathValue("module_id"), r.PathValue("view"), ids)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) listDomains(w http.ResponseWriter, r *http.Request, c Caller) {
	v, err := h.Svc.DomainHandlers(r.Context(), c.Principal)
	if err != nil {
		writeErr(w, err)
		return
	}
	help.ListPage(w, r, h.Svc.d.Settings, v)
}

func (h *Handler) switchDomain(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		ModuleID *string `json:"module_id"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	mod := ""
	if body.ModuleID != nil {
		mod = *body.ModuleID
	}
	v, err := h.Svc.SwitchDomain(r.Context(), c.Principal, r.PathValue("domain"), mod)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) deliveries(w http.ResponseWriter, r *http.Request, c Caller) {
	d, err := h.Svc.Deliveries(r.Context(), c.Principal, r.URL.Query().Get("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": d, "page": map[string]any{"limit": len(d) + 1, "next_cursor": nil}})
}

func (h *Handler) aiCall(w http.ResponseWriter, r *http.Request, c Caller) {
	var body AICallInput
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.CallAI(r.Context(), c.Principal, c.Scopes, c.Bearer, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) aiCalls(w http.ResponseWriter, r *http.Request, c Caller) {
	q, err := h.aiCallQuery(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, next, err := h.Svc.AICalls(r.Context(), c.Principal, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	page := map[string]any{"limit": q.Limit, "next_cursor": nil}
	if next != "" {
		page["next_cursor"] = next
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "page": page})
}

// aiCallQuery reads the call log filters and the page size: api.page_size by default,
// and a limit above api.max_page_size is refused (docs/api/conventions.md).
func (h *Handler) aiCallQuery(r *http.Request) (AICallQuery, error) {
	v := r.URL.Query()
	q := AICallQuery{Module: v.Get("module_id"), Task: v.Get("task"), Provider: v.Get("provider"),
		MatterID: domain.ID(v.Get("matter_id")), Cursor: v.Get("cursor")}
	for _, f := range []struct {
		name string
		to   *time.Time
	}{{"from_time", &q.From}, {"to_time", &q.To}} {
		if raw := v.Get(f.name); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return q, invalid(f.name, f.name+" is an RFC 3339 timestamp")
			}
			*f.to = t
		}
	}
	def, err := h.Svc.intSetting(r.Context(), "api.page_size")
	if err != nil {
		return q, err
	}
	max, err := h.Svc.intSetting(r.Context(), "api.max_page_size")
	if err != nil {
		return q, err
	}
	q.Limit = def
	if raw := v.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < one || n > max {
			return q, invalid("limit", "limit is between 1 and api.max_page_size")
		}
		q.Limit = n
	}
	return q, nil
}

// --- frames ---

// staffFrameSandbox is the iframe sandbox for staff frames (CORE-09). Client frames add
// allow-top-navigation-by-user-activation. Neither ever includes allow-same-origin.
const staffFrameSandbox = "allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox"

// coreFrameNavigatePrefixes are core pages a module frame may open via togavsu:navigate,
// matching the widget-target allowlist (a tab of the same module, or a core page).
var coreFrameNavigatePrefixes = []string{
	"/practice/", "/billing/", "/trust/", "/calendar/", "/files/", "/help", "/me/", "/settings/", "/",
}

// BridgeNavigateURL builds the in-app URL for a togavsu:navigate message, or false when
// page is outside the widget-target allowlist (same module tab/widget/matter-item, or a
// core page). Absolute and protocol-relative URLs are refused.
func BridgeNavigateURL(moduleID, page string, params map[string]string) (string, bool) {
	page = strings.TrimSpace(page)
	if !safeNavigationPage(page) {
		return "", false
	}
	path, rawQuery, _ := strings.Cut(page, "?")
	ok := false
	modPrefix := "/modules/" + moduleID + "/"
	for _, p := range []string{modPrefix + "tabs/", modPrefix + "widgets/", modPrefix + "matter-items/"} {
		if strings.HasPrefix(path, p) {
			ok = true
			break
		}
	}
	if !ok {
		for _, p := range coreFrameNavigatePrefixes {
			if p == "/" {
				if path == "/" {
					ok = true
					break
				}
				continue
			}
			if strings.HasPrefix(path, p) || path == strings.TrimSuffix(p, "/") {
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", false
	}
	q := url.Values{}
	if rawQuery != "" {
		if existing, err := url.ParseQuery(rawQuery); err == nil {
			q = existing
		}
	}
	for k, v := range params {
		if k != "" {
			q.Set(k, v)
		}
	}
	if enc := q.Encode(); enc != "" {
		return path + "?" + enc, true
	}
	return path, true
}

var framePage = template.Must(template.New("frame").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>{{.Title}}</title>
<style>html,body{margin:0;height:100%}iframe{border:0;width:100%;height:100%}</style>
<script nonce="{{.Nonce}}">{{.Bridge}}</script>
</head>
<body><main aria-label="{{.Title}}" data-module="{{.Module}}">
{{template "iframe" .}}
</main></body></html>
{{define "iframe"}}<iframe class="module-frame" title="{{.Title}}" src="{{.Src}}" sandbox="{{.Sandbox}}" referrerpolicy="no-referrer" data-module="{{.Module}}"></iframe>{{end}}`))

// writeFrame renders the core page that shows a module page in a sandboxed iframe. The
// sandbox has no allow-same-origin: the frame runs in an opaque origin, so script in it
// can't read the core's cookies or page even though /m/ is on the core's domain.
// CORE-09 adds the postMessage bridge (nonce-gated) and the staff sandbox list.
//
// A page that opens as a screen of its own (a module tab, a matter action) is drawn
// inside the app shell; a panel or widget the core embeds in its own iframe is the bare
// frame page, so the shell never nests.
func writeFrame(w http.ResponseWriter, r *http.Request, screen bool, title, src, moduleID string) {
	writeFrameSandbox(w, r, screen, title, src, staffFrameSandbox, moduleID, true)
}

func writeFrameSandbox(w http.ResponseWriter, r *http.Request, screen bool, title, src, sandbox, moduleID string, staff bool) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce := frameNonce()
	script := frameBridgeScript(moduleID, staff)
	bridge := template.JS(script)
	data := map[string]any{"Title": title, "Src": src, "Nonce": nonce, "Sandbox": sandbox, "Module": moduleID, "Bridge": bridge}
	var buf bytes.Buffer
	if screen && r.Header.Get("Sec-Fetch-Dest") != "iframe" {
		if err := framePage.ExecuteTemplate(&buf, "iframe", data); err != nil {
			writeErr(w, err)
			return
		}
		// Shell would default to help.CSP (script-src 'self') and block the nonce bridge;
		// set a policy that keeps the shell assets and allows this one inline script.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'self'; form-action 'self'; base-uri 'none'")
		body := `<span hidden data-module="` + template.HTMLEscapeString(moduleID) + `"></span>` +
			`<script nonce="` + nonce + `">` + script + `</script>` + buf.String()
		frame.Write(w, r, frame.Page{Title: title, Area: "module", Body: template.HTML(body), WorkInFrame: true})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// connect-src 'self' lets the bridge fetch .../context-token (CORE-09 widgets and bare panels).
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'nonce-"+nonce+"'; connect-src 'self'; frame-src 'self'; frame-ancestors 'self'")
	if err := framePage.Execute(&buf, data); err != nil {
		writeErr(w, err)
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// FrameNonce is a CSP nonce for an inline CORE-09 bridge script.
func FrameNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "togavsu"
	}
	return hex.EncodeToString(b[:])
}

func frameNonce() string { return FrameNonce() }

// FrameBridgeScript is the CORE-09 postMessage listener for one module's frame.
// staff enables togavsu:editor.open; client frames omit it. Navigate and editor.open
// use the top-level window so an embedded panel/widget iframe does not trap the app.
// refreshPath, when set, is the absolute path the bridge fetches for context.refresh
// (matter-screen panels host the bridge on /practice/matters/... rather than the frame URL).
func FrameBridgeScript(moduleID string, staff bool, refreshPath string) string {
	esc := strings.ReplaceAll(moduleID, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `"`, `\"`)
	refreshEsc := strings.ReplaceAll(refreshPath, `\`, `\\`)
	refreshEsc = strings.ReplaceAll(refreshEsc, `"`, `\"`)
	editor := ""
	if staff {
		editor = `
    if (t === "togavsu:editor.open" && d.file_id) {
      go("/files/" + encodeURIComponent(d.file_id) + "/edit");
      return;
    }`
	}
	refreshJS := `window.location.pathname.replace(/\/$/, "") + "/context-token" + window.location.search`
	if refreshPath != "" {
		refreshJS = `"` + refreshEsc + `"`
	}
	return `
(function () {
  var moduleId = "` + esc + `";
  var frame = null;
  function bind() {
    var nodes = document.querySelectorAll("iframe.module-frame");
    for (var i = 0; i < nodes.length; i++) {
      if (nodes[i].getAttribute("data-module") === moduleId) { frame = nodes[i]; return; }
    }
    frame = nodes.length ? nodes[0] : null;
  }
  function go(url) {
    try {
      var w = (window.top && window.top !== window) ? window.top : window;
      w.location.assign(url);
    } catch (e) {
      window.location.assign(url);
    }
  }
  function allowed(page) {
    if (typeof page !== "string" || page.charAt(0) !== "/" || page.indexOf("//") === 0 || page.indexOf("://") >= 0) return false;
    var path = page.split("?")[0];
    if (path.indexOf("%") >= 0 || path.indexOf("\\") >= 0 || path.indexOf("#") >= 0 || path.split("/").some(function (part) { return part === "." || part === ".."; })) return false;
    var pref = "/modules/" + moduleId + "/";
    if (path.indexOf(pref + "tabs/") === 0 || path.indexOf(pref + "widgets/") === 0 || path.indexOf(pref + "matter-items/") === 0) return true;
    var cores = ["/practice/", "/billing/", "/trust/", "/calendar/", "/files/", "/help", "/me/", "/settings/"];
    for (var i = 0; i < cores.length; i++) {
      if (path === cores[i].replace(/\/$/, "") || path.indexOf(cores[i]) === 0) return true;
    }
    return path === "/";
  }
  window.addEventListener("message", function (ev) {
    if (!frame) bind();
    if (!frame || ev.source !== frame.contentWindow || !ev.data || typeof ev.data !== "object") return;
    var d = ev.data, t = d.type;` + editor + `
    if (t === "togavsu:navigate" && d.page) {
      if (typeof d.page === "string" && d.page.indexOf("/modules/") === 0 && d.page.indexOf("/modules/" + moduleId + "/") !== 0) {
        var nav = new URLSearchParams({page: d.page, params: JSON.stringify(d.params || {})});
        fetch("/modules/" + encodeURIComponent(moduleId) + "/navigate?" + nav.toString(), {credentials: "same-origin", headers: {"Accept": "application/json"}})
          .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
          .then(function (body) { if (body.url) go(body.url); })
          .catch(function () {});
        return;
      }
      if (!allowed(d.page)) return;
      var u = d.page;
      if (d.params && typeof d.params === "object") {
        var q = new URLSearchParams();
        Object.keys(d.params).forEach(function (k) { q.set(k, d.params[k]); });
        u += (u.indexOf("?") >= 0 ? "&" : "?") + q.toString();
      }
      go(u);
      return;
    }
    if (t === "togavsu:context.refresh" && d.request_id) {
      var refresh = ` + refreshJS + `;
      fetch(refresh, {credentials: "same-origin", headers: {"Accept": "application/json"}})
        .then(function (r) { return r.ok ? r.json() : Promise.reject(r.status); })
        .then(function (body) {
          frame.contentWindow.postMessage({type: "togavsu:context.token", request_id: d.request_id, token: body.token, expires_at: body.expires_at}, "*");
        })
        .catch(function () {
          frame.contentWindow.postMessage({type: "togavsu:context.error", request_id: d.request_id, code: "session_expired"}, "*");
        });
    }
  });
})();
`
}

func frameBridgeScript(moduleID string, staff bool) string {
	return FrameBridgeScript(moduleID, staff, "")
}

func (h *Handler) tabPage(w http.ResponseWriter, r *http.Request, c Caller) {
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	tab, ok := in.Manifest.Tab(r.PathValue("tab"))
	if !ok {
		writeErr(w, domain.NotFound("tab"))
		return
	}
	matter := domain.ID(r.URL.Query().Get("matter_id"))
	if tab.Placement == "matter" && matter.IsZero() {
		writeErr(w, invalid("matter_id", "a matter tab needs the matter"))
		return
	}
	t, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := url.Values{}
	for k, v := range r.URL.Query() {
		q[k] = v
	}
	writeFrame(w, r, true, tab.Title, FramePath(in.ID, tab.Path, t.Token, q), in.ID)
}

// matterItemPage frames a module's matter-screen panel or action for one matter, with a
// context token for the viewer scoped to that matter, the same way a matter tab is framed.
func (h *Handler) matterItemPage(w http.ResponseWriter, r *http.Request, c Caller) {
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	item, ok := in.Manifest.MatterItem(r.PathValue("item"))
	if !ok {
		writeErr(w, domain.NotFound("matter item"))
		return
	}
	matter := domain.ID(r.URL.Query().Get("matter_id"))
	if matter.IsZero() {
		writeErr(w, invalid("matter_id", "a matter item needs the matter"))
		return
	}
	t, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeFrame(w, r, item.Kind != "panel", item.Title, FramePath(in.ID, item.Path, t.Token, url.Values{"matter_id": {string(matter)}}), in.ID)
}

func (h *Handler) widgetPage(w http.ResponseWriter, r *http.Request, c Caller) {
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	wd, ok := in.Manifest.Widget(r.PathValue("widget"))
	if !ok {
		writeErr(w, domain.NotFound("widget"))
		return
	}
	t, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	q := url.Values{}
	if wd.Target != nil {
		for _, k := range wd.Target.Params {
			if v := r.URL.Query().Get(k); v != "" {
				q.Set(k, v)
			}
		}
	}
	writeFrame(w, r, false, wd.Title, FramePath(in.ID, wd.Path, t.Token, q), in.ID)
}

// staff frame context-token routes (CORE-09): same user, module and matter as the frame.
func (h *Handler) staffTabContextToken(w http.ResponseWriter, r *http.Request, c Caller) {
	h.staffFrameContextToken(w, r, c, "tab", r.PathValue("tab"))
}

func (h *Handler) staffWidgetContextToken(w http.ResponseWriter, r *http.Request, c Caller) {
	h.staffFrameContextToken(w, r, c, "widget", r.PathValue("widget"))
}

func (h *Handler) staffMatterItemContextToken(w http.ResponseWriter, r *http.Request, c Caller) {
	h.staffFrameContextToken(w, r, c, "item", r.PathValue("item"))
}

func (h *Handler) staffFrameContextToken(w http.ResponseWriter, r *http.Request, c Caller, kind, id string) {
	if ClientOnly(c.Principal) {
		writeErr(w, domain.NotFound("frame"))
		return
	}
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	var matter domain.ID
	switch kind {
	case "tab":
		tab, ok := in.Manifest.Tab(id)
		if !ok || tab.Placement == "client" {
			writeErr(w, domain.NotFound("tab"))
			return
		}
		if tab.Placement == "matter" {
			matter = domain.ID(r.URL.Query().Get("matter_id"))
			if matter.IsZero() {
				writeErr(w, invalid("matter_id", "a matter tab needs the matter"))
				return
			}
		}
	case "widget":
		if _, ok := in.Manifest.Widget(id); !ok {
			writeErr(w, domain.NotFound("widget"))
			return
		}
		if mid := r.URL.Query().Get("matter_id"); mid != "" {
			matter = domain.ID(mid)
		}
	case "item":
		if _, ok := in.Manifest.MatterItem(id); !ok {
			writeErr(w, domain.NotFound("matter item"))
			return
		}
		matter = domain.ID(r.URL.Query().Get("matter_id"))
		if matter.IsZero() {
			writeErr(w, invalid("matter_id", "a matter item needs the matter"))
			return
		}
	default:
		writeErr(w, domain.NotFound("frame"))
		return
	}
	tok, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok.Token, "expires_at": tok.ExpiresAt.UTC().Format(time.RFC3339)})
}

// frame proxies /m/{id}/... to the module without the core's cookie or credentials.
// In the Compose stack caddy routes /m/{id}/ straight to the module container; this is
// the same route for a core that serves it itself.
func (h *Handler) frame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("module_id")
	in, err := h.Svc.Installed(r.Context(), id)
	if err != nil || !in.Enabled || h.Svc.d.Runtime == nil {
		writeErr(w, domain.NotFound("module"))
		return
	}
	h.Svc.proxy(id, "/m/"+id, "").ServeHTTP(w, r)
}

// --- include= ---

// pluralRecords maps an API collection to its core record.
var pluralRecords = map[string]string{
	"matters": "matter", "contacts": "contact", "users": "user", "invoices": "invoice", "payments": "payment",
	"time-entries": "time_entry", "expenses": "expense", "files": "file", "notes": "note",
	"calendar-events": "calendar_event", "deadlines": "deadline",
	"fee-agreements": "fee_agreement", // CORE-22
}

// Include wraps the core's read and list endpoints: GET /api/v1/{records}/{id} and
// GET /api/v1/{records} with include=module.name add an "included" object to each record
// the core returned, read inside the viewer's access (PRD 9.9). The core handler runs
// first and decides which records the viewer gets; Include adds rows only to those.
func (h *Handler) Include(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		names := r.URL.Query()["include"]
		if r.Method != http.MethodGet || len(names) == 0 || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
		if upload, ok := strings.CutPrefix(r.URL.Path, "/api/v1/files/unassigned/"); ok && !strings.Contains(upload, "/") {
			// reviewUnassignedFile returns one file record.
			parts = []string{"files", upload}
		}
		record, ok := pluralRecords[parts[0]]
		if !ok || len(parts) > two {
			next.ServeHTTP(w, r)
			return
		}
		var all []string
		for _, n := range names {
			all = append(all, strings.Split(n, ",")...)
		}
		inner := r.Clone(r.Context())
		q := inner.URL.Query()
		q.Del("include")
		inner.URL.RawQuery = q.Encode()
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, inner)
		if rec.Code != http.StatusOK {
			copyResponse(w, rec)
			return
		}
		c, err := h.caller(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			copyResponse(w, rec)
			return
		}
		var items []map[string]any
		if data, ok := body["data"].([]any); ok && len(parts) == one {
			for _, it := range data {
				if m, ok := it.(map[string]any); ok {
					items = append(items, m)
				}
			}
		} else {
			items = []map[string]any{body}
		}
		var ids []domain.ID
		for _, it := range items {
			if id, ok := it["id"].(string); ok {
				ids = append(ids, domain.ID(id))
			}
		}
		got, err := h.Svc.Include(r.Context(), c.Principal, c.Scopes, record, ids, all)
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, it := range items {
			id, _ := it["id"].(string)
			inc := got[domain.ID(id)]
			if inc == nil {
				inc = map[string][]Row{}
			}
			it["included"] = inc
		}
		for k, v := range rec.Header() {
			if k != "Content-Length" {
				w.Header()[k] = v
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
}

func copyResponse(w http.ResponseWriter, rec *httptest.ResponseRecorder) {
	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

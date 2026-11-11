package modules

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
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
	Svc  *Service
	Auth func(*http.Request) (Caller, error)

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
	m.HandleFunc("PUT /api/v1/modules/{module_id}/settings", h.write("modules:write", h.putSettings))
	m.HandleFunc("POST /api/v1/modules/{module_id}/context-tokens", h.write("", h.contextToken))
	m.HandleFunc("POST /api/v1/modules/{module_id}/context-tokens/verify", h.scoped("", h.verifyContext))
	m.HandleFunc("GET /api/v1/modules/{module_id}/views/{view}", h.scoped("", h.readView))
	m.HandleFunc("GET /api/v1/domain-handlers", h.scoped("modules:read", h.listDomains))
	m.HandleFunc("PUT /api/v1/domain-handlers/{domain}", h.write("modules:write", h.switchDomain))
	m.HandleFunc("GET /api/v1/trusted-signing-keys", h.scoped("modules:read", h.listKeys))
	m.HandleFunc("POST /api/v1/trusted-signing-keys", h.write("modules:write", h.addKey))
	m.HandleFunc("GET /api/v1/trusted-signing-keys/{key_id}", h.scoped("modules:read", h.getKey))
	m.HandleFunc("PATCH /api/v1/trusted-signing-keys/{key_id}", h.write("modules:write", h.patchKey))
	m.HandleFunc("DELETE /api/v1/trusted-signing-keys/{key_id}", h.write("modules:write", h.revokeKey))
	m.HandleFunc("GET /api/v1/webhook-deliveries", h.scoped("webhooks:read", h.deliveries))
	m.HandleFunc("POST /api/v1/ai/calls", h.write("ai:use", h.aiCall))
	m.HandleFunc("GET /api/v1/ai/calls", h.scoped("ai:admin", h.aiCalls))
	m.HandleFunc("GET /modules/{module_id}/tabs/{tab}", h.scoped("", h.tabPage))
	m.HandleFunc("GET /modules/{module_id}/widgets/{widget}", h.scoped("", h.widgetPage))
	m.HandleFunc("/m/{module_id}/", h.frame)
	return m
}

// --- plumbing ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func statusOf(err error) int {
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
	}
	return api.HTTPStatus()
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if !errors.As(err, &api) {
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
	if tok := r.Header.Get(ContextHeader); tok != "" {
		p, err := h.Svc.Delegate(r.Context(), c.Principal, tok)
		if err != nil {
			return Caller{}, err
		}
		c.Principal = p
	}
	return c, nil
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
		if scope != "" && c.Bearer && !hasScope(c.Scopes, scope) {
			writeErr(w, domain.Forbidden("this token lacks the "+scope+" scope"))
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
		v, err = h.Svc.Install(r.Context(), c.Principal, raw, "local")
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
	if !upload {
		if body.Source != "marketplace" || h.Svc.d.Market == nil {
			writeErr(w, invalid("package", "upload the new package file"))
			return
		}
		if raw, err = h.Svc.d.Market.Package(r.Context(), id, body.Version); err != nil {
			writeErr(w, err)
			return
		}
	}
	src := "local"
	if !upload {
		src = "marketplace"
	}
	res, err := h.Svc.Update(r.Context(), c.Principal, id, raw, src)
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

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request, c Caller) {
	keys, err := h.Svc.ListTrustedKeys(r.Context(), c.Principal)
	if err != nil {
		writeErr(w, err)
		return
	}
	help.ListPage(w, r, h.Svc.d.Settings, keys)
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request, c Caller) {
	k, err := h.Svc.GetTrustedKey(r.Context(), c.Principal, r.PathValue("key_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (h *Handler) addKey(w http.ResponseWriter, r *http.Request, c Caller) {
	var body struct {
		Name         string `json:"name"`
		PublicKeyPEM string `json:"public_key_pem"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	k, err := h.Svc.CreateTrustedKey(r.Context(), c.Principal, body.Name, body.PublicKeyPEM)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (h *Handler) patchKey(w http.ResponseWriter, r *http.Request, c Caller) {
	var body TrustedKeyPatch
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	k, err := h.Svc.UpdateTrustedKey(r.Context(), c.Principal, r.PathValue("key_id"), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (h *Handler) revokeKey(w http.ResponseWriter, r *http.Request, c Caller) {
	if err := h.Svc.DeleteTrustedKey(r.Context(), c.Principal, r.PathValue("key_id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	out, err := h.Svc.AICalls(r.Context(), c.Principal, r.URL.Query().Get("module_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "page": map[string]any{"limit": len(out) + 1, "next_cursor": nil}})
}

// --- frames ---

var framePage = template.Must(template.New("frame").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>{{.Title}}</title>
<style>html,body{margin:0;height:100%}iframe{border:0;width:100%;height:100%}</style></head>
<body><main aria-label="{{.Title}}">
<iframe title="{{.Title}}" src="{{.Src}}" sandbox="allow-scripts allow-forms" referrerpolicy="no-referrer"></iframe>
</main></body></html>`))

// writeFrame renders the core page that shows a module page in a sandboxed iframe. The
// sandbox has no allow-same-origin: the frame runs in an opaque origin, so script in it
// can't read the core's cookies or page even though /m/ is on the core's domain.
func writeFrame(w http.ResponseWriter, title, src string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'; frame-ancestors 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	var buf bytes.Buffer
	if err := framePage.Execute(&buf, map[string]string{"Title": title, "Src": src}); err != nil {
		writeErr(w, err)
		return
	}
	_, _ = w.Write(buf.Bytes())
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
	writeFrame(w, tab.Title, FramePath(in.ID, tab.Path, t.Token, q))
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
	writeFrame(w, wd.Title, FramePath(in.ID, wd.Path, t.Token, q))
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

package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

var sessionCookie = identity.SessionCookie("").Name

func (a *App) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", a.health)
	m.HandleFunc("GET /api/v1/health", a.health)
	m.HandleFunc("GET /api/v1/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(api.OpenAPI)
	})
	m.HandleFunc("GET /api/v1/setup", a.setupStatus)
	m.HandleFunc("POST /api/v1/setup", a.setupJSON)
	m.HandleFunc("POST /api/v1/auth/login", a.loginJSON)
	m.HandleFunc("POST /api/v1/auth/totp", a.totpJSON)
	m.HandleFunc("POST /api/v1/auth/recovery", a.recoveryJSON)
	m.HandleFunc("POST /api/v1/auth/totp/enroll", a.enrollJSON)
	m.HandleFunc("POST /api/v1/auth/totp/confirm", a.confirmJSON)
	m.HandleFunc("POST /api/v1/auth/logout", a.logoutJSON)
	m.HandleFunc("GET /api/v1/me", a.meJSON)
	m.HandleFunc("GET /api/v1/settings", a.settingsJSON)
	m.HandleFunc("GET /api/v1/settings/{setting_key}", a.settingJSON)
	m.HandleFunc("PATCH /api/v1/settings/{setting_key}", a.setSettingJSON)
	m.HandleFunc("POST /api/v1/settings/{setting_key}/reset", a.resetSettingJSON)
	m.HandleFunc("GET /api/v1/audit-log", a.auditJSON)
	m.HandleFunc("GET /api/v1/api-tokens", a.tokensJSON)
	m.HandleFunc("POST /api/v1/api-tokens", a.issueTokenJSON)
	m.HandleFunc("DELETE /api/v1/api-tokens/{token_id}", a.revokeTokenJSON)
	a.identityRoutes(m)
	a.meContactRoutes(m)
	a.meAppointmentsRoutes(m)
	a.meTrustRoutes(m)
	a.directoryRoutes(m)
	a.booksRoutes(m)
	a.clientAccessRoutes(m)
	a.portalRoutes(m)
	a.systemRoutes(m)
	a.moduleMailRoutes(m)
	a.eventRoutes(m)
	a.aiProviderRoutes(m)
	a.emailChangeRoutes(m)
	m.HandleFunc("POST /api/v1/custom-field-values:bulk", a.bulkCustomFields)
	m.HandleFunc("POST /mcp", a.mcp)
	a.backups.mount(m)
	a.backups.mountUpdates(m)

	m.HandleFunc("GET /{$}", a.home)
	m.HandleFunc("GET /setup", a.setupPage)
	m.HandleFunc("POST /setup", a.setupPost)
	m.HandleFunc("POST /setup/confirm", a.confirmPost)
	m.HandleFunc("GET /login", a.loginPage)
	m.HandleFunc("POST /login", a.loginPost)
	m.HandleFunc("POST /login/totp", a.totpPost)
	m.HandleFunc("GET /logout", a.logoutPage)
	m.HandleFunc("POST /logout", a.logoutPost)
	m.HandleFunc("GET /admin/settings", a.settingsPage)
	m.HandleFunc("POST /admin/settings/{setting_key}", a.settingsPost)
	m.HandleFunc("POST /admin/settings/{setting_key}/reset", a.settingsResetPost)
	m.Handle("/", a.screens(http.HandlerFunc(a.dispatch)))
	a.mux = m
}

// domain is the service mux that serves r: the first of trust, billing and practice with
// a route for it. A path no service routes falls to practice for its 404 or 405.
func (a *App) domain(r *http.Request) *http.ServeMux {
	for _, mux := range a.domains {
		if _, pattern := mux.Handler(r); pattern != "" {
			return mux
		}
	}
	return a.domains[len(a.domains)-1]
}

func (a *App) dispatch(w http.ResponseWriter, r *http.Request) { a.domain(r).ServeHTTP(w, r) }

// screenPrefixes are the server-rendered areas; staticPrefixes are their public assets.
var screenPrefixes = []string{"/practice/", "/billing", "/trust", "/calendar", "/files/", "/matters/", "/doc-tender", "/admin/doc-tender"}
var staticPrefixes = []string{"/practice/static/", "/calendar/static/", "/files/static/", "/editor/", "/editor-host/"}

func isScreen(path string) bool {
	for _, p := range staticPrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	for _, p := range screenPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// screens sends a signed-out browser on a practice, billing or trust screen to sign-in.
// API callers keep their 401; static files and anything else pass through.
func (a *App) screens(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = memoCaller(r)
		if r.Method == http.MethodGet && isScreen(r.URL.Path) {
			if _, err := a.principal(r); status(err) == http.StatusUnauthorized {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// openBeforeSetup are the paths a fresh install serves before setup completes. Sign-in
// stays open so an admin whose first code was wrong can sign in again and re-enroll.
func openBeforeSetup(path string) bool {
	switch path {
	case "/healthz", "/api/v1/health", "/api/v1/openapi.json", "/api/v1/setup", "/setup", "/setup/confirm",
		"/api/v1/auth/login", "/api/v1/auth/totp/enroll", "/api/v1/auth/totp/confirm", "/login", "/favicon.ico",
		fileBytesPath:
		return true
	}
	for _, p := range staticPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// setupGate sends every request to first-run setup until firm, admin and two-factor exist.
func (a *App) setupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if openBeforeSetup(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		ok, err := a.Ready(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/mcp" {
			writeErr(w, &domain.APIError{Code: "setup_required", Message: "first-run setup is not complete"})
			return
		}
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
	})
}

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if err := a.Pool.PingContext(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "version": Version})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// callerMemo holds one request's authentication result, so the sign-in redirect gate,
// the service handler and the trust per-entry scope check authenticate once: one session
// touch and one api_rate_per_minute hit per request.
type callerMemo struct {
	done bool
	c    identity.Caller
	err  error
}

type callerKey struct{}

// memoCaller gives r one callerMemo for the whole request. Nested gates (password
// change, screens, the outer hold gate) may call it again; a second call keeps the
// existing memo so api_rate_per_minute counts once. A presented task authorization
// is stashed for mail_filing / mail_matching checks.
func memoCaller(r *http.Request) *http.Request {
	ctx := r.Context()
	if _, ok := ctx.Value(callerKey{}).(*callerMemo); !ok {
		ctx = context.WithValue(ctx, callerKey{}, &callerMemo{})
	}
	if g := r.Header.Get(modules.TaskAuthorizationHeader); g != "" {
		if modules.TaskAuth(ctx) == "" {
			ctx = modules.WithTaskAuth(ctx, g)
		}
	}
	return r.WithContext(ctx)
}

// caller authenticates a cookie session or a bearer token, once per memoized request.
// Cookie writes need the CSRF token from sign-in, in X-CSRF-Token or a csrf form field.
// A bearer call needs every scope the contract lists for that route; a route the contract
// does not list is denied.
func (a *App) caller(r *http.Request) (identity.Caller, error) {
	m, ok := r.Context().Value(callerKey{}).(*callerMemo)
	if !ok {
		return a.authenticate(r)
	}
	if !m.done {
		m.c, m.err = a.authenticate(r)
		m.done = true
	}
	return m.c, m.err
}

func (a *App) authenticate(r *http.Request) (identity.Caller, error) {
	in := identity.Presented{Write: !safeMethod(r.Method)}
	if c, err := r.Cookie(sessionCookie); err == nil {
		in.SessionToken = c.Value
	}
	if h := r.Header.Get("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			return identity.Caller{}, domain.Unauthenticated("authorization must be a bearer token")
		}
		in.Bearer = strings.TrimSpace(token)
	}
	if in.Write && in.SessionToken != "" {
		in.CSRF = r.Header.Get("X-CSRF-Token")
		if in.CSRF == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			in.CSRF = r.FormValue("csrf")
		}
	}
	c, err := a.Identity.Authenticate(r.Context(), in)
	if err != nil {
		return identity.Caller{}, shared(err)
	}
	if in.Bearer != "" {
		if moduleRuntimeRoute(r, c.Principal) {
			return c, nil
		}
		t, ok := a.toolFor(r.Method, r.URL.Path)
		if !ok {
			return identity.Caller{}, domain.Forbidden("this route is not available to tokens")
		}
		if entryTypeScoped[t.Name] {
			return c, nil
		}
		if err := c.RequireScopes(t.Scopes...); err != nil {
			return identity.Caller{}, shared(err)
		}
	}
	return c, nil
}

// moduleRuntimeRoute reports a module token calling the runtime's module-facing routes
// that either sit outside the published contract (verify, own settings, published view)
// or need a trailing path wildcard the contract's single-segment {name} cannot express
// (module secrets with / in the name, CORE-06). The module handler still enforces own-id
// and scope rules.
// ponytail: ceiling: verify/settings/views plus secrets wildcard. Upgrade path: contract
// path params that span segments. Trigger: OpenAPI gains a multi-segment secret name form.
func moduleRuntimeRoute(r *http.Request, p domain.Principal) bool {
	if p.ModuleID == "" || p.Source != domain.SourceModule {
		return false
	}
	_, pattern := moduleRuntimeRoutes.Handler(r)
	switch pattern {
	case "POST /api/v1/modules/{module_id}/context-tokens/verify", "GET /api/v1/modules/{module_id}/settings":
		return moduleIDOf(r.URL.Path) == p.ModuleID
	case "GET /api/v1/modules/{module_id}/views/{view}":
		return true
	case "PUT /api/v1/modules/{module_id}/secrets/{name...}",
		"GET /api/v1/modules/{module_id}/secrets/{name...}",
		"DELETE /api/v1/modules/{module_id}/secrets/{name...}":
		return moduleIDOf(r.URL.Path) == p.ModuleID
	}
	return false
}

// moduleRuntimeRoutes matches moduleRuntimeRoute's patterns.
var moduleRuntimeRoutes = func() *http.ServeMux {
	m := http.NewServeMux()
	for _, p := range []string{
		"POST /api/v1/modules/{module_id}/context-tokens/verify",
		"GET /api/v1/modules/{module_id}/settings",
		"GET /api/v1/modules/{module_id}/views/{view}",
		"PUT /api/v1/modules/{module_id}/secrets/{name...}",
		"GET /api/v1/modules/{module_id}/secrets/{name...}",
		"DELETE /api/v1/modules/{module_id}/secrets/{name...}",
	} {
		m.Handle(p, http.NotFoundHandler())
	}
	return m
}()

// moduleIDOf is the {module_id} segment of a /api/v1/modules/ path.
func moduleIDOf(path string) string {
	rest, _ := strings.CutPrefix(path, "/api/v1/modules/")
	id, _, _ := strings.Cut(rest, "/")
	return id
}

// shared maps identity's specific codes onto the shared error table, so every handler,
// including practice's, answers 400, 403 or 429 rather than 500.
func shared(err error) error {
	var api *domain.APIError
	if !errors.As(err, &api) {
		return err
	}
	switch api.Code {
	case "csrf_required":
		return domain.Forbidden(api.Message)
	// CORE-36: keep scope_denied in the JSON body (status stays 403 via APIError.HTTPStatus).
	case "scope_denied":
		return err
	case "ambiguous_auth":
		return domain.Invalid("authorization", api.Message)
	case "locked_out":
		return &domain.APIError{Code: domain.CodeRateLimited, Message: api.Message}
	}
	return err
}

// principal is the hook practice and every core route use. Services check roles,
// walls and attorney-only redaction against it.
// A module's call carrying a context token runs as the viewer (delegate). A module
// token with no context token is the service principal (CORE-01), audited as source
// service, and only reaches operations marked for it.
func (a *App) principal(r *http.Request) (domain.Principal, error) {
	c, err := a.caller(r)
	if err != nil {
		return c.Principal, err
	}
	p, err := a.delegate(r, c.Principal)
	if err != nil {
		return p, err
	}
	if moduleOwn(p) {
		runtime := moduleRuntimeRoute(r, p)
		if err := a.allowServiceCall(r, p); err != nil {
			return p, err
		}
		p.Source = domain.SourceService
		if err := a.elevateBooksReader(r, &p, c); err != nil {
			return p, err
		}
		// CORE-30: non-books service calls need a staff role so Decide allows them; UserID
		// stays zero so the principal is on no wall (walled matters stay omitted / not_found).
		a.elevateServiceStaff(&p, r)
		// CORE-01: every service call writes an audit row (module, operation, source service),
		// including reads that would otherwise leave no write-side audit.
		if !runtime {
			if err := a.auditServiceCall(r, p); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

// elevateServiceStaff gives a CORE-30 service principal the staff role when books
// elevation did not already assign roles.
func (a *App) elevateServiceStaff(p *domain.Principal, r *http.Request) {
	if len(p.Roles) > 0 || p.ModuleID == "" || p.Source != domain.SourceService {
		return
	}
	t, ok := a.toolFor(r.Method, r.URL.Path)
	if !ok || !serviceDelegation[t.Delegation] {
		return
	}
	p.Roles = []domain.Role{domain.RoleStaff}
}

// auditServiceCall appends one CORE-01 audit row for a module service principal call.
func (a *App) auditServiceCall(r *http.Request, p domain.Principal) error {
	if a.Audit == nil {
		return nil
	}
	t, ok := a.toolFor(r.Method, r.URL.Path)
	if !ok {
		return nil
	}
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = a.Audit.Record(r.Context(), p, audit.Change{
		Entity: "service_call", EntityID: id, Field: "operation", New: t.Name, Reason: "service.call",
	})
	return err
}

// serviceDelegation marks an operation a module's own token may call without a viewer.
// service_principal and delegated_viewer_or_service_principal are CORE-01 service calls.
// delegated_viewer_or_noninteractive is the grant path (publication, payment, refund,
// mail filing): the handler checks the grant or task-authorization header.
var serviceDelegation = map[string]bool{
	"service_principal":                     true,
	"delegated_viewer_or_service_principal": true,
	"delegated_viewer_or_noninteractive":    true,
}

// allowServiceCall refuses a module's own token on every operation that is not marked
// for a service principal or a noninteractive grant workflow (CORE-01). A task-authorization
// header only skips this gate for delegated_viewer_or_noninteractive (the grant path);
// a junk header on an unmarked write still answers scope_denied.
func (a *App) allowServiceCall(r *http.Request, p domain.Principal) error {
	if moduleRuntimeRoute(r, p) {
		return nil
	}
	t, ok := a.toolFor(r.Method, r.URL.Path)
	if !ok {
		return domain.Forbidden("this route is not available to tokens")
	}
	if r.Header.Get(modules.TaskAuthorizationHeader) != "" && t.Delegation == "delegated_viewer_or_noninteractive" {
		return nil
	}
	if !serviceDelegation[t.Delegation] {
		return &domain.APIError{Code: "scope_denied", Message: "operation does not allow a module service call"}
	}
	return a.limitServiceCall(r.Context(), p, t.Name)
}

// limitServiceCall applies modules.service_call_limit.<operation> when that setting
// exists (CORE-30). Operations without a dedicated setting are unlimited here; the
// firm-wide security.api_rate_per_minute still applies.
func (a *App) limitServiceCall(ctx context.Context, p domain.Principal, op string) error {
	if a.Settings == nil {
		return nil
	}
	key := "modules.service_call_limit." + op
	raw, err := a.Settings.Get(ctx, key)
	if err != nil {
		// Unknown setting: not a CORE-30 limited operation.
		return nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || !serviceCallLimitOK(limit) {
		return nil
	}
	id := p.ModuleID + "\x00" + op
	now := time.Now().UTC()
	window := serviceCallWindow()
	a.svcHitMu.Lock()
	defer a.svcHitMu.Unlock()
	if a.svcHits == nil {
		a.svcHits = map[string][]time.Time{}
	}
	var kept []time.Time
	for _, at := range a.svcHits[id] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		a.svcHits[id] = kept
		return domain.RateLimited()
	}
	a.svcHits[id] = append(kept, now)
	return nil
}

// serviceCallWindow is the minute named by modules.service_call_limit.* (CORE-30).
func serviceCallWindow() time.Duration { return time.Minute }

// serviceCallLimitOK rejects non-positive limits from a corrupt setting value.
func serviceCallLimitOK(n int) bool { return n >= 1 }

// status is the HTTP status for err: identity's own codes go through shared, and the
// two setup codes are conflicts.
func status(err error) int {
	var api *domain.APIError
	if !errors.As(shared(err), &api) {
		return http.StatusInternalServerError
	}
	if api.Code == "already_set_up" || api.Code == "setup_required" {
		return http.StatusConflict
	}
	return api.HTTPStatus()
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status(err))
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.APIError{Code: "internal", Message: "internal error"})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "request body must be a JSON object")
	}
	return nil
}

func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// signIn sets the identity session cookie and the CSRF cookie practice forms read.
// Both hold identity's values, so practice's double-submit check and identity's
// session-bound CSRF check accept the same token.
func signIn(w http.ResponseWriter, r *http.Request, res identity.LoginResult) {
	c := identity.SessionCookie(res.SessionToken)
	http.SetCookie(w, &http.Cookie{Name: c.Name, Value: c.Value, Path: c.Path, HttpOnly: c.HTTPOnly, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: practice.CSRFCookie, Value: res.CSRFToken, Path: "/", HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
}

func signOut(w http.ResponseWriter) {
	for _, name := range []string{sessionCookie, practice.CSRFCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
}

func userJSON(u domain.User) map[string]any {
	roles, _ := domain.ParseRoles(u.Roles)
	if roles == nil {
		roles = []domain.Role{}
	}
	return map[string]any{
		"id": u.ID, "created_at": u.CreatedAt.UTC().Format(time.RFC3339), "created_by": u.CreatedBy,
		"updated_at": u.UpdatedAt.UTC().Format(time.RFC3339), "name": u.Name, "email": u.Email,
		"roles": roles, "active": u.Active,
	}
}

func loginJSON(res identity.LoginResult) map[string]any {
	out := map[string]any{"status": res.Status}
	if res.TOTPToken != "" {
		out["totp_token"] = res.TOTPToken
	}
	if res.EnrollmentToken != "" {
		out["enrollment_token"] = res.EnrollmentToken
	}
	if res.SessionToken != "" {
		out["user"] = userJSON(res.User)
		out["csrf_token"] = res.CSRFToken
	}
	if res.RecoveryCodes != nil {
		out["recovery_codes"] = res.RecoveryCodes
	}
	return out
}

func (a *App) setupStatus(w http.ResponseWriter, r *http.Request) {
	ok, err := a.Ready(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ready": ok})
}

type setupBody struct {
	FirmName         string `json:"firm_name"`
	TimeZone         string `json:"time_zone"`
	AdminName        string `json:"admin_name"`
	AdminEmail       string `json:"admin_email"`
	Password         string `json:"password"`
	BackupPassphrase string `json:"backup_passphrase"`
}

func (b setupBody) input() identity.SetupInput {
	return identity.SetupInput{FirmName: b.FirmName, TimeZone: b.TimeZone, AdminName: b.AdminName,
		AdminEmail: b.AdminEmail, Password: b.Password, BackupPassphrase: b.BackupPassphrase}
}

func enrollJSON(e identity.Enrollment) map[string]any {
	return map[string]any{"status": "totp_enrollment_required", "enrollment_token": e.EnrollmentToken, "otpauth_uri": e.OTPAuthURI, "secret": e.Secret}
}

func (a *App) setupJSON(w http.ResponseWriter, r *http.Request) {
	var body setupBody
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	e, err := a.Identity.Setup(r.Context(), body.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, enrollJSON(e))
}

func (a *App) loginJSON(w http.ResponseWriter, r *http.Request) {
	var body struct{ Email, Password string }
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	a.finish(w, r)(a.Identity.Login(r.Context(), body.Email, body.Password))
}

func (a *App) totpJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TOTPToken string `json:"totp_token"`
		Code      string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	a.finish(w, r)(a.Identity.VerifyTOTP(r.Context(), body.TOTPToken, body.Code))
}

func (a *App) recoveryJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TOTPToken    string `json:"totp_token"`
		RecoveryCode string `json:"recovery_code"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	a.finish(w, r)(a.Identity.VerifyRecovery(r.Context(), body.TOTPToken, body.RecoveryCode))
}

func (a *App) enrollJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	e, err := a.Identity.StartEnrollment(r.Context(), body.EnrollmentToken)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrollJSON(e))
}

func (a *App) confirmJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EnrollmentToken string `json:"enrollment_token"`
		Code            string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	a.finish(w, r)(a.Identity.ConfirmEnrollment(r.Context(), body.EnrollmentToken, body.Code))
}

// finish writes a login step and signs the browser in when it authenticated.
func (a *App) finish(w http.ResponseWriter, r *http.Request) func(identity.LoginResult, error) {
	return func(res identity.LoginResult, err error) {
		if err != nil {
			writeErr(w, err)
			return
		}
		if res.SessionToken != "" {
			signIn(w, r, res)
		}
		writeJSON(w, http.StatusOK, loginJSON(res))
	}
}

func (a *App) logoutJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.Logout(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	signOut(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) meJSON(w http.ResponseWriter, r *http.Request) {
	// CORE-36: a module bearer with X-Togavsu-Context-Token answers as the viewer.
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	u, err := a.Users.Get(r.Context(), p.UserID)
	if err != nil {
		writeErr(w, domain.Unauthenticated("sign in is required"))
		return
	}
	out := userJSON(u)
	if !u.ContactID.IsZero() {
		out["linked_contact_id"] = u.ContactID
	} else {
		out["linked_contact_id"] = nil
	}
	if practice.ClientOnly(p) && a.Practice != nil {
		n, err := a.Practice.CountAccessEndedMatters(r.Context(), p)
		if err != nil {
			writeErr(w, err)
			return
		}
		out["access_ended_matters"] = n
	}
	writeJSON(w, http.StatusOK, out)
}

// admin authorizes settings and the audit log in the service layer of this package:
// only an admin principal, by session or by a token that carries the route's scope.
func (a *App) admin(r *http.Request) (domain.Principal, error) {
	c, err := a.caller(r)
	if err != nil {
		return domain.Principal{}, err
	}
	if !c.Principal.HasRole(domain.RoleAdmin) {
		return domain.Principal{}, domain.Forbidden("admin role is required")
	}
	return c.Principal, nil
}

func settingJSON(e settings.Entry) map[string]any {
	out := map[string]any{
		"key": e.Key, "type": e.Kind, "tab": e.Tab, "default": e.Default, "configured": e.Overridden,
		"locked": e.Locked, "help": e.Help,
	}
	if e.Secret {
		out["value"] = nil
		out["configured"] = e.SecretSet
	} else {
		out["value"] = e.Value
	}
	if len(e.Allowed) > 0 {
		out["allowed"] = e.Allowed
	}
	return out
}

func (a *App) entry(ctx context.Context, key string) (settings.Entry, error) {
	list, err := a.Settings.List(ctx)
	if err != nil {
		return settings.Entry{}, err
	}
	for _, e := range list {
		if e.Key == key {
			return e, nil
		}
	}
	return settings.Entry{}, domain.NotFound("setting")
}

func (a *App) settingsJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	list, err := a.Settings.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if tab := r.URL.Query().Get("tab"); tab != "" && tab != e.Tab {
			continue
		}
		data = append(data, settingJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

func (a *App) settingJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	e, err := a.entry(r.Context(), r.PathValue("setting_key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingJSON(e))
}

// settingValue accepts a JSON string as is and any other JSON value as its text.
func settingValue(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil || v == nil {
		return "", domain.Invalid("value", "value is required")
	}
	return string(raw), nil
}

func (a *App) setSettingJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	value, err := settingValue(body.Value)
	if err != nil {
		writeErr(w, err)
		return
	}
	key := r.PathValue("setting_key")
	if _, err := a.Settings.Set(r.Context(), key, value, p); err != nil {
		writeErr(w, settingErr(err))
		return
	}
	a.settingJSON(w, r)
}

func (a *App) resetSettingJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, err := a.Settings.Reset(r.Context(), r.PathValue("setting_key"), p); err != nil {
		writeErr(w, settingErr(err))
		return
	}
	a.settingJSON(w, r)
}

// settingErr turns registry validation into a 400 and an unknown key into a 404.
func settingErr(err error) error {
	var api *domain.APIError
	if errors.As(err, &api) {
		return err
	}
	if strings.Contains(err.Error(), "unknown") {
		return domain.NotFound("setting")
	}
	return domain.Invalid("value", err.Error())
}

// auditJSON serves the shared ledger to an admin. Each row then keeps its record's
// practice read scope: a row about a matter behind a wall the caller is not on is
// dropped, and attorney-only values are redacted, exactly as matter history does.
func (a *App) auditJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	entity, id := r.URL.Query().Get("entity_type"), r.URL.Query().Get("entity_id")
	var rows []domain.AuditEntry
	if entity != "" && id != "" {
		if rows, err = a.Audit.Rows(r.Context(), audit.RowQuery{Entity: entity, EntityID: domain.ID(id)}); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		// ponytail: ceiling: without an entity id this reads the whole ledger and returns one page.
		// Upgrade path: cursor pagination over (created_at, id) in audit.Postgres. Trigger: an install passes 100k audit rows.
		page, err := a.ledger.List(r.Context(), repository.Filter{})
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, e := range page.Items {
			if entity == "" || e.Entity == entity {
				rows = append(rows, e)
			}
		}
	}
	shown := a.Practice.AuditLog(r.Context(), p, rows)
	data := make([]map[string]any, 0, len(shown))
	for _, row := range shown {
		data = append(data, practice.HistoryItem(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

func tokenJSON(t identity.TokenView) map[string]any {
	return map[string]any{
		"id": t.ID, "created_at": t.CreatedAt.UTC().Format(time.RFC3339), "created_by": t.UserID,
		"updated_at": t.CreatedAt.UTC().Format(time.RFC3339), "name": t.Name, "owner_user_id": t.UserID,
		"module_id": t.ModuleID, "scopes": t.Scopes, "revoked": t.Revoked,
	}
}

func (a *App) tokensJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	list, err := a.Identity.ListTokens(r.Context(), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(list))
	for _, t := range list {
		data = append(data, tokenJSON(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

func (a *App) issueTokenJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name     string    `json:"name"`
		UserID   domain.ID `json:"owner_user_id"`
		ModuleID string    `json:"module_id"`
		Scopes   []string  `json:"scopes"`
		Approved []string  `json:"approved_scopes"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	// A token is bound to a user or to a module, never both: only a user token
	// defaults to the caller.
	if body.UserID.IsZero() && body.ModuleID == "" {
		body.UserID = c.Principal.UserID
	}
	out, err := a.Identity.IssueToken(r.Context(), c, identity.Issue{Name: body.Name, UserID: body.UserID, ModuleID: body.ModuleID, Scopes: body.Scopes, Approved: body.Approved})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tokenJSON(out.Token), "secret": out.Secret})
}

func (a *App) revokeTokenJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.RevokeToken(r.Context(), c, domain.ID(r.PathValue("token_id"))); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
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
	m.HandleFunc("POST /mcp", a.mcp)

	m.HandleFunc("GET /{$}", a.home)
	m.HandleFunc("GET /setup", a.setupPage)
	m.HandleFunc("POST /setup", a.setupPost)
	m.HandleFunc("POST /setup/confirm", a.confirmPost)
	m.HandleFunc("GET /login", a.loginPage)
	m.HandleFunc("POST /login", a.loginPost)
	m.HandleFunc("POST /login/totp", a.totpPost)
	m.HandleFunc("POST /logout", a.logoutPost)
	m.HandleFunc("GET /admin/settings", a.settingsPage)
	m.HandleFunc("POST /admin/settings/{setting_key}", a.settingsPost)
	m.HandleFunc("POST /admin/settings/{setting_key}/reset", a.settingsResetPost)
	m.Handle("/", a.practice)
	a.mux = m
}

// openBeforeSetup are the paths a fresh install serves before the first admin exists.
func openBeforeSetup(path string) bool {
	switch path {
	case "/healthz", "/api/v1/health", "/api/v1/openapi.json", "/api/v1/setup", "/setup", "/setup/confirm",
		"/api/v1/auth/totp/enroll", "/api/v1/auth/totp/confirm", "/favicon.ico":
		return true
	}
	return strings.HasPrefix(path, "/practice/static/")
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

func safeMethod(m string) bool { return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions }

// caller authenticates a cookie session or a bearer token. Cookie writes need the CSRF
// token from sign-in, in X-CSRF-Token or a csrf form field. A bearer call needs every
// scope the contract lists for that route; a route the contract does not list is denied.
func (a *App) caller(r *http.Request) (identity.Caller, error) {
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
		return identity.Caller{}, err
	}
	if in.Bearer != "" {
		t, ok := a.toolFor(r.Method, r.URL.Path)
		if !ok {
			return identity.Caller{}, &domain.APIError{Code: "scope_denied", Message: "this route is not available to tokens"}
		}
		if err := c.RequireScopes(t.Scopes...); err != nil {
			return identity.Caller{}, err
		}
	}
	return c, nil
}

// principal is the hook practice and every core route use. Services check roles,
// walls and attorney-only redaction against it.
func (a *App) principal(r *http.Request) (domain.Principal, error) {
	c, err := a.caller(r)
	return c.Principal, err
}

// status maps identity codes the shared table does not know.
func status(err error) int {
	var api *domain.APIError
	if !errors.As(err, &api) {
		return http.StatusInternalServerError
	}
	switch api.Code {
	case "csrf_required", "scope_denied":
		return http.StatusForbidden
	case "already_set_up", "setup_required":
		return http.StatusConflict
	case "ambiguous_auth":
		return http.StatusBadRequest
	case "locked_out":
		return http.StatusTooManyRequests
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
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	u, err := a.Users.Get(r.Context(), c.Principal.UserID)
	if err != nil {
		writeErr(w, domain.Unauthenticated("sign in is required"))
		return
	}
	writeJSON(w, http.StatusOK, userJSON(u))
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

func (a *App) auditJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	q := audit.RowQuery{Entity: r.URL.Query().Get("entity_type"), EntityID: domain.ID(r.URL.Query().Get("entity_id"))}
	rows, err := a.Audit.Rows(r.Context(), q)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		kind := "user"
		if e.Source == string(domain.SourceSystem) {
			kind = "system"
		} else if e.Source == string(domain.SourceModule) {
			kind = "module"
		}
		data = append(data, map[string]any{
			"id": e.ID, "at": e.CreatedAt.UTC().Format(time.RFC3339Nano), "actor_id": e.ActorID, "actor_kind": kind,
			"role": e.Role, "source": e.Source, "entity_type": e.Entity, "entity_id": e.EntityID, "field": e.Field,
			"old_value": e.OldValue, "new_value": e.NewValue, "reason": e.Reason, "kind": "change",
		})
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
	if body.UserID.IsZero() {
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

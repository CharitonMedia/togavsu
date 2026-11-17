package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// clientAccessRoutes mounts CORE-14 client portal access management.
func (a *App) clientAccessRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/client-access", a.getClientAccessJSON)
	m.HandleFunc("POST /api/v1/client-access", a.grantClientAccessJSON)
	m.HandleFunc("POST /api/v1/client-access/{user_id}/temporary-password", a.tempPasswordClientAccessJSON)
	m.HandleFunc("POST /api/v1/client-access/{user_id}/deactivate", a.deactivateClientAccessJSON)
	m.HandleFunc("POST /api/v1/client-access/{user_id}/reactivate", a.reactivateClientAccessJSON)
}

// passwordChangeGate forces clients with must_change_password onto /portal/security
// and answers 403 password_change_required on every other API or /mcp call (CORE-14).
// Static assets stay open so the security page can load; every other page redirects.
func (a *App) passwordChangeGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/api/v1/me/password" || path == "/api/v1/auth/logout" ||
			path == "/portal/security" || path == "/portal/security/totp" || path == "/portal/security/totp/confirm" ||
			path == "/portal/sign-out" || passwordChangeStatic(path) {
			next.ServeHTTP(w, r)
			return
		}
		r = memoCaller(r)
		c, err := a.caller(r)
		if err != nil || !c.Principal.MustChangePassword || !modules.ClientOnly(c.Principal) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(path, "/api/") || path == "/mcp" {
			writeErr(w, &domain.APIError{Code: "password_change_required", Message: "change your password before continuing"})
			return
		}
		http.Redirect(w, r, "/portal/security", http.StatusSeeOther)
	})
}

func passwordChangeStatic(path string) bool {
	if uiOpenPath(path) {
		return true
	}
	for _, p := range staticPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func (a *App) clientAccessCaller(w http.ResponseWriter, r *http.Request, write bool) (domain.Principal, bool) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return p, false
	}
	if err := clientAccessStaff(p); err != nil {
		writeErr(w, err)
		return p, false
	}
	scope := "client_access:read"
	if write {
		scope = "client_access:write"
	}
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return p, false
	}
	if err := c.RequireScopes(scope); err != nil {
		writeErr(w, shared(err))
		return p, false
	}
	return p, true
}

func clientAccessStaff(p domain.Principal) error {
	if p.UserID.IsZero() && p.ModuleID == "" {
		return domain.Unauthenticated("sign in is required")
	}
	if p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff) {
		return nil
	}
	return domain.Forbidden("admin, attorney or staff role is required")
}

func clientOnlyUser(u domain.User) error {
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil {
		return err
	}
	if len(roles) != 1 || roles[0] != domain.RoleClient {
		return domain.Conflict("user holds a role besides client")
	}
	return nil
}

func (a *App) getClientAccessJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := a.clientAccessCaller(w, r, false)
	if !ok {
		return
	}
	contact := domain.ID(strings.TrimSpace(r.URL.Query().Get("contact_id")))
	if contact.IsZero() {
		writeErr(w, domain.Invalid("contact_id", "contact_id is required"))
		return
	}
	if err := a.Practice.AssertReadableClientParty(r.Context(), p, contact); err != nil {
		writeErr(w, err)
		return
	}
	u, found, err := a.Identity.UserByContact(r.Context(), contact)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		writeErr(w, domain.NotFound("client_access"))
		return
	}
	if err := clientOnlyUser(u); err != nil {
		writeErr(w, err)
		return
	}
	last, err := a.Identity.LastSignInAt(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{
		"user_id": u.ID, "email": u.Email, "active": u.Active,
		"must_change_password": u.MustChangePassword,
		"granted_at":           u.CreatedAt.UTC().Format(time.RFC3339),
	}
	if last != nil {
		out["last_sign_in_at"] = last.UTC().Format(time.RFC3339)
	} else {
		out["last_sign_in_at"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) grantClientAccessJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := a.clientAccessCaller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ContactID domain.ID `json:"contact_id"`
		Email     string    `json:"email"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.ContactID.IsZero() {
		writeErr(w, domain.Invalid("contact_id", "contact_id is required"))
		return
	}
	if err := a.Practice.AssertReadableClientParty(r.Context(), p, body.ContactID); err != nil {
		writeErr(w, err)
		return
	}
	email := strings.TrimSpace(body.Email)
	if email == "" {
		email = a.Practice.PrimaryContactEmail(body.ContactID)
	}
	if email == "" {
		writeErr(w, domain.Invalid("email", "contact has no primary email"))
		return
	}
	temp, err := identity.TemporaryPassword()
	if err != nil {
		writeErr(w, err)
		return
	}
	u, found, err := a.Identity.UserByContact(r.Context(), body.ContactID)
	if err != nil {
		writeErr(w, err)
		return
	}
	created := false
	if found {
		if err := clientOnlyUser(u); err != nil {
			writeErr(w, err)
			return
		}
		if err := a.Identity.SetTemporaryPassword(r.Context(), p, u.ID, temp); err != nil {
			writeErr(w, err)
			return
		}
		u, err = a.Identity.GetUser(r.Context(), u.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
	} else {
		name, err := a.Practice.ContactDisplayName(r.Context(), body.ContactID)
		if err != nil {
			writeErr(w, err)
			return
		}
		u, err = a.Identity.CreateClientPortalUser(r.Context(), p, name, email, body.ContactID, temp)
		if err != nil {
			writeErr(w, err)
			return
		}
		created = true
	}
	mailStatus := a.mailClientAccess(r.Context(), u.Email, temp)
	_ = a.publish(r.Context(), p, "client_access.granted", "user", u.ID, "Client portal access granted")
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": u.ID, "created": created, "email": u.Email, "mail_status": mailStatus,
	})
}

func (a *App) tempPasswordClientAccessJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := a.clientAccessCaller(w, r, true)
	if !ok {
		return
	}
	u, err := a.loadClientAccessUser(r.Context(), p, domain.ID(r.PathValue("user_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	temp, err := identity.TemporaryPassword()
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.SetTemporaryPassword(r.Context(), p, u.ID, temp); err != nil {
		writeErr(w, err)
		return
	}
	_ = a.publish(r.Context(), p, "client_access.password_reset", "user", u.ID, "Client portal temporary password set")
	writeJSON(w, http.StatusOK, map[string]any{"temporary_password": temp})
}

func (a *App) deactivateClientAccessJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := a.clientAccessCaller(w, r, true)
	if !ok {
		return
	}
	u, err := a.loadClientAccessUser(r.Context(), p, domain.ID(r.PathValue("user_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.SetUserActive(r.Context(), p, u.ID, false); err != nil {
		writeErr(w, err)
		return
	}
	_ = a.publish(r.Context(), p, "client_access.deactivated", "user", u.ID, "Client portal access deactivated")
	u, err = a.Identity.GetUser(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientAccessStatus(u))
}

func (a *App) reactivateClientAccessJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := a.clientAccessCaller(w, r, true)
	if !ok {
		return
	}
	u, err := a.loadClientAccessUser(r.Context(), p, domain.ID(r.PathValue("user_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.SetUserActive(r.Context(), p, u.ID, true); err != nil {
		writeErr(w, err)
		return
	}
	_ = a.publish(r.Context(), p, "client_access.reactivated", "user", u.ID, "Client portal access reactivated")
	u, err = a.Identity.GetUser(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientAccessStatus(u))
}

func clientAccessStatus(u domain.User) map[string]any {
	return map[string]any{
		"user_id": u.ID, "email": u.Email, "active": u.Active,
		"must_change_password": u.MustChangePassword,
	}
}

func (a *App) loadClientAccessUser(ctx context.Context, p domain.Principal, userID domain.ID) (domain.User, error) {
	u, err := a.Identity.GetUser(ctx, userID)
	if err != nil {
		return domain.User{}, err
	}
	if err := clientOnlyUser(u); err != nil {
		return domain.User{}, err
	}
	if u.ContactID.IsZero() {
		return domain.User{}, domain.NotFound("user")
	}
	if err := a.Practice.AssertReadableClientParty(ctx, p, u.ContactID); err != nil {
		return domain.User{}, domain.NotFound("user")
	}
	return u, nil
}

func (a *App) mailClientAccess(ctx context.Context, to, temporaryPassword string) string {
	if a.sysMail == nil {
		return "skipped"
	}
	text := "You have access to the client portal.\n\n" +
		"Sign-in email: " + to + "\n" +
		"Temporary password: " + temporaryPassword + "\n"
	if url := a.clientAccessSignInURL(); url != "" {
		text += "Sign-in address: " + url + "\n"
	}
	text += "\nSign in and change your password before using the portal."
	err := a.sysMail.deliver(ctx, systemMail{
		Kind: "client_access", To: []string{to},
		Subject: "Your client portal access", Text: text,
	})
	if err == nil {
		return "sent"
	}
	if errors.Is(err, errMailNotConfigured) {
		return "skipped"
	}
	return "failed"
}

// clientAccessSignInURL is the absolute portal sign-in link (CORE-14 / CORE-04 style).
func (a *App) clientAccessSignInURL() string {
	host := ""
	if a != nil && a.Trust != nil {
		host = strings.TrimSpace(a.Trust.FirmDomain)
	}
	if host == "" {
		return ""
	}
	return "https://" + host + "/login"
}

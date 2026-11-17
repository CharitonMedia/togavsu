package core

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func (a *App) portalRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /portal/security", a.portalSecurityPage)
	m.HandleFunc("POST /portal/security", a.portalSecurityPost)
	m.HandleFunc("POST /portal/security/totp", a.portalTOTPPost)
	m.HandleFunc("POST /portal/security/totp/confirm", a.portalTOTPConfirmPost)
	m.HandleFunc("POST /portal/sign-out", a.portalSignOut)
}

func (a *App) portalSecurityPage(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !modules.ClientOnly(p) {
		http.NotFound(w, r)
		return
	}
	render(w, http.StatusOK, "portal_security", a.portalSecurityView(r, p, ""))
}

func (a *App) portalSecurityView(r *http.Request, p domain.Principal, errMsg string) view {
	v := view{Title: "Security", CSRF: csrfOf(r), Error: errMsg, Form: map[string]string{}}
	if a.Identity != nil && !p.UserID.IsZero() {
		if on, err := a.Identity.TOTPConfirmed(r.Context(), p.UserID); err == nil && on {
			v.Form["totp_on"] = "1"
		}
	}
	return v
}

func (a *App) portalSecurityPost(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !modules.ClientOnly(c.Principal) {
		http.NotFound(w, r)
		return
	}
	current, next, confirm := r.FormValue("current_password"), r.FormValue("new_password"), r.FormValue("confirm_password")
	if next != confirm {
		render(w, http.StatusBadRequest, "portal_security", a.portalSecurityView(r, c.Principal, "The new passwords do not match."))
		return
	}
	if err := a.Identity.ChangePassword(r.Context(), c, current, next); err != nil {
		render(w, status(err), "portal_security", a.portalSecurityView(r, c.Principal, message(err)))
		return
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// portalTOTPPost starts optional TOTP enrollment for a client portal user (CORE-10),
// reusing identity.ResetTOTP with the current password as the factor.
func (a *App) portalTOTPPost(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !modules.ClientOnly(c.Principal) {
		http.NotFound(w, r)
		return
	}
	en, err := a.Identity.ResetTOTP(r.Context(), c, identity.Factor{Password: r.FormValue("password")})
	if err != nil {
		render(w, status(err), "portal_security", a.portalSecurityView(r, c.Principal, message(err)))
		return
	}
	v := a.portalSecurityView(r, c.Principal, "")
	v.Enroll = en
	render(w, http.StatusOK, "portal_security", v)
}

func (a *App) portalTOTPConfirmPost(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !modules.ClientOnly(c.Principal) {
		http.NotFound(w, r)
		return
	}
	codes, err := a.Identity.ConfirmReset(r.Context(), c, r.FormValue("enrollment_token"), r.FormValue("code"))
	if err != nil {
		v := a.portalSecurityView(r, c.Principal, message(err))
		v.Enroll = identity.Enrollment{
			EnrollmentToken: r.FormValue("enrollment_token"),
			OTPAuthURI:      r.FormValue("otpauth_uri"),
			Secret:          r.FormValue("secret"),
		}
		render(w, status(err), "portal_security", v)
		return
	}
	v := a.portalSecurityView(r, c.Principal, "")
	v.Form["totp_on"] = "1"
	v.Codes = codes
	render(w, http.StatusOK, "portal_security", v)
}

func (a *App) portalSignOut(w http.ResponseWriter, r *http.Request) {
	if c, err := a.caller(r); err == nil {
		_ = a.Identity.Logout(r.Context(), c)
	}
	signOut(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

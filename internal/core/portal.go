package core

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func (a *App) portalRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /portal/security", a.portalSecurityPage)
	m.HandleFunc("POST /portal/security", a.portalSecurityPost)
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
	render(w, http.StatusOK, "portal_security", view{Title: "Security", CSRF: csrfOf(r)})
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
		render(w, http.StatusBadRequest, "portal_security", view{Title: "Security", CSRF: csrfOf(r), Error: "The new passwords do not match."})
		return
	}
	if err := a.Identity.ChangePassword(r.Context(), c, current, next); err != nil {
		render(w, status(err), "portal_security", view{Title: "Security", CSRF: csrfOf(r), Error: message(err)})
		return
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

func (a *App) portalSignOut(w http.ResponseWriter, r *http.Request) {
	if c, err := a.caller(r); err == nil {
		_ = a.Identity.Logout(r.Context(), c)
	}
	signOut(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

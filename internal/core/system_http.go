package core

import (
	"net/http"
	"net/mail"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// systemRoutes mounts the System tab's seat count (PRD 3) and the Notifications and mail
// tab's SMTP password and test send (PRD 8.7, 8.8). All are admin only.
func (a *App) systemRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/system/seats", a.seatsJSON)
	m.HandleFunc("PUT /api/v1/mail/smtp-password", a.smtpPasswordJSON)
	m.HandleFunc("POST /api/v1/mail/test", a.testMailJSON)
}

// seatsJSON counts active users. A user with any staff-app role is one seat; a
// client-only user is in by_role and not in active_seats. No license is checked (PRD 3).
func (a *App) seatsJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	users, err := ListUsers(r.Context(), a)
	if err != nil {
		writeErr(w, err)
		return
	}
	seats := 0
	byRole := map[string]int{}
	for _, u := range users {
		if !u.Active {
			continue
		}
		roles, _ := domain.ParseRoles(u.Roles)
		staff := false
		for _, role := range roles {
			byRole[string(role)]++
			staff = staff || role != domain.RoleClient
		}
		if staff {
			seats++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"active_seats": seats, "by_role": byRole})
}

// smtpPasswordJSON stores the SMTP password through the settings registry, which seals it
// and leaves its value out of the audit log.
func (a *App) smtpPasswordJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Password == "" {
		writeErr(w, domain.Invalid("password", "password is required"))
		return
	}
	if _, err := a.Settings.Set(r.Context(), "mail.smtp_password", body.Password, p); err != nil {
		writeErr(w, settingErr(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testMailJSON sends one message through the current SMTP settings, so the admin sees at
// once whether system mail works.
func (a *App) testMailJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	to := strings.TrimSpace(body.To)
	if addr, err := mail.ParseAddress(to); err != nil || addr.Address != to {
		writeErr(w, domain.Invalid("to", "to must be one email address"))
		return
	}
	err := smtpMailer{Settings: a.Settings}.Send(r.Context(), billing.Message{
		To: []string{to}, Subject: "Togavsu system mail test",
		Text: "This is a test of Togavsu's system mail settings. No action is needed.",
	})
	if err != nil {
		writeErr(w, domain.Invalid("mail", "the test message was not sent: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

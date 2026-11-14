package core

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// identityRoutes mounts the contract's user, session, password, two-factor reset,
// recovery-code and token-detail operations (PRD 4, 5 and 8.17 Profile). Identity and the
// admin page's Accounts check roles in the service layer; authenticate has already
// required the route's contract scopes of a bearer caller.
func (a *App) identityRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/auth/password-resets", a.passwordResetJSON)
	m.HandleFunc("POST /api/v1/auth/password-resets/confirm", a.passwordResetConfirmJSON)
	m.HandleFunc("PATCH /api/v1/me", a.updateMeJSON)
	m.HandleFunc("POST /api/v1/me/password", a.changePasswordJSON)
	m.HandleFunc("POST /api/v1/me/totp/reset", a.totpResetJSON)
	m.HandleFunc("POST /api/v1/me/totp/reset/confirm", a.totpResetConfirmJSON)
	m.HandleFunc("POST /api/v1/me/recovery-codes", a.recoveryCodesJSON)
	m.HandleFunc("GET /api/v1/sessions", a.sessionsJSON)
	m.HandleFunc("DELETE /api/v1/sessions/{session_id}", a.deleteSessionJSON)
	m.HandleFunc("GET /api/v1/api-tokens/{token_id}", a.tokenJSON)
	m.HandleFunc("GET /api/v1/users", a.usersJSON)
	m.HandleFunc("POST /api/v1/users", a.createUserJSON)
	m.HandleFunc("GET /api/v1/users/{user_id}", a.userJSON)
	m.HandleFunc("PATCH /api/v1/users/{user_id}", a.updateUserJSON)
	m.HandleFunc("POST /api/v1/users/{user_id}/deactivate", a.deactivateUserJSON)
}

// accounts is the admin page's user service, set when the ui mounts.
func (a *App) accounts() (*dashboard.Accounts, error) {
	if a.UI == nil || a.UI.Accounts == nil {
		return nil, domain.NotFound("route")
	}
	return a.UI.Accounts, nil
}

func userRowJSON(row dashboard.UserRow) map[string]any {
	out := userJSON(row.User)
	if row.Phone != "" {
		out["phone"] = row.Phone
	}
	out["default_hourly_rate_cents"] = int64(row.User.DefaultHourlyRateCents)
	if row.User.BarNumber != "" {
		out["bar_number"] = row.User.BarNumber
	}
	if row.User.BarState != "" {
		out["bar_state"] = row.User.BarState
	}
	if row.User.ContactID.IsZero() {
		out["linked_contact_id"] = nil
	} else {
		out["linked_contact_id"] = row.User.ContactID
	}
	return out
}

// userBody is UserCreate and UserWritePatch.
type userBody struct {
	Name      *string    `json:"name"`
	Email     *string    `json:"email"`
	Phone     *string    `json:"phone"`
	Roles     *[]string  `json:"roles"`
	Password  string     `json:"password"`
	RateCents *int64     `json:"default_hourly_rate_cents"`
	BarNumber *string    `json:"bar_number"`
	BarState  *string    `json:"bar_state"`
	ContactID *domain.ID `json:"linked_contact_id"`
	Initials  *string    `json:"initials"`
}

func (b userBody) input() (dashboard.UserInput, error) {
	in := dashboard.UserInput{Name: b.Name, Email: b.Email, Phone: b.Phone, Password: b.Password,
		RateCents: b.RateCents, BarNumber: b.BarNumber, BarState: b.BarState, ContactID: b.ContactID}
	if b.Roles != nil {
		roles, err := dashboard.ParseRoleList(*b.Roles)
		if err != nil {
			return in, domain.Invalid("roles", "roles are invalid")
		}
		in.Roles = &roles
	}
	return in, nil
}

func (a *App) usersJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	acc, err := a.accounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := acc.UsersFor(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, userRowJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

func (a *App) userJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	acc, err := a.accounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	row, err := acc.User(r.Context(), p, domain.ID(r.PathValue("user_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userRowJSON(row))
}

func (a *App) createUserJSON(w http.ResponseWriter, r *http.Request) {
	a.writeUser(w, r, "")
}

func (a *App) updateUserJSON(w http.ResponseWriter, r *http.Request) {
	a.writeUser(w, r, domain.ID(r.PathValue("user_id")))
}

// writeUser creates a user (id empty) or applies an admin patch, then answers the row.
func (a *App) writeUser(w http.ResponseWriter, r *http.Request, id domain.ID) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	acc, err := a.accounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	var body userBody
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	in, err := body.input()
	if err != nil {
		writeErr(w, err)
		return
	}
	code := http.StatusOK
	var u domain.User
	if id.IsZero() {
		code = http.StatusCreated
		u, err = acc.CreateUser(r.Context(), p, in)
	} else {
		u, err = acc.UpdateUser(r.Context(), p, id, in)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	row, err := acc.User(r.Context(), p, u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, code, userRowJSON(row))
}

func (a *App) deactivateUserJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	acc, err := a.accounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("user_id"))
	if err := acc.DeactivateUser(r.Context(), p, id); err != nil {
		writeErr(w, err)
		return
	}
	row, err := acc.User(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userRowJSON(row))
}

func (a *App) updateMeJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	acc, err := a.accounts()
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Phone    *string `json:"phone"`
		Initials *string `json:"initials"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	me, err := acc.UpdateMe(r.Context(), p, body.Name, body.Phone, body.Initials)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := userJSON(me.User)
	out["phone"], out["initials"] = me.Phone, me.Initials
	writeJSON(w, http.StatusOK, out)
}

func (a *App) changePasswordJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.ChangePassword(r.Context(), c, body.Current, body.Next); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// factorBody is FactorReauthentication: exactly one proof of an existing factor.
type factorBody struct {
	Password string `json:"current_password"`
	TOTP     string `json:"totp_code"`
	Recovery string `json:"recovery_code"`
	Reauth   string `json:"reauthentication_token"`
}

func (b factorBody) factor() (identity.Factor, error) {
	errFactor := domain.Invalid("factor", "give exactly one of current_password, totp_code, recovery_code or reauthentication_token")
	var proof string
	for _, v := range []string{b.Password, b.TOTP, b.Recovery, b.Reauth} {
		if v == "" {
			continue
		}
		if proof != "" {
			return identity.Factor{}, errFactor
		}
		proof = v
	}
	if proof == "" {
		return identity.Factor{}, errFactor
	}
	return identity.Factor{Password: b.Password, TOTPCode: strings.TrimSpace(b.TOTP), Recovery: strings.TrimSpace(b.Recovery), ReauthToken: b.Reauth}, nil
}

func (a *App) totpResetJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body factorBody
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	f, err := body.factor()
	if err != nil {
		writeErr(w, err)
		return
	}
	en, err := a.Identity.ResetTOTP(r.Context(), c, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "totp_reset_pending", "enrollment_token": en.EnrollmentToken,
		"expires_at": en.ExpiresAt.UTC().Format(time.RFC3339), "otpauth_uri": en.OTPAuthURI, "secret": en.Secret,
	})
}

func (a *App) totpResetConfirmJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Token string `json:"enrollment_token"`
		Code  string `json:"code"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	// The contract's response is User, so recovery codes from a first enrollment are not
	// returned here; regenerateRecoveryCodes issues a fresh set for an API client.
	if _, err := a.Identity.ConfirmReset(r.Context(), c, body.Token, strings.TrimSpace(body.Code)); err != nil {
		writeErr(w, err)
		return
	}
	u, err := a.Users.Get(r.Context(), c.Principal.UserID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userJSON(u))
}

func (a *App) recoveryCodesJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body factorBody
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	f, err := body.factor()
	if err != nil {
		writeErr(w, err)
		return
	}
	codes, err := a.Identity.RegenerateRecovery(r.Context(), c, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes})
}

func (a *App) sessionsJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	list, err := a.Identity.ListSessions(r.Context(), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(list))
	for _, s := range list {
		if s.Revoked {
			continue
		}
		// The session id is a UUIDv7, so its time prefix is when it was created.
		created, _ := s.ID.Time()
		data = append(data, map[string]any{
			"id": s.ID, "user_id": s.UserID, "created_at": created.UTC().Format(time.RFC3339),
			"expires_at": s.ExpiresAt.UTC().Format(time.RFC3339), "current": s.Current,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"next_cursor": nil}})
}

func (a *App) deleteSessionJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.DeleteSession(r.Context(), c, domain.ID(r.PathValue("session_id"))); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) tokenJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	t, err := a.Identity.GetToken(r.Context(), c, domain.ID(r.PathValue("token_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenJSON(t))
}

func (a *App) passwordResetJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	// An unknown address answers the same 202, so the route does not reveal accounts.
	if err := a.Identity.RequestPasswordReset(r.Context(), body.Email); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{})
}

func (a *App) passwordResetConfirmJSON(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Next  string `json:"new_password"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := a.Identity.ConfirmPasswordReset(r.Context(), body.Token, body.Next); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetMailer sends the password-reset token by system mail (PRD 8.8: password resets
// are system mail). Until the admin sets the SMTP settings the send fails, the request
// still answers 202, and the admin resets the user's password instead.
type resetMailer struct {
	mail interface {
		Send(context.Context, billing.Message) error
	}
}

func (m resetMailer) SendPasswordReset(email, token string) error {
	// smtpMailer bounds the send with mail.smtp_timeout_seconds.
	err := m.mail.Send(context.Background(), billing.Message{
		To:      []string{email},
		Subject: "Reset your Togavsu password",
		Text: "Someone asked to reset the password for this Togavsu account.\n\n" +
			"Reset token: " + token + "\n\n" +
			"Send it with your new password to POST /api/v1/auth/password-resets/confirm. " +
			"The token works once and expires soon. If you did not ask for this, ignore this message.",
	})
	if err != nil {
		// Logged without the token or the address. Returning the error would answer a
		// known address differently from an unknown one.
		log.Printf("core: password reset mail was not sent: %v", err)
	}
	return nil
}

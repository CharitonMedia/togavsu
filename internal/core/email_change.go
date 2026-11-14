package core

import (
	"context"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// emailChangeRoutes mount the caller's verified email change (PRD 8.7 users; contract
// requestEmailChange and confirmEmailChange). updateMe still refuses an email.
func (a *App) emailChangeRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/me/email-change", a.requestEmailChangeJSON)
	m.HandleFunc("POST /api/v1/me/email-change/confirm", a.confirmEmailChangeJSON)
}

func (a *App) requestEmailChangeJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	raw, err := readStrict(w, r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, _, err := a.idempotentTx(r.Context(), c.Principal, "requestEmailChange", r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		email, err := a.Identity.RequestEmailChange(ctx, c, body.Email)
		if err != nil {
			return idemResult{}, err
		}
		if err := a.publish(ctx, c.Principal, "user.email_change_requested", "user", c.Principal.UserID, "Asked to change the account email"); err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusOK, Body: map[string]any{"status": "verification_sent", "email": email}}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

func (a *App) confirmEmailChangeJSON(w http.ResponseWriter, r *http.Request) {
	c, err := a.caller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	raw, err := readStrict(w, r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	if body.Token == "" {
		writeErr(w, domain.Invalid("token", "token is required"))
		return
	}
	res, _, err := a.idempotentTx(r.Context(), c.Principal, "confirmEmailChange", r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		u, err := a.Identity.ConfirmEmailChange(ctx, c, body.Token)
		if err != nil {
			return idemResult{}, err
		}
		if err := a.publish(ctx, c.Principal, "user.updated", "user", c.Principal.UserID, "Changed the account email"); err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusOK, Body: userJSON(u)}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

// SendEmailChange mails the verification token to the new address. Unlike a password
// reset, a failed send is the caller's error: they asked for this change and must know
// the message did not go.
func (m resetMailer) SendEmailChange(email, token string) error {
	err := m.mail.Send(context.Background(), billing.Message{
		To:      []string{email},
		Subject: "Confirm your new Togavsu email",
		Text: "Someone asked to use this address for a Togavsu account.\n\n" +
			"Confirmation token: " + token + "\n\n" +
			"Signed in to that account, send it to POST /api/v1/me/email-change/confirm. " +
			"The token works once and expires soon. Until then the account keeps its old email. " +
			"If you did not ask for this, ignore this message.",
	})
	if err != nil {
		return domain.Conflict("the verification message was not sent; check system mail")
	}
	return nil
}

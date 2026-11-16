package core

import (
	"context"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// sendModuleMailJSON is POST /api/v1/mail/messages (CORE-04). Delivery uses the
// SMTP/outbox path (MailSender); CORE-05 systemMailer will route through the email
// module when that setting is on.
func (a *App) sendModuleMailJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body trust.ModuleMailWrite
	raw, err := readStrict(w, r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, _, err := a.idempotentTx(r.Context(), p, "sendModuleMail", r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		out, err := a.Trust.SendModuleMail(ctx, p, body)
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusOK, Body: out}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

// trustSMTPSender adapts smtpMailer to trust.Sender for sendModuleMail (CORE-04).
// CORE-05 replaces this with systemMailer.AsSender() when email-module routing is on.
type trustSMTPSender struct{ smtpMailer }

func (s trustSMTPSender) Send(ctx context.Context, to, subject, body string) error {
	return s.smtpMailer.Send(ctx, billing.Message{To: []string{to}, Subject: subject, Text: body})
}

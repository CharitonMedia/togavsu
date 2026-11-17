package core

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

// drainSupervisorMail sends each notice in the supervisor outbox through systemMailer
// (CORE-05). Update-related subjects use kind update_failure so aboutRoutingModule
// forces SMTP when the notice is about the email routing module itself.
func (a *App) drainSupervisorMail(ctx context.Context) (int, error) {
	if a == nil || a.sysMail == nil || a.supervisorMailOutbox == "" {
		return 0, nil
	}
	box := supervisor.OpenOutbox(a.supervisorMailOutbox)
	notices, err := box.Take()
	if err != nil || len(notices) == 0 {
		return 0, err
	}
	admins, err := usersWithRole(ctx, a.Pool, domain.RoleAdmin)
	if err != nil {
		_ = box.PutAll(notices)
		return 0, err
	}
	var to []string
	for _, u := range admins {
		if u.Email != "" {
			to = append(to, u.Email)
		}
	}
	if len(to) == 0 {
		_ = box.PutAll(notices)
		return 0, nil
	}
	var failed []supervisor.Notice
	var sent int
	for _, n := range notices {
		kind := "system"
		if strings.Contains(strings.ToLower(n.Subject), "update") {
			kind = "update_failure"
		}
		id, err := domain.NewID()
		if err != nil {
			failed = append(failed, n)
			continue
		}
		if err := a.sysMail.deliver(ctx, systemMail{
			ID: string(id), Kind: kind, To: to, Subject: n.Subject, Text: n.Body,
		}); err != nil {
			failed = append(failed, n)
			continue
		}
		sent++
	}
	if err := box.PutAll(failed); err != nil && len(failed) > 0 {
		return sent, err
	}
	if len(failed) > 0 {
		return sent, nil
	}
	return sent, nil
}

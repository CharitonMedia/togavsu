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
// Each notice keeps a stable ID used as Idempotency-Key across 5xx/timeout retries.
// Take leaves a .draining file until finishDrain Acks it so a crash mid-send cannot
// orphan the batch.
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
		return 0, finishDrain(box, notices, err)
	}
	var to []string
	for _, u := range admins {
		if u.Email != "" {
			to = append(to, u.Email)
		}
	}
	if len(to) == 0 {
		return 0, finishDrain(box, notices, nil)
	}
	var failed []supervisor.Notice
	var sent int
	for _, n := range notices {
		kind := "system"
		if strings.Contains(strings.ToLower(n.Subject), "update") {
			kind = "update_failure"
		}
		id := n.ID
		if id == "" {
			// Legacy lines without id: mint once and keep on the notice for PutAll retry.
			newID, err := domain.NewID()
			if err != nil {
				failed = append(failed, n)
				continue
			}
			id = string(newID)
			n.ID = id
		}
		if err := a.sysMail.deliver(ctx, systemMail{
			ID: id, Kind: kind, To: to, Subject: n.Subject, Text: n.Body,
		}); err != nil {
			failed = append(failed, n)
			continue
		}
		sent++
	}
	if err := finishDrain(box, failed, nil); err != nil {
		return sent, err
	}
	return sent, nil
}

// finishDrain restores remaining notices then Acks the .draining file. When PutAll
// fails, .draining is left so the next Take recovers the original batch.
func finishDrain(box *supervisor.Outbox, remaining []supervisor.Notice, prior error) error {
	if err := box.PutAll(remaining); err != nil && len(remaining) > 0 {
		if prior != nil {
			return prior
		}
		return err
	}
	if err := box.Ack(); err != nil {
		if prior != nil {
			return prior
		}
		return err
	}
	return prior
}

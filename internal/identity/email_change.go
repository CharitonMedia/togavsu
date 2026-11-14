package identity

import (
	"context"
	"errors"
	"net/mail"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// purposeEmail is a verified email change's challenge.
const purposeEmail = "email"

// EmailChangeMailer sends the verification message for an email change to the new
// address. The core's system mailer implements it next to SendPasswordReset.
type EmailChangeMailer interface {
	SendEmailChange(email, token string) error
}

// duplicateEmail is the contract's duplicate_email problem.
func duplicateEmail() error {
	return &domain.APIError{Code: domain.CodeDuplicateEmail, Message: "another user already has this email", Field: "email"}
}

// checkNewEmail parses one plain address and refuses one another user holds.
func (s *Service) checkNewEmail(ctx context.Context, user domain.User, raw string) (string, error) {
	email := normalizeEmail(raw)
	addr, err := mail.ParseAddress(email)
	if email == "" || err != nil || addr.Address != email || addr.Name != "" {
		return "", domain.Invalid("email", "email must be one plain address")
	}
	if email == normalizeEmail(user.Email) {
		return "", domain.Invalid("email", "this is already the account's email")
	}
	other, err := s.userByEmail(ctx, email)
	switch {
	case err == nil && other.ID != user.ID:
		return "", duplicateEmail()
	case err != nil && !errors.Is(err, repository.ErrNotFound):
		return "", err
	}
	return email, nil
}

// RequestEmailChange starts a verified change of the caller's own email (PRD 8.7 users;
// contract requestEmailChange). Nothing on the account changes: a one-use token bound
// to this user and the new address goes to the new address, and only
// ConfirmEmailChange applies it. An address another user holds is duplicate_email.
func (s *Service) RequestEmailChange(ctx context.Context, caller Caller, raw string) (string, error) {
	if err := caller.RequireScopes("profile:write"); err != nil {
		return "", err
	}
	if caller.Principal.UserID.IsZero() {
		return "", domain.Forbidden("an email change is for a signed-in user")
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil || !user.Active {
		return "", domain.Unauthenticated("sign in is required")
	}
	email, err := s.checkNewEmail(ctx, user, raw)
	if err != nil {
		return "", err
	}
	sender, ok := s.mail.(EmailChangeMailer)
	if !ok {
		return "", domain.Conflict("system mail cannot send the verification message")
	}
	token, _, err := s.issueChallenge(ctx, user, purposeEmail, "", email)
	if err != nil {
		return "", err
	}
	if err := s.record(ctx, caller.Principal, audit.Change{Entity: "user", EntityID: user.ID, Field: "email_change",
		Old: user.Email, New: email, Reason: "user.email_change_requested"}); err != nil {
		return "", err
	}
	if err := sender.SendEmailChange(email, token); err != nil {
		return "", err
	}
	return email, nil
}

// ConfirmEmailChange consumes the token and sets only the caller's email. A token issued
// to another user is forbidden and stays unused; an expired, used or forged token is
// unauthenticated. The address is checked again, since another user may have taken it
// since the request.
func (s *Service) ConfirmEmailChange(ctx context.Context, caller Caller, token string) (domain.User, error) {
	if err := caller.RequireScopes("profile:write"); err != nil {
		return domain.User{}, err
	}
	if caller.Principal.UserID.IsZero() {
		return domain.User{}, domain.Forbidden("an email change is for a signed-in user")
	}
	body, err := openChallenge(s.key, token)
	if err != nil {
		return domain.User{}, err
	}
	if body.Purpose == purposeEmail && body.UserID != string(caller.Principal.UserID) {
		return domain.User{}, domain.Forbidden("this token was issued to another user")
	}
	_, sess, user, err := s.loadChallenge(ctx, token, purposeEmail)
	if err != nil {
		return domain.User{}, err
	}
	if user.ID != caller.Principal.UserID {
		return domain.User{}, domain.Forbidden("this token was issued to another user")
	}
	email := body.Action
	err = s.withUser(ctx, user.ID, func() error {
		fresh, err := s.users.Get(ctx, user.ID)
		if err != nil {
			return err
		}
		other, err := s.userByEmail(ctx, email)
		if err == nil && other.ID != user.ID {
			return duplicateEmail()
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		old := fresh.Email
		fresh.Email, fresh.UpdatedAt = email, s.now().UTC()
		// The claim, the new address and its audit row commit together: a refused
		// address leaves the token unused.
		return s.audit.Atomic(ctx, func(ctx context.Context) error {
			if err := s.claimChallenge(ctx, sess, caller.Principal); err != nil {
				return s.claimResult(ctx, fresh, err)
			}
			if err := s.users.Update(ctx, fresh); err != nil {
				if errors.Is(mapConflict(err), repository.ErrConflict) {
					return duplicateEmail()
				}
				return err
			}
			user = fresh
			return s.record(ctx, caller.Principal, audit.Change{Entity: "user", EntityID: user.ID, Field: "email",
				Old: old, New: email, Reason: "user.updated"})
		})
	})
	if err != nil {
		return domain.User{}, err
	}
	return user, nil
}

package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// TemporaryPassword returns a random password long enough for HashPassword.
// Callers must not log, audit or put the value in events.
func TemporaryPassword() (string, error) {
	raw := make([]byte, domain.TempPasswordBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// UserByContact returns the user linked to contact, if any.
func (s *Service) UserByContact(ctx context.Context, contact domain.ID) (domain.User, bool, error) {
	if contact.IsZero() {
		return domain.User{}, false, nil
	}
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return domain.User{}, false, err
	}
	for _, u := range page.Items {
		if u.ContactID == contact {
			return u, true, nil
		}
	}
	return domain.User{}, false, nil
}

// GetUser returns one user by id.
func (s *Service) GetUser(ctx context.Context, id domain.ID) (domain.User, error) {
	u, err := s.users.Get(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.User{}, domain.NotFound("user")
	}
	return u, err
}

// CreateClientPortalUser inserts a client-only portal account with must_change_password.
// Authorization (client_access:write and roles) is the caller's job.
func (s *Service) CreateClientPortalUser(ctx context.Context, actor domain.Principal, name, email string, contact domain.ID, password string) (domain.User, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if name == "" || email == "" || contact.IsZero() || len([]rune(password)) < minPassword() {
		return domain.User{}, domain.Invalid("email", "name, email, contact and password are required")
	}
	roles := []domain.Role{domain.RoleClient}
	encoded, err := domain.MarshalRoles(roles)
	if err != nil {
		return domain.User{}, err
	}
	meta, err := s.meta(actor.UserID)
	if err != nil {
		return domain.User{}, err
	}
	if meta.CreatedBy.IsZero() {
		meta.CreatedBy = domain.SystemUserID
	}
	user := domain.User{
		Meta: meta, Name: name, Email: email, Roles: encoded, Active: true,
		MustChangePassword: true, ContactID: contact,
	}
	err = s.audit.Atomic(ctx, func(ctx context.Context) error {
		if err := s.users.Insert(ctx, user); err != nil {
			if errors.Is(err, repository.ErrConflict) {
				return domain.Conflict("email is already in use")
			}
			return err
		}
		if err := s.setPassword(ctx, user, roles, password, ""); err != nil {
			return err
		}
		return s.record(ctx, actor, audit.Change{
			Entity: "user", EntityID: user.ID, Field: "must_change_password", New: "true", Reason: "client_access.granted",
		})
	})
	if err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// SetTemporaryPassword replaces the password, sets must_change_password and revokes sessions.
// The password value is never audited.
func (s *Service) SetTemporaryPassword(ctx context.Context, actor domain.Principal, userID domain.ID, password string) error {
	if len([]rune(password)) < minPassword() {
		return domain.Invalid("password", "password is too short")
	}
	user, err := s.users.Get(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound("user")
	}
	if err != nil {
		return err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	previous := ""
	if cred, err := s.passwords.GetByUser(ctx, user.ID); err == nil {
		previous = cred.Argon2Hash
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	return s.withUser(ctx, user.ID, func() error {
		if err := s.setPassword(ctx, user, roles, password, previous); err != nil {
			return err
		}
		user.MustChangePassword = true
		user.UpdatedAt = s.now().UTC()
		if err := s.users.Update(ctx, user); err != nil {
			return err
		}
		if err := s.record(ctx, actor, audit.Change{
			Entity: "user", EntityID: user.ID, Field: "must_change_password", Old: "false", New: "true", Reason: "client_access.password_reset",
		}); err != nil {
			return err
		}
		return s.revokeOtherSessions(ctx, user.ID, "", actor)
	})
}

// SetUserActive turns a user on or off. Sessions stop authenticating when inactive.
func (s *Service) SetUserActive(ctx context.Context, actor domain.Principal, userID domain.ID, active bool) error {
	user, err := s.users.Get(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound("user")
	}
	if err != nil {
		return err
	}
	if user.Active == active {
		return nil
	}
	old, next := "true", "false"
	reason := "client_access.deactivated"
	if active {
		old, next = "false", "true"
		reason = "client_access.reactivated"
	}
	user.Active = active
	user.UpdatedAt = s.now().UTC()
	return s.audit.Atomic(ctx, func(ctx context.Context) error {
		if err := s.users.Update(ctx, user); err != nil {
			return err
		}
		return s.record(ctx, actor, audit.Change{
			Entity: "user", EntityID: user.ID, Field: "active", Old: old, New: next, Reason: reason,
		})
	})
}

// LastSignInAt is the newest browser session created_at for the user, if any.
func (s *Service) LastSignInAt(ctx context.Context, userID domain.ID) (*time.Time, error) {
	page, err := s.sessions.List(ctx, repository.Filter{UserID: userID})
	if err != nil {
		return nil, err
	}
	var latest *time.Time
	for _, row := range page.Items {
		if !browserSession(row) {
			continue
		}
		t := row.CreatedAt.UTC()
		if latest == nil || t.After(*latest) {
			latest = &t
		}
	}
	return latest, nil
}

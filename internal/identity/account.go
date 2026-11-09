package identity

import (
	"context"
	"encoding/base64"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// SetupInput fields are checked here. A second setup is refused once an admin exists.

// Setup creates the firm settings and the first admin, then returns a TOTP enrollment.
// It does not open a session.
func (s *Service) Setup(ctx context.Context, in SetupInput) (Enrollment, error) {
	if err := ctx.Err(); err != nil {
		return Enrollment{}, err
	}
	if in.FirmName == "" || in.AdminName == "" || normalizeEmail(in.AdminEmail) == "" {
		return Enrollment{}, domain.Invalid("firm_name", "firm name, admin name, and email are required")
	}
	if len(in.Password) < minPassword() || len(in.BackupPassphrase) < minPassword() {
		return Enrollment{}, domain.Invalid("password", "password is too short")
	}
	if _, err := domain.LoadZone(in.TimeZone); err != nil {
		return Enrollment{}, domain.Invalid("time_zone", "time zone is not recognized")
	}
	admins, err := s.activeAdmins(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	if len(admins) != 0 {
		return Enrollment{}, apiCode("already_set_up", "firm is already set up")
	}
	roles, err := domain.MarshalRoles([]domain.Role{domain.RoleAdmin})
	if err != nil {
		return Enrollment{}, err
	}
	meta, err := s.meta(domain.SystemUserID)
	if err != nil {
		return Enrollment{}, err
	}
	user := domain.User{
		Meta: meta, Name: in.AdminName, Email: normalizeEmail(in.AdminEmail),
		Roles: roles, Active: true,
	}
	user.CreatedBy = user.ID
	if err := s.users.Insert(ctx, user); err != nil {
		return Enrollment{}, err
	}
	if err := s.setPassword(ctx, user, []domain.Role{domain.RoleAdmin}, in.Password, ""); err != nil {
		user.Active = false
		_ = s.users.Update(ctx, user)
		return Enrollment{}, err
	}
	who := principalOf(user, []domain.Role{domain.RoleAdmin})
	if err := s.record(ctx, who, audit.Change{
		Entity: "user", EntityID: user.ID, Field: "email", New: user.Email, Reason: "user.created",
	}); err != nil {
		return Enrollment{}, err
	}
	for _, pair := range []struct{ key, value string }{
		{"firm.name", in.FirmName},
		{"firm.time_zone", in.TimeZone},
		{"backups.passphrase", in.BackupPassphrase},
	} {
		if _, err := s.settings.Set(ctx, pair.key, pair.value, who); err != nil {
			user.Active = false
			_ = s.users.Update(ctx, user)
			return Enrollment{}, err
		}
	}
	return s.beginEnrollment(ctx, user)
}

// Ready reports whether firm name, an active admin, and confirmed admin TOTP are stored.
func (s *Service) Ready(ctx context.Context) (bool, error) {
	name, err := s.settings.Get(ctx, "firm.name")
	if err != nil {
		return false, err
	}
	if name == "" {
		return false, nil
	}
	admins, err := s.activeAdmins(ctx)
	if err != nil || len(admins) == 0 {
		return false, err
	}
	for _, user := range admins {
		row, err := s.totp.GetByUser(ctx, user.ID)
		if err == nil && row.Confirmed {
			return true, nil
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return false, err
		}
	}
	return false, nil
}

func (s *Service) activeAdmins(ctx context.Context) ([]domain.User, error) {
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	var out []domain.User
	for _, user := range page.Items {
		if !user.Active {
			continue
		}
		roles, err := rolesOf(user)
		if err != nil {
			return nil, err
		}
		for _, role := range roles {
			if role == domain.RoleAdmin {
				out = append(out, user)
				break
			}
		}
	}
	return out, nil
}

// NewUser is an admin-created account.
type NewUser struct {
	Name      string
	Email     string
	Password  string
	Roles     []domain.Role
	ContactID domain.ID
}

// CreateUser adds a user. Only an admin may call it.
func (s *Service) CreateUser(ctx context.Context, caller Caller, in NewUser) (domain.User, error) {
	if !caller.Principal.HasRole(domain.RoleAdmin) {
		return domain.User{}, domain.Forbidden("admin role is required")
	}
	if err := caller.RequireScopes("users:write"); err != nil {
		return domain.User{}, err
	}
	email := normalizeEmail(in.Email)
	if in.Name == "" || email == "" || len(in.Password) < minPassword() {
		return domain.User{}, domain.Invalid("email", "name, email, and password are required")
	}
	encoded, err := domain.MarshalRoles(in.Roles)
	if err != nil {
		return domain.User{}, domain.Invalid("roles", "roles are invalid")
	}
	meta, err := s.meta(caller.Principal.UserID)
	if err != nil {
		return domain.User{}, err
	}
	user := domain.User{
		Meta: meta, Name: in.Name, Email: email, Roles: encoded, Active: true, ContactID: in.ContactID,
	}
	if err := s.users.Insert(ctx, user); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return domain.User{}, domain.Conflict("email is already in use")
		}
		return domain.User{}, err
	}
	if err := s.setPassword(ctx, user, in.Roles, in.Password, ""); err != nil {
		return domain.User{}, err
	}
	if err := s.record(ctx, caller.Principal, audit.Change{
		Entity: "user", EntityID: user.ID, Field: "email", New: user.Email, Reason: "user.created",
	}); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

// ChangePassword replaces the caller's password and revokes other sessions.
func (s *Service) ChangePassword(ctx context.Context, caller Caller, current, next string) error {
	if caller.Principal.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return domain.Unauthenticated("sign in is required")
	}
	cred, err := s.passwords.GetByUser(ctx, user.ID)
	if err != nil || !VerifyPassword(current, cred.Argon2Hash) {
		return domain.Unauthenticated("wrong email or password")
	}
	if err := s.setPassword(ctx, user, caller.Principal.Roles, next, cred.Argon2Hash); err != nil {
		return err
	}
	return s.revokeOtherSessions(ctx, user.ID, caller.Principal.SessionID)
}

func (s *Service) setPassword(ctx context.Context, user domain.User, roles []domain.Role, next, previous string) error {
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	existing, err := s.passwords.GetByUser(ctx, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		meta, err := s.meta(user.ID)
		if err != nil {
			return err
		}
		existing = domain.PasswordCredential{Meta: meta, UserID: user.ID, Argon2Hash: hash}
		if err := s.passwords.Insert(ctx, existing); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		existing.Argon2Hash = hash
		existing.UpdatedAt = s.now().UTC()
		if err := s.passwords.Update(ctx, existing); err != nil {
			return err
		}
	}
	return s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "password", EntityID: existing.ID, Field: "argon2_hash",
		Old: previous, New: hash, Secret: true, Reason: "user.password_changed",
	})
}

func (s *Service) revokeOtherSessions(ctx context.Context, userID, keep domain.ID) error {
	page, err := s.sessions.List(ctx, repository.Filter{UserID: userID})
	if err != nil {
		return err
	}
	for _, row := range page.Items {
		if row.ID == keep || row.RevokedAt != nil || row.IdleExpiresAt.IsZero() {
			continue
		}
		if err := s.revoke(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// RequestPasswordReset stores a token for a known user and gives it to the mailer.
// An unknown email returns the same nil error.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	email = normalizeEmail(email)
	if email == "" {
		return domain.Invalid("email", "email is required")
	}
	user, err := s.userByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !user.Active) {
		return nil
	}
	if err != nil {
		return err
	}
	token, _, err := s.issueChallenge(ctx, user, purposePassword, "", "")
	if err != nil {
		return err
	}
	if s.mail == nil {
		return nil
	}
	return s.mail.SendPasswordReset(user.Email, token)
}

// ConfirmPasswordReset sets a new password and revokes sessions.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, next string) error {
	_, sess, user, err := s.loadChallenge(ctx, token, purposePassword)
	if err != nil {
		return err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	cred, err := s.passwords.GetByUser(ctx, user.ID)
	previous := ""
	if err == nil {
		previous = cred.Argon2Hash
	}
	if err := s.setPassword(ctx, user, roles, next, previous); err != nil {
		return err
	}
	if err := s.revoke(ctx, sess); err != nil {
		return err
	}
	return s.revokeOtherSessions(ctx, user.ID, "")
}

// ResetTOTP starts a replacement secret. The current secret stays active.
func (s *Service) ResetTOTP(ctx context.Context, caller Caller, factor Factor) (Enrollment, error) {
	if err := s.checkFactor(ctx, caller, factor, "resetTotp"); err != nil {
		return Enrollment{}, err
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return Enrollment{}, domain.NotFound("user")
	}
	secret, err := newTOTPSecret()
	if err != nil {
		return Enrollment{}, err
	}
	sealed, err := seal(s.key, base64.RawStdEncoding.EncodeToString(secret))
	if err != nil {
		return Enrollment{}, err
	}
	token, exp, err := s.issueChallenge(ctx, user, purposeReset, sealed, "")
	if err != nil {
		return Enrollment{}, err
	}
	if err := s.record(ctx, caller.Principal, audit.Change{
		Entity: "totp", EntityID: user.ID, Field: "reset", New: "pending", Reason: "user.totp_reset_started",
	}); err != nil {
		return Enrollment{}, err
	}
	return Enrollment{
		Status: statusReset, EnrollmentToken: token, ExpiresAt: exp,
		OTPAuthURI: otpauth(user.Email, secret), Secret: encodeSecret(secret),
	}, nil
}

// ConfirmReset activates the pending secret for the signed-in user.
// A wrong code, a replay, or another user's token leaves the current secret in place.
func (s *Service) ConfirmReset(ctx context.Context, caller Caller, enrollmentToken, code string) error {
	body, sess, user, err := s.loadChallenge(ctx, enrollmentToken, purposeReset)
	if err != nil {
		return domain.Forbidden("enrollment token is not valid")
	}
	if caller.Principal.UserID != user.ID {
		return domain.Forbidden("enrollment token is not valid")
	}
	plain, err := openSeal(s.key, body.Secret)
	if err != nil {
		return err
	}
	raw, err := base64.RawStdEncoding.DecodeString(plain)
	if err != nil {
		return err
	}
	if !totpMatch(raw, code, s.now().UTC()) {
		return domain.Unauthenticated("wrong code")
	}
	row, err := s.totp.GetByUser(ctx, user.ID)
	if errors.Is(err, repository.ErrNotFound) {
		meta, err := s.meta(user.ID)
		if err != nil {
			return err
		}
		row = domain.TOTPSecret{Meta: meta, UserID: user.ID}
		row.Ciphertext = body.Secret
		row.Confirmed = true
		if err := s.totp.Insert(ctx, row); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		row.Ciphertext = body.Secret
		row.Confirmed = true
		row.UpdatedAt = s.now().UTC()
		if err := s.totp.Update(ctx, row); err != nil {
			return err
		}
	}
	return s.revoke(ctx, sess)
}

// RegenerateRecovery replaces unused codes after a fresh factor check.
func (s *Service) RegenerateRecovery(ctx context.Context, caller Caller, factor Factor) ([]string, error) {
	if err := s.checkFactor(ctx, caller, factor, "regenerateRecoveryCodes"); err != nil {
		return nil, err
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return nil, domain.NotFound("user")
	}
	return s.replaceRecovery(ctx, user, caller.Principal.Roles)
}

// Reauthenticate issues a single-use token bound to the user and the action.
func (s *Service) Reauthenticate(ctx context.Context, caller Caller, factor Factor, action string) (string, error) {
	if action == "" {
		return "", domain.Invalid("action", "action is required")
	}
	if err := s.checkFactor(ctx, caller, factor, action); err != nil {
		return "", err
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return "", domain.NotFound("user")
	}
	token, _, err := s.issueChallenge(ctx, user, purposeReauth, "", action)
	return token, err
}

func (s *Service) checkFactor(ctx context.Context, caller Caller, factor Factor, action string) error {
	if caller.Principal.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	n := len([]byte{})
	if factor.Password != "" {
		n++
	}
	if factor.TOTPCode != "" {
		n++
	}
	if factor.Recovery != "" {
		n++
	}
	if factor.ReauthToken != "" {
		n++
	}
	if n != match() {
		return domain.Unauthenticated("a current factor is required")
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return domain.Unauthenticated("sign in is required")
	}
	switch {
	case factor.Password != "":
		cred, err := s.passwords.GetByUser(ctx, user.ID)
		if err != nil || !VerifyPassword(factor.Password, cred.Argon2Hash) {
			return domain.Unauthenticated("wrong email or password")
		}
		return nil
	case factor.TOTPCode != "":
		return s.matchStoredTOTP(ctx, user, factor.TOTPCode, true)
	case factor.Recovery != "":
		return s.spendRecovery(ctx, user.ID, factor.Recovery)
	default:
		body, sess, owner, err := s.loadChallenge(ctx, factor.ReauthToken, purposeReauth)
		if err != nil {
			return domain.Unauthenticated("sign in is required")
		}
		if owner.ID != caller.Principal.UserID {
			return domain.Forbidden("enrollment token is not valid")
		}
		if body.Action != action {
			return domain.Forbidden("enrollment token is not valid")
		}
		return s.revoke(ctx, sess)
	}
}

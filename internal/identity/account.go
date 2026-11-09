package identity

import (
	"context"
	"errors"
	"strings"

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
	if len([]rune(in.Password)) < minPassword() || len(in.BackupPassphrase) < minPassword() {
		return Enrollment{}, domain.Invalid("password", "password is too short")
	}
	if _, err := domain.LoadZone(in.TimeZone); err != nil {
		return Enrollment{}, domain.Invalid("time_zone", "time zone is not recognized")
	}
	firm, err := s.settings.Get(ctx, "firm.name")
	if err != nil {
		return Enrollment{}, err
	}
	confirmed, err := s.adminConfirmed(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	if confirmed {
		return Enrollment{}, apiCode("already_set_up", "firm is already set up")
	}
	admins, err := s.activeAdmins(ctx)
	if err != nil {
		return Enrollment{}, err
	}
	if len(admins) != 0 {
		return Enrollment{}, apiCode("already_set_up", "firm is already set up")
	}
	// firm.name is written before the other setup settings. A later failure leaves that name
	// and deactivates the admin. The same email can resume. Any other caller stays closed.
	if firm != "" {
		existing, gerr := s.userByEmail(ctx, normalizeEmail(in.AdminEmail))
		if gerr != nil || !s.rollbackAdmin(ctx, existing) {
			return Enrollment{}, apiCode("already_set_up", "firm is already set up")
		}
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
	user, err = s.claimSetupAdmin(ctx, user)
	if err != nil {
		return Enrollment{}, err
	}
	if err := s.setPassword(ctx, user, []domain.Role{domain.RoleAdmin}, in.Password, ""); err != nil {
		if derr := s.deactivate(ctx, user, []domain.Role{domain.RoleAdmin}); derr != nil {
			return Enrollment{}, derr
		}
		return Enrollment{}, err
	}
	who := principalOf(user, []domain.Role{domain.RoleAdmin})
	if err := s.auditCreatedUser(ctx, who, user); err != nil {
		return Enrollment{}, err
	}
	for _, pair := range []struct{ key, value string }{
		{"firm.name", in.FirmName},
		{"firm.time_zone", in.TimeZone},
		{"backups.passphrase", in.BackupPassphrase},
	} {
		if _, err := s.settings.Set(ctx, pair.key, pair.value, who); err != nil {
			if derr := s.deactivate(ctx, user, []domain.Role{domain.RoleAdmin}); derr != nil {
				return Enrollment{}, derr
			}
			return Enrollment{}, err
		}
	}
	return s.beginEnrollment(ctx, user)
}

// claimSetupAdmin inserts the first admin, or reactivates the inactive row left by a rolled-back setup.
func (s *Service) claimSetupAdmin(ctx context.Context, user domain.User) (domain.User, error) {
	err := s.users.Insert(ctx, user)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, repository.ErrConflict) {
		return domain.User{}, err
	}
	existing, gerr := s.userByEmail(ctx, user.Email)
	if gerr != nil || existing.Active || !s.rollbackAdmin(ctx, existing) {
		return domain.User{}, domain.Conflict("email is already in use")
	}
	existing.Name = user.Name
	existing.Roles = user.Roles
	existing.Active = true
	existing.UpdatedAt = s.now().UTC()
	if uerr := s.users.Update(ctx, existing); uerr != nil {
		return domain.User{}, uerr
	}
	return existing, nil
}

// rollbackAdmin is an inactive admin from a failed setup: no confirmed TOTP, and the audit log records the rollback.
func (s *Service) rollbackAdmin(ctx context.Context, user domain.User) bool {
	roles, err := rolesOf(user)
	if err != nil || !principalOf(user, roles).HasRole(domain.RoleAdmin) {
		return false
	}
	secret, err := s.totp.GetByUser(ctx, user.ID)
	if err == nil && secret.Confirmed {
		return false
	}
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return false
	}
	rows, err := s.audit.Rows(ctx, audit.RowQuery{Entity: "user", EntityID: user.ID, Field: "active"})
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.NewValue == "false" && strings.Contains(row.Reason, "setup rolled back") {
			return true
		}
	}
	return false
}

// adminConfirmed reports whether any admin, including an inactive one, has confirmed TOTP.
// That closes first-run setup for good.
func (s *Service) adminConfirmed(ctx context.Context) (bool, error) {
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return false, err
	}
	for _, user := range page.Items {
		roles, err := rolesOf(user)
		if err != nil || !principalOf(user, roles).HasRole(domain.RoleAdmin) {
			continue
		}
		secret, err := s.totp.GetByUser(ctx, user.ID)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if secret.Confirmed {
			return true, nil
		}
	}
	return false, nil
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
	if in.Name == "" || email == "" || len([]rune(in.Password)) < minPassword() {
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
		if derr := s.markInactive(ctx, user, in.Roles, "user create rolled back"); derr != nil {
			return domain.User{}, derr
		}
		return domain.User{}, err
	}
	if err := s.auditCreatedUser(ctx, caller.Principal, user); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Service) auditCreatedUser(ctx context.Context, who domain.Principal, user domain.User) error {
	active := "false"
	if user.Active {
		active = "true"
	}
	fields := []struct{ field, value string }{
		{"email", user.Email},
		{"name", user.Name},
		{"roles", user.Roles},
		{"active", active},
		{"contact_id", string(user.ContactID)},
	}
	for _, field := range fields {
		if err := s.record(ctx, who, audit.Change{
			Entity: "user", EntityID: user.ID, Field: field.field, New: field.value, Reason: "user.created",
		}); err != nil {
			return err
		}
	}
	return nil
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
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	ok := false
	var previous string
	cred, err := s.passwords.GetByUser(ctx, user.ID)
	if err == nil {
		previous = cred.Argon2Hash
		ok = VerifyPassword(current, cred.Argon2Hash)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	return s.withUser(ctx, user.ID, func() error {
		if err := s.rejectIfLocked(ctx, user); err != nil {
			return err
		}
		if !ok {
			return s.recordFailure(ctx, user, roles)
		}
		if err := s.setPassword(ctx, user, caller.Principal.Roles, next, previous); err != nil {
			return err
		}
		return s.revokeOtherSessions(ctx, user.ID, caller.Principal.SessionID, caller.Principal)
	})
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

func (s *Service) deactivate(ctx context.Context, user domain.User, roles []domain.Role) error {
	return s.markInactive(ctx, user, roles, "setup rolled back")
}

func (s *Service) markInactive(ctx context.Context, user domain.User, roles []domain.Role, reason string) error {
	user.Active = false
	user.UpdatedAt = s.now().UTC()
	if err := s.users.Update(ctx, user); err != nil {
		return err
	}
	return s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "user", EntityID: user.ID, Field: "active", Old: "true", New: "false", Reason: reason,
	})
}

func (s *Service) revokeOtherSessions(ctx context.Context, userID, keep domain.ID, who domain.Principal) error {
	page, err := s.sessions.List(ctx, repository.Filter{UserID: userID})
	if err != nil {
		return err
	}
	for _, row := range page.Items {
		if row.ID == keep || row.RevokedAt != nil {
			continue
		}
		reason := "session.ended"
		if !browserSession(row) {
			reason = "challenge.spent"
		}
		if err := s.revoke(ctx, row, who, reason); err != nil {
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
// The reset token is claimed before the password changes, so one token changes it once.
func (s *Service) ConfirmPasswordReset(ctx context.Context, token, next string) error {
	if len([]rune(next)) < minPassword() {
		return domain.Invalid("password", "password is too short")
	}
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
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	return s.withUser(ctx, user.ID, func() error {
		who := principalOf(user, roles)
		if err := s.claimChallenge(ctx, sess, who); err != nil {
			return s.claimResult(ctx, user, err)
		}
		if err := s.setPassword(ctx, user, roles, next, previous); err != nil {
			return err
		}
		return s.revokeOtherSessions(ctx, user.ID, "", who)
	})
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
	var unused int64
	sealed, err := sealTOTP(s.key, secret, unused, false)
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
	state, raw, err := openTOTP(s.key, body.Secret)
	if err != nil {
		return err
	}
	step, ok := acceptedStep(raw, code, s.now().UTC())
	if !ok || (state.Used && step <= state.Step) {
		return domain.Unauthenticated("wrong code")
	}
	sealed, err := sealTOTP(s.key, raw, step, true)
	if err != nil {
		return err
	}
	row, err := s.totp.GetByUser(ctx, user.ID)
	old := ""
	if errors.Is(err, repository.ErrNotFound) {
		meta, err := s.meta(user.ID)
		if err != nil {
			return err
		}
		row = domain.TOTPSecret{Meta: meta, UserID: user.ID, Ciphertext: sealed, Confirmed: true}
		if err := s.totp.Insert(ctx, row); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		old = row.Ciphertext
		row.Ciphertext = sealed
		row.Confirmed = true
		row.UpdatedAt = s.now().UTC()
		if err := s.totp.Update(ctx, row); err != nil {
			return err
		}
	}
	if err := s.record(ctx, caller.Principal, audit.Change{
		Entity: "totp", EntityID: row.ID, Field: "ciphertext", Old: old, New: sealed, Secret: true, Reason: "user.totp_reset_confirmed",
	}); err != nil {
		return err
	}
	return s.revoke(ctx, sess, caller.Principal, "challenge.spent")
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

func (s *Service) factorFailure(ctx context.Context, user domain.User, roles []domain.Role, cause error) error {
	var api *domain.APIError
	if errors.As(cause, &api) && api.Code == domain.CodeUnauthenticated {
		return s.recordFailure(ctx, user, roles)
	}
	return cause
}

func (s *Service) checkFactor(ctx context.Context, caller Caller, factor Factor, action string) error {
	if caller.Principal.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	var n int
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
	if !singleFactor(n) {
		return domain.Unauthenticated("a current factor is required")
	}
	user, err := s.users.Get(ctx, caller.Principal.UserID)
	if err != nil {
		return domain.Unauthenticated("sign in is required")
	}
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	passwordOK := false
	if factor.Password != "" {
		cred, cerr := s.passwords.GetByUser(ctx, user.ID)
		if cerr == nil {
			passwordOK = VerifyPassword(factor.Password, cred.Argon2Hash)
		} else if !errors.Is(cerr, repository.ErrNotFound) {
			return cerr
		}
	}
	var reauthSess domain.Session
	var reauthOwner domain.User
	var reauthAction string
	if factor.ReauthToken != "" {
		body, sess, owner, lerr := s.loadChallenge(ctx, factor.ReauthToken, purposeReauth)
		if lerr != nil {
			return lerr
		}
		reauthSess = sess
		reauthOwner = owner
		reauthAction = body.Action
	}
	return s.withUser(ctx, user.ID, func() error {
		if err := s.rejectIfLocked(ctx, user); err != nil {
			return err
		}
		switch {
		case factor.Password != "":
			if !passwordOK {
				return s.recordFailure(ctx, user, roles)
			}
		case factor.TOTPCode != "":
			if err := s.matchStoredTOTP(ctx, user, factor.TOTPCode, true); err != nil {
				return s.factorFailure(ctx, user, roles, err)
			}
		case factor.Recovery != "":
			if err := s.spendRecovery(ctx, user, roles, factor.Recovery); err != nil {
				return s.factorFailure(ctx, user, roles, err)
			}
		default:
			if reauthOwner.ID != caller.Principal.UserID {
				return domain.Forbidden("enrollment token is not valid")
			}
			if reauthAction != action {
				return domain.Forbidden("enrollment token is not valid")
			}
			if err := s.claimChallenge(ctx, reauthSess, caller.Principal); err != nil {
				return s.claimResult(ctx, user, err)
			}
		}
		return s.rejectIfLocked(ctx, user)
	})
}

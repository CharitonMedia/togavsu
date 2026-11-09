package identity

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Login checks the password. No session is opened until TOTP is confirmed
// when the role requires it or a secret is already enrolled.
func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return LoginResult{}, domain.Invalid("email", "email and password are required")
	}
	user, lookupErr := s.userByEmail(ctx, email)
	if lookupErr != nil && !errors.Is(lookupErr, repository.ErrNotFound) {
		return LoginResult{}, lookupErr
	}
	hash := ""
	if lookupErr == nil {
		cred, err := s.passwords.GetByUser(ctx, user.ID)
		if err == nil {
			hash = cred.Argon2Hash
		} else if !errors.Is(err, repository.ErrNotFound) {
			return LoginResult{}, err
		}
	}
	if hash == "" {
		hash = dummyHash()
	}
	// One Argon2id check on every path. The dummy hash is used only when no credential is stored.
	ok := VerifyPassword(password, hash)
	if lookupErr != nil || !ok || !user.Active {
		if lookupErr == nil && user.Active {
			return s.failPassword(ctx, user)
		}
		if lookupErr != nil {
			return s.failUnknown(ctx, email)
		}
		return LoginResult{}, domain.Unauthenticated("wrong email or password")
	}
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	locked, err := s.locked(ctx, user)
	if err != nil {
		return LoginResult{}, err
	}
	if locked {
		return LoginResult{}, apiCode("locked_out", "account is locked")
	}
	return s.afterPassword(ctx, user, roles)
}

func (s *Service) failUnknown(ctx context.Context, email string) (LoginResult, error) {
	limit, err := s.intSetting(ctx, "security.lockout_attempts")
	if err != nil {
		return LoginResult{}, err
	}
	window, err := s.duration(ctx, "security.lockout_length")
	if err != nil {
		return LoginResult{}, err
	}
	id := unknownLoginID(email)
	locked, err := s.failureLocked(ctx, "login", id, limit, window)
	if err != nil {
		return LoginResult{}, err
	}
	if locked {
		return LoginResult{}, apiCode("locked_out", "account is locked")
	}
	if err := s.record(ctx, domain.SystemPrincipal(), audit.Change{
		Entity: "login", EntityID: id, Field: signInField, New: "failed", Reason: "unknown account",
	}); err != nil {
		return LoginResult{}, err
	}
	locked, err = s.failureLocked(ctx, "login", id, limit, window)
	if err != nil {
		return LoginResult{}, err
	}
	if locked {
		return LoginResult{}, apiCode("locked_out", "account is locked")
	}
	return LoginResult{}, domain.Unauthenticated("wrong email or password")
}

func (s *Service) failPassword(ctx context.Context, user domain.User) (LoginResult, error) {
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	locked, err := s.locked(ctx, user)
	if err != nil {
		return LoginResult{}, err
	}
	if locked {
		return LoginResult{}, apiCode("locked_out", "account is locked")
	}
	return LoginResult{}, s.recordFailure(ctx, user, roles)
}

func (s *Service) afterPassword(ctx context.Context, user domain.User, roles []domain.Role) (LoginResult, error) {
	required, err := s.totpRequired(ctx, roles)
	if err != nil {
		return LoginResult{}, err
	}
	secret, err := s.totp.GetByUser(ctx, user.ID)
	confirmed := err == nil && secret.Confirmed
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return LoginResult{}, err
	}
	if confirmed {
		token, _, err := s.issueChallenge(ctx, user, purposeTOTP, "", "")
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Status: statusTOTP, TOTPToken: token, User: user}, nil
	}
	if required {
		token, _, err := s.issueChallenge(ctx, user, purposeEnroll, "", "")
		if err != nil {
			return LoginResult{}, err
		}
		return LoginResult{Status: statusEnroll, EnrollmentToken: token, User: user}, nil
	}
	return s.openSession(ctx, user, roles)
}

// StartEnrollment turns a login enrollment token into a one-time secret.
func (s *Service) StartEnrollment(ctx context.Context, enrollmentToken string) (Enrollment, error) {
	_, sess, user, err := s.loadChallenge(ctx, enrollmentToken, purposeEnroll)
	if err != nil {
		return Enrollment{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return Enrollment{}, err
	}
	if err := s.rejectIfLocked(ctx, user); err != nil {
		return Enrollment{}, err
	}
	if err := s.revoke(ctx, sess, principalOf(user, roles), "challenge.spent"); err != nil {
		return Enrollment{}, err
	}
	return s.beginEnrollment(ctx, user)
}

// ConfirmEnrollment checks the first code, stores recovery codes, and opens a session.
func (s *Service) ConfirmEnrollment(ctx context.Context, enrollmentToken, code string) (LoginResult, error) {
	_, sess, user, err := s.loadChallenge(ctx, enrollmentToken, purposeEnroll)
	if err != nil {
		return LoginResult{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.rejectIfLocked(ctx, user); err != nil {
		return LoginResult{}, err
	}
	if err := s.matchStoredTOTP(ctx, user, code, false); err != nil {
		return LoginResult{}, err
	}
	row, err := s.totp.GetByUser(ctx, user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	row.Confirmed = true
	row.UpdatedAt = s.now().UTC()
	if err := s.totp.Update(ctx, row); err != nil {
		return LoginResult{}, err
	}
	codes, err := s.replaceRecovery(ctx, user, roles)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.revoke(ctx, sess, principalOf(user, roles), "challenge.spent"); err != nil {
		return LoginResult{}, err
	}
	if err := s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "totp", EntityID: row.ID, Field: "confirmed", Old: "false", New: "true",
	}); err != nil {
		return LoginResult{}, err
	}
	out, err := s.openSession(ctx, user, roles)
	if err != nil {
		return LoginResult{}, err
	}
	out.RecoveryCodes = codes
	return out, nil
}

// VerifyTOTP finishes sign-in for a user who already enrolled.
func (s *Service) VerifyTOTP(ctx context.Context, totpToken, code string) (LoginResult, error) {
	_, sess, user, err := s.loadChallenge(ctx, totpToken, purposeTOTP)
	if err != nil {
		return LoginResult{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.rejectIfLocked(ctx, user); err != nil {
		return LoginResult{}, err
	}
	if err := s.matchStoredTOTP(ctx, user, code, true); err != nil {
		return s.failSecond(ctx, sess, user, roles, err)
	}
	if err := s.revoke(ctx, sess, principalOf(user, roles), "challenge.spent"); err != nil {
		return LoginResult{}, err
	}
	return s.openSession(ctx, user, roles)
}

// VerifyRecovery spends one recovery code and opens a session.
func (s *Service) VerifyRecovery(ctx context.Context, totpToken, code string) (LoginResult, error) {
	_, sess, user, err := s.loadChallenge(ctx, totpToken, purposeTOTP)
	if err != nil {
		return LoginResult{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	if err := s.rejectIfLocked(ctx, user); err != nil {
		return LoginResult{}, err
	}
	if err := s.spendRecovery(ctx, user, roles, code); err != nil {
		return s.failSecond(ctx, sess, user, roles, err)
	}
	if err := s.revoke(ctx, sess, principalOf(user, roles), "challenge.spent"); err != nil {
		return LoginResult{}, err
	}
	return s.openSession(ctx, user, roles)
}

func (s *Service) failSecond(ctx context.Context, sess domain.Session, user domain.User, roles []domain.Role, cause error) (LoginResult, error) {
	var api *domain.APIError
	if errors.As(cause, &api) && api.Code == domain.CodeUnauthenticated {
		if err := s.revoke(ctx, sess, principalOf(user, roles), "challenge.failed"); err != nil {
			return LoginResult{}, err
		}
	}
	if err := s.recordFailure(ctx, user, roles); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{}, cause
}

func (s *Service) matchStoredTOTP(ctx context.Context, user domain.User, code string, mustConfirmed bool) error {
	row, err := s.totp.GetByUser(ctx, user.ID)
	if err != nil {
		return domain.Unauthenticated("wrong code")
	}
	if mustConfirmed && !row.Confirmed {
		return domain.Unauthenticated("wrong code")
	}
	state, raw, err := openTOTP(s.key, row.Ciphertext)
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
	previous := row.Ciphertext
	row.Ciphertext = sealed
	row.UpdatedAt = s.now().UTC()
	if err := s.swapTOTP(ctx, row, previous); err != nil {
		if errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrNotFound) {
			return domain.Unauthenticated("wrong code")
		}
		return err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	return s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "totp", EntityID: row.ID, Field: "ciphertext", New: sealed, Secret: true, Reason: "totp.accepted",
	})
}

func (s *Service) beginEnrollment(ctx context.Context, user domain.User) (Enrollment, error) {
	secret, err := newTOTPSecret()
	if err != nil {
		return Enrollment{}, err
	}
	var unused int64
	sealed, err := sealTOTP(s.key, secret, unused, false)
	if err != nil {
		return Enrollment{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return Enrollment{}, err
	}
	existing, err := s.totp.GetByUser(ctx, user.ID)
	if err == nil && existing.Confirmed {
		return Enrollment{}, domain.Conflict("two-factor is already enrolled")
	}
	if errors.Is(err, repository.ErrNotFound) {
		meta, err := s.meta(user.ID)
		if err != nil {
			return Enrollment{}, err
		}
		existing = domain.TOTPSecret{Meta: meta, UserID: user.ID, Ciphertext: sealed, Confirmed: false}
		if err := s.totp.Insert(ctx, existing); err != nil {
			return Enrollment{}, err
		}
	} else if err != nil {
		return Enrollment{}, err
	} else {
		existing.Ciphertext = sealed
		existing.Confirmed = false
		existing.UpdatedAt = s.now().UTC()
		if err := s.totp.Update(ctx, existing); err != nil {
			return Enrollment{}, err
		}
	}
	if err := s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "totp", EntityID: existing.ID, Field: "ciphertext", New: sealed, Secret: true,
	}); err != nil {
		return Enrollment{}, err
	}
	token, exp, err := s.issueChallenge(ctx, user, purposeEnroll, "", "")
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{
		Status: statusEnrollChallenge, EnrollmentToken: token, ExpiresAt: exp,
		OTPAuthURI: otpauth(user.Email, secret), Secret: encodeSecret(secret),
	}, nil
}

func (s *Service) replaceRecovery(ctx context.Context, user domain.User, roles []domain.Role) ([]string, error) {
	page, err := s.recovery.List(ctx, repository.Filter{UserID: user.ID})
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	for _, row := range page.Items {
		if row.UsedAt != nil {
			continue
		}
		row.UsedAt = &now
		row.UpdatedAt = now
		if err := s.recovery.Update(ctx, row); err != nil && !errors.Is(err, repository.ErrConflict) {
			return nil, err
		}
	}
	var codes []string
	for i := 0; i < recoveryCount(); i++ {
		plain, hash, err := recoveryCode()
		if err != nil {
			return nil, err
		}
		meta, err := s.meta(user.ID)
		if err != nil {
			return nil, err
		}
		if err := s.recovery.Insert(ctx, domain.RecoveryCode{
			Meta: meta, UserID: user.ID, CodeHash: hash,
		}); err != nil {
			return nil, err
		}
		codes = append(codes, plain)
	}
	if err := s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "user", EntityID: user.ID, Field: "recovery_codes", New: "issued", Secret: true,
	}); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) spendRecovery(ctx context.Context, user domain.User, roles []domain.Role, plain string) error {
	page, err := s.recovery.List(ctx, repository.Filter{UserID: user.ID})
	if err != nil {
		return err
	}
	var found *domain.RecoveryCode
	for i := range page.Items {
		row := &page.Items[i]
		if row.UsedAt != nil {
			continue
		}
		if codeMatches(row.CodeHash, plain) {
			found = row
		}
	}
	if found == nil {
		return domain.Unauthenticated("wrong recovery code")
	}
	now := s.now().UTC()
	found.UsedAt = &now
	found.UpdatedAt = now
	if err := s.recovery.Update(ctx, *found); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return domain.Unauthenticated("wrong recovery code")
		}
		return err
	}
	return s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "recovery_code", EntityID: found.ID, Field: "recovery_code", New: plain, Secret: true, Reason: "recovery.spent",
	})
}

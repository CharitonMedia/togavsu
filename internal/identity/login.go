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
	stored := false
	if lookupErr == nil {
		cred, err := s.passwords.GetByUser(ctx, user.ID)
		if err == nil {
			hash = cred.Argon2Hash
			stored = true
		} else if !errors.Is(err, repository.ErrNotFound) {
			return LoginResult{}, err
		}
	}
	if !stored {
		hash = dummyHash()
	}
	// One Argon2id check on every path. The dummy hash is used only when no credential is stored,
	// and that check cannot succeed: the dummy password is public.
	ok := VerifyPassword(password, hash)
	if !stored {
		ok = false
	}
	lockID := unknownLoginID(email)
	if lookupErr == nil && user.Active {
		lockID = user.ID
	}
	var result LoginResult
	err := s.withUser(ctx, lockID, func() error {
		if lookupErr != nil || !ok || !user.Active {
			if lookupErr == nil && user.Active {
				var ferr error
				result, ferr = s.failPassword(ctx, user)
				return ferr
			}
			// Unknown and inactive emails share one counter and the same responses.
			var ferr error
			result, ferr = s.failUnknown(ctx, email)
			return ferr
		}
		if err := s.rejectIfLocked(ctx, user); err != nil {
			return err
		}
		roles, err := rolesOf(user)
		if err != nil {
			return err
		}
		result, err = s.afterPassword(ctx, user, roles)
		return err
	})
	if err != nil {
		return LoginResult{}, err
	}
	return result, nil
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
	err = s.withUser(ctx, user.ID, func() error {
		if err := s.claimChallenge(ctx, sess, principalOf(user, roles)); err != nil {
			return s.claimResult(ctx, user, err)
		}
		return s.rejectIfLocked(ctx, user)
	})
	if err != nil {
		return Enrollment{}, err
	}
	return s.beginEnrollment(ctx, user)
}

// ConfirmEnrollment checks the first code, stores recovery codes, and opens a session.
func (s *Service) ConfirmEnrollment(ctx context.Context, enrollmentToken, code string) (LoginResult, error) {
	var codes []string
	out, err := s.useChallenge(ctx, enrollmentToken, purposeEnroll, func(user domain.User, roles []domain.Role) error {
		return s.audit.Atomic(ctx, func(ctx context.Context) error {
			if err := s.matchStoredTOTP(ctx, user, code, false); err != nil {
				return err
			}
			row, err := s.totp.GetByUser(ctx, user.ID)
			if err != nil {
				return err
			}
			row.Confirmed = true
			row.UpdatedAt = s.now().UTC()
			if err := s.totp.Update(ctx, row); err != nil {
				return err
			}
			codes, err = s.replaceRecovery(ctx, user, roles)
			if err != nil {
				return err
			}
			return s.record(ctx, principalOf(user, roles), audit.Change{
				Entity: "totp", EntityID: row.ID, Field: "confirmed", Old: "false", New: "true",
			})
		})
	})
	if err != nil {
		return LoginResult{}, err
	}
	out.RecoveryCodes = codes
	return out, nil
}

// VerifyTOTP finishes sign-in for a user who already enrolled.
func (s *Service) VerifyTOTP(ctx context.Context, totpToken, code string) (LoginResult, error) {
	return s.useChallenge(ctx, totpToken, purposeTOTP, func(user domain.User, _ []domain.Role) error {
		return s.matchStoredTOTP(ctx, user, code, true)
	})
}

// VerifyRecovery spends one recovery code and opens a session.
func (s *Service) VerifyRecovery(ctx context.Context, totpToken, code string) (LoginResult, error) {
	return s.useChallenge(ctx, totpToken, purposeTOTP, func(user domain.User, roles []domain.Role) error {
		return s.spendRecovery(ctx, user, roles, code)
	})
}

// useChallenge spends the challenge once, then evaluates the code.
// A second caller on the same token does not get a guess. The lock is checked again before a session opens.
func (s *Service) useChallenge(ctx context.Context, token, purpose string, eval func(domain.User, []domain.Role) error) (LoginResult, error) {
	_, sess, user, err := s.loadChallenge(ctx, token, purpose)
	if err != nil {
		return LoginResult{}, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return LoginResult{}, err
	}
	var result LoginResult
	err = s.withUser(ctx, user.ID, func() error {
		if err := s.claimChallenge(ctx, sess, principalOf(user, roles)); err != nil {
			return s.claimResult(ctx, user, err)
		}
		if err := s.rejectIfLocked(ctx, user); err != nil {
			return err
		}
		if err := eval(user, roles); err != nil {
			var api *domain.APIError
			if errors.As(err, &api) && api.Code == domain.CodeUnauthenticated {
				return s.recordFailure(ctx, user, roles)
			}
			return err
		}
		if err := s.rejectIfLocked(ctx, user); err != nil {
			return err
		}
		result, err = s.openSession(ctx, user, roles)
		return err
	})
	if err != nil {
		return LoginResult{}, err
	}
	return result, nil
}

func (s *Service) claimResult(ctx context.Context, user domain.User, err error) error {
	if !errors.Is(err, repository.ErrConflict) {
		return err
	}
	if lerr := s.rejectIfLocked(ctx, user); lerr != nil {
		return lerr
	}
	return domain.Unauthenticated("sign in is required")
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
	roles, err := rolesOf(user)
	if err != nil {
		return err
	}
	err = s.audit.Atomic(ctx, func(ctx context.Context) error {
		if err := s.swapTOTP(ctx, row, previous); err != nil {
			return err
		}
		return s.record(ctx, principalOf(user, roles), audit.Change{
			Entity: "totp", EntityID: row.ID, Field: "ciphertext", New: sealed, Secret: true, Reason: "totp.accepted",
		})
	})
	if errors.Is(err, repository.ErrConflict) || errors.Is(err, repository.ErrNotFound) {
		return domain.Unauthenticated("wrong code")
	}
	return err
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
	required, err := s.totpRequired(ctx, roles)
	if err != nil {
		return Enrollment{}, err
	}
	existing, err := s.totp.GetByUser(ctx, user.ID)
	if err == nil && existing.Confirmed {
		return Enrollment{}, domain.Conflict("two-factor is already enrolled")
	}
	var enroll Enrollment
	err = s.audit.Atomic(ctx, func(ctx context.Context) error {
		if errors.Is(err, repository.ErrNotFound) {
			meta, err := s.meta(user.ID)
			if err != nil {
				return err
			}
			existing = domain.TOTPSecret{Meta: meta, UserID: user.ID, Ciphertext: sealed, Confirmed: false}
			if err := s.totp.Insert(ctx, existing); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			existing.Ciphertext = sealed
			existing.Confirmed = false
			existing.UpdatedAt = s.now().UTC()
			if err := s.totp.Update(ctx, existing); err != nil {
				return err
			}
		}
		if err := s.record(ctx, principalOf(user, roles), audit.Change{
			Entity: "totp", EntityID: existing.ID, Field: "ciphertext", New: sealed, Secret: true,
		}); err != nil {
			return err
		}
		token, exp, err := s.issueChallenge(ctx, user, purposeEnroll, "", "")
		if err != nil {
			return err
		}
		enroll = Enrollment{
			Status: statusEnrollChallenge, EnrollmentToken: token, ExpiresAt: exp,
			OTPAuthURI: otpauth(user.Email, secret), Secret: encodeSecret(secret),
			Optional: !required,
		}
		return nil
	})
	return enroll, err
}

func (s *Service) replaceRecovery(ctx context.Context, user domain.User, roles []domain.Role) ([]string, error) {
	page, err := s.recovery.List(ctx, repository.Filter{UserID: user.ID})
	if err != nil {
		return nil, err
	}
	var codes []string
	err = s.audit.Atomic(ctx, func(ctx context.Context) error {
		now := s.now().UTC()
		for _, row := range page.Items {
			if row.UsedAt != nil {
				continue
			}
			row.UsedAt = &now
			row.UpdatedAt = now
			if err := s.recovery.Update(ctx, row); err != nil && !errors.Is(err, repository.ErrConflict) {
				return err
			}
		}
		codes = nil
		for i := 0; i < recoveryCount(); i++ {
			plain, hash, err := recoveryCode()
			if err != nil {
				return err
			}
			meta, err := s.meta(user.ID)
			if err != nil {
				return err
			}
			if err := s.recovery.Insert(ctx, domain.RecoveryCode{
				Meta: meta, UserID: user.ID, CodeHash: hash,
			}); err != nil {
				return err
			}
			codes = append(codes, plain)
		}
		return s.record(ctx, principalOf(user, roles), audit.Change{
			Entity: "user", EntityID: user.ID, Field: "recovery_codes", New: "issued", Secret: true,
		})
	})
	if err != nil {
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
	err = s.audit.Atomic(ctx, func(ctx context.Context) error {
		if err := s.recovery.Update(ctx, *found); err != nil {
			return err
		}
		return s.record(ctx, principalOf(user, roles), audit.Change{
			Entity: "recovery_code", EntityID: found.ID, Field: "recovery_code", New: plain, Secret: true, Reason: "recovery.spent",
		})
	})
	if errors.Is(err, repository.ErrConflict) {
		return domain.Unauthenticated("wrong recovery code")
	}
	return err
}

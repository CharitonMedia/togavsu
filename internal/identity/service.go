package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

type emailLookup interface {
	GetByEmail(context.Context, string) (domain.User, error)
}
type sessionLookup interface {
	GetByHash(context.Context, string) (domain.Session, error)
}
type tokenLookup interface {
	GetByHash(context.Context, string) (domain.APIToken, error)
}

// Deps wires the stores. Key is the 32-byte installer secret.
type Deps struct {
	Users     repository.Users
	Sessions  repository.Sessions
	Passwords repository.PasswordCredentials
	TOTP      repository.TOTPSecrets
	Recovery  repository.RecoveryCodes
	Tokens    repository.APITokens
	Audit     *audit.Service
	Settings  *settings.Registry
	Key       []byte
	Now       func() time.Time
	Mail      ResetMailer
}

// Service is the identity service layer.
type Service struct {
	users     repository.Users
	sessions  repository.Sessions
	passwords repository.PasswordCredentials
	totp      repository.TOTPSecrets
	recovery  repository.RecoveryCodes
	tokens    repository.APITokens
	audit     *audit.Service
	settings  *settings.Registry
	key       []byte
	now       func() time.Time
	mail      ResetMailer
	hits      map[string][]time.Time
	hitMu     sync.Mutex
	gates     map[domain.ID]*sync.Mutex
	gateMu    sync.Mutex
}

// New returns a service. A short key is rejected.
func New(d Deps) (*Service, error) {
	if d.Users == nil || d.Sessions == nil || d.Passwords == nil || d.TOTP == nil || d.Recovery == nil || d.Tokens == nil {
		return nil, errors.New("identity: stores are required")
	}
	if d.Audit == nil || d.Settings == nil {
		return nil, errors.New("identity: audit and settings are required")
	}
	if len(d.Key) != aesKey() {
		return nil, errors.New("identity: encryption key must be 32 bytes")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		users: d.Users, sessions: d.Sessions, passwords: d.Passwords, totp: d.TOTP,
		recovery: d.Recovery, tokens: d.Tokens, audit: d.Audit, settings: d.Settings,
		key: d.Key, now: now, mail: d.Mail, hits: map[string][]time.Time{},
		gates: map[domain.ID]*sync.Mutex{},
	}, nil
}

type sessionClaim interface {
	Claim(ctx context.Context, id domain.ID, at time.Time) error
}

type userLock interface {
	WithUserLock(ctx context.Context, id domain.ID, fn func() error) error
}

func (s *Service) mutexFor(id domain.ID) *sync.Mutex {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	mu, ok := s.gates[id]
	if !ok {
		mu = &sync.Mutex{}
		s.gates[id] = mu
	}
	return mu
}

// withUser serializes lockout checks and failure rows for one account.
func (s *Service) withUser(ctx context.Context, id domain.ID, fn func() error) error {
	mu := s.mutexFor(id)
	mu.Lock()
	defer mu.Unlock()
	if lock, ok := s.users.(userLock); ok {
		err := lock.WithUserLock(ctx, id, fn)
		if errors.Is(err, repository.ErrNotFound) {
			return fn()
		}
		return err
	}
	return fn()
}

func (s *Service) claimChallenge(ctx context.Context, sess domain.Session, who domain.Principal) error {
	now := s.now().UTC()
	if claim, ok := s.sessions.(sessionClaim); ok {
		if err := claim.Claim(ctx, sess.ID, now); err != nil {
			return err
		}
	} else if sess.RevokedAt != nil {
		return repository.ErrConflict
	} else {
		sess.RevokedAt = &now
		sess.UpdatedAt = now
		if err := s.sessions.Update(ctx, sess); err != nil {
			return err
		}
	}
	return s.record(ctx, who, audit.Change{
		Entity: "session", EntityID: sess.ID, Field: "revoked_at", New: "revoked", Reason: "challenge.attempt",
	})
}

func (s *Service) meta(actor domain.ID) (domain.Meta, error) {
	id, err := domain.NewID()
	if err != nil {
		return domain.Meta{}, err
	}
	now := s.now().UTC()
	return domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor}, nil
}

func (s *Service) record(ctx context.Context, who domain.Principal, change audit.Change) error {
	_, err := s.audit.Record(ctx, who, change)
	return err
}

func (s *Service) userByEmail(ctx context.Context, email string) (domain.User, error) {
	if lookup, ok := s.users.(emailLookup); ok {
		return lookup.GetByEmail(ctx, email)
	}
	// ponytail: ceiling: email lookup lists every user when the store has no GetByEmail. Upgrade path: add GetByEmail to repository.Users and a unique email index. trigger: a login's user scan exceeds 50ms.
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return domain.User{}, err
	}
	for _, row := range page.Items {
		if strings.EqualFold(row.Email, email) {
			return row, nil
		}
	}
	return domain.User{}, repository.ErrNotFound
}

func (s *Service) sessionByHash(ctx context.Context, hash string) (domain.Session, error) {
	if lookup, ok := s.sessions.(sessionLookup); ok {
		return lookup.GetByHash(ctx, hash)
	}
	page, err := s.sessions.List(ctx, repository.Filter{})
	if err != nil {
		return domain.Session{}, err
	}
	for _, row := range page.Items {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.Session{}, repository.ErrNotFound
}

func (s *Service) tokenByHash(ctx context.Context, hash string) (domain.APIToken, error) {
	if lookup, ok := s.tokens.(tokenLookup); ok {
		return lookup.GetByHash(ctx, hash)
	}
	page, err := s.tokens.List(ctx, repository.Filter{})
	if err != nil {
		return domain.APIToken{}, err
	}
	for _, row := range page.Items {
		if row.TokenHash == hash {
			return row, nil
		}
	}
	return domain.APIToken{}, repository.ErrNotFound
}

func (s *Service) duration(ctx context.Context, key string) (time.Duration, error) {
	value, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(value)
}

func (s *Service) intSetting(ctx context.Context, key string) (int, error) {
	value, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(value)
}

func (s *Service) flag(ctx context.Context, key string) (bool, error) {
	value, err := s.settings.Get(ctx, key)
	if err != nil {
		return false, err
	}
	return value == "true", nil
}

// challengeTTL is the short lifetime of a pending auth token.
// ponytail: ceiling: the token expires after security.lockout_length because that is the short duration in the registry. Upgrade path: add security.challenge_ttl and read it here. trigger: the registry defines security.challenge_ttl.
func (s *Service) challengeTTL(ctx context.Context) (time.Duration, error) {
	return s.duration(ctx, "security.lockout_length")
}

func (s *Service) locked(ctx context.Context, user domain.User) (bool, error) {
	limit, err := s.intSetting(ctx, "security.lockout_attempts")
	if err != nil {
		return false, err
	}
	window, err := s.duration(ctx, "security.lockout_length")
	if err != nil {
		return false, err
	}
	return s.failureLocked(ctx, "user", user.ID, limit, window)
}

func (s *Service) failureLocked(ctx context.Context, entity string, id domain.ID, limit int, window time.Duration) (bool, error) {
	now := s.now().UTC()
	rows, err := s.audit.Rows(ctx, audit.RowQuery{
		Entity: entity, EntityID: id, Field: signInField, Since: now.Add(-window),
	})
	if err != nil {
		return false, err
	}
	lastSuccess := len(rows)
	sawSuccess := false
	for i, row := range rows {
		if row.NewValue == "succeeded" {
			lastSuccess = i
			sawSuccess = true
		}
	}
	var failures int
	for i, row := range rows {
		if sawSuccess && i <= lastSuccess {
			continue
		}
		if row.NewValue != "failed" {
			continue
		}
		failures++
	}
	return failures >= limit, nil
}

func (s *Service) noteSignIn(ctx context.Context, user domain.User, roles []domain.Role, outcome string) error {
	return s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "user", EntityID: user.ID, Field: signInField, New: outcome,
	})
}

func (s *Service) totpRequired(ctx context.Context, roles []domain.Role) (bool, error) {
	// PRD section 5 requires TOTP for these roles. The writable settings do not turn that off.
	for _, role := range roles {
		switch role {
		case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant:
			return true, nil
		}
	}
	for _, role := range roles {
		if role == domain.RoleClient {
			return s.flag(ctx, "security.totp_required.client")
		}
	}
	return false, nil
}

func principalOf(user domain.User, roles []domain.Role) domain.Principal {
	return domain.Principal{UserID: user.ID, Roles: roles, Source: domain.SourceUI}
}

func rolesOf(user domain.User) ([]domain.Role, error) {
	return domain.ParseRoles(user.Roles)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func epoch() time.Time { return time.Unix(0, 0).UTC() }

func unknownLoginID(email string) domain.ID {
	sum := sha256.Sum256([]byte("login:" + email))
	return domain.ID(hex.EncodeToString(sum[:]))
}

func apiCode(code, message string) error {
	return &domain.APIError{Code: code, Message: message}
}

type totpSwap interface {
	CompareAndSwap(ctx context.Context, row domain.TOTPSecret, previous string) error
}

func (s *Service) swapTOTP(ctx context.Context, row domain.TOTPSecret, previous string) error {
	if swap, ok := s.totp.(totpSwap); ok {
		return swap.CompareAndSwap(ctx, row, previous)
	}
	return s.totp.Update(ctx, row)
}

func (s *Service) issueChallenge(ctx context.Context, user domain.User, purpose, sealed, action string) (string, time.Time, error) {
	ttl, err := s.challengeTTL(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	nonce, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := s.now().UTC().Add(ttl)
	token, err := signChallenge(s.key, challengeBody{
		Purpose: purpose, UserID: string(user.ID), Exp: domain.UnixMicroUTC(exp),
		Nonce: nonce, Secret: sealed, Action: action,
	})
	if err != nil {
		return "", time.Time{}, err
	}
	meta, err := s.meta(user.ID)
	if err != nil {
		return "", time.Time{}, err
	}
	row := domain.Session{
		Meta: meta, UserID: user.ID, TokenHash: challengeHash(token),
		ExpiresAt: exp, IdleExpiresAt: epoch(),
	}
	if err := s.sessions.Insert(ctx, row); err != nil {
		return "", time.Time{}, err
	}
	return token, exp, nil
}

func (s *Service) loadChallenge(ctx context.Context, token, purpose string) (challengeBody, domain.Session, domain.User, error) {
	var zero challengeBody
	body, err := openChallenge(s.key, token)
	if err != nil {
		return zero, domain.Session{}, domain.User{}, err
	}
	if body.Purpose != purpose || domain.FromUnixMicro(body.Exp).Before(s.now().UTC()) {
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	sess, err := s.sessionByHash(ctx, challengeHash(token))
	if err != nil || string(sess.UserID) != body.UserID {
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	user, err := s.users.Get(ctx, sess.UserID)
	if err != nil || !user.Active {
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	if sess.RevokedAt != nil || !sess.ExpiresAt.After(s.now().UTC()) {
		locked, err := s.locked(ctx, user)
		if err != nil {
			return zero, domain.Session{}, domain.User{}, err
		}
		if locked {
			return zero, sess, user, apiCode("locked_out", "account is locked")
		}
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	return body, sess, user, nil
}

func (s *Service) rejectIfLocked(ctx context.Context, user domain.User) error {
	locked, err := s.locked(ctx, user)
	if err != nil {
		return err
	}
	if locked {
		return apiCode("locked_out", "account is locked")
	}
	return nil
}

func (s *Service) recordFailure(ctx context.Context, user domain.User, roles []domain.Role) error {
	if err := s.noteSignIn(ctx, user, roles, "failed"); err != nil {
		return err
	}
	locked, err := s.locked(ctx, user)
	if err != nil {
		return err
	}
	if locked {
		if err := s.revokeOpenChallenges(ctx, user, roles); err != nil {
			return err
		}
		return apiCode("locked_out", "account is locked")
	}
	return domain.Unauthenticated("wrong email or password")
}

func (s *Service) revokeOpenChallenges(ctx context.Context, user domain.User, roles []domain.Role) error {
	page, err := s.sessions.List(ctx, repository.Filter{UserID: user.ID})
	if err != nil {
		return err
	}
	who := principalOf(user, roles)
	for _, row := range page.Items {
		if row.RevokedAt != nil || browserSession(row) {
			continue
		}
		if err := s.revoke(ctx, row, who, "challenge.locked"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) revoke(ctx context.Context, row domain.Session, who domain.Principal, reason string) error {
	now := s.now().UTC()
	row.RevokedAt = &now
	row.UpdatedAt = now
	if err := s.sessions.Update(ctx, row); err != nil {
		return err
	}
	return s.record(ctx, who, audit.Change{
		Entity: "session", EntityID: row.ID, Field: "revoked_at", New: "revoked", Reason: reason,
	})
}

func (s *Service) openSession(ctx context.Context, user domain.User, roles []domain.Role) (LoginResult, error) {
	raw, err := randomToken()
	if err != nil {
		return LoginResult{}, err
	}
	idle, err := s.duration(ctx, "security.idle_timeout")
	if err != nil {
		return LoginResult{}, err
	}
	meta, err := s.meta(user.ID)
	if err != nil {
		return LoginResult{}, err
	}
	exp := s.now().UTC().Add(idle)
	row := domain.Session{
		Meta: meta, UserID: user.ID, TokenHash: sessionHash(raw),
		ExpiresAt: exp, IdleExpiresAt: exp,
	}
	if err := s.sessions.Insert(ctx, row); err != nil {
		return LoginResult{}, err
	}
	if err := s.record(ctx, principalOf(user, roles), audit.Change{
		Entity: "session", EntityID: row.ID, Field: "token_hash", New: raw, Secret: true, Reason: "session.created",
	}); err != nil {
		return LoginResult{}, err
	}
	if err := s.noteSignIn(ctx, user, roles, "succeeded"); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{
		Status: statusAuthenticated, User: user, CSRFToken: csrfToken(s.key, row.ID),
		SessionToken: raw, SessionID: row.ID,
	}, nil
}

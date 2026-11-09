package identity

import (
	"context"
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
	}, nil
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
	rows, err := s.audit.ForEntity(ctx, "user", user.ID)
	if err != nil {
		return false, err
	}
	lastSuccess := len(rows)
	sawSuccess := false
	for i, row := range rows {
		if row.Field == signInField && row.NewValue == "succeeded" {
			lastSuccess = i
			sawSuccess = true
		}
	}
	failures := len([]byte{})
	now := s.now().UTC()
	for i, row := range rows {
		if sawSuccess && i <= lastSuccess {
			continue
		}
		if row.Field != signInField || row.NewValue != "failed" {
			continue
		}
		if now.Sub(row.CreatedAt) > window {
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
	for _, role := range roles {
		on, err := s.flag(ctx, "security.totp_required."+string(role))
		if err != nil {
			return false, err
		}
		if on {
			return true, nil
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

func apiCode(code, message string) error {
	return &domain.APIError{Code: code, Message: message}
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
	if err != nil || sess.RevokedAt != nil || !sess.ExpiresAt.After(s.now().UTC()) || string(sess.UserID) != body.UserID {
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	user, err := s.users.Get(ctx, sess.UserID)
	if err != nil || !user.Active {
		return zero, domain.Session{}, domain.User{}, domain.Unauthenticated("sign in is required")
	}
	return body, sess, user, nil
}

func (s *Service) revoke(ctx context.Context, row domain.Session) error {
	now := s.now().UTC()
	row.RevokedAt = &now
	row.UpdatedAt = now
	return s.sessions.Update(ctx, row)
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
	return LoginResult{
		Status: statusAuthenticated, User: user, CSRFToken: csrfToken(s.key, row.ID),
		SessionToken: raw, SessionID: row.ID,
	}, nil
}

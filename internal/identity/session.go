package identity

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Authenticate resolves a session cookie or a bearer token.
// It never returns a system principal. A cookie write without CSRF is refused.
// Sending both credentials is ambiguous. An empty bearer scope list is denied.
func (s *Service) Authenticate(ctx context.Context, in Presented) (Caller, error) {
	if err := ctx.Err(); err != nil {
		return Caller{}, err
	}
	if in.SessionToken != "" && in.Bearer != "" {
		return Caller{}, apiCode("ambiguous_auth", "send a session or a bearer token")
	}
	if in.SessionToken != "" {
		return s.authSession(ctx, in)
	}
	if in.Bearer != "" {
		return s.authBearer(ctx, in.Bearer)
	}
	return Caller{}, domain.Unauthenticated("sign in is required")
}

func (s *Service) authSession(ctx context.Context, in Presented) (Caller, error) {
	row, err := s.sessionByHash(ctx, sessionHash(in.SessionToken))
	if err != nil || !liveSession(row, s.now().UTC()) {
		return Caller{}, domain.Unauthenticated("sign in is required")
	}
	user, err := s.users.Get(ctx, row.UserID)
	if err != nil || !user.Active {
		return Caller{}, domain.Unauthenticated("sign in is required")
	}
	roles, err := rolesOf(user)
	if err != nil {
		return Caller{}, err
	}
	if in.Write {
		want := csrfToken(s.key, row.ID)
		if subtle.ConstantTimeCompare([]byte(in.CSRF), []byte(want)) != match() {
			return Caller{}, apiCode("csrf_required", "csrf token is required")
		}
	}
	idle, err := s.duration(ctx, "security.idle_timeout")
	if err != nil {
		return Caller{}, err
	}
	now := s.now().UTC()
	row.IdleExpiresAt = now.Add(idle)
	row.ExpiresAt = row.IdleExpiresAt
	row.UpdatedAt = now
	if err := s.sessions.Update(ctx, row); err != nil {
		return Caller{}, err
	}
	return Caller{Principal: domain.Principal{
		UserID: user.ID, Roles: roles, Source: domain.SourceUI, SessionID: row.ID,
	}}, nil
}

func browserSession(row domain.Session) bool {
	return strings.HasPrefix(row.TokenHash, "s:")
}

func liveSession(row domain.Session, now time.Time) bool {
	if !browserSession(row) || row.RevokedAt != nil {
		return false
	}
	return row.ExpiresAt.After(now) && row.IdleExpiresAt.After(now)
}

func (s *Service) authBearer(ctx context.Context, raw string) (Caller, error) {
	row, err := s.tokenByHash(ctx, digest(raw))
	if err != nil || row.RevokedAt != nil {
		return Caller{}, domain.Unauthenticated("sign in is required")
	}
	if err := s.rate(ctx, string(row.ID)); err != nil {
		return Caller{}, err
	}
	scopes, err := decodeScopes(row.Scopes)
	if err != nil || len(scopes) == 0 {
		return Caller{}, apiCode("scope_denied", "token has no scopes")
	}
	who := domain.Principal{Source: domain.SourceAPIToken, TokenID: row.ID, ModuleID: row.ModuleID}
	if !row.UserID.IsZero() {
		user, err := s.users.Get(ctx, row.UserID)
		if err != nil || !user.Active {
			return Caller{}, domain.Unauthenticated("sign in is required")
		}
		roles, err := rolesOf(user)
		if err != nil {
			return Caller{}, err
		}
		who.UserID = user.ID
		who.Roles = roles
	}
	if row.ModuleID != "" && row.UserID.IsZero() {
		who.Source = domain.SourceModule
	}
	return Caller{Principal: who, Scopes: scopes}, nil
}

// RequireScopes checks a bearer token. A session is authorized by role.
func (c Caller) RequireScopes(need ...string) error {
	if c.Principal.Source != domain.SourceAPIToken && c.Principal.Source != domain.SourceModule {
		return nil
	}
	have := map[string]struct{}{}
	for _, scope := range c.Scopes {
		have[scope] = struct{}{}
	}
	if len(have) == 0 {
		return apiCode("scope_denied", "token has no scopes")
	}
	for _, scope := range need {
		if _, ok := have[scope]; !ok {
			return apiCode("scope_denied", "token is missing a scope")
		}
	}
	return nil
}

// rate limits bearer use to security.api_rate_per_minute.
// ponytail: ceiling: the counter lives in this process. Upgrade path: store the hits in Postgres so every core process shares them. trigger: more than one core process serves API traffic.
func (s *Service) rate(ctx context.Context, id string) error {
	limit, err := s.intSetting(ctx, "security.api_rate_per_minute")
	if err != nil {
		return err
	}
	window, err := perMinuteWindow()
	if err != nil {
		return err
	}
	now := s.now().UTC()
	s.hitMu.Lock()
	defer s.hitMu.Unlock()
	var kept []time.Time
	for _, at := range s.hits[id] {
		if now.Sub(at) < window {
			kept = append(kept, at)
		}
	}
	if len(kept) >= limit {
		s.hits[id] = kept
		return domain.RateLimited()
	}
	s.hits[id] = append(kept, now)
	return nil
}

// Logout revokes the current session.
func (s *Service) Logout(ctx context.Context, caller Caller) error {
	if caller.Principal.SessionID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	row, err := s.sessions.Get(ctx, caller.Principal.SessionID)
	if err != nil {
		return domain.Unauthenticated("sign in is required")
	}
	if err := s.revoke(ctx, row); err != nil {
		return err
	}
	return s.record(ctx, caller.Principal, audit.Change{
		Entity: "session", EntityID: row.ID, Field: "revoked_at", New: "revoked", Reason: "session.ended",
	})
}

// ListSessions returns the caller's sessions. An admin sees every session.
func (s *Service) ListSessions(ctx context.Context, caller Caller) ([]SessionView, error) {
	if caller.Principal.UserID.IsZero() && !caller.Principal.HasRole(domain.RoleAdmin) {
		return nil, domain.Unauthenticated("sign in is required")
	}
	filter := repository.Filter{}
	if !caller.Principal.HasRole(domain.RoleAdmin) {
		filter.UserID = caller.Principal.UserID
	}
	page, err := s.sessions.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	var out []SessionView
	for _, row := range page.Items {
		if !browserSession(row) {
			continue
		}
		out = append(out, SessionView{
			ID: row.ID, UserID: row.UserID, ExpiresAt: row.ExpiresAt, IdleExpiresAt: row.IdleExpiresAt,
			Revoked: row.RevokedAt != nil, Current: row.ID == caller.Principal.SessionID,
		})
	}
	return out, nil
}

// DeleteSession revokes one session. Another user's session is not found.
func (s *Service) DeleteSession(ctx context.Context, caller Caller, id domain.ID) error {
	row, err := s.sessions.Get(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound("session")
	}
	if err != nil {
		return err
	}
	if row.UserID != caller.Principal.UserID && !caller.Principal.HasRole(domain.RoleAdmin) {
		return domain.NotFound("session")
	}
	if !browserSession(row) {
		return domain.NotFound("session")
	}
	if err := s.revoke(ctx, row); err != nil {
		return err
	}
	return s.record(ctx, caller.Principal, audit.Change{
		Entity: "session", EntityID: row.ID, Field: "revoked_at", New: "revoked",
	})
}

func decodeScopes(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var scopes []string
	if err := json.Unmarshal([]byte(raw), &scopes); err != nil {
		return nil, err
	}
	return scopes, nil
}

func encodeScopes(scopes []string) (string, error) {
	b, err := json.Marshal(scopes)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

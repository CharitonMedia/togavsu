package identity

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// IssuedToken is the one response that contains the bearer secret.
type IssuedToken struct {
	Token  TokenView
	Secret string
}

// IssueToken mints a bearer token. Only an admin may issue one.
// Scopes cannot exceed the issuer or the bound user, or the module's approved list.
func (s *Service) IssueToken(ctx context.Context, caller Caller, in Issue) (IssuedToken, error) {
	if !caller.Principal.HasRole(domain.RoleAdmin) {
		return IssuedToken{}, domain.Forbidden("admin role is required")
	}
	if in.Name == "" || len(in.Scopes) == 0 {
		return IssuedToken{}, domain.Invalid("scopes", "name and scopes are required")
	}
	if in.UserID.IsZero() == (in.ModuleID == "") {
		return IssuedToken{}, domain.Invalid("user_id", "bind the token to a user or a module")
	}
	ceiling := map[string]struct{}{}
	if caller.Principal.Source == domain.SourceUI {
		ceiling = scopesForRoles(caller.Principal.Roles)
	} else {
		if err := caller.RequireScopes("tokens:write"); err != nil {
			return IssuedToken{}, err
		}
		for _, scope := range caller.Scopes {
			ceiling[scope] = struct{}{}
		}
	}
	allowed, err := s.allowedScopes(ctx, in)
	if err != nil {
		return IssuedToken{}, err
	}
	for _, scope := range in.Scopes {
		if !knownScope(scope) {
			return IssuedToken{}, domain.Invalid("scopes", "unknown scope")
		}
		if _, ok := ceiling[scope]; !ok && !viewScope(scope) {
			return IssuedToken{}, apiCode("scope_denied", "issuer cannot grant this scope")
		}
		if viewScope(scope) {
			if _, ok := ceiling[scope]; !ok && caller.Principal.Source != domain.SourceUI {
				return IssuedToken{}, apiCode("scope_denied", "issuer cannot grant this scope")
			}
		} else if _, ok := ceiling[scope]; !ok {
			return IssuedToken{}, apiCode("scope_denied", "issuer cannot grant this scope")
		}
		if _, ok := allowed[scope]; !ok {
			return IssuedToken{}, apiCode("scope_denied", "scope exceeds the owner")
		}
	}
	raw, err := randomToken()
	if err != nil {
		return IssuedToken{}, err
	}
	encoded, err := encodeScopes(in.Scopes)
	if err != nil {
		return IssuedToken{}, err
	}
	meta, err := s.meta(caller.Principal.UserID)
	if err != nil {
		return IssuedToken{}, err
	}
	row := domain.APIToken{
		Meta: meta, UserID: in.UserID, ModuleID: in.ModuleID, Name: in.Name,
		TokenHash: digest(raw), Scopes: encoded,
	}
	if err := s.tokens.Insert(ctx, row); err != nil {
		return IssuedToken{}, err
	}
	if err := s.record(ctx, caller.Principal, audit.Change{
		Entity: "api_token", EntityID: row.ID, Field: "token_hash", New: raw, Secret: true,
	}); err != nil {
		return IssuedToken{}, err
	}
	return IssuedToken{Token: tokenView(row), Secret: raw}, nil
}

func (s *Service) allowedScopes(ctx context.Context, in Issue) (map[string]struct{}, error) {
	if in.ModuleID != "" {
		if len(in.Approved) == 0 {
			return nil, apiCode("scope_denied", "module approval list is empty")
		}
		out := map[string]struct{}{}
		for _, scope := range in.Approved {
			if knownScope(scope) {
				out[scope] = struct{}{}
			}
		}
		return out, nil
	}
	user, err := s.users.Get(ctx, in.UserID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, domain.NotFound("user")
	}
	if err != nil {
		return nil, err
	}
	roles, err := rolesOf(user)
	if err != nil {
		return nil, err
	}
	return scopesForRoles(roles), nil
}

// ListTokens returns the caller's tokens. An admin sees every token.
func (s *Service) ListTokens(ctx context.Context, caller Caller) ([]TokenView, error) {
	filter := repository.Filter{}
	if !caller.Principal.HasRole(domain.RoleAdmin) {
		if caller.Principal.UserID.IsZero() {
			return nil, domain.Unauthenticated("sign in is required")
		}
		filter.UserID = caller.Principal.UserID
	}
	page, err := s.tokens.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	var out []TokenView
	for _, row := range page.Items {
		if !caller.Principal.HasRole(domain.RoleAdmin) && row.UserID != caller.Principal.UserID {
			continue
		}
		out = append(out, tokenView(row))
	}
	return out, nil
}

// GetToken returns one token. Another user's token is not found.
func (s *Service) GetToken(ctx context.Context, caller Caller, id domain.ID) (TokenView, error) {
	row, err := s.visibleToken(ctx, caller, id)
	if err != nil {
		return TokenView{}, err
	}
	return tokenView(row), nil
}

// RevokeToken spends a token. Another user's token is not found.
func (s *Service) RevokeToken(ctx context.Context, caller Caller, id domain.ID) error {
	row, err := s.visibleToken(ctx, caller, id)
	if err != nil {
		return err
	}
	if row.RevokedAt != nil {
		return nil
	}
	now := s.now().UTC()
	row.RevokedAt = &now
	row.UpdatedAt = now
	if err := s.tokens.Update(ctx, row); err != nil {
		return err
	}
	return s.record(ctx, caller.Principal, audit.Change{
		Entity: "api_token", EntityID: row.ID, Field: "revoked_at", New: "revoked",
	})
}

func (s *Service) visibleToken(ctx context.Context, caller Caller, id domain.ID) (domain.APIToken, error) {
	row, err := s.tokens.Get(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.APIToken{}, domain.NotFound("api token")
	}
	if err != nil {
		return domain.APIToken{}, err
	}
	if caller.Principal.HasRole(domain.RoleAdmin) {
		return row, nil
	}
	if row.UserID.IsZero() || row.UserID != caller.Principal.UserID {
		return domain.APIToken{}, domain.NotFound("api token")
	}
	return row, nil
}

func tokenView(row domain.APIToken) TokenView {
	scopes, _ := decodeScopes(row.Scopes)
	return TokenView{
		ID: row.ID, Name: row.Name, UserID: row.UserID, ModuleID: row.ModuleID,
		Scopes: scopes, Revoked: row.RevokedAt != nil, CreatedAt: row.CreatedAt,
	}
}

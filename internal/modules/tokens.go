package modules

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// IdentityTokens issues module tokens through identity (PRD 9.3): bound to the module,
// limited to the scopes the admin approved, and revocable. The admin's own scopes bound
// what a bearer-token admin can approve, as for any token.
type IdentityTokens struct{ Svc *identity.Service }

// IssueModuleToken mints the module's scoped token.
func (t IdentityTokens) IssueModuleToken(ctx context.Context, admin domain.Principal, module string, scopes []string) (string, string, error) {
	if len(scopes) == 0 {
		return "", "", nil
	}
	res, err := t.Svc.IssueToken(ctx, identity.Caller{Principal: admin, Scopes: CallerScopes(ctx)},
		identity.Issue{Name: "module " + module, ModuleID: module, Scopes: scopes, Approved: scopes})
	if err != nil {
		return "", "", err
	}
	return string(res.Token.ID), res.Secret, nil
}

// RevokeModuleToken revokes a module token.
func (t IdentityTokens) RevokeModuleToken(ctx context.Context, admin domain.Principal, id string) error {
	if id == "" {
		return nil
	}
	return t.Svc.RevokeToken(ctx, identity.Caller{Principal: admin, Scopes: CallerScopes(ctx)}, domain.ID(id))
}

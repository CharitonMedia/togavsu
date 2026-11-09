package identity

import (
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// catalog is the bearer scope list from the API contract.
var catalog = map[string]struct{}{
	"profile:read": {}, "profile:write": {},
	"sessions:read": {}, "sessions:write": {},
	"tokens:read": {}, "tokens:write": {},
	"users:read": {}, "users:write": {},
	"contacts:read": {}, "contacts:write": {},
	"matters:read": {}, "matters:write": {},
	"markers:write": {},
	"notes:read":    {}, "notes:write": {},
	"conflicts:read": {}, "conflicts:write": {}, "conflicts:decide": {},
	"settings:read": {}, "settings:write": {},
	"time:read": {}, "time:write": {},
	"expenses:read": {}, "expenses:write": {},
	"invoices:read": {}, "invoices:write": {}, "invoices:approve": {}, "invoices:send": {},
	"payments:read": {}, "payments:write": {},
	"trust:read": {}, "trust:write": {}, "trust:withdraw": {},
	"reports:read":  {},
	"calendar:read": {}, "calendar:write": {},
	"deadlines:read": {}, "deadlines:write": {},
	"files:read": {}, "files:write": {},
	"editor:run":      {},
	"doc_tender:read": {}, "doc_tender:write": {},
	"search:use":         {},
	"notifications:read": {}, "notifications:write": {},
	"layouts:read": {}, "layouts:write": {},
	"help:read": {}, "help:write": {},
	"events:read": {}, "events:write": {},
	"audit:read":    {},
	"webhooks:read": {},
	"ai:use":        {}, "ai:admin": {},
	"backups:admin": {}, "updates:admin": {},
	"modules:read": {}, "modules:write": {},
}

func knownScope(scope string) bool {
	if _, ok := catalog[scope]; ok {
		return true
	}
	return viewScope(scope)
}

func viewScope(scope string) bool {
	if !strings.HasPrefix(scope, "views:") || !strings.HasSuffix(scope, ":read") {
		return false
	}
	rest := strings.TrimSuffix(strings.TrimPrefix(scope, "views:"), ":read")
	mod, view, ok := strings.Cut(rest, ".")
	return ok && mod != "" && view != "" && !strings.ContainsAny(rest, " /")
}

func roleScopes(role domain.Role) map[string]struct{} {
	switch role {
	case domain.RoleAdmin:
		return copyCatalog()
	case domain.RoleAttorney:
		out := copyCatalog()
		for _, drop := range []string{"settings:write", "users:write", "tokens:write", "backups:admin", "updates:admin", "modules:write", "ai:admin"} {
			delete(out, drop)
		}
		return out
	case domain.RoleStaff:
		return scopeSet(
			"profile:read", "profile:write", "sessions:read", "sessions:write", "tokens:read",
			"contacts:read", "contacts:write", "matters:read", "matters:write",
			"notes:read", "notes:write", "files:read", "files:write", "editor:run",
			"calendar:read", "calendar:write", "deadlines:read", "deadlines:write",
			"time:read", "time:write", "expenses:read", "expenses:write",
			"invoices:read", "invoices:write", "payments:read", "payments:write",
			"trust:read", "trust:write", "reports:read", "search:use",
			"notifications:read", "notifications:write", "layouts:read", "layouts:write",
			"help:read", "events:read", "doc_tender:read", "conflicts:read", "conflicts:write",
			"users:read", "settings:read",
		)
	case domain.RoleAccountant:
		return scopeSet(
			"profile:read", "profile:write", "sessions:read", "sessions:write", "tokens:read",
			"invoices:read", "payments:read", "trust:read", "reports:read", "files:read",
		)
	case domain.RoleClient:
		return scopeSet(
			"profile:read", "profile:write", "sessions:read", "sessions:write", "tokens:read",
			"matters:read", "files:read", "invoices:read", "notes:read", "calendar:read", "deadlines:read",
		)
	default:
		return map[string]struct{}{}
	}
}

func scopesForRoles(roles []domain.Role) map[string]struct{} {
	out := map[string]struct{}{}
	for _, role := range roles {
		for scope := range roleScopes(role) {
			out[scope] = struct{}{}
		}
	}
	return out
}

func copyCatalog() map[string]struct{} {
	out := make(map[string]struct{}, len(catalog))
	for scope := range catalog {
		out[scope] = struct{}{}
	}
	return out
}

func scopeSet(scopes ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		out[scope] = struct{}{}
	}
	return out
}

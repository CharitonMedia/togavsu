package identity

import (
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// catalog is the bearer scope list from the API contract.
var catalog = map[string]struct{}{
	"profile:read": {}, "profile:write": {},
	"sessions:read": {}, "sessions:write": {},
	"tokens:read": {}, "tokens:write": {},
	"users:read": {}, "users:write": {},
	"users:directory":       {},
	"module_settings:write": {}, "webhooks:authorization": {}, "event_records:read": {},
	"firm:read":     {},
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
	"module_secrets:write": {},
	"mail:send":            {},
	"mail:system":          {},
	"client_access:read":   {},
	"client_access:write":  {},
	"books:read":           {},
	// CORE-31 / CORE-32 / CORE-34 module API update 2.
	"contracts:call":     {},
	"contracts:provide":  {},
	"public_pages:serve": {},
	"banner:show":        {},
}

// moduleOnlyScopes are grantable only on a module token (CORE-02, CORE-04 to CORE-06).
// Entries land with the CORE item that adds the scope to the catalog.
var moduleOnlyScopes = map[string]struct{}{
	"module_settings:write": {}, "webhooks:authorization": {}, "event_records:read": {},
	"books:read":           {},
	"module_secrets:write": {},
	"mail:send":            {},
	"mail:system":          {},
	"contracts:call":       {},
	"contracts:provide":    {},
	"public_pages:serve":   {},
	"banner:show":          {},
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
		for scope := range moduleOnlyScopes {
			delete(out, scope)
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
			"users:read", "users:directory", "settings:read", "firm:read",
			"client_access:read", "client_access:write",
		)
	case domain.RoleAccountant:
		return scopeSet(
			"profile:read", "profile:write", "sessions:read", "sessions:write", "tokens:read",
			"invoices:read", "payments:read", "trust:read", "reports:read", "files:read", "firm:read",
		)
	case domain.RoleClient:
		return scopeSet(
			"profile:read", "profile:write", "sessions:read", "sessions:write", "tokens:read",
			"matters:read", "files:read", "invoices:read", "notes:read", "calendar:read", "deadlines:read",
			"trust:read", "firm:read", "users:directory",
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

// ModuleOnlyScope reports a scope that may only sit on a module token (CORE-02).
func ModuleOnlyScope(scope string) bool {
	_, ok := moduleOnlyScopes[scope]
	return ok
}

func scopeSet(scopes ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		out[scope] = struct{}{}
	}
	return out
}

// KnownScope reports whether scope is in the bearer scope list, or is a published view
// scope (views:<module>.<view>:read). A token request, a module's approval included, is
// refused for any other scope.
func KnownScope(scope string) bool { return knownScope(scope) }

// Scopes is the bearer scope list, sorted.
func Scopes() []string {
	out := make([]string, 0, len(catalog))
	for scope := range catalog {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

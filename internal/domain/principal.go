package domain

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Role is one of the five firm roles. A user may hold more than one.
type Role string

const (
	RoleAdmin      Role = "admin"
	RoleAttorney   Role = "attorney"
	RoleStaff      Role = "staff"
	RoleAccountant Role = "accountant"
	RoleClient     Role = "client"
)

// ActorSource names where a write came from. System is reserved for the job runner.
// Service is a module's own token with no viewer (CORE-01 service call).
type ActorSource string

const (
	SourceUI       ActorSource = "ui"
	SourceAPIToken ActorSource = "api_token"
	SourceModule   ActorSource = "module"
	SourceService  ActorSource = "service"
	SourceSystem   ActorSource = "system"
)

// Principal is the authenticated caller. Handlers build it; services receive it.
type Principal struct {
	UserID             ID
	Roles              []Role
	Source             ActorSource
	SessionID          ID
	TokenID            ID
	ModuleID           string
	MustChangePassword bool // CORE-14: client must change a temporary password before other API use.
}

// ErrRole is returned when a role list is empty or contains an unknown role.
var ErrRole = errors.New("invalid role")

// ParseRole parses one role name.
func ParseRole(s string) (Role, error) {
	switch Role(s) {
	case RoleAdmin, RoleAttorney, RoleStaff, RoleAccountant, RoleClient:
		return Role(s), nil
	default:
		return "", ErrRole
	}
}

// HasRole reports whether the principal holds role.
func (p Principal) HasRole(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// SystemPrincipal is the only caller that may bypass matter walls.
// Request handlers must not set SourceSystem from client input.
func SystemPrincipal() Principal {
	return Principal{Source: SourceSystem}
}

// MarshalRoles encodes roles as a JSON array.
func MarshalRoles(roles []Role) (string, error) {
	if len(roles) == 0 {
		return "", ErrRole
	}
	seen := map[Role]bool{}
	out := make([]Role, 0, len(roles))
	for _, r := range roles {
		if _, err := ParseRole(string(r)); err != nil {
			return "", err
		}
		if seen[r] {
			return "", fmt.Errorf("%w: duplicate %s", ErrRole, r)
		}
		seen[r] = true
		out = append(out, r)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseRoles decodes a JSON role array.
func ParseRoles(s string) ([]Role, error) {
	var raw []string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, ErrRole
	}
	roles := make([]Role, len(raw))
	for i, item := range raw {
		roles[i] = Role(item)
	}
	encoded, err := MarshalRoles(roles)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(encoded), &raw); err != nil {
		return nil, err
	}
	return roles, nil
}

// EventActor keeps the delegated person as actor. Only background calls use the
// module id; Event.ModuleID retains the module channel separately.
func (p Principal) EventActor() ID {
	if !p.UserID.IsZero() {
		return p.UserID
	}
	if p.ModuleID != "" {
		return ID(p.ModuleID)
	}
	return SystemUserID
}

// EventActorKind identifies the person when delegated and the module for background work.
func (p Principal) EventActorKind() string {
	if p.Source == SourceSystem {
		return "system"
	}
	if p.UserID.IsZero() && p.ModuleID != "" {
		return "module"
	}
	if p.Source == SourceAPIToken {
		return "agent"
	}
	if p.HasRole(RoleClient) && !p.HasRole(RoleAdmin) && !p.HasRole(RoleAttorney) && !p.HasRole(RoleStaff) && !p.HasRole(RoleAccountant) {
		return "client"
	}
	return "staff"
}

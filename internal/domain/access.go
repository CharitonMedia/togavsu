package domain

import (
	"context"
	"errors"
)

// Action is the operation a service asks the policy to allow.
type Action string

const (
	ActionRead           Action = "read"
	ActionWrite          Action = "write"
	ActionTrustWithdraw  Action = "trust_withdraw"
	ActionTrustDeposit   Action = "trust_deposit"
	ActionConflictDecide Action = "conflict_decide"
	ActionApproveInvoice Action = "approve_invoice"
)

// Resource is the kind of record being checked.
type Resource string

const (
	ResMatter           Resource = "matter"
	ResContact          Resource = "contact"
	ResNote             Resource = "note"
	ResFile             Resource = "file"
	ResInvoice          Resource = "invoice"
	ResPayment          Resource = "payment"
	ResTrust            Resource = "trust"
	ResConflictReason   Resource = "conflict_reason"
	ResSensitiveContact Resource = "sensitive_contact"
	ResBillingReport    Resource = "billing_report"
	ResSettings         Resource = "settings"
	ResUser             Resource = "user"
	ResCalendar         Resource = "calendar"
	ResTime             Resource = "time"
	ResExpense          Resource = "expense"
)

// FileClass distinguishes files an accountant may read from the rest.
type FileClass string

const (
	FileGeneral    FileClass = "general"
	FileReceipt    FileClass = "receipt"
	FileInvoicePDF FileClass = "invoice_pdf"
)

// Decision reasons are stable strings for API errors.
const (
	ReasonUnauthenticated = "unauthenticated"
	ReasonMatterWall      = "matter_wall"
	ReasonRole            = "role"
	ReasonAttorneyOnly    = "attorney_only"
	ReasonClientScope     = "client_scope"
	ReasonSystem          = "system"
)

// AccessInput is the pure policy input. The caller has already resolved the wall.
type AccessInput struct {
	Resource         Resource
	Action           Action
	MatterID         ID
	Walled           bool
	WallAllows       bool
	AttorneyOnly     bool
	FileClass        FileClass
	ClientMatter     bool
	SharedWithClient bool
}

// Decision is the policy answer. Redact means the record may be returned with attorney-only values removed.
type Decision struct {
	Allow  bool
	Redact bool
	Reason string
}

// ResourceRef is what a service passes to Policy.Authorize.
type ResourceRef struct {
	Resource         Resource
	Action           Action
	ID               ID
	MatterID         ID
	AttorneyOnly     bool
	FileClass        FileClass
	SharedWithClient bool
}

// WallView is the matter-wall slice identity loads for one caller.
type WallView struct {
	Walled       bool
	Allows       bool
	ClientMatter bool
}

// WallReader loads wall membership. Identity implements it.
type WallReader interface {
	View(ctx context.Context, p Principal, matterID ID) (WallView, error)
}

// ErrWallReader is returned when a matter-scoped check has no wall reader.
var ErrWallReader = errors.New("access: wall reader required")

// Policy checks roles and matter walls. It does not query SQL itself.
type Policy struct {
	Walls WallReader
}

// Authorize resolves the wall, then applies Decide.
func (p Policy) Authorize(ctx context.Context, pr Principal, ref ResourceRef) (Decision, error) {
	in := AccessInput{
		Resource:         ref.Resource,
		Action:           ref.Action,
		MatterID:         ref.MatterID,
		AttorneyOnly:     ref.AttorneyOnly,
		FileClass:        ref.FileClass,
		SharedWithClient: ref.SharedWithClient,
	}
	if ref.MatterID != "" {
		if p.Walls == nil {
			return Decision{}, ErrWallReader
		}
		view, err := p.Walls.View(ctx, pr, ref.MatterID)
		if err != nil {
			return Decision{}, err
		}
		in.Walled = view.Walled
		in.WallAllows = view.Allows
		in.ClientMatter = view.ClientMatter
	}
	return Decide(pr, in), nil
}

// Decide applies the role and wall rules. Walls are already resolved.
func Decide(p Principal, in AccessInput) Decision {
	if p.Source == SourceSystem {
		return Decision{Allow: true, Reason: ReasonSystem}
	}
	if p.UserID.IsZero() || len(p.Roles) == 0 {
		return Decision{Allow: false, Reason: ReasonUnauthenticated}
	}
	if in.MatterID != "" && in.Walled && !in.WallAllows {
		return Decision{Allow: false, Reason: ReasonMatterWall}
	}
	if !roleAllows(p, in) {
		if clientDenied(p, in) {
			return Decision{Allow: false, Reason: ReasonClientScope}
		}
		return Decision{Allow: false, Reason: ReasonRole}
	}
	if attorneyOnly(in) && !p.HasRole(RoleAdmin) && !p.HasRole(RoleAttorney) {
		// An attorney-only file is hidden. A client-only principal does not get redacted field access,
		// but another role that already grants the read keeps that redacted access.
		if in.Resource == ResFile || in.Action != ActionRead || !redactedFieldRead(p) {
			return Decision{Allow: false, Reason: ReasonAttorneyOnly}
		}
		return Decision{Allow: true, Redact: true, Reason: ReasonAttorneyOnly}
	}
	return Decision{Allow: true}
}

func attorneyOnly(in AccessInput) bool {
	return in.AttorneyOnly || in.Resource == ResConflictReason || in.Resource == ResSensitiveContact
}

// redactedFieldRead reports whether a non-attorney role grants a redacted read.
// RoleClient does not, and it does not cancel a role that does.
func redactedFieldRead(p Principal) bool {
	for _, role := range p.Roles {
		if role == RoleStaff {
			return true
		}
	}
	return false
}

func clientDenied(p Principal, in AccessInput) bool {
	if !p.HasRole(RoleClient) || p.HasRole(RoleAdmin) || p.HasRole(RoleAttorney) || p.HasRole(RoleStaff) {
		return false
	}
	return in.Action == ActionRead && (!in.ClientMatter || !in.SharedWithClient)
}

func roleAllows(p Principal, in AccessInput) bool {
	for _, role := range p.Roles {
		if roleAllowsOne(role, in) {
			return true
		}
	}
	return false
}

func roleAllowsOne(role Role, in AccessInput) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleAttorney:
		if in.Resource == ResSettings && in.Action != ActionRead {
			return false
		}
		if in.Resource == ResUser && in.Action != ActionRead {
			return false
		}
		return true
	case RoleStaff:
		switch in.Action {
		case ActionTrustWithdraw, ActionConflictDecide, ActionApproveInvoice:
			return false
		case ActionRead, ActionWrite, ActionTrustDeposit:
		default:
			return false
		}
		if in.Resource == ResTrust && in.Action == ActionWrite {
			return false
		}
		if in.Resource == ResSettings || in.Resource == ResUser || in.Resource == ResBillingReport {
			return in.Action == ActionRead
		}
		return true
	case RoleAccountant:
		if in.Action != ActionRead {
			return false
		}
		switch in.Resource {
		case ResInvoice, ResPayment, ResTrust, ResBillingReport:
			return true
		case ResFile:
			return in.FileClass == FileReceipt || in.FileClass == FileInvoicePDF
		default:
			return false
		}
	case RoleClient:
		if in.Action != ActionRead || !in.ClientMatter || !in.SharedWithClient {
			return false
		}
		switch in.Resource {
		case ResSettings, ResUser, ResTrust, ResBillingReport, ResConflictReason, ResSensitiveContact:
			return false
		default:
			return true
		}
	default:
		return false
	}
}

package access

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Shown is the attorney-only JSON shape: redacted, or redacted false with a value.
type Shown struct {
	Redacted bool   `json:"redacted"`
	Value    string `json:"value,omitempty"`
}

// Entry is one audit row after attorney-only redaction.
type Entry struct {
	domain.AuditEntry
	Old Shown
	New Shown
}

// Gate checks domain.Policy and applies the same answer to every surface.
type Gate struct {
	Policy domain.Policy
}

// New returns a gate. reader may be nil only for checks that have no matter id.
func New(reader domain.WallReader) Gate {
	return Gate{Policy: domain.Policy{Walls: reader}}
}

// Decide resolves the wall and the role.
func (g Gate) Decide(ctx context.Context, who domain.Principal, ref domain.ResourceRef) (domain.Decision, error) {
	return g.Policy.Authorize(ctx, who, ref)
}

// Allow returns a client error when Decide does not allow the call.
// A matter wall and an attorney-only file use the same not-found result as a missing row.
func (g Gate) Allow(ctx context.Context, who domain.Principal, ref domain.ResourceRef) error {
	d, err := g.Decide(ctx, who, ref)
	if err != nil {
		return err
	}
	if d.Allow {
		return nil
	}
	return Denial(ref, d)
}

// Denial maps a policy decision to the API error. Allow with redaction is not an error.
func Denial(ref domain.ResourceRef, d domain.Decision) error {
	switch d.Reason {
	case domain.ReasonUnauthenticated:
		return domain.Unauthenticated("sign in is required")
	case domain.ReasonMatterWall:
		return domain.NotFound(string(ref.Resource))
	case domain.ReasonAttorneyOnly:
		if ref.Resource == domain.ResFile || ref.Action != domain.ActionRead {
			return domain.NotFound(string(ref.Resource))
		}
		return domain.Forbidden("attorney only")
	case domain.ReasonClientScope:
		return domain.NotFound(string(ref.Resource))
	default:
		return domain.Forbidden("role does not allow this")
	}
}

// Project returns the value, or a redacted object when the decision says so.
func Project(d domain.Decision, value string) (Shown, error) {
	if !d.Allow {
		return Shown{}, domain.Forbidden("not allowed")
	}
	if d.Redact {
		return Shown{Redacted: true}, nil
	}
	return Shown{Redacted: false, Value: value}, nil
}

// VisibleMatter reports whether this caller may know the matter exists.
func (g Gate) VisibleMatter(ctx context.Context, who domain.Principal, matterID domain.ID) error {
	return g.Allow(ctx, who, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: matterID})
}

// FilterMatters drops matters the caller cannot see. One wall check per id.
func (g Gate) FilterMatters(ctx context.Context, who domain.Principal, matters []domain.Matter) ([]domain.Matter, error) {
	var out []domain.Matter
	for _, matter := range matters {
		err := g.VisibleMatter(ctx, who, matter.ID)
		if err == nil {
			out = append(out, matter)
			continue
		}
		if IsNotFound(err) {
			continue
		}
		if IsForbidden(err) {
			continue
		}
		return nil, err
	}
	return out, nil
}

// Apply runs mutate and records the write only after the policy allows it.
// A denial leaves the stored row and the audit log unchanged.
// mutate and the audit row run in one log.Atomic unit: when the audit append fails the
// write rolls back with it, so a change is never kept without its history row.
// mutate must write through ctx (audit.Conn) to join that transaction.
func (g Gate) Apply(ctx context.Context, who domain.Principal, ref domain.ResourceRef, change audit.Change, log *audit.Service, mutate func(ctx context.Context) error) error {
	d, err := g.Decide(ctx, who, ref)
	if err != nil {
		return err
	}
	if !d.Allow {
		return Denial(ref, d)
	}
	if log == nil {
		return errors.New("access: an audit log is required for a write")
	}
	return log.Atomic(ctx, func(ctx context.Context) error {
		if mutate != nil {
			if err := mutate(ctx); err != nil {
				return err
			}
		}
		_, err := log.Record(ctx, who, change)
		return err
	})
}

// History returns audit rows for a matter the caller can see.
// Attorney-only fields are redacted for every other role.
func (g Gate) History(ctx context.Context, who domain.Principal, matterID domain.ID, rows []domain.AuditEntry) ([]Entry, error) {
	if err := g.VisibleMatter(ctx, who, matterID); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(rows))
	for _, row := range rows {
		if row.EntityID != matterID {
			continue
		}
		entry := Entry{AuditEntry: row, Old: Shown{Value: row.OldValue}, New: Shown{Value: row.NewValue}}
		if attorneyField(row.Field) {
			d, err := g.Decide(ctx, who, domain.ResourceRef{
				Resource: domain.ResConflictReason, Action: domain.ActionRead, MatterID: matterID,
			})
			if err != nil {
				return nil, err
			}
			if d.Redact || !d.Allow {
				entry.Old = Shown{Redacted: true}
				entry.New = Shown{Redacted: true}
				entry.OldValue = audit.Redacted
				entry.NewValue = audit.Redacted
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// attorneyField is the edit-history set staff may see only as redacted.
func attorneyField(name string) bool {
	switch name {
	case "conflict_reason", "attorney_reason", "ssn", "ein", "ssn_ein", "ssn_ein_ciphertext":
		return true
	default:
		return false
	}
}

// IsNotFound reports a not-found API error.
func IsNotFound(err error) bool {
	return code(err) == domain.CodeNotFound
}

// IsForbidden reports a forbidden API error.
func IsForbidden(err error) bool {
	return code(err) == domain.CodeForbidden
}

func code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

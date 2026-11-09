package audit

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Redacted is the only text stored for a secret old or new value.
const Redacted = "[redacted]"

// ErrAppendOnly is returned when a caller tries to change an audit row.
var ErrAppendOnly = errors.New("audit: append only")

// Change is one field write. Secret forces both values to Redacted.
type Change struct {
	Entity   string
	EntityID domain.ID
	Field    string
	Old      string
	New      string
	Reason   string
	Secret   bool
}

// Store is the append-only persistence surface.
type Store interface {
	Append(ctx context.Context, row domain.AuditEntry) error
	Get(ctx context.Context, id domain.ID) (domain.AuditEntry, error)
	List(ctx context.Context, filter repository.Filter) (repository.Page[domain.AuditEntry], error)
}

// Service writes and reads the audit log. It has no update or delete.
type Service struct {
	store Store
	now   func() time.Time
	ids   func() (domain.ID, error)
}

// New returns a service. now and ids default to the clock and UUIDv7.
func New(store Store, now func() time.Time) (*Service, error) {
	if store == nil {
		return nil, errors.New("audit: store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now, ids: domain.NewID}, nil
}

// Record appends one write. A secret field is stored redacted, never in the clear.
func (s *Service) Record(ctx context.Context, who domain.Principal, change Change) (domain.AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.AuditEntry{}, err
	}
	if change.Entity == "" || change.Field == "" || change.EntityID.IsZero() {
		return domain.AuditEntry{}, domain.Invalid("audit", "entity, id, and field are required")
	}
	actor := who.UserID
	if actor.IsZero() {
		if who.Source != domain.SourceSystem {
			return domain.AuditEntry{}, domain.Unauthenticated("actor is required")
		}
		actor = domain.SystemUserID
	}
	old, newValue := change.Old, change.New
	if change.Secret || sensitive(change.Field) {
		if old != "" {
			old = Redacted
		}
		if newValue != "" {
			newValue = Redacted
		}
	}
	id, err := s.ids()
	if err != nil {
		return domain.AuditEntry{}, err
	}
	at := s.now().UTC()
	row := domain.AuditEntry{
		Meta:     domain.Meta{ID: id, CreatedAt: at, UpdatedAt: at, CreatedBy: actor},
		ActorID:  actor,
		Role:     joinRoles(who.Roles),
		Source:   string(who.Source),
		Entity:   change.Entity,
		EntityID: change.EntityID,
		Field:    change.Field,
		OldValue: old,
		NewValue: newValue,
		Reason:   provenance(who, change.Reason),
	}
	if err := s.store.Append(ctx, row); err != nil {
		return domain.AuditEntry{}, err
	}
	return row, nil
}

// SettingChanged records a settings registry change.
func (s *Service) SettingChanged(ctx context.Context, change settings.Change) error {
	who := domain.Principal{
		UserID:   change.Actor,
		Source:   change.Source,
		TokenID:  change.TokenID,
		ModuleID: change.ModuleID,
	}
	if change.Role != "" {
		for _, part := range strings.Split(change.Role, ",") {
			role, err := domain.ParseRole(strings.TrimSpace(part))
			if err != nil {
				return err
			}
			who.Roles = append(who.Roles, role)
		}
	}
	entityID, err := s.ids()
	if err != nil {
		return err
	}
	// The settings key is not a UUIDv7. EntityID is a new row id; the key is the field.
	_, err = s.Record(ctx, who, Change{
		Entity:   "setting",
		EntityID: entityID,
		Field:    change.Key,
		Old:      change.Old,
		New:      change.New,
		Reason:   "settings update",
		Secret:   change.Redacted,
	})
	return err
}

// ForEntity returns every row for one record, oldest first.
func (s *Service) ForEntity(ctx context.Context, entity string, id domain.ID) ([]domain.AuditEntry, error) {
	page, err := s.store.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	var out []domain.AuditEntry
	for _, row := range page.Items {
		if row.Entity == entity && row.EntityID == id {
			out = append(out, row)
		}
	}
	return out, nil
}

// Mutate is the denied path. It does not read or write a row.
func (s *Service) Mutate(context.Context, domain.ID) error {
	return ErrAppendOnly
}

// Get returns one row.
func (s *Service) Get(ctx context.Context, id domain.ID) (domain.AuditEntry, error) {
	return s.store.Get(ctx, id)
}

func joinRoles(roles []domain.Role) string {
	if len(roles) == 0 {
		return ""
	}
	parts := make([]string, len(roles))
	for i, role := range roles {
		parts[i] = string(role)
	}
	return strings.Join(parts, ",")
}

// provenance keeps token and module ids on the row.
// ponytail: ceiling: token id and module id are appended to the reason because audit_log has no columns for them. Upgrade path: add those columns in an identity migration and write them as themselves. trigger: the integration lead ships that migration.
func provenance(who domain.Principal, reason string) string {
	var parts []string
	if !who.TokenID.IsZero() {
		parts = append(parts, "token="+string(who.TokenID))
	}
	if who.ModuleID != "" {
		parts = append(parts, "module="+who.ModuleID)
	}
	if reason != "" {
		parts = append(parts, reason)
	}
	return strings.Join(parts, " ")
}

func sensitive(field string) bool {
	name := strings.ToLower(field)
	for _, part := range []string{
		"password", "argon", "totp", "secret", "recovery", "token",
		"passphrase", "ciphertext", "csrf", "authorization", "ssn", "ein",
	} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

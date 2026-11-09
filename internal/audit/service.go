package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// RowQuery selects audit rows. Empty Field and a zero Since omit those predicates.
type RowQuery struct {
	Entity   string
	EntityID domain.ID
	Field    string
	Since    time.Time
}

// Store is the append-only persistence surface.
type Store interface {
	Append(ctx context.Context, row domain.AuditEntry) error
	Get(ctx context.Context, id domain.ID) (domain.AuditEntry, error)
	List(ctx context.Context, filter repository.Filter) (repository.Page[domain.AuditEntry], error)
	Rows(ctx context.Context, query RowQuery) ([]domain.AuditEntry, error)
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
		// A module-bound token has no user. The row is the System actor with source
		// module, and provenance keeps the module id in the reason.
		moduleCall := who.Source == domain.SourceModule && who.ModuleID != ""
		if who.Source != domain.SourceSystem && !moduleCall {
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

// atomicStore is a store that can hold a write and its audit rows in one transaction.
type atomicStore interface {
	Atomic(ctx context.Context, fn func(ctx context.Context) error) error
}

// Atomic runs fn so its writes and the rows it records commit or roll back together.
// On Postgres that is one transaction: store writes that join WithTx on the same
// pool are inside it. A store without transactions runs fn as it is.
func (s *Service) Atomic(ctx context.Context, fn func(ctx context.Context) error) error {
	if store, ok := s.store.(atomicStore); ok {
		return store.Atomic(ctx, fn)
	}
	return fn(ctx)
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
	_, err := s.Record(ctx, who, Change{
		Entity:   "setting",
		EntityID: SettingEntityID(change.Key),
		Field:    change.Key,
		Old:      change.Old,
		New:      change.New,
		Reason:   "settings update",
		Secret:   change.Redacted,
	})
	return err
}

// SettingEntityID is stable for one settings key, so later reads can find that key's history.
func SettingEntityID(key string) domain.ID {
	sum := sha256.Sum256([]byte("setting:" + key))
	return domain.ID(hex.EncodeToString(sum[:]))
}

// ForEntity returns every row for one record, oldest first.
func (s *Service) ForEntity(ctx context.Context, entity string, id domain.ID) ([]domain.AuditEntry, error) {
	return s.store.Rows(ctx, RowQuery{Entity: entity, EntityID: id})
}

// Rows returns the store query. Lockout uses it so a sign-in does not load the whole log.
func (s *Service) Rows(ctx context.Context, query RowQuery) ([]domain.AuditEntry, error) {
	return s.store.Rows(ctx, query)
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
	switch strings.ToLower(field) {
	case "password", "argon2_hash", "totp", "secret", "recovery_code", "recovery_codes",
		"token", "token_hash", "passphrase", "ciphertext", "csrf", "authorization",
		"ssn", "ein", "ssn_ein", "ssn_ein_ciphertext":
		return true
	default:
		return false
	}
}

package modules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Device grant lifetime and exchange token TTL (CORE-28).
const deviceGrantUnusedDays = domain.ModuleDeviceUnusedDays

func deviceContextTTL() time.Duration { return 10 * time.Minute }

// ModuleDevice is one device grant row for list and create responses.
type ModuleDevice struct {
	ID         domain.ID `json:"id"`
	ModuleID   string    `json:"module_id"`
	UserID     domain.ID `json:"user_id"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Secret     string    `json:"secret,omitempty"`
}

// ModuleDeviceWrite is the body for createModuleDeviceGrant.
type ModuleDeviceWrite struct {
	ModuleID string `json:"module_id"`
	Name     string `json:"name"`
}

// DeviceContextRequest is the body for exchangeModuleDeviceGrant.
type DeviceContextRequest struct {
	Secret   string     `json:"secret"`
	MatterID *domain.ID `json:"matter_id"`
}

func hashDeviceSecret(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func newDeviceSecret() (string, error) {
	buf := make([]byte, limits().SecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func firmStaff(p domain.Principal) bool {
	// Device grants are staff portal work (admin, attorney, staff); not accountant (AC-004).
	return p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff)
}

func (s *Service) deviceUnusedCutoff() time.Time {
	return s.now().Add(-time.Duration(deviceGrantUnusedDays) * 24 * time.Hour)
}

// CreateModuleDeviceGrant mints a device grant for the signed-in staff user (CORE-28).
// Session only: a bearer token is refused at the handler.
func (s *Service) CreateModuleDeviceGrant(ctx context.Context, p domain.Principal, in ModuleDeviceWrite) (ModuleDevice, error) {
	if p.UserID.IsZero() || p.Source == domain.SourceModule || p.Source == domain.SourceSystem {
		return ModuleDevice{}, domain.Forbidden("a device grant is created by a signed-in staff user")
	}
	if !firmStaff(p) {
		return ModuleDevice{}, domain.Forbidden("only firm staff may connect a device")
	}
	module := strings.TrimSpace(in.ModuleID)
	if module == "" {
		return ModuleDevice{}, domain.Invalid("module_id", "module_id is required")
	}
	inst, err := s.Installed(ctx, module)
	if err != nil {
		return ModuleDevice{}, err
	}
	if !inst.Enabled {
		return ModuleDevice{}, domain.NotFound("module")
	}
	path := strings.TrimSpace(inst.Manifest.DeviceConnect)
	if path == "" {
		return ModuleDevice{}, domain.NotFound("module")
	}
	secret, err := newDeviceSecret()
	if err != nil {
		return ModuleDevice{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return ModuleDevice{}, err
	}
	now := s.now()
	at := now.UnixMicro()
	name := strings.TrimSpace(in.Name)
	out := ModuleDevice{
		ID: id, ModuleID: module, UserID: p.UserID, Name: name,
		CreatedAt: now.UTC(), LastUsedAt: now.UTC(), Secret: secret,
	}
	err = s.commit(ctx, func(ctx context.Context) error {
		if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_devices
(id, created_at, updated_at, user_id, module_id, name, secret_hash, last_used_at, revoked_at)
VALUES ($1,$2,$2,$3,$4,$5,$6,$2,NULL)`,
			string(id), at, string(p.UserID), module, name, hashDeviceSecret(secret)); err != nil {
			return err
		}
		if _, err := s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_device", EntityID: id, Field: "module_id", New: module, Reason: "createModuleDeviceGrant",
		}); err != nil {
			return err
		}
		return s.deviceEvent(ctx, p, "module_device.created", id, module, "Device connected to "+inst.Manifest.Name)
	})
	if err != nil {
		return ModuleDevice{}, err
	}
	return out, nil
}

// ListModuleDeviceGrants returns the caller's active, unlapsed device grants.
func (s *Service) ListModuleDeviceGrants(ctx context.Context, p domain.Principal) ([]ModuleDevice, error) {
	if p.UserID.IsZero() {
		return nil, domain.Forbidden("sign in")
	}
	cutoff := s.deviceUnusedCutoff().UnixMicro()
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, created_at, user_id, module_id, name, last_used_at
FROM core.module_devices
WHERE user_id = $1 AND revoked_at IS NULL AND last_used_at >= $2
ORDER BY created_at DESC`, string(p.UserID), cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModuleDevice{}
	for rows.Next() {
		d, err := scanModuleDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeModuleDeviceGrant revokes one grant. An admin may revoke anyone's; others only their own.
func (s *Service) RevokeModuleDeviceGrant(ctx context.Context, p domain.Principal, deviceID domain.ID) error {
	if p.UserID.IsZero() {
		return domain.Forbidden("sign in")
	}
	var userID, moduleID string
	var revoked sql.NullInt64
	err := s.q(ctx).QueryRowContext(ctx, `SELECT user_id, module_id, revoked_at FROM core.module_devices WHERE id = $1`, string(deviceID)).
		Scan(&userID, &moduleID, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NotFound("device")
	}
	if err != nil {
		return err
	}
	if userID != string(p.UserID) && !p.HasRole(domain.RoleAdmin) {
		return domain.NotFound("device")
	}
	if revoked.Valid {
		return nil
	}
	now := s.now().UnixMicro()
	return s.commit(ctx, func(ctx context.Context) error {
		res, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_devices SET revoked_at = $1, updated_at = $1
WHERE id = $2 AND revoked_at IS NULL`, now, string(deviceID))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return nil
		}
		if _, err := s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_device", EntityID: deviceID, Field: "revoked_at", New: "true", Reason: "revokeModuleDeviceGrant",
		}); err != nil {
			return err
		}
		return s.deviceEvent(ctx, p, "module_device.revoked", deviceID, moduleID, "Device grant revoked")
	})
}

// RevokeDevicesForUser revokes every active grant for a deactivated user (CORE-28).
func (s *Service) RevokeDevicesForUser(ctx context.Context, p domain.Principal, userID domain.ID) error {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, module_id FROM core.module_devices
WHERE user_id = $1 AND revoked_at IS NULL`, string(userID))
	if err != nil {
		return err
	}
	var pending []struct{ id, module string }
	for rows.Next() {
		var id, module string
		if err := rows.Scan(&id, &module); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, struct{ id, module string }{id, module})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	now := s.now().UnixMicro()
	return s.commit(ctx, func(ctx context.Context) error {
		for _, row := range pending {
			res, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_devices SET revoked_at = $1, updated_at = $1
WHERE id = $2 AND revoked_at IS NULL`, now, row.id)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				continue
			}
			if _, err := s.d.Audit.Record(ctx, p, audit.Change{
				Entity: "module_device", EntityID: domain.ID(row.id), Field: "revoked_at", New: "true", Reason: "user.deactivated",
			}); err != nil {
				return err
			}
			if err := s.deviceEvent(ctx, p, "module_device.revoked", domain.ID(row.id), row.module, "Device grant lapsed: user deactivated"); err != nil {
				return err
			}
		}
		return nil
	})
}

// ExchangeModuleDeviceGrant turns a device secret into a 10-minute context token for that user.
// Only the named module's own bearer may call. Optional matter_id is wall-checked.
func (s *Service) ExchangeModuleDeviceGrant(ctx context.Context, p domain.Principal, module string, in DeviceContextRequest) (ContextToken, error) {
	if err := ownModuleDeviceExchange(p, module); err != nil {
		return ContextToken{}, err
	}
	secret := strings.TrimSpace(in.Secret)
	if secret == "" {
		return ContextToken{}, domain.Unauthenticated("the device grant is invalid or expired")
	}
	var (
		id, userID string
		lastUsed   int64
		revoked    sql.NullInt64
	)
	err := s.q(ctx).QueryRowContext(ctx, `SELECT id, user_id, last_used_at, revoked_at FROM core.module_devices
WHERE module_id = $1 AND secret_hash = $2`, module, hashDeviceSecret(secret)).
		Scan(&id, &userID, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextToken{}, domain.Unauthenticated("the device grant is invalid or expired")
	}
	if err != nil {
		return ContextToken{}, err
	}
	if revoked.Valid || lastUsed < s.deviceUnusedCutoff().UnixMicro() {
		return ContextToken{}, domain.Unauthenticated("the device grant is invalid or expired")
	}
	u, err := s.d.Users.Get(ctx, domain.ID(userID))
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !u.Active) {
		return ContextToken{}, domain.Unauthenticated("the device grant is invalid or expired")
	}
	if err != nil {
		return ContextToken{}, err
	}
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil || len(roles) == 0 {
		return ContextToken{}, domain.Unauthenticated("the device grant is invalid or expired")
	}
	viewer := domain.Principal{UserID: u.ID, Roles: roles, Source: domain.SourceUI}
	var matter domain.ID
	if in.MatterID != nil {
		matter = *in.MatterID
	}
	tok, err := s.issueContext(ctx, viewer, module, matter, deviceContextTTL())
	if err != nil {
		return ContextToken{}, err
	}
	now := s.now().UnixMicro()
	if _, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_devices SET last_used_at = $1, updated_at = $1 WHERE id = $2 AND revoked_at IS NULL`,
		now, id); err != nil {
		return ContextToken{}, err
	}
	return tok, nil
}

// DeviceConnectPath returns the enabled module's device_connect path, or not_found.
func (s *Service) DeviceConnectPath(ctx context.Context, module string) (name, path string, err error) {
	inst, err := s.Installed(ctx, module)
	if err != nil {
		return "", "", err
	}
	if !inst.Enabled {
		return "", "", domain.NotFound("module")
	}
	path = strings.TrimSpace(inst.Manifest.DeviceConnect)
	if path == "" {
		return "", "", domain.NotFound("module")
	}
	return inst.Manifest.Name, path, nil
}

func ownModuleDeviceExchange(p domain.Principal, module string) error {
	if p.Source != domain.SourceModule || p.ModuleID == "" {
		return domain.Forbidden("only the module's own token may exchange a device grant")
	}
	if p.ModuleID != module {
		return domain.Forbidden("only the module's own token may exchange a device grant")
	}
	if !p.UserID.IsZero() {
		return domain.Forbidden("device grant exchange ignores a context token")
	}
	return nil
}

func (s *Service) deviceEvent(ctx context.Context, p domain.Principal, action string, device domain.ID, module, summary string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	at := s.now().UnixMicro()
	actor := string(p.UserID)
	if actor == "" {
		actor = p.ModuleID
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES ($1,$2,$3,$4,$5,$6,$7,'module_device',$8,NULL,$9,0)`,
		string(id), at, at, actor, actorKind(p), actor, action, string(device), summary)
	return err
}

func scanModuleDevice(sc interface {
	Scan(dest ...any) error
}) (ModuleDevice, error) {
	var id, userID, moduleID, name string
	var created, lastUsed int64
	if err := sc.Scan(&id, &created, &userID, &moduleID, &name, &lastUsed); err != nil {
		return ModuleDevice{}, err
	}
	return ModuleDevice{
		ID: domain.ID(id), UserID: domain.ID(userID), ModuleID: moduleID, Name: name,
		CreatedAt: domain.FromUnixMicro(created), LastUsedAt: domain.FromUnixMicro(lastUsed),
	}, nil
}

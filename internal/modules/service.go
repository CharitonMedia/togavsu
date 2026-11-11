package modules

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
)

// Module statuses (API contract Module.status, plus the terminal states the runtime
// keeps for history). removed and cancelled never leave the API: List and Get skip them
// and Uninstall answers uninstalling.
const (
	StatusAwaiting     = "awaiting_approval"
	StatusInstalled    = "installed"
	StatusDisabled     = "disabled"
	StatusUninstalling = "uninstalling"
	StatusRemoved      = "removed"
	StatusCancelled    = "cancelled"
)

// Deps wires the module runtime. DB is the core pool the audit store uses, so a state
// change and its audit row commit together.
type Deps struct {
	DB       *sql.DB
	Key      []byte
	Audit    *audit.Service
	Settings Settings
	Walls    domain.WallReader
	Users    Users
	Tokens   Tokens
	Help     Help
	Widgets  Widgets
	Runtime  Runtime
	Backups  Backups
	// Market is the marketplace client; nil accepts trusted local keys only.
	Market KeySource
	// Contacts applies the core contact rule (*practice.Service satisfies it). Nil means
	// no contact-keyed module row is readable, so a composition that leaves it out fails
	// closed.
	Contacts ContactReader
	HTTP     *http.Client
	Now      func() time.Time
}

// Service is the module runtime (PRD 9).
type Service struct {
	d       Deps
	schemas *Schemas
	mu      sync.Mutex // serializes lifecycle changes
	cacheMu sync.RWMutex
	cache   map[string]*Installed
	aiOnce  sync.Once
	aiErr   error
	// AIBaseURLs overrides provider endpoints, as doctender.Router.BaseURLs does.
	AIBaseURLs map[string]string
}

// Installed is an installed module as the runtime serves it.
type Installed struct {
	ID       string
	Version  string
	Status   string
	Enabled  bool
	Manifest Manifest
	Approved map[string]bool
	TokenID  string
}

// New checks dependencies.
func New(d Deps) (*Service, error) {
	if d.DB == nil || len(d.Key) != limits().AESKeyBytes || d.Audit == nil || d.Settings == nil || d.Walls == nil || d.Users == nil || d.Tokens == nil {
		return nil, errors.New("modules: db, 32-byte key, audit, settings, walls, users and tokens are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.HTTP == nil {
		d.HTTP = http.DefaultClient
	}
	s := &Service{d: d, schemas: &Schemas{DB: d.DB, Now: d.Now}, cache: map[string]*Installed{}}
	return s, nil
}

// Schemas exposes the schema owner for the core's delete and merge paths.
func (s *Service) Schemas() *Schemas { return s.schemas }

func (s *Service) now() time.Time { return s.d.Now().UTC() }

// Migrate applies the module runtime's own core migrations (range 1000-1099) to plain,
// a pool without a search_path, as files and billing do.
func Migrate(ctx context.Context, plain *sql.DB) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	if err := db.ValidateMigrationVersions(ms); err != nil {
		return err
	}
	return db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}

// --- secrets ---

func (s *Service) subkey(purpose string) []byte {
	mac := hmac.New(sha256.New, s.d.Key)
	mac.Write([]byte("togavsu-modules:" + purpose))
	return mac.Sum(nil)
}

func (s *Service) seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(s.subkey("seal"))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Service) open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.subkey("seal"))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("modules: sealed value is short")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(out), err
}

// --- access ---

func requireAdmin(p domain.Principal) error {
	if p.Source == domain.SourceModule || p.ModuleID != "" {
		return domain.Forbidden("modules can't manage modules")
	}
	if p.UserID.IsZero() || !p.HasRole(domain.RoleAdmin) {
		return domain.Forbidden("admin role is required")
	}
	return nil
}

// --- rows ---

type modRow struct {
	ID, Name, Version, Status, Source, PackageID, PendingPackageID string
	Enabled                                                        bool
	TokenID, TokenSecret, WebhookSecret, DBRole, DBPassword        string
	SchemaState, LastError, CreatedBy                              string
	CreatedAt                                                      int64
	// DataQuietSince is when the schema last stopped changing; 0 while the module can
	// write to it (see migration 1001).
	DataQuietSince int64
}

const modCols = `id, name, version, status, enabled, source, package_id, pending_package_id, token_id, token_secret, webhook_secret, db_role, db_password, schema_state, last_error, created_by, created_at, data_quiet_since`

func (s *Service) q(ctx context.Context) audit.Querier { return audit.Conn(ctx, s.d.DB) }

func scanMod(sc interface{ Scan(...any) error }) (modRow, error) {
	var r modRow
	var enabled int
	err := sc.Scan(&r.ID, &r.Name, &r.Version, &r.Status, &enabled, &r.Source, &r.PackageID, &r.PendingPackageID, &r.TokenID,
		&r.TokenSecret, &r.WebhookSecret, &r.DBRole, &r.DBPassword, &r.SchemaState, &r.LastError, &r.CreatedBy, &r.CreatedAt, &r.DataQuietSince)
	r.Enabled = enabled == one
	return r, err
}

func (s *Service) row(ctx context.Context, id string) (modRow, error) {
	r, err := scanMod(s.q(ctx).QueryRowContext(ctx, `SELECT `+modCols+` FROM core.modules WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return modRow{}, domain.NotFound("module")
	}
	return r, err
}

func b2i(b bool) int {
	if b {
		return one
	}
	return zero
}

func (s *Service) saveRow(ctx context.Context, r modRow) error {
	at := s.now().UnixMicro()
	_, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.modules (`+modCols+`, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, version = EXCLUDED.version, status = EXCLUDED.status, enabled = EXCLUDED.enabled,
source = EXCLUDED.source, package_id = EXCLUDED.package_id, pending_package_id = EXCLUDED.pending_package_id, token_id = EXCLUDED.token_id,
token_secret = EXCLUDED.token_secret, webhook_secret = EXCLUDED.webhook_secret, db_role = EXCLUDED.db_role, db_password = EXCLUDED.db_password,
schema_state = EXCLUDED.schema_state, last_error = EXCLUDED.last_error, data_quiet_since = EXCLUDED.data_quiet_since, updated_at = EXCLUDED.updated_at`,
		r.ID, r.Name, r.Version, r.Status, b2i(r.Enabled), r.Source, r.PackageID, r.PendingPackageID, r.TokenID, r.TokenSecret,
		r.WebhookSecret, r.DBRole, r.DBPassword, r.SchemaState, r.LastError, r.CreatedBy, r.CreatedAt, r.DataQuietSince, at)
	return err
}

type pkgRow struct {
	ID, Version, Digest, KeyID string
	Manifest                   Manifest
	Archive                    []byte
}

func (s *Service) savePackage(ctx context.Context, p domain.Principal, pkg *Package, key TrustedKey, raw []byte) (string, error) {
	id, err := domain.NewID()
	if err != nil {
		return "", err
	}
	man, err := json.Marshal(pkg.Manifest)
	if err != nil {
		return "", err
	}
	at := s.now().UnixMicro()
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_packages (id, created_at, updated_at, created_by, module_id, version, digest, key_id, manifest, archive)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, string(id), at, at, string(p.UserID), pkg.Manifest.ID, pkg.Manifest.Version, pkg.PackageDigest(), key.KeyID,
		string(man), base64.StdEncoding.EncodeToString(raw))
	return string(id), err
}

func (s *Service) loadPackage(ctx context.Context, id string) (pkgRow, error) {
	var r pkgRow
	var man, archive string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT id, version, digest, key_id, manifest, archive FROM core.module_packages WHERE id = $1`, id).
		Scan(&r.ID, &r.Version, &r.Digest, &r.KeyID, &man, &archive)
	if errors.Is(err, sql.ErrNoRows) {
		return pkgRow{}, domain.NotFound("module package")
	}
	if err != nil {
		return pkgRow{}, err
	}
	if err := json.Unmarshal([]byte(man), &r.Manifest); err != nil {
		return pkgRow{}, err
	}
	r.Archive, err = base64.StdEncoding.DecodeString(archive)
	return r, err
}

// Permission is one requested scope with the admin's decision.
type PermissionView struct {
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Approved bool   `json:"approved"`
	Decision string `json:"-"`
}

func (s *Service) permissions(ctx context.Context, module string) ([]PermissionView, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT scope, required, decision FROM core.module_permissions WHERE module_id = $1 ORDER BY scope`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PermissionView{}
	for rows.Next() {
		var pv PermissionView
		var req int
		if err := rows.Scan(&pv.Scope, &req, &pv.Decision); err != nil {
			return nil, err
		}
		pv.Required = req == one
		pv.Approved = pv.Decision == "approved"
		out = append(out, pv)
	}
	return out, rows.Err()
}

func (s *Service) putPermission(ctx context.Context, p domain.Principal, module, scope string, required bool, decision string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	at := s.now().UnixMicro()
	decidedAt := int64(0)
	decidedBy := ""
	if decision != "pending" {
		decidedAt, decidedBy = at, string(p.UserID)
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_permissions (id, created_at, updated_at, created_by, module_id, scope, required, decision, decided_by, decided_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (module_id, scope) DO UPDATE SET required = EXCLUDED.required, decision = EXCLUDED.decision,
decided_by = EXCLUDED.decided_by, decided_at = EXCLUDED.decided_at, updated_at = EXCLUDED.updated_at`,
		string(id), at, at, string(p.UserID), module, scope, b2i(required), decision, decidedBy, decidedAt)
	return err
}

// --- audit and events ---

func actorKind(p domain.Principal) string {
	switch {
	case p.Source == domain.SourceSystem:
		return "system"
	case p.Source == domain.SourceModule:
		return "module"
	case p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff):
		return "client"
	case p.Source == domain.SourceAPIToken:
		return "agent"
	}
	return "staff"
}

// moduleEntityID is the audit entity id of a module (module ids are slugs, not UUIDs).
func moduleEntityID(module string) domain.ID {
	sum := sha256.Sum256([]byte("module:" + module))
	return domain.ID(fmt.Sprintf("%x", sum[:limits().KeyIDBytes]))
}

func (s *Service) audit(ctx context.Context, p domain.Principal, module, field, old, new, reason string) error {
	_, err := s.d.Audit.Record(ctx, p, audit.Change{Entity: "module:" + module, EntityID: moduleEntityID(module), Field: field, Old: old, New: new, Reason: reason})
	return err
}

func (s *Service) event(ctx context.Context, p domain.Principal, action, module, summary string, matter domain.ID) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	at := s.now().UnixMicro()
	actor := string(p.UserID)
	if actor == "" {
		actor = p.ModuleID
	}
	var m any
	if !matter.IsZero() {
		m = string(matter)
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES ($1,$2,$3,$4,$5,$6,$7,'module',$8,$9,$10,0)`, string(id), at, at, actor, actorKind(p), actor, action, module, m, summary)
	return err
}

// commit runs fn in one transaction with its audit rows and events.
func (s *Service) commit(ctx context.Context, fn func(ctx context.Context) error) error {
	return audit.WithTx(ctx, s.d.DB, fn)
}

// --- keys ---

func (s *Service) trustedKeys(ctx context.Context) ([]TrustedKey, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT key_id, name, public_key FROM core.module_signing_keys WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrustedKey
	for rows.Next() {
		var k TrustedKey
		var pub string
		if err := rows.Scan(&k.KeyID, &k.Name, &pub); err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(pub)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			continue
		}
		k.PublicKey, k.Source = raw, "local"
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.d.Market != nil {
		more, err := s.d.Market.Keys(ctx)
		if err != nil {
			// A marketplace outage leaves local keys working (PRD 10.5: firm control).
			log.Printf("modules: marketplace keys unavailable: %v", err)
		}
		for _, k := range more {
			k.Source = "marketplace"
			out = append(out, k)
		}
	}
	return out, nil
}

// --- view ---

// View is the API contract's Module.
type View struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	Enabled     bool             `json:"enabled"`
	Permissions []PermissionView `json:"permissions"`
	Replaces    *string          `json:"replaces"`
	Status      string           `json:"status"`
}

func (s *Service) view(ctx context.Context, r modRow) (View, error) {
	perms, err := s.permissions(ctx, r.ID)
	if err != nil {
		return View{}, err
	}
	v := View{ID: r.ID, Name: r.Name, Version: r.Version, Enabled: r.Enabled, Permissions: perms, Status: r.Status}
	if r.PackageID != "" {
		pkg, err := s.loadPackage(ctx, r.PackageID)
		if err != nil {
			return View{}, err
		}
		if pkg.Manifest.Replaces != "" {
			rep := pkg.Manifest.Replaces
			v.Replaces = &rep
		}
	}
	return v, nil
}

// List returns every module that isn't removed or cancelled.
func (s *Service) List(ctx context.Context, p domain.Principal) ([]View, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+modCols+` FROM core.modules WHERE status NOT IN ('removed', 'cancelled') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var mods []modRow
	for rows.Next() {
		r, err := scanMod(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		mods = append(mods, r)
	}
	rows.Close()
	out := []View{}
	for _, r := range mods {
		v, err := s.view(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns one module.
func (s *Service) Get(ctx context.Context, p domain.Principal, id string) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	r, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	if r.Status == StatusRemoved || r.Status == StatusCancelled {
		// Removed and cancelled are history, not contract Module states.
		return View{}, domain.NotFound("module")
	}
	return s.view(ctx, r)
}

// --- install ---

// verify reads, verifies and checks a package before anything in it is used.
func (s *Service) verify(ctx context.Context, raw []byte) (*Package, TrustedKey, []db.Migration, error) {
	pkg, err := ReadPackage(raw)
	if err != nil {
		return nil, TrustedKey{}, nil, err
	}
	keys, err := s.trustedKeys(ctx)
	if err != nil {
		return nil, TrustedKey{}, nil, err
	}
	key, err := pkg.Verify(keys)
	if err != nil {
		return nil, TrustedKey{}, nil, err
	}
	ms, err := pkg.Migrations()
	if err != nil {
		return nil, TrustedKey{}, nil, err
	}
	if err := CheckMigrations(pkg.Manifest, ms); err != nil {
		return nil, TrustedKey{}, nil, err
	}
	return pkg, key, ms, nil
}

// Install verifies a module package and records it awaiting the admin's permission
// decisions (PRD 9.3). Nothing runs and no schema is created until they are approved.
func (s *Service) Install(ctx context.Context, p domain.Principal, raw []byte, source string) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	if source != "local" && source != "marketplace" {
		return View{}, invalid("source", "source is local or marketplace")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg, key, ms, err := s.verify(ctx, raw)
	if err != nil {
		return View{}, err
	}
	if source == "marketplace" && key.Source != "marketplace" {
		return View{}, refuse(CodeUnsigned, "a marketplace install must be signed with a key published through the marketplace")
	}
	m := pkg.Manifest
	existing, err := s.row(ctx, m.ID)
	switch {
	case err == nil && existing.Status != StatusRemoved && existing.Status != StatusCancelled:
		return View{}, domain.Conflict("module " + m.ID + " is already installed; update it instead")
	case err != nil && !isNotFound(err):
		return View{}, err
	}
	// A kept schema from an earlier uninstall is picked up; its migration history must
	// still match this package.
	if err := s.schemas.CheckHistory(ctx, m.ID, ms); err != nil {
		return View{}, refuse(CodeMigration, err.Error())
	}
	var out View
	err = s.commit(ctx, func(ctx context.Context) error {
		pid, err := s.savePackage(ctx, p, pkg, key, raw)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_permissions WHERE module_id = $1`, m.ID); err != nil {
			return err
		}
		for _, perm := range m.Permissions {
			if err := s.putPermission(ctx, p, m.ID, perm.Scope, perm.IsRequired(), "pending"); err != nil {
				return err
			}
		}
		r := modRow{ID: m.ID, Name: m.Name, Version: m.Version, Status: StatusAwaiting, Source: source, PackageID: pid,
			SchemaState: "none", CreatedBy: string(p.UserID), CreatedAt: s.now().UnixMicro()}
		if existing.ID != "" {
			r.SchemaState, r.DBRole, r.DataQuietSince = existing.SchemaState, existing.DBRole, existing.DataQuietSince
		}
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, m.ID, "status", existing.Status, StatusAwaiting, "install "+m.Version+" signed by "+key.KeyID); err != nil {
			return err
		}
		if err := s.event(ctx, p, "module.install_requested", m.ID, "Install requested: "+m.Name+" "+m.Version, ""); err != nil {
			return err
		}
		out, err = s.view(ctx, r)
		return err
	})
	return out, err
}

// InstallFromMarketplace downloads a listing's version and installs it. A pulled module
// can't be downloaded; the firm can still install it from a local file (PRD 10.4).
func (s *Service) InstallFromMarketplace(ctx context.Context, p domain.Principal, listing, version string) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	if s.d.Market == nil {
		return View{}, &domain.APIError{Code: "marketplace_unconfigured", Message: "no marketplace is configured"}
	}
	raw, err := s.d.Market.Package(ctx, listing, version)
	if err != nil {
		return View{}, err
	}
	return s.Install(ctx, p, raw, "marketplace")
}

func isNotFound(err error) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == domain.CodeNotFound
}

// Decision is one scope decision.
type Decision struct {
	Scope    string `json:"scope"`
	Decision string `json:"decision"`
}

// DecidePermissions records the admin's approve or decline for each requested scope.
// A declined required scope cancels the install, or drops a pending update while the
// old version keeps running. When every required scope is approved the module installs
// (or updates) without a core restart.
func (s *Service) DecidePermissions(ctx context.Context, p domain.Principal, id string, decisions []Decision) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	pendingUpdate := r.PendingPackageID != "" && (r.Status == StatusInstalled || r.Status == StatusDisabled)
	if r.Status != StatusAwaiting && !pendingUpdate {
		return View{}, domain.Conflict("module " + id + " has no permissions awaiting a decision")
	}
	perms, err := s.permissions(ctx, id)
	if err != nil {
		return View{}, err
	}
	byScope := map[string]string{}
	for _, d := range decisions {
		if d.Decision != "approve" && d.Decision != "decline" {
			return View{}, invalid("decisions", "a decision is approve or decline")
		}
		byScope[d.Scope] = d.Decision
	}
	// During an update the pending manifest says which scopes are required; rows that
	// were approved earlier still carry the running version's flag.
	required := map[string]bool{}
	for _, pv := range perms {
		required[pv.Scope] = pv.Required
	}
	if pendingUpdate {
		pkg, err := s.loadPackage(ctx, r.PendingPackageID)
		if err != nil {
			return View{}, err
		}
		for _, perm := range pkg.Manifest.Permissions {
			required[perm.Scope] = perm.IsRequired()
		}
	}
	for scope := range byScope {
		found := false
		for _, pv := range perms {
			found = found || pv.Scope == scope
		}
		if !found {
			return View{}, invalid("decisions", scope+" isn't requested")
		}
	}
	// changes holds every scope whose decision moves: each pending scope, and any
	// earlier decision the admin explicitly reverses (an approved scope can be declined
	// here, PRD 9.3: the reissued token carries only what stays approved).
	type change struct {
		scope, from, to string
		required        bool
	}
	var changes []change
	declinedRequired := ""
	for _, pv := range perms {
		d, ok := byScope[pv.Scope]
		if !ok {
			if pv.Decision == "pending" {
				return View{}, invalid("decisions", "decide every requested permission: "+pv.Scope+" is missing")
			}
			continue
		}
		to := map[string]string{"approve": "approved", "decline": "declined"}[d]
		if to == pv.Decision {
			continue
		}
		changes = append(changes, change{pv.Scope, pv.Decision, to, required[pv.Scope]})
		if to == "declined" && required[pv.Scope] && declinedRequired == "" {
			declinedRequired = pv.Scope
		}
	}
	if declinedRequired != "" {
		err := s.commit(ctx, func(ctx context.Context) error {
			for _, c := range changes {
				if pendingUpdate {
					// The update is dropped: its new scopes go, and the running version
					// keeps the decisions it runs under.
					if c.from != "pending" {
						continue
					}
					if _, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_permissions WHERE module_id = $1 AND scope = $2`, id, c.scope); err != nil {
						return err
					}
					continue
				}
				if err := s.putPermission(ctx, p, id, c.scope, c.required, c.to); err != nil {
					return err
				}
			}
			old := r.Status
			what := "install cancelled"
			if pendingUpdate {
				r.PendingPackageID = ""
				what = "update dropped; version " + r.Version + " keeps running"
			} else {
				r.Status = StatusCancelled
			}
			if err := s.saveRow(ctx, r); err != nil {
				return err
			}
			if err := s.audit(ctx, p, id, "status", old, r.Status, "required permission "+declinedRequired+" declined: "+what); err != nil {
				return err
			}
			return s.event(ctx, p, "module.permissions_decided", id, "Required permission "+declinedRequired+" declined: "+what, "")
		})
		if err != nil {
			return View{}, err
		}
		if pendingUpdate {
			return View{}, refuse(CodeDeclined, "required permission "+declinedRequired+" was declined; the update is dropped and version "+r.Version+" keeps running")
		}
		return View{}, refuse(CodeDeclined, "required permission "+declinedRequired+" was declined; the install is cancelled")
	}
	if err := s.commit(ctx, func(ctx context.Context) error {
		for _, c := range changes {
			if err := s.putPermission(ctx, p, id, c.scope, c.required, c.to); err != nil {
				return err
			}
			if err := s.audit(ctx, p, id, "permission:"+c.scope, c.from, c.to, ""); err != nil {
				return err
			}
		}
		return s.event(ctx, p, "module.permissions_decided", id, "Permissions decided", "")
	}); err != nil {
		return View{}, err
	}
	if pendingUpdate {
		prior := map[string]string{}
		for _, c := range changes {
			prior[c.scope] = c.from
		}
		if _, err := s.applyUpdate(ctx, p, r, r.PendingPackageID, prior); err != nil {
			return View{}, err
		}
	} else if err := s.activate(ctx, p, r); err != nil {
		return View{}, err
	}
	r, err = s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(ctx, r)
}

func (s *Service) approvedScopes(ctx context.Context, module string) ([]string, error) {
	perms, err := s.permissions(ctx, module)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, pv := range perms {
		if pv.Approved {
			out = append(out, pv.Scope)
		}
	}
	sort.Strings(out)
	return out, nil
}

// activate creates the schema and login, runs the migrations, issues the scoped token
// and brings the module up: help pages, widgets and its container.
func (s *Service) activate(ctx context.Context, p domain.Principal, r modRow) error {
	pkg, err := s.loadPackage(ctx, r.PackageID)
	if err != nil {
		return err
	}
	parsed, err := ReadPackage(pkg.Archive)
	if err != nil {
		return err
	}
	ms, err := parsed.Migrations()
	if err != nil {
		return err
	}
	existed, err := s.schemas.SchemaExists(ctx, r.ID)
	if err != nil {
		return err
	}
	role, password, err := s.schemas.Provision(ctx, r.ID)
	if err != nil {
		return err
	}
	if _, err := s.schemas.Migrate(ctx, r.ID, role, ms); err != nil {
		if !existed {
			if derr := s.schemas.Drop(ctx, r.ID); derr != nil {
				log.Printf("modules: drop %s after a failed install: %v", r.ID, derr)
			}
		} else if lerr := s.schemas.Lock(ctx, r.ID); lerr != nil {
			log.Printf("modules: lock %s after a failed install: %v", r.ID, lerr)
		} else {
			r.DataQuietSince = s.now().UnixMicro()
		}
		r.Status, r.LastError = StatusCancelled, err.Error()
		if serr := s.commit(ctx, func(ctx context.Context) error {
			if err := s.saveRow(ctx, r); err != nil {
				return err
			}
			return s.audit(ctx, p, r.ID, "status", StatusAwaiting, StatusCancelled, "migration failed")
		}); serr != nil {
			return serr
		}
		return refuse(CodeMigration, err.Error())
	}
	scopes, err := s.approvedScopes(ctx, r.ID)
	if err != nil {
		return err
	}
	tokenID, tokenSecret, err := s.d.Tokens.IssueModuleToken(ctx, p, r.ID, scopes)
	if err != nil {
		return err
	}
	webhook, err := randomSecret()
	if err != nil {
		return err
	}
	r.TokenID, r.DBRole, r.SchemaState, r.LastError, r.DataQuietSince = tokenID, role, "active", "", int64(zero)
	if r.TokenSecret, err = s.seal(tokenSecret); err != nil {
		return err
	}
	if r.WebhookSecret, err = s.seal(webhook); err != nil {
		return err
	}
	if r.DBPassword, err = s.seal(password); err != nil {
		return err
	}
	r.Status, r.Enabled = StatusInstalled, true
	if err := s.commit(ctx, func(ctx context.Context) error {
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, r.ID, "status", StatusAwaiting, StatusInstalled, "installed "+r.Version); err != nil {
			return err
		}
		return s.event(ctx, p, "module.installed", r.ID, "Installed "+r.Name+" "+r.Version, "")
	}); err != nil {
		return err
	}
	return s.bringUp(ctx, p, r, pkg.Manifest)
}

// bringUp publishes the module's guide and widgets and starts its container. These are
// live registrations: no core restart (PRD 9.3).
func (s *Service) bringUp(ctx context.Context, p domain.Principal, r modRow, m Manifest) error {
	s.forget(r.ID)
	var errs []string
	if s.d.Help != nil {
		pages := make([]help.PageInput, 0, len(m.Help.Pages))
		for _, pg := range m.Help.Pages {
			pages = append(pages, help.PageInput{ID: pg.ID, Title: pg.Title, Markdown: pg.Body, Keywords: pg.Keywords, Covers: pg.Covers})
		}
		if _, err := s.d.Help.ReplacePages(ctx, p, r.ID, pages); err != nil {
			errs = append(errs, "help: "+err.Error())
		} else if err := s.d.Help.SetModuleEnabled(ctx, r.ID, true); err != nil {
			errs = append(errs, "help: "+err.Error())
		}
	}
	if s.d.Widgets != nil {
		ws, err := m.DashboardWidgets()
		if err == nil {
			s.d.Widgets.RemoveModule(r.ID)
			err = s.d.Widgets.AddModule(r.ID, ws, func(widgetID string) dashboard.Source { return s.widgetSource(r.ID, widgetID) })
		}
		if err != nil {
			errs = append(errs, "widgets: "+err.Error())
		}
	}
	if s.d.Runtime != nil {
		dep, err := s.deployment(ctx, r, m)
		if err == nil {
			err = s.d.Runtime.Deploy(ctx, dep)
		}
		if err != nil {
			errs = append(errs, "container: "+err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	r.LastError = strings.Join(errs, "; ")
	return s.commit(ctx, func(ctx context.Context) error { return s.saveRow(ctx, r) })
}

func (s *Service) deployment(ctx context.Context, r modRow, m Manifest) (Deployment, error) {
	token, err := s.open(r.TokenSecret)
	if err != nil {
		return Deployment{}, err
	}
	webhook, err := s.open(r.WebhookSecret)
	if err != nil {
		return Deployment{}, err
	}
	pass, err := s.open(r.DBPassword)
	if err != nil {
		return Deployment{}, err
	}
	return Deployment{Module: r.ID, Version: m.Version, Image: m.Container.Image, Digest: m.Container.Digest, Port: m.Container.Port,
		Secrets: map[string]string{"togavsu_api_token": token, "togavsu_webhook_secret": webhook, "togavsu_db_role": r.DBRole,
			"togavsu_db_password": pass, "togavsu_db_schema": SchemaFor(r.ID)}}, nil
}

// bringDown removes the module from the screens and stops its container.
func (s *Service) bringDown(ctx context.Context, module string, remove bool) []string {
	s.forget(module)
	var errs []string
	if s.d.Widgets != nil {
		s.d.Widgets.RemoveModule(module)
	}
	if s.d.Help != nil {
		var err error
		if remove {
			err = s.d.Help.RemoveModule(ctx, module)
		} else {
			err = s.d.Help.SetModuleEnabled(ctx, module, false)
		}
		if err != nil && !isNotFound(err) {
			errs = append(errs, "help: "+err.Error())
		}
	}
	if s.d.Runtime != nil {
		if err := s.d.Runtime.Stop(ctx, module); err != nil {
			errs = append(errs, "container: "+err.Error())
		}
		if remove {
			if err := s.d.Runtime.Remove(ctx, module); err != nil {
				errs = append(errs, "container: "+err.Error())
			}
		}
	}
	return errs
}

// Enable turns an installed, disabled module back on with a fresh token.
func (s *Service) Enable(ctx context.Context, p domain.Principal, id string) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	if r.Status != StatusDisabled && r.Status != StatusInstalled {
		return View{}, domain.Conflict("module " + id + " isn't installed")
	}
	pkg, err := s.loadPackage(ctx, r.PackageID)
	if err != nil {
		return View{}, err
	}
	if !r.Enabled {
		scopes, err := s.approvedScopes(ctx, id)
		if err != nil {
			return View{}, err
		}
		tid, secret, err := s.d.Tokens.IssueModuleToken(ctx, p, id, scopes)
		if err != nil {
			return View{}, err
		}
		r.TokenID = tid
		if r.TokenSecret, err = s.seal(secret); err != nil {
			return View{}, err
		}
		if _, password, err := s.schemas.Provision(ctx, id); err != nil {
			return View{}, err
		} else if r.DBPassword, err = s.seal(password); err != nil {
			return View{}, err
		}
		r.DataQuietSince = int64(zero)
	}
	r.Status, r.Enabled, r.LastError = StatusInstalled, true, ""
	if err := s.commit(ctx, func(ctx context.Context) error {
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "enabled", "false", "true", ""); err != nil {
			return err
		}
		return s.event(ctx, p, "module.enabled", id, "Enabled "+r.Name, "")
	}); err != nil {
		return View{}, err
	}
	if err := s.bringUp(ctx, p, r, pkg.Manifest); err != nil {
		return View{}, err
	}
	return s.Get(ctx, p, id)
}

// Disable stops the module and revokes its token; its schema and data stay (PRD 9.9).
func (s *Service) Disable(ctx context.Context, p domain.Principal, id string) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	if r.Status != StatusInstalled && r.Status != StatusDisabled {
		return View{}, domain.Conflict("module " + id + " isn't installed")
	}
	if d, err := s.replacing(ctx, id); err != nil {
		return View{}, err
	} else if d != "" {
		return View{}, domain.Conflict("module " + id + " replaces " + d + "; switch " + d + " back to the core first")
	}
	if r.TokenID != "" {
		if err := s.d.Tokens.RevokeModuleToken(ctx, p, r.TokenID); err != nil && !isNotFound(err) {
			return View{}, err
		}
	}
	r.Status, r.Enabled, r.TokenID, r.TokenSecret = StatusDisabled, false, "", ""
	if err := s.commit(ctx, func(ctx context.Context) error {
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "enabled", "true", "false", ""); err != nil {
			return err
		}
		return s.event(ctx, p, "module.disabled", id, "Disabled "+r.Name, "")
	}); err != nil {
		return View{}, err
	}
	if errs := s.bringDown(ctx, id, false); len(errs) > 0 {
		log.Printf("modules: disable %s: %s", id, strings.Join(errs, "; "))
	}
	// Lock the login so the stopped module can't write; Enable unlocks it with a new
	// password. From here a backup holds all of the module's data (Delete data, PRD 9.9).
	if err := s.schemas.Lock(ctx, id); err != nil {
		log.Printf("modules: disable %s: lock login: %v", id, err)
	} else {
		r.DataQuietSince = s.now().UnixMicro()
		if err := s.commit(ctx, func(ctx context.Context) error { return s.saveRow(ctx, r) }); err != nil {
			return View{}, err
		}
	}
	return s.Get(ctx, p, id)
}

// UpdateResult is the API contract's ModuleUpdateResult.
type UpdateResult struct {
	Module          View   `json:"module"`
	PreviousVersion string `json:"previous_version"`
	Outcome         string `json:"outcome"`
}

// Update installs a new signed version. New permissions wait for the admin while the old
// version keeps running; a migration that already ran and changed refuses the update; a
// failing migration rolls back and the old version keeps running (PRD 9.3, 9.9).
func (s *Service) Update(ctx context.Context, p domain.Principal, id string, raw []byte, source string) (UpdateResult, error) {
	if err := requireAdmin(p); err != nil {
		return UpdateResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.row(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	if r.Status != StatusInstalled && r.Status != StatusDisabled {
		return UpdateResult{}, domain.Conflict("module " + id + " isn't installed")
	}
	pkg, key, ms, err := s.verify(ctx, raw)
	if err != nil {
		return UpdateResult{}, err
	}
	m := pkg.Manifest
	if m.ID != id {
		return UpdateResult{}, invalid("package", "this package is module "+m.ID+", not "+id)
	}
	if m.Version == r.Version {
		return UpdateResult{}, domain.Conflict("version " + m.Version + " is already installed")
	}
	if err := s.schemas.CheckHistory(ctx, id, ms); err != nil {
		return UpdateResult{}, refuse(CodeMigration, err.Error())
	}
	current, err := s.permissions(ctx, id)
	if err != nil {
		return UpdateResult{}, err
	}
	approved := map[string]bool{}
	for _, pv := range current {
		if pv.Approved {
			approved[pv.Scope] = true
		}
	}
	var fresh []Permission
	for _, perm := range m.Permissions {
		if !approved[perm.Scope] {
			fresh = append(fresh, perm)
		}
	}
	var pid string
	err = s.commit(ctx, func(ctx context.Context) error {
		var err error
		if pid, err = s.savePackage(ctx, p, pkg, key, raw); err != nil {
			return err
		}
		if len(fresh) == 0 {
			return s.event(ctx, p, "module.update_requested", id, "Update to "+m.Version, "")
		}
		for _, perm := range fresh {
			if err := s.putPermission(ctx, p, id, perm.Scope, perm.IsRequired(), "pending"); err != nil {
				return err
			}
		}
		r.PendingPackageID = pid
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "pending_version", "", m.Version, "new permissions await approval"); err != nil {
			return err
		}
		return s.event(ctx, p, "module.update_requested", id, "Update to "+m.Version+" awaits permission approval", "")
	})
	if err != nil {
		return UpdateResult{}, err
	}
	prev := r.Version
	if len(fresh) > 0 {
		v, err := s.view(ctx, r)
		return UpdateResult{Module: v, PreviousVersion: prev, Outcome: "awaiting_approval"}, err
	}
	return s.applyUpdate(ctx, p, r, pid, nil)
}

// applyUpdate runs the new version's migrations and switches to it. prior holds the
// permission decisions the admin just changed for this update (scope to earlier
// decision); if the update fails, keepPrevious brings the decisions and the running
// token back in step with the version that keeps running.
func (s *Service) applyUpdate(ctx context.Context, p domain.Principal, r modRow, pid string, prior map[string]string) (UpdateResult, error) {
	prev := r.Version
	pkg, err := s.loadPackage(ctx, pid)
	if err != nil {
		return UpdateResult{}, err
	}
	parsed, err := ReadPackage(pkg.Archive)
	if err != nil {
		return UpdateResult{}, err
	}
	ms, err := parsed.Migrations()
	if err != nil {
		return UpdateResult{}, err
	}
	_, err = s.schemas.Migrate(ctx, r.ID, r.DBRole, ms)
	if !r.Enabled && r.DataQuietSince > int64(zero) {
		// Migrations change a locked schema too; an earlier backup no longer holds it.
		r.DataQuietSince = s.now().UnixMicro()
	}
	if err != nil {
		refused := errors.Is(err, ErrChangedMigration) || errors.Is(err, ErrMissingMigration)
		save := len(prior) > 0 || r.PendingPackageID != "" || !refused
		r.PendingPackageID = ""
		if !refused {
			r.LastError = err.Error()
		}
		after, kerr := s.keepPrevious(ctx, p, &r, prior, pkg.Version)
		if kerr != nil {
			return UpdateResult{}, kerr
		}
		if save {
			if serr := s.commit(ctx, func(ctx context.Context) error {
				if err := s.saveRow(ctx, r); err != nil {
					return err
				}
				if refused {
					return nil
				}
				return s.audit(ctx, p, r.ID, "version", prev, prev, "update to "+pkg.Version+" kept the previous version: "+err.Error())
			}); serr != nil {
				return UpdateResult{}, serr
			}
		}
		if aerr := after(); aerr != nil {
			return UpdateResult{}, aerr
		}
		if refused {
			return UpdateResult{}, refuse(CodeMigration, err.Error())
		}
		v, verr := s.view(ctx, r)
		return UpdateResult{Module: v, PreviousVersion: prev, Outcome: "kept_previous"}, verr
	}
	if err := s.commit(ctx, func(ctx context.Context) error { return s.dropUnrequested(ctx, p, r.ID, pkg.Manifest) }); err != nil {
		return UpdateResult{}, err
	}
	s.forget(r.ID)
	scopes, err := s.approvedScopes(ctx, r.ID)
	if err != nil {
		return UpdateResult{}, err
	}
	if r.Enabled {
		tid, secret, err := s.d.Tokens.IssueModuleToken(ctx, p, r.ID, scopes)
		if err != nil {
			return UpdateResult{}, err
		}
		old := r.TokenID
		r.TokenID = tid
		if r.TokenSecret, err = s.seal(secret); err != nil {
			return UpdateResult{}, err
		}
		defer func() {
			if old != "" {
				if err := s.d.Tokens.RevokeModuleToken(ctx, p, old); err != nil {
					log.Printf("modules: revoke old token of %s: %v", r.ID, err)
				}
			}
		}()
	}
	r.Version, r.Name, r.PackageID, r.PendingPackageID, r.LastError = pkg.Manifest.Version, pkg.Manifest.Name, pid, "", ""
	if err := s.commit(ctx, func(ctx context.Context) error {
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if err := s.audit(ctx, p, r.ID, "version", prev, r.Version, "updated"); err != nil {
			return err
		}
		return s.event(ctx, p, "module.updated", r.ID, "Updated "+r.Name+" to "+r.Version, "")
	}); err != nil {
		return UpdateResult{}, err
	}
	if r.Enabled {
		if err := s.bringUp(ctx, p, r, pkg.Manifest); err != nil {
			return UpdateResult{}, err
		}
	}
	v, err := s.view(ctx, r)
	return UpdateResult{Module: v, PreviousVersion: prev, Outcome: "updated"}, err
}

// keepPrevious brings a module whose update failed back in step with the version that
// keeps running (PRD 9.3). Decisions for scopes only the failed version requested are
// removed, so a later Enable or event delivery can't grant them. prior holds the
// decisions the admin changed for this update (scope to earlier decision): each goes
// back to its earlier value, except an approved scope the running version doesn't
// require that the admin declined. That decline stands, and the running module gets a
// token reissued from what stays approved, so the withdrawn scope leaves the running
// token too. The caller saves r, then calls the returned func to revoke the old token
// and redeploy with the new one.
func (s *Service) keepPrevious(ctx context.Context, p domain.Principal, r *modRow, prior map[string]string, failed string) (func() error, error) {
	none := func() error { return nil }
	running, err := s.loadPackage(ctx, r.PackageID)
	if err != nil {
		return none, err
	}
	required := map[string]bool{}
	for _, perm := range running.Manifest.Permissions {
		required[perm.Scope] = perm.IsRequired()
	}
	narrowed := false
	err = s.commit(ctx, func(ctx context.Context) error {
		if err := s.dropUnrequested(ctx, p, r.ID, running.Manifest); err != nil {
			return err
		}
		current, err := s.permissions(ctx, r.ID)
		if err != nil {
			return err
		}
		for _, pv := range current {
			was, changed := prior[pv.Scope]
			if !changed {
				continue
			}
			to := was
			if was == "approved" && pv.Decision == "declined" && !required[pv.Scope] {
				to, narrowed = "declined", true
			}
			if err := s.putPermission(ctx, p, r.ID, pv.Scope, required[pv.Scope], to); err != nil {
				return err
			}
			if to != pv.Decision {
				if err := s.audit(ctx, p, r.ID, "permission:"+pv.Scope, pv.Decision, to, "update to "+failed+" failed; version "+r.Version+" keeps its decision"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil || !narrowed || !r.Enabled {
		return none, err
	}
	scopes, err := s.approvedScopes(ctx, r.ID)
	if err != nil {
		return none, err
	}
	tid, secret, err := s.d.Tokens.IssueModuleToken(ctx, p, r.ID, scopes)
	if err != nil {
		return none, err
	}
	sealed, err := s.seal(secret)
	if err != nil {
		return none, err
	}
	old := r.TokenID
	r.TokenID, r.TokenSecret = tid, sealed
	rr := *r
	return func() error {
		if old != "" {
			if err := s.d.Tokens.RevokeModuleToken(ctx, p, old); err != nil && !isNotFound(err) {
				return err
			}
		}
		return s.bringUp(ctx, p, rr, running.Manifest)
	}, nil
}

// dropUnrequested removes the module's permission decisions for scopes the new
// manifest no longer requests, so the reissued token carries only what the running
// version declares (PRD 9.3). Each removal is audited.
func (s *Service) dropUnrequested(ctx context.Context, p domain.Principal, id string, m Manifest) error {
	want := map[string]bool{}
	for _, perm := range m.Permissions {
		want[perm.Scope] = true
	}
	current, err := s.permissions(ctx, id)
	if err != nil {
		return err
	}
	for _, pv := range current {
		if want[pv.Scope] {
			continue
		}
		if _, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_permissions WHERE module_id = $1 AND scope = $2`, id, pv.Scope); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "permission:"+pv.Scope, pv.Decision, "removed", "version "+m.Version+" no longer requests it"); err != nil {
			return err
		}
	}
	return nil
}

// backupHolds reports why b can't stand in for a backup of the module's current data:
// it must be a complete archive this installation wrote, not one being deleted, taken
// after the schema last stopped changing (quietSince). A module that can still write
// (quietSince 0) has no such backup.
func backupHolds(b Backup, quietSince int64) error {
	switch {
	case b.ID == "" || b.SHA256 == "":
		return errors.New("the backup is incomplete")
	case b.Source != "":
		return errors.New("the backup came from " + b.Source + ", not from this installation")
	case b.Deleting:
		return errors.New("the backup is being deleted")
	case quietSince == int64(zero):
		return errors.New("the module can still write its data, so an earlier backup may miss some of it")
	case !b.CreatedAt.After(time.UnixMicro(quietSince)):
		return errors.New("the backup is older than the module's last data change")
	}
	return nil
}

// freshBackup locks the module's login, so nothing writes between the backup and the
// drop, and takes a full backup. If the backup fails, the login is unlocked again and
// the module keeps running.
func (s *Service) freshBackup(ctx context.Context, r modRow) (Backup, error) {
	exists, err := s.schemas.SchemaExists(ctx, r.ID)
	if err != nil {
		return Backup{}, err
	}
	locked := false
	if exists && r.DataQuietSince == int64(zero) {
		if err := s.schemas.Lock(ctx, r.ID); err != nil {
			return Backup{}, err
		}
		locked = true
	}
	b, err := s.d.Backups.Create(ctx, "before deleting module "+r.ID+" data")
	if err == nil && (b.ID == "" || b.SHA256 == "") {
		err = errors.New("the backup is incomplete")
	}
	if err != nil {
		if locked && r.Enabled {
			if uerr := s.schemas.Unlock(ctx, r.ID); uerr != nil {
				log.Printf("modules: unlock %s after a failed backup: %v", r.ID, uerr)
			}
		}
		return Backup{}, domain.Conflict("backup failed, so the module data was not deleted: " + err.Error())
	}
	return b, nil
}

// UninstallInput is the API contract's ModuleUninstall.
type UninstallInput struct {
	Data         string  `json:"data"`
	Confirmation string  `json:"confirmation"`
	BackupID     *string `json:"backup_id"`
}

// Uninstall removes the module without a restart. Keep data locks the schema for a later
// reinstall; Delete data drops the schema and its login, only after the typed
// confirmation (the module id) and a backup that includes the schema (PRD 9.9).
func (s *Service) Uninstall(ctx context.Context, p domain.Principal, id string, in UninstallInput) (View, error) {
	if err := requireAdmin(p); err != nil {
		return View{}, err
	}
	if in.Data != "keep" && in.Data != "delete" {
		return View{}, invalid("data", "choose keep or delete")
	}
	if in.Confirmation != id {
		return View{}, invalid("confirmation", "type the module id, "+id+", to confirm")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	if r.Status == StatusRemoved || r.Status == StatusCancelled {
		return View{}, domain.NotFound("module")
	}
	if d, err := s.replacing(ctx, id); err != nil {
		return View{}, err
	} else if d != "" {
		return View{}, domain.Conflict("module " + id + " replaces " + d + "; switch " + d + " back to the core first")
	}
	backup := ""
	if in.Data == "delete" {
		if s.d.Backups == nil {
			return View{}, domain.Conflict("no backup service is available; data can't be deleted without a backup")
		}
		if in.BackupID != nil && *in.BackupID != "" {
			b, err := s.d.Backups.Get(ctx, *in.BackupID)
			if err == nil {
				err = backupHolds(b, r.DataQuietSince)
			}
			if err != nil {
				return View{}, domain.Conflict("that backup can't be used, so the module data was not deleted: " + err.Error() +
					". Leave the backup empty and Togavsu takes a new one")
			}
			backup = b.ID
		} else {
			b, err := s.freshBackup(ctx, r)
			if err != nil {
				return View{}, err
			}
			backup = b.ID
		}
	}
	old := r.Status
	r.Status = StatusUninstalling
	if err := s.commit(ctx, func(ctx context.Context) error { return s.saveRow(ctx, r) }); err != nil {
		return View{}, err
	}
	if r.TokenID != "" {
		if err := s.d.Tokens.RevokeModuleToken(ctx, p, r.TokenID); err != nil && !isNotFound(err) {
			return View{}, err
		}
	}
	if errs := s.bringDown(ctx, id, true); len(errs) > 0 {
		log.Printf("modules: uninstall %s: %s", id, strings.Join(errs, "; "))
	}
	if exists, err := s.schemas.SchemaExists(ctx, id); err != nil {
		return View{}, err
	} else if exists {
		if in.Data == "delete" {
			err = s.schemas.Drop(ctx, id)
			r.SchemaState, r.DataQuietSince = "dropped", int64(zero)
		} else {
			err = s.schemas.Lock(ctx, id)
			r.SchemaState, r.DataQuietSince = "locked", s.now().UnixMicro()
		}
		if err != nil {
			return View{}, err
		}
	}
	r.Status, r.Enabled, r.TokenID, r.TokenSecret, r.DBPassword, r.WebhookSecret, r.PendingPackageID = StatusRemoved, false, "", "", "", "", ""
	reason := "uninstalled; data kept"
	if in.Data == "delete" {
		reason = "uninstalled; data deleted after backup " + backup
	}
	if err := s.commit(ctx, func(ctx context.Context) error {
		if err := s.saveRow(ctx, r); err != nil {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_deliveries SET status = 'failed', last_error = 'module removed', updated_at = $1 WHERE module_id = $2 AND status = 'pending'`, s.now().UnixMicro(), id); err != nil {
			return err
		}
		if err := s.audit(ctx, p, id, "status", old, StatusRemoved, reason); err != nil {
			return err
		}
		return s.event(ctx, p, "module.removed", id, "Removed "+r.Name+": "+reason, "")
	}); err != nil {
		return View{}, err
	}
	// The contract's Module.status has no removed value: the response reports the last
	// contract state, uninstalling, with the module disabled. The record then leaves List
	// and Get (not_found), and module.removed has gone out with the commit.
	v, err := s.view(ctx, r)
	v.Status, v.Enabled = StatusUninstalling, false
	return v, err
}

// --- installed cache ---

func (s *Service) forget(id string) {
	s.cacheMu.Lock()
	delete(s.cache, id)
	s.cacheMu.Unlock()
}

// Installed returns an installed module's manifest and approvals. It reads through a
// small cache that lifecycle changes clear, so screens and the API follow installs,
// updates and removals without a restart.
func (s *Service) Installed(ctx context.Context, id string) (*Installed, error) {
	s.cacheMu.RLock()
	in, ok := s.cache[id]
	s.cacheMu.RUnlock()
	if ok {
		return in, nil
	}
	r, err := s.row(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Status != StatusInstalled && r.Status != StatusDisabled {
		return nil, domain.NotFound("module")
	}
	pkg, err := s.loadPackage(ctx, r.PackageID)
	if err != nil {
		return nil, err
	}
	scopes, err := s.approvedScopes(ctx, id)
	if err != nil {
		return nil, err
	}
	in = &Installed{ID: r.ID, Version: r.Version, Status: r.Status, Enabled: r.Enabled, Manifest: pkg.Manifest, Approved: map[string]bool{}, TokenID: r.TokenID}
	for _, sc := range scopes {
		in.Approved[sc] = true
	}
	s.cacheMu.Lock()
	s.cache[id] = in
	s.cacheMu.Unlock()
	return in, nil
}

// Enabled lists the enabled modules.
func (s *Service) Enabled(ctx context.Context) ([]*Installed, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id FROM core.modules WHERE status = 'installed' AND enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := make([]*Installed, 0, len(ids))
	for _, id := range ids {
		in, err := s.Installed(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

// Start re-registers every enabled module's widgets and guide when the core starts.
// Help pages are persistent; the widget catalog is in memory.
func (s *Service) Start(ctx context.Context) error {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return err
	}
	for _, in := range mods {
		if s.d.Widgets == nil {
			break
		}
		ws, err := in.Manifest.DashboardWidgets()
		if err != nil {
			return err
		}
		id := in.ID
		if err := s.d.Widgets.AddModule(id, ws, func(widgetID string) dashboard.Source { return s.widgetSource(id, widgetID) }); err != nil {
			return err
		}
	}
	return nil
}

package marketplace

import (
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the marketplace migrations (range 1100-1199). They run after the
// core migrations, in schema core, under the core owner.
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// Settings is the part of the settings registry the service reads on every call, so a
// change applies on the next one.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
}

// Users lists the firm's users; the service picks the active admins from it.
type Users interface {
	List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error)
}

// InstalledModule is one module the firm runs, from the module runtime.
type InstalledModule struct {
	ID      string
	Version string
}

// Installed lists the installed modules (internal/modules supplies it). Nothing in this
// package changes an installed module: the firm admin decides (PRD 10.5).
type Installed interface {
	InstalledModules(ctx context.Context) ([]InstalledModule, error)
}

// Deps wires the service. DB must be the pool the audit service writes through, so an
// alert, its notifications, its mail and its audit row commit together.
type Deps struct {
	DB        *sql.DB
	Audit     *audit.Service
	Settings  Settings
	Users     Users
	Installed Installed
	// Client defaults to one that reads modules.marketplace_address on each request.
	Client *Client
	Now    func() time.Time
	// Logf, when set, receives failures that have no caller to return to.
	Logf func(format string, args ...any)
}

// Service is the core's marketplace client side (PRD 10.5, 9.3): browsing, verified
// installs from the marketplace, signature checks for local files, the firm's trusted
// local keys, and flag and pull alerts for installed modules.
type Service struct {
	d       Deps
	checkMu sync.Mutex
}

// New returns the service.
func New(d Deps) (*Service, error) {
	if d.DB == nil || d.Audit == nil || d.Settings == nil || d.Users == nil {
		return nil, errors.New("marketplace: DB, Audit, Settings and Users are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Service{d: d}
	if s.d.Client == nil {
		s.d.Client = &Client{Address: func(ctx context.Context) string { return s.setting(ctx, "modules.marketplace_address") }}
	}
	return s, nil
}

// SetInstalled wires the module runtime once it is composed.
func (s *Service) SetInstalled(i Installed) { s.d.Installed = i }

func (s *Service) setting(ctx context.Context, key string) string {
	v, _ := s.d.Settings.Get(ctx, key)
	return v
}

func (s *Service) now() time.Time { return s.d.Now().UTC() }

func micro(t time.Time) int64 { return domain.UnixMicroUTC(t) }

func fromMicro(v int64) time.Time { return domain.FromUnixMicro(v) }

// admin is the contract's access rule for every marketplace operation: admin only, and
// never a client.
func admin(p domain.Principal) error {
	if p.UserID.IsZero() && p.Source != domain.SourceModule {
		return domain.Unauthenticated("sign in is required")
	}
	if !p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleClient) {
		return domain.Forbidden("admins only")
	}
	return nil
}

// apiError maps a client or verification error onto the shared error table.
func apiError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return domain.NotFound("marketplace listing")
	case errors.Is(err, ErrYanked):
		return &Error{Code: "yanked", Message: "this module was pulled from the marketplace or the version was yanked; it can still be installed from a local file", Status: http.StatusGone}
	case errors.Is(err, ErrNotConfigured):
		return &Error{Code: "unavailable", Message: "the marketplace address is not configured (modules.marketplace_address)", Status: http.StatusServiceUnavailable}
	case errors.Is(err, ErrUnavailable):
		return &Error{Code: "unavailable", Message: "the marketplace could not be reached", Status: http.StatusServiceUnavailable}
	case errors.Is(err, ErrDigestMismatch), errors.Is(err, ErrSignatureInvalid), errors.Is(err, ErrUntrustedKey):
		return &Error{Code: verificationCode(err), Message: "the package failed verification (" + verificationCode(err) + "); nothing was installed", Status: http.StatusUnprocessableEntity}
	case errors.Is(err, ErrManifest):
		return domain.Invalid("package", "the package has no readable "+ManifestName)
	}
	return err
}

// Error is a marketplace failure with its own status: a pull (410), an unreachable
// marketplace (503) or a failed verification (422). Code is the contract's word.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// HTTPStatus is the status the API serves.
func (e *Error) HTTPStatus() int { return e.Status }

func verificationCode(err error) string {
	for _, e := range []error{ErrDigestMismatch, ErrSignatureInvalid, ErrUntrustedKey} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return err.Error()
}

// --- Browsing (PRD 8.7 Modules and marketplace tab) ---

// Listings searches the marketplace. Pulled modules are not in the results.
func (s *Service) Listings(ctx context.Context, p domain.Principal, query string) ([]Listing, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	ls, err := s.d.Client.Listings(ctx, query)
	if err != nil {
		return nil, apiError(err)
	}
	out := ls[:0]
	for _, l := range ls {
		// The marketplace already leaves pulled listings out of search; a marketplace
		// that does not is not trusted to.
		if !l.Pulled {
			l.Notice = Notice
			out = append(out, l)
		}
	}
	return out, nil
}

// Listing reads one listing with its flag warning, pulled or not.
func (s *Service) Listing(ctx context.Context, p domain.Principal, id string) (Listing, error) {
	if err := admin(p); err != nil {
		return Listing{}, err
	}
	l, err := s.d.Client.Listing(ctx, id)
	if err != nil {
		return Listing{}, apiError(err)
	}
	l.Notice = Notice
	return l, nil
}

// --- Verified packages (PRD 9.3, 10.3, 10.4) ---

// Verified is a package whose digest, signer and signature checked out. The module
// runtime installs from it and shows Warning, when set, before the admin confirms.
type Verified struct {
	ModuleID string
	Version  string
	Manifest Manifest
	Package  []byte
	Digest   string
	Signer   TrustedKey
	// Source is "marketplace" or "local".
	Source string
	// Warning is the flag or pull text shown again at install; empty when none applies.
	Warning string
}

// trusted is every signer the firm accepts: the keys the marketplace publishes (fresh
// when it answers, the last copy when it does not) plus the trusted local keys.
func (s *Service) trusted(ctx context.Context) ([]TrustedKey, error) {
	_ = s.refreshMarketplaceKeys(ctx)
	var out []TrustedKey
	rows, err := s.d.DB.QueryContext(ctx, `SELECT key_id, author, public_key_pem, 'marketplace' FROM marketplace_signing_keys
		UNION ALL SELECT key_id, name, public_key_pem, 'local' FROM trusted_signing_keys ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k TrustedKey
		var pemText string
		if err := rows.Scan(&k.KeyID, &k.Author, &pemText, &k.Source); err != nil {
			return nil, err
		}
		pub, err := ParsePublicKeyPEM(pemText)
		if err != nil {
			continue
		}
		k.Public = pub
		out = append(out, k)
	}
	return out, rows.Err()
}

// refreshMarketplaceKeys replaces the cached marketplace keys with what it publishes now.
// A key whose id does not match its bytes is dropped. Unreachable keeps the cache.
func (s *Service) refreshMarketplaceKeys(ctx context.Context) error {
	keys, err := s.d.Client.SigningKeys(ctx)
	if err != nil {
		return err
	}
	now := micro(s.now())
	return audit.WithTx(ctx, s.d.DB, func(ctx context.Context) error {
		q := audit.Conn(ctx, s.d.DB)
		if _, err := q.ExecContext(ctx, `DELETE FROM marketplace_signing_keys`); err != nil {
			return err
		}
		for _, k := range keys {
			pub, err := ParsePublicKeyPEM(k.PublicKeyPEM)
			if err != nil || KeyID(pub) != k.KeyID {
				continue
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO marketplace_signing_keys (key_id, author, public_key_pem, fetched_at) VALUES ($1,$2,$3,$4) ON CONFLICT (key_id) DO NOTHING`,
				k.KeyID, k.Author, k.PublicKeyPEM, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// FetchForInstall gets one version from the marketplace and verifies it. A pulled module
// or a yanked version is refused (PRD 10.4); a flagged one verifies and carries the
// warning so it shows again at install (PRD 10.3). Nothing in the package runs here.
func (s *Service) FetchForInstall(ctx context.Context, p domain.Principal, listingID, version string) (Verified, error) {
	if err := admin(p); err != nil {
		return Verified{}, err
	}
	l, err := s.d.Client.Listing(ctx, listingID)
	if err != nil {
		return Verified{}, apiError(err)
	}
	if l.Pulled {
		return Verified{}, apiError(ErrYanked)
	}
	if version == "" {
		version = l.Version
	}
	sp, err := s.d.Client.Package(ctx, listingID, version)
	if err != nil {
		return Verified{}, apiError(err)
	}
	pkg, err := decodePackage(sp.PackageBase64)
	if err != nil {
		return Verified{}, apiError(err)
	}
	v, err := s.verify(ctx, pkg, sp.Digest, sp.Signature, sp.KeyID)
	if err != nil {
		return Verified{}, apiError(err)
	}
	if v.ModuleID != listingID || v.Version != version {
		return Verified{}, apiError(fmt.Errorf("%w: the signed manifest is not %s %s", ErrDigestMismatch, listingID, version))
	}
	v.Source = "marketplace"
	v.Warning = installWarning(l, version)
	return v, nil
}

func installWarning(l Listing, version string) string {
	if l.Pulled {
		return Warning(KindPull, "")
	}
	if l.Flagged {
		return l.FlagText()
	}
	if ok, note := l.VersionFlag(version); ok {
		return Warning(KindFlag, note)
	}
	return ""
}

func decodePackage(b64 string) ([]byte, error) {
	if int64(len(b64)) > fixed.MaxPackageBytes/3*4+fixed.MaxResponseBytes {
		return nil, ErrDigestMismatch
	}
	pkg, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || int64(len(pkg)) > fixed.MaxPackageBytes || len(pkg) == 0 {
		return nil, ErrDigestMismatch
	}
	return pkg, nil
}

func (s *Service) verify(ctx context.Context, pkg []byte, digest, signature, keyID string) (Verified, error) {
	keys, err := s.trusted(ctx)
	if err != nil {
		return Verified{}, err
	}
	signer, err := Verify(pkg, digest, signature, keyID, keys)
	if err != nil {
		return Verified{}, err
	}
	m, err := ReadManifest(pkg)
	if err != nil {
		return Verified{}, err
	}
	return Verified{ModuleID: m.ID, Version: m.Version, Manifest: m, Package: pkg, Digest: Digest(pkg), Signer: signer}, nil
}

// VerifyLocal checks a package the admin uploaded from a file (PRD 9.3, 10.4). The
// marketplace's pull and flag state never blocks it: the marketplace doesn't control
// code a firm already has. The signer must still be a marketplace-published key or a
// trusted local key. When the marketplace knows the module, its warning is attached.
func (s *Service) VerifyLocal(ctx context.Context, p domain.Principal, pkg []byte, signature, keyID string) (Verified, error) {
	if err := admin(p); err != nil {
		return Verified{}, err
	}
	if len(pkg) == 0 || int64(len(pkg)) > fixed.MaxPackageBytes {
		return Verified{}, domain.Invalid("package", "the package is empty or too large")
	}
	v, err := s.verify(ctx, pkg, "", signature, keyID)
	if err != nil {
		return Verified{}, apiError(err)
	}
	v.Source = "local"
	if l, err := s.d.Client.Listing(ctx, v.ModuleID); err == nil {
		v.Warning = installWarning(l, v.Version)
	}
	return v, nil
}

// VerifyLocalFile reads a signed package file (the SignedPackage JSON the marketplace
// serves, saved to disk) and verifies it as VerifyLocal does, digest included.
func (s *Service) VerifyLocalFile(ctx context.Context, p domain.Principal, file []byte) (Verified, error) {
	if err := admin(p); err != nil {
		return Verified{}, err
	}
	var sp SignedPackage
	if err := json.Unmarshal(file, &sp); err != nil {
		return Verified{}, domain.Invalid("package", "a signed package file is JSON with package_base64, digest, signature and key_id")
	}
	pkg, err := decodePackage(sp.PackageBase64)
	if err != nil {
		return Verified{}, apiError(err)
	}
	if sp.Digest != "" && sp.Digest != Digest(pkg) {
		return Verified{}, apiError(ErrDigestMismatch)
	}
	return s.VerifyLocal(ctx, p, pkg, sp.Signature, sp.KeyID)
}

// --- Trusted local signing keys (PRD 8.7) ---

// LocalKey is one trusted local signing key.
type LocalKey struct {
	domain.Meta
	Name         string
	KeyID        string
	PublicKeyPEM string
}

// KeyInput is a create or patch. A nil field is left alone on patch.
type KeyInput struct {
	Name         *string
	PublicKeyPEM *string
}

const keyEntity = "trusted_signing_keys"

func cleanKey(in KeyInput, create bool) (name, pemText, keyID string, err error) {
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > fixed.MaxNoteRunes {
			return "", "", "", domain.Invalid("name", "a key needs a name")
		}
	} else if create {
		return "", "", "", domain.Invalid("name", "a key needs a name")
	}
	if in.PublicKeyPEM != nil {
		pub, perr := ParsePublicKeyPEM(*in.PublicKeyPEM)
		if perr != nil {
			return "", "", "", domain.Invalid("public_key_pem", "the key must be one Ed25519 PUBLIC KEY block")
		}
		pemText, keyID = PublicKeyPEM(pub), KeyID(pub)
	} else if create {
		return "", "", "", domain.Invalid("public_key_pem", "the key must be one Ed25519 PUBLIC KEY block")
	}
	return name, pemText, keyID, nil
}

func (s *Service) record(ctx context.Context, p domain.Principal, entity string, id domain.ID, field, old, new string) error {
	_, err := s.d.Audit.Record(ctx, p, audit.Change{Entity: entity, EntityID: id, Field: field, Old: old, New: new})
	return err
}

// CreateKey adds a trusted local key.
func (s *Service) CreateKey(ctx context.Context, p domain.Principal, in KeyInput) (LocalKey, error) {
	if err := admin(p); err != nil {
		return LocalKey{}, err
	}
	name, pemText, keyID, err := cleanKey(in, true)
	if err != nil {
		return LocalKey{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return LocalKey{}, err
	}
	now := s.now()
	k := LocalKey{Meta: domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor(p)}, Name: name, KeyID: keyID, PublicKeyPEM: pemText}
	err = s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		q := audit.Conn(ctx, s.d.DB)
		res, err := q.ExecContext(ctx, `INSERT INTO trusted_signing_keys (id, created_at, updated_at, created_by, name, key_id, public_key_pem) VALUES ($1,$2,$2,$3,$4,$5,$6) ON CONFLICT (key_id) DO NOTHING`,
			string(id), micro(now), string(k.CreatedBy), name, keyID, pemText)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == int64(0) {
			return domain.Conflict("that key is already trusted")
		}
		if err := s.record(ctx, p, keyEntity, id, "name", "", name); err != nil {
			return err
		}
		return s.record(ctx, p, keyEntity, id, "key_id", "", keyID)
	})
	return k, err
}

func actor(p domain.Principal) domain.ID {
	if p.UserID.IsZero() {
		return domain.SystemUserID
	}
	return p.UserID
}

// Keys lists the trusted local keys, oldest first.
func (s *Service) Keys(ctx context.Context, p domain.Principal) ([]LocalKey, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	return s.keys(ctx, "")
}

func (s *Service) keys(ctx context.Context, id domain.ID) ([]LocalKey, error) {
	rows, err := audit.Conn(ctx, s.d.DB).QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, name, key_id, public_key_pem FROM trusted_signing_keys WHERE ($1 = '' OR id = $1) ORDER BY id`, string(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalKey
	for rows.Next() {
		var k LocalKey
		var c, u int64
		if err := rows.Scan(&k.ID, &c, &u, &k.CreatedBy, &k.Name, &k.KeyID, &k.PublicKeyPEM); err != nil {
			return nil, err
		}
		k.CreatedAt, k.UpdatedAt = fromMicro(c), fromMicro(u)
		out = append(out, k)
	}
	return out, rows.Err()
}

// Key reads one trusted local key.
func (s *Service) Key(ctx context.Context, p domain.Principal, id domain.ID) (LocalKey, error) {
	if err := admin(p); err != nil {
		return LocalKey{}, err
	}
	ks, err := s.keys(ctx, id)
	if err != nil {
		return LocalKey{}, err
	}
	if len(ks) == 0 {
		return LocalKey{}, domain.NotFound("trusted signing key")
	}
	return ks[0], nil
}

// UpdateKey renames a key or replaces its public key.
func (s *Service) UpdateKey(ctx context.Context, p domain.Principal, id domain.ID, in KeyInput) (LocalKey, error) {
	if err := admin(p); err != nil {
		return LocalKey{}, err
	}
	name, pemText, keyID, err := cleanKey(in, false)
	if err != nil {
		return LocalKey{}, err
	}
	var out LocalKey
	err = s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		ks, err := s.keys(ctx, id)
		if err != nil {
			return err
		}
		if len(ks) == 0 {
			return domain.NotFound("trusted signing key")
		}
		old := ks[0]
		out = old
		if in.Name != nil {
			out.Name = name
		}
		if in.PublicKeyPEM != nil {
			out.PublicKeyPEM, out.KeyID = pemText, keyID
		}
		out.UpdatedAt = s.now()
		_, err = audit.Conn(ctx, s.d.DB).ExecContext(ctx, `UPDATE trusted_signing_keys SET name = $1, key_id = $2, public_key_pem = $3, updated_at = $4 WHERE id = $5`,
			out.Name, out.KeyID, out.PublicKeyPEM, micro(out.UpdatedAt), string(id))
		if err != nil {
			if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
				return domain.Conflict("that key is already trusted")
			}
			return err
		}
		if out.Name != old.Name {
			if err := s.record(ctx, p, keyEntity, id, "name", old.Name, out.Name); err != nil {
				return err
			}
		}
		if out.KeyID != old.KeyID {
			return s.record(ctx, p, keyEntity, id, "key_id", old.KeyID, out.KeyID)
		}
		return nil
	})
	return out, err
}

// DeleteKey stops trusting a local key. Modules it signed keep running; only later
// installs and updates signed by it are refused.
func (s *Service) DeleteKey(ctx context.Context, p domain.Principal, id domain.ID) error {
	if err := admin(p); err != nil {
		return err
	}
	return s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		ks, err := s.keys(ctx, id)
		if err != nil {
			return err
		}
		if len(ks) == 0 {
			return domain.NotFound("trusted signing key")
		}
		if _, err := audit.Conn(ctx, s.d.DB).ExecContext(ctx, `DELETE FROM trusted_signing_keys WHERE id = $1`, string(id)); err != nil {
			return err
		}
		return s.record(ctx, p, keyEntity, id, "key_id", ks[0].KeyID, "")
	})
}

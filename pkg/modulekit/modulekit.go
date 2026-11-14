// Package modulekit is the public module kit: the core's own manifest, migration,
// packaging and signing code for module repositories (github.com/CharitonMedia/togavsu-<id>),
// which cannot import the core's internal packages. Every function here calls the code
// the core runs at install; nothing is reimplemented, so a package that passes Check and
// Verify here passes the same checks on a running core.
//
// A module package is a gzip tar holding togavsu-module.yaml at its root and numbered
// SQL migrations under migrations/. Its author signs the exact package bytes with an
// Ed25519 key and ships the signed package file (SignFile), which the firm's admin
// installs after adding the author's public key (PublicKeyPEM) as a trusted key.
//
// The command cmd/togavsu-module wraps this package; pkg/modulekit/coretest runs a
// module's acceptance suite against the real composed core.
package modulekit

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// ManifestFile is the manifest's path inside a package.
const ManifestFile = modules.ManifestFile

// Manifest is togavsu-module.yaml as the module runtime reads it.
type Manifest = modules.Manifest

// Migration is one numbered SQL migration (migrations/NNNN_name.sql), with the checksum
// the core records when it runs.
type Migration = db.Migration

// Package is an unpacked module package: its files by path, its manifest and the
// marketplace digest of its bytes.
type Package = modules.Package

// ListingManifest is the part of the manifest a marketplace listing shows (id, name,
// version, permissions), as ReadManifest returns it.
type ListingManifest = marketplace.Manifest

// SignedPackage is the signed package file: the package bytes (base64), their digest,
// an Ed25519 signature over the exact bytes and the signer's key id.
type SignedPackage = marketplace.SignedPackage

// TrustedKey is a signer the verifier accepts.
type TrustedKey = marketplace.TrustedKey

// Verified is a package whose signature, signer and manifest passed.
type Verified = marketplace.Verified

// Verification results Verify returns, as the core and the marketplace contract name
// them. VerifyFile reports them as the install API does (Code: tampered_module or
// unsigned_module).
var (
	ErrDigestMismatch   = marketplace.ErrDigestMismatch
	ErrSignatureInvalid = marketplace.ErrSignatureInvalid
	ErrUntrustedKey     = marketplace.ErrUntrustedKey
	ErrBadKey           = marketplace.ErrBadKey
	// ErrBadSeed means a signing key file does not hold a base64 Ed25519 seed.
	ErrBadSeed = errors.New("modulekit: the key file must hold a base64 ed25519 seed")
)

// Refusal is one problem a check found: its kind, code, file, field path or statement,
// and the core's own error. Refusals is every refusal of one check, as an error.
type (
	Refusal  = modules.Refusal
	Refusals = modules.Refusals
)

// Refusal kinds.
const (
	KindPackage   = modules.KindPackage
	KindManifest  = modules.KindManifest
	KindScope     = modules.KindScope
	KindMigration = modules.KindMigration
	// KindPending is a problem with the pending list or with holding a part back.
	KindPending = "pending"
)

// Stable refusal codes (Refusal.Code). Match on these, never on message text.
const (
	RefusePackage = modules.RefusePackage
	// RefuseUnknownFile: a package file the core doesn't read; a package holds only the
	// manifest and migrations/NNNN_name.sql.
	RefuseUnknownFile   = modules.RefuseUnknownFile
	RefuseYAML          = modules.RefuseYAML
	RefuseUnknownField  = modules.RefuseUnknownField
	RefuseManifest      = modules.RefuseManifest
	RefuseUnknownScope  = modules.RefuseUnknownScope
	RefuseCoreReference = modules.RefuseCoreReference
	// RefuseCoreRelationMissing: a migration links to a core relation modules may link
	// to, which this core's schema doesn't have yet.
	RefuseCoreRelationMissing = modules.RefuseCoreRelationMissing
	RefuseMigrationFile       = modules.RefuseMigrationFile
	RefuseStatement           = modules.RefuseStatement
	RefuseCrossCheck          = modules.RefuseCrossCheck
	// RefusePendingInvalid: PendingFile is not in the pending format.
	RefusePendingInvalid = "pending_invalid"
	// RefusePendingEdit: a pending part can't be removed cleanly.
	RefusePendingEdit = "pending_edit"
	// RefusePendingOrder: a held-back migration comes before one that installs.
	RefusePendingOrder = "pending_order"
	// RefusePendingUnused: a pending entry holds nothing back (togavsu-module -strict).
	RefusePendingUnused = "pending_unused"
	// RefusePendingUnbuilt: Build was handed the full package with its pending list.
	RefusePendingUnbuilt = "pending_unbuilt"
	// RefuseUpgrade: the package is not a valid update of the previous release.
	RefuseUpgrade = "upgrade_refused"
	// RefuseUpgradeBaseline: the previous release can't be told from its source
	// directory, because its build held parts back; compare against what it shipped.
	RefuseUpgradeBaseline = "upgrade_baseline"
	// RefuseUnreadable: a file or directory the command was given can't be read.
	RefuseUnreadable = "unreadable"
)

// Accepted is what a core accepts from a module package: the manifest fields it reads
// and their digest, its scope list, and the core records and relations a module may use.
type Accepted = modules.Accepted

// Accepts reports what the core this kit belongs to accepts, from the tables its install
// checks use.
func Accepts() Accepted { return modules.Accepts() }

// Scopes is the core's scope list, the one it checks a module's approved scopes against.
// A published view scope (views:<module>.<view>:read) is accepted by its shape.
func Scopes() []string { return identity.Scopes() }

// KnownScope reports whether the core accepts scope as a permission.
func KnownScope(scope string) bool { return identity.KnownScope(scope) }

// Install refusal codes the checks return in a *domain.APIError's Code (see Code).
const (
	CodeUnsigned  = modules.CodeUnsigned
	CodeTampered  = modules.CodeTampered
	CodeMigration = modules.CodeMigration
)

// Code returns the API error code the core would answer a check's error with
// ("migration_refused", "validation", "tampered_module", "unsigned_module" and
// the like), or "" when err carries none.
func Code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	var me *marketplace.Error
	if errors.As(err, &me) {
		return me.Code
	}
	return ""
}

// --- Manifest and migrations ---

// ParseManifest reads and validates togavsu-module.yaml with every install-time manifest
// check except the migration checks.
func ParseManifest(src []byte) (Manifest, error) { return modules.ParseManifest(src) }

// CheckMigrations runs the install check over a package's migrations and the manifest's
// data declarations (own schema only, no triggers, no copies of core tables, declared
// extensions and views present). It returns migration_refused naming the file and the
// statement.
func CheckMigrations(m Manifest, ms []Migration) error { return modules.CheckMigrations(m, ms) }

// CheckChecksums is the update rule: every migration in applied (version to checksum,
// what a running core has recorded) must still be in ms, unchanged. A failure is
// migration_refused, as the core reports it.
func CheckChecksums(applied map[int]string, ms []Migration) error {
	if err := modules.CheckChecksums(applied, ms); err != nil {
		return &domain.APIError{Code: modules.CodeMigration, Message: err.Error()}
	}
	return nil
}

// Applied is the history a core records after running ms: version to checksum.
func Applied(ms []Migration) map[int]string {
	out := make(map[int]string, len(ms))
	for _, m := range ms {
		out[m.Version] = m.Checksum
	}
	return out
}

// ReadPackage unpacks gzip tar package bytes with the core's bounds and file-name rules
// and parses the manifest. It does not check migrations or signatures.
func ReadPackage(raw []byte) (*Package, error) { return modules.ReadPackage(raw) }

// CheckPackage runs the core's install checks on package bytes: unpacking, the manifest,
// the migration files and CheckMigrations. It returns the package and its migrations.
func CheckPackage(raw []byte) (*Package, []Migration, error) { return modules.CheckPackage(raw) }

// Check archives files (as Archive does) and runs the install checks on the result, so
// it checks exactly the bytes a later Archive and SignFile would ship. It also refuses a
// scope that is not in the core's scope list (Scopes), which the core otherwise refuses
// only when the admin approves the install. A refusal is a Refusals error listing every
// problem CheckAll finds; Code(err) is the install API's code for the first.
func Check(files map[string][]byte) (*Package, []Migration, error) {
	raw, err := Archive(files)
	if err != nil {
		return nil, nil, Refusals{{Kind: KindPackage, Code: RefusePackage, Message: err.Error(), Err: err}}
	}
	if _, _, refs := modules.CheckAll(raw); len(refs) > 0 {
		for i, r := range refs {
			if r.Code == RefuseUnknownFile && r.File == PendingFile {
				refs[i].Message += "; " + PendingFile + " lists parts held back for core changes and never ships: check and build the package with Installable (togavsu-module -installable)"
			}
		}
		return nil, nil, refs
	}
	// The install path decides; CheckAll reports nothing it would pass.
	pkg, ms, err := CheckPackage(raw)
	if err != nil {
		return nil, nil, Refusals{{Kind: KindPackage, Code: RefusePackage, Message: err.Error(), APICode: Code(err), Err: err}}
	}
	return pkg, ms, nil
}

// CheckAll is Check's list of refusals: every file the core doesn't read, every unknown
// manifest field at any depth, every refused manifest value (the widget checks report
// their first; a value of the wrong type stops the manifest checks), every unknown
// scope, and every migration statement and cross-check the core refuses. It is empty
// when Check passes.
func CheckAll(files map[string][]byte) Refusals {
	_, _, err := Check(files)
	var refs Refusals
	if errors.As(err, &refs) {
		return refs
	}
	return nil
}

// CheckUpgrade checks next as an update of prev: both pass CheckPackage, they are the
// same module, and next keeps every migration of prev unchanged.
func CheckUpgrade(prev, next []byte) error {
	old, oldMS, err := CheckPackage(prev)
	if err != nil {
		return err
	}
	pkg, ms, err := CheckPackage(next)
	if err != nil {
		return err
	}
	if old.Manifest.ID != pkg.Manifest.ID {
		return domain.Invalid("package", "this package is module "+pkg.Manifest.ID+", not "+old.Manifest.ID)
	}
	return CheckChecksums(Applied(oldMS), ms)
}

// CheckUpdate checks next as an update of shipped, the package bytes a firm may have
// installed (the previous release's signed package, see ReadSignedFile). Only shipped's
// module id and migrations are read: it is the record of what ran, so it is not judged
// again by this core, and parts its build held back (Installable) are not in it. next
// must pass CheckPackage, be the same module, and keep every migration of shipped
// unchanged. A refusal is a Refusals error with code upgrade_refused.
func CheckUpdate(shipped, next []byte) error {
	refused := func(err error) error {
		return Refusals{{Kind: KindMigration, Code: RefuseUpgrade, Message: err.Error(), APICode: Code(err), Err: err}}
	}
	old, err := ReadManifest(shipped)
	if err != nil {
		return refused(err)
	}
	files, err := modules.Unpack(shipped)
	if err != nil {
		return refused(err)
	}
	oldMS, err := (&Package{Files: files}).Migrations()
	if err != nil {
		return refused(err)
	}
	pkg, ms, err := CheckPackage(next)
	if err != nil {
		return refused(err)
	}
	if old.ID != pkg.Manifest.ID {
		return refused(domain.Invalid("package", "this package is module "+pkg.Manifest.ID+", not "+old.ID))
	}
	if err := CheckChecksums(Applied(oldMS), ms); err != nil {
		return refused(err)
	}
	return nil
}

// ReadSignedFile reads a signed package file's form and returns it with the package
// bytes it carries. It does not verify the signature; Verify does.
func ReadSignedFile(file []byte) (SignedPackage, []byte, error) {
	return marketplace.ReadSignedFile(file)
}

// --- Package build ---

// Files reads a package directory into package files by slash path. It reads only the
// package's own files: ManifestFile, PendingFile and the files under migrations/. Any
// other file there (a files.go that embeds the directory, a README) is left out, never
// shipped; the core refuses a package holding one (RefuseUnknownFile), so Check and
// Build do too. As go:embed does for a directory, names starting with "." or "_" are
// left out, so a module that embeds its package directory and one built from disk ship
// the same bytes.
func Files(fsys fs.FS) (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != "." && p != "migrations" && !strings.HasPrefix(p, "migrations/") {
				return fs.SkipDir
			}
			return nil
		}
		if !packageFile(p) {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out[p] = b
		return nil
	})
	return out, err
}

// packageFile reports whether name is one of a package directory's own files.
func packageFile(name string) bool { return name == PendingFile || modules.PackageFile(name) }

// ReadDir is Files over a directory on disk.
func ReadDir(dir string) (map[string][]byte, error) { return Files(os.DirFS(dir)) }

// Archive writes package files as the gzip tar the core installs, in path order with
// fixed headers, so the same files always give the same bytes.
func Archive(files map[string][]byte) ([]byte, error) { return modules.Archive(files) }

// BuildPackage writes the marketplace's plain (uncompressed) tar of manifest plus extra
// files, which ReadManifest reads. The core installs only gzip packages: ship Archive's
// output, and use BuildPackage for marketplace listing tests.
func BuildPackage(manifest []byte, extra map[string][]byte) ([]byte, error) {
	return marketplace.BuildPackage(manifest, extra)
}

// ReadManifest reads the listing manifest at the root of package bytes (tar, gzip or not).
func ReadManifest(pkg []byte) (ListingManifest, error) { return marketplace.ReadManifest(pkg) }

// --- Keys and signatures ---

// KeyID is the fingerprint the core and the marketplace name pub by.
func KeyID(pub ed25519.PublicKey) string { return marketplace.KeyID(pub) }

// PublicKeyPEM encodes pub as the PKIX "PUBLIC KEY" block the admin pastes as a trusted
// signing key.
func PublicKeyPEM(pub ed25519.PublicKey) string { return marketplace.PublicKeyPEM(pub) }

// ParsePublicKeyPEM reads one Ed25519 PUBLIC KEY block, as the core does.
func ParsePublicKeyPEM(text string) (ed25519.PublicKey, error) {
	return marketplace.ParsePublicKeyPEM(text)
}

// ParseSeed reads a signing key file's text: a base64 Ed25519 seed.
func ParseSeed(text string) (ed25519.PrivateKey, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, ErrBadSeed
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Digest is the package digest, sha256:<hex>.
func Digest(pkg []byte) string { return marketplace.Digest(pkg) }

// Sign returns the base64 Ed25519 signature of pkg.
func Sign(priv ed25519.PrivateKey, pkg []byte) string { return marketplace.Sign(priv, pkg) }

// SignFile wraps package bytes in a signed package file, the file the admin installs.
func SignFile(priv ed25519.PrivateKey, pkg []byte) ([]byte, error) {
	m, err := marketplace.ReadManifest(pkg)
	if err != nil {
		return nil, err
	}
	return json.Marshal(SignedPackage{ListingID: m.ID, Version: m.Version, Digest: Digest(pkg),
		Signature: Sign(priv, pkg), KeyID: KeyID(priv.Public().(ed25519.PublicKey)),
		PackageBase64: base64.StdEncoding.EncodeToString(pkg)})
}

// Build checks files (Check), archives them and signs the result. It refuses to sign a
// package Check refuses, and one that still holds its pending list (PendingFile): build
// that with BuildInstallable. SignFile signs package bytes without checking them.
func Build(priv ed25519.PrivateKey, files map[string][]byte) ([]byte, error) {
	if _, ok := files[PendingFile]; ok {
		return nil, Refusals{{Kind: KindPending, Code: RefusePendingUnbuilt, File: PendingFile,
			Message: "the package lists pending parts; build it with BuildInstallable (togavsu-module package -installable)"}}
	}
	if _, _, err := Check(files); err != nil {
		return nil, err
	}
	pkg, err := Archive(files)
	if err != nil {
		return nil, err
	}
	return SignFile(priv, pkg)
}

// BuildInstallable builds and signs Installable's files: the package without the
// pending parts the core refuses. It returns Installable's result with the signed file.
func BuildInstallable(priv ed25519.PrivateKey, files map[string][]byte) ([]byte, InstallablePackage, error) {
	ip, err := Installable(files)
	if err != nil {
		return nil, InstallablePackage{}, err
	}
	signed, err := Build(priv, ip.Files)
	if err != nil {
		return nil, InstallablePackage{}, err
	}
	return signed, ip, nil
}

// Trust is pub as a trusted local key, for Verify and VerifyFile.
func Trust(pub ed25519.PublicKey) TrustedKey {
	return TrustedKey{KeyID: KeyID(pub), Public: pub, Source: "local"}
}

// Verify checks package bytes as the core does: the digest (when named), the signer is
// in trusted, and the signature over the exact bytes. It returns the signing key.
func Verify(pkg []byte, digest, signature, keyID string, trusted []TrustedKey) (TrustedKey, error) {
	return marketplace.Verify(pkg, digest, signature, keyID, trusted)
}

// VerifyFile is the core's whole check of an uploaded signed package file against the
// keys in trusted: the file's form and digest, the signature and signer, the signed
// manifest, then CheckPackage's install checks and that the package's manifest is the
// signed one. It returns what was verified and the package's migrations. A refusal
// carries the code the core's install API answers with (see Code).
func VerifyFile(file []byte, trusted []TrustedKey) (Verified, []Migration, error) {
	sp, pkg, err := marketplace.ReadSignedFile(file)
	if err != nil {
		return Verified{}, nil, err
	}
	v, err := marketplace.VerifyPackage(pkg, sp.Digest, sp.Signature, sp.KeyID, trusted)
	if err != nil {
		return Verified{}, nil, marketplace.APIError(err)
	}
	v.Source = "local"
	_, ms, err := modules.CheckSigned(v)
	if err != nil {
		return Verified{}, nil, err
	}
	return v, ms, nil
}

// CheckSource is the core's settings source check (AC-030) over a module's Go source
// tree at root: it fails on an environment read or an unproven numeric configuration
// literal outside a setting, as the core's own check does. Test files, vendor and
// testdata are skipped.
func CheckSource(root string) error { return settings.CheckModule(root) }

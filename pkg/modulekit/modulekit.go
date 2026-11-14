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

// Check archives files (as Archive does) and runs CheckPackage on the result, so it
// checks exactly the bytes a later Archive and SignFile would ship.
func Check(files map[string][]byte) (*Package, []Migration, error) {
	raw, err := Archive(files)
	if err != nil {
		return nil, nil, err
	}
	return CheckPackage(raw)
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

// --- Package build ---

// Files reads a package directory into package files by slash path. As go:embed does
// for a directory, names starting with "." or "_" are left out, so a module that embeds
// its package directory and one built from disk ship the same bytes.
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

// Build archives files and signs the result: Archive then SignFile.
func Build(priv ed25519.PrivateKey, files map[string][]byte) ([]byte, error) {
	pkg, err := Archive(files)
	if err != nil {
		return nil, err
	}
	return SignFile(priv, pkg)
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

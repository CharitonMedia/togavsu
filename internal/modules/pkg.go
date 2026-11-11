package modules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing/fstest"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// SignatureFile holds the package signature. Every other file is covered by it.
const SignatureFile = "togavsu-module.sig"

// signingContext prefixes the signed digest list, so a signature over something else
// can't be replayed as a module signature.
const signingContext = "togavsu-module-signature-v1\n"

//go:embed limits.json
var limitsJSON []byte

// limits are fixed protocol bounds of the package format, not firm settings: they keep
// a hostile upload from exhausting memory before its signature is checked.
type limitFacts struct {
	MaxPackageBytes  int64 `json:"max_package_bytes"`
	MaxFiles         int   `json:"max_files"`
	ContextTTLSecond int64 `json:"context_token_ttl_seconds"`
	ProxyBodyBytes   int64 `json:"proxy_error_body_bytes"`
	WebhookBodyBytes int64 `json:"webhook_response_bytes"`
	PumpBatch        int   `json:"event_pump_batch"`
	NonceBytes       int   `json:"nonce_bytes"`
	SecretBytes      int   `json:"secret_bytes"`
	KeyIDBytes       int   `json:"key_id_bytes"`
	RoleTagBytes     int   `json:"role_tag_bytes"`
	MaxPort          int   `json:"max_port"`
	FileMode         int64 `json:"file_mode"`
	AESKeyBytes      int   `json:"aes_key_bytes"`
}

var limits = sync.OnceValue(func() limitFacts {
	var l limitFacts
	if err := json.Unmarshal(limitsJSON, &l); err != nil {
		panic("modules: limits.json: " + err.Error())
	}
	return l
})

// Package is a verified-or-not module package: the manifest, its migrations and the
// rest of its files, by path.
type Package struct {
	Files     map[string][]byte
	Manifest  Manifest
	Digest    string
	Signature Signature
}

// Signature is togavsu-module.sig.
type Signature struct {
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Install refusal codes from the API contract.
const (
	CodeUnsigned  = "unsigned_module"
	CodeTampered  = "tampered_module"
	CodeMigration = "migration_refused"
	CodeDeclined  = "permission_declined"
)

func refuse(code, msg string) error {
	return &domain.APIError{Code: code, Message: msg}
}

// ReadPackage unpacks a gzip tar module package. It reads and bounds the files; it does
// not verify the signature (Verify does).
func ReadPackage(raw []byte) (*Package, error) {
	if int64(len(raw)) > limits().MaxPackageBytes {
		return nil, invalid("package", "the package is too large")
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, invalid("package", "a module package is a .tar.gz file")
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalid("package", "the package archive is damaged")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, invalid("package", "packages hold only regular files: "+h.Name)
		}
		name := strings.TrimPrefix(path.Clean(h.Name), "./")
		if !safeName.MatchString(name) || strings.Contains(name, "..") {
			return nil, invalid("package", "unsafe file name in package: "+h.Name)
		}
		if _, dup := files[name]; dup {
			return nil, invalid("package", "duplicate file in package: "+name)
		}
		if len(files) >= limits().MaxFiles {
			return nil, invalid("package", "the package holds too many files")
		}
		body, err := io.ReadAll(io.LimitReader(tr, limits().MaxPackageBytes-total+1))
		if err != nil {
			return nil, invalid("package", "the package archive is damaged")
		}
		total += int64(len(body))
		if total > limits().MaxPackageBytes {
			return nil, invalid("package", "the package is too large")
		}
		files[name] = body
	}
	p := &Package{Files: files, Digest: digestFiles(files)}
	if sig, ok := files[SignatureFile]; ok {
		if err := json.Unmarshal(sig, &p.Signature); err != nil {
			return nil, refuse(CodeUnsigned, "the module signature file is unreadable")
		}
	}
	return p, nil
}

// digestFiles is the signed message: one "sha256  path" line per file except the
// signature, sorted by path.
func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		if n != SignatureFile {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(signingContext)
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		b.WriteString(hex.EncodeToString(sum[:]))
		b.WriteString("  ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	return b.String()
}

// PackageDigest is the hex SHA-256 of the signed message, recorded for audit.
func (p *Package) PackageDigest() string {
	sum := sha256.Sum256([]byte(p.Digest))
	return hex.EncodeToString(sum[:])
}

// KeyID is the id of an ed25519 public key: the first 16 bytes of its SHA-256, in hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:limits().KeyIDBytes])
}

// TrustedKey is a signing key the core accepts.
type TrustedKey struct {
	KeyID     string
	PublicKey ed25519.PublicKey
	Source    string // "local" or "marketplace"
	Name      string
}

// Verify checks the package signature against keys and then parses the manifest. An
// unsigned package or one signed by an unknown key is unsigned_module; a signature that
// doesn't match the bytes is tampered_module (AC-013). Nothing in the package is used
// before this passes.
func (p *Package) Verify(keys []TrustedKey) (TrustedKey, error) {
	if p.Signature.KeyID == "" || p.Signature.Signature == "" {
		return TrustedKey{}, refuse(CodeUnsigned, "the module package is not signed")
	}
	var key *TrustedKey
	for i := range keys {
		if keys[i].KeyID == p.Signature.KeyID && len(keys[i].PublicKey) == ed25519.PublicKeySize {
			key = &keys[i]
			break
		}
	}
	if key == nil {
		return TrustedKey{}, refuse(CodeUnsigned, "the module is signed by a key this firm doesn't trust")
	}
	sig, err := base64.StdEncoding.DecodeString(p.Signature.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return TrustedKey{}, refuse(CodeTampered, "the module signature is malformed")
	}
	if !ed25519.Verify(key.PublicKey, []byte(p.Digest), sig) {
		return TrustedKey{}, refuse(CodeTampered, "the module package doesn't match its signature")
	}
	src, ok := p.Files[ManifestFile]
	if !ok {
		return TrustedKey{}, invalid("package", "the package has no "+ManifestFile)
	}
	m, err := ParseManifest(src)
	if err != nil {
		return TrustedKey{}, err
	}
	p.Manifest = m
	return *key, nil
}

// Migrations returns the package's numbered SQL migrations (migrations/NNNN_name.sql).
func (p *Package) Migrations() ([]db.Migration, error) {
	var out []db.Migration
	for name, body := range p.Files {
		if !strings.HasPrefix(name, "migrations/") {
			continue
		}
		base := path.Base(name)
		if path.Dir(name) != "migrations" || !strings.HasSuffix(base, ".sql") {
			return nil, refuse(CodeMigration, "migrations sit directly in migrations/ as .sql files: "+name)
		}
		ms, err := db.LoadFS(fstest.MapFS{base: &fstest.MapFile{Data: body}})
		if err != nil || len(ms) != one {
			return nil, refuse(CodeMigration, "migration file names are NNNN_name.sql: "+name)
		}
		out = append(out, ms[0])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := one; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, refuse(CodeMigration, fmt.Sprintf("two migrations share version %d", out[i].Version))
		}
	}
	return out, nil
}

// Sign signs files in place with priv and returns the archive. The sample module and
// tests use it; firms that patch a module sign their build with their own local key.
func Sign(files map[string][]byte, priv ed25519.PrivateKey) ([]byte, error) {
	clean := map[string][]byte{}
	for k, v := range files {
		if k != SignatureFile {
			clean[k] = v
		}
	}
	sig := ed25519.Sign(priv, []byte(digestFiles(clean)))
	raw, err := json.Marshal(Signature{KeyID: KeyID(priv.Public().(ed25519.PublicKey)), Signature: base64.StdEncoding.EncodeToString(sig)})
	if err != nil {
		return nil, err
	}
	clean[SignatureFile] = raw
	return Archive(clean)
}

// Archive writes files as a gzip tar in path order.
func Archive(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, n := range names {
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: limits().FileMode, Size: int64(len(files[n])), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeKey(pub ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(pub) }

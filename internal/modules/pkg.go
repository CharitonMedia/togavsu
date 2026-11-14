package modules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
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
	"github.com/CharitonMedia/togavsu/internal/marketplace"
)

//go:embed limits.json
var limitsJSON []byte

// limits are fixed protocol bounds of the package format, not firm settings: they keep
// a hostile package from exhausting memory while it is unpacked.
type limitFacts struct {
	MaxPackageBytes     int64 `json:"max_package_bytes"`
	MaxFiles            int   `json:"max_files"`
	ContextTTLSecond    int64 `json:"context_token_ttl_seconds"`
	ProxyBodyBytes      int64 `json:"proxy_error_body_bytes"`
	WebhookBodyBytes    int64 `json:"webhook_response_bytes"`
	PumpBatch           int   `json:"event_pump_batch"`
	PumpLookbackSeconds int64 `json:"event_pump_lookback_seconds"`
	NonceBytes          int   `json:"nonce_bytes"`
	SecretBytes         int   `json:"secret_bytes"`
	KeyIDBytes          int   `json:"key_id_bytes"`
	RoleTagBytes        int   `json:"role_tag_bytes"`
	MaxPort             int   `json:"max_port"`
	FileMode            int64 `json:"file_mode"`
	AESKeyBytes         int   `json:"aes_key_bytes"`
	IdemKeyChars        int   `json:"idempotency_key_max_chars"`
}

var limits = sync.OnceValue(func() limitFacts {
	var l limitFacts
	if err := json.Unmarshal(limitsJSON, &l); err != nil {
		panic("modules: limits.json: " + err.Error())
	}
	return l
})

// Package is a module package: the manifest, its migrations and the rest of its files,
// by path. Digest is the marketplace digest of the package bytes ("sha256:" and hex).
type Package struct {
	Files    map[string][]byte
	Manifest Manifest
	Digest   string
}

// Signer is the key a verified package was signed with.
type Signer struct {
	KeyID  string
	Source string // "local" or "marketplace": where the key is published
	Name   string
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// Install refusal codes from the API contract.
const (
	CodeUnsigned  = "unsigned_module"
	CodeTampered  = "tampered_module"
	CodeMigration = "migration_refused"
	CodeDeclined  = "permission_declined"
	// CodeIdemConflict is a reused Idempotency-Key with a different body.
	CodeIdemConflict = "idempotency_conflict"
)

func refuse(code, msg string) error {
	return &domain.APIError{Code: code, Message: msg}
}

// ReadPackage unpacks a gzip tar module package and parses its manifest. It reads and
// bounds the files; it does not verify the signature, so the runtime passes it only
// bytes the marketplace component has verified (or bytes it stored after that check).
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
	src, ok := files[ManifestFile]
	if !ok {
		return nil, invalid("package", "the package has no "+ManifestFile)
	}
	m, err := ParseManifest(src)
	if err != nil {
		return nil, err
	}
	return &Package{Files: files, Manifest: m, Digest: marketplace.Digest(raw)}, nil
}

// PackageDigest is the digest recorded for audit.
func (p *Package) PackageDigest() string { return p.Digest }

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

// CheckedMigrations returns the package's migrations once they pass the install check
// (CheckMigrations against the manifest).
func (p *Package) CheckedMigrations() ([]db.Migration, error) {
	ms, err := p.Migrations()
	if err != nil {
		return nil, err
	}
	if err := CheckMigrations(p.Manifest, ms); err != nil {
		return nil, err
	}
	return ms, nil
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

package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

// ManifestName is the signed manifest file at the root of a module package (PRD 9.2).
const ManifestName = "togavsu-module.yaml"

// gzipMagic opens a gzip stream (RFC 1952).
const gzipMagic = "\x1f\x8b"

// ErrManifest means the package has no readable manifest or the manifest is incomplete.
var ErrManifest = errors.New("manifest")

// Manifest is the part of togavsu-module.yaml a listing shows: the module id, its name,
// the version and the permissions it requests. Raw is the whole manifest, kept so the
// core hands the module runtime exactly what was signed.
type Manifest struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Permissions []string        `json:"permissions"`
	Raw         json.RawMessage `json:"-"`
}

// moduleID is the module id form the dashboard and the module runtime use.
var moduleID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// version is a dotted release number with an optional pre-release or build suffix.
var versionForm = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}([-+][0-9A-Za-z.-]{1,64})?$`)

// ValidModuleID reports whether id is a module id.
func ValidModuleID(id string) bool { return moduleID.MatchString(id) }

// ValidVersion reports whether v is a version string the marketplace accepts.
func ValidVersion(v string) bool { return versionForm.MatchString(v) }

// ReadManifest finds togavsu-module.yaml at the root of a package (a tar archive, gzip
// compressed or not) and reads it. Call it only after Verify passes. The scan stops at
// max_expanded_bytes of archive and max_package_entries entries, so a small compressed
// package cannot expand without bound before its manifest.
//
// ponytail: ceiling: the manifest must be written in YAML's JSON-compatible flow form
// and is read with encoding/json; block-style YAML is refused as unreadable. Upgrade
// path: read it with the module runtime's manifest parser. trigger: internal/modules
// ships a togavsu-module.yaml parser.
func ReadManifest(pkg []byte) (Manifest, error) {
	var r io.Reader = bytes.NewReader(pkg)
	if bytes.HasPrefix(pkg, []byte(gzipMagic)) {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return Manifest{}, ErrManifest
		}
		defer gz.Close()
		r = gz
	}
	bounded := &io.LimitedReader{R: r, N: fixed.MaxExpandedBytes}
	tr := tar.NewReader(bounded)
	for entries := 0; ; entries++ {
		if entries >= fixed.MaxPackageEntries {
			return Manifest{}, ErrManifest
		}
		h, err := tr.Next()
		if err != nil {
			return Manifest{}, ErrManifest
		}
		if h.Typeflag != tar.TypeReg || path.Clean(strings.TrimPrefix(h.Name, "./")) != ManifestName {
			continue
		}
		if h.Size > fixed.MaxManifestBytes {
			return Manifest{}, ErrManifest
		}
		body, err := io.ReadAll(io.LimitReader(tr, fixed.MaxManifestBytes+1))
		if err != nil || int64(len(body)) > fixed.MaxManifestBytes {
			return Manifest{}, ErrManifest
		}
		return ParseManifest(body)
	}
}

// ParseManifest reads manifest text. Permissions may be plain scope strings or objects
// with a scope field (PRD 9.2: strings are the required shorthand).
func ParseManifest(body []byte) (Manifest, error) {
	var raw struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Version     string            `json:"version"`
		Permissions []json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Manifest{}, ErrManifest
	}
	m := Manifest{ID: strings.TrimSpace(raw.ID), Name: strings.TrimSpace(raw.Name), Version: strings.TrimSpace(raw.Version), Permissions: []string{}, Raw: json.RawMessage(body)}
	if !ValidModuleID(m.ID) || m.Name == "" || !ValidVersion(m.Version) {
		return Manifest{}, ErrManifest
	}
	seen := map[string]bool{}
	for _, p := range raw.Permissions {
		var scope string
		if err := json.Unmarshal(p, &scope); err != nil {
			var obj struct {
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal(p, &obj); err != nil {
				return Manifest{}, ErrManifest
			}
			scope = obj.Scope
		}
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return Manifest{}, ErrManifest
		}
		if !seen[scope] {
			seen[scope] = true
			m.Permissions = append(m.Permissions, scope)
		}
	}
	return m, nil
}

// BuildPackage writes a tar archive holding manifest as togavsu-module.yaml plus extra
// files. It is for author tooling and tests; the module runtime defines what else a
// package carries.
func BuildPackage(manifest []byte, extra map[string][]byte) ([]byte, error) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	write := func(name string, body []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(body)
		return err
	}
	if err := write(ManifestName, manifest); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := write(name, extra[name]); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

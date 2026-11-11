package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The service keeps its records in one JSON document and each package in a file named by
// its digest, under one data directory. Writes go to a temporary file, are synced and
// renamed over the old one, so a crash leaves the previous state or the new one.
//
// ponytail: ceiling: one process and one state file, rewritten whole on each write; fine
// for hundreds of listings. Upgrade path: a Postgres store behind the same methods.
// trigger: the state file passes 10 MB, or a second marketplace replica is deployed.

// Author is a publisher account.
type Author struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at"`
}

// Token is an author's API session, stored as a SHA-256 of the bearer value.
type Token struct {
	Hash      string    `json:"hash"`
	AuthorID  string    `json:"author_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Key is a signing key an author registered. It signs packages only once Verified: the
// author proved they hold the private key by signing the challenge.
type Key struct {
	KeyID        string    `json:"key_id"`
	AuthorID     string    `json:"author_id"`
	PublicKeyPEM string    `json:"public_key_pem"`
	Challenge    string    `json:"challenge"`
	Verified     bool      `json:"verified"`
	CreatedAt    time.Time `json:"created_at"`
	VerifiedAt   time.Time `json:"verified_at,omitempty"`
}

// Version is one immutable upload.
type Version struct {
	Version     string         `json:"version"`
	Name        string         `json:"name"`
	Permissions []string       `json:"permissions"`
	Manifest    map[string]any `json:"manifest"`
	Digest      string         `json:"digest"`
	Signature   string         `json:"signature"`
	KeyID       string         `json:"key_id"`
	Size        int64          `json:"size"`
	ReleasedAt  time.Time      `json:"released_at"`
	Flagged     bool           `json:"flagged"`
	FlagNote    string         `json:"flag_note"`
}

// ListingRecord is one module's listing. Its id is the module id in the signed manifest.
type ListingRecord struct {
	ID        string    `json:"id"`
	AuthorID  string    `json:"author_id"`
	PriceNote string    `json:"price_note"`
	Versions  []Version `json:"versions"`
	Flagged   bool      `json:"flagged"`
	FlagNote  string    `json:"flag_note"`
	Pulled    bool      `json:"pulled"`
	PullNote  string    `json:"pull_note"`
	CreatedAt time.Time `json:"created_at"`
}

// AlertRecord is one operator flag or pull.
type AlertRecord struct {
	ID        string    `json:"id"`
	ModuleID  string    `json:"module_id"`
	Version   string    `json:"version,omitempty"`
	Kind      string    `json:"kind"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// State is everything the service stores apart from package bytes.
type State struct {
	Authors  []Author        `json:"authors"`
	Tokens   []Token         `json:"tokens"`
	Keys     []Key           `json:"keys"`
	Listings []ListingRecord `json:"listings"`
	Alerts   []AlertRecord   `json:"alerts"`
}

// fileStore persists State and package bytes under dir.
type fileStore struct {
	dir string
}

const stateName = "state.json"

func (f fileStore) packagesDir() string { return filepath.Join(f.dir, "packages") }

func (f fileStore) load() (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(f.dir, stateName))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("marketplace: %s: %w", stateName, err)
	}
	return st, nil
}

func (f fileStore) writeAtomic(name string, body []byte) error {
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, privateDir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (f fileStore) save(st State) error {
	b, err := json.MarshalIndent(st, "", " ")
	if err != nil {
		return err
	}
	return f.writeAtomic(filepath.Join(f.dir, stateName), b)
}

// packagePath names the file holding the package with digest d (sha256:<hex>).
func (f fileStore) packagePath(digest string) (string, error) {
	hexPart, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || strings.ContainsAny(hexPart, `/\.`) || hexPart == "" {
		return "", errors.New("marketplace: bad digest")
	}
	return filepath.Join(f.packagesDir(), hexPart), nil
}

func (f fileStore) putPackage(digest string, body []byte) error {
	p, err := f.packagePath(digest)
	if err != nil {
		return err
	}
	return f.writeAtomic(p, body)
}

func (f fileStore) getPackage(digest string) ([]byte, error) {
	p, err := f.packagePath(digest)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

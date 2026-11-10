// Package storage holds the byte adapters behind Togavsu's one file store (PRD 8.19).
//
// Every backend is one Go interface: put, get with byte ranges, stat, delete and a
// health check. Keys are opaque (the version or rendition id), never file names.
// Sealed wraps a backend with AES-256-GCM, and Verify checks a read against the
// SHA-256 the file record holds. Modules cannot add backends; only the core builds
// them, from the admin's storage_backends rows.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// Kinds match storage_backends.kind.
const (
	KindLocalDisk   = "local_disk"
	KindNetworkPath = "network_path"
	KindS3          = "s3"
	KindDropbox     = "dropbox"
)

var (
	// ErrNotFound means the key has no object on this backend.
	ErrNotFound = errors.New("storage: object not found")
	// ErrExists means a put would overwrite an object. Versions never change.
	ErrExists = errors.New("storage: object already exists")
	// ErrIntegrity means the bytes read do not match the recorded SHA-256 or fail authentication.
	ErrIntegrity = errors.New("storage: integrity check failed")
	// ErrKey means a key is not an opaque storage key.
	ErrKey = errors.New("storage: invalid key")
	// ErrConfig means the backend settings are incomplete or malformed.
	ErrConfig = errors.New("storage: invalid backend settings")
)

// Info is what Stat reports. Size is the stored object's length on this backend.
type Info struct {
	Size int64
}

// Backend is the one interface every storage adapter implements.
// Get returns length bytes from offset; a negative length reads to the end.
type Backend interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error)
	Stat(ctx context.Context, key string) (Info, error)
	Delete(ctx context.Context, key string) error
	Health(ctx context.Context) error
}

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// CheckKey refuses anything that could be a path or a file name with separators.
func CheckKey(key string) error {
	if !keyPattern.MatchString(key) || len(key) > maxKey {
		return fmt.Errorf("%w: %q", ErrKey, key)
	}
	return nil
}

// zero is the integer origin for offsets, lengths and counts.
const zero = int64(0)

// maxKey is a format bound: UUIDv7 text plus a short suffix fits well inside it.
const maxKey = 1 << 7

// Settings is the decrypted settings object of one storage_backends row.
// Only the fields for that row's kind are used.
type Settings struct {
	// Path is the local directory (local disk) or the mounted share (network path).
	Path string `json:"path,omitempty"`
	// DropPath is the drop folder on a network path. Empty means <path>/drop.
	DropPath string `json:"drop_path,omitempty"`

	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`

	// Dropbox: an app-folder app writes under Apps/Togavsu. A refresh token with the
	// app key and secret renews the short-lived access token.
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AppKey       string `json:"app_key,omitempty"`
	AppSecret    string `json:"app_secret,omitempty"`
	APIBase      string `json:"api_base,omitempty"`
	ContentBase  string `json:"content_base,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
}

// Public returns the settings with every secret removed, for API responses.
func (s Settings) Public() map[string]any {
	out := map[string]any{}
	add := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	add("path", s.Path)
	add("drop_path", s.DropPath)
	add("endpoint", s.Endpoint)
	add("region", s.Region)
	add("bucket", s.Bucket)
	add("prefix", s.Prefix)
	add("access_key", s.AccessKey)
	add("api_base", s.APIBase)
	add("content_base", s.ContentBase)
	if s.SecretKey != "" || s.AccessToken != "" || s.RefreshToken != "" || s.AppSecret != "" {
		out["secrets_set"] = true
	}
	return out
}

// ParseSettings decodes a settings object and rejects unknown fields.
func ParseSettings(raw []byte) (Settings, error) {
	var s Settings
	if len(raw) == 0 {
		return s, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	return s, nil
}

// Options carries what adapters share. A nil HTTP client uses http.DefaultClient.
type Options struct {
	HTTP *http.Client
}

// Open builds the adapter for kind. The caller wraps it with Sealed when encryption is on.
func Open(kind string, s Settings, opts Options) (Backend, error) {
	switch kind {
	case KindLocalDisk, KindNetworkPath:
		return NewDisk(s.Path)
	case KindS3:
		return NewS3(s, opts.HTTP)
	case KindDropbox:
		return NewDropbox(s, opts.HTTP)
	default:
		return nil, fmt.Errorf("%w: kind %q", ErrConfig, kind)
	}
}

// AlwaysEncrypted reports the kinds whose bytes are always sealed before they leave the core.
func AlwaysEncrypted(kind string) bool {
	return kind == KindS3 || kind == KindDropbox
}

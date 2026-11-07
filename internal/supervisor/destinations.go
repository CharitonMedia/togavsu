package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Destination copies an encrypted archive off the local volume.
type Destination interface {
	ID() string
	Kind() string
	Put(ctx context.Context, name string, r io.Reader, sha256 string) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	Delete(ctx context.Context, name string) error
	List(ctx context.Context) ([]RemoteObject, error)
	Test(ctx context.Context) error
}

type RemoteObject struct {
	Name    string
	Created time.Time
}

// DestinationSpec is the on-disk record for one copy target.
type DestinationSpec struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	RetentionDays int    `json:"retention_days"`
	Path          string `json:"path,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Region        string `json:"region,omitempty"`
	Bucket        string `json:"bucket,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	AccessKey     string `json:"access_key,omitempty"`
	SecretKey     string `json:"secret_key,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user,omitempty"`
	IdentityFile  string `json:"identity_file,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
	RemotePath    string `json:"remote_path,omitempty"`
}

// Public hides credentials from API responses.
func (s DestinationSpec) Public() DestinationSpec {
	s.SecretKey = ""
	if s.AccessKey != "" {
		s.AccessKey = "configured"
	}
	return s
}

// DestinationBook persists destination specs as JSON on the backup volume.
//
// ponytail: destinations live in a supervisor JSON file, ceiling: not the
// core settings table, upgrade: core backup-destination records push here,
// trigger: /api/v1/backup-destinations is served by core.
type DestinationBook struct {
	Path string
}

func (b *DestinationBook) List() ([]Destination, error) {
	specs, err := b.Specs()
	if err != nil {
		return nil, err
	}
	out := make([]Destination, 0, len(specs))
	for _, spec := range specs {
		dest, err := OpenDestination(spec)
		if err != nil {
			return nil, err
		}
		out = append(out, dest)
	}
	return out, nil
}

func (b *DestinationBook) Specs() ([]DestinationSpec, error) {
	raw, err := os.ReadFile(b.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var specs []DestinationSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return nil, err
	}
	return specs, nil
}

func (b *DestinationBook) Put(spec DestinationSpec) (DestinationSpec, error) {
	if err := validateSpec(spec); err != nil {
		return DestinationSpec{}, err
	}
	if spec.RetentionDays == 0 {
		spec.RetentionDays = 30
	}
	specs, err := b.Specs()
	if err != nil {
		return DestinationSpec{}, err
	}
	replaced := false
	for i := range specs {
		if specs[i].ID == spec.ID {
			specs[i] = spec
			replaced = true
		}
	}
	if !replaced {
		specs = append(specs, spec)
	}
	if err := b.save(specs); err != nil {
		return DestinationSpec{}, err
	}
	return spec, nil
}

func (b *DestinationBook) Delete(id string) error {
	specs, err := b.Specs()
	if err != nil {
		return err
	}
	next := specs[:0]
	found := false
	for _, spec := range specs {
		if spec.ID == id {
			found = true
			continue
		}
		next = append(next, spec)
	}
	if !found {
		return ErrNotFound
	}
	return b.save(next)
}

func (b *DestinationBook) Get(id string) (Destination, error) {
	specs, err := b.Specs()
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if spec.ID == id {
			return OpenDestination(spec)
		}
	}
	return nil, ErrNotFound
}

func (b *DestinationBook) save(specs []DestinationSpec) error {
	if err := os.MkdirAll(filepath.Dir(b.Path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := writeFileAtomic(b.Path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(b.Path, 0o600)
}

func validateSpec(spec DestinationSpec) error {
	if spec.ID == "" || strings.Contains(spec.ID, "/") || spec.ID == "." || spec.ID == ".." {
		return fmt.Errorf("destination id: %w", ErrBadRequest)
	}
	if spec.RetentionDays < 0 || spec.RetentionDays > 36500 {
		return fmt.Errorf("retention: %w", ErrBadRequest)
	}
	switch spec.Kind {
	case "path":
		if spec.Path == "" || !filepath.IsAbs(spec.Path) {
			return fmt.Errorf("path: %w", ErrBadRequest)
		}
	case "s3":
		if spec.Endpoint == "" || spec.Bucket == "" || spec.Region == "" || spec.AccessKey == "" || spec.SecretKey == "" {
			return fmt.Errorf("s3: %w", ErrBadRequest)
		}
		if !strings.HasPrefix(spec.Endpoint, "https://") && !strings.HasPrefix(spec.Endpoint, "http://") {
			return fmt.Errorf("s3 endpoint: %w", ErrBadRequest)
		}
	case "sftp":
		if spec.Host == "" || spec.User == "" || spec.IdentityFile == "" || spec.KnownHosts == "" || spec.RemotePath == "" {
			return fmt.Errorf("sftp: %w", ErrBadRequest)
		}
		if strings.Contains(spec.RemotePath, "..") {
			return fmt.Errorf("sftp path: %w", ErrBadRequest)
		}
	default:
		return fmt.Errorf("kind: %w", ErrBadRequest)
	}
	return nil
}

// OpenDestination builds a copier from a spec.
func OpenDestination(spec DestinationSpec) (Destination, error) {
	if err := validateSpec(spec); err != nil {
		return nil, err
	}
	switch spec.Kind {
	case "path":
		return &PathDestination{Spec: spec}, nil
	case "s3":
		return &S3Destination{Spec: spec}, nil
	case "sftp":
		return &SFTPDestination{Spec: spec, Command: "sftp"}, nil
	default:
		return nil, ErrBadRequest
	}
}

// PathDestination copies archives onto a local or mounted network path.
type PathDestination struct {
	Spec DestinationSpec
}

func (p *PathDestination) ID() string   { return p.Spec.ID }
func (p *PathDestination) Kind() string { return "path" }

func (p *PathDestination) Put(_ context.Context, name string, r io.Reader, sha string) error {
	if err := safeName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Spec.Path, 0o700); err != nil {
		return err
	}
	target := filepath.Join(p.Spec.Path, name)
	tmp := target + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if sha != "" {
		got, err := hashFile(tmp)
		if err != nil {
			os.Remove(tmp)
			return err
		}
		if got != sha {
			os.Remove(tmp)
			return ErrChecksum
		}
	}
	return os.Rename(tmp, target)
}

func (p *PathDestination) Get(_ context.Context, name string) (io.ReadCloser, error) {
	if err := safeName(name); err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(p.Spec.Path, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

func (p *PathDestination) Delete(_ context.Context, name string) error {
	if err := safeName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(p.Spec.Path, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (p *PathDestination) List(context.Context) ([]RemoteObject, error) {
	entries, err := os.ReadDir(p.Spec.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []RemoteObject
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, RemoteObject{Name: ent.Name(), Created: info.ModTime()})
	}
	return out, nil
}

func (p *PathDestination) Test(ctx context.Context) error {
	return roundTripTest(ctx, p)
}

func safeName(name string) error {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		return ErrBadRequest
	}
	return nil
}

func roundTripTest(ctx context.Context, dest Destination) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	name := "togavsu-destination-test"
	sum, err := sha256Hex(bytes.NewReader(raw[:]))
	if err != nil {
		return err
	}
	if err := dest.Put(ctx, name, bytes.NewReader(raw[:]), sum); err != nil {
		return err
	}
	defer dest.Delete(ctx, name)
	rc, err := dest.Get(ctx, name)
	if err != nil {
		return err
	}
	defer rc.Close()
	got, err := sha256Hex(rc)
	if err != nil {
		return err
	}
	if got != sum {
		return ErrChecksum
	}
	return nil
}

// PruneDestination deletes objects older than the destination retention.
func PruneDestination(ctx context.Context, dest Destination, retention time.Duration, now time.Time) (int, error) {
	if retention <= 0 {
		return 0, fmt.Errorf("retention: %w", ErrBadRequest)
	}
	objs, err := dest.List(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-retention)
	n := 0
	for _, obj := range objs {
		if strings.HasPrefix(obj.Name, "togavsu-destination-test") || obj.Created.IsZero() {
			continue
		}
		if obj.Created.Before(cutoff) && strings.HasSuffix(obj.Name, ".tgbk") {
			if err := dest.Delete(ctx, obj.Name); err != nil {
				return n, err
			}
			_ = dest.Delete(ctx, strings.TrimSuffix(obj.Name, ".tgbk")+".sha256")
			n++
		}
	}
	return n, nil
}

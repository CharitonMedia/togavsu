package editor

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// Settings is the registry read the editor needs.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
}

// Users resolves a user's name for the lock notice, the author of tracked changes and
// the attorney block of a pleading. identity's user repository satisfies it.
type Users interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
}

// Config wires the editor.
type Config struct {
	Files    *files.Service
	Pool     *sql.DB
	Audit    *audit.Service
	Settings Settings
	Users    Users
	// Matters reads a matter's caption as the caller; nil leaves New Pleading unfilled.
	Matters Matters
	CLI     *CLI
	Now     func() time.Time
}

// Service is the document editor. Every method takes the real principal and checks it
// here, so the page, the API, /mcp and modules get one answer.
type Service struct {
	files    *files.Service
	pool     *sql.DB
	audit    *audit.Service
	settings Settings
	users    Users
	matters  Matters
	cli      *CLI
	now      func() time.Time

	// beforeLockedWrite, when set (tests), runs inside writeUnderLock after the lock check
	// and before the version is written.
	beforeLockedWrite func()
}

// New validates cfg.
func New(cfg Config) (*Service, error) {
	if cfg.Files == nil || cfg.Pool == nil || cfg.Audit == nil || cfg.Settings == nil || cfg.Users == nil || cfg.CLI == nil {
		return nil, errors.New("editor: files, pool, audit, settings, users and the wordcraft-cli runner are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	s := &Service{files: cfg.Files, pool: cfg.Pool, audit: cfg.Audit, settings: cfg.Settings, users: cfg.Users,
		matters: cfg.Matters, cli: cfg.CLI, now: cfg.Now}
	if cfg.CLI.Limits == nil {
		cfg.CLI.Limits = s.Limits
	}
	return s, nil
}

// Limits reads the Document editor tab.
func (s *Service) Limits(ctx context.Context) (Limits, error) {
	n, err := s.intSetting(ctx, "editor.max_runs")
	if err != nil {
		return Limits{}, err
	}
	t, err := s.durationSetting(ctx, "editor.run_timeout")
	if err != nil {
		return Limits{}, err
	}
	m, err := s.intSetting(ctx, "editor.run_memory_bytes")
	if err != nil {
		return Limits{}, err
	}
	return Limits{Concurrent: int(n), Timeout: t, MemoryBytes: m}, nil
}

func (s *Service) intSetting(ctx context.Context, key string) (int64, error) {
	raw, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

func (s *Service) durationSetting(ctx context.Context, key string) (time.Duration, error) {
	raw, err := s.settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(raw)
}

// editorRoles may use the editor (PRD 4: staff and up edit files; client users get no
// editor in the core, and the accountant role has no file writes).
func requireEditor(p domain.Principal) error {
	if p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff) {
		return nil
	}
	if p.HasRole(domain.RoleClient) {
		return domain.Forbidden("client users get no editor")
	}
	return domain.Forbidden("the editor needs the admin, attorney or staff role")
}

// current is the file, its current version and that version's bytes, read with write
// access: the same check as a save, so a caller who could not save cannot read for one.
type current struct {
	File    files.FileView
	Version files.VersionView
	Bytes   []byte
}

// maxDocumentBytes bounds what the editor loads into memory for one run.
const maxDocumentBytes = 256 << 20

func (s *Service) loadCurrent(ctx context.Context, p domain.Principal, fileID domain.ID) (current, error) {
	if err := requireEditor(p); err != nil {
		return current{}, err
	}
	f, err := s.files.Get(ctx, p, fileID)
	if err != nil {
		return current{}, err
	}
	if f.TrashedAt != nil {
		return current{}, domain.Conflict("restore the file from the trash first")
	}
	if f.CurrentVersionID == nil {
		return current{}, domain.Conflict("the file has no version yet")
	}
	v, err := s.files.Version(ctx, p, fileID, *f.CurrentVersionID)
	if err != nil {
		return current{}, err
	}
	d, err := s.files.OpenForProcessing(ctx, p, fileID, v.ID)
	if err != nil {
		return current{}, err
	}
	defer d.Close()
	if v.Quarantined {
		return current{}, domain.Conflict("the current version is quarantined; restore a clean version or clear the quarantine first")
	}
	if d.Size > maxDocumentBytes {
		return current{}, domain.Invalid("file", "the document is too large for the editor")
	}
	b, err := io.ReadAll(d.Body)
	if err != nil {
		return current{}, err
	}
	return current{File: f, Version: v, Bytes: b}, nil
}

func (s *Service) userName(ctx context.Context, id domain.ID) string {
	if id.IsZero() {
		return ""
	}
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return ""
	}
	return u.Name
}

func (s *Service) record(ctx context.Context, p domain.Principal, entity string, id domain.ID, field, old, new string) error {
	if p.Source == domain.SourceSystem && p.UserID.IsZero() {
		p.UserID = domain.SystemUserID
	}
	_, err := s.audit.Record(ctx, p, audit.Change{Entity: entity, EntityID: id, Field: field, Old: old, New: new})
	return err
}

func us(t time.Time) int64 { return domain.UnixMicroUTC(t) }

func fromUS(v int64) time.Time { return time.UnixMicro(v).UTC() }

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func reader(b []byte) (io.Reader, int64) { return bytes.NewReader(b), int64(len(b)) }

func newID() (domain.ID, error) {
	id, err := domain.NewID()
	if err != nil {
		return "", fmt.Errorf("editor: id: %w", err)
	}
	return id, nil
}

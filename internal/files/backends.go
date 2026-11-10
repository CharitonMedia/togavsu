package files

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// sealSettings encrypts a backend's settings object with AES-256-GCM.
func (s *Service) sealSettings(set storage.Settings) (string, error) {
	raw, err := json.Marshal(set)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.configKey)
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "v1:" + base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, raw, []byte("storage_backends.settings"))), nil
}

func (s *Service) openSettings(sealed string) (storage.Settings, error) {
	raw, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return storage.Settings{}, fmt.Errorf("%w: settings are not sealed", storage.ErrConfig)
	}
	buf, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return storage.Settings{}, err
	}
	block, err := aes.NewCipher(s.configKey)
	if err != nil {
		return storage.Settings{}, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return storage.Settings{}, err
	}
	if len(buf) < g.NonceSize() {
		return storage.Settings{}, storage.ErrConfig
	}
	plain, err := g.Open(nil, buf[:g.NonceSize()], buf[g.NonceSize():], []byte("storage_backends.settings"))
	if err != nil {
		return storage.Settings{}, fmt.Errorf("%w: settings do not open with this key", storage.ErrConfig)
	}
	return storage.ParseSettings(plain)
}

// BackendView is the StorageBackend contract shape. Secrets never leave the core.
type BackendView struct {
	ID         domain.ID      `json:"id"`
	CreatedAt  string         `json:"created_at"`
	CreatedBy  domain.ID      `json:"created_by"`
	UpdatedAt  string         `json:"updated_at"`
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Encryption bool           `json:"encryption"`
	Default    bool           `json:"default"`
	Health     string         `json:"health"`
	BackedUp   bool           `json:"backed_up"`
	DropFolder bool           `json:"drop_folder"`
	Settings   map[string]any `json:"settings,omitempty"`
}

func (s *Service) backendView(b domain.StorageBackend) BackendView {
	v := BackendView{
		ID: b.ID, CreatedAt: domain.FormatUTC(b.CreatedAt), CreatedBy: b.CreatedBy, UpdatedAt: domain.FormatUTC(b.UpdatedAt),
		Kind: b.Kind, Name: b.Name, Encryption: b.Encryption || storage.AlwaysEncrypted(b.Kind), Default: b.IsDefault,
		Health: b.Health, BackedUp: b.BackedUp, DropFolder: b.DropFolder,
	}
	if set, err := s.openSettings(b.SettingsCiphertext); err == nil {
		v.Settings = set.Public()
	}
	return v
}

// BackendWrite is StorageBackendWrite. Nil fields keep their value on update.
type BackendWrite struct {
	Kind       string           `json:"kind"`
	Name       string           `json:"name"`
	Encryption *bool            `json:"encryption"`
	Default    *bool            `json:"default"`
	BackedUp   *bool            `json:"backed_up"`
	DropFolder *bool            `json:"drop_folder"`
	Settings   *json.RawMessage `json:"settings"`
}

// encryptionDefault reads the per-kind setting. S3 and Dropbox are locked on.
func (s *Service) encryptionDefault(ctx context.Context, kind string) (bool, error) {
	key := map[string]string{
		storage.KindLocalDisk: "files.encrypt_local_disk", storage.KindNetworkPath: "files.encrypt_network",
		storage.KindS3: "files.encrypt_s3", storage.KindDropbox: "files.encrypt_dropbox",
	}[kind]
	raw, err := s.settings.Get(ctx, key)
	return raw == "true", err
}

// Backends lists storage backends. Admin only.
func (s *Service) Backends(ctx context.Context, p domain.Principal) ([]BackendView, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	rows, err := s.st.backends(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BackendView, 0, len(rows))
	for _, b := range rows {
		out = append(out, s.backendView(b))
	}
	return out, nil
}

// GetBackend reads one backend. Admin only.
func (s *Service) GetBackend(ctx context.Context, p domain.Principal, id domain.ID) (BackendView, error) {
	if err := admin(p); err != nil {
		return BackendView{}, err
	}
	b, err := s.loadBackend(ctx, id)
	if err != nil {
		return BackendView{}, err
	}
	return s.backendView(b), nil
}

func (s *Service) loadBackend(ctx context.Context, id domain.ID) (domain.StorageBackend, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return domain.StorageBackend{}, domain.NotFound("storage backend")
	}
	b, err := s.st.getBackend(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.StorageBackend{}, domain.NotFound("storage backend")
	}
	return b, err
}

// CreateBackend adds a backend after its settings open an adapter. Admin only.
func (s *Service) CreateBackend(ctx context.Context, p domain.Principal, in BackendWrite) (BackendView, error) {
	if err := admin(p); err != nil {
		return BackendView{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return BackendView{}, domain.Invalid("name", "a backend needs a name")
	}
	var set storage.Settings
	if in.Settings != nil {
		var err error
		if set, err = storage.ParseSettings(*in.Settings); err != nil {
			return BackendView{}, domain.Invalid("settings", err.Error())
		}
	}
	if _, err := storage.Open(in.Kind, set, storage.Options{HTTP: s.http}); err != nil {
		return BackendView{}, domain.Invalid("settings", err.Error())
	}
	enc, err := s.encryptionDefault(ctx, in.Kind)
	if err != nil {
		return BackendView{}, err
	}
	if in.Encryption != nil {
		if !*in.Encryption && storage.AlwaysEncrypted(in.Kind) {
			return BackendView{}, domain.Invalid("encryption", "S3-compatible and Dropbox storage are always encrypted")
		}
		enc = *in.Encryption
	}
	if in.DropFolder != nil && *in.DropFolder && in.Kind != storage.KindNetworkPath {
		return BackendView{}, domain.Invalid("drop_folder", "only a network path has a drop folder")
	}
	sealed, err := s.sealSettings(set)
	if err != nil {
		return BackendView{}, err
	}
	m, err := s.meta(actor(p, domain.SystemUserID))
	if err != nil {
		return BackendView{}, err
	}
	b := domain.StorageBackend{
		Meta: m, Kind: in.Kind, Name: in.Name, SettingsCiphertext: sealed, Encryption: enc,
		IsDefault: in.Default != nil && *in.Default, Health: "unknown", BackedUp: in.BackedUp == nil || *in.BackedUp,
		DropFolder: in.DropFolder != nil && *in.DropFolder,
	}
	err = s.tx(ctx, func(ctx context.Context) error {
		existing, err := s.st.backends(ctx)
		if err != nil {
			return err
		}
		if len(existing) == 0 {
			b.IsDefault = true
		}
		if err := s.st.insertBackend(ctx, b); err != nil {
			return err
		}
		if b.IsDefault {
			if err := s.st.clearDefault(ctx, b.ID, us(m.CreatedAt)); err != nil {
				return err
			}
		}
		return s.record(ctx, p, "storage_backend", b.ID, "created", "", b.Kind+" "+b.Name)
	})
	if err != nil {
		return BackendView{}, err
	}
	return s.backendView(b), nil
}

// UpdateBackend edits a backend. Turning encryption on or off applies to new bytes;
// stored objects keep the form they were written in until a move rewrites them.
func (s *Service) UpdateBackend(ctx context.Context, p domain.Principal, id domain.ID, in BackendWrite) (BackendView, error) {
	if err := admin(p); err != nil {
		return BackendView{}, err
	}
	var out domain.StorageBackend
	err := s.tx(ctx, func(ctx context.Context) error {
		b, err := s.loadBackend(ctx, id)
		if err != nil {
			return err
		}
		old := s.backendView(b)
		if in.Kind != "" && in.Kind != b.Kind {
			return domain.Invalid("kind", "a backend's kind cannot change")
		}
		if in.Name != "" {
			b.Name = in.Name
		}
		if in.Settings != nil {
			set, err := storage.ParseSettings(*in.Settings)
			if err != nil {
				return domain.Invalid("settings", err.Error())
			}
			if cur, err := s.openSettings(b.SettingsCiphertext); err == nil {
				set = keepSecrets(set, cur)
			}
			if _, err := storage.Open(b.Kind, set, storage.Options{HTTP: s.http}); err != nil {
				return domain.Invalid("settings", err.Error())
			}
			if b.SettingsCiphertext, err = s.sealSettings(set); err != nil {
				return err
			}
		}
		if in.Encryption != nil {
			if !*in.Encryption && storage.AlwaysEncrypted(b.Kind) {
				return domain.Invalid("encryption", "S3-compatible and Dropbox storage are always encrypted")
			}
			// Bytes already stored keep the form they were written in; each object
			// records whether it is sealed.
			b.Encryption = *in.Encryption
		}
		if in.BackedUp != nil {
			b.BackedUp = *in.BackedUp
		}
		if in.DropFolder != nil {
			if *in.DropFolder && b.Kind != storage.KindNetworkPath {
				return domain.Invalid("drop_folder", "only a network path has a drop folder")
			}
			b.DropFolder = *in.DropFolder
		}
		if in.Default != nil {
			if !*in.Default && b.IsDefault {
				return domain.Invalid("default", "mark another backend default instead")
			}
			b.IsDefault = *in.Default
		}
		b.UpdatedAt = s.now().UTC()
		if err := s.st.updateBackend(ctx, b); err != nil {
			return err
		}
		if b.IsDefault {
			if err := s.st.clearDefault(ctx, b.ID, us(b.UpdatedAt)); err != nil {
				return err
			}
		}
		nv := s.backendView(b)
		for _, c := range []struct{ field, old, new string }{
			{"name", old.Name, nv.Name}, {"default", fmt.Sprint(old.Default), fmt.Sprint(nv.Default)},
			{"encryption", fmt.Sprint(old.Encryption), fmt.Sprint(nv.Encryption)},
			{"backed_up", fmt.Sprint(old.BackedUp), fmt.Sprint(nv.BackedUp)}, {"drop_folder", fmt.Sprint(old.DropFolder), fmt.Sprint(nv.DropFolder)},
		} {
			if c.old != c.new {
				if err := s.record(ctx, p, "storage_backend", b.ID, c.field, c.old, c.new); err != nil {
					return err
				}
			}
		}
		if in.Settings != nil {
			if err := s.record(ctx, p, "storage_backend", b.ID, "settings", "", "changed"); err != nil {
				return err
			}
		}
		out = b
		return nil
	})
	if err != nil {
		return BackendView{}, err
	}
	return s.backendView(out), nil
}

// keepSecrets carries stored secrets forward when an edit leaves them blank.
func keepSecrets(next, cur storage.Settings) storage.Settings {
	if next.SecretKey == "" {
		next.SecretKey = cur.SecretKey
	}
	if next.AccessToken == "" {
		next.AccessToken = cur.AccessToken
	}
	if next.RefreshToken == "" {
		next.RefreshToken = cur.RefreshToken
	}
	if next.AppSecret == "" {
		next.AppSecret = cur.AppSecret
	}
	return next
}

// TestBackend is the Test button: a health check that records ok or failed.
func (s *Service) TestBackend(ctx context.Context, p domain.Principal, id domain.ID) (BackendView, error) {
	if err := admin(p); err != nil {
		return BackendView{}, err
	}
	row, err := s.loadBackend(ctx, id)
	if err != nil {
		return BackendView{}, err
	}
	health := "ok"
	b, _, err := s.adapter(ctx, id)
	if err == nil {
		err = b.Health(ctx)
	}
	if err != nil {
		health = "failed"
	}
	if health != row.Health {
		row.Health = health
		if err := s.st.updateBackend(ctx, row); err != nil {
			return BackendView{}, err
		}
	}
	return s.backendView(row), nil
}

// EnsureLocalDisk creates the default local disk backend on a fresh install, at dir
// (the core's file volume), when no backend exists yet. Encryption follows
// files.encrypt_local_disk.
func (s *Service) EnsureLocalDisk(ctx context.Context, p domain.Principal, dir string) (BackendView, error) {
	rows, err := s.st.backends(ctx)
	if err != nil {
		return BackendView{}, err
	}
	if len(rows) > 0 {
		def, err := s.defaultBackend(ctx)
		if err != nil {
			return BackendView{}, err
		}
		return s.backendView(def), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return BackendView{}, err
	}
	if err := os.MkdirAll(abs, os.ModePerm&0o700); err != nil {
		return BackendView{}, err
	}
	raw := json.RawMessage(fmt.Sprintf(`{"path":%q}`, abs))
	yes := true
	return s.CreateBackend(ctx, p, BackendWrite{Kind: storage.KindLocalDisk, Name: "Local disk", Default: &yes, Settings: &raw})
}

// FolderView is the Folder contract shape.
type FolderView struct {
	ID        domain.ID `json:"id"`
	CreatedAt string    `json:"created_at"`
	CreatedBy domain.ID `json:"created_by"`
	UpdatedAt string    `json:"updated_at"`
	MatterID  domain.ID `json:"matter_id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sort_order"`
}

func folderView(f Folder) FolderView {
	return FolderView{ID: f.ID, CreatedAt: domain.FormatUTC(f.CreatedAt), CreatedBy: f.CreatedBy, UpdatedAt: domain.FormatUTC(f.UpdatedAt), MatterID: f.MatterID, Name: f.Name, SortOrder: f.SortOrder}
}

// Folders lists the folders of a matter the caller may read (walls, role and client
// scope, matterReadable). The first time someone who can write files on the matter
// opens it, its default folders are created: the matter's practice area defaults when
// the directory knows them, else files.default_folders. A reader who cannot write
// never creates rows.
func (s *Service) Folders(ctx context.Context, p domain.Principal, matter domain.ID) ([]FolderView, error) {
	if err := s.matterReadable(ctx, p, matter); err != nil {
		return nil, err
	}
	m, err := s.dir.Matter(ctx, matter)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, domain.NotFound("matter")
		}
		return nil, err
	}
	rows, err := s.st.folders(ctx, matter)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && s.canWriteTo(ctx, p, matter, "", false) == nil {
		if rows, err = s.seedFolders(ctx, p, m); err != nil {
			return nil, err
		}
	}
	out := make([]FolderView, 0, len(rows))
	for _, f := range rows {
		out = append(out, folderView(f))
	}
	return out, nil
}

// defaultFolders is the folder list for a new matter: its practice area's
// default_folders when the directory provides them (AreaFolders), else the global
// files.default_folders setting.
func (s *Service) defaultFolders(ctx context.Context, m Matter) ([]string, error) {
	if src, ok := s.dir.(AreaFolders); ok && m.PracticeAreaID != "" {
		names, err := src.AreaFolders(ctx, m.PracticeAreaID)
		if err != nil {
			return nil, err
		}
		if len(names) > 0 {
			return names, nil
		}
	}
	raw, err := s.settings.Get(ctx, "files.default_folders")
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil, err
	}
	return names, nil
}

func (s *Service) seedFolders(ctx context.Context, p domain.Principal, m Matter) ([]Folder, error) {
	matter := m.ID
	names, err := s.defaultFolders(ctx, m)
	if err != nil {
		return nil, err
	}
	var out []Folder
	err = s.tx(ctx, func(ctx context.Context) error {
		for i, name := range names {
			m, err := s.meta(actor(p, domain.SystemUserID))
			if err != nil {
				return err
			}
			f := Folder{Meta: m, MatterID: matter, Name: name, SortOrder: i}
			if err := s.st.insertFolder(ctx, f); err != nil {
				return err
			}
			out = append(out, f)
		}
		return nil
	})
	return out, err
}

// FolderByName returns a matter folder by its name, for Doc Tender's category folders.
func (s *Service) FolderByName(ctx context.Context, p domain.Principal, matter domain.ID, name string) (FolderView, error) {
	rows, err := s.Folders(ctx, p, matter)
	if err != nil {
		return FolderView{}, err
	}
	for _, f := range rows {
		if strings.EqualFold(f.Name, name) {
			return f, nil
		}
	}
	return s.CreateFolder(ctx, p, matter, name, len(rows))
}

// CreateFolder adds a folder to a matter the caller can write files on.
func (s *Service) CreateFolder(ctx context.Context, p domain.Principal, matter domain.ID, name string, order int) (FolderView, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return FolderView{}, err
	}
	if err := s.canWriteTo(ctx, p, matter, "", false); err != nil {
		return FolderView{}, err
	}
	m, err := s.meta(actor(p, domain.SystemUserID))
	if err != nil {
		return FolderView{}, err
	}
	f := Folder{Meta: m, MatterID: matter, Name: name, SortOrder: order}
	err = s.tx(ctx, func(ctx context.Context) error {
		existing, err := s.st.folders(ctx, matter)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if strings.EqualFold(e.Name, name) {
				return domain.Conflict("the matter already has a folder named " + e.Name)
			}
		}
		if err := s.st.insertFolder(ctx, f); err != nil {
			return err
		}
		return s.record(ctx, p, "folder", f.ID, "name", "", name)
	})
	if err != nil {
		return FolderView{}, err
	}
	return folderView(f), nil
}

func (s *Service) writableFolder(ctx context.Context, p domain.Principal, id domain.ID) (Folder, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return Folder{}, domain.NotFound("folder")
	}
	f, err := s.st.getFolder(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return Folder{}, domain.NotFound("folder")
	}
	if err != nil {
		return Folder{}, err
	}
	if err := s.canWriteTo(ctx, p, f.MatterID, "", false); err != nil {
		return Folder{}, err
	}
	return f, nil
}

// GetFolder reads one folder on a visible matter.
func (s *Service) GetFolder(ctx context.Context, p domain.Principal, id domain.ID) (FolderView, error) {
	if _, err := domain.ParseID(string(id)); err != nil {
		return FolderView{}, domain.NotFound("folder")
	}
	f, err := s.st.getFolder(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return FolderView{}, domain.NotFound("folder")
	}
	if err != nil {
		return FolderView{}, err
	}
	if err := s.matterReadable(ctx, p, f.MatterID); err != nil {
		return FolderView{}, domain.NotFound("folder")
	}
	return folderView(f), nil
}

// UpdateFolder renames or reorders a folder. Files keep their folder id.
func (s *Service) UpdateFolder(ctx context.Context, p domain.Principal, id domain.ID, name *string, order *int) (FolderView, error) {
	var out Folder
	err := s.tx(ctx, func(ctx context.Context) error {
		f, err := s.writableFolder(ctx, p, id)
		if err != nil {
			return err
		}
		old := f.Name
		if name != nil {
			if err := validName(*name); err != nil {
				return err
			}
			f.Name = strings.TrimSpace(*name)
		}
		if order != nil {
			f.SortOrder = *order
		}
		f.UpdatedAt = s.now().UTC()
		if err := s.st.updateFolder(ctx, f); err != nil {
			return err
		}
		out = f
		if old != f.Name {
			return s.record(ctx, p, "folder", f.ID, "name", old, f.Name)
		}
		return nil
	})
	if err != nil {
		return FolderView{}, err
	}
	return folderView(out), nil
}

// DeleteFolder removes an empty folder. A folder that holds files is refused.
func (s *Service) DeleteFolder(ctx context.Context, p domain.Principal, id domain.ID) error {
	return s.tx(ctx, func(ctx context.Context) error {
		f, err := s.writableFolder(ctx, p, id)
		if err != nil {
			return err
		}
		used, err := s.st.folderInUse(ctx, id)
		if err != nil {
			return err
		}
		if used {
			return domain.Conflict("move the folder's files out before deleting it")
		}
		if err := s.st.deleteFolder(ctx, id); err != nil {
			return err
		}
		return s.record(ctx, p, "folder", f.ID, "name", f.Name, "")
	})
}

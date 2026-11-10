package files

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/storage"
)

// BackupObject is one manifest entry of a file-bytes backup.
type BackupObject struct {
	ObjectID  domain.ID `json:"object_id"`
	Kind      string    `json:"kind"`
	FileID    domain.ID `json:"file_id"`
	BackendID domain.ID `json:"storage_backend_id"`
	Key       string    `json:"storage_key"`
	Size      int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
}

// BackupReport says what a backup or restore did.
type BackupReport struct {
	Objects  int         `json:"objects"`
	Bytes    int64       `json:"bytes"`
	Skipped  int         `json:"skipped"`
	Restored int         `json:"restored"`
	Verified int         `json:"verified"`
	Excluded []domain.ID `json:"excluded_backends,omitempty"`
}

const manifestName = "files-manifest.json"

// ExportBytes writes every version and rendition (Markdown copies included) from
// every backend the backup covers into one tar stream: a manifest, then each object's
// verified plaintext under objects/<id>. The supervisor encrypts the archive. skip
// names objects an earlier backup already holds; versions never change, so their
// bytes are copied once. A backend with backed_up off, or not in files.backup_scope,
// is listed in Excluded.
func (s *Service) ExportBytes(ctx context.Context, p domain.Principal, w io.Writer, skip map[domain.ID]bool) (BackupReport, error) {
	var rep BackupReport
	if err := admin(p); err != nil {
		return rep, err
	}
	scope, err := s.settings.Get(ctx, "files.backup_scope")
	if err != nil {
		return rep, err
	}
	backends, err := s.st.backends(ctx)
	if err != nil {
		return rep, err
	}
	covered := map[domain.ID]bool{}
	for _, b := range backends {
		if b.BackedUp && inScope(scope, b) {
			covered[b.ID] = true
		} else {
			rep.Excluded = append(rep.Excluded, b.ID)
		}
	}
	var list []BackupObject
	var objs []object
	for _, b := range backends {
		if !covered[b.ID] {
			continue
		}
		placed, err := s.objectsOn(ctx, b.ID, "")
		if err != nil {
			return rep, err
		}
		for _, pl := range placed {
			if skip[pl.obj.ID] {
				rep.Skipped++
				continue
			}
			objs = append(objs, pl.obj)
			list = append(list, BackupObject{ObjectID: pl.obj.ID, Kind: pl.obj.Kind, FileID: pl.obj.FileID, BackendID: pl.loc.Backend,
				Key: pl.loc.Key, Size: pl.obj.Size, SHA256: pl.obj.SHA})
		}
	}
	tw := tar.NewWriter(w)
	raw, err := json.Marshal(list)
	if err != nil {
		return rep, err
	}
	if err := writeEntry(tw, manifestName, int64(len(raw)), strings.NewReader(string(raw))); err != nil {
		return rep, err
	}
	for _, o := range objs {
		body, err := s.open(ctx, o)
		if err != nil {
			return rep, fmt.Errorf("files: backup %s %s: %w", o.Kind, o.ID, err)
		}
		err = writeEntry(tw, "objects/"+string(o.ID), o.Size, body)
		_ = body.Close()
		if err != nil {
			return rep, fmt.Errorf("files: backup %s %s: %w", o.Kind, o.ID, err)
		}
		rep.Objects++
		rep.Bytes += o.Size
	}
	return rep, tw.Close()
}

func inScope(scope string, b domain.StorageBackend) bool {
	if scope == "" || scope == "all" {
		return true
	}
	for _, part := range strings.Split(scope, ",") {
		part = strings.TrimSpace(part)
		if part == string(b.ID) || part == b.Kind || part == b.Name {
			return true
		}
	}
	return false
}

func writeEntry(tw *tar.Writer, name string, size int64, r io.Reader) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: size, Mode: int64(fs.ModePerm & 0o600), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	n, err := io.Copy(tw, r)
	if err == nil && n != size {
		err = fmt.Errorf("wrote %d of %d bytes", n, size)
	}
	return err
}

// RestoreBytes puts every object of an ExportBytes stream back where the restored
// database says it lives, with that location's encryption, and checks each copy's
// SHA-256 on its backend against the database's file_versions or file_renditions row.
// An entry the database does not know, or whose manifest disagrees with its row, fails
// the restore before any byte of it is written. An object that already reads back correctly is left alone.
func (s *Service) RestoreBytes(ctx context.Context, p domain.Principal, r io.Reader) (BackupReport, error) {
	var rep BackupReport
	if err := admin(p); err != nil {
		return rep, err
	}
	tr := tar.NewReader(r)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != manifestName {
		return rep, errors.New("files: the backup has no file manifest")
	}
	var list []BackupObject
	if err := json.NewDecoder(tr).Decode(&list); err != nil {
		return rep, fmt.Errorf("files: manifest: %w", err)
	}
	byID := map[domain.ID]BackupObject{}
	for _, o := range list {
		if _, dup := byID[o.ObjectID]; dup {
			return rep, fmt.Errorf("files: manifest lists object %s twice", o.ObjectID)
		}
		byID[o.ObjectID] = o
	}
	// Every manifest object must appear exactly once: an archive that ends cleanly
	// before all of them, or repeats one, is incomplete and fails the restore.
	seen := make(map[domain.ID]bool, len(byID))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rep, err
		}
		id := domain.ID(strings.TrimPrefix(hdr.Name, "objects/"))
		m, ok := byID[id]
		if !ok || hdr.Size != m.Size {
			return rep, fmt.Errorf("files: backup entry %s is not in the manifest", hdr.Name)
		}
		if seen[id] {
			return rep, fmt.Errorf("files: backup entry %s appears twice", hdr.Name)
		}
		seen[id] = true
		// The restored database, not the archive, says what each object is and where it
		// lives: an entry it does not know, or whose size or SHA-256 differ, is refused.
		o, err := s.knownObject(ctx, id)
		if err != nil {
			return rep, fmt.Errorf("files: restore %s: %w", id, err)
		}
		if m.Kind != o.Kind || m.Size != o.Size || m.SHA256 != o.SHA {
			return rep, fmt.Errorf("files: restore %s: the backup's %s of %d bytes, sha256 %s, does not match the database's %s of %d bytes, sha256 %s: %w",
				id, m.Kind, m.Size, m.SHA256, o.Kind, o.Size, o.SHA, storage.ErrIntegrity)
		}
		loc, err := s.st.location(ctx, id, o.Home)
		if err != nil {
			return rep, err
		}
		b, err := s.at(ctx, loc)
		if err != nil {
			return rep, err
		}
		if storage.Check(ctx, b, loc.Key, o.Size, o.SHA) == nil {
			rep.Verified++
			continue
		}
		if raw, _, err := s.adapter(ctx, loc.Backend); err == nil {
			_ = raw.Delete(ctx, loc.Key)
		}
		h := storage.NewHashingReader(tr)
		if err := b.Put(ctx, loc.Key, h, o.Size); err != nil {
			return rep, fmt.Errorf("files: restore %s: %w", id, err)
		}
		if h.Sum() != o.SHA {
			s.dropObject(ctx, loc)
			return rep, fmt.Errorf("files: restore %s: %w", id, storage.ErrIntegrity)
		}
		if err := storage.Check(ctx, b, loc.Key, o.Size, o.SHA); err != nil {
			return rep, fmt.Errorf("files: restore %s: %w", id, err)
		}
		rep.Restored++
		rep.Bytes += o.Size
	}
	if len(seen) != len(byID) {
		for _, o := range list {
			if !seen[o.ObjectID] {
				return rep, fmt.Errorf("files: the backup ends before object %s of its manifest (%d of %d present)", o.ObjectID, len(seen), len(byID))
			}
		}
	}
	rep.Objects = len(list)
	return rep, nil
}

// knownObject reads a version or rendition row by id. An id in neither table is not
// one of this database's objects.
func (s *Service) knownObject(ctx context.Context, id domain.ID) (object, error) {
	v, err := s.st.getVersion(ctx, id)
	if err == nil {
		return versionObject(v), nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return object{}, err
	}
	r, err := s.st.getRendition(ctx, id)
	if errors.Is(err, repository.ErrNotFound) {
		return object{}, errors.New("the restored database has no version or rendition with this id")
	}
	if err != nil {
		return object{}, err
	}
	v, err = s.st.getVersion(ctx, r.FileVersionID)
	if err != nil {
		return object{}, err
	}
	return renditionObject(r, v.FileID), nil
}

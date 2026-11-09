package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultMaxImportBytes bounds an archive uploaded for a restore or fetched
// from a destination when the configuration does not set one (64 GiB).
const DefaultMaxImportBytes int64 = 64 << 30

func (s *Store) maxImport() int64 {
	if s.MaxImportBytes > 0 {
		return s.MaxImportBytes
	}
	return DefaultMaxImportBytes
}

// importSource is an encrypted archive arriving from outside the local
// volume. Sidecar is called only after Archive has been read to the end, so
// a multipart upload may send the checksum before or after the archive.
type importSource struct {
	ID      string
	Archive io.Reader
	Sidecar func() ([]byte, error)
	Origin  string
}

// Import brings one encrypted archive onto the local backup volume so the
// ordinary restore path can use it. It writes {id}.tgbk under a publication
// intent, stops at the size bound, checks the SHA-256 of the received bytes
// against the {id}.sha256 sidecar the source supplied, and decrypts it with
// the passphrase through the same manifest, id, and dump checksum checks
// that a local restore runs. Only an archive that passes all of those gets
// its sidecar and .json record published. Any failure, including a crash,
// leaves no archive behind: the intent is abandoned now or on the next
// recovery pass. An id already on the local volume is refused, never
// overwritten.
func (s *Store) Import(ctx context.Context, src importSource, passphrase string) (BackupMeta, Manifest, error) {
	if !validID(src.ID) {
		return BackupMeta{}, Manifest{}, fmt.Errorf("%w: backup name is not a backup id", ErrUnsafePath)
	}
	if passphrase == "" {
		return BackupMeta{}, Manifest{}, ErrPassphraseRequired
	}
	if src.Archive == nil || src.Sidecar == nil {
		return BackupMeta{}, Manifest{}, fmt.Errorf("archive and checksum: %w", ErrBadRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	unlock, err := s.lockScratch()
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	defer unlock()
	if err := s.recoverPlaintext(); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	id := src.ID
	for _, suffix := range []string{".json", ".tgbk", ".sha256", publicationSuffix} {
		path, err := s.contained(id, suffix)
		if err != nil {
			return BackupMeta{}, Manifest{}, err
		}
		if _, err := os.Lstat(path); err == nil {
			return BackupMeta{}, Manifest{}, ErrBackupExists
		} else if !os.IsNotExist(err) {
			return BackupMeta{}, Manifest{}, err
		}
	}
	if err := s.beginPublication(id, s.now().UTC()); err != nil {
		if os.IsExist(err) {
			return BackupMeta{}, Manifest{}, ErrBackupExists
		}
		return BackupMeta{}, Manifest{}, err
	}
	published := false
	defer func() {
		if !published {
			_ = s.abandonPublication(id)
		}
	}()
	archive := filepath.Join(filepath.Clean(s.Dir), id+".tgbk")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	limit := s.maxImport()
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(boundReader{ctx: ctx, r: src.Archive}, limit+1))
	if copyErr == nil && n > limit {
		copyErr = fmt.Errorf("%w: more than %d bytes", ErrTooLarge, limit)
	}
	if copyErr == nil {
		copyErr = s.syncRequired(f, "archive")
	}
	if cerr := f.Close(); copyErr == nil {
		copyErr = cerr
	}
	if copyErr != nil {
		var bodyLimit *http.MaxBytesError
		if errors.As(copyErr, &bodyLimit) {
			copyErr = fmt.Errorf("%w: request body over %d bytes", ErrTooLarge, bodyLimit.Limit)
		}
		return BackupMeta{}, Manifest{}, copyErr
	}
	side, err := src.Sidecar()
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	want, err := parseSidecar(side)
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != want {
		return BackupMeta{}, Manifest{}, ErrChecksum
	}
	if err := s.publishStep("archive"); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	in, err := os.Open(archive)
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	payload, cleanup, err := s.decryptLocked(in, id, passphrase)
	in.Close()
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	man := payload.Manifest
	cleanup()
	if err := s.writeSyncedFile(filepath.Join(filepath.Clean(s.Dir), id+".sha256"), []byte(sum+"\n"), "checksum"); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	if err := s.publishStep("checksum"); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	created := man.CreatedAt.UTC()
	if created.IsZero() {
		created, _ = backupTimeFromName(id + ".tgbk")
	}
	meta := BackupMeta{ID: id, CreatedAt: created, Reason: man.Reason, SHA256: sum, Bytes: n, Source: src.Origin}
	if err := s.publishStep("metadata"); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	if err := writeFileAtomic(filepath.Join(filepath.Clean(s.Dir), id+".json"), mustJSON(meta), 0o600); err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	published = true
	_ = s.finishPublication(id)
	return meta, man, nil
}

// parseSidecar accepts the .sha256 sidecar format this store writes: 64 hex
// characters, optionally followed by whitespace. Upper-case hex is folded.
func parseSidecar(raw []byte) (string, error) {
	if len(raw) > maxChecksumSidecar {
		return "", fmt.Errorf("%w: checksum sidecar is too large", ErrChecksum)
	}
	text := strings.ToLower(string(bytes.TrimSpace(raw)))
	if len(text) != 64 || strings.Trim(text, "0123456789abcdef") != "" {
		return "", fmt.Errorf("%w: checksum sidecar is not a SHA-256 hex digest", ErrChecksum)
	}
	return text, nil
}

// ImportFromDestination fetches {id}.sha256 and {id}.tgbk from this
// installation's namespace on a configured destination and imports them.
// The id is checked before any remote call. A missing object is ErrNotFound.
//
// ponytail: S3 and SFTP reads use the existing Get, which runs the whole
// transfer under destinationIOTimeout (2 minutes) and lands an SFTP copy in
// the system temp directory first, ceiling: an archive that cannot transfer
// in that time fails with a destination error before writers stop, upgrade:
// a per-read idle deadline for S3 bodies and a progress-based SFTP timeout,
// trigger: a destination restore or copy check of a real archive times out.
func (s *Store) ImportFromDestination(ctx context.Context, destID, id, passphrase string) (BackupMeta, Manifest, error) {
	if !validID(id) {
		return BackupMeta{}, Manifest{}, fmt.Errorf("%w: backup name is not a backup id", ErrUnsafePath)
	}
	dest, err := s.destination(destID)
	if err != nil {
		return BackupMeta{}, Manifest{}, err
	}
	sideName := id + ".sha256"
	rc, err := dest.Get(ctx, sideName)
	if err != nil {
		return BackupMeta{}, Manifest{}, remoteMissing(err, sideName, destID)
	}
	side, readErr := readChecksumSidecar(boundReader{ctx: ctx, r: rc})
	closeErr := rc.Close()
	if errors.Is(readErr, errSidecarOversized) {
		return BackupMeta{}, Manifest{}, fmt.Errorf("%w: %v", ErrChecksum, readErr)
	}
	if readErr != nil {
		return BackupMeta{}, Manifest{}, remoteMissing(readErr, sideName, destID)
	}
	if closeErr != nil {
		return BackupMeta{}, Manifest{}, closeErr
	}
	name := id + ".tgbk"
	archive, err := dest.Get(ctx, name)
	if err != nil {
		return BackupMeta{}, Manifest{}, remoteMissing(err, name, destID)
	}
	// A close error after the checksum and decrypt checks passed does not
	// change the bytes that were verified, so it is not returned.
	defer archive.Close()
	return s.Import(ctx, importSource{
		ID:      id,
		Archive: archive,
		Sidecar: func() ([]byte, error) { return side, nil },
		Origin:  "destination:" + destID,
	}, passphrase)
}

// remoteMissing names the object and destination on a not-found error. An
// S3 or SFTP response with no object, and a path with no file, all map here.
func remoteMissing(err error, name, destID string) error {
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %s on destination %s", ErrNotFound, name, destID)
	}
	return err
}

func (s *Store) destination(destID string) (Destination, error) {
	if s.Book == nil {
		return nil, ErrNotFound
	}
	specs, err := s.Book.Specs()
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		if spec.ID == destID {
			return s.openDest(spec)
		}
	}
	return nil, fmt.Errorf("%w: destination %s", ErrNotFound, destID)
}

// RemoteBackup is one archive found in this installation's namespace on a
// destination, with its checksum sidecar beside it.
type RemoteBackup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Local     bool      `json:"local"`
}

// DestinationBackups lists the archives a destination restore can name.
// Only canonical ids with both the archive and its sidecar are listed.
// Local is true when that id is already on the local volume, where it is
// restored by backup_id instead.
func (s *Store) DestinationBackups(ctx context.Context, destID string) ([]RemoteBackup, error) {
	dest, err := s.destination(destID)
	if err != nil {
		return nil, err
	}
	objs, err := dest.List(ctx)
	if err != nil {
		return nil, err
	}
	archives := map[string]bool{}
	sidecars := map[string]bool{}
	for _, obj := range objs {
		switch {
		case strings.HasSuffix(obj.Name, ".tgbk") && validID(strings.TrimSuffix(obj.Name, ".tgbk")):
			archives[strings.TrimSuffix(obj.Name, ".tgbk")] = true
		case strings.HasSuffix(obj.Name, ".sha256") && validID(strings.TrimSuffix(obj.Name, ".sha256")):
			sidecars[strings.TrimSuffix(obj.Name, ".sha256")] = true
		}
	}
	out := []RemoteBackup{}
	for id := range archives {
		if !sidecars[id] {
			continue
		}
		created, _ := backupTimeFromName(id + ".tgbk")
		_, lerr := s.lookup(id)
		out = append(out, RemoteBackup{ID: id, CreatedAt: created, Local: lerr == nil})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// uploadName reads the raw filename parameter of a multipart part. The
// standard FileName strips directories, which would hide a traversal
// attempt; this rejects any name that is not a bare file name.
func uploadName(part *multipart.Part) (string, error) {
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return "", fmt.Errorf("content-disposition: %w", ErrBadRequest)
	}
	name := params["filename"]
	if name == "" {
		return "", nil
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || name != filepath.Base(name) {
		return "", fmt.Errorf("%w: upload file name", ErrUnsafePath)
	}
	return name, nil
}

func uploadID(part *multipart.Part, suffix string, required bool) (string, error) {
	name, err := uploadName(part)
	if err != nil {
		return "", err
	}
	if name == "" && !required {
		return "", nil
	}
	if !strings.HasSuffix(name, suffix) || !validID(strings.TrimSuffix(name, suffix)) {
		return "", fmt.Errorf("%w: upload file name must be <backup id>%s", ErrUnsafePath, suffix)
	}
	return strings.TrimSuffix(name, suffix), nil
}

// ImportUpload reads a multipart restore upload. It takes exactly two parts:
// "archive", a file named {id}.tgbk, and "checksum", the {id}.sha256
// sidecar (its file name, when sent, must name the same id). The parts may
// come in either order. The archive streams straight to the backup volume.
// The id is returned as soon as it is known, also on failure.
func (s *Store) ImportUpload(ctx context.Context, mr *multipart.Reader, passphrase string) (string, BackupMeta, Manifest, error) {
	var side []byte
	var sideID string
	haveSide := false
	readSide := func(part *multipart.Part) error {
		if haveSide {
			return fmt.Errorf("duplicate checksum part: %w", ErrBadRequest)
		}
		id, err := uploadID(part, ".sha256", false)
		if err != nil {
			return err
		}
		body, err := readChecksumSidecar(part)
		if errors.Is(err, errSidecarOversized) {
			return fmt.Errorf("%w: %v", ErrChecksum, err)
		}
		if err != nil {
			return err
		}
		side, sideID, haveSide = body, id, true
		return nil
	}
	expectEnd := func() error {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("multipart: %w", ErrBadRequest)
		}
		part.Close()
		return fmt.Errorf("unexpected part %q: %w", part.FormName(), ErrBadRequest)
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return "", BackupMeta{}, Manifest{}, fmt.Errorf("archive part is required: %w", ErrBadRequest)
		}
		if err != nil {
			return "", BackupMeta{}, Manifest{}, fmt.Errorf("multipart: %w", ErrBadRequest)
		}
		switch part.FormName() {
		case "checksum":
			err := readSide(part)
			part.Close()
			if err != nil {
				return "", BackupMeta{}, Manifest{}, err
			}
		case "archive":
			id, err := uploadID(part, ".tgbk", true)
			if err != nil {
				return "", BackupMeta{}, Manifest{}, err
			}
			if haveSide && sideID != "" && sideID != id {
				return id, BackupMeta{}, Manifest{}, fmt.Errorf("checksum names %s and archive names %s: %w", sideID, id, ErrBadRequest)
			}
			meta, man, err := s.Import(ctx, importSource{
				ID:      id,
				Archive: part,
				Origin:  "upload",
				Sidecar: func() ([]byte, error) {
					if !haveSide {
						next, err := mr.NextPart()
						if errors.Is(err, io.EOF) {
							return nil, fmt.Errorf("checksum part is required: %w", ErrBadRequest)
						}
						if err != nil {
							return nil, fmt.Errorf("multipart: %w", ErrBadRequest)
						}
						if next.FormName() != "checksum" {
							next.Close()
							return nil, fmt.Errorf("unexpected part %q: %w", next.FormName(), ErrBadRequest)
						}
						err = readSide(next)
						next.Close()
						if err != nil {
							return nil, err
						}
						if sideID != "" && sideID != id {
							return nil, fmt.Errorf("checksum names %s and archive names %s: %w", sideID, id, ErrBadRequest)
						}
					}
					if err := expectEnd(); err != nil {
						return nil, err
					}
					return side, nil
				},
			}, passphrase)
			return id, meta, man, err
		default:
			name := part.FormName()
			part.Close()
			return "", BackupMeta{}, Manifest{}, fmt.Errorf("unexpected part %q: %w", name, ErrBadRequest)
		}
	}
}

package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	reasonNightly   = "nightly"
	reasonPreUpdate = "pre-update"
	reasonManual    = "manual"
	reasonSafety    = "safety"
)

// FileBlob is one immutable file version. Versions are stored once, keyed
// by SHA-256, because a stored version never changes.
type FileBlob struct {
	Name   string
	SHA256 string
	Open   func() (io.ReadCloser, error)
}

// BackupMeta describes an encrypted archive on the local backup volume.
// Record counts stay inside the encrypted Manifest.
type BackupMeta struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Reason            string    `json:"reason"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"`
	DestinationErrors []string  `json:"destination_errors,omitempty"`
	Deleting          bool      `json:"deleting,omitempty"`
}

// Store writes encrypted backups under Dir.
type Store struct {
	Dir        string
	Passphrase string
	Dump       func(ctx context.Context, w io.Writer) error
	Files      func(ctx context.Context) ([]FileBlob, error)
	Now        func() time.Time
	Book       *DestinationBook
	Outbox     *Outbox
	// OpenDestination, when set, replaces the real destination opener. Tests
	// use it to fail one remote delete and then retry.
	OpenDestination func(DestinationSpec) (Destination, error)
}

func (s *Store) passphrase() (string, error) {
	if s.Passphrase == "" {
		return "", ErrPassphraseRequired
	}
	return s.Passphrase, nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func validReason(reason string) bool {
	switch reason {
	case reasonNightly, reasonPreUpdate, reasonManual, reasonSafety:
		return true
	default:
		return false
	}
}

// Create dumps the database, copies file versions once, and encrypts the tar.
func (s *Store) Create(ctx context.Context, reason string) (BackupMeta, error) {
	if !validReason(reason) {
		return BackupMeta{}, fmt.Errorf("reason %q: %w", reason, ErrBadRequest)
	}
	pass, err := s.passphrase()
	if err != nil {
		return BackupMeta{}, err
	}
	if s.Dump == nil {
		return BackupMeta{}, errors.New("database dump is not configured")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return BackupMeta{}, err
	}
	now := s.now().UTC()
	id, err := newBackupID(now)
	if err != nil {
		return BackupMeta{}, err
	}
	work, err := os.MkdirTemp(s.Dir, "staging-")
	if err != nil {
		return BackupMeta{}, err
	}
	defer os.RemoveAll(work)

	dumpPath := filepath.Join(work, "db", "dump.sql")
	if err := os.MkdirAll(filepath.Dir(dumpPath), 0o700); err != nil {
		return BackupMeta{}, err
	}
	dumpFile, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return BackupMeta{}, err
	}
	if err := s.Dump(ctx, dumpFile); err != nil {
		dumpFile.Close()
		return BackupMeta{}, err
	}
	if err := dumpFile.Close(); err != nil {
		return BackupMeta{}, err
	}
	dumpHash, err := hashFile(dumpPath)
	if err != nil {
		return BackupMeta{}, err
	}

	var files []ManifestFile
	seen := map[string]struct{}{}
	if s.Files != nil {
		blobs, err := s.Files(ctx)
		if err != nil {
			return BackupMeta{}, err
		}
		for _, blob := range blobs {
			if err := ctx.Err(); err != nil {
				return BackupMeta{}, err
			}
			sum := strings.ToLower(blob.SHA256)
			if len(sum) != 64 || strings.Trim(sum, "0123456789abcdef") != "" {
				return BackupMeta{}, fmt.Errorf("file %s: %w", blob.Name, ErrBadRequest)
			}
			files = append(files, ManifestFile{Name: blob.Name, SHA256: sum})
			if _, ok := seen[sum]; ok {
				continue
			}
			seen[sum] = struct{}{}
			if err := s.storeBlob(work, sum, blob); err != nil {
				return BackupMeta{}, err
			}
		}
	}

	dumpForCounts, err := os.Open(dumpPath)
	if err != nil {
		return BackupMeta{}, err
	}
	counts, err := countsFromDump(dumpForCounts)
	dumpForCounts.Close()
	if err != nil {
		return BackupMeta{}, err
	}
	man := Manifest{
		Format:       manifestFormat,
		ID:           id,
		CreatedAt:    now,
		Reason:       reason,
		RecordCounts: counts,
		DumpSHA256:   dumpHash,
		Files:        files,
	}
	raw, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return BackupMeta{}, err
	}
	if err := os.WriteFile(filepath.Join(work, "manifest.json"), raw, 0o600); err != nil {
		return BackupMeta{}, err
	}

	archive := filepath.Join(s.Dir, id+".tgbk")
	if err := s.encryptDir(work, archive, pass); err != nil {
		os.Remove(archive)
		return BackupMeta{}, err
	}
	sum, err := hashFile(archive)
	if err != nil {
		return BackupMeta{}, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return BackupMeta{}, err
	}
	if err := os.WriteFile(filepath.Join(s.Dir, id+".sha256"), []byte(sum+"\n"), 0o600); err != nil {
		return BackupMeta{}, err
	}
	meta := BackupMeta{ID: id, CreatedAt: now, Reason: reason, SHA256: sum, Bytes: info.Size()}
	if err := writeFileAtomic(filepath.Join(s.Dir, id+".json"), mustJSON(meta), 0o600); err != nil {
		return BackupMeta{}, err
	}
	if s.Book != nil {
		meta.DestinationErrors = s.copyOut(ctx, meta)
		if len(meta.DestinationErrors) > 0 {
			_ = writeFileAtomic(filepath.Join(s.Dir, id+".json"), mustJSON(meta), 0o600)
			if s.Outbox != nil {
				_ = s.Outbox.Notify(ctx, "Backup copy failed", strings.Join(meta.DestinationErrors, "\n"))
			}
		}
	}
	return meta, nil
}

func (s *Store) storeBlob(work, sum string, blob FileBlob) error {
	if blob.Open == nil {
		return fmt.Errorf("file %s: %w", blob.Name, ErrBadRequest)
	}
	rc, err := blob.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	dest := filepath.Join(work, "files", sum)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), rc); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != sum {
		os.Remove(dest)
		return fmt.Errorf("file %s: %w", blob.Name, ErrChecksum)
	}
	return nil
}

func (s *Store) encryptDir(work, archive, pass string) error {
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = writeEncryptedTar(out, work, pass)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// writeEncryptedTar streams the tar into the encrypted writer. The pipe reader
// is closed on every exit so a failed destination cannot leave the tar
// producer blocked, which would hold the operation gate.
func writeEncryptedTar(dst io.Writer, work, pass string) error {
	pr, pw := io.Pipe()
	errc := make(chan error, 1)
	go func() {
		errc <- pw.CloseWithError(writeTar(pw, work))
	}()
	defer pr.Close()
	encErr := Encrypt(dst, pr, pass)
	_ = pr.Close()
	tarErr := <-errc
	if encErr != nil {
		return encErr
	}
	if tarErr != nil {
		return tarErr
	}
	return nil
}

func (s *Store) copyOut(ctx context.Context, meta BackupMeta) []string {
	var errs []string
	dests, err := s.Book.List()
	if err != nil {
		return []string{err.Error()}
	}
	for _, dest := range dests {
		if err := s.copyOne(ctx, dest, meta); err != nil {
			errs = append(errs, dest.ID()+": "+Scrub(err.Error()))
		}
	}
	return errs
}

func (s *Store) copyOne(ctx context.Context, dest Destination, meta BackupMeta) error {
	name := meta.ID + ".tgbk"
	f, err := os.Open(filepath.Join(s.Dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := dest.Put(ctx, name, f, meta.SHA256); err != nil {
		return err
	}
	if receipts := s.receipts(); receipts != nil {
		if err := receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: name, Created: meta.CreatedAt}); err != nil {
			return err
		}
	}
	rc, err := dest.Get(ctx, name)
	if err != nil {
		return err
	}
	defer rc.Close()
	got, err := sha256Hex(rc)
	if err != nil {
		return err
	}
	if got != meta.SHA256 {
		return ErrChecksum
	}
	side, err := os.ReadFile(filepath.Join(s.Dir, meta.ID+".sha256"))
	if err != nil {
		return err
	}
	return dest.Put(ctx, meta.ID+".sha256", bytes.NewReader(side), "")
}

// Open verifies the sidecar checksum and returns the encrypted bytes.
func (s *Store) Open(id string) (io.ReadCloser, BackupMeta, error) {
	meta, err := s.lookup(id)
	if err != nil {
		return nil, BackupMeta{}, err
	}
	path := filepath.Join(s.Dir, id+".tgbk")
	sum, err := hashFile(path)
	if err != nil {
		return nil, BackupMeta{}, err
	}
	if sum != meta.SHA256 {
		return nil, BackupMeta{}, ErrChecksum
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, BackupMeta{}, err
	}
	return f, meta, nil
}

func (s *Store) lookup(id string) (BackupMeta, error) {
	if !validID(id) {
		return BackupMeta{}, ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, id+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return BackupMeta{}, ErrNotFound
		}
		return BackupMeta{}, err
	}
	var meta BackupMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return BackupMeta{}, err
	}
	return meta, nil
}

func (s *Store) List() ([]BackupMeta, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupMeta
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".sha256") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if !validID(id) {
			continue
		}
		meta, err := s.lookup(id)
		if err != nil {
			return nil, err
		}
		out = append(out, meta)
	}
	return out, nil
}

// Delete removes every owned destination copy, including the checksum
// sidecar, and only then removes the local archive and metadata. A remote
// failure leaves the metadata in place so the same call can be retried.
// Retention pruning uses removeLocal so a longer remote retention survives.
func (s *Store) Delete(ctx context.Context, id string) error {
	meta, err := s.lookup(id)
	if err != nil {
		return err
	}
	meta.Deleting = true
	if err := writeFileAtomic(filepath.Join(s.Dir, id+".json"), mustJSON(meta), 0o600); err != nil {
		return err
	}
	if err := s.deleteRemotes(ctx, id); err != nil {
		return err
	}
	return s.removeLocal(id)
}

func (s *Store) deleteRemotes(ctx context.Context, id string) error {
	if s.Book == nil {
		return nil
	}
	specs, err := s.Book.Specs()
	if err != nil {
		return err
	}
	var failed []string
	for _, spec := range specs {
		dest, err := s.openDest(spec)
		if err != nil {
			failed = append(failed, spec.ID+": "+err.Error())
			continue
		}
		archiveErr := dest.Delete(ctx, id+".tgbk")
		sideErr := dest.Delete(ctx, id+".sha256")
		if archiveErr != nil || sideErr != nil {
			failed = append(failed, spec.ID+": "+errors.Join(archiveErr, sideErr).Error())
			continue
		}
		if receipts := s.receipts(); receipts != nil {
			_ = receipts.Remove(spec.ID, id+".tgbk")
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrDestination, strings.Join(failed, "; "))
	}
	return nil
}

func (s *Store) openDest(spec DestinationSpec) (Destination, error) {
	if s.OpenDestination != nil {
		return s.OpenDestination(spec)
	}
	return OpenDestination(spec)
}

func (s *Store) receipts() *ReceiptBook {
	if s.Book == nil || s.Book.Path == "" {
		return nil
	}
	return &ReceiptBook{Path: filepath.Join(filepath.Dir(s.Book.Path), "destination-receipts.json")}
}

func (s *Store) removeLocal(id string) error {
	if _, err := s.lookup(id); err != nil {
		return err
	}
	for _, suffix := range []string{".tgbk", ".sha256", ".json"} {
		if err := os.Remove(filepath.Join(s.Dir, id+suffix)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Prune deletes local archives older than retention. It does not touch
// destination copies; those follow PruneDestinations.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, fmt.Errorf("retention: %w", ErrBadRequest)
	}
	all, err := s.List()
	if err != nil {
		return 0, err
	}
	cutoff := s.now().Add(-retention)
	n := 0
	for _, meta := range all {
		if meta.Deleting {
			continue
		}
		if meta.CreatedAt.Before(cutoff) {
			if err := s.removeLocal(meta.ID); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// PruneDestinations applies each destination's own retention.
func (s *Store) PruneDestinations(ctx context.Context) (int, error) {
	if s.Book == nil {
		return 0, nil
	}
	specs, err := s.Book.Specs()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, spec := range specs {
		days := spec.RetentionDays
		if days == 0 {
			days = 30
		}
		dest, err := s.openDest(spec)
		if err != nil {
			return n, err
		}
		var owned []OwnedObject
		if receipts := s.receipts(); receipts != nil {
			owned, err = receipts.List(spec.ID)
			if err != nil {
				return n, err
			}
		}
		deleted, err := PruneDestination(ctx, dest, time.Duration(days)*24*time.Hour, s.now(), owned)
		if receipts := s.receipts(); receipts != nil {
			for _, name := range deleted {
				_ = receipts.Remove(spec.ID, name)
			}
		}
		removed := len(deleted)
		n += removed
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// RestorePayload is the decrypted backup. File paths are readable only until
// apply returns; the staging directory is removed after that.
type RestorePayload struct {
	Dump     io.Reader
	Manifest Manifest
	Files    []RestoredFile
}

// RestoredFile is one logical file version recovered from the archive.
type RestoredFile struct {
	Name   string
	SHA256 string
	Path   string
}

// Validate decrypts the archive and checks its manifest and file checksums.
// It does not apply the dump or stop any writer.
func (s *Store) Validate(id, passphrase string) (Manifest, error) {
	if passphrase == "" {
		var err error
		passphrase, err = s.passphrase()
		if err != nil {
			return Manifest{}, err
		}
	}
	payload, cleanup, err := s.openPlain(id, passphrase)
	if err != nil {
		return Manifest{}, err
	}
	cleanup()
	return payload.Manifest, nil
}

// Restore decrypts id and calls apply with the dump and recovered file bytes.
func (s *Store) Restore(ctx context.Context, id, passphrase string, apply func(context.Context, RestorePayload) error) error {
	if passphrase == "" {
		var err error
		passphrase, err = s.passphrase()
		if err != nil {
			return err
		}
	}
	payload, cleanup, err := s.openPlain(id, passphrase)
	if err != nil {
		return err
	}
	defer cleanup()
	return apply(ctx, payload)
}

// TestRestore decrypts a backup, asks measure for live counts from the dump,
// and compares them to the Manifest. It does not replace the primary database.
func (s *Store) TestRestore(ctx context.Context, id string, measure func(context.Context, io.Reader) (map[string]int64, error)) error {
	pass, err := s.passphrase()
	if err != nil {
		return err
	}
	payload, cleanup, err := s.openPlain(id, pass)
	if err != nil {
		return err
	}
	defer cleanup()
	got, err := measure(ctx, payload.Dump)
	if err != nil {
		return err
	}
	return compareCounts(payload.Manifest.RecordCounts, got)
}

func (s *Store) openPlain(id, passphrase string) (RestorePayload, func(), error) {
	fail := func(err error) (RestorePayload, func(), error) {
		return RestorePayload{}, func() {}, err
	}
	rc, _, err := s.Open(id)
	if err != nil {
		return fail(err)
	}
	defer rc.Close()
	work, err := os.MkdirTemp(s.Dir, "restore-")
	if err != nil {
		return fail(err)
	}
	cleanupWork := func() { os.RemoveAll(work) }
	plainPath := filepath.Join(work, "plain.tar")
	plainFile, err := os.OpenFile(plainPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		cleanupWork()
		return fail(err)
	}
	decErr := Decrypt(plainFile, rc, passphrase)
	closeErr := plainFile.Close()
	if decErr != nil {
		cleanupWork()
		return fail(decErr)
	}
	if closeErr != nil {
		cleanupWork()
		return fail(closeErr)
	}
	in, err := os.Open(plainPath)
	if err != nil {
		cleanupWork()
		return fail(err)
	}
	untarErr := untar(work, in)
	in.Close()
	os.Remove(plainPath)
	if untarErr != nil {
		cleanupWork()
		return fail(untarErr)
	}
	raw, err := os.ReadFile(filepath.Join(work, "manifest.json"))
	if err != nil {
		cleanupWork()
		return fail(ErrArchive)
	}
	var man Manifest
	if err := json.Unmarshal(raw, &man); err != nil {
		cleanupWork()
		return fail(ErrArchive)
	}
	if man.Format != manifestFormat || man.ID != id {
		cleanupWork()
		return fail(ErrArchive)
	}
	dumpPath := filepath.Join(work, "db", "dump.sql")
	sum, err := hashFile(dumpPath)
	if err != nil || sum != man.DumpSHA256 {
		cleanupWork()
		return fail(ErrChecksum)
	}
	var files []RestoredFile
	for _, file := range man.Files {
		blob := filepath.Join(work, "files", file.SHA256)
		got, err := hashFile(blob)
		if err != nil || got != file.SHA256 {
			cleanupWork()
			return fail(ErrChecksum)
		}
		files = append(files, RestoredFile{Name: file.Name, SHA256: file.SHA256, Path: blob})
	}
	f, err := os.Open(dumpPath)
	if err != nil {
		cleanupWork()
		return fail(err)
	}
	payload := RestorePayload{Dump: f, Manifest: man, Files: files}
	return payload, func() {
		f.Close()
		cleanupWork()
	}, nil
}

func compareCounts(want, got map[string]int64) error {
	if len(want) != len(got) {
		return fmt.Errorf("%w: %d Manifest tables, %d restored", ErrChecksum, len(want), len(got))
	}
	for table, n := range want {
		if got[table] != n {
			return fmt.Errorf("%w: %s Manifest %d restored %d", ErrChecksum, table, n, got[table])
		}
	}
	return nil
}

func backupTimeFromName(name string) (time.Time, bool) {
	base := name
	switch {
	case strings.HasSuffix(base, ".tgbk"):
		base = strings.TrimSuffix(base, ".tgbk")
	case strings.HasSuffix(base, ".sha256"):
		base = strings.TrimSuffix(base, ".sha256")
	default:
		return time.Time{}, false
	}
	if !validID(base) {
		return time.Time{}, false
	}
	when, err := time.Parse("20060102T150405Z", base[:len("20060102T150405Z")])
	if err != nil {
		return time.Time{}, false
	}
	return when, true
}

func validID(id string) bool {
	const stamp = len("20060102T150405Z")
	if len(id) != stamp+1+16 || id[stamp] != '-' {
		return false
	}
	for _, c := range id[stamp+1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mustJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	raw = append(raw, '\n')
	return raw
}

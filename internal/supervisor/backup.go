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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	mu              sync.Mutex
	receiptsMu      sync.Mutex
	receipts        *ReceiptBook
	// failSync, when set by tests, fails the next Sync of that file.
	// "archive" is the encrypted tar. "checksum" is its sidecar.
	// Empty means every Sync is attempted.
	failSync string
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(ctx, reason)
}

func (s *Store) createLocked(ctx context.Context, reason string) (BackupMeta, error) {
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
		os.Remove(archive)
		return BackupMeta{}, err
	}
	info, err := os.Stat(archive)
	if err != nil {
		os.Remove(archive)
		return BackupMeta{}, err
	}
	side := filepath.Join(s.Dir, id+".sha256")
	if err := s.writeSyncedFile(side, []byte(sum+"\n"), "checksum"); err != nil {
		os.Remove(side)
		os.Remove(archive)
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
	var syncErr error
	if err == nil {
		syncErr = s.syncRequired(out, "archive")
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncDir(filepath.Dir(archive))
}

// writeSyncedFile persists data before the caller may publish success.
// A write, close, or sync error is returned and the caller must not record
// the backup as complete.
func (s *Store) writeSyncedFile(path string, data []byte, kind string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := s.syncRequired(f, kind); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func (s *Store) syncRequired(f *os.File, kind string) error {
	if s != nil && s.failSync != "" && s.failSync == kind {
		s.failSync = ""
		return errors.New("backup sync failed")
	}
	if f == nil {
		return errors.New("backup file is not open")
	}
	return f.Sync()
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

// PutDestination updates a destination under the same lock as copy and
// delete. A storage move is refused while this installation still has a
// receipt for that destination, so delete and retention keep using the
// location that holds the archive.
func (s *Store) PutDestination(spec DestinationSpec) (DestinationSpec, error) {
	if s == nil || s.Book == nil {
		return DestinationSpec{}, errors.New("destinations are not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	specs, err := s.Book.Specs()
	if err != nil {
		return DestinationSpec{}, err
	}
	var previous *DestinationSpec
	for i := range specs {
		if specs[i].ID == spec.ID {
			previous = &specs[i]
			break
		}
	}
	prepared, err := prepareSpec(spec, "")
	if previous != nil {
		prepared, err = prepareSpec(spec, previous.Namespace)
	}
	if err != nil {
		return DestinationSpec{}, err
	}
	if previous != nil && !sameStorage(*previous, prepared) {
		owned, err := s.receiptBook().List(spec.ID)
		if err != nil {
			return DestinationSpec{}, err
		}
		if len(owned) > 0 {
			return DestinationSpec{}, errDestinationRelocation
		}
	}
	return s.Book.Put(prepared)
}

// DeleteDestination removes a destination under the same lock as copy and
// delete. Receipts or an in-flight upload keep the stored specification, so
// the same id cannot be added back at a different location and inherit the
// old ownership history.
func (s *Store) DeleteDestination(id string) error {
	if s == nil || s.Book == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipts := s.receiptBook(); receipts != nil {
		owned, err := receipts.List(id)
		if err != nil {
			return err
		}
		if len(owned) > 0 {
			return errDestinationRemoval
		}
	}
	if destinationBusy(id) {
		return fmt.Errorf("%w: an upload for this destination is still running", ErrRemoteUncertain)
	}
	return s.Book.Delete(id)
}

func (s *Store) copyOut(ctx context.Context, meta BackupMeta) []string {
	if s.Book == nil {
		return nil
	}
	specs, err := s.Book.Specs()
	if err != nil {
		return []string{err.Error()}
	}
	var errs []string
	for _, spec := range specs {
		dest, err := s.openDest(spec)
		if err != nil {
			errs = append(errs, spec.ID+": "+Scrub(err.Error()))
			continue
		}
		if err := s.reconcileDestination(ctx, dest); err != nil {
			errs = append(errs, dest.ID()+": "+Scrub(err.Error()))
			continue
		}
		if err := s.copyOne(ctx, dest, meta); err != nil {
			errs = append(errs, dest.ID()+": "+Scrub(err.Error()))
		}
	}
	return errs
}

func (s *Store) copyOne(ctx context.Context, dest Destination, meta BackupMeta) error {
	name := meta.ID + ".tgbk"
	path, err := s.contained(meta.ID, ".tgbk")
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	receipts := s.receiptBook()
	if receipts == nil {
		return fmt.Errorf("receipt: %w", ErrBadRequest)
	}
	if err := receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: name, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		return err
	}
	if err := dest.Put(ctx, name, f, meta.SHA256); err != nil {
		if ambiguousRemote(err) || (errors.Is(err, ErrRemoteUncertain) && !remoteBusy(remoteKey(dest.ID(), name))) {
			_ = s.markUncertain(receipts, dest.ID(), name, meta.CreatedAt)
			if errors.Is(err, ErrRemoteUncertain) {
				return err
			}
			return fmt.Errorf("%w: %s", ErrRemoteUncertain, err.Error())
		}
		if uncertainRemote(err, dest.ID(), name) {
			return err
		}
		if s.dropRemote(ctx, dest, name) {
			_ = receipts.Remove(dest.ID(), name)
		}
		return err
	}
	rc, err := dest.Get(ctx, name)
	if err != nil {
		if ambiguousRemote(err) || (errors.Is(err, ErrRemoteUncertain) && !remoteBusy(remoteKey(dest.ID(), name))) {
			_ = s.markUncertain(receipts, dest.ID(), name, meta.CreatedAt)
			if errors.Is(err, ErrRemoteUncertain) {
				return err
			}
			return fmt.Errorf("%w: %s", ErrRemoteUncertain, err.Error())
		}
		if uncertainRemote(err, dest.ID(), name) {
			return err
		}
		if s.dropRemote(ctx, dest, name) {
			_ = receipts.Remove(dest.ID(), name)
		}
		return err
	}
	got, err := sha256Hex(rc)
	closeErr := rc.Close()
	if errors.Is(err, ErrRemoteUncertain) || errors.Is(closeErr, ErrRemoteUncertain) || ambiguousRemote(err) || ambiguousRemote(closeErr) {
		_ = s.markUncertain(receipts, dest.ID(), name, meta.CreatedAt)
		return uncertainJoin(err, closeErr)
	}
	if err != nil {
		if s.dropRemote(ctx, dest, name) {
			_ = receipts.Remove(dest.ID(), name)
		}
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if got != meta.SHA256 {
		if s.dropRemote(ctx, dest, name) {
			_ = receipts.Remove(dest.ID(), name)
		}
		return ErrChecksum
	}
	// The archive acknowledgment is not a completed copy. The checksum
	// sidecar is its own mutation and needs its own receipt before the bytes
	// can be sent. Neither receipt is complete until both puts have returned.
	sideName := meta.ID + ".sha256"
	if err := receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		return err
	}
	sidePath, err := s.contained(meta.ID, ".sha256")
	if err != nil {
		return err
	}
	side, err := os.ReadFile(sidePath)
	if err != nil {
		return err
	}
	if err := dest.Put(ctx, sideName, bytes.NewReader(side), ""); err != nil {
		if ambiguousRemote(err) || (errors.Is(err, ErrRemoteUncertain) && !remoteBusy(remoteKey(dest.ID(), sideName))) {
			_ = s.markUncertain(receipts, dest.ID(), sideName, meta.CreatedAt)
			if errors.Is(err, ErrRemoteUncertain) {
				return err
			}
			return fmt.Errorf("%w: %s", ErrRemoteUncertain, err.Error())
		}
		if uncertainRemote(err, dest.ID(), sideName) {
			return err
		}
		if s.dropRemote(ctx, dest, sideName) {
			_ = receipts.Remove(dest.ID(), sideName)
		}
		return err
	}
	if err := receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: name, Created: meta.CreatedAt}); err != nil {
		return err
	}
	return receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: meta.CreatedAt})
}

func uncertainJoin(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func uncertainRemote(err error, destID, name string) bool {
	return errors.Is(err, ErrRemoteUncertain) || ambiguousRemote(err) || remoteBusy(remoteKey(destID, name))
}

// remoteStatusError is a completed remote response with an explicit status.
// The server answered, so the mutation was not a lost response.
type remoteStatusError struct {
	Status int
	Detail string
}

func (e *remoteStatusError) Error() string {
	if e == nil {
		return "remote status"
	}
	if e.Detail == "" {
		return fmt.Sprintf("remote status %d", e.Status)
	}
	return fmt.Sprintf("remote status %d: %s", e.Status, e.Detail)
}

// ambiguousRemote reports a mutating request whose response was lost.
// EOF, a wrapped EOF, an unexpected EOF, a reset, a broken pipe, and a
// timeout are lost responses even when the parent context is still live.
// A definite failure requires explicit evidence: a known sentinel, an HTTP
// status, a finished command, or a local path error. Wording inside an
// error string is not that evidence.
func ambiguousRemote(err error) bool {
	if err == nil || errors.Is(err, ErrRemoteUncertain) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrBadRequest) || errors.Is(err, ErrChecksum) {
		return false
	}
	var status *remoteStatusError
	if errors.As(err, &status) {
		return false
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return false
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return false
	}
	return true
}

// markUncertain records when the acknowledgment was lost. Created stays the
// backup's own time and is not used to decide that the server finished.
func (s *Store) markUncertain(receipts *ReceiptBook, destID, name string, created time.Time) error {
	return receipts.Add(OwnedObject{
		DestinationID: destID, Name: name, Created: created,
		State: receiptUncertain, UncertainAt: s.now(),
	})
}

// dropRemote removes one owned name and its partial sibling. It reports
// false when the deletion itself is still running or failed.
func (s *Store) dropRemote(ctx context.Context, dest Destination, name string) bool {
	if err := dest.Delete(ctx, name); err != nil {
		return false
	}
	partial, ok := dest.(interface {
		RemovePartial(context.Context, string) error
	})
	if !ok {
		return true
	}
	return partial.RemovePartial(ctx, name) == nil
}

// reconcileDestination resolves pending receipts for this destination only.
// An in-flight filesystem worker is left alone. Other names are not listed
// and are not deleted.
func (s *Store) reconcileDestination(ctx context.Context, dest Destination) error {
	receipts := s.receiptBook()
	if receipts == nil || dest == nil {
		return nil
	}
	owned, err := receipts.List(dest.ID())
	if err != nil {
		return err
	}
	// Sidecars first so an archive is not marked complete while its checksum
	// mutation is still unsettled.
	for _, pass := range []string{".sha256", ".tgbk"} {
		for _, obj := range owned {
			if !strings.HasSuffix(obj.Name, pass) {
				continue
			}
			switch obj.State {
			case receiptPending, receiptUncertain:
				if err := s.reconcilePending(ctx, dest, receipts, obj); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) reconcilePending(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	if remoteBusy(remoteKey(dest.ID(), obj.Name)) {
		return nil
	}
	archive := strings.HasSuffix(obj.Name, ".tgbk")
	sidecar := strings.HasSuffix(obj.Name, ".sha256")
	if !archive && !sidecar {
		return nil
	}
	id := strings.TrimSuffix(strings.TrimSuffix(obj.Name, ".tgbk"), ".sha256")
	localSHA := ""
	deleting := false
	if meta, err := s.lookup(id); err == nil {
		localSHA = meta.SHA256
		deleting = meta.Deleting
	}
	// A crash leaves remoteBusy false while the server may still publish.
	// Absence is not completion and is not permission to forget the intent.
	if obj.State == receiptUncertain || (obj.State == receiptPending && deleting) {
		if obj.State == receiptPending {
			_ = s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created)
		}
		return s.reconcileUncertain(ctx, dest, receipts, obj)
	}
	rc, err := dest.Get(ctx, obj.Name)
	if errors.Is(err, ErrRemoteUncertain) || ambiguousRemote(err) {
		_ = s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created)
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		_ = s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created)
		return nil
	}
	if err != nil {
		_ = s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created)
		return nil
	}
	body, hashErr := io.ReadAll(rc)
	closeErr := rc.Close()
	if errors.Is(hashErr, ErrRemoteUncertain) || errors.Is(closeErr, ErrRemoteUncertain) || ambiguousRemote(hashErr) || ambiguousRemote(closeErr) {
		_ = s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created)
		return nil
	}
	if hashErr != nil || closeErr != nil {
		return nil
	}
	if sidecar {
		sidePath, err := s.contained(id, ".sha256")
		if err != nil {
			return nil
		}
		local, err := os.ReadFile(sidePath)
		if err != nil {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(local)) {
			return receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: obj.Name, Created: obj.Created})
		}
	}
	got := hex.EncodeToString(sha256Sum(body))
	if archive && localSHA != "" && got == localSHA && !s.sidecarUnsettled(receipts, dest.ID(), id) {
		return receipts.Add(OwnedObject{DestinationID: dest.ID(), Name: obj.Name, Created: obj.Created})
	}
	if archive && localSHA != "" && got == localSHA {
		return nil
	}
	if localSHA == "" && archive {
		return nil
	}
	if s.dropRemote(ctx, dest, obj.Name) && confirmNameAbsent(ctx, dest, obj.Name) == nil {
		return receipts.Remove(dest.ID(), obj.Name)
	}
	return nil
}

func sha256Sum(body []byte) []byte {
	sum := sha256.Sum256(body)
	return sum[:]
}

// sidecarUnsettled reports a checksum mutation that has not been acknowledged.
// A missing sidecar receipt means that mutation was never started.
func (s *Store) sidecarUnsettled(receipts *ReceiptBook, destID, id string) bool {
	if receipts == nil {
		return false
	}
	owned, err := receipts.List(destID)
	if err != nil {
		return true
	}
	obj, ok := matchingReceipt(owned, destID, id+".sha256")
	if !ok {
		return false
	}
	return obj.State == receiptPending || obj.State == receiptUncertain
}

// reconcileUncertain keeps the tombstone until the late object is actually
// seen and removed. A missing read, including after the client timeout has
// elapsed, does not prove the original PUT finished.
func (s *Store) reconcileUncertain(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	done, err := s.observedUncertainCleanup(ctx, dest, obj.Name)
	if err != nil || !done {
		return nil
	}
	return receipts.Remove(dest.ID(), obj.Name)
}

// observedUncertainCleanup deletes an object only after GET shows it.
// NotFound leaves the tombstone. The return is true only when that observed
// object was then confirmed absent.
func (s *Store) observedUncertainCleanup(ctx context.Context, dest Destination, name string) (bool, error) {
	if remoteBusy(remoteKey(dest.ID(), name)) {
		return false, fmt.Errorf("%w: filesystem call still running", ErrRemoteUncertain)
	}
	rc, err := dest.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return false, fmt.Errorf("%w: upload outcome is still uncertain", ErrRemoteUncertain)
	}
	if errors.Is(err, ErrRemoteUncertain) || ambiguousRemote(err) || err != nil {
		if err == nil {
			err = ErrRemoteUncertain
		}
		return false, err
	}
	_ = rc.Close()
	if err := s.removeOwnedRemote(ctx, dest, name); err != nil {
		return false, err
	}
	if err := confirmNameAbsent(ctx, dest, name); err != nil {
		return false, err
	}
	return true, nil
}

// Open verifies the sidecar checksum and returns the encrypted bytes.
func (s *Store) Open(id string) (io.ReadCloser, BackupMeta, error) {
	meta, err := s.lookup(id)
	if err != nil {
		return nil, BackupMeta{}, err
	}
	path, err := s.contained(id, ".tgbk")
	if err != nil {
		return nil, BackupMeta{}, err
	}
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
	path, err := s.contained(id, ".json")
	if err != nil {
		return BackupMeta{}, err
	}
	raw, err := os.ReadFile(path)
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
	if meta.ID != id || !validID(meta.ID) {
		return BackupMeta{}, ErrNotFound
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteLocked(ctx, id)
}

func (s *Store) deleteLocked(ctx context.Context, id string) error {
	meta, err := s.lookup(id)
	if err != nil {
		return err
	}
	meta.Deleting = true
	path, err := s.contained(id, ".json")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, mustJSON(meta), 0o600); err != nil {
		return err
	}
	if err := s.deleteRemotes(ctx, id); err != nil {
		return err
	}
	return s.removeLocal(id)
}

func matchingReceipt(owned []OwnedObject, destID, name string) (OwnedObject, bool) {
	for _, obj := range owned {
		if obj.Name == name && (obj.DestinationID == "" || obj.DestinationID == destID) {
			return obj, true
		}
	}
	return OwnedObject{}, false
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
	names := []string{id + ".tgbk", id + ".sha256"}
	receipts := s.receiptBook()
	for _, spec := range specs {
		var owned []OwnedObject
		if receipts != nil {
			var rerr error
			owned, rerr = receipts.List(spec.ID)
			if rerr != nil {
				return rerr
			}
		}
		var matched []OwnedObject
		for _, name := range names {
			if obj, ok := matchingReceipt(owned, spec.ID, name); ok {
				matched = append(matched, obj)
			}
		}
		if len(matched) == 0 {
			continue
		}
		dest, err := s.openDest(spec)
		if err != nil {
			failed = append(failed, spec.ID+": "+err.Error())
			continue
		}
		blocked := false
		for _, obj := range matched {
			if remoteBusy(remoteKey(spec.ID, obj.Name)) {
				failed = append(failed, spec.ID+": filesystem call still running")
				blocked = true
				continue
			}
			if obj.State != receiptPending && obj.State != receiptUncertain {
				continue
			}
			// Pending after the worker is gone is the same unknown as a lost ack.
			if obj.State == receiptPending {
				_ = s.markUncertain(receipts, spec.ID, obj.Name, obj.Created)
			}
			done, uerr := s.observedUncertainCleanup(ctx, dest, obj.Name)
			if uerr != nil || !done {
				if uerr == nil {
					uerr = fmt.Errorf("%w: upload outcome is still uncertain", ErrRemoteUncertain)
				}
				failed = append(failed, spec.ID+": "+uerr.Error())
				blocked = true
				continue
			}
			if receipts != nil {
				_ = receipts.Remove(spec.ID, obj.Name)
			}
		}
		if blocked {
			continue
		}
		if receipts != nil {
			owned, err = receipts.List(spec.ID)
			if err != nil {
				return err
			}
		}
		for _, name := range names {
			obj, ok := matchingReceipt(owned, spec.ID, name)
			if !ok || obj.State == receiptPending || obj.State == receiptUncertain {
				continue
			}
			if err := s.removeOwnedRemote(ctx, dest, name); err != nil {
				failed = append(failed, spec.ID+": "+err.Error())
				continue
			}
			if receipts != nil {
				_ = receipts.Remove(spec.ID, name)
			}
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrDestination, strings.Join(failed, "; "))
	}
	return nil
}

// removeOwnedRemote deletes one owned archive, its sidecar, and its partial
// sibling. It returns an error until the final name and the partial name are
// both confirmed absent. A late writer still registered under that object
// keeps the receipt.
func (s *Store) removeOwnedRemote(ctx context.Context, dest Destination, name string) error {
	if remoteBusy(remoteKey(dest.ID(), name)) {
		return fmt.Errorf("%w: filesystem call still running", ErrRemoteUncertain)
	}
	if err := dest.Delete(ctx, name); err != nil {
		return err
	}
	if strings.HasSuffix(name, ".tgbk") {
		side := strings.TrimSuffix(name, ".tgbk") + ".sha256"
		if !s.sidecarTracked(dest.ID(), side) {
			if err := dest.Delete(ctx, side); err != nil {
				return err
			}
		}
	}
	if partial, ok := dest.(interface {
		RemovePartial(context.Context, string) error
	}); ok {
		if err := partial.RemovePartial(ctx, name); err != nil {
			return err
		}
		if err := confirmNameAbsent(ctx, dest, name+".partial"); err != nil {
			return err
		}
	}
	return confirmNameAbsent(ctx, dest, name)
}

func (s *Store) sidecarTracked(destID, name string) bool {
	receipts := s.receiptBook()
	if receipts == nil {
		return false
	}
	owned, err := receipts.List(destID)
	if err != nil {
		return true
	}
	_, ok := matchingReceipt(owned, destID, name)
	return ok
}

func confirmNameAbsent(ctx context.Context, dest Destination, name string) error {
	rc, err := dest.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = rc.Close()
	return fmt.Errorf("%w: %s still present", ErrRemoteUncertain, name)
}

func (s *Store) openDest(spec DestinationSpec) (Destination, error) {
	if s.OpenDestination != nil {
		return s.OpenDestination(spec)
	}
	return OpenDestination(spec)
}

func (s *Store) receiptBook() *ReceiptBook {
	if s.Book == nil || s.Book.Path == "" {
		return nil
	}
	s.receiptsMu.Lock()
	defer s.receiptsMu.Unlock()
	if s.receipts == nil {
		s.receipts = &ReceiptBook{Path: filepath.Join(filepath.Dir(s.Book.Path), "destination-receipts.json")}
	}
	return s.receipts
}

func (s *Store) removeLocal(id string) error {
	if _, err := s.lookup(id); err != nil {
		return err
	}
	for _, suffix := range []string{".tgbk", ".sha256", ".json"} {
		path, err := s.contained(id, suffix)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Prune deletes local archives older than retention. It does not touch
// destination copies; those follow PruneDestinations.
func (s *Store) Prune(ctx context.Context, retention time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked(ctx, retention)
}

func (s *Store) pruneLocked(ctx context.Context, retention time.Duration) (int, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneDestinationsLocked(ctx)
}

func (s *Store) pruneDestinationsLocked(ctx context.Context) (int, error) {
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
		if err := s.reconcileDestination(ctx, dest); err != nil {
			return n, err
		}
		var owned []OwnedObject
		book := s.receiptBook()
		if book != nil {
			owned, err = book.List(spec.ID)
			if err != nil {
				return n, err
			}
		}
		var stable []OwnedObject
		for _, obj := range owned {
			if obj.State == receiptPending || obj.State == receiptUncertain {
				continue
			}
			if strings.HasSuffix(obj.Name, ".tgbk") && s.sidecarUnsettled(book, dest.ID(), strings.TrimSuffix(obj.Name, ".tgbk")) {
				continue
			}
			stable = append(stable, obj)
		}
		deleted, err := PruneDestination(ctx, dest, time.Duration(days)*24*time.Hour, s.now(), stable)
		if book != nil {
			for _, name := range deleted {
				_ = book.Remove(spec.ID, name)
				if strings.HasSuffix(name, ".tgbk") {
					_ = book.Remove(spec.ID, strings.TrimSuffix(name, ".tgbk")+".sha256")
				}
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
	if want == nil {
		want = map[string]int64{}
	}
	if got == nil {
		got = map[string]int64{}
	}
	for table, n := range want {
		gotN, ok := got[table]
		if !ok {
			return fmt.Errorf("%w: %s missing from restored counts", ErrChecksum, table)
		}
		if gotN != n {
			return fmt.Errorf("%w: %s Manifest %d restored %d", ErrChecksum, table, n, gotN)
		}
	}
	for table := range got {
		if _, ok := want[table]; !ok {
			return fmt.Errorf("%w: unexpected restored table %s", ErrChecksum, table)
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
	const layout = "20060102T150405Z"
	if len(id) != len(layout)+1+16 || id[len(layout)] != '-' {
		return false
	}
	for i, c := range id[:len(layout)] {
		switch i {
		case 8:
			if c != 'T' {
				return false
			}
		case 15:
			if c != 'Z' {
				return false
			}
		default:
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	if _, err := time.Parse(layout, id[:len(layout)]); err != nil {
		return false
	}
	for _, c := range id[len(layout)+1:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// contained is the only path a backup id may name. The id must already be
// the canonical timestamp-and-hex form, and the cleaned path must stay
// inside the store directory.
func (s *Store) contained(id, suffix string) (string, error) {
	if s == nil || !validID(id) || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", ErrNotFound
	}
	name := id + suffix
	if name != filepath.Base(name) {
		return "", ErrNotFound
	}
	dir := filepath.Clean(s.Dir)
	path := filepath.Join(dir, name)
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrNotFound
	}
	return path, nil
}

func mustJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	raw = append(raw, '\n')
	return raw
}

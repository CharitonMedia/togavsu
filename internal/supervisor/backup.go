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
	// Scratch is the non-persistent directory for plaintext dumps and extracted
	// archives. Empty uses a private directory under the system temp dir.
	// It must not be the backup volume or a directory inside it.
	Scratch string
	// OpenDestination, when set, replaces the real destination opener. Tests
	// use it to fail one remote delete and then retry.
	OpenDestination func(DestinationSpec) (Destination, error)
	mu              sync.Mutex
	receiptsMu      sync.Mutex
	receipts        *ReceiptBook
	scratchMu       sync.Mutex
	scratchActive   map[string]struct{}
	// failSync, when set by tests, fails the next Sync of that file.
	// "archive" is the encrypted tar. "checksum" is its sidecar.
	// Empty means every Sync is attempted.
	failSync string
	// failReceiptRemove, when set by tests, fails the next receipt removal
	// after a cleanup delete has already been recorded.
	failReceiptRemove func() error
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
	unlock, err := s.lockScratch()
	if err != nil {
		return BackupMeta{}, err
	}
	defer unlock()
	if err := s.recoverPlaintext(); err != nil {
		return BackupMeta{}, err
	}
	now := s.now().UTC()
	id, err := newBackupID(now)
	if err != nil {
		return BackupMeta{}, err
	}
	work, cleanup, err := s.plaintextWorkspace("staging-")
	if err != nil {
		return BackupMeta{}, err
	}
	defer cleanup()

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
	if err := s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: name, Created: meta.CreatedAt, State: receiptPending, Digest: meta.SHA256}); err != nil {
		return err
	}
	if err := dest.Put(ctx, name, f, meta.SHA256); err != nil {
		return s.recordMutation(ctx, dest, receipts, name, meta.CreatedAt, err)
	}
	rc, err := dest.Get(ctx, name)
	if err != nil {
		return s.recordMutation(ctx, dest, receipts, name, meta.CreatedAt, err)
	}
	got, err := hashBounded(ctx, rc)
	closeErr := rc.Close()
	if errors.Is(err, ErrRemoteUncertain) || errors.Is(closeErr, ErrRemoteUncertain) || ambiguousRemote(err) || ambiguousRemote(closeErr) {
		if merr := s.markUncertain(receipts, dest.ID(), name, meta.CreatedAt); merr != nil {
			return merr
		}
		return uncertainJoin(err, closeErr)
	}
	if err != nil {
		return s.recordMutation(ctx, dest, receipts, name, meta.CreatedAt, err)
	}
	if closeErr != nil {
		return s.recordMutation(ctx, dest, receipts, name, meta.CreatedAt, closeErr)
	}
	if got != meta.SHA256 {
		return s.recordMutation(ctx, dest, receipts, name, meta.CreatedAt, ErrChecksum)
	}
	// The archive acknowledgment is not a completed copy. The checksum
	// sidecar is its own mutation and needs its own receipt before the bytes
	// can be sent. Neither receipt is complete until both puts have returned.
	sideName := meta.ID + ".sha256"
	if err := s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: meta.CreatedAt, State: receiptPending, Digest: meta.SHA256}); err != nil {
		return err
	}
	sidePath, err := s.contained(meta.ID, ".sha256")
	if err != nil {
		return err
	}
	side, err := readFileLimited(sidePath, maxChecksumSidecar)
	if err != nil {
		return err
	}
	if err := dest.Put(ctx, sideName, bytes.NewReader(side), ""); err != nil {
		return s.recordMutation(ctx, dest, receipts, sideName, meta.CreatedAt, err)
	}
	if err := s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: name, Created: meta.CreatedAt, Digest: meta.SHA256}); err != nil {
		return err
	}
	return s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: meta.CreatedAt, Digest: meta.SHA256})
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
// A 4xx other than 408 refuses the write. A 408 or 5xx does not: a gateway
// can return 504 while the storage write is still running.
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

// ambiguousRemote reports a mutating request whose outcome is not known.
// EOF, a wrapped EOF, an unexpected EOF, a reset, a broken pipe, a timeout,
// and an HTTP 408 or 5xx do not prove the write stopped. A definite failure
// is a known sentinel, an explicit 4xx other than 408, a local path error,
// or a raw local errno from an open that never published a name. Wording
// inside an error string is not that evidence.
func ambiguousRemote(err error) bool {
	if err == nil || errors.Is(err, ErrRemoteUncertain) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrBadRequest) || errors.Is(err, ErrChecksum) {
		return false
	}
	var status *remoteStatusError
	if errors.As(err, &status) {
		if status == nil {
			return false
		}
		return ambiguousHTTPStatus(status.Status)
	}
	// An SFTP client exit, including a group kill or a dropped connection,
	// does not prove the remote write was rejected. A raw network errno is
	// the same, including when a path error wraps it.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// A local refusal, wrapped or raw, happened before a remote name existed.
	// syscall.ENOENT from a missing parent is this case. It is not a lost ack.
	if localFilesystemRefusal(err) {
		return false
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

// localFilesystemRefusal is a completed local filesystem error. The operation
// did not publish a destination name. Network and timeout errnos are absent
// so a lost server response is not reclassified as a rejection.
func localFilesystemRefusal(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ENOENT, syscall.ENOTDIR, syscall.EACCES, syscall.EPERM,
		syscall.EROFS, syscall.ENAMETOOLONG, syscall.EINVAL, syscall.ELOOP,
		syscall.EISDIR, syscall.ENOSPC, syscall.EDQUOT, syscall.ENODEV,
		syscall.ENXIO, syscall.EEXIST, syscall.ENOTSUP,
		syscall.EFBIG, syscall.EMLINK, syscall.ENOTEMPTY, syscall.EXDEV,
		syscall.EMFILE, syscall.ENFILE, syscall.EBADF, syscall.EFAULT,
		syscall.EOVERFLOW:
		return true
	default:
		return false
	}
}

// beforePublication leaves timeouts and lost client exits unchanged. A missing
// path or a local refusal that the client reported before publication becomes
// a definite absence, so a later repair can drop the receipt.
func beforePublication(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, ErrRemoteUncertain) {
		return err
	}
	if !ambiguousRemote(err) {
		return err
	}
	if remoteObjectAbsent(err) || localFilesystemRefusal(err) {
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	}
	return err
}

// ambiguousHTTPStatus is a response that can arrive before the storage write
// finishes. 408 and every 5xx are in that set. A 403 or other 4xx is a refusal.
func ambiguousHTTPStatus(code int) bool {
	return code == 408 || code >= 500
}

// markUncertain records when the acknowledgment was lost. Created stays the
// backup's own time and is not used to decide that the server finished.
func (s *Store) markUncertain(receipts *ReceiptBook, destID, name string, created time.Time) error {
	return s.noteReceipt(receipts, OwnedObject{
		DestinationID: destID, Name: name, Created: created,
		State: receiptUncertain, UncertainAt: s.now(),
	})
}

// markRejected records that the mutation itself was explicitly refused.
// A later read error is not this state. The receipt is written before any
// cleanup delete. A rejected receipt is never turned into an uncertain one.
func (s *Store) markRejected(receipts *ReceiptBook, destID, name string, created time.Time) error {
	return s.noteReceipt(receipts, OwnedObject{
		DestinationID: destID, Name: name, Created: created,
		State: receiptRejected,
	})
}

// markObserved records that the remote object was seen and will be deleted.
// The record is written before that delete, so a crash after the delete can
// still finish when a later read shows the name is gone.
func (s *Store) markObserved(receipts *ReceiptBook, destID, name string, created time.Time) error {
	return s.noteReceipt(receipts, OwnedObject{
		DestinationID: destID, Name: name, Created: created,
		State: receiptObserved,
	})
}

func (s *Store) noteReceipt(receipts *ReceiptBook, obj OwnedObject) error {
	if receipts == nil {
		return fmt.Errorf("receipt: %w", ErrBadRequest)
	}
	if obj.Digest == "" {
		obj.Digest = s.knownDigest(receipts, obj.DestinationID, obj.Name)
	}
	return receipts.Add(obj)
}

func (s *Store) knownDigest(receipts *ReceiptBook, destID, name string) string {
	if receipts != nil {
		if owned, err := receipts.List(destID); err == nil {
			if obj, ok := matchingReceipt(owned, destID, name); ok && obj.Digest != "" {
				return obj.Digest
			}
			id := strings.TrimSuffix(strings.TrimSuffix(name, ".tgbk"), ".sha256")
			if obj, ok := matchingReceipt(owned, destID, id+".tgbk"); ok && obj.Digest != "" {
				return obj.Digest
			}
		}
	}
	id := strings.TrimSuffix(strings.TrimSuffix(name, ".tgbk"), ".sha256")
	if meta, err := s.lookup(id); err == nil {
		return meta.SHA256
	}
	return ""
}

func (s *Store) removeReceipt(receipts *ReceiptBook, destID, name string) error {
	if s != nil && s.failReceiptRemove != nil {
		if err := s.failReceiptRemove(); err != nil {
			return err
		}
	}
	if receipts == nil {
		return nil
	}
	return receipts.Remove(destID, name)
}

// definiteRejection is an explicit remote or local failure. A lost response,
// a timeout, and an HTTP 408 or 5xx are not rejections. Delete plus a missing
// read must not drop that receipt, because the write can still publish.
func definiteRejection(err error) bool {
	if err == nil || errors.Is(err, ErrRemoteUncertain) {
		return false
	}
	if errors.Is(err, errSidecarOversized) {
		return true
	}
	return !ambiguousRemote(err)
}

// recordMutation persists the outcome before it tries to clean up. A definite
// rejection stays rejected when the cleanup delete fails, so a later 404 can
// drop the receipt without waiting for an object that was never stored. A
// lost response stays uncertain instead.
func (s *Store) recordMutation(ctx context.Context, dest Destination, receipts *ReceiptBook, name string, created time.Time, err error) error {
	if err == nil {
		return nil
	}
	if remoteBusy(remoteKey(dest.ID(), name)) && !definiteRejection(err) {
		return err
	}
	if !definiteRejection(err) {
		if merr := s.markUncertain(receipts, dest.ID(), name, created); merr != nil {
			return merr
		}
		if errors.Is(err, ErrRemoteUncertain) {
			return err
		}
		return fmt.Errorf("%w: %s", ErrRemoteUncertain, err.Error())
	}
	if merr := s.markRejected(receipts, dest.ID(), name, created); merr != nil {
		return merr
	}
	if s.dropRemote(ctx, dest, name) && confirmNameAbsent(ctx, dest, name) == nil {
		if rerr := s.removeReceipt(receipts, dest.ID(), name); rerr != nil {
			return rerr
		}
	}
	return err
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
			case receiptPending, receiptUncertain, receiptRejected, receiptObserved:
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
	if localSHA == "" {
		localSHA = obj.Digest
	}
	// A crash leaves remoteBusy false while the server may still publish.
	// Absence is not completion and is not permission to forget the intent.
	// A rejected mutation is different: the server already refused it.
	if obj.State == receiptRejected {
		return s.reconcileRejected(ctx, dest, receipts, obj)
	}
	if obj.State == receiptObserved {
		return s.finishObserved(ctx, dest, receipts, obj)
	}
	if obj.State == receiptUncertain || (obj.State == receiptPending && deleting) {
		if obj.State == receiptPending {
			if err := s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created); err != nil {
				return err
			}
		}
		return s.reconcileUncertain(ctx, dest, receipts, obj)
	}
	rc, err := dest.Get(ctx, obj.Name)
	if errors.Is(err, ErrRemoteUncertain) || ambiguousRemote(err) || errors.Is(err, ErrNotFound) || err != nil {
		// A failed or missing read does not prove the original PUT was rejected.
		if merr := s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created); merr != nil {
			return merr
		}
		return nil
	}
	if sidecar {
		body, readErr := readChecksumSidecar(boundReader{ctx: ctx, r: rc})
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			joined := uncertainJoin(readErr, closeErr)
			if errors.Is(joined, errSidecarOversized) {
				return s.beginObservedCleanup(ctx, dest, receipts, obj)
			}
			if merr := s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created); merr != nil {
				return merr
			}
			return nil
		}
		local, err := s.expectedSidecar(receipts, dest.ID(), id)
		if err != nil {
			return nil
		}
		if bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(local)) {
			return s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: obj.Name, Created: obj.Created, Digest: obj.Digest})
		}
		return s.beginObservedCleanup(ctx, dest, receipts, obj)
	}
	got, hashErr := hashBounded(ctx, rc)
	closeErr := rc.Close()
	if errors.Is(hashErr, ErrRemoteUncertain) || errors.Is(closeErr, ErrRemoteUncertain) || ambiguousRemote(hashErr) || ambiguousRemote(closeErr) {
		if merr := s.markUncertain(receipts, dest.ID(), obj.Name, obj.Created); merr != nil {
			return merr
		}
		return nil
	}
	if hashErr != nil || closeErr != nil {
		return nil
	}
	if archive && localSHA != "" && got == localSHA {
		if deleting {
			return s.beginObservedCleanup(ctx, dest, receipts, obj)
		}
		// A missing sidecar receipt is not a completed copy. Recovery writes
		// that intent and uploads the checksum before the archive can complete.
		if err := s.ensureValidSidecar(ctx, dest, receipts, id, obj.Created); err != nil {
			return nil
		}
		if !s.sidecarConfirmed(receipts, dest.ID(), id) {
			return nil
		}
		return s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: obj.Name, Created: obj.Created, Digest: obj.Digest})
	}
	if localSHA == "" && archive {
		return nil
	}
	return s.beginObservedCleanup(ctx, dest, receipts, obj)
}

// reconcileRejected finishes a mutation the server explicitly refused.
// Confirmed absence removes the receipt. The missing object does not become
// an uncertain upload, because this PUT was not accepted.
func (s *Store) reconcileRejected(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	if remoteBusy(remoteKey(dest.ID(), obj.Name)) {
		return nil
	}
	rc, err := dest.Get(ctx, obj.Name)
	if errors.Is(err, ErrNotFound) {
		if err := dest.Delete(ctx, obj.Name); err != nil {
			return nil
		}
		return receipts.Remove(dest.ID(), obj.Name)
	}
	if err != nil {
		return nil
	}
	_ = rc.Close()
	if s.dropRemote(ctx, dest, obj.Name) && confirmNameAbsent(ctx, dest, obj.Name) == nil {
		return receipts.Remove(dest.ID(), obj.Name)
	}
	return nil
}

var errSidecarOversized = errors.New("checksum sidecar is too large")

// boundReader stops a stream when ctx ends. It does not buffer the archive.
type boundReader struct {
	ctx context.Context
	r   io.Reader
}

func (b boundReader) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.r.Read(p)
}

func hashBounded(ctx context.Context, r io.Reader) (string, error) {
	hashCtx, cancel := destinationContext(ctx)
	defer cancel()
	return sha256Hex(boundReader{ctx: hashCtx, r: r})
}

func readChecksumSidecar(r io.Reader) ([]byte, error) {
	buf := make([]byte, maxChecksumSidecar+1)
	n, err := io.ReadFull(r, buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return buf[:n], nil
	}
	if err == nil {
		return nil, errSidecarOversized
	}
	if n > maxChecksumSidecar {
		return nil, errSidecarOversized
	}
	return buf[:n], err
}

func readFileLimited(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit+1)
	n, err := io.ReadFull(f, buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		return buf[:n], nil
	}
	if err == nil || n > limit {
		return nil, errSidecarOversized
	}
	return buf[:n], err
}

func (s *Store) sidecarPath(id string) string {
	path, err := s.contained(id, ".sha256")
	if err != nil {
		return ""
	}
	return path
}

// sidecarUnsettled reports a checksum receipt that is still open. A missing
// receipt does not make a pending archive complete; sidecarConfirmed requires
// the receipt. Retention of an already complete archive does not invent one.
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
	return obj.State == receiptPending || obj.State == receiptUncertain || obj.State == receiptRejected
}

// sidecarConfirmed is true only when the checksum receipt exists and the
// sidecar upload was acknowledged. A missing receipt is not confirmation.
func (s *Store) sidecarConfirmed(receipts *ReceiptBook, destID, id string) bool {
	if receipts == nil {
		return false
	}
	owned, err := receipts.List(destID)
	if err != nil {
		return false
	}
	obj, ok := matchingReceipt(owned, destID, id+".sha256")
	return ok && obj.State == ""
}

// ensureValidSidecar writes a sidecar intent when the archive was acknowledged
// and the checksum receipt is missing, then uploads and checks that sidecar
// before the archive copy may be called complete.
func (s *Store) ensureValidSidecar(ctx context.Context, dest Destination, receipts *ReceiptBook, id string, created time.Time) error {
	sideName := id + ".sha256"
	owned, err := receipts.List(dest.ID())
	if err != nil {
		return err
	}
	obj, ok := matchingReceipt(owned, dest.ID(), sideName)
	if ok && obj.State == receiptUncertain {
		return nil
	}
	if ok && obj.State == "" {
		return s.sidecarMatches(ctx, dest, id)
	}
	if ok && obj.State == receiptRejected {
		if err := s.reconcileRejected(ctx, dest, receipts, obj); err != nil {
			return err
		}
		owned, err = receipts.List(dest.ID())
		if err != nil {
			return err
		}
		if _, still := matchingReceipt(owned, dest.ID(), sideName); still {
			return nil
		}
		ok = false
	}
	if !ok {
		if err := s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: created, State: receiptPending, Digest: s.knownDigest(receipts, dest.ID(), id+".tgbk")}); err != nil {
			return err
		}
	}
	local, err := s.expectedSidecar(receipts, dest.ID(), id)
	if err != nil {
		return err
	}
	if err := dest.Put(ctx, sideName, bytes.NewReader(local), ""); err != nil {
		return s.recordMutation(ctx, dest, receipts, sideName, created, err)
	}
	if err := s.sidecarMatches(ctx, dest, id); err != nil {
		return s.recordMutation(ctx, dest, receipts, sideName, created, err)
	}
	return s.noteReceipt(receipts, OwnedObject{DestinationID: dest.ID(), Name: sideName, Created: created, Digest: s.knownDigest(receipts, dest.ID(), id+".tgbk")})
}

func (s *Store) expectedSidecar(receipts *ReceiptBook, destID, id string) ([]byte, error) {
	if local, err := readFileLimited(s.sidecarPath(id), maxChecksumSidecar); err == nil {
		return local, nil
	}
	digest := s.knownDigest(receipts, destID, id+".tgbk")
	if digest == "" {
		return nil, os.ErrNotExist
	}
	return []byte(digest), nil
}

func (s *Store) sidecarMatches(ctx context.Context, dest Destination, id string) error {
	rc, err := dest.Get(ctx, id+".sha256")
	if err != nil {
		return err
	}
	body, readErr := readChecksumSidecar(boundReader{ctx: ctx, r: rc})
	closeErr := rc.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	local, err := s.expectedSidecar(s.receiptBook(), dest.ID(), id)
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(local)) {
		return ErrChecksum
	}
	return nil
}

// reconcileUncertain keeps the tombstone until the late object is actually
// seen and removed. A missing read, including after the client timeout has
// elapsed, does not prove the original PUT finished.
func (s *Store) reconcileUncertain(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	if obj.State == receiptObserved {
		return s.finishObserved(ctx, dest, receipts, obj)
	}
	done, err := s.observedUncertainCleanup(ctx, dest, receipts, obj)
	if err != nil || !done {
		return nil
	}
	return s.removeReceipt(receipts, dest.ID(), obj.Name)
}

// beginObservedCleanup records that the object was seen before deleting it.
func (s *Store) beginObservedCleanup(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	if err := s.markObserved(receipts, dest.ID(), obj.Name, obj.Created); err != nil {
		return nil
	}
	obj.State = receiptObserved
	return s.finishObserved(ctx, dest, receipts, obj)
}

// finishObserved completes a delete that was already decided. A missing name
// is acknowledged only after Delete syncs the containing directory. An
// unobserved upload does not use this path.
func (s *Store) finishObserved(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) error {
	if remoteBusy(remoteKey(dest.ID(), obj.Name)) {
		return nil
	}
	rc, err := dest.Get(ctx, obj.Name)
	if errors.Is(err, ErrNotFound) {
		// A previous unlink can already have removed the name. That absence
		// is not acknowledged until the containing directory is synced.
		if err := dest.Delete(ctx, obj.Name); err != nil {
			return nil
		}
		return s.removeReceipt(receipts, dest.ID(), obj.Name)
	}
	if err != nil {
		return nil
	}
	_ = rc.Close()
	if s.dropRemote(ctx, dest, obj.Name) && confirmNameAbsent(ctx, dest, obj.Name) == nil {
		return s.removeReceipt(receipts, dest.ID(), obj.Name)
	}
	return nil
}

// observedUncertainCleanup deletes an object only after GET shows it.
// The observed state is stored before that delete. NotFound on an
// unobserved upload leaves the tombstone.
func (s *Store) observedUncertainCleanup(ctx context.Context, dest Destination, receipts *ReceiptBook, obj OwnedObject) (bool, error) {
	if remoteBusy(remoteKey(dest.ID(), obj.Name)) {
		return false, fmt.Errorf("%w: filesystem call still running", ErrRemoteUncertain)
	}
	rc, err := dest.Get(ctx, obj.Name)
	if errors.Is(err, ErrNotFound) {
		return false, fmt.Errorf("%w: upload outcome is still uncertain", ErrRemoteUncertain)
	}
	if err != nil {
		return false, err
	}
	_ = rc.Close()
	if err := s.markObserved(receipts, dest.ID(), obj.Name, obj.Created); err != nil {
		return false, err
	}
	if err := s.removeOwnedRemote(ctx, dest, obj.Name); err != nil {
		return false, err
	}
	if err := confirmNameAbsent(ctx, dest, obj.Name); err != nil {
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
			if obj.State == receiptRejected {
				if err := s.reconcileRejected(ctx, dest, receipts, obj); err != nil {
					return err
				}
				if receipts != nil {
					still, lerr := receipts.List(spec.ID)
					if lerr != nil {
						return lerr
					}
					if _, ok := matchingReceipt(still, spec.ID, obj.Name); ok {
						failed = append(failed, spec.ID+": rejected upload is still recorded")
						blocked = true
					}
				}
				continue
			}
			if obj.State == receiptObserved {
				_ = s.finishObserved(ctx, dest, receipts, obj)
				if receipts != nil {
					still, lerr := receipts.List(spec.ID)
					if lerr != nil {
						return lerr
					}
					if _, ok := matchingReceipt(still, spec.ID, obj.Name); ok {
						failed = append(failed, spec.ID+": observed upload is not confirmed gone")
						blocked = true
					}
				}
				continue
			}
			if obj.State != receiptPending && obj.State != receiptUncertain {
				continue
			}
			// Pending after the worker is gone is the same unknown as a lost ack.
			if obj.State == receiptPending {
				if err := s.markUncertain(receipts, spec.ID, obj.Name, obj.Created); err != nil {
					failed = append(failed, spec.ID+": "+err.Error())
					blocked = true
					continue
				}
			}
			done, uerr := s.observedUncertainCleanup(ctx, dest, receipts, obj)
			if uerr != nil || !done {
				if uerr == nil {
					uerr = fmt.Errorf("%w: upload outcome is still uncertain", ErrRemoteUncertain)
				}
				failed = append(failed, spec.ID+": "+uerr.Error())
				blocked = true
				continue
			}
			if err := s.removeReceipt(receipts, spec.ID, obj.Name); err != nil {
				failed = append(failed, spec.ID+": receipt removal failed")
				blocked = true
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
			if !ok || obj.State == receiptPending || obj.State == receiptUncertain || obj.State == receiptRejected || obj.State == receiptObserved {
				continue
			}
			if err := s.removeOwnedRemote(ctx, dest, name); err != nil {
				failed = append(failed, spec.ID+": "+err.Error())
				continue
			}
			if err := s.removeReceipt(receipts, spec.ID, name); err != nil {
				failed = append(failed, spec.ID+": "+err.Error())
				continue
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
	if err := s.RecoverPlaintext(); err != nil {
		return 0, err
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
			if s.remoteRecoveryOpen(meta.ID) {
				continue
			}
			if err := s.removeLocal(meta.ID); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// remoteRecoveryOpen reports a destination receipt that still needs the local
// archive, metadata, or checksum. Local retention leaves those files in place.
func (s *Store) remoteRecoveryOpen(id string) bool {
	book := s.receiptBook()
	if book == nil {
		return false
	}
	all, err := book.List("")
	if err != nil {
		return true
	}
	for _, obj := range all {
		base := strings.TrimSuffix(strings.TrimSuffix(obj.Name, ".tgbk"), ".sha256")
		if base != id {
			continue
		}
		switch obj.State {
		case receiptPending, receiptUncertain, receiptRejected, receiptObserved:
			return true
		}
	}
	return false
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
	if err := s.RecoverPlaintext(); err != nil {
		return 0, err
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
			if obj.State == receiptPending || obj.State == receiptUncertain || obj.State == receiptRejected || obj.State == receiptObserved {
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
	unlock, err := s.lockScratch()
	if err != nil {
		return fail(err)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	if err := s.recoverPlaintext(); err != nil {
		return fail(err)
	}
	work, cleanupWork, err := s.plaintextWorkspace("restore-")
	if err != nil {
		return fail(err)
	}
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
	locked = false
	return payload, func() {
		f.Close()
		cleanupWork()
		unlock()
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

// RecoverPlaintext removes plaintext dumps left on the backup volume and in
// this installation's scratch directory. A workspace this process still has
// open is left in place. Symlinks are not removed and are not followed.
// The scratch lock is shared with every other process using this backup
// directory, so recovery waits instead of deleting a live workspace.
func (s *Store) RecoverPlaintext() error {
	if s == nil || s.Dir == "" {
		return nil
	}
	unlock, err := s.lockScratch()
	if err != nil {
		return err
	}
	defer unlock()
	return s.recoverPlaintext()
}

func (s *Store) recoverPlaintext() error {
	if s == nil || s.Dir == "" {
		return nil
	}
	if err := removeAbandonedPlaintext(s.Dir, nil); err != nil {
		return err
	}
	root, err := s.scratchRoot()
	if err != nil {
		return err
	}
	return removeAbandonedPlaintext(root, s.activeScratch())
}

// lockScratch is an exclusive lock shared by every process that uses this
// backup directory. It is held for the whole life of a plaintext workspace.
func (s *Store) lockScratch() (func(), error) {
	if s == nil || s.Dir == "" {
		return func() {}, nil
	}
	root, err := s.scratchRoot()
	if err != nil {
		return func() {}, err
	}
	f, err := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

func (s *Store) activeScratch() map[string]struct{} {
	s.scratchMu.Lock()
	defer s.scratchMu.Unlock()
	keep := make(map[string]struct{}, len(s.scratchActive))
	for path := range s.scratchActive {
		keep[path] = struct{}{}
	}
	return keep
}

func (s *Store) scratchRoot() (string, error) {
	root := s.Scratch
	if root == "" {
		sum := sha256.Sum256([]byte(filepath.Clean(s.Dir)))
		root = filepath.Join(os.TempDir(), "togavsu-scratch-"+hex.EncodeToString(sum[:8]))
	}
	root = filepath.Clean(root)
	backup := filepath.Clean(s.Dir)
	if dirContains(backup, root) || dirContains(root, backup) {
		return "", fmt.Errorf("plaintext scratch shares the backup volume: %w", ErrBadRequest)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func (s *Store) plaintextWorkspace(prefix string) (string, func(), error) {
	root, err := s.scratchRoot()
	if err != nil {
		return "", func() {}, err
	}
	work, err := os.MkdirTemp(root, prefix)
	if err != nil {
		return "", func() {}, err
	}
	s.scratchMu.Lock()
	if s.scratchActive == nil {
		s.scratchActive = map[string]struct{}{}
	}
	s.scratchActive[work] = struct{}{}
	s.scratchMu.Unlock()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = os.RemoveAll(work)
			s.scratchMu.Lock()
			delete(s.scratchActive, work)
			s.scratchMu.Unlock()
		})
	}
	return work, cleanup, nil
}

func removeAbandonedPlaintext(dir string, keep map[string]struct{}) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasPrefix(name, "staging-") && !strings.HasPrefix(name, "restore-") {
			continue
		}
		if !ent.IsDir() {
			continue
		}
		path := filepath.Join(dir, name)
		if _, ok := keep[path]; ok {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func dirContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mustJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	raw = append(raw, '\n')
	return raw
}

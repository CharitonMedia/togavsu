package supervisor

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// destinationIOTimeout bounds how long a caller waits for one destination
// operation. Shortening it does not cancel a filesystem syscall the
// operating system is blocked in. Tests replace the variable.
var destinationIOTimeout = 2 * time.Minute

func destinationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, destinationIOTimeout)
}

// remoteOp is one destination filesystem operation. The goroutine runs
// until the syscall returns. A caller that stops waiting leaves the entry
// registered so the operation stays tracked.
type remoteOp struct {
	done      chan struct{}
	err       error
	abandoned atomic.Bool
}

var remoteOps sync.Map

// canonicalOwnedName is the receipt name for an archive, its partial sibling,
// and its checksum sidecar. Put, get, delete, and temp cleanup share this
// identity so one of them cannot decide the object is absent while another
// is still writing it.
func canonicalOwnedName(name string) string {
	name = strings.TrimSuffix(name, ".partial")
	if strings.HasSuffix(name, ".sha256") {
		return strings.TrimSuffix(name, ".sha256") + ".tgbk"
	}
	return name
}

func remoteKey(destID, name string) string {
	return destID + "\x00" + canonicalOwnedName(name)
}

func listKey(destID string) string {
	return destID + "\x00\x00list"
}

// trackFS waits up to ctx for fn. On deadline it returns ErrRemoteUncertain
// and keeps fn registered. fn is not asked to stop.
func trackFS(key string, ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	op := &remoteOp{done: make(chan struct{})}
	stored := false
	for attempt := 0; attempt < 2; attempt++ {
		existing, loaded := remoteOps.LoadOrStore(key, op)
		if !loaded {
			stored = true
			break
		}
		prev := existing.(*remoteOp)
		select {
		case <-prev.done:
			remoteOps.CompareAndDelete(key, prev)
		default:
			return fmt.Errorf("%w: a destination operation for this object is already running", ErrRemoteUncertain)
		}
	}
	if !stored {
		return fmt.Errorf("%w: a destination operation for this object is already running", ErrRemoteUncertain)
	}
	go func() {
		op.err = fn()
		close(op.done)
		if op.abandoned.Load() {
			remoteOps.CompareAndDelete(key, op)
		}
	}()
	select {
	case <-op.done:
		remoteOps.CompareAndDelete(key, op)
		return op.err
	case <-ctx.Done():
		op.abandoned.Store(true)
		select {
		case <-op.done:
			remoteOps.CompareAndDelete(key, op)
			return op.err
		default:
			return fmt.Errorf("%w: filesystem call still running after the wait ended: %v", ErrRemoteUncertain, ctx.Err())
		}
	}
}

// destinationBusy reports an in-flight filesystem operation for one destination.
func destinationBusy(destID string) bool {
	prefix := destID + "\x00"
	busy := false
	remoteOps.Range(func(key, _ any) bool {
		name, ok := key.(string)
		if ok && strings.HasPrefix(name, prefix) && remoteBusy(name) {
			busy = true
			return false
		}
		return true
	})
	return busy
}

// remoteBusy reports whether a tracked filesystem operation for key has not
// finished. A finished operation is dropped from the registry.
func remoteBusy(key string) bool {
	value, ok := remoteOps.Load(key)
	if !ok {
		return false
	}
	op := value.(*remoteOp)
	select {
	case <-op.done:
		remoteOps.CompareAndDelete(key, op)
		return false
	default:
		return true
	}
}

func dupFile(f *os.File) (*os.File, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), f.Name()), nil
}

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
	// Namespace is the per-installation directory or key prefix. Retention
	// deletes only receipts inside it. Objects already in the configured root
	// are left where they are.
	Namespace string `json:"namespace,omitempty"`
	Path      string `json:"path,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	// SecretKey is plaintext only in memory. The book seals it with the
	// installer key before destinations.json is written.
	SecretKey    string `json:"secret_key,omitempty"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	User         string `json:"user,omitempty"`
	IdentityFile string `json:"identity_file,omitempty"`
	KnownHosts   string `json:"known_hosts,omitempty"`
	RemotePath   string `json:"remote_path,omitempty"`
}

// Public hides credentials from API responses.
func (s DestinationSpec) Public() DestinationSpec {
	s.SecretKey = ""
	if s.AccessKey != "" {
		s.AccessKey = "configured"
	}
	return s
}

// DestinationBook persists destination specs as JSON on the supervisor data
// volume. SecretKey is sealed with Key, the installer field key. Key is not
// written next to the ciphertext. SFTP keeps using IdentityFile as a path
// reference and does not copy the key file into this record.
//
// ponytail: destinations live in a supervisor JSON file, ceiling: not the
// core settings table, upgrade: core backup-destination records push here,
// trigger: /api/v1/backup-destinations is served by core.
type DestinationBook struct {
	Path string
	Key  []byte
	mu   sync.Mutex
}

// destSealPrefix marks a secret sealed with the installer key. A value
// without the prefix is a legacy plaintext credential and is rewritten.
const destSealPrefix = "destseal:v1:"

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
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readSpecs()
}

func (b *DestinationBook) readSpecs() ([]DestinationSpec, error) {
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
	legacy := false
	for i := range specs {
		plain, migrated, err := b.openSecret(specs[i].SecretKey)
		if err != nil {
			return nil, err
		}
		if migrated {
			legacy = true
		}
		specs[i].SecretKey = plain
	}
	if legacy {
		if err := b.writeSealed(specs); err != nil {
			return nil, err
		}
	}
	return specs, nil
}

func (b *DestinationBook) Put(spec DestinationSpec) (DestinationSpec, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	specs, err := b.readSpecs()
	if err != nil {
		return DestinationSpec{}, err
	}
	previous := ""
	replaced := false
	var replacedAt int
	for i := range specs {
		if specs[i].ID == spec.ID {
			previous = specs[i].Namespace
			replaced = true
			replacedAt = i
		}
	}
	spec, err = prepareSpec(spec, previous)
	if err != nil {
		return DestinationSpec{}, err
	}
	if replaced {
		specs[replacedAt] = spec
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
	b.mu.Lock()
	defer b.mu.Unlock()
	specs, err := b.readSpecs()
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
	return b.writeSealed(specs)
}

func (b *DestinationBook) writeSealed(specs []DestinationSpec) error {
	if err := os.MkdirAll(filepath.Dir(b.Path), 0o700); err != nil {
		return err
	}
	stored := make([]DestinationSpec, len(specs))
	for i, spec := range specs {
		sealed, err := b.sealSecret(spec.SecretKey)
		if err != nil {
			return err
		}
		spec.SecretKey = sealed
		stored[i] = spec
	}
	raw, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := writeFileAtomic(b.Path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(b.Path, 0o600)
}

func (b *DestinationBook) sealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if strings.HasPrefix(plain, destSealPrefix) {
		return "", fmt.Errorf("destination secret: %w", ErrBadRequest)
	}
	aead, err := b.destAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plain), []byte(destSealPrefix))
	return destSealPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

func (b *DestinationBook) openSecret(stored string) (string, bool, error) {
	if stored == "" {
		return "", false, nil
	}
	if !strings.HasPrefix(stored, destSealPrefix) {
		return stored, true, nil
	}
	aead, err := b.destAEAD()
	if err != nil {
		return "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, destSealPrefix))
	if err != nil {
		return "", false, fmt.Errorf("destination secret: %w", ErrDecrypt)
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", false, fmt.Errorf("destination secret: %w", ErrDecrypt)
	}
	plain, err := aead.Open(nil, raw[:ns], raw[ns:], []byte(destSealPrefix))
	if err != nil {
		return "", false, fmt.Errorf("destination secret: %w", ErrDecrypt)
	}
	return string(plain), false, nil
}

func (b *DestinationBook) destAEAD() (cipher.AEAD, error) {
	if b == nil || len(b.Key) != 32 {
		return nil, fmt.Errorf("installer key: %w", ErrBadRequest)
	}
	block, err := aes.NewCipher(b.Key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
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
		if err := validSFTPPath(spec.RemotePath); err != nil {
			return err
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
// stall, when set, runs inside the tracked filesystem worker. Tests use it
// to hold a syscall without blocking the caller past destinationIOTimeout.
// dirSync, when set, runs before a directory fsync. Steps are "root" and
// "namespace" for the parent of those directories, "file" after rename, and
// "delete" after unlink. A returned error replaces that fsync. Put syncs a
// parent even when the directory is already there, so a failed sync is
// retried before the copy can be acknowledged.
type PathDestination struct {
	Spec    DestinationSpec
	stall   func(step string)
	dirSync func(step string) error
}

func (p *PathDestination) ID() string   { return p.Spec.ID }
func (p *PathDestination) Kind() string { return "path" }

func (p *PathDestination) dir() string {
	if p.Spec.Namespace == "" {
		return p.Spec.Path
	}
	return filepath.Join(p.Spec.Path, p.Spec.Namespace)
}

// openNamespace opens the configured destination root, then the namespace,
// without following symlinks. create is only for a new owned namespace
// directory. A symlink at that name is never used as the directory.
func (p *PathDestination) openNamespace(create bool) (*os.File, error) {
	var syncParent func(*os.File) error
	if create {
		// Every put syncs the parent. A directory left behind by a failed
		// sync, including after this process is gone, is not treated as durable.
		syncParent = func(parent *os.File) error {
			return p.syncDirectory(parent, "root")
		}
	}
	root, err := openConfiguredRoot(p.Spec.Path, syncParent)
	if err != nil {
		return nil, err
	}
	if p.Spec.Namespace == "" {
		return root, nil
	}
	if !create {
		fd, err := syscall.Openat(int(root.Fd()), p.Spec.Namespace, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		root.Close()
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), p.Spec.Namespace), nil
	}
	dir, err := mkdirChildNoFollow(root, p.Spec.Namespace)
	if err != nil {
		root.Close()
		return nil, err
	}
	if err := p.syncDirectory(root, "namespace"); err != nil {
		dir.Close()
		root.Close()
		return nil, err
	}
	root.Close()
	return dir, nil
}

// openConfiguredRoot opens the destination directory the operator named.
// The final component is created only when it is missing, and it is never
// a followed symlink. Parent components are opened without following
// symlinks, so a replaced parent cannot redirect the root.
func openConfiguredRoot(path string, syncParent func(*os.File) error) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: destination path", ErrBadRequest)
	}
	parent, err := openNoFollowDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	base := filepath.Base(path)
	if base == "." || base == ".." || base == string(os.PathSeparator) {
		return nil, fmt.Errorf("%w: destination path", ErrBadRequest)
	}
	fd, err := syscall.Openat(int(parent.Fd()), base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err == syscall.ENOENT {
		if err := syscall.Mkdirat(int(parent.Fd()), base, 0o700); err != nil {
			return nil, pathError("mkdir", path, err)
		}
		fd, err = syscall.Openat(int(parent.Fd()), base, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, pathError("open", path, err)
	}
	if syncParent != nil {
		if err := syncParent(parent); err != nil {
			syscall.Close(fd)
			return nil, err
		}
	}
	return os.NewFile(uintptr(fd), base), nil
}

// openNoFollowDir opens path by walking from the filesystem root. Each
// component is opened with O_NOFOLLOW, so a symlink in the configured
// directory cannot be swapped for another path between checks.
func openNoFollowDir(path string) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: destination path", ErrBadRequest)
	}
	clean := filepath.Clean(path)
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, pathError("open", "/", err)
	}
	cur := os.NewFile(uintptr(fd), "/")
	walked := "/"
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			cur.Close()
			return nil, fmt.Errorf("%w: destination path", ErrBadRequest)
		}
		walked = filepath.Join(walked, part)
		next, err := syscall.Openat(int(cur.Fd()), part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		cur.Close()
		if err != nil {
			return nil, pathError("open", walked, err)
		}
		cur = os.NewFile(uintptr(next), part)
	}
	return cur, nil
}

// pathError keeps a local filesystem failure as a path error. Callers that
// compare a raw errno do so before this wrap. Classification treats the
// result as a completed local failure, not an uncertain remote write.
func pathError(op, path string, err error) error {
	if err == nil {
		return nil
	}
	return &os.PathError{Op: op, Path: path, Err: err}
}

// mkdirChildNoFollow creates or opens one directory entry. A symlink is not
// followed and is not treated as the directory.
func mkdirChildNoFollow(parent *os.File, name string) (*os.File, error) {
	if err := validNamespace(name); err != nil {
		return nil, err
	}
	fd, err := syscall.Openat(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		return os.NewFile(uintptr(fd), name), nil
	}
	if err != syscall.ENOENT {
		return nil, pathError("open", name, err)
	}
	if err := syscall.Mkdirat(int(parent.Fd()), name, 0o700); err != nil && err != syscall.EEXIST {
		return nil, pathError("mkdir", name, err)
	}
	fd, err = syscall.Openat(int(parent.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

// syncDirectory makes one directory entry durable. The file descriptor was
// opened with O_NOFOLLOW, so the sync cannot follow a swapped symlink.
func (p *PathDestination) syncDirectory(dir *os.File, step string) error {
	if p != nil && p.dirSync != nil {
		if err := p.dirSync(step); err != nil {
			return err
		}
	}
	if dir == nil {
		return fmt.Errorf("%w: directory sync", ErrBadRequest)
	}
	return dir.Sync()
}

func exclusiveTemp(dir *os.File) (string, *os.File, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, err
	}
	name := ".togavsu-" + hexEncode(raw[:]) + ".tmp"
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_RDWR, 0o600)
	if err != nil {
		return "", nil, pathError("open", name, err)
	}
	return name, os.NewFile(uintptr(fd), name), nil
}

func (p *PathDestination) Put(ctx context.Context, name string, r io.Reader, sha string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := safeName(name); err != nil {
		return err
	}
	src := r
	var srcClose func() error
	if f, ok := r.(*os.File); ok {
		dup, err := dupFile(f)
		if err != nil {
			return pathError("dup", f.Name(), err)
		}
		src = dup
		srcClose = dup.Close
	}
	wait, cancel := destinationContext(ctx)
	defer cancel()
	return trackFS(remoteKey(p.ID(), name), wait, func() error {
		if srcClose != nil {
			defer srcClose()
		}
		return p.putFile(wait, name, src, sha)
	})
}

type pathTempKey struct{}

// WithPathTemp records the exact temporary file before its bytes can be
// abandoned. An empty name clears that ownership after the rename.
// inode and mtime are the file identity. A recreated name does not match.
func WithPathTemp(ctx context.Context, fn func(name string, inode uint64, mtime int64, digest string) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, pathTempKey{}, fn)
}

func pathTempOwner(ctx context.Context) func(string, uint64, int64, string) error {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(pathTempKey{}).(func(string, uint64, int64, string) error)
	return fn
}

func statMtime(info syscall.Stat_t) int64 {
	return info.Mtim.Sec*1_000_000_000 + info.Mtim.Nsec
}

func (p *PathDestination) putFile(ctx context.Context, name string, r io.Reader, sha string) error {
	if p.stall != nil {
		p.stall("write")
	}
	dir, err := p.openNamespace(true)
	if err != nil {
		return err
	}
	defer dir.Close()
	tmpName, f, err := exclusiveTemp(dir)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		p.removeTemp(dir, tmpName)
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	var info syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &info); err != nil {
		return err
	}
	if own := pathTempOwner(ctx); own != nil {
		if err := own(tmpName, info.Ino, statMtime(info), ""); err != nil {
			return err
		}
	}
	if _, copyErr := io.Copy(f, r); copyErr != nil {
		return copyErr
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := syscall.Fstat(int(f.Fd()), &info); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sum, err := sha256Hex(f)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if own := pathTempOwner(ctx); own != nil {
		if err := own(tmpName, info.Ino, statMtime(info), sum); err != nil {
			return err
		}
	}
	if p.stall != nil {
		p.stall("hash")
	}
	if sha != "" {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		got, err := sha256Hex(f)
		if err != nil {
			return err
		}
		if got != sha {
			return ErrChecksum
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if p.stall != nil {
		p.stall("rename")
	}
	if err := syscall.Renameat(int(dir.Fd()), tmpName, int(dir.Fd()), name); err != nil {
		return pathError("rename", name, err)
	}
	tmpName = ""
	if own := pathTempOwner(ctx); own != nil {
		if err := own("", 0, 0, ""); err != nil {
			return fmt.Errorf("%w: temp ownership: %v", ErrRemoteUncertain, err)
		}
	}
	// The new name is visible. A failed directory sync must not become a
	// confirmed receipt, and it must not be a definite rejection: that would
	// delete the object and drop ownership. A later read can still see it.
	if err := p.syncDirectory(dir, "file"); err != nil {
		return fmt.Errorf("%w: directory sync after rename: %v", ErrRemoteUncertain, err)
	}
	return nil
}

func (p *PathDestination) removeTemp(dir *os.File, name string) {
	if name == "" || dir == nil {
		return
	}
	if p.stall != nil {
		p.stall("remove")
	}
	_ = syscall.Unlinkat(int(dir.Fd()), name)
}

func (p *PathDestination) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := safeName(name); err != nil {
		return nil, err
	}
	wait, cancel := destinationContext(ctx)
	var opened *os.File
	var box struct {
		mu        sync.Mutex
		f         *os.File
		abandoned bool
	}
	err := trackFS(remoteKey(p.ID(), name), wait, func() error {
		if p.stall != nil {
			p.stall("open")
		}
		dir, err := p.openNamespace(false)
		if err != nil {
			return err
		}
		fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
		dir.Close()
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(fd), name)
		box.mu.Lock()
		left := box.abandoned
		if !left {
			box.f = f
		}
		box.mu.Unlock()
		if left {
			if p.stall != nil {
				p.stall("close")
			}
			return f.Close()
		}
		return nil
	})
	if errors.Is(err, ErrRemoteUncertain) {
		box.mu.Lock()
		box.abandoned = true
		leaked := box.f
		box.mu.Unlock()
		if leaked != nil {
			go p.closeTracked(leaked, name)
		}
		cancel()
		return nil, err
	}
	if err != nil {
		cancel()
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	opened = box.f
	return &pathRead{
		f:       opened,
		parent:  ctx,
		cancel:  cancel,
		readKey: remoteKey(p.ID(), name),
		stall:   p.stall,
	}, nil
}

func (p *PathDestination) closeTracked(f *os.File, name string) {
	_ = trackFS(remoteKey(p.ID(), name), context.Background(), func() error {
		if p.stall != nil {
			p.stall("close")
		}
		return f.Close()
	})
}

// RemovePartial deletes the in-progress sibling of an owned archive name.
func (p *PathDestination) RemovePartial(ctx context.Context, name string) error {
	return p.Delete(ctx, name+".partial")
}

var (
	errTempBusy     = errors.New("temp upload is active")
	errTempIdentity = errors.New("temp file identity changed")
)

func ownedTempName(name string) bool {
	const prefix = ".togavsu-"
	const suffix = ".tmp"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(hexPart) != 32 {
		return false
	}
	for _, c := range hexPart {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return false
		}
	}
	return true
}

// RemoveOwnedTemp unlinks one recorded temporary file when its inode still
// matches and no process holds its lock. A different inode or a symlink is
// left in place. A missing name, including after a successful rename, is done.
func (p *PathDestination) RemoveOwnedTemp(ctx context.Context, name string, inode uint64, mtime int64, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ownedTempName(name) || inode == 0 || mtime == 0 {
		return fmt.Errorf("temp name: %w", ErrBadRequest)
	}
	dir, err := p.openNamespace(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer dir.Close()
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if err == syscall.ENOENT {
			return nil
		}
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var info syscall.Stat_t
	if err := syscall.Fstat(fd, &info); err != nil {
		return err
	}
	if digest == "" || info.Ino != inode || statMtime(info) != mtime {
		return errTempIdentity
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errTempBusy
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	hashed, err := sha256Hex(file)
	if err != nil {
		return err
	}
	if hashed != digest {
		return errTempIdentity
	}
	err = syscall.Unlinkat(int(dir.Fd()), name)
	if err != nil && err != syscall.ENOENT {
		return err
	}
	return nil
}

func (p *PathDestination) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := safeName(name); err != nil {
		return err
	}
	wait, cancel := destinationContext(ctx)
	defer cancel()
	return trackFS(remoteKey(p.ID(), name), wait, func() error {
		if p.stall != nil {
			p.stall("remove")
		}
		dir, err := p.openNamespace(false)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer dir.Close()
		err = syscall.Unlinkat(int(dir.Fd()), name)
		if err != nil && err != syscall.ENOENT {
			return err
		}
		// Absence is not durable until the directory entry is synced. A retry
		// of a name that is already gone still has to sync before success.
		if err := p.syncDirectory(dir, "delete"); err != nil {
			return err
		}
		return nil
	})
}

func (p *PathDestination) List(ctx context.Context) ([]RemoteObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	wait, cancel := destinationContext(ctx)
	defer cancel()
	var out []RemoteObject
	err := trackFS(listKey(p.ID()), wait, func() error {
		if p.stall != nil {
			p.stall("list")
		}
		dir, err := p.openNamespace(false)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		defer dir.Close()
		entries, err := dir.Readdir(-1)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			out = append(out, RemoteObject{Name: ent.Name(), Created: ent.ModTime()})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// pathRead bounds each read and close. A timed-out read leaves that syscall
// registered. Close does not start a second syscall on a file the read is
// still using; the read worker closes it when the syscall returns.
type pathRead struct {
	f         *os.File
	parent    context.Context
	cancel    context.CancelFunc
	readKey   string
	stall     func(string)
	mu        sync.Mutex
	abandoned bool
	closed    bool
}

func (r *pathRead) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0, io.EOF
	}
	r.mu.Unlock()
	if remoteBusy(r.readKey) {
		return 0, fmt.Errorf("%w: a destination operation for this object is already running", ErrRemoteUncertain)
	}
	wait, cancel := destinationContext(r.parent)
	defer cancel()
	// The worker must not write into p after Read has returned. owned stays
	// reachable from the tracked operation until the syscall returns.
	owned := make([]byte, len(p))
	var n int
	var rerr error
	err := trackFS(r.readKey, wait, func() error {
		if r.stall != nil {
			r.stall("read")
		}
		n, rerr = r.f.Read(owned)
		r.mu.Lock()
		if r.abandoned && !r.closed {
			cerr := r.f.Close()
			r.closed = true
			if rerr == nil {
				rerr = cerr
			}
		}
		r.mu.Unlock()
		if rerr == io.EOF {
			return nil
		}
		return rerr
	})
	if errors.Is(err, ErrRemoteUncertain) {
		return 0, err
	}
	if n > 0 {
		copy(p, owned[:n])
	}
	if err != nil {
		return n, err
	}
	return n, rerr
}

func (r *pathRead) Close() error {
	if r.cancel != nil {
		defer r.cancel()
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	if remoteBusy(r.readKey) {
		r.abandoned = true
		r.mu.Unlock()
		return fmt.Errorf("%w: read still running; the read operation closes the file when the syscall returns", ErrRemoteUncertain)
	}
	r.mu.Unlock()
	wait, cancel := destinationContext(r.parent)
	defer cancel()
	return trackFS(r.readKey, wait, func() error {
		if r.stall != nil {
			r.stall("close")
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.closed {
			return nil
		}
		r.closed = true
		return r.f.Close()
	})
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

// receiptPending is an ownership record written before bytes can reach the
// destination. An empty state is a confirmed copy. Older receipts omit it.
const (
	receiptPending   = "pending"
	receiptUncertain = "uncertain"
	receiptRejected  = "rejected"
	receiptObserved  = "observed"
)

// maxChecksumSidecar is the most a checksum sidecar may contain. A SHA-256
// hex digest is 64 characters. Anything larger is not a sidecar.
const maxChecksumSidecar = 128

// An uncertain receipt is not aged out. A lost PUT that never publishes leaves
// the tombstone in place so a later copy can still be deleted. That is the
// limit of what the client can know.

// OwnedObject is one archive this installation stored on a destination.
// Retention deletes these names only. A matching suffix is not ownership.
//
// UncertainAt is when a mutating upload lost its acknowledgment. It is not
// the backup's Created time. The client deadline does not bound the server,
// so an uncertain receipt stays until the object is observed and removed.
// Elapsed wall time is not evidence that the original PUT finished.
type OwnedObject struct {
	DestinationID string    `json:"destination_id"`
	Name          string    `json:"name"`
	Created       time.Time `json:"created"`
	State         string    `json:"state,omitempty"`
	UncertainAt   time.Time `json:"uncertain_at,omitempty"`
	// Digest is the archive checksum recorded with the intent. Recovery can
	// validate a copy after local retention has removed the metadata file.
	Digest string `json:"digest,omitempty"`
	// Temp is the exact unpublished file for this upload. TempInode and
	// TempMtime are the identity recorded when that file was created.
	// Recovery removes only that identity. A recreated name is left alone.
	Temp       string `json:"temp,omitempty"`
	TempInode  uint64 `json:"temp_inode,omitempty"`
	TempMtime  int64  `json:"temp_mtime,omitempty"`
	TempDigest string `json:"temp_digest,omitempty"`
}

// PruneDestination deletes owned archives older than retention. It does not
// list the destination to decide what is owned, and a checksum sidecar is
// removed only for an owned archive that was itself deleted.
func PruneDestination(ctx context.Context, dest Destination, retention time.Duration, now time.Time, owned []OwnedObject) (deleted []string, err error) {
	if retention <= 0 {
		return nil, fmt.Errorf("retention: %w", ErrBadRequest)
	}
	cutoff := now.Add(-retention)
	for _, obj := range owned {
		if dest.ID() != "" && obj.DestinationID != "" && obj.DestinationID != dest.ID() {
			continue
		}
		if obj.State == receiptUncertain || obj.State == receiptPending || obj.State == receiptRejected || obj.State == receiptObserved || obj.Name == "" || strings.HasPrefix(obj.Name, "togavsu-destination-test") || !strings.HasSuffix(obj.Name, ".tgbk") {
			continue
		}
		when := obj.Created
		if when.IsZero() {
			parsed, ok := backupTimeFromName(obj.Name)
			if !ok {
				continue
			}
			when = parsed
		}
		if !when.Before(cutoff) {
			continue
		}
		if err := dest.Delete(ctx, obj.Name); err != nil {
			return deleted, err
		}
		side := strings.TrimSuffix(obj.Name, ".tgbk") + ".sha256"
		if err := dest.Delete(ctx, side); err != nil {
			return deleted, err
		}
		deleted = append(deleted, obj.Name)
	}
	return deleted, nil
}

// ReceiptBook is the local inventory of archives this installation copied.
// Missing receipts are not backfilled from filenames.
type ReceiptBook struct {
	Path string
}

func (b *ReceiptBook) withLock(fn func() error) error {
	f, err := os.OpenFile(b.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (b *ReceiptBook) Add(obj OwnedObject) error {
	if b == nil || b.Path == "" || obj.DestinationID == "" || obj.Name == "" {
		return fmt.Errorf("receipt: %w", ErrBadRequest)
	}
	return b.withLock(func() error {
		all, err := b.read()
		if err != nil {
			return err
		}
		replaced := false
		for i := range all {
			if all[i].DestinationID == obj.DestinationID && all[i].Name == obj.Name {
				all[i] = obj
				replaced = true
			}
		}
		if !replaced {
			all = append(all, obj)
		}
		return b.write(all)
	})
}

func (b *ReceiptBook) Remove(destID, name string) error {
	if b == nil || b.Path == "" {
		return nil
	}
	return b.withLock(func() error {
		all, err := b.read()
		if err != nil {
			return err
		}
		kept := all[:0]
		for _, obj := range all {
			if obj.DestinationID == destID && obj.Name == name {
				continue
			}
			kept = append(kept, obj)
		}
		return b.write(kept)
	})
}

func (b *ReceiptBook) List(destID string) ([]OwnedObject, error) {
	if b == nil || b.Path == "" {
		return nil, nil
	}
	var out []OwnedObject
	err := b.withLock(func() error {
		all, err := b.read()
		if err != nil {
			return err
		}
		for _, obj := range all {
			if destID == "" || obj.DestinationID == destID {
				out = append(out, obj)
			}
		}
		return nil
	})
	return out, err
}

func (b *ReceiptBook) read() ([]OwnedObject, error) {
	raw, err := os.ReadFile(b.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var all []OwnedObject
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	return all, nil
}

func (b *ReceiptBook) write(all []OwnedObject) error {
	if all == nil {
		all = []OwnedObject{}
	}
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return writeFileAtomic(b.Path, raw, 0o600)
}

// errDestinationRelocation means a destination still has copied archives,
// so its storage location cannot be replaced. Retention and credentials
// can change. The historical path would otherwise be abandoned.
var errDestinationRelocation = fmt.Errorf("%w: destination location cannot change while copies remain", ErrBadRequest)

// errDestinationRemoval means the specification still names owned copies or
// an upload. Removing it would let the same id come back at a new location.
var errDestinationRemoval = fmt.Errorf("%w: destination cannot be removed while copies or an upload remain", ErrBadRequest)

func sameStorage(a, b DestinationSpec) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case "path":
		return filepath.Clean(a.Path) == filepath.Clean(b.Path) && a.Namespace == b.Namespace
	case "s3":
		return a.Endpoint == b.Endpoint && a.Region == b.Region && a.Bucket == b.Bucket && a.Prefix == b.Prefix && a.Namespace == b.Namespace
	case "sftp":
		return a.Host == b.Host && a.Port == b.Port && a.User == b.User && a.RemotePath == b.RemotePath && a.Namespace == b.Namespace
	default:
		return false
	}
}

func prepareSpec(spec DestinationSpec, previousNamespace string) (DestinationSpec, error) {
	if spec.Namespace == "" {
		spec.Namespace = previousNamespace
	}
	if spec.Namespace == "" {
		ns, err := newNamespace()
		if err != nil {
			return DestinationSpec{}, err
		}
		spec.Namespace = ns
	}
	if err := validNamespace(spec.Namespace); err != nil {
		return DestinationSpec{}, err
	}
	if spec.RetentionDays == 0 {
		spec.RetentionDays = 30
	}
	if err := validateSpec(spec); err != nil {
		return DestinationSpec{}, err
	}
	return spec, nil
}

func newNamespace() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "ns" + hexEncode(raw[:]), nil
}

func hexEncode(raw []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(raw)*2)
	for i, b := range raw {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&0x0f]
	}
	return string(out)
}

func validNamespace(s string) error {
	if len(s) < 8 || len(s) > 63 {
		return fmt.Errorf("namespace: %w", ErrBadRequest)
	}
	for i, c := range s {
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || (c == '-' && i > 0)
		if !ok {
			return fmt.Errorf("namespace: %w", ErrBadRequest)
		}
	}
	return nil
}

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
	"unsafe"
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
	// noXattr forces the in-file token used when a mounted share returns
	// ENOTSUP for user xattrs. Production still tries the xattr first.
	noXattr bool
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
// abandoned. The token does not change while the upload is copied. link is
// the private hardlink when the filesystem provides one. release drops the
// record only after publication or a synced removal has already been verified.
func WithPathTemp(ctx context.Context, fn func(name string, inode uint64, token, link string, release bool) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, pathTempKey{}, fn)
}

func pathTempOwner(ctx context.Context) func(string, uint64, string, string, bool) error {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(pathTempKey{}).(func(string, uint64, string, string, bool) error)
	return fn
}

func tempPrefix(token string) string {
	return "TGVTMP1\n" + token + "\n"
}

func tempLinkName(tmp string) string {
	return strings.TrimSuffix(tmp, ".tmp") + ".own"
}

func tempHex(name string) (string, bool) {
	const prefix = ".togavsu-"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	hexPart := rest
	if i := strings.IndexByte(rest, '.'); i >= 0 {
		hexPart = rest[:i]
	}
	if len(hexPart) != 32 {
		return "", false
	}
	for _, c := range hexPart {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return "", false
		}
	}
	return hexPart, true
}

func xattrUnsupported(err error) bool {
	return errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOTSUP)
}

const tempTokenAttr = "user.togavsu.own"

func newTempToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hexEncode(raw[:]), nil
}

func fsetxattr(fd int, attr string, data []byte) error {
	name, err := syscall.BytePtrFromString(attr)
	if err != nil {
		return err
	}
	var value unsafe.Pointer
	if len(data) > 0 {
		value = unsafe.Pointer(&data[0])
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_FSETXATTR, uintptr(fd), uintptr(unsafe.Pointer(name)), uintptr(value), uintptr(len(data)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func fgetxattr(fd int, attr string) (string, error) {
	name, err := syscall.BytePtrFromString(attr)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 64)
	n, _, errno := syscall.Syscall6(syscall.SYS_FGETXATTR, uintptr(fd), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
	if errno != 0 {
		return "", errno
	}
	if int(n) > len(buf) {
		return "", syscall.ERANGE
	}
	return string(buf[:n]), nil
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
	var info syscall.Stat_t
	var linkName string
	journaled := false
	defer func() {
		if tmpName != "" {
			if journaled && info.Ino != 0 {
				_ = p.disposeExact(dir, tmpName, info.Ino)
				_ = p.disposeExact(dir, linkName, info.Ino)
			} else {
				_ = syscall.Unlinkat(int(dir.Fd()), tmpName)
			}
		}
		f.Close()
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	if err := syscall.Fstat(int(f.Fd()), &info); err != nil {
		return err
	}
	token, err := newTempToken()
	if err != nil {
		return err
	}
	header := false
	xerr := error(syscall.EOPNOTSUPP)
	if !p.noXattr {
		xerr = fsetxattr(int(f.Fd()), tempTokenAttr, []byte(token))
	}
	if xerr != nil {
		if !xattrUnsupported(xerr) && !p.noXattr {
			return xerr
		}
		if _, err := f.Write([]byte(tempPrefix(token))); err != nil {
			return err
		}
		header = true
	}
	if err := f.Sync(); err != nil {
		return err
	}
	linkName = tempLinkName(tmpName)
	if err := linkAt(int(dir.Fd()), tmpName, int(dir.Fd()), linkName); err != nil {
		if !xattrUnsupported(err) {
			return err
		}
		linkName = ""
	}
	if err := p.syncDirectory(dir, "link"); err != nil {
		return err
	}
	if own := pathTempOwner(ctx); own != nil {
		if err := own(tmpName, info.Ino, token, linkName, false); err != nil {
			return err
		}
	}
	journaled = true
	payloadAt := int64(0)
	if header {
		payloadAt = int64(len(tempPrefix(token)))
	}
	if p.stall != nil {
		var one [1]byte
		n, rerr := r.Read(one[:])
		if n > 0 {
			if _, werr := f.Write(one[:n]); werr != nil {
				return werr
			}
		}
		if rerr != nil && rerr != io.EOF {
			return rerr
		}
		p.stall("copy")
	}
	if _, copyErr := io.Copy(f, r); copyErr != nil {
		return copyErr
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if p.stall != nil {
		p.stall("hash")
	}
	if sha != "" {
		if _, err := f.Seek(payloadAt, io.SeekStart); err != nil {
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
	// The descriptor stays open, so this process keeps the exclusive lock
	// until the published name and the directory sync have finished.
	publish := tmpName
	if header {
		cleanName, clean, err := exclusiveTemp(dir)
		if err != nil {
			return err
		}
		if _, err := f.Seek(payloadAt, io.SeekStart); err != nil {
			clean.Close()
			_ = syscall.Unlinkat(int(dir.Fd()), cleanName)
			return err
		}
		if _, err := io.Copy(clean, f); err != nil {
			clean.Close()
			_ = syscall.Unlinkat(int(dir.Fd()), cleanName)
			return err
		}
		if err := clean.Sync(); err != nil {
			clean.Close()
			_ = syscall.Unlinkat(int(dir.Fd()), cleanName)
			return err
		}
		clean.Close()
		publish = cleanName
	}
	if p.stall != nil {
		p.stall("rename")
	}
	if err := syscall.Renameat(int(dir.Fd()), publish, int(dir.Fd()), name); err != nil {
		if publish != tmpName {
			_ = syscall.Unlinkat(int(dir.Fd()), publish)
		}
		return pathError("rename", name, err)
	}
	if err := p.syncDirectory(dir, "file"); err != nil {
		return fmt.Errorf("%w: directory sync after rename: %v", ErrRemoteUncertain, err)
	}
	if header {
		_ = p.disposeExact(dir, tmpName, info.Ino)
	}
	_ = p.disposeExact(dir, linkName, info.Ino)
	tmpName = ""
	if own := pathTempOwner(ctx); own != nil {
		if err := own("", 0, "", "", true); err != nil {
			return fmt.Errorf("%w: temp ownership: %v", ErrRemoteUncertain, err)
		}
	}
	return nil
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
// carries the ownership token and no process holds its lock. The wait is the
// same bounded tracked filesystem operation as an upload. A timeout leaves
// that operation registered and does not report the removal as finished.
// A different inode, a missing token, a symlink, or a directory is left in
// place. Absence is acknowledged only after the directory sync.
func (p *PathDestination) RemoveOwnedTemp(ctx context.Context, obj OwnedObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !ownedTempName(obj.Temp) || obj.TempInode == 0 || !ownedTempToken(obj.TempToken) {
		return fmt.Errorf("temp name: %w", ErrBadRequest)
	}
	wait, cancel := destinationContext(ctx)
	defer cancel()
	return trackFS(remoteKey(p.ID(), obj.Temp), wait, func() error {
		if p.stall != nil {
			p.stall("cleanup")
		}
		return p.removeOwnedTemp(obj)
	})
}

// tempDisplacedError means the directory entry was not the owned inode.
// hold is the quarantine name that still contains the other file because the
// original name was no longer free. The owned inode is not deleted.
type tempDisplacedError struct {
	hold string
}

func (e tempDisplacedError) Error() string {
	return "temp disposition kept a different file"
}

func ownedTempToken(token string) bool {
	if len(token) != 32 {
		return false
	}
	for _, c := range token {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return false
		}
	}
	return true
}

func (p *PathDestination) removeOwnedTemp(obj OwnedObject) error {
	dir, err := p.openNamespace(false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer dir.Close()
	fd, err := p.openOwned(dir, obj)
	if err != nil {
		if errors.Is(err, errTempIdentity) || errors.Is(err, errTempBusy) {
			return err
		}
		if os.IsNotExist(err) || err == syscall.ENOENT {
			if obj.TempLink != "" {
				_ = p.acknowledgeTempGone(dir, obj.TempLink)
			}
			return p.acknowledgeTempGone(dir, obj.Temp)
		}
		return err
	}
	defer syscall.Close(fd)
	if p.stall != nil {
		p.stall("dispose")
	}
	err = p.disposeExact(dir, obj.Temp, obj.TempInode)
	if errors.Is(err, errTempIdentity) || errors.As(err, new(tempDisplacedError)) {
		if obj.TempLink != "" {
			if lerr := p.disposeExact(dir, obj.TempLink, obj.TempInode); lerr != nil && !errors.Is(lerr, errTempIdentity) {
				return lerr
			}
		}
		return err
	}
	if err != nil {
		return err
	}
	if obj.TempLink != "" {
		if err := p.disposeExact(dir, obj.TempLink, obj.TempInode); err != nil {
			return err
		}
	}
	if err := p.acknowledgeTempGone(dir, obj.Temp); err != nil {
		return err
	}
	if obj.TempLink != "" {
		return p.acknowledgeTempGone(dir, obj.TempLink)
	}
	return nil
}

// openOwned opens the recorded temp, or its private hardlink when the temp
// name no longer has the owned inode. A symlink or directory is not opened.
func (p *PathDestination) openOwned(dir *os.File, obj OwnedObject) (int, error) {
	missing := 0
	checked := 0
	for _, name := range []string{obj.Temp, obj.TempLink} {
		if name == "" {
			continue
		}
		checked++
		fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDWR|syscall.O_NOFOLLOW, 0)
		if err == syscall.ENOENT {
			missing++
			continue
		}
		if err == syscall.ELOOP || err == syscall.EISDIR {
			continue
		}
		if err != nil {
			return -1, err
		}
		var info syscall.Stat_t
		if err := syscall.Fstat(fd, &info); err != nil {
			syscall.Close(fd)
			return -1, err
		}
		if info.Mode&syscall.S_IFMT != syscall.S_IFREG || info.Ino != obj.TempInode || !tokenOnFile(fd, obj.TempToken) {
			syscall.Close(fd)
			continue
		}
		if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			syscall.Close(fd)
			return -1, errTempBusy
		}
		return fd, nil
	}
	if checked > 0 && missing == checked {
		return -1, syscall.ENOENT
	}
	return -1, errTempIdentity
}

func tokenOnFile(fd int, token string) bool {
	if got, err := fgetxattr(fd, tempTokenAttr); err == nil && got == token {
		return true
	}
	buf := make([]byte, len(tempPrefix(token)))
	n, err := syscall.Pread(fd, buf, 0)
	return err == nil && n == len(buf) && string(buf) == tempPrefix(token)
}

const (
	sysRenameat2    = 316
	renameNoReplace = 1
)

func linkAt(oldDir int, oldName string, newDir int, newName string) error {
	oldPtr, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPtr, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_LINKAT, uintptr(oldDir), uintptr(unsafe.Pointer(oldPtr)), uintptr(newDir), uintptr(unsafe.Pointer(newPtr)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func renameNoReplaceAt(oldDir int, oldName string, newDir int, newName string) error {
	oldPtr, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	newPtr, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(sysRenameat2, uintptr(oldDir), uintptr(unsafe.Pointer(oldPtr)), uintptr(newDir), uintptr(unsafe.Pointer(newPtr)), renameNoReplace, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// disposeExact moves one directory entry aside and deletes it only when the
// moved inode is the owned one. A different inode is renamed back. If the
// original name is no longer free, the other file stays under the quarantine
// name and the caller journals that name.
func (p *PathDestination) disposeExact(dir *os.File, name string, inode uint64) error {
	if name == "" || inode == 0 {
		return nil
	}
	hexPart, ok := tempHex(name)
	if !ok {
		return errTempIdentity
	}
	suffix := ".qrt"
	if strings.HasSuffix(name, ".own") {
		suffix = ".oqr"
	}
	hold := ".togavsu-" + hexPart + suffix
	err := renameNoReplaceAt(int(dir.Fd()), name, int(dir.Fd()), hold)
	if err == syscall.ENOENT {
		return nil
	}
	if err == syscall.EEXIST {
		return errTempIdentity
	}
	if err != nil {
		return err
	}
	fd, err := syscall.Openat(int(dir.Fd()), hold, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == syscall.ELOOP || err == syscall.EISDIR {
		restore := renameNoReplaceAt(int(dir.Fd()), hold, int(dir.Fd()), name)
		if restore == syscall.EEXIST || restore == syscall.ENOENT {
			return tempDisplacedError{hold: hold}
		}
		if restore != nil {
			return restore
		}
		return errTempIdentity
	}
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	var info syscall.Stat_t
	if err := syscall.Fstat(fd, &info); err != nil {
		return err
	}
	if info.Mode&syscall.S_IFMT != syscall.S_IFREG || info.Ino != inode {
		restore := renameNoReplaceAt(int(dir.Fd()), hold, int(dir.Fd()), name)
		if restore == syscall.EEXIST || restore == syscall.ENOENT {
			return tempDisplacedError{hold: hold}
		}
		if restore != nil {
			return restore
		}
		return errTempIdentity
	}
	if err := syscall.Unlinkat(int(dir.Fd()), hold); err != nil && err != syscall.ENOENT {
		return err
	}
	return nil
}

// acknowledgeTempGone syncs the directory and checks the name again. A sync
// failure or a name that is still present keeps the caller from forgetting it.
func (p *PathDestination) acknowledgeTempGone(dir *os.File, name string) error {
	if err := p.syncDirectory(dir, "delete"); err != nil {
		return fmt.Errorf("%w: directory sync after temp delete: %v", ErrRemoteUncertain, err)
	}
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == syscall.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	syscall.Close(fd)
	return errTempIdentity
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
	receiptAborted   = "aborted"
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
	// TempToken are recorded when that file is created, before its bytes are
	// copied. The token lives on the inode and stays valid through a partial
	// write. Recovery removes only that inode. releaseTemp lets a verified
	// removal or publication clear the record.
	Temp        string `json:"temp,omitempty"`
	TempInode   uint64 `json:"temp_inode,omitempty"`
	TempToken   string `json:"temp_token,omitempty"`
	TempLink    string `json:"temp_link,omitempty"`
	TempHold    string `json:"temp_hold,omitempty"`
	TempMtime   int64  `json:"temp_mtime,omitempty"`
	TempDigest  string `json:"temp_digest,omitempty"`
	releaseTemp bool
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
		if obj.State == receiptUncertain || obj.State == receiptPending || obj.State == receiptRejected || obj.State == receiptObserved || obj.State == receiptAborted || obj.Name == "" || strings.HasPrefix(obj.Name, "togavsu-destination-test") || !strings.HasSuffix(obj.Name, ".tgbk") {
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
				if !obj.releaseTemp && obj.Temp == "" {
					obj.Temp = all[i].Temp
					obj.TempInode = all[i].TempInode
					obj.TempToken = all[i].TempToken
					obj.TempLink = all[i].TempLink
					obj.TempHold = all[i].TempHold
				}
				obj.releaseTemp = false
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
				if obj.Temp != "" {
					kept = append(kept, obj)
				}
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

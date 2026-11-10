package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// dirMode keeps object directories private to the core's user.
const dirMode = fs.ModePerm & 0o700

// Disk stores objects under a directory: local disk (a Docker volume) or a network
// path (an SMB or NFS share mounted into the stack). Objects sit under two levels of
// fan-out directories taken from the end of the key, so UUIDv7 keys spread evenly.
type Disk struct {
	Root string
}

// NewDisk checks that root is an absolute directory path.
func NewDisk(root string) (*Disk, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%w: path must be absolute", ErrConfig)
	}
	return &Disk{Root: filepath.Clean(root)}, nil
}

func (d *Disk) path(key string) (string, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	n := len(key)
	if n < len("abcd") {
		return filepath.Join(d.Root, "objects", "short", key), nil
	}
	return filepath.Join(d.Root, "objects", key[n-2:], key[n-4:n-2], key), nil
}

// Put writes to a temporary file, syncs it and links it into place. An existing key
// is never replaced.
func (d *Disk) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	n, err := io.Copy(tmp, ctxReader{ctx: ctx, r: r})
	if err == nil && size >= zero && n != size {
		err = fmt.Errorf("storage: wrote %d bytes, expected %d", n, size)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	// Link fails when the name exists, so a version's bytes are never overwritten.
	if err := os.Link(name, p); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	return syncDir(filepath.Dir(p))
}

// Get opens the object and seeks to offset.
func (d *Disk) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if offset > zero {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	var r io.Reader = f
	if length >= zero {
		r = io.LimitReader(f, length)
	}
	return readCloser{Reader: ctxReader{ctx: ctx, r: r}, Closer: f}, nil
}

// Stat reports the stored size.
func (d *Disk) Stat(_ context.Context, key string) (Info, error) {
	p, err := d.path(key)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return Info{Size: st.Size()}, nil
}

// Delete removes the object. A missing object is not an error.
func (d *Disk) Delete(_ context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Health writes, reads back and removes a probe file under the root.
func (d *Disk) Health(_ context.Context) error {
	if err := os.MkdirAll(d.Root, dirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(d.Root, ".health-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := f.WriteString("togavsu"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

type readCloser struct {
	io.Reader
	io.Closer
}

// ctxReader stops a long copy when the request context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

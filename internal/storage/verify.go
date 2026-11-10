package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Verified reads a whole object and checks its length and SHA-256 at the end.
// A mismatch returns ErrIntegrity in place of io.EOF, so a caller that streams the
// bytes on never reports a changed object as a clean read.
type Verified struct {
	rc       io.ReadCloser
	h        hash.Hash
	n        int64
	wantSize int64
	wantSHA  string
	done     bool
	hashed   bool
	err      error
	// OnMismatch runs once when the check fails, before Read returns the error.
	OnMismatch func(err error)
}

// OpenVerified reads key from b and verifies it against size and sha (lowercase hex).
func OpenVerified(ctx context.Context, b Backend, key string, size int64, sha string) (*Verified, error) {
	rc, err := b.Get(ctx, key, zero, -1)
	if err != nil {
		return nil, err
	}
	return &Verified{rc: rc, h: sha256.New(), wantSize: size, wantSHA: sha}, nil
}

func (v *Verified) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.rc.Read(p)
	v.h.Write(p[:n])
	v.n += int64(n)
	if v.n > v.wantSize {
		return 0, v.fail(fmt.Errorf("%w: more than %d bytes", ErrIntegrity, v.wantSize))
	}
	// The hash is checked on the read that completes the object, and those bytes are
	// withheld on a mismatch, so a streamed copy never ends looking whole.
	if v.n == v.wantSize && !v.hashed {
		v.hashed = true
		if got := hex.EncodeToString(v.h.Sum(nil)); got != v.wantSHA {
			return 0, v.fail(fmt.Errorf("%w: sha256 %s, expected %s", ErrIntegrity, got, v.wantSHA))
		}
	}
	if errors.Is(err, io.EOF) {
		if v.n != v.wantSize {
			return 0, v.fail(fmt.Errorf("%w: read %d bytes, expected %d", ErrIntegrity, v.n, v.wantSize))
		}
		v.done = true
		return n, io.EOF
	}
	if err != nil {
		if errors.Is(err, ErrIntegrity) {
			return 0, v.fail(err)
		}
		return n, err
	}
	return n, nil
}

func (v *Verified) fail(err error) error {
	v.err = err
	if v.OnMismatch != nil {
		v.OnMismatch(err)
		v.OnMismatch = nil
	}
	return err
}

// Close releases the backend reader.
func (v *Verified) Close() error { return v.rc.Close() }

// Checked reports whether the whole object was read and matched.
func (v *Verified) Checked() bool { return v.done }

// Check reads key to the end and verifies it without keeping the bytes.
func Check(ctx context.Context, b Backend, key string, size int64, sha string) error {
	v, err := OpenVerified(ctx, b, key, size, sha)
	if err != nil {
		return err
	}
	defer v.Close()
	_, err = io.Copy(io.Discard, v)
	return err
}

// HashingReader hashes what passes through it. Sum is the lowercase hex SHA-256.
type HashingReader struct {
	R io.Reader
	h hash.Hash
	N int64
}

// NewHashingReader wraps r.
func NewHashingReader(r io.Reader) *HashingReader {
	return &HashingReader{R: r, h: sha256.New()}
}

func (h *HashingReader) Read(p []byte) (int, error) {
	n, err := h.R.Read(p)
	h.h.Write(p[:n])
	h.N += int64(n)
	return n, err
}

// Sum returns the digest of everything read so far.
func (h *HashingReader) Sum() string { return hex.EncodeToString(h.h.Sum(nil)) }

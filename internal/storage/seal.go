package storage

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Sealed encrypts every object with AES-256-GCM before it reaches the inner backend.
//
// Format: a header (magic, then a random salt of sha256.Size bytes) followed by
// segments of segmentSize plaintext bytes, each sealed on its own so a byte range can
// be read without the whole object. Each object gets its own key, HMAC-SHA256 of the
// master key and the salt. A segment's nonce and additional data bind the storage key,
// the segment index and whether it is the last segment, so a changed, reordered,
// truncated or swapped segment fails authentication.
type Sealed struct {
	Inner  Backend
	master []byte
}

const (
	sealMagic   = "TGVSEAL1"
	segmentSize = 1 << 16
)

// NewSealed wraps inner. key must be sha256.Size (32) bytes, the AES-256 width.
func NewSealed(inner Backend, key []byte) (*Sealed, error) {
	if len(key) != sha256.Size {
		return nil, fmt.Errorf("%w: storage key must be %d bytes", ErrConfig, sha256.Size)
	}
	return &Sealed{Inner: inner, master: append([]byte(nil), key...)}, nil
}

func headerSize() int64 { return int64(len(sealMagic) + sha256.Size) }

func (s *Sealed) aead(salt []byte) (cipher.AEAD, error) {
	mac := hmac.New(sha256.New, s.master)
	mac.Write([]byte("togavsu/storage/object/"))
	mac.Write(salt)
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func segmentAD(key string, index int64, last bool) []byte {
	ad := []byte(key)
	ad = binary.BigEndian.AppendUint64(ad, uint64(index))
	if last {
		return append(ad, "/last"...)
	}
	return append(ad, "/more"...)
}

func nonceFor(aead cipher.AEAD, salt, ad []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ad)
	return mac.Sum(nil)[:aead.NonceSize()]
}

// overhead is the GCM tag length added to every segment.
func overhead() int64 {
	block, _ := aes.NewCipher(make([]byte, sha256.Size))
	g, _ := cipher.NewGCM(block)
	return int64(g.Overhead())
}

// SealedSize is the stored length of a plaintext of size bytes.
func SealedSize(size int64) int64 {
	segs := (size + segmentSize - 1) / segmentSize
	if segs == zero {
		segs++
	}
	return headerSize() + size + segs*overhead()
}

// PlainSize inverts SealedSize. A length no plaintext could produce is an integrity error.
func PlainSize(stored int64) (int64, error) {
	body := stored - headerSize()
	full := segmentSize + overhead()
	if body < overhead() {
		return 0, ErrIntegrity
	}
	segs, rem := body/full, body%full
	if rem == zero {
		return segs * segmentSize, nil
	}
	if rem < overhead() {
		return 0, ErrIntegrity
	}
	return segs*segmentSize + rem - overhead(), nil
}

// Put seals r as it streams. size must be the exact plaintext length.
func (s *Sealed) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := CheckKey(key); err != nil {
		return err
	}
	if size < zero {
		return fmt.Errorf("%w: sealed put needs the size", ErrConfig)
	}
	salt := make([]byte, sha256.Size)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	aead, err := s.aead(salt)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(sealStream(pw, r, aead, key, salt, size))
	}()
	err = s.Inner.Put(ctx, key, pr, SealedSize(size))
	_ = pr.CloseWithError(errors.New("storage: put finished"))
	return err
}

func sealStream(w io.Writer, r io.Reader, aead cipher.AEAD, key string, salt []byte, size int64) error {
	if _, err := io.WriteString(w, sealMagic); err != nil {
		return err
	}
	if _, err := w.Write(salt); err != nil {
		return err
	}
	segs := (size + segmentSize - 1) / segmentSize
	if segs == zero {
		segs++
	}
	buf := make([]byte, segmentSize)
	out := make([]byte, 0, segmentSize+overhead())
	var done int64
	for i := int64(0); i < segs; i++ {
		want := size - done
		if want > segmentSize {
			want = segmentSize
		}
		if _, err := io.ReadFull(r, buf[:want]); err != nil {
			return fmt.Errorf("storage: plaintext shorter than %d bytes: %w", size, err)
		}
		done += want
		ad := segmentAD(key, i, i == segs-1)
		out = aead.Seal(out[:0], nonceFor(aead, salt, ad), buf[:want], ad)
		if _, err := w.Write(out); err != nil {
			return err
		}
	}
	// A reader that has more than size bytes is refused rather than silently cut.
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n > int(zero) {
		return fmt.Errorf("storage: plaintext longer than %d bytes", size)
	}
	return nil
}

// Stat reports the plaintext size.
func (s *Sealed) Stat(ctx context.Context, key string) (Info, error) {
	info, err := s.Inner.Stat(ctx, key)
	if err != nil {
		return Info{}, err
	}
	n, err := PlainSize(info.Size)
	if err != nil {
		return Info{}, err
	}
	return Info{Size: n}, nil
}

// Get opens only the segments the range needs and authenticates each one.
func (s *Sealed) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	if err := CheckKey(key); err != nil {
		return nil, err
	}
	info, err := s.Stat(ctx, key)
	if err != nil {
		return nil, err
	}
	size := info.Size
	if offset < zero || offset > size {
		return nil, fmt.Errorf("storage: offset %d outside %d bytes", offset, size)
	}
	end := size
	if length >= zero && offset+length < size {
		end = offset + length
	}
	head, err := readRange(ctx, s.Inner, key, zero, headerSize())
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(head, []byte(sealMagic)) {
		return nil, ErrIntegrity
	}
	salt := head[len(sealMagic):]
	aead, err := s.aead(salt)
	if err != nil {
		return nil, err
	}
	segs := (size + segmentSize - 1) / segmentSize
	if segs == zero {
		segs++
	}
	first := offset / segmentSize
	last := first
	if end > offset {
		last = (end - 1) / segmentSize
	}
	full := segmentSize + overhead()
	inner, err := s.Inner.Get(ctx, key, headerSize()+first*full, (last-first+1)*full)
	if err != nil {
		return nil, err
	}
	return &openReader{
		src: inner, aead: aead, salt: append([]byte(nil), salt...), key: key,
		index: first, lastSeg: last, totalSegs: segs, size: size,
		skip: offset - first*segmentSize, remain: end - offset,
	}, nil
}

type openReader struct {
	src       io.ReadCloser
	aead      cipher.AEAD
	salt      []byte
	key       string
	index     int64
	lastSeg   int64
	totalSegs int64
	size      int64
	skip      int64
	remain    int64
	plain     []byte
	err       error
	opened    bool // at least one segment was authenticated
}

func (o *openReader) Read(p []byte) (int, error) {
	for len(o.plain) == 0 {
		if o.err != nil {
			return 0, o.err
		}
		if o.remain <= zero || o.index > o.lastSeg {
			// An empty object still has one sealed segment; open it before the
			// first EOF so a forged or swapped empty object fails its tag check.
			if o.size == zero && !o.opened {
				o.err = o.next()
				continue
			}
			return 0, io.EOF
		}
		o.err = o.next()
	}
	n := copy(p, o.plain)
	o.plain = o.plain[n:]
	return n, nil
}

func (o *openReader) next() error {
	want := o.size - o.index*segmentSize
	if want > segmentSize {
		want = segmentSize
	}
	buf := make([]byte, want+overhead())
	if _, err := io.ReadFull(o.src, buf); err != nil {
		return fmt.Errorf("%w: segment %d is short", ErrIntegrity, o.index)
	}
	ad := segmentAD(o.key, o.index, o.index == o.totalSegs-1)
	plain, err := o.aead.Open(buf[:0], nonceFor(o.aead, o.salt, ad), buf, ad)
	if err != nil {
		return fmt.Errorf("%w: segment %d", ErrIntegrity, o.index)
	}
	o.opened = true
	o.index++
	plain = plain[o.skip:]
	o.skip = zero
	if int64(len(plain)) > o.remain {
		plain = plain[:o.remain]
	}
	o.remain -= int64(len(plain))
	o.plain = plain
	return nil
}

func (o *openReader) Close() error { return o.src.Close() }

// Delete removes the sealed object.
func (s *Sealed) Delete(ctx context.Context, key string) error { return s.Inner.Delete(ctx, key) }

// Health checks the inner backend.
func (s *Sealed) Health(ctx context.Context) error { return s.Inner.Health(ctx) }

func readRange(ctx context.Context, b Backend, key string, off, n int64) ([]byte, error) {
	rc, err := b.Get(ctx, key, off, n)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	buf := make([]byte, n)
	if _, err := io.ReadFull(rc, buf); err != nil {
		return nil, ErrIntegrity
	}
	return buf, nil
}

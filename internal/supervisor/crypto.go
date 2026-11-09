package supervisor

import (
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

const (
	backupMagic       = "TOGAVBK1"
	backupVersion     = byte(1)
	backupChunkSize   = 1 << 20
	kdfIterations     = 200_000
	minKDFIterations  = 10_000
	maxKDFIterations  = 5_000_000
	backupHeaderBytes = 8 + 1 + 4 + 16 + 4
)

// Encrypt writes an AES-256-GCM archive. The passphrase is stretched with
// PBKDF2-HMAC-SHA256. Chunks stay under backupChunkSize so a database dump
// is never held whole in memory.
func Encrypt(dst io.Writer, src io.Reader, passphrase string) error {
	if passphrase == "" {
		return ErrPassphraseRequired
	}
	salt := make([]byte, 16)
	prefix := make([]byte, 4)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	aead, err := newAEAD(passphrase, salt, kdfIterations)
	if err != nil {
		return err
	}
	header := make([]byte, 0, backupHeaderBytes)
	header = append(header, backupMagic...)
	header = append(header, backupVersion)
	var iter [4]byte
	binary.BigEndian.PutUint32(iter[:], kdfIterations)
	header = append(header, iter[:]...)
	header = append(header, salt...)
	header = append(header, prefix...)
	if _, err := dst.Write(header); err != nil {
		return err
	}
	buf := make([]byte, backupChunkSize)
	var counter uint64
	for {
		n, err := io.ReadFull(src, buf)
		switch {
		case err == io.EOF:
			return writeEnd(dst)
		case errors.Is(err, io.ErrUnexpectedEOF):
			if n > 0 {
				if err := writeChunk(dst, aead, prefix, &counter, buf[:n]); err != nil {
					return err
				}
			}
			return writeEnd(dst)
		case err != nil:
			return err
		default:
			if err := writeChunk(dst, aead, prefix, &counter, buf[:n]); err != nil {
				return err
			}
		}
	}
}

// Decrypt reverses Encrypt. A wrong passphrase and a tampered chunk both
// return ErrDecrypt. Callers should write to a file they can delete, because
// plaintext from earlier chunks is already flushed when a later chunk fails.
func Decrypt(dst io.Writer, src io.Reader, passphrase string) error {
	if passphrase == "" {
		return ErrPassphraseRequired
	}
	header := make([]byte, backupHeaderBytes)
	if _, err := io.ReadFull(src, header); err != nil {
		return ErrArchive
	}
	if string(header[:8]) != backupMagic || header[8] != backupVersion {
		return ErrArchive
	}
	iterations := binary.BigEndian.Uint32(header[9:13])
	if iterations < minKDFIterations || iterations > maxKDFIterations {
		return ErrArchive
	}
	salt := header[13:29]
	prefix := header[29:33]
	aead, err := newAEAD(passphrase, salt, int(iterations))
	if err != nil {
		return err
	}
	var counter uint64
	lenBuf := make([]byte, 4)
	for {
		if _, err := io.ReadFull(src, lenBuf); err != nil {
			return ErrArchive
		}
		n := binary.BigEndian.Uint32(lenBuf)
		if n == 0 {
			return nil
		}
		if n > backupChunkSize+16 {
			return ErrArchive
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(src, sealed); err != nil {
			return ErrArchive
		}
		counter++
		nonce := chunkNonce(prefix, counter)
		plain, err := aead.Open(nil, nonce, sealed, nil)
		if err != nil {
			return ErrDecrypt
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
	}
}

func newAEAD(passphrase string, salt []byte, iterations int) (cipher.AEAD, error) {
	key := pbkdf2Key([]byte(passphrase), salt, iterations, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func writeChunk(dst io.Writer, aead cipher.AEAD, prefix []byte, counter *uint64, plain []byte) error {
	*counter++
	sealed := aead.Seal(nil, chunkNonce(prefix, *counter), plain, nil)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(sealed)))
	if _, err := dst.Write(n[:]); err != nil {
		return err
	}
	_, err := dst.Write(sealed)
	return err
}

func writeEnd(dst io.Writer) error {
	var n [4]byte
	_, err := dst.Write(n[:])
	return err
}

func chunkNonce(prefix []byte, counter uint64) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint64(nonce[4:], counter)
	return nonce
}

func pbkdf2Key(password, salt []byte, iterations, keyLen int) []byte {
	if iterations < 1 || keyLen < 1 {
		return nil
	}
	hLen := sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	dk := make([]byte, 0, blocks*hLen)
	for i := 1; i <= blocks; i++ {
		dk = append(dk, pbkdf2Block(password, salt, iterations, i)...)
	}
	return dk[:keyLen]
}

func pbkdf2Block(password, salt []byte, iterations, blockNum int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(blockNum))
	mac.Write(b[:])
	u := mac.Sum(nil)
	t := append([]byte(nil), u...)
	for i := 1; i < iterations; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range t {
			t[j] ^= u[j]
		}
	}
	return t
}

func sha256Hex(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

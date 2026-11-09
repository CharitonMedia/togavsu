package supervisor

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
)

func TestEncryptRoundTrip(t *testing.T) {
	plain := bytes.Repeat([]byte("synthetic-row-"), 100_000)
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader(plain), "correct horse"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc.Bytes(), []byte("synthetic-row-")) {
		t.Fatal("ciphertext contains plaintext")
	}
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(enc.Bytes()), "correct horse"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatalf("round trip changed %d bytes into %d", len(plain), out.Len())
	}
}

func TestEncryptEmpty(t *testing.T) {
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader(nil), "phrase"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Decrypt(&out, bytes.NewReader(enc.Bytes()), "phrase"); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("empty archive restored %d bytes", out.Len())
	}
}

func TestDecryptWrongPassphrase(t *testing.T) {
	var enc bytes.Buffer
	if err := Encrypt(&enc, bytes.NewReader([]byte("secret")), "right"); err != nil {
		t.Fatal(err)
	}
	err := Decrypt(io.Discard, bytes.NewReader(enc.Bytes()), "wrong")
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v", err)
	}
}

func TestDecryptTamper(t *testing.T) {
	var enc bytes.Buffer
	payload := make([]byte, backupChunkSize+50)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := Encrypt(&enc, bytes.NewReader(payload), "right"); err != nil {
		t.Fatal(err)
	}
	raw := enc.Bytes()
	raw[len(raw)/2] ^= 0xff
	err := Decrypt(io.Discard, bytes.NewReader(raw), "right")
	if !errors.Is(err, ErrDecrypt) && !errors.Is(err, ErrArchive) {
		t.Fatalf("got %v", err)
	}
}

func TestEncryptRejectsEmptyPassphrase(t *testing.T) {
	err := Encrypt(io.Discard, bytes.NewReader([]byte("x")), "")
	if !errors.Is(err, ErrPassphraseRequired) {
		t.Fatalf("got %v", err)
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	err := Decrypt(io.Discard, bytes.NewReader([]byte("not a backup")), "phrase")
	if !errors.Is(err, ErrArchive) {
		t.Fatalf("got %v", err)
	}
}

func TestPBKDF2KnownAnswer(t *testing.T) {
	// PBKDF2-HMAC-SHA256 (P="password", S="salt", c=2, dkLen=32).
	got := pbkdf2Key([]byte("password"), []byte("salt"), 2, 32)
	want := "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"
	if hex := fmtHex(got); hex != want {
		t.Fatalf("pbkdf2 = %s", hex)
	}
}

func fmtHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}

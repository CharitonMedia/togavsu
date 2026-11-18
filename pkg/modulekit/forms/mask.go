package forms

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// CipherPrefix marks a masked-field answer value as AES-256-GCM ciphertext.
// The AES key itself lives in the module secret store under Field.SecretKey;
// only ciphertext is stored in the module's answers.
const CipherPrefix = "enc:v1:"

// KeyBytes is the AES-256 key width (fixed protocol size, not a firm setting).
func KeyBytes() int { return 32 }

var (
	ErrNoSecretKey    = errors.New("forms: masked field needs a secret_key")
	ErrBadCipher      = errors.New("forms: masked ciphertext is not valid")
	ErrBadKey         = errors.New("forms: secret store key must be 32 raw bytes (base64-encoded)")
	ErrSecretNotFound = errors.New("forms: secret not found")
)

// SecretStore is the calling module's view of the core module secret store
// (PUT/GET /api/v1/modules/{id}/secrets/{name}). The kit never talks to the
// core itself; the module supplies an implementation that carries its bearer.
type SecretStore interface {
	PutSecret(ctx context.Context, name, value string) error
	GetSecret(ctx context.Context, name string) (string, error)
}

// LoadKey reads the AES-256 key for name from store (standard-base64 of 32 bytes).
func LoadKey(ctx context.Context, store SecretStore, name string) ([]byte, error) {
	if name == "" {
		return nil, ErrNoSecretKey
	}
	raw, err := store.GetSecret(ctx, name)
	if err != nil {
		return nil, err
	}
	return decodeKey(raw)
}

// EnsureKey returns the AES-256 key for name, creating and storing one when missing.
// Stored value is standard-base64 of 32 random bytes. Only ErrSecretNotFound (or a
// wrapped equivalent) triggers key creation; transient store failures and malformed
// values are returned so existing sealed answers stay decryptable.
func EnsureKey(ctx context.Context, store SecretStore, name string) ([]byte, error) {
	if name == "" {
		return nil, ErrNoSecretKey
	}
	key, err := LoadKey(ctx, store, name)
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrSecretNotFound) {
		return nil, err
	}
	key = make([]byte, KeyBytes())
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)
	if err := store.PutSecret(ctx, name, encoded); err != nil {
		return nil, err
	}
	return key, nil
}

// EncryptMasked seals plaintext with AES-256-GCM. The key is loaded (or created)
// from store under keyName. Returns CipherPrefix + base64(nonce||ciphertext).
// A value that merely starts with CipherPrefix is still encrypted: sealed state is
// decided from stored answers (see SealAnswers), never from user input shape.
func EncryptMasked(ctx context.Context, store SecretStore, keyName, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	key, err := EnsureKey(ctx, store, keyName)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return CipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptMasked opens a value produced by EncryptMasked.
func DecryptMasked(ctx context.Context, store SecretStore, keyName, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, CipherPrefix) {
		return "", ErrBadCipher
	}
	key, err := LoadKey(ctx, store, keyName)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, CipherPrefix))
	if err != nil {
		return "", ErrBadCipher
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", ErrBadCipher
	}
	nonce, body := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", ErrBadCipher
	}
	return string(plain), nil
}

// SealAnswers encrypts every TypeMasked field value in answers (in place) using
// each field's SecretKey. Non-masked and already-sealed values are left alone.
func SealAnswers(ctx context.Context, store SecretStore, form Form, answers Answers) error {
	for _, track := range form.Tracks {
		for _, screen := range track.Screens {
			if err := sealFields(ctx, store, screen.Fields, answers); err != nil {
				return err
			}
		}
	}
	return nil
}

func sealFields(ctx context.Context, store SecretStore, fields []Field, answers Answers) error {
	for _, f := range fields {
		if f.Type == TypeMasked {
			ans := answers.Get(f.ID)
			if ans.Unknown || ans.Empty() {
				continue
			}
			plain := ans.StringValue()
			// Already sealed under this key: leave alone. A typed CipherPrefix that does
			// not decrypt is treated as plaintext and encrypted.
			if strings.HasPrefix(plain, CipherPrefix) {
				if _, err := DecryptMasked(ctx, store, f.SecretKey, plain); err == nil {
					continue
				}
			}
			sealed, err := EncryptMasked(ctx, store, f.SecretKey, plain)
			if err != nil {
				return err
			}
			answers.Set(f.ID, Answer{Value: sealed})
			continue
		}
		if f.Type == TypeRepeating {
			rows := answers.Get(f.ID).RowsValue()
			for i := range rows {
				row := Answers(rows[i])
				if err := sealFields(ctx, store, f.Fields, row); err != nil {
					return err
				}
				rows[i] = row
			}
			if rows != nil {
				answers.Set(f.ID, Answer{Value: rows})
			}
		}
	}
	return nil
}

func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != KeyBytes() {
		return nil, ErrBadKey
	}
	return key, nil
}

// MemorySecretStore is an in-process SecretStore for tests and local fakes.
type MemorySecretStore struct {
	m map[string]string
}

// NewMemorySecretStore returns an empty store.
func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{m: map[string]string{}}
}

// PutSecret implements SecretStore.
func (s *MemorySecretStore) PutSecret(_ context.Context, name, value string) error {
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[name] = value
	return nil
}

// GetSecret implements SecretStore.
func (s *MemorySecretStore) GetSecret(_ context.Context, name string) (string, error) {
	v, ok := s.m[name]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

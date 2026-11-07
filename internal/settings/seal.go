package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const sealPrefix = "enc:v1:"

// SealingStore encrypts values whose keys the secret predicate selects.
// AES-256-GCM uses the installer key from bootstrap. Empty values stay empty.
type SealingStore struct {
	inner  Store
	aead   cipher.AEAD
	secret func(string) bool
}

// NewSealingStore wraps inner. key must be 32 bytes.
func NewSealingStore(inner Store, key []byte, secret func(string) bool) (*SealingStore, error) {
	if inner == nil || secret == nil {
		return nil, errors.New("settings: sealing store requires an inner store")
	}
	if len(key) != 32 {
		return nil, errors.New("settings: encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SealingStore{inner: inner, aead: aead, secret: secret}, nil
}

// Get decrypts a sealed secret. A non-secret value is returned unchanged, even if it begins with the seal prefix.
func (s *SealingStore) Get(ctx context.Context, key string) (string, bool, error) {
	value, ok, err := s.inner.Get(ctx, key)
	if err != nil || !ok || !s.secret(key) || !strings.HasPrefix(value, sealPrefix) {
		return value, ok, err
	}
	plain, err := s.open(value)
	if err != nil {
		return "", false, err
	}
	return plain, true, nil
}

// Put seals secret values before they reach the inner store.
func (s *SealingStore) Put(ctx context.Context, key, value string, at time.Time, actor domain.ID) error {
	if s.secret(key) && value != "" {
		sealed, err := s.seal(value)
		if err != nil {
			return err
		}
		value = sealed
	}
	return s.inner.Put(ctx, key, value, at, actor)
}

// Delete removes the inner row.
func (s *SealingStore) Delete(ctx context.Context, key string) error {
	return s.inner.Delete(ctx, key)
}

// Transact wraps the inner transaction so secret values are sealed inside it.
func (s *SealingStore) Transact(ctx context.Context, fn func(Store) error) error {
	return s.inner.Transact(ctx, func(tx Store) error {
		return fn(&SealingStore{inner: tx, aead: s.aead, secret: s.secret})
	})
}

// LogChange forwards a durable audit write when the inner transaction records one.
func (s *SealingStore) LogChange(ctx context.Context, change Change) error {
	logger, ok := s.inner.(ChangeLogger)
	if !ok {
		return nil
	}
	return logger.LogChange(ctx, change)
}

// Raw returns the inner stored text. Tests use it to prove a secret is not plaintext.
func (s *SealingStore) Raw(ctx context.Context, key string) (string, bool, error) {
	return s.inner.Get(ctx, key)
}

func (s *SealingStore) seal(plain string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, []byte(plain), nil)
	return sealPrefix + base64.StdEncoding.EncodeToString(out), nil
}

func (s *SealingStore) open(stored string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealPrefix))
	if err != nil {
		return "", err
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("settings: sealed value is short")
	}
	plain, err := s.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

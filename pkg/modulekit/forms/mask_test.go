package forms

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEncryptDecryptMasked(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySecretStore()
	sealed, err := EncryptMasked(ctx, store, "forms/ssn", "900-00-0000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, CipherPrefix) {
		t.Fatalf("sealed = %q", sealed)
	}
	if _, ok := store.m["forms/ssn"]; !ok {
		t.Fatal("expected key in secret store")
	}
	plain, err := DecryptMasked(ctx, store, "forms/ssn", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "900-00-0000" {
		t.Fatalf("plain = %q", plain)
	}
	// Idempotent seal.
	again, err := EncryptMasked(ctx, store, "forms/ssn", sealed)
	if err != nil || again != sealed {
		t.Fatalf("re-encrypt = %q %v", again, err)
	}
}

func TestDecryptRejectsBadCipher(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySecretStore()
	_, _ = EnsureKey(ctx, store, "k")
	if _, err := DecryptMasked(ctx, store, "k", "not-cipher"); err != ErrBadCipher {
		t.Fatalf("err = %v", err)
	}
	if _, err := DecryptMasked(ctx, store, "k", CipherPrefix+"@@@@"); err != ErrBadCipher {
		t.Fatalf("err = %v", err)
	}
}

func TestSealAnswers(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySecretStore()
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn"},
		{ID: "name", Type: TypeText},
	}}}}}}
	answers := Answers{
		"ssn":  {Value: "900-00-0000"},
		"name": {Value: "Avery"},
	}
	if err := SealAnswers(ctx, store, form, answers); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(answers.Get("ssn").StringValue(), CipherPrefix) {
		t.Fatalf("ssn not sealed: %#v", answers["ssn"])
	}
	if answers.Get("name").StringValue() != "Avery" {
		t.Fatal("name should stay plaintext")
	}
}

func TestEncryptRequiresSecretKey(t *testing.T) {
	_, err := EncryptMasked(context.Background(), NewMemorySecretStore(), "", "x")
	if err != ErrNoSecretKey {
		t.Fatalf("err = %v", err)
	}
}

type flakySecretStore struct {
	inner    *MemorySecretStore
	getErr   error
	gets     int
	putCalls int
}

func (s *flakySecretStore) PutSecret(ctx context.Context, name, value string) error {
	s.putCalls++
	return s.inner.PutSecret(ctx, name, value)
}
func (s *flakySecretStore) GetSecret(ctx context.Context, name string) (string, error) {
	s.gets++
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.inner.GetSecret(ctx, name)
}

func TestEnsureKeyDoesNotReplaceOnTransientError(t *testing.T) {
	ctx := context.Background()
	inner := NewMemorySecretStore()
	key, err := EnsureKey(ctx, inner, "forms/ssn")
	if err != nil {
		t.Fatal(err)
	}
	flaky := &flakySecretStore{inner: inner, getErr: errors.New("network blip")}
	if _, err := EnsureKey(ctx, flaky, "forms/ssn"); err == nil || !strings.Contains(err.Error(), "network blip") {
		t.Fatalf("want transient error, got %v", err)
	}
	if flaky.putCalls != 0 {
		t.Fatalf("EnsureKey must not PutSecret on transient GetSecret failure")
	}
	again, err := LoadKey(ctx, inner, "forms/ssn")
	if err != nil || !bytes.Equal(again, key) {
		t.Fatalf("original key changed: %v", err)
	}
	// Malformed stored value must also refuse, not regenerate.
	_ = inner.PutSecret(ctx, "forms/bad", "not-base64")
	if _, err := EnsureKey(ctx, inner, "forms/bad"); err != ErrBadKey {
		t.Fatalf("want ErrBadKey, got %v", err)
	}
}

func TestEnsureKeyCreatesOnlyWhenMissing(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySecretStore()
	k1, err := EnsureKey(ctx, store, "k")
	if err != nil {
		t.Fatal(err)
	}
	k2, err := EnsureKey(ctx, store, "k")
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("second EnsureKey regenerated key")
	}
}

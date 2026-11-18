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
	// A typed CipherPrefix is still encrypted (sealed state comes from storage, not input).
	typed := CipherPrefix + "not-really-sealed"
	again, err := EncryptMasked(ctx, store, "forms/ssn", typed)
	if err != nil || again == typed || !strings.HasPrefix(again, CipherPrefix) {
		t.Fatalf("prefix-as-plaintext = %q %v", again, err)
	}
	out, err := DecryptMasked(ctx, store, "forms/ssn", again)
	if err != nil || out != typed {
		t.Fatalf("round-trip typed prefix: %q %v", out, err)
	}
}

func TestSealAnswersSkipsAlreadySealed(t *testing.T) {
	ctx := context.Background()
	store := NewMemorySecretStore()
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn"},
	}}}}}}
	sealed, err := EncryptMasked(ctx, store, "forms/ssn", "900-00-0000")
	if err != nil {
		t.Fatal(err)
	}
	answers := Answers{"ssn": {Value: sealed}}
	if err := SealAnswers(ctx, store, form, answers); err != nil {
		t.Fatal(err)
	}
	if answers.Get("ssn").StringValue() != sealed {
		t.Fatalf("already-sealed value changed: %#v", answers["ssn"])
	}
}

func TestMergeFormSubmitKeepsMaskedOnEmpty(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "ssn", Type: TypeMasked, SecretKey: "k"},
		{ID: "name", Type: TypeText},
	}}}}}}
	existing := Answers{"ssn": {Value: CipherPrefix + "abc"}, "name": {Value: "Avery"}}
	posted := Answers{"ssn": {Value: ""}, "name": {Value: "Blake"}}
	got := MergeFormSubmit(existing, posted, form)
	if got.Get("ssn").StringValue() != CipherPrefix+"abc" {
		t.Fatalf("empty masked post wiped ssn: %#v", got["ssn"])
	}
	if got.Get("name").StringValue() != "Blake" {
		t.Fatalf("name = %#v", got["name"])
	}
	cleared := MergeFormSubmit(existing, Answers{"ssn": {Value: ClearMaskedValue}}, form)
	if cleared.Get("ssn").StringValue() != "" {
		t.Fatalf("clear marker: %#v", cleared["ssn"])
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

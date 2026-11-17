package forms

import (
	"context"
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

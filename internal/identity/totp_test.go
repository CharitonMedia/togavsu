package identity

import (
	"encoding/base32"
	"testing"
	"time"
)

func TestRFC6238SHA1(t *testing.T) {
	secret := []byte("12345678901234567890")
	when := time.Unix(59, 0).UTC()
	counter := uint64(when.Unix() / totpPeriod())
	if got := hotp(secret, counter, 8); got != "94287082" {
		t.Fatalf("got %s", got)
	}
	if got := hotp(secret, counter, hotpDigits()); got != "287082" {
		t.Fatalf("got %s", got)
	}
}

func TestTOTPWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(59, 0).UTC()
	code := TOTPCode(secret, now)
	if _, ok := acceptedStep(secret, code, now); !ok {
		t.Fatal("current")
	}
	if _, ok := acceptedStep(secret, "000000", now); ok {
		t.Fatal("accepted a zero code")
	}
	earlier := now.Add(-time.Duration(totpPeriod()) * time.Second)
	if _, ok := acceptedStep(secret, TOTPCode(secret, earlier), now); !ok {
		t.Fatal("previous step")
	}
	tooOld := now.Add(-time.Duration(totpPeriod()*(totpSkew()+totpSkew()+totpSkew())) * time.Second)
	if _, ok := acceptedStep(secret, TOTPCode(secret, tooOld), now); ok {
		t.Fatal("old step")
	}
	enc := encodeSecret(secret)
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enc)
	if err != nil || string(raw) != string(secret) {
		t.Fatal(err)
	}
}

func TestPasswordHashHidesTheSecret(t *testing.T) {
	const password = "correct-horse-battery"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(password, hash) || VerifyPassword("wrong-horse-battery", hash) {
		t.Fatal("verify")
	}
	if len(hash) == 0 || contains(hash, password) {
		t.Fatal(hash)
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password")
	}
	if VerifyPassword(password, "$argon2id$v=19$m=99999999,t=2,p=1$aa$bb") {
		t.Fatal("hostile parameters")
	}
}

func contains(hay, needle string) bool {
	return len(needle) > 0 && len(hay) >= len(needle) && (hay == needle || len(hay) > 0 && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})())
}

package modulekit_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/marketplace"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// ProveKey's signature verifies the key on the marketplace service itself, and it signs
// nothing but that key's proof message.
func TestProveKeyVerifiesOnTheMarketplace(t *testing.T) {
	m, err := marketplace.Open(t.TempDir(), time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.RegisterAuthor("Kit Author", "kit@example.test", "a long enough password"); err != nil {
		t.Fatal(err)
	}
	tok, err := m.Login("kit@example.test", "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Author(tok)
	if err != nil {
		t.Fatal(err)
	}
	pub, priv := key(t)
	kv, err := m.AddKey(a, modulekit.PublicKeyPEM(pub))
	if err != nil {
		t.Fatal(err)
	}
	_, other := key(t)
	for name, msg := range map[string]string{
		"another key's proof": kv.ProofMessage,
		"package bytes":       "\x1f\x8b\x08",
		"no challenge":        "togavsu-marketplace-key-proof\n" + kv.KeyID + "\n",
		"two lines":           kv.ProofMessage + "\nmore",
	} {
		signer := priv
		if name == "another key's proof" {
			signer = other
		}
		if _, err := modulekit.ProveKey(signer, msg); !errors.Is(err, modulekit.ErrNotKeyProof) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	sig, err := modulekit.ProveKey(priv, kv.ProofMessage)
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.VerifyKey(a, kv.KeyID, sig)
	if err != nil || !got.Verified {
		t.Fatalf("verify: %+v %v", got, err)
	}
	if !ed25519.Verify(pub, []byte(kv.ProofMessage), mustB64(t, sig)) {
		t.Fatal("not a signature of the proof message")
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

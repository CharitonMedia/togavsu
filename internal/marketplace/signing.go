package marketplace

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
)

// Signing (PRD 9.3, 10.1, 10.6). A module package is signed with Ed25519 over its exact
// bytes. The signature proves who published the package, not that it works. A key is
// named by its key id, a fingerprint of the raw public key, so a firm and the
// marketplace name the same key the same way without trusting a URL.

// Verification results. They match the marketplace contract's x-togavsu-verification
// names, so the API reports the same word the contract test expects.
var (
	ErrDigestMismatch   = errors.New("digest_mismatch")
	ErrSignatureInvalid = errors.New("signature_invalid")
	ErrUntrustedKey     = errors.New("untrusted_key")
	ErrBadKey           = errors.New("bad_key")
)

const keyIDPrefix = "ed25519-"

// keyIDHexLen is the fingerprint length: the first 16 bytes of SHA-256, as hex.
const keyIDHexLen = sha256.Size

// KeyID is the fingerprint the marketplace and the core use to name pub.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return keyIDPrefix + hex.EncodeToString(sum[:])[:keyIDHexLen]
}

// ParsePublicKeyPEM reads one PKIX "PUBLIC KEY" block holding an Ed25519 key. Any other
// key type, trailing data or a second block is refused.
func ParsePublicKeyPEM(text string) (ed25519.PublicKey, error) {
	block, rest := pem.Decode([]byte(strings.TrimSpace(text)))
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, ErrBadKey
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, ErrBadKey
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, ErrBadKey
	}
	return pub, nil
}

// PublicKeyPEM encodes pub as a PKIX "PUBLIC KEY" block.
func PublicKeyPEM(pub ed25519.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// MarshalPKIXPublicKey fails only for unsupported key types.
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// Digest is the package digest in the contract's form, sha256:<hex>.
func Digest(pkg []byte) string {
	sum := sha256.Sum256(pkg)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Sign returns the base64 signature of pkg. Authors sign; the core and the service only
// verify. It is here so tests and author tooling use the same encoding.
func Sign(priv ed25519.PrivateKey, pkg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, pkg))
}

// VerifySignature checks a base64 Ed25519 signature of msg against pub.
func VerifySignature(pub ed25519.PublicKey, msg []byte, signature string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrSignatureInvalid
	}
	if !ed25519.Verify(pub, msg, sig) {
		return ErrSignatureInvalid
	}
	return nil
}

// KeyProofMessage is what an author signs to prove they hold the private key of a key
// they register (the marketplace's "verified signing key", PRD 10.2 and 10.6). The
// prefix keeps a proof from doubling as a package signature.
func KeyProofMessage(keyID, challenge string) []byte {
	return []byte("togavsu-marketplace-key-proof\n" + keyID + "\n" + challenge)
}

// TrustedKey is one signer a firm accepts: a key published through the marketplace or a
// trusted local key the admin added (PRD 9.3).
type TrustedKey struct {
	KeyID  string
	Public ed25519.PublicKey
	// Source is "marketplace" or "local".
	Source string
	Author string
}

// Verify checks a package before anything in it runs: the digest (when the caller names
// one), the signer is in trusted, and the signature over the bytes. It returns the key
// that signed it. The order is the contract's: digest, then signature, then trust; a
// signature by an unknown key is reported as untrusted because it cannot be checked.
func Verify(pkg []byte, digest, signature, keyID string, trusted []TrustedKey) (TrustedKey, error) {
	if digest != "" && digest != Digest(pkg) {
		return TrustedKey{}, ErrDigestMismatch
	}
	for _, k := range trusted {
		if k.KeyID != keyID {
			continue
		}
		if KeyID(k.Public) != k.KeyID {
			// A stored key whose id does not match its bytes is not that key.
			continue
		}
		if err := VerifySignature(k.Public, pkg, signature); err != nil {
			return TrustedKey{}, err
		}
		return k, nil
	}
	return TrustedKey{}, ErrUntrustedKey
}

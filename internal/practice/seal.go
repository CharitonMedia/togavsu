package practice

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

var errBadSeal = errors.New("practice: identifier is not sealed")

// sealPrefix matches the settings sealer so an installer key can open these values later.
const sealPrefix = "enc:v1:"

func sealIdentifier(key []byte, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if strings.HasPrefix(plain, sealPrefix) {
		if _, err := openIdentifier(key, plain); err != nil {
			return "", err
		}
		return plain, nil
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
	out := aead.Seal(nonce, nonce, []byte(plain), nil)
	return sealPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// openIdentifier returns the plaintext when stored is nonce plus AES-GCM data this key can open.
func openIdentifier(key []byte, stored string) (string, error) {
	if len(key) != 32 || !strings.HasPrefix(stored, sealPrefix) {
		return "", errBadSeal
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealPrefix))
	if err != nil {
		return "", errBadSeal
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errBadSeal
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", errBadSeal
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return "", errBadSeal
	}
	nonce, ciphertext := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errBadSeal
	}
	return string(plain), nil
}

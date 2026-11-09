package identity

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const sealPrefix = "enc:v1:"

func seal(key []byte, plain string) (string, error) {
	aead, err := aead(key)
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

func openSeal(key []byte, stored string) (string, error) {
	if !strings.HasPrefix(stored, sealPrefix) {
		return "", errors.New("identity: secret is not sealed")
	}
	aead, err := aead(key)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, sealPrefix))
	if err != nil {
		return "", err
	}
	ns := aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("identity: sealed value is short")
	}
	plain, err := aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func aead(key []byte) (cipher.AEAD, error) {
	if len(key) != aesKey() {
		return nil, errors.New("identity: encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func cryptoRead(buf []byte) (int, error) {
	return rand.Read(buf)
}

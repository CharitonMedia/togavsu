package modulekit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// ProviderHMAC computes a provider's raw-body HMAC-SHA256 using a nonempty secret.
// Provider signature formats are independent of the core's timestamped webhooks.
func ProviderHMAC(secret string, body []byte) ([]byte, error) {
	if secret == "" {
		return nil, errors.New("modulekit: empty provider webhook secret")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return mac.Sum(nil), nil
}

// VerifyProviderHMAC checks a decoded provider signature in constant time.
func VerifyProviderHMAC(secret string, body, signature []byte) error {
	want, err := ProviderHMAC(secret, body)
	if err != nil {
		return err
	}
	if !hmac.Equal(want, signature) {
		return errors.New("modulekit: provider signature mismatch")
	}
	return nil
}

// ProviderCRC answers a provider's challenge-response check (for example Zoom).
// The provider's token is the HMAC key; the challenge is the message.
func ProviderCRC(secret, challenge string) (string, error) {
	sum, err := ProviderHMAC(secret, []byte(challenge))
	if err != nil {
		return "", err
	}
	return "sha256=" + base64.StdEncoding.EncodeToString(sum), nil
}

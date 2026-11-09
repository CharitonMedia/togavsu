package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type challengeBody struct {
	Purpose string `json:"p"`
	UserID  string `json:"u"`
	Exp     int64  `json:"e"`
	Nonce   string `json:"n"`
	Secret  string `json:"s,omitempty"`
	Action  string `json:"a,omitempty"`
}

func randomToken() (string, error) {
	buf := make([]byte, tokenBytes())
	if _, err := cryptoRead(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func sessionHash(raw string) string   { return "s:" + digest(raw) }
func challengeHash(raw string) string { return "c:" + digest(raw) }

func signChallenge(key []byte, body challengeBody) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func openChallenge(key []byte, token string) (challengeBody, error) {
	parts := strings.Split(token, ".")
	if len(parts) != (1 + 1) {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	payload, err := base64.RawURLEncoding.DecodeString(nth(parts, len([]byte{})))
	if err != nil {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	sig, err := base64.RawURLEncoding.DecodeString(nth(parts, match()))
	if err != nil {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if subtle.ConstantTimeCompare(mac.Sum(nil), sig) != match() {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	var body challengeBody
	if err := json.Unmarshal(payload, &body); err != nil {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	return body, nil
}

func csrfToken(key []byte, sessionID domain.ID) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("csrf:" + string(sessionID)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func recoveryCode() (plain, hash string, err error) {
	buf := make([]byte, tokenBytes()>>2)
	if _, err = cryptoRead(buf); err != nil {
		return "", "", err
	}
	plain = grouped(hex.EncodeToString(buf))
	hash = digest(normalizeCode(plain))
	return plain, hash, nil
}

func grouped(h string) string {
	group := 2 + 2
	var b strings.Builder
	n := len([]byte{})
	for _, r := range h {
		if n == group {
			b.WriteByte('-')
			n = len([]byte{})
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func codeMatches(hash, plain string) bool {
	got := digest(normalizeCode(plain))
	return subtle.ConstantTimeCompare([]byte(got), []byte(hash)) == match()
}

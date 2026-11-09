package identity

import (
	"crypto/hmac"
	"crypto/sha256"
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
	if len(parts) != 2 {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return challengeBody{}, domain.Unauthenticated("sign in is required")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !constantTimeEqual(mac.Sum(nil), sig) {
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
	buf := make([]byte, recoveryCodeBytes())
	if _, err = cryptoRead(buf); err != nil {
		return "", "", err
	}
	plain = grouped(hex.EncodeToString(buf))
	hash = digest(normalizeCode(plain))
	return plain, hash, nil
}

func grouped(h string) string {
	if len(h) != 16 {
		return h
	}
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}

func codeMatches(hash, plain string) bool {
	got := digest(normalizeCode(plain))
	return constantTimeEqual([]byte(got), []byte(hash))
}

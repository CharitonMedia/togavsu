package practice

import (
	"crypto/rand"
	"encoding/hex"
)

const csrfCookie = "togavsu_practice_csrf"

func newToken() string {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "csrf-unavailable"
	}
	return hex.EncodeToString(buf[:])
}

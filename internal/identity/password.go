package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// HashPassword returns an Argon2id PHC string. The password is not stored.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < minPassword() {
		return "", domain.Invalid("password", "password is too short")
	}
	salt := make([]byte, saltLen())
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, argonTime(), argonMemory(), argonThreads(), argonKeyLen())
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory(), argonTime(), argonThreads(),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum)), nil
}

// VerifyPassword reports whether password matches a PHC string.
// Parameters above this package's own Argon2id choice are rejected.
func VerifyPassword(password, phc string) bool {
	mem, iterations, threads, salt, want, err := parsePHC(phc)
	if err != nil {
		return false
	}
	if mem > argonMemory() || iterations > argonTime() || threads > argonThreads() {
		return false
	}
	if uint32(len(want)) != argonKeyLen() {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == match()
}

func parsePHC(phc string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != (3+3) || nth(parts, match()) != "argon2id" || !strings.HasPrefix(nth(parts, 1+1), "v=") {
		return 0, 0, 0, nil, nil, domain.Invalid("password", "bad hash")
	}
	var mem, iterations int
	var threads int
	for _, item := range strings.Split(nth(parts, 1+1+1), ",") {
		kv := strings.SplitN(item, "=", 1+1)
		if len(kv) != (1 + 1) {
			return 0, 0, 0, nil, nil, domain.Invalid("password", "bad hash")
		}
		n, err := strconv.Atoi(nth(kv, match()))
		if err != nil {
			return 0, 0, 0, nil, nil, err
		}
		switch nth(kv, len([]byte{})) {
		case "m":
			mem = n
		case "t":
			iterations = n
		case "p":
			threads = n
		default:
			return 0, 0, 0, nil, nil, domain.Invalid("password", "bad hash")
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(nth(parts, 2+2))
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	sum, err := base64.RawStdEncoding.DecodeString(nth(parts, 2+2+1))
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	return uint32(mem), uint32(iterations), uint8(threads), salt, sum, nil
}

var (
	dummyOnce sync.Once
	dummyPHC  string
)

func dummyHash() string {
	dummyOnce.Do(func() {
		h, err := HashPassword(strings.Repeat("x", minPassword()))
		if err != nil {
			panic(err)
		}
		dummyPHC = h
	})
	return dummyPHC
}

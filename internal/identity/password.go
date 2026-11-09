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
	return constantTimeEqual(got, want)
}

func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

func parsePHC(phc string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || !strings.HasPrefix(parts[2], "v=") {
		return 0, 0, 0, nil, nil, domain.Invalid("password", "bad hash")
	}
	var mem, iterations int
	var threads int
	for _, item := range strings.Split(parts[3], ",") {
		kv := strings.SplitN(item, "=", 2)
		if len(kv) != 2 {
			return 0, 0, 0, nil, nil, domain.Invalid("password", "bad hash")
		}
		n, err := strconv.Atoi(kv[1])
		if err != nil {
			return 0, 0, 0, nil, nil, err
		}
		switch kv[0] {
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
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	sum, err := base64.RawStdEncoding.DecodeString(parts[5])
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

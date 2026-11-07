package domain

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ID is a canonical lowercase UUIDv7.
type ID string

// ErrID is returned when an identifier is missing or not a UUIDv7.
var ErrID = errors.New("invalid id")

// SystemUserID is the synthetic actor for reference rows written before any person exists.
var SystemUserID = systemUserID()

func systemUserID() ID {
	// 2026-01-01T00:00:00Z, fixed rand fields. Not a firm setting.
	return mustCompose(1_767_225_600_000, 0, 1)
}

// NewID returns a UUIDv7 from the current UTC time and crypto/rand.
func NewID() (ID, error) {
	var a [2]byte
	var b [8]byte
	if _, err := rand.Read(a[:]); err != nil {
		return "", err
	}
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	randA := (uint16(a[0])<<8 | uint16(a[1])) & 0x0FFF
	randB := binary.BigEndian.Uint64(b[:]) & ((1 << 62) - 1)
	return ComposeV7(uint64(time.Now().UnixMilli()), randA, randB)
}

// ComposeV7 builds a UUIDv7. unixMilli must fit in 48 bits, randA in 12 bits, and randB in 62 bits.
func ComposeV7(unixMilli uint64, randA uint16, randB uint64) (ID, error) {
	if unixMilli >= 1<<48 || randA >= 1<<12 || randB >= 1<<62 {
		return "", ErrID
	}
	var u [16]byte
	u[0] = byte(unixMilli >> 40)
	u[1] = byte(unixMilli >> 32)
	u[2] = byte(unixMilli >> 24)
	u[3] = byte(unixMilli >> 16)
	u[4] = byte(unixMilli >> 8)
	u[5] = byte(unixMilli)
	u[6] = 0x70 | byte(randA>>8)
	u[7] = byte(randA)
	u[8] = 0x80 | byte(randB>>56)
	u[9] = byte(randB >> 48)
	u[10] = byte(randB >> 40)
	u[11] = byte(randB >> 32)
	u[12] = byte(randB >> 24)
	u[13] = byte(randB >> 16)
	u[14] = byte(randB >> 8)
	u[15] = byte(randB)
	return ID(formatUUID(u)), nil
}

// ParseID accepts a canonical or uppercase UUIDv7 and returns the lowercase form.
func ParseID(s string) (ID, error) {
	raw := strings.TrimSpace(s)
	if len(raw) != 36 {
		return "", ErrID
	}
	hexOnly := strings.ReplaceAll(raw, "-", "")
	if len(hexOnly) != 32 {
		return "", ErrID
	}
	dec, err := hex.DecodeString(hexOnly)
	if err != nil || len(dec) != 16 {
		return "", ErrID
	}
	if dec[6]>>4 != 7 || dec[8]>>6 != 2 {
		return "", ErrID
	}
	var u [16]byte
	copy(u[:], dec)
	return ID(formatUUID(u)), nil
}

// String returns the canonical text.
func (id ID) String() string { return string(id) }

// IsZero reports whether the identifier is empty.
func (id ID) IsZero() bool { return id == "" }

// Time returns the UUIDv7 timestamp in UTC.
func (id ID) Time() (time.Time, error) {
	parsed, err := ParseID(string(id))
	if err != nil {
		return time.Time{}, err
	}
	hexOnly := strings.ReplaceAll(string(parsed), "-", "")
	dec, err := hex.DecodeString(hexOnly)
	if err != nil {
		return time.Time{}, err
	}
	ms := uint64(dec[0])<<40 | uint64(dec[1])<<32 | uint64(dec[2])<<24 | uint64(dec[3])<<16 | uint64(dec[4])<<8 | uint64(dec[5])
	return time.UnixMilli(int64(ms)).UTC(), nil
}

func formatUUID(u [16]byte) string {
	h := hex.EncodeToString(u[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

func mustCompose(ms uint64, a uint16, b uint64) ID {
	id, err := ComposeV7(ms, a, b)
	if err != nil {
		panic(err)
	}
	return id
}

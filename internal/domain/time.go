package domain

import (
	"errors"
	"time"
)

// StorageLayout is the fixed UTC text form used when a timestamp is stored as text.
// Database columns in this build store unix microseconds instead; this layout is for APIs and logs.
const StorageLayout = "2006-01-02T15:04:05.000000Z"

// ErrTime is returned when a timestamp or time zone cannot be interpreted.
var ErrTime = errors.New("invalid time")

// Now returns the current time in UTC.
func Now() time.Time { return time.Now().UTC() }

// FormatUTC formats t as UTC truncated to microseconds.
func FormatUTC(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format(StorageLayout)
}

// ParseUTC parses StorageLayout or RFC3339 and returns UTC.
func ParseUTC(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, ErrTime
	}
	if t, err := time.Parse(StorageLayout, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, ErrTime
	}
	return t.UTC(), nil
}

// UnixMicroUTC returns microseconds since the unix epoch in UTC.
func UnixMicroUTC(t time.Time) int64 {
	return t.UTC().UnixMicro()
}

// FromUnixMicro interprets microseconds since the unix epoch as UTC.
func FromUnixMicro(v int64) time.Time {
	return time.UnixMicro(v).UTC()
}

// LoadZone loads an IANA time zone. The empty name is UTC.
func LoadZone(name string) (*time.Location, error) {
	if name == "" || name == "UTC" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrTime
	}
	return loc, nil
}

// InZone converts t for display in the firm's time zone.
func InZone(t time.Time, zone string) (time.Time, error) {
	loc, err := LoadZone(zone)
	if err != nil {
		return time.Time{}, err
	}
	return t.In(loc), nil
}

package domain

import (
	"errors"
	"regexp"
)

// ErrPhone is returned when a phone number is not E.164.
var ErrPhone = errors.New("invalid phone")

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// ParseE164 returns a canonical E.164 phone number.
func ParseE164(s string) (string, error) {
	if !e164.MatchString(s) {
		return "", ErrPhone
	}
	return s, nil
}

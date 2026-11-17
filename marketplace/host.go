package marketplace

import (
	"errors"
	"net/url"
	"strings"
)

// ErrOutsideHostRefused means a test helper was pointed at a non-local host.
var ErrOutsideHostRefused = errors.New("marketplace: test helpers refuse any host that is not local")

// RequireLocalBaseURL refuses any base URL that is not a local test server.
// Every marketplace test helper that would dial an outside service must call this (MKT-07).
func RequireLocalBaseURL(base string) error {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ErrOutsideHostRefused
	}
	host := u.Hostname()
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		if strings.HasSuffix(host, ".localhost") {
			return nil
		}
		return ErrOutsideHostRefused
	}
}

package marketplace

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config is the service's own configuration, read from a JSON file named on the command
// line (config.example.json). Fields left out keep the defaults in fixed.json. The
// operator token is read from a file, a Docker secret, never from the config itself.
type Config struct {
	Listen                   string `json:"listen"`
	DataDir                  string `json:"data_dir"`
	OperatorTokenFile        string `json:"operator_token_file"`
	SecureCookies            bool   `json:"secure_cookies"`
	AuthorSessionHours       int    `json:"author_session_hours"`
	OperatorSessionMinutes   int    `json:"operator_session_minutes"`
	ReadHeaderTimeoutSeconds int    `json:"read_header_timeout_seconds"`
	MaxJSONBytes             int64  `json:"max_json_bytes"`
	MinOperatorTokenBytes    int    `json:"min_operator_token_bytes"`
}

//go:embed fixed.json
var fixedJSON []byte

// Defaults is the configuration with nothing overridden.
func Defaults() Config {
	var c Config
	if err := json.Unmarshal(fixedJSON, &c); err != nil {
		panic(fmt.Sprintf("marketplace: fixed.json: %v", err))
	}
	return c
}

// LoadConfig reads path over the defaults. Unknown fields are refused so a typo is not
// silently ignored.
func LoadConfig(path string) (Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("marketplace: %s: %w", path, err)
	}
	if c.DataDir == "" || c.Listen == "" || c.AuthorSessionHours <= int(0) || c.OperatorSessionMinutes <= int(0) {
		return c, errors.New("marketplace: listen, data_dir and positive session lengths are required")
	}
	return c, nil
}

// OperatorToken reads the operator's bearer token from its file. A short token is
// refused.
func (c Config) OperatorToken() (string, error) {
	b, err := os.ReadFile(c.OperatorTokenFile)
	if err != nil {
		return "", fmt.Errorf("marketplace: operator token: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if len(t) < c.MinOperatorTokenBytes {
		return "", fmt.Errorf("marketplace: the operator token must be at least %d bytes", c.MinOperatorTokenBytes)
	}
	return t, nil
}

func dur(unit string, n int) time.Duration {
	d, _ := time.ParseDuration(fmt.Sprintf("%d%s", n, unit))
	return d
}

// AuthorSession is how long an author's bearer token lasts.
func (c Config) AuthorSession() time.Duration { return dur("h", c.AuthorSessionHours) }

// OperatorSession is how long an operator's browser sign-in lasts.
func (c Config) OperatorSession() time.Duration { return dur("m", c.OperatorSessionMinutes) }

// ReadHeaderTimeout bounds a slow client.
func (c Config) ReadHeaderTimeout() time.Duration { return dur("s", c.ReadHeaderTimeoutSeconds) }

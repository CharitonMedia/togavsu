package bootstrap

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// EnvNames is the only environment the core reads. Anything adjustable after
// the database is open belongs in the settings registry.
var EnvNames = []string{
	"TOGAVSU_DATABASE_URL",
	"TOGAVSU_ENCRYPTION_KEY",
	"TOGAVSU_DOMAIN",
	"TOGAVSU_SUPERVISOR_SECRET",
}

// Config is the pre-database configuration. EncryptionKey is raw 32 bytes.
type Config struct {
	DatabaseURL      string
	EncryptionKey    []byte
	Domain           string
	SupervisorSecret string
}

var domainName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

// Load reads EnvNames from the process environment.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads EnvNames through getenv. Tests pass a stub.
func LoadFrom(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("bootstrap: getenv is required")
	}
	values := make(map[string]string, len(EnvNames))
	for _, name := range EnvNames {
		values[name] = strings.TrimSpace(getenv(name))
	}
	cfg := Config{
		DatabaseURL:      values["TOGAVSU_DATABASE_URL"],
		Domain:           values["TOGAVSU_DOMAIN"],
		SupervisorSecret: values["TOGAVSU_SUPERVISOR_SECRET"],
	}
	if !strings.HasPrefix(cfg.DatabaseURL, "postgres://") && !strings.HasPrefix(cfg.DatabaseURL, "postgresql://") {
		return Config{}, fmt.Errorf("bootstrap: database URL must start with postgres:// or postgresql://")
	}
	key, err := base64.StdEncoding.DecodeString(values["TOGAVSU_ENCRYPTION_KEY"])
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("bootstrap: encryption key must be standard base64 of 32 bytes")
	}
	cfg.EncryptionKey = key
	if !domainName.MatchString(cfg.Domain) {
		return Config{}, fmt.Errorf("bootstrap: domain must be a hostname")
	}
	if len(cfg.SupervisorSecret) < 16 {
		return Config{}, fmt.Errorf("bootstrap: supervisor secret must be at least 16 characters")
	}
	return cfg, nil
}

// Run loads configuration and reports that the HTTP service is not started.
// It returns 0 when the four bootstrap values are valid and 2 when they are not.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunFrom(args, os.Getenv, stdout, stderr)
}

// RunFrom is Run with an injectable environment reader.
func RunFrom(_ []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := LoadFrom(getenv)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	fmt.Fprintf(stdout, "togavsu core: bootstrap configuration accepted; domain=%s; HTTP service and migrations are not started by this skeleton\n", cfg.Domain)
	return 0
}

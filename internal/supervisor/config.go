package supervisor

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the bootstrap configuration the supervisor can read before the
// settings registry exists. Database URL, field key, domain, and the local
// API token are installer secrets. Tunables that the PRD puts on the admin
// page keep the PRD default here until core can push the live setting.
//
// ponytail: firm settings are environment values, ceiling: no settings table,
// upgrade: a SettingsReader backed by the core registry, trigger: the
// settings registry is present in the database this process can read.
type Config struct {
	Token              string
	Listen             string
	DataDir            string
	BackupDir          string
	Passphrase         string
	PassphraseFile     string
	DatabaseURL        string
	AdminDatabaseURL   string
	CaddyAdminURL      string
	CaddyAdminSocket   string
	UpstreamFile       string
	DockerSocket       string
	DockerNetwork      string
	AppSecretsVolume   string
	FirmTimezone       string
	ReleaseIndexURL    string
	CosignBin          string
	CosignKey          string
	UpdateWindowStart  string
	UpdateWindowEnd    string
	BackupTime         string
	TestRestoreDay     int
	LocalRetentionDays int
	LogRetentionDays   int
	LogDir             string
	UpdatesPaused      bool
}

func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

func LoadFrom(getenv func(string) string) (Config, error) {
	cfg := Config{
		Listen:             envDefault(getenv, "SUPERVISOR_LISTEN", ":8081"),
		DataDir:            envDefault(getenv, "SUPERVISOR_DATA", "/var/lib/togavsu"),
		CaddyAdminSocket:   envDefault(getenv, "CADDY_ADMIN_SOCKET", "/run/caddy-admin/admin.sock"),
		DockerSocket:       envDefault(getenv, "DOCKER_SOCKET", "/var/run/docker.sock"),
		DockerNetwork:      envDefault(getenv, "TOGAVSU_DOCKER_NETWORK", "togavsu_default"),
		AppSecretsVolume:   envDefault(getenv, "TOGAVSU_APPSECRETS_VOLUME", "togavsu_appsecrets"),
		FirmTimezone:       envDefault(getenv, "FIRM_TIMEZONE", "UTC"),
		ReleaseIndexURL:    getenv("TOGAVSU_RELEASE_INDEX"),
		CosignBin:          envDefault(getenv, "COSIGN_BIN", "cosign"),
		CosignKey:          getenv("COSIGN_KEY"),
		UpdateWindowStart:  envDefault(getenv, "UPDATE_WINDOW_START", "02:00"),
		UpdateWindowEnd:    envDefault(getenv, "UPDATE_WINDOW_END", "04:00"),
		BackupTime:         envDefault(getenv, "BACKUP_TIME", "01:00"),
		TestRestoreDay:     envInt(getenv, "TEST_RESTORE_DAY", 1),
		LocalRetentionDays: envInt(getenv, "BACKUP_RETENTION_DAYS", 30),
		LogRetentionDays:   envInt(getenv, "LOG_RETENTION_DAYS", 400),
		UpdatesPaused:      getenv("UPDATES_PAUSED") == "1" || strings.EqualFold(getenv("UPDATES_PAUSED"), "true"),
	}
	cfg.BackupDir = envDefault(getenv, "BACKUP_DIR", cfg.DataDir+"/backups")
	cfg.UpstreamFile = envDefault(getenv, "UPSTREAM_FILE", cfg.DataDir+"/upstream")
	cfg.LogDir = envDefault(getenv, "LOG_DIR", cfg.DataDir+"/logs")
	cfg.PassphraseFile = getenv("BACKUP_PASSPHRASE_FILE")
	var err error
	cfg.Passphrase, err = optionalSecret(getenv, "BACKUP_PASSPHRASE", "BACKUP_PASSPHRASE_FILE")
	if err != nil {
		return Config{}, err
	}
	cfg.Token, err = secret(getenv, "SUPERVISOR_TOKEN", "SUPERVISOR_TOKEN_FILE")
	if err != nil {
		return Config{}, err
	}
	if cfg.Token == "" {
		return Config{}, fmt.Errorf("SUPERVISOR_TOKEN: %w", ErrBadRequest)
	}
	cfg.DatabaseURL, err = secret(getenv, "DATABASE_URL", "DATABASE_URL_FILE")
	if err != nil {
		return Config{}, err
	}
	cfg.AdminDatabaseURL, err = secret(getenv, "ADMIN_DATABASE_URL", "ADMIN_DATABASE_URL_FILE")
	if err != nil {
		return Config{}, err
	}
	if _, err := ParseClock(cfg.UpdateWindowStart); err != nil {
		return Config{}, err
	}
	if _, err := ParseClock(cfg.UpdateWindowEnd); err != nil {
		return Config{}, err
	}
	if _, err := ParseClock(cfg.BackupTime); err != nil {
		return Config{}, err
	}
	if _, err := time.LoadLocation(cfg.FirmTimezone); err != nil {
		return Config{}, fmt.Errorf("FIRM_TIMEZONE: %w", ErrBadRequest)
	}
	return cfg, nil
}

// ReloadPassphrase reads the passphrase file on each backup so first-run can
// create it after the supervisor has started.
func (c Config) ReloadPassphrase() (string, error) {
	if c.PassphraseFile != "" {
		raw, err := os.ReadFile(c.PassphraseFile)
		if err != nil {
			if os.IsNotExist(err) {
				return "", ErrPassphraseRequired
			}
			return "", err
		}
		pass := strings.TrimSpace(string(raw))
		if pass == "" {
			return "", ErrPassphraseRequired
		}
		return pass, nil
	}
	if c.Passphrase == "" {
		return "", ErrPassphraseRequired
	}
	return c.Passphrase, nil
}

func (c Config) Schedule() (Schedule, error) {
	loc, err := time.LoadLocation(c.FirmTimezone)
	if err != nil {
		return Schedule{}, err
	}
	start, err := ParseClock(c.UpdateWindowStart)
	if err != nil {
		return Schedule{}, err
	}
	end, err := ParseClock(c.UpdateWindowEnd)
	if err != nil {
		return Schedule{}, err
	}
	backupAt, err := ParseClock(c.BackupTime)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{
		Location:       loc,
		BackupAt:       backupAt,
		Window:         Window{Start: start, End: end, Location: loc},
		TestRestoreDay: c.TestRestoreDay,
	}, nil
}

func secret(getenv func(string) string, envKey, fileKey string) (string, error) {
	return readSecret(getenv, envKey, fileKey, false)
}

func optionalSecret(getenv func(string) string, envKey, fileKey string) (string, error) {
	return readSecret(getenv, envKey, fileKey, true)
}

func readSecret(getenv func(string) string, envKey, fileKey string, missingOK bool) (string, error) {
	if path := getenv(fileKey); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			if missingOK && os.IsNotExist(err) {
				return "", nil
			}
			return "", fmt.Errorf("%s: %w", fileKey, err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return strings.TrimSpace(getenv(envKey)), nil
}

func envDefault(getenv func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(getenv func(string) string, key string, fallback int) int {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

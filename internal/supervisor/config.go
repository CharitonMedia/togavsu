package supervisor

import (
	"encoding/base64"
	"encoding/hex"
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
	FieldKey           []byte
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
	AdminSecretsVolume string
	BackupKeyVolume    string
	CoreDataVolume     string
	FirmTimezone       string
	ReleaseIndexURL    string
	CosignBin          string
	CosignKey          string
	CosignFlags        []string
	UpdateWindowStart  string
	UpdateWindowEnd    string
	BackupTime         string
	TestRestoreDay     int
	LocalRetentionDays int
	LogRetentionDays   int
	LogDir             string
	UpdatesPaused      bool
	// RestoreMaxBytes bounds an archive uploaded for a restore or fetched
	// from a destination. RESTORE_MAX_BYTES, default DefaultMaxImportBytes.
	RestoreMaxBytes int64
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
		AdminSecretsVolume: envDefault(getenv, "TOGAVSU_ADMINSECRETS_VOLUME", "togavsu_adminsecrets"),
		BackupKeyVolume:    envDefault(getenv, "TOGAVSU_BACKUPKEY_VOLUME", "togavsu_backupkey"),
		CoreDataVolume:     envDefault(getenv, "TOGAVSU_CORE_DATA_VOLUME", "togavsu_core_data"),
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
	cfg.FieldKey, err = loadFieldKey(getenv)
	if err != nil {
		return Config{}, err
	}
	cfg.AdminDatabaseURL, err = secret(getenv, "ADMIN_DATABASE_URL", "ADMIN_DATABASE_URL_FILE")
	if err != nil {
		return Config{}, err
	}
	cfg.CosignFlags, err = ParseCosignFlags(getenv("COSIGN_VERIFY_FLAGS"))
	if err != nil {
		return Config{}, err
	}
	cfg.RestoreMaxBytes = DefaultMaxImportBytes
	if v := strings.TrimSpace(getenv("RESTORE_MAX_BYTES")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, fmt.Errorf("RESTORE_MAX_BYTES: %w", ErrBadRequest)
		}
		cfg.RestoreMaxBytes = n
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

// loadFieldKey reads the installer field key. The file is the same one
// bootstrap writes: standard base64 of 32 bytes, or the earlier 64-character
// hex form. The key is not copied into the supervisor data volume.
func loadFieldKey(getenv func(string) string) ([]byte, error) {
	text, err := optionalSecret(getenv, "FIELD_KEY", "FIELD_KEY_FILE")
	if err != nil {
		return nil, err
	}
	if text == "" {
		return nil, nil
	}
	return decodeFieldKey(text)
}

func decodeFieldKey(text string) ([]byte, error) {
	if len(text) == 64 && isHex(text) {
		raw, err := hex.DecodeString(text)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("FIELD_KEY: %w", ErrBadRequest)
		}
		return raw, nil
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("FIELD_KEY: %w", ErrBadRequest)
	}
	return raw, nil
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
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

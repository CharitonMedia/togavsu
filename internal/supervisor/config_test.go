package supervisor

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRequiresTokenAndReadsFiles(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("local-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(func(k string) string {
		switch k {
		case "SUPERVISOR_TOKEN_FILE":
			return token
		case "FIRM_TIMEZONE":
			return "America/Chicago"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "local-token" {
		t.Fatalf("token %q", cfg.Token)
	}
	if cfg.UpdateWindowStart != "02:00" || cfg.BackupTime != "01:00" || cfg.LogRetentionDays != 400 || cfg.LocalRetentionDays != 30 {
		t.Fatalf("defaults %+v", cfg)
	}
	if cfg.FirmTimezone != "America/Chicago" {
		t.Fatal(cfg.FirmTimezone)
	}
	if _, err := LoadFrom(func(string) string { return "" }); err == nil {
		t.Fatal("missing token was accepted")
	}
}

func TestLoadAllowsMissingPassphraseFile(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("local-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing-passphrase")
	cfg, err := LoadFrom(func(k string) string {
		switch k {
		case "SUPERVISOR_TOKEN_FILE":
			return token
		case "BACKUP_PASSPHRASE_FILE":
			return missing
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ReloadPassphrase(); err == nil {
		t.Fatal("missing passphrase was accepted")
	}
}

func TestLoadFieldKeyAcceptsInstallerEncodings(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	dir := t.TempDir()
	token := filepath.Join(dir, "token")
	if err := os.WriteFile(token, []byte("local-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b64 := filepath.Join(dir, "field-b64")
	if err := os.WriteFile(b64, []byte(base64.StdEncoding.EncodeToString(raw)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hexPath := filepath.Join(dir, "field-hex")
	if err := os.WriteFile(hexPath, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{b64, hexPath} {
		cfg, err := LoadFrom(func(k string) string {
			switch k {
			case "SUPERVISOR_TOKEN_FILE":
				return token
			case "FIELD_KEY_FILE":
				return path
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cfg.FieldKey, raw) {
			t.Fatalf("key from %s = %x", path, cfg.FieldKey)
		}
	}
	if _, err := LoadFrom(func(k string) string {
		switch k {
		case "SUPERVISOR_TOKEN":
			return "local-token"
		case "FIELD_KEY":
			return "aaaa"
		default:
			return ""
		}
	}); err == nil {
		t.Fatal("short field key was accepted")
	}
}

func TestReloadPassphraseAppearsLater(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	cfg := Config{PassphraseFile: path}
	if _, err := cfg.ReloadPassphrase(); err == nil {
		t.Fatal("missing passphrase was accepted")
	}
	if err := os.WriteFile(path, []byte("set-at-first-run\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := cfg.ReloadPassphrase()
	if err != nil || got != "set-at-first-run" {
		t.Fatalf("%q %v", got, err)
	}
}

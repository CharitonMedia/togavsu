package bootstrap

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func validEnv() (map[string]string, []byte) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return map[string]string{
		"TOGAVSU_DATABASE_URL":      "postgres://user:secret-pass@127.0.0.1:5432/togavsu",
		"TOGAVSU_ENCRYPTION_KEY":    base64.StdEncoding.EncodeToString(key),
		"TOGAVSU_DOMAIN":            "firm.example",
		"TOGAVSU_SUPERVISOR_SECRET": "supervisor-secret-value",
	}, key
}

func TestLoadFrom(t *testing.T) {
	env, key := validEnv()
	var asked []string
	cfg, err := LoadFrom(func(name string) string {
		asked = append(asked, name)
		return env[name]
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, ",") != strings.Join(EnvNames, ",") {
		t.Fatalf("asked %v", asked)
	}
	if len(EnvNames) != 4 {
		t.Fatal(EnvNames)
	}
	if string(cfg.EncryptionKey) != string(key) || cfg.Domain != "firm.example" {
		t.Fatal(cfg.Domain)
	}
	bad := []map[string]string{
		replace(env, "TOGAVSU_DATABASE_URL", "mysql://localhost/togavsu"),
		replace(env, "TOGAVSU_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("short"))),
		replace(env, "TOGAVSU_ENCRYPTION_KEY", "not base64"),
		replace(env, "TOGAVSU_DOMAIN", "http://firm.example"),
		replace(env, "TOGAVSU_DOMAIN", ""),
		replace(env, "TOGAVSU_SUPERVISOR_SECRET", "short"),
	}
	for _, env := range bad {
		if _, err := LoadFrom(func(name string) string { return env[name] }); err == nil {
			t.Fatalf("accepted %#v", env)
		}
	}
}

func TestRunHidesSecrets(t *testing.T) {
	env, key := validEnv()
	var stdout, stderr bytes.Buffer
	code := RunFrom(nil, func(name string) string { return env[name] }, &stdout, &stderr)
	if code != 0 {
		t.Fatal(code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "domain=firm.example") || !strings.Contains(out, "not started") {
		t.Fatal(out)
	}
	if strings.Contains(out, "secret-pass") || strings.Contains(out, base64.StdEncoding.EncodeToString(key)) || strings.Contains(out, "supervisor-secret") {
		t.Fatal(out)
	}
	stdout.Reset()
	stderr.Reset()
	code = RunFrom(nil, func(string) string { return "" }, &stdout, &stderr)
	if code != 2 || stderr.Len() == 0 || stdout.Len() != 0 {
		t.Fatalf("code %d err %q", code, stderr.String())
	}
}

func replace(src map[string]string, key, value string) map[string]string {
	out := map[string]string{}
	for k, v := range src {
		out[k] = v
	}
	out[key] = value
	return out
}

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

// contractSettings returns every settings catalog entry in the published contract,
// keyed by setting key.
func contractSettings(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "v1", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			key, okKey := x["key"].(string)
			_, okType := x["type"].(string)
			_, okTab := x["tab"].(string)
			if okKey && okType && okTab {
				out[key] = x
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
	return out
}

// TestNewSettingsMatchTheContract checks that each setting this integration adds has
// one key, type, default and range in the runtime registry and the published contract.
func TestNewSettingsMatchTheContract(t *testing.T) {
	contract := contractSettings(t)
	defs := map[string]settings.Definition{}
	for _, d := range settings.Definitions() {
		defs[d.Key] = d
	}
	for _, key := range []string{"mail.smtp_timeout_seconds", "api.bulk_batch_max"} {
		d, ok := defs[key]
		if !ok {
			t.Fatalf("%s is not in the settings registry", key)
		}
		c, ok := contract[key]
		if !ok {
			t.Fatalf("%s is not in the published settings contract", key)
		}
		if d.Kind != settings.KindInt || c["type"] != "integer" {
			t.Fatalf("%s: registry kind %s, contract type %v, want int and integer", key, d.Kind, c["type"])
		}
		want, err := strconv.ParseFloat(d.Default, 64)
		if err != nil {
			t.Fatal(err)
		}
		if c["default"] != want {
			t.Fatalf("%s: registry default %s, contract default %v", key, d.Default, c["default"])
		}
		_, cMin := c["minimum"]
		_, cMax := c["maximum"]
		if d.HasMin != cMin || (cMin && c["minimum"] != float64(d.Min)) || d.HasMax != cMax || (cMax && c["maximum"] != float64(d.Max)) {
			t.Fatalf("%s: registry range %d (set %v) to %d (set %v), contract range %v to %v", key, d.Min, d.HasMin, d.Max, d.HasMax, c["minimum"], c["maximum"])
		}
	}
}

// Opus checkpoint P1: mail.smtp_starttls is registered under the contract's key, as a
// boolean that defaults to on.
func TestSTARTTLSSettingMatchesTheContract(t *testing.T) {
	contract := contractSettings(t)
	var d settings.Definition
	for _, def := range settings.Definitions() {
		if def.Key == "mail.smtp_starttls" {
			d = def
		}
	}
	c, ok := contract["mail.smtp_starttls"]
	if d.Key == "" || !ok {
		t.Fatalf("mail.smtp_starttls registered %v, in the contract %v", d.Key != "", ok)
	}
	if d.Kind != settings.KindBool || c["type"] != "boolean" || d.Default != "true" || c["default"] != true {
		t.Fatalf("registry %s default %q, contract %v default %v; want a boolean defaulting to true", d.Kind, d.Default, c["type"], c["default"])
	}
}

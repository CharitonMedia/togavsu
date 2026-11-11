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
	for _, key := range []string{"mail.smtp_timeout_seconds"} {
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
		if !d.HasMin || !d.HasMax || c["minimum"] != float64(d.Min) || c["maximum"] != float64(d.Max) {
			t.Fatalf("%s: registry range %d to %d, contract range %v to %v", key, d.Min, d.Max, c["minimum"], c["maximum"])
		}
	}
}

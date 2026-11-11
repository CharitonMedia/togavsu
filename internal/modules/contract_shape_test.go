package modules_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
)

// shapeOf checks v (decoded JSON) against a schema of the shipped OpenAPI contract:
// types, required fields, enums, anyOf and additionalProperties false. It covers the
// keywords the module schemas use, so a response outside the contract fails a test.
func shapeOf(t *testing.T, schema string, v any) error {
	t.Helper()
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	raw, ok := spec.Components.Schemas[schema]
	if !ok {
		t.Fatalf("the contract has no schema %s", schema)
	}
	var s map[string]any
	_ = json.Unmarshal(raw, &s)
	return checkShape(spec.Components.Schemas, s, v, schema)
}

func checkShape(all map[string]json.RawMessage, s map[string]any, v any, at string) error {
	if ref, ok := s["$ref"].(string); ok {
		var r map[string]any
		_ = json.Unmarshal(all[strings.TrimPrefix(ref, "#/components/schemas/")], &r)
		return checkShape(all, r, v, at)
	}
	if alts, ok := s["anyOf"].([]any); ok {
		var errs []string
		for _, alt := range alts {
			err := checkShape(all, alt.(map[string]any), v, at)
			if err == nil {
				return nil
			}
			errs = append(errs, err.Error())
		}
		return fmt.Errorf("%s matches no anyOf: %s", at, strings.Join(errs, "; "))
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || e == v
		}
		if !found {
			return fmt.Errorf("%s = %v is not in the enum %v", at, v, enum)
		}
	}
	switch s["type"] {
	case "null":
		if v != nil {
			return fmt.Errorf("%s should be null", at)
		}
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("%s should be a string, got %T", at, v)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s should be a boolean, got %T", at, v)
		}
	case "integer", "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("%s should be a number, got %T", at, v)
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s should be an array, got %T", at, v)
		}
		items, _ := s["items"].(map[string]any)
		for i, it := range arr {
			if err := checkShape(all, items, it, fmt.Sprintf("%s[%d]", at, i)); err != nil {
				return err
			}
		}
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s should be an object, got %T", at, v)
		}
		props, _ := s["properties"].(map[string]any)
		for _, r := range asStrings(s["required"]) {
			if _, ok := obj[r]; !ok {
				return fmt.Errorf("%s is missing required %s", at, r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, ok := props[k].(map[string]any)
			if !ok {
				if s["additionalProperties"] == false {
					return fmt.Errorf("%s has %s, which the contract doesn't allow", at, k)
				}
				continue
			}
			if err := checkShape(all, p, obj[k], at+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

func asStrings(v any) []string {
	var out []string
	for _, x := range asSlice(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// The shape checker itself: a Module with a status outside the enum, an extra field or a
// missing required field fails; the contract's shape passes.
func TestContractShapeChecker(t *testing.T) {
	good := map[string]any{"id": "m", "name": "M", "version": "1.0.0", "enabled": false, "replaces": nil, "status": "uninstalling",
		"permissions": []any{map[string]any{"scope": "matters:read", "required": true, "approved": true}}}
	if err := shapeOf(t, "Module", good); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(map[string]any){
		"removed status": func(m map[string]any) { m["status"] = "removed" },
		"extra field":    func(m map[string]any) { m["source"] = "local" },
		"missing field":  func(m map[string]any) { delete(m, "version") },
		"bad replaces":   func(m map[string]any) { m["replaces"] = "billing" },
	} {
		bad := map[string]any{}
		for k, v := range good {
			bad[k] = v
		}
		edit(bad)
		if err := shapeOf(t, "Module", bad); err == nil {
			t.Fatalf("%s passed the contract check", name)
		}
	}
	if err := shapeOf(t, "DomainHandler", map[string]any{"domain": "calendar", "module_id": "", "handler": "core"}); err != nil {
		t.Fatal(err)
	}
	if err := shapeOf(t, "TrustedKey", map[string]any{"id": "x", "name": "k", "key_id": "ab"}); err == nil {
		t.Fatal("a TrustedKey without public_key_pem passed")
	}
}

package modules

import (
	"context"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// SettingValue is one field of a module's settings form with its current value.
type SettingValue struct {
	SettingSpec
	Value     string `json:"value"`
	IsDefault bool   `json:"is_default"`
}

func (s *Service) settingsOf(ctx context.Context, module string) (map[string]string, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT key, value FROM core.module_settings WHERE module_id = $1`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// ModuleSettings returns the module's settings form. An admin sees secret fields as
// [redacted]; the module itself, through its own token, reads the real values.
func (s *Service) ModuleSettings(ctx context.Context, p domain.Principal, scopes []string, bearer bool, module string) ([]SettingValue, error) {
	own := p.ModuleID == module && p.ModuleID != ""
	if !own {
		if err := requireAdmin(p); err != nil {
			return nil, err
		}
		if bearer && !hasScope(scopes, "modules:read") {
			return nil, domain.Forbidden("this token lacks the modules:read scope")
		}
	}
	in, err := s.Installed(ctx, module)
	if err != nil {
		return nil, err
	}
	stored, err := s.settingsOf(ctx, module)
	if err != nil {
		return nil, err
	}
	out := []SettingValue{}
	for _, spec := range in.Manifest.Settings {
		v, ok := stored[spec.Key]
		sv := SettingValue{SettingSpec: spec, Value: spec.Default, IsDefault: !ok}
		if ok {
			if spec.Secret {
				if v, err = s.open(v); err != nil {
					return nil, err
				}
			}
			sv.Value = v
		}
		if spec.Secret && !own && sv.Value != "" {
			sv.Value = settings.Redacted
		}
		out = append(out, sv)
	}
	return out, nil
}

// SetModuleSettings validates and stores values against the manifest's settings schema.
// Each change goes to the audit log; secret values are sealed and audited as redacted.
func (s *Service) SetModuleSettings(ctx context.Context, p domain.Principal, module string, values map[string]string) ([]SettingValue, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	in, err := s.Installed(ctx, module)
	if err != nil {
		return nil, err
	}
	specs := map[string]SettingSpec{}
	for _, sp := range in.Manifest.Settings {
		specs[sp.Key] = sp
	}
	for k, v := range values {
		sp, ok := specs[k]
		if !ok {
			return nil, invalid(k, "the module has no setting "+k)
		}
		switch sp.Type {
		case "int":
			if _, err := strconv.Atoi(v); err != nil {
				return nil, invalid(k, "a whole number is required")
			}
		case "bool":
			if _, err := strconv.ParseBool(v); err != nil {
				return nil, invalid(k, "true or false is required")
			}
		case "enum":
			if !contains(sp.Options, v) {
				return nil, invalid(k, "choose one of the listed options")
			}
		}
		if sp.Required && v == "" {
			return nil, invalid(k, "this setting is required")
		}
	}
	stored, err := s.settingsOf(ctx, module)
	if err != nil {
		return nil, err
	}
	err = s.commit(ctx, func(ctx context.Context) error {
		for k, v := range values {
			sp := specs[k]
			val, oldShown, newShown := v, stored[k], v
			if sp.Secret {
				if val, err = s.seal(v); err != nil {
					return err
				}
				oldShown, newShown = settings.Redacted, settings.Redacted
			}
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			at := s.now().UnixMicro()
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_settings (id, created_at, updated_at, created_by, module_id, key, value) VALUES ($1,$2,$2,$3,$4,$5,$6)
ON CONFLICT (module_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, string(id), at, string(p.UserID), module, k, val); err != nil {
				return err
			}
			if err := s.audit(ctx, p, module, "setting:"+k, oldShown, newShown, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.ModuleSettings(ctx, p, nil, false, module)
}

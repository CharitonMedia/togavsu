package modules

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Secret limits (CORE-06).
const (
	maxSecretsPerModule = 512
	maxSecretBytes      = 16 << 10
)

var secretName = regexp.MustCompile(`^[a-z0-9/_-]{1,128}$`)

// ModuleSecret is the GET response: the clear value for the calling module only.
type ModuleSecret struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ownModuleSecret refuses anyone but the named module's own bearer (CORE-06).
// Handlers ignore a context token before calling, so p is the module principal.
func ownModuleSecret(p domain.Principal, module string) error {
	if p.Source != domain.SourceModule && p.Source != domain.SourceService {
		return domain.Forbidden("only the module's own token may use module secrets")
	}
	if p.ModuleID == "" || p.ModuleID != module {
		return domain.Forbidden("only the module's own token may use module secrets")
	}
	if !p.UserID.IsZero() {
		return domain.Forbidden("module secrets ignore a context token")
	}
	return nil
}

// PutSecret stores a sealed secret for the calling module (putModuleSecret).
func (s *Service) PutSecret(ctx context.Context, p domain.Principal, module, name, value string) error {
	if err := ownModuleSecret(p, module); err != nil {
		return err
	}
	if !secretName.MatchString(name) {
		return domain.Invalid("name", "secret names match ^[a-z0-9/_-]{1,128}$")
	}
	if value == "" || len(value) > maxSecretBytes {
		return domain.Invalid("value", "secret value is required and at most 16 KiB")
	}
	if _, err := s.Installed(ctx, module); err != nil {
		return domain.NotFound("module")
	}
	sealed, err := s.seal(value)
	if err != nil {
		return err
	}
	now := s.now().UnixMicro()
	return s.commit(ctx, func(ctx context.Context) error {
		var n int
		if err := s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM core.module_secrets WHERE module_id = $1`, module).Scan(&n); err != nil {
			return err
		}
		var exists int
		if err := s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM core.module_secrets WHERE module_id = $1 AND name = $2`, module, name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 && n >= maxSecretsPerModule {
			return domain.Invalid("name", "a module may store at most 512 secrets")
		}
		if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_secrets (module_id, name, value, created_at, updated_at)
VALUES ($1,$2,$3,$4,$4)
ON CONFLICT (module_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
			module, name, sealed, now); err != nil {
			return err
		}
		_, err := s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_secret", EntityID: domain.ID(module), Field: "name", New: name, Reason: "putModuleSecret",
		})
		return err
	})
}

// GetSecret returns one secret for the calling module (getModuleSecret).
func (s *Service) GetSecret(ctx context.Context, p domain.Principal, module, name string) (ModuleSecret, error) {
	if err := ownModuleSecret(p, module); err != nil {
		return ModuleSecret{}, err
	}
	if !secretName.MatchString(name) {
		return ModuleSecret{}, domain.NotFound("secret")
	}
	var sealed string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT value FROM core.module_secrets WHERE module_id = $1 AND name = $2`, module, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrNotFound) {
		return ModuleSecret{}, domain.NotFound("secret")
	}
	if err != nil {
		return ModuleSecret{}, err
	}
	plain, err := s.open(sealed)
	if err != nil {
		return ModuleSecret{}, err
	}
	return ModuleSecret{Name: name, Value: plain}, nil
}

// DeleteSecret removes one secret for the calling module (deleteModuleSecret).
func (s *Service) DeleteSecret(ctx context.Context, p domain.Principal, module, name string) error {
	if err := ownModuleSecret(p, module); err != nil {
		return err
	}
	if !secretName.MatchString(name) {
		return domain.NotFound("secret")
	}
	return s.commit(ctx, func(ctx context.Context) error {
		res, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_secrets WHERE module_id = $1 AND name = $2`, module, name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return domain.NotFound("secret")
		}
		_, err = s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_secret", EntityID: domain.ID(module), Field: "name", Old: name, Reason: "deleteModuleSecret",
		})
		return err
	})
}

// deleteModuleSecrets removes every secret for module (uninstall with delete data).
func (s *Service) deleteModuleSecrets(ctx context.Context, module string) error {
	_, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_secrets WHERE module_id = $1`, module)
	return err
}

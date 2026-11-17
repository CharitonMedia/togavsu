package modules

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// SystemMailRoute is the enabled module that receives system mail POSTs (CORE-05).
type SystemMailRoute struct {
	ModuleID string
	Path     string
	BaseURL  string
	Secret   string
}

// SystemMailRoute returns the enabled module that declares container.system_mail with
// mail:system approved. ok is false when none is configured.
func (s *Service) SystemMailRoute(ctx context.Context) (SystemMailRoute, bool, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return SystemMailRoute{}, false, err
	}
	var found *Installed
	for _, in := range mods {
		path := strings.TrimSpace(in.Manifest.Container.SystemMail)
		if path == "" || !in.Approved["mail:system"] {
			continue
		}
		if found != nil {
			return SystemMailRoute{}, false, domain.Conflict("more than one enabled module declares container.system_mail")
		}
		found = in
	}
	if found == nil || s.d.Runtime == nil {
		return SystemMailRoute{}, false, nil
	}
	row, err := s.row(ctx, found.ID)
	if err != nil {
		return SystemMailRoute{}, false, err
	}
	secret, err := s.open(row.WebhookSecret)
	if err != nil {
		return SystemMailRoute{}, false, err
	}
	return SystemMailRoute{
		ModuleID: found.ID,
		Path:     strings.TrimSpace(found.Manifest.Container.SystemMail),
		BaseURL:  strings.TrimRight(s.d.Runtime.BaseURL(found.ID), "/"),
		Secret:   secret,
	}, true, nil
}

// OutboxAlertModule returns the module_id for a module_alert outbox row when known.
func (s *Service) OutboxAlertModule(ctx context.Context, outboxID string) (string, error) {
	var mod string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT a.module_id FROM mail_outbox o
JOIN core.module_alerts a ON a.id = o.record_id
WHERE o.id = $1 AND o.kind = 'module_alert'`, outboxID).Scan(&mod)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return mod, err
}

// checkSystemMailUnique refuses a second install that declares container.system_mail.
func (s *Service) checkSystemMailUnique(ctx context.Context, m Manifest) error {
	if strings.TrimSpace(m.Container.SystemMail) == "" {
		return nil
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, package_id FROM core.modules
WHERE id <> $1 AND status NOT IN ('removed', 'cancelled')`, m.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, pid string
		if err := rows.Scan(&id, &pid); err != nil {
			return err
		}
		if pid == "" {
			continue
		}
		pkg, err := s.loadPackage(ctx, pid)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		parsed, err := ReadPackage(pkg.Archive)
		if err != nil {
			return err
		}
		if strings.TrimSpace(parsed.Manifest.Container.SystemMail) != "" {
			return domain.Conflict("module " + id + " already declares container.system_mail")
		}
	}
	return rows.Err()
}

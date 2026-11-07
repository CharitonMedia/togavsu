package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Setup is the first-run state the identity wave finishes.
type Setup struct {
	FirmName   string
	AdminUsers int
	AdminTOTP  int
	Complete   bool
}

// ReadSetup reports whether firm name, an active admin, and confirmed admin two-factor are present.
func ReadSetup(ctx context.Context, db *sql.DB) (Setup, error) {
	var out Setup
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'firm.name'`).Scan(&out.FirmName)
	if errors.Is(err, sql.ErrNoRows) {
		out.FirmName = ""
	} else if err != nil {
		return Setup{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT roles FROM users WHERE active = 1`)
	if err != nil {
		return Setup{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return Setup{}, err
		}
		roles, err := domain.ParseRoles(raw)
		if err != nil {
			return Setup{}, err
		}
		for _, role := range roles {
			if role == domain.RoleAdmin {
				out.AdminUsers++
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return Setup{}, err
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM totp_secrets ts JOIN users u ON u.id = ts.user_id WHERE u.active = 1 AND ts.confirmed = 1 AND u.roles LIKE '%"admin"%'`).Scan(&out.AdminTOTP); err != nil {
		return Setup{}, err
	}
	out.Complete = out.FirmName != "" && out.AdminUsers > 0 && out.AdminTOTP > 0
	return out, nil
}

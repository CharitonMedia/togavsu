package dashboard

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the dashboard migrations (ui range, 900-949). They run after the
// core migrations, in schema core, under the core owner.
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// PG is the Postgres Store on the core pool (search_path core).
type PG struct{ DB *sql.DB }

func micro(t time.Time) int64 { return t.UTC().UnixMicro() }

func fromMicro(v int64) time.Time { return time.UnixMicro(v).UTC() }

func encodeWidgets(w []LayoutWidget) (string, error) {
	if w == nil {
		w = []LayoutWidget{}
	}
	b, err := json.Marshal(w)
	return string(b), err
}

func decodeWidgets(s string) ([]LayoutWidget, error) {
	var w []LayoutWidget
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return nil, err
	}
	return w, nil
}

func (s PG) Working(ctx context.Context, user domain.ID) ([]LayoutWidget, bool, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT widgets FROM home_working_layouts WHERE user_id = $1`, string(user)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	w, err := decodeWidgets(raw)
	return w, err == nil, err
}

func (s PG) PutWorking(ctx context.Context, user domain.ID, widgets []LayoutWidget, at time.Time) error {
	raw, err := encodeWidgets(widgets)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO home_working_layouts (user_id, updated_at, widgets) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = excluded.updated_at, widgets = excluded.widgets`,
		string(user), micro(at), raw)
	return err
}

const layoutCols = `id, created_at, updated_at, created_by, user_id, name, (is_default = 1), slot, widgets`
const layoutInsertCols = `id, created_at, updated_at, created_by, user_id, name, is_default, slot, widgets`

func scanLayout(row interface{ Scan(...any) error }) (Layout, error) {
	var l Layout
	var created, updated int64
	var id, by, user, raw string
	if err := row.Scan(&id, &created, &updated, &by, &user, &l.Name, &l.Default, &l.Slot, &raw); err != nil {
		return Layout{}, err
	}
	w, err := decodeWidgets(raw)
	if err != nil {
		return Layout{}, err
	}
	l.ID, l.CreatedAt, l.UpdatedAt, l.CreatedBy, l.UserID = domain.ID(id), fromMicro(created), fromMicro(updated), domain.ID(by), domain.ID(user)
	l.Widgets = w
	return l, nil
}

func (s PG) Layouts(ctx context.Context, user domain.ID) ([]Layout, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+layoutCols+` FROM home_layouts WHERE user_id = $1 ORDER BY slot`, string(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Layout
	for rows.Next() {
		l, err := scanLayout(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func isUnique(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && (constraint == "" || pg.ConstraintName == constraint)
}

func (s PG) InsertLayout(ctx context.Context, l Layout) error {
	raw, err := encodeWidgets(l.Widgets)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO home_layouts (`+layoutInsertCols+`) VALUES ($1, $2, $2, $3, $4, $5, 0, $6, $7)`,
		string(l.ID), micro(l.CreatedAt), string(l.CreatedBy), string(l.UserID), l.Name, l.Slot, raw)
	switch {
	case isUnique(err, "home_layouts_user_id_slot_key"):
		return ErrSlotTaken
	case isUnique(err, "home_layouts_user_id_name_key"):
		return domain.Conflict("a saved layout already has that name")
	}
	return err
}

func (s PG) UpdateLayout(ctx context.Context, l Layout) error {
	raw, err := encodeWidgets(l.Widgets)
	if err != nil {
		return err
	}
	var id string
	err = s.DB.QueryRowContext(ctx, `UPDATE home_layouts SET name = $1, widgets = $2, updated_at = $3 WHERE id = $4 AND user_id = $5 RETURNING id`,
		l.Name, raw, micro(l.UpdatedAt), string(l.ID), string(l.UserID)).Scan(&id)
	if isUnique(err, "home_layouts_user_id_name_key") {
		return domain.Conflict("a saved layout already has that name")
	}
	return notFound(err)
}

func (s PG) DeleteLayout(ctx context.Context, user, id domain.ID) error {
	var gone string
	return notFound(s.DB.QueryRowContext(ctx, `DELETE FROM home_layouts WHERE id = $1 AND user_id = $2 RETURNING id`, string(id), string(user)).Scan(&gone))
}

func (s PG) SetDefault(ctx context.Context, user, id domain.ID, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE home_layouts SET is_default = 0, updated_at = $1 WHERE user_id = $2 AND is_default = 1`, micro(at), string(user)); err != nil {
		return err
	}
	if !id.IsZero() {
		var marked string
		if err := notFound(tx.QueryRowContext(ctx, `UPDATE home_layouts SET is_default = 1, updated_at = $1 WHERE id = $2 AND user_id = $3 RETURNING id`, micro(at), string(id), string(user)).Scan(&marked)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// notFound maps a RETURNING query that touched no row to ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s PG) RoleLayout(ctx context.Context, role domain.Role) (RoleLayout, bool, error) {
	var raw, by string
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT widgets, updated_at, updated_by FROM role_layouts WHERE role = $1`, string(role)).Scan(&raw, &at, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return RoleLayout{}, false, nil
	}
	if err != nil {
		return RoleLayout{}, false, err
	}
	w, err := decodeWidgets(raw)
	if err != nil {
		return RoleLayout{}, false, err
	}
	return RoleLayout{Role: role, Widgets: w, UpdatedAt: fromMicro(at), UpdatedBy: domain.ID(by)}, true, nil
}

func (s PG) PutRoleLayout(ctx context.Context, l RoleLayout) error {
	raw, err := encodeWidgets(l.Widgets)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO role_layouts (role, updated_at, updated_by, widgets) VALUES ($1, $2, $3, $4)
		ON CONFLICT (role) DO UPDATE SET updated_at = excluded.updated_at, updated_by = excluded.updated_by, widgets = excluded.widgets`,
		string(l.Role), micro(l.UpdatedAt), string(l.UpdatedBy), raw)
	return err
}

func (s PG) Prefs(ctx context.Context, user domain.ID) (Prefs, bool, error) {
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT prefs FROM user_preferences WHERE user_id = $1`, string(user)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Prefs{}, false, nil
	}
	if err != nil {
		return Prefs{}, false, err
	}
	var p Prefs
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return Prefs{}, false, err
	}
	return p, true, nil
}

func (s PG) PutPrefs(ctx context.Context, user domain.ID, p Prefs, at time.Time) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO user_preferences (user_id, updated_at, prefs) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET updated_at = excluded.updated_at, prefs = excluded.prefs`, string(user), micro(at), string(raw))
	return err
}

func (s PG) NotificationPrefs(ctx context.Context, user domain.ID) ([]NotificationPref, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT kind, (in_app = 1), (email = 1) FROM notification_preferences WHERE user_id = $1 ORDER BY kind`, string(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NotificationPref
	for rows.Next() {
		var p NotificationPref
		if err := rows.Scan(&p.Kind, &p.InApp, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s PG) PutNotificationPrefs(ctx context.Context, user domain.ID, prefs []NotificationPref, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_preferences WHERE user_id = $1`, string(user)); err != nil {
		return err
	}
	for _, p := range prefs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_preferences (user_id, kind, updated_at, in_app, email)
			VALUES ($1, $2, $3, CASE WHEN $4 THEN 1 ELSE 0 END, CASE WHEN $5 THEN 1 ELSE 0 END)`,
			string(user), p.Kind, micro(at), p.InApp, p.Email); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s PG) InsertProblem(ctx context.Context, r ProblemReport) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO problem_reports (id, created_at, updated_at, created_by, user_id, page, message) VALUES ($1, $2, $2, $3, $4, $5, $6)`,
		string(r.ID), micro(r.CreatedAt), string(r.CreatedBy), string(r.UserID), r.Page, r.Message)
	return err
}

func (s PG) Problems(ctx context.Context) ([]ProblemReport, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, user_id, page, message FROM problem_reports ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProblemReport
	for rows.Next() {
		var r ProblemReport
		var id, by, user string
		var c, u int64
		if err := rows.Scan(&id, &c, &u, &by, &user, &r.Page, &r.Message); err != nil {
			return nil, err
		}
		r.ID, r.CreatedAt, r.UpdatedAt, r.CreatedBy, r.UserID = domain.ID(id), fromMicro(c), fromMicro(u), domain.ID(by), domain.ID(user)
		out = append(out, r)
	}
	return out, rows.Err()
}

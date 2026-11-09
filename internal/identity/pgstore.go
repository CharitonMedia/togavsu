package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// PG is the Postgres store for the identity tables in the core schema.
type PG struct{ db *sql.DB }

// NewPG returns a store. The pool's search_path must include core.
func NewPG(sqlDB *sql.DB) *PG { return &PG{db: sqlDB} }

func (p *PG) Users() repository.Users                   { return pgUsers{p} }
func (p *PG) Sessions() repository.Sessions             { return pgSessions{p} }
func (p *PG) Passwords() repository.PasswordCredentials { return pgPasswords{p} }
func (p *PG) TOTP() repository.TOTPSecrets              { return pgTOTP{p} }
func (p *PG) Recovery() repository.RecoveryCodes        { return pgRecovery{p} }
func (p *PG) Tokens() repository.APITokens              { return pgTokens{p} }

func (p *PG) exec(ctx context.Context, query string, args ...any) error {
	_, err := p.db.ExecContext(ctx, db.Rebind(query, "postgres"), args...)
	return err
}

func (p *PG) execOne(ctx context.Context, query string, args ...any) error {
	res, err := p.db.ExecContext(ctx, db.Rebind(query, "postgres"), args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	var none int64
	if n == none {
		return repository.ErrNotFound
	}
	return nil
}

func (p *PG) row(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, db.Rebind(query, "postgres"), args...)
}

func (p *PG) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return p.db.QueryContext(ctx, db.Rebind(query, "postgres"), args...)
}

func flag(v bool) int {
	if v {
		return 1
	}
	return 0
}

func isOn(n int64) bool {
	var off int64
	return n != off
}

func nullID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return string(id)
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return domain.UnixMicroUTC(*t)
}

type rowScanner interface{ Scan(dest ...any) error }

type pgUsers struct{ *PG }

func mapConflict(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return repository.ErrConflict
	}
	return err
}

func (s pgUsers) Insert(ctx context.Context, row domain.User) error {
	return mapConflict(s.exec(ctx, `INSERT INTO users (
		id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy),
		row.Name, row.Email, row.Roles, flag(row.Active), int64(row.DefaultHourlyRateCents), nullID(row.ContactID), row.BarNumber, row.BarState))
}

func (s pgUsers) Update(ctx context.Context, row domain.User) error {
	return s.execOne(ctx, `UPDATE users SET updated_at=?, name=?, email=?, roles=?, active=?, default_hourly_rate_cents=?, contact_id=?, bar_number=?, bar_state=? WHERE id=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.Name, row.Email, row.Roles, flag(row.Active), int64(row.DefaultHourlyRateCents), nullID(row.ContactID), row.BarNumber, row.BarState, string(row.ID))
}

func (s pgUsers) Get(ctx context.Context, id domain.ID) (domain.User, error) {
	return scanUser(s.row(ctx, userSelect+` WHERE id=?`, string(id)))
}

func (s pgUsers) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	return scanUser(s.row(ctx, userSelect+` WHERE lower(email)=lower(?)`, email))
}

func (s pgUsers) List(ctx context.Context, _ repository.Filter) (repository.Page[domain.User], error) {
	rows, err := s.query(ctx, userSelect+` ORDER BY created_at, id`)
	if err != nil {
		return repository.Page[domain.User]{}, err
	}
	defer rows.Close()
	var items []domain.User
	for rows.Next() {
		row, err := scanUser(rows)
		if err != nil {
			return repository.Page[domain.User]{}, err
		}
		items = append(items, row)
	}
	return repository.Page[domain.User]{Items: items}, rows.Err()
}

const userSelect = `SELECT id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state FROM users`

func scanUser(sc rowScanner) (domain.User, error) {
	var row domain.User
	var id, createdBy string
	var contact sql.NullString
	var created, updated, active, rate int64
	err := sc.Scan(&id, &created, &updated, &createdBy, &row.Name, &row.Email, &row.Roles, &active, &rate, &contact, &row.BarNumber, &row.BarState)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	row.ID = domain.ID(id)
	row.CreatedBy = domain.ID(createdBy)
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	row.Active = isOn(active)
	row.DefaultHourlyRateCents = domain.Cents(rate)
	if contact.Valid {
		row.ContactID = domain.ID(contact.String)
	}
	return row, nil
}

type pgSessions struct{ *PG }

func (s pgSessions) Insert(ctx context.Context, row domain.Session) error {
	return s.exec(ctx, `INSERT INTO sessions (
		id, created_at, updated_at, created_by, user_id, token_hash, expires_at, idle_expires_at, revoked_at
	) VALUES (?,?,?,?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy),
		string(row.UserID), row.TokenHash, domain.UnixMicroUTC(row.ExpiresAt), domain.UnixMicroUTC(row.IdleExpiresAt), nullTime(row.RevokedAt))
}

func (s pgSessions) Update(ctx context.Context, row domain.Session) error {
	return s.execOne(ctx, `UPDATE sessions SET updated_at=?, token_hash=?, expires_at=?, idle_expires_at=?, revoked_at=? WHERE id=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.TokenHash, domain.UnixMicroUTC(row.ExpiresAt), domain.UnixMicroUTC(row.IdleExpiresAt), nullTime(row.RevokedAt), string(row.ID))
}

func (s pgSessions) Get(ctx context.Context, id domain.ID) (domain.Session, error) {
	return scanSession(s.row(ctx, sessionSelect+` WHERE id=?`, string(id)))
}

func (s pgSessions) GetByHash(ctx context.Context, hash string) (domain.Session, error) {
	return scanSession(s.row(ctx, sessionSelect+` WHERE token_hash=?`, hash))
}

func (s pgSessions) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.Session], error) {
	q := sessionSelect
	var args []any
	if filter.UserID != "" {
		q += ` WHERE user_id=?`
		args = append(args, string(filter.UserID))
	}
	return listSessions(s, ctx, q+` ORDER BY created_at, id`, args...)
}

const sessionSelect = `SELECT id, created_at, updated_at, created_by, user_id, token_hash, expires_at, idle_expires_at, revoked_at FROM sessions`

func listSessions(s pgSessions, ctx context.Context, query string, args ...any) (repository.Page[domain.Session], error) {
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return repository.Page[domain.Session]{}, err
	}
	defer rows.Close()
	var items []domain.Session
	for rows.Next() {
		row, err := scanSession(rows)
		if err != nil {
			return repository.Page[domain.Session]{}, err
		}
		items = append(items, row)
	}
	return repository.Page[domain.Session]{Items: items}, rows.Err()
}

func scanSession(sc rowScanner) (domain.Session, error) {
	var row domain.Session
	var id, createdBy, userID string
	var created, updated, expires, idle int64
	var revoked sql.NullInt64
	err := sc.Scan(&id, &created, &updated, &createdBy, &userID, &row.TokenHash, &expires, &idle, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	row.ID = domain.ID(id)
	row.CreatedBy = domain.ID(createdBy)
	row.UserID = domain.ID(userID)
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	row.ExpiresAt = domain.FromUnixMicro(expires)
	row.IdleExpiresAt = domain.FromUnixMicro(idle)
	if revoked.Valid {
		t := domain.FromUnixMicro(revoked.Int64)
		row.RevokedAt = &t
	}
	return row, nil
}

type pgPasswords struct{ *PG }

func (s pgPasswords) Insert(ctx context.Context, row domain.PasswordCredential) error {
	return s.exec(ctx, `INSERT INTO password_credentials (id, created_at, updated_at, created_by, user_id, argon2_hash) VALUES (?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy), string(row.UserID), row.Argon2Hash)
}

func (s pgPasswords) Update(ctx context.Context, row domain.PasswordCredential) error {
	return s.execOne(ctx, `UPDATE password_credentials SET updated_at=?, argon2_hash=? WHERE user_id=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.Argon2Hash, string(row.UserID))
}

func (s pgPasswords) GetByUser(ctx context.Context, userID domain.ID) (domain.PasswordCredential, error) {
	var row domain.PasswordCredential
	var id, createdBy, owner string
	var created, updated int64
	err := s.row(ctx, `SELECT id, created_at, updated_at, created_by, user_id, argon2_hash FROM password_credentials WHERE user_id=?`, string(userID)).
		Scan(&id, &created, &updated, &createdBy, &owner, &row.Argon2Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PasswordCredential{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.PasswordCredential{}, err
	}
	row.ID = domain.ID(id)
	row.CreatedBy = domain.ID(createdBy)
	row.UserID = domain.ID(owner)
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	return row, nil
}

type pgTOTP struct{ *PG }

func (s pgTOTP) Insert(ctx context.Context, row domain.TOTPSecret) error {
	return s.exec(ctx, `INSERT INTO totp_secrets (id, created_at, updated_at, created_by, user_id, ciphertext, confirmed) VALUES (?,?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy), string(row.UserID), row.Ciphertext, flag(row.Confirmed))
}

func (s pgTOTP) Update(ctx context.Context, row domain.TOTPSecret) error {
	return s.execOne(ctx, `UPDATE totp_secrets SET updated_at=?, ciphertext=?, confirmed=? WHERE user_id=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.Ciphertext, flag(row.Confirmed), string(row.UserID))
}

// CompareAndSwap writes the new secret only while the stored ciphertext is still previous.
func (s pgTOTP) CompareAndSwap(ctx context.Context, row domain.TOTPSecret, previous string) error {
	err := s.execOne(ctx, `UPDATE totp_secrets SET updated_at=?, ciphertext=?, confirmed=? WHERE user_id=? AND ciphertext=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.Ciphertext, flag(row.Confirmed), string(row.UserID), previous)
	if errors.Is(err, repository.ErrNotFound) {
		return repository.ErrConflict
	}
	return err
}

func (s pgTOTP) GetByUser(ctx context.Context, userID domain.ID) (domain.TOTPSecret, error) {
	var row domain.TOTPSecret
	var id, createdBy, owner string
	var created, updated, confirmed int64
	err := s.row(ctx, `SELECT id, created_at, updated_at, created_by, user_id, ciphertext, confirmed FROM totp_secrets WHERE user_id=?`, string(userID)).
		Scan(&id, &created, &updated, &createdBy, &owner, &row.Ciphertext, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TOTPSecret{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.TOTPSecret{}, err
	}
	row.ID = domain.ID(id)
	row.CreatedBy = domain.ID(createdBy)
	row.UserID = domain.ID(owner)
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	row.Confirmed = isOn(confirmed)
	return row, nil
}

type pgRecovery struct{ *PG }

func (s pgRecovery) Insert(ctx context.Context, row domain.RecoveryCode) error {
	return s.exec(ctx, `INSERT INTO recovery_codes (id, created_at, updated_at, created_by, user_id, code_hash, used_at) VALUES (?,?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy), string(row.UserID), row.CodeHash, nullTime(row.UsedAt))
}

func (s pgRecovery) Update(ctx context.Context, row domain.RecoveryCode) error {
	q := `UPDATE recovery_codes SET updated_at=?, code_hash=?, used_at=? WHERE id=?`
	if row.UsedAt != nil {
		q += ` AND used_at IS NULL`
	}
	err := s.execOne(ctx, q, domain.UnixMicroUTC(row.UpdatedAt), row.CodeHash, nullTime(row.UsedAt), string(row.ID))
	if row.UsedAt != nil && errors.Is(err, repository.ErrNotFound) {
		return repository.ErrConflict
	}
	return err
}

func (s pgRecovery) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.RecoveryCode], error) {
	q := `SELECT id, created_at, updated_at, created_by, user_id, code_hash, used_at FROM recovery_codes`
	var args []any
	if filter.UserID != "" {
		q += ` WHERE user_id=?`
		args = append(args, string(filter.UserID))
	}
	rows, err := s.query(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return repository.Page[domain.RecoveryCode]{}, err
	}
	defer rows.Close()
	var items []domain.RecoveryCode
	for rows.Next() {
		var row domain.RecoveryCode
		var id, createdBy, userID string
		var created, updated int64
		var used sql.NullInt64
		if err := rows.Scan(&id, &created, &updated, &createdBy, &userID, &row.CodeHash, &used); err != nil {
			return repository.Page[domain.RecoveryCode]{}, err
		}
		row.ID = domain.ID(id)
		row.CreatedBy = domain.ID(createdBy)
		row.UserID = domain.ID(userID)
		row.CreatedAt = domain.FromUnixMicro(created)
		row.UpdatedAt = domain.FromUnixMicro(updated)
		if used.Valid {
			t := domain.FromUnixMicro(used.Int64)
			row.UsedAt = &t
		}
		items = append(items, row)
	}
	return repository.Page[domain.RecoveryCode]{Items: items}, rows.Err()
}

type pgTokens struct{ *PG }

func (s pgTokens) Insert(ctx context.Context, row domain.APIToken) error {
	return s.exec(ctx, `INSERT INTO api_tokens (id, created_at, updated_at, created_by, user_id, module_id, name, token_hash, scopes, revoked_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy),
		nullID(row.UserID), row.ModuleID, row.Name, row.TokenHash, row.Scopes, nullTime(row.RevokedAt))
}

func (s pgTokens) Update(ctx context.Context, row domain.APIToken) error {
	return s.execOne(ctx, `UPDATE api_tokens SET updated_at=?, name=?, scopes=?, revoked_at=? WHERE id=?`,
		domain.UnixMicroUTC(row.UpdatedAt), row.Name, row.Scopes, nullTime(row.RevokedAt), string(row.ID))
}

func (s pgTokens) Get(ctx context.Context, id domain.ID) (domain.APIToken, error) {
	return scanToken(s.row(ctx, tokenSelect+` WHERE id=?`, string(id)))
}

func (s pgTokens) GetByHash(ctx context.Context, hash string) (domain.APIToken, error) {
	return scanToken(s.row(ctx, tokenSelect+` WHERE token_hash=?`, hash))
}

func (s pgTokens) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.APIToken], error) {
	q := tokenSelect
	var args []any
	if filter.UserID != "" {
		q += ` WHERE user_id=?`
		args = append(args, string(filter.UserID))
	}
	rows, err := s.query(ctx, q+` ORDER BY created_at, id`, args...)
	if err != nil {
		return repository.Page[domain.APIToken]{}, err
	}
	defer rows.Close()
	var items []domain.APIToken
	for rows.Next() {
		row, err := scanToken(rows)
		if err != nil {
			return repository.Page[domain.APIToken]{}, err
		}
		items = append(items, row)
	}
	return repository.Page[domain.APIToken]{Items: items}, rows.Err()
}

const tokenSelect = `SELECT id, created_at, updated_at, created_by, user_id, module_id, name, token_hash, scopes, revoked_at FROM api_tokens`

func scanToken(sc rowScanner) (domain.APIToken, error) {
	var row domain.APIToken
	var id, createdBy string
	var userID sql.NullString
	var created, updated int64
	var revoked sql.NullInt64
	err := sc.Scan(&id, &created, &updated, &createdBy, &userID, &row.ModuleID, &row.Name, &row.TokenHash, &row.Scopes, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.APIToken{}, repository.ErrNotFound
	}
	if err != nil {
		return domain.APIToken{}, err
	}
	row.ID = domain.ID(id)
	row.CreatedBy = domain.ID(createdBy)
	if userID.Valid {
		row.UserID = domain.ID(userID.String)
	}
	row.CreatedAt = domain.FromUnixMicro(created)
	row.UpdatedAt = domain.FromUnixMicro(updated)
	if revoked.Valid {
		t := domain.FromUnixMicro(revoked.Int64)
		row.RevokedAt = &t
	}
	return row, nil
}

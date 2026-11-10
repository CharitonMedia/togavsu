package files

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// store is the files repository over schema core. Queries are portable SQL with ?
// placeholders; every write goes through audit.Conn so it joins the audit transaction.
type store struct {
	pool    *sql.DB
	dialect string
}

func (s *store) q(ctx context.Context) audit.Querier { return audit.Conn(ctx, s.pool) }

func (s *store) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.q(ctx).ExecContext(ctx, db.Rebind(query, s.dialect), args...)
	return err
}

func (s *store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.q(ctx).QueryContext(ctx, db.Rebind(query, s.dialect), args...)
}

func (s *store) row(ctx context.Context, query string, args ...any) *sql.Row {
	return s.q(ctx).QueryRowContext(ctx, db.Rebind(query, s.dialect), args...)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return repository.ErrNotFound
	}
	return err
}

// us stores a time as unix microseconds UTC, the column type of every section 5 table.
func us(t time.Time) int64 { return domain.UnixMicroUTC(t) }

// ts scans a unix-microsecond column into a time.
func ts(t *time.Time) sql.Scanner { return microScanner{t} }

type microScanner struct{ t *time.Time }

func (m microScanner) Scan(v any) error {
	n, ok := v.(int64)
	if !ok {
		return errors.New("files: timestamp column is not an integer")
	}
	*m.t = domain.FromUnixMicro(n)
	return nil
}

func flag(b bool) int {
	var n int
	if b {
		n++
	}
	return n
}

func nullID(id domain.ID) any {
	if id == "" {
		return nil
	}
	return string(id)
}

type nullString struct{ sql.NullString }

func (n nullString) id() domain.ID { return domain.ID(n.String) }

// fileRow is the files row with the columns migration 600 adds.
type fileRow struct {
	domain.File
	FinancialClass string
	PurgedAt       int64
	PurgedBy       domain.ID
}

const fileCols = `id, created_at, updated_at, created_by, name, extension, matter_id, contact_id, folder, category,
status, origin, attorney_only, shared_with_client, source, arrived_name, locked_fields, current_version_id,
trashed_at, trashed_by, financial_class, purged_at, purged_by`

func scanFile(sc interface{ Scan(...any) error }) (fileRow, error) {
	var f fileRow
	var matter, contact, category, trashedBy, purgedBy nullString
	var ao, shared int
	err := sc.Scan(&f.ID, ts(&f.CreatedAt), ts(&f.UpdatedAt), &f.CreatedBy, &f.Name, &f.Extension, &matter, &contact,
		&f.Folder, &category, &f.Status, &f.Origin, &ao, &shared, &f.Source, &f.ArrivedName, &f.LockedFields,
		&f.CurrentVersionID, &f.TrashedAt, &trashedBy, &f.FinancialClass, &f.PurgedAt, &purgedBy)
	if err != nil {
		return fileRow{}, notFound(err)
	}
	f.MatterID, f.ContactID, f.Category = matter.id(), contact.id(), category.String
	f.TrashedBy, f.PurgedBy = trashedBy.id(), purgedBy.id()
	f.AttorneyOnly, f.SharedWithClient = ao != int(zero), shared != int(zero)
	return f, nil
}

func (s *store) insertFile(ctx context.Context, f fileRow) error {
	return s.exec(ctx, `INSERT INTO files (`+fileCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, us(f.CreatedAt), us(f.UpdatedAt), f.CreatedBy, f.Name, f.Extension, nullID(f.MatterID), nullID(f.ContactID),
		f.Folder, nullCategory(f.Category), f.Status, f.Origin, flag(f.AttorneyOnly), flag(f.SharedWithClient), f.Source,
		f.ArrivedName, f.LockedFields, f.CurrentVersionID, f.TrashedAt, nullID(f.TrashedBy), f.FinancialClass,
		f.PurgedAt, nullID(f.PurgedBy))
}

func nullCategory(c string) any {
	if c == "" {
		return nil
	}
	return c
}

// updateFile writes the mutable record fields. Versions are separate rows.
func (s *store) updateFile(ctx context.Context, f fileRow) error {
	return s.exec(ctx, `UPDATE files SET updated_at = ?, name = ?, extension = ?, matter_id = ?, contact_id = ?, folder = ?,
category = ?, status = ?, attorney_only = ?, shared_with_client = ?, locked_fields = ?, current_version_id = ?,
trashed_at = ?, trashed_by = ?, financial_class = ?, purged_at = ?, purged_by = ? WHERE id = ?`,
		us(f.UpdatedAt), f.Name, f.Extension, nullID(f.MatterID), nullID(f.ContactID), f.Folder, nullCategory(f.Category),
		f.Status, flag(f.AttorneyOnly), flag(f.SharedWithClient), f.LockedFields, f.CurrentVersionID, f.TrashedAt,
		nullID(f.TrashedBy), f.FinancialClass, f.PurgedAt, nullID(f.PurgedBy), f.ID)
}

func (s *store) getFile(ctx context.Context, id domain.ID) (fileRow, error) {
	return scanFile(s.row(ctx, `SELECT `+fileCols+` FROM files WHERE id = ?`, id))
}

// lockFile reads a file FOR UPDATE inside a transaction, so two saves to one file
// number their versions one after the other.
func (s *store) lockFile(ctx context.Context, id domain.ID) (fileRow, error) {
	query := `SELECT ` + fileCols + ` FROM files WHERE id = ?`
	if s.dialect == "postgres" {
		query += " FOR UPDATE"
	}
	return scanFile(s.row(ctx, query, id))
}

// fileQuery narrows a list in SQL. Access is applied to every row afterwards.
type fileQuery struct {
	MatterID  domain.ID
	ContactID domain.ID
	Folder    *string
	Trashed   bool
	Text      string
	IDs       []domain.ID
	After     domain.ID
	Limit     int
}

func likePattern(text string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(text)) + "%"
}

func (s *store) listFiles(ctx context.Context, fq fileQuery) ([]fileRow, error) {
	where := []string{"purged_at = 0"}
	var args []any
	if fq.Trashed {
		where = append(where, "trashed_at <> 0")
	} else {
		where = append(where, "trashed_at = 0")
	}
	if fq.MatterID != "" {
		where, args = append(where, "matter_id = ?"), append(args, fq.MatterID)
	}
	if fq.ContactID != "" {
		where, args = append(where, "contact_id = ?"), append(args, fq.ContactID)
	}
	if fq.Folder != nil {
		where, args = append(where, "folder = ?"), append(args, *fq.Folder)
	}
	if fq.After != "" {
		where, args = append(where, "id > ?"), append(args, fq.After)
	}
	if len(fq.IDs) > 0 {
		marks := make([]string, len(fq.IDs))
		for i, id := range fq.IDs {
			marks[i] = "?"
			args = append(args, id)
		}
		where = append(where, "id IN ("+strings.Join(marks, ",")+")")
	}
	if fq.Text != "" {
		p := likePattern(fq.Text)
		where = append(where, `(LOWER(name) LIKE ? ESCAPE '\' OR id IN (SELECT file_id FROM file_search_text WHERE body LIKE ? ESCAPE '\'))`)
		args = append(args, p, p)
	}
	query := `SELECT ` + fileCols + ` FROM files WHERE ` + strings.Join(where, " AND ") + ` ORDER BY id`
	if fq.Limit > int(zero) {
		query += " LIMIT ?"
		args = append(args, fq.Limit)
	}
	rows, err := s.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileRow
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// sameSHA lists live files in a matter whose current version has sha.
func (s *store) sameSHA(ctx context.Context, matter domain.ID, sha string) ([]domain.ID, error) {
	rows, err := s.query(ctx, `SELECT f.id FROM files f JOIN file_versions v ON v.id = f.current_version_id
WHERE f.matter_id = ? AND v.sha256 = ? AND f.purged_at = 0 ORDER BY f.id`, matter, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ID
	for rows.Next() {
		var id domain.ID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *store) namesInMatter(ctx context.Context, matter domain.ID) (map[string]bool, error) {
	rows, err := s.query(ctx, `SELECT LOWER(name) FROM files WHERE matter_id = ? AND purged_at = 0`, matter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

const versionCols = `id, created_at, updated_at, created_by, file_id, number, storage_backend_id, storage_key, size_bytes,
sha256, media_type, saved_by, saved_at, how, note, quarantined`

func scanVersion(sc interface{ Scan(...any) error }) (domain.FileVersion, error) {
	var v domain.FileVersion
	var q int
	err := sc.Scan(&v.ID, ts(&v.CreatedAt), ts(&v.UpdatedAt), &v.CreatedBy, &v.FileID, &v.Number, &v.StorageBackendID,
		&v.StorageKey, &v.SizeBytes, &v.SHA256, &v.MediaType, &v.SavedBy, &v.SavedAt, &v.How, &v.Note, &q)
	v.Quarantined = q != int(zero)
	return v, notFound(err)
}

// appendVersion is the only write to file_versions. There is no update or delete.
func (s *store) appendVersion(ctx context.Context, v domain.FileVersion) error {
	return s.exec(ctx, `INSERT INTO file_versions (`+versionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, us(v.CreatedAt), us(v.UpdatedAt), v.CreatedBy, v.FileID, v.Number, v.StorageBackendID, v.StorageKey, v.SizeBytes,
		v.SHA256, v.MediaType, v.SavedBy, v.SavedAt, v.How, v.Note, flag(v.Quarantined))
}

func (s *store) getVersion(ctx context.Context, id domain.ID) (domain.FileVersion, error) {
	return scanVersion(s.row(ctx, `SELECT `+versionCols+` FROM file_versions WHERE id = ?`, id))
}

func (s *store) listVersions(ctx context.Context, file domain.ID) ([]domain.FileVersion, error) {
	rows, err := s.query(ctx, `SELECT `+versionCols+` FROM file_versions WHERE file_id = ? ORDER BY number`, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FileVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// allVersions lists every version of every live file, optionally in one matter.
func (s *store) allVersions(ctx context.Context, matter domain.ID) ([]domain.FileVersion, error) {
	query := `SELECT ` + prefixed("v.", versionCols) + ` FROM file_versions v JOIN files f ON f.id = v.file_id WHERE f.purged_at = 0`
	var args []any
	if matter != "" {
		query += " AND f.matter_id = ?"
		args = append(args, matter)
	}
	rows, err := s.query(ctx, query+" ORDER BY v.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FileVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = p + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// quarantined reports the newest scanner mark, or the version row's own flag.
func (s *store) quarantined(ctx context.Context, v domain.FileVersion) (bool, error) {
	var q int
	err := s.row(ctx, `SELECT quarantined FROM file_quarantine_marks WHERE file_version_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, v.ID).Scan(&q)
	if errors.Is(err, sql.ErrNoRows) {
		return v.Quarantined, nil
	}
	return q != int(zero), err
}

func (s *store) appendQuarantine(ctx context.Context, m domain.Meta, version domain.ID, on bool, source string) error {
	return s.exec(ctx, `INSERT INTO file_quarantine_marks (id, created_at, updated_at, created_by, file_version_id, quarantined, source) VALUES (?,?,?,?,?,?,?)`,
		m.ID, us(m.CreatedAt), us(m.UpdatedAt), m.CreatedBy, version, flag(on), source)
}

const renditionCols = `id, created_at, updated_at, created_by, file_version_id, kind, number, storage_backend_id, storage_key,
size_bytes, sha256, made_by, made_at`

func scanRendition(sc interface{ Scan(...any) error }) (domain.FileRendition, error) {
	var r domain.FileRendition
	err := sc.Scan(&r.ID, ts(&r.CreatedAt), ts(&r.UpdatedAt), &r.CreatedBy, &r.FileVersionID, &r.Kind, &r.Number,
		&r.StorageBackendID, &r.StorageKey, &r.SizeBytes, &r.SHA256, &r.MadeBy, &r.MadeAt)
	return r, notFound(err)
}

// appendRendition is the only write to file_renditions.
func (s *store) appendRendition(ctx context.Context, r domain.FileRendition) error {
	return s.exec(ctx, `INSERT INTO file_renditions (`+renditionCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, us(r.CreatedAt), us(r.UpdatedAt), r.CreatedBy, r.FileVersionID, r.Kind, r.Number, r.StorageBackendID, r.StorageKey,
		r.SizeBytes, r.SHA256, r.MadeBy, r.MadeAt)
}

func (s *store) getRendition(ctx context.Context, id domain.ID) (domain.FileRendition, error) {
	return scanRendition(s.row(ctx, `SELECT `+renditionCols+` FROM file_renditions WHERE id = ?`, id))
}

// renditionsOf lists a file's renditions across its versions, oldest first.
func (s *store) renditionsOf(ctx context.Context, file domain.ID, matter domain.ID) ([]domain.FileRendition, error) {
	query := `SELECT ` + prefixed("r.", renditionCols) + ` FROM file_renditions r JOIN file_versions v ON v.id = r.file_version_id
JOIN files f ON f.id = v.file_id WHERE f.purged_at = 0`
	var args []any
	if file != "" {
		query += " AND v.file_id = ?"
		args = append(args, file)
	}
	if matter != "" {
		query += " AND f.matter_id = ?"
		args = append(args, matter)
	}
	rows, err := s.query(ctx, query+" ORDER BY r.made_at, r.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FileRendition
	for rows.Next() {
		r, err := scanRendition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *store) nextRenditionNumber(ctx context.Context, file domain.ID, kind string) (int, error) {
	var n sql.NullInt64
	err := s.row(ctx, `SELECT MAX(r.number) FROM file_renditions r JOIN file_versions v ON v.id = r.file_version_id WHERE v.file_id = ? AND r.kind = ?`, file, kind).Scan(&n)
	return int(n.Int64) + 1, err
}

func (s *store) setSearchText(ctx context.Context, file, rendition domain.ID, body string) error {
	if err := s.exec(ctx, `DELETE FROM file_search_text WHERE file_id = ?`, file); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO file_search_text (file_id, rendition_id, body) VALUES (?,?,?)`, file, rendition, strings.ToLower(body))
}

func (s *store) insertLink(ctx context.Context, l domain.FileLink) error {
	return s.exec(ctx, `INSERT INTO file_links (id, created_at, updated_at, created_by, from_file_id, to_file_id, kind) VALUES (?,?,?,?,?,?,?)`,
		l.ID, us(l.CreatedAt), us(l.UpdatedAt), l.CreatedBy, l.FromFileID, l.ToFileID, l.Kind)
}

func (s *store) links(ctx context.Context, file domain.ID) ([]domain.FileLink, error) {
	rows, err := s.query(ctx, `SELECT id, created_at, updated_at, created_by, from_file_id, to_file_id, kind FROM file_links
WHERE from_file_id = ? OR to_file_id = ? ORDER BY id`, file, file)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FileLink
	for rows.Next() {
		var l domain.FileLink
		if err := rows.Scan(&l.ID, ts(&l.CreatedAt), ts(&l.UpdatedAt), &l.CreatedBy, &l.FromFileID, &l.ToFileID, &l.Kind); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Folder is one matter folder record.
type Folder struct {
	domain.Meta
	MatterID  domain.ID
	Name      string
	SortOrder int
}

func (s *store) insertFolder(ctx context.Context, f Folder) error {
	return s.exec(ctx, `INSERT INTO folders (id, created_at, updated_at, created_by, matter_id, name, sort_order) VALUES (?,?,?,?,?,?,?)`,
		f.ID, us(f.CreatedAt), us(f.UpdatedAt), f.CreatedBy, f.MatterID, f.Name, f.SortOrder)
}

func (s *store) updateFolder(ctx context.Context, f Folder) error {
	return s.exec(ctx, `UPDATE folders SET updated_at = ?, name = ?, sort_order = ? WHERE id = ?`, us(f.UpdatedAt), f.Name, f.SortOrder, f.ID)
}

func (s *store) deleteFolder(ctx context.Context, id domain.ID) error {
	return s.exec(ctx, `DELETE FROM folders WHERE id = ?`, id)
}

func scanFolder(sc interface{ Scan(...any) error }) (Folder, error) {
	var f Folder
	err := sc.Scan(&f.ID, ts(&f.CreatedAt), ts(&f.UpdatedAt), &f.CreatedBy, &f.MatterID, &f.Name, &f.SortOrder)
	return f, notFound(err)
}

func (s *store) getFolder(ctx context.Context, id domain.ID) (Folder, error) {
	return scanFolder(s.row(ctx, `SELECT id, created_at, updated_at, created_by, matter_id, name, sort_order FROM folders WHERE id = ?`, id))
}

func (s *store) folders(ctx context.Context, matter domain.ID) ([]Folder, error) {
	rows, err := s.query(ctx, `SELECT id, created_at, updated_at, created_by, matter_id, name, sort_order FROM folders WHERE matter_id = ? ORDER BY sort_order, name`, matter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *store) folderInUse(ctx context.Context, id domain.ID) (bool, error) {
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM files WHERE folder = ? AND purged_at = 0`, id).Scan(&n)
	return n != int(zero), err
}

const backendCols = `id, created_at, updated_at, created_by, kind, name, settings_ciphertext, encryption, is_default, health, backed_up, drop_folder`

func scanBackend(sc interface{ Scan(...any) error }) (domain.StorageBackend, error) {
	var b domain.StorageBackend
	var enc, def, bu, drop int
	err := sc.Scan(&b.ID, ts(&b.CreatedAt), ts(&b.UpdatedAt), &b.CreatedBy, &b.Kind, &b.Name, &b.SettingsCiphertext, &enc, &def, &b.Health, &bu, &drop)
	b.Encryption, b.IsDefault, b.BackedUp, b.DropFolder = enc != int(zero), def != int(zero), bu != int(zero), drop != int(zero)
	return b, notFound(err)
}

func (s *store) insertBackend(ctx context.Context, b domain.StorageBackend) error {
	return s.exec(ctx, `INSERT INTO storage_backends (`+backendCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, us(b.CreatedAt), us(b.UpdatedAt), b.CreatedBy, b.Kind, b.Name, b.SettingsCiphertext, flag(b.Encryption),
		flag(b.IsDefault), b.Health, flag(b.BackedUp), flag(b.DropFolder))
}

func (s *store) updateBackend(ctx context.Context, b domain.StorageBackend) error {
	return s.exec(ctx, `UPDATE storage_backends SET updated_at = ?, name = ?, settings_ciphertext = ?, encryption = ?, is_default = ?,
health = ?, backed_up = ?, drop_folder = ? WHERE id = ?`, us(b.UpdatedAt), b.Name, b.SettingsCiphertext, flag(b.Encryption),
		flag(b.IsDefault), b.Health, flag(b.BackedUp), flag(b.DropFolder), b.ID)
}

func (s *store) clearDefault(ctx context.Context, keep domain.ID, at int64) error {
	return s.exec(ctx, `UPDATE storage_backends SET is_default = 0, updated_at = ? WHERE id <> ? AND is_default = 1`, at, keep)
}

func (s *store) getBackend(ctx context.Context, id domain.ID) (domain.StorageBackend, error) {
	return scanBackend(s.row(ctx, `SELECT `+backendCols+` FROM storage_backends WHERE id = ?`, id))
}

func (s *store) backends(ctx context.Context) ([]domain.StorageBackend, error) {
	rows, err := s.query(ctx, `SELECT `+backendCols+` FROM storage_backends ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StorageBackend
	for rows.Next() {
		b, err := scanBackend(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// location is where an object's bytes are now.
type location struct {
	Backend domain.ID
	Key     string
	Sealed  bool
	// known is false for a fallback that came from the version row alone.
	known bool
}

func (s *store) location(ctx context.Context, object domain.ID, fallback location) (location, error) {
	var l location
	var sealed int
	err := s.row(ctx, `SELECT storage_backend_id, storage_key, sealed FROM file_object_locations WHERE object_id = ?`, object).Scan(&l.Backend, &l.Key, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	l.Sealed, l.known = sealed != int(zero), true
	return l, err
}

func (s *store) setLocation(ctx context.Context, object domain.ID, kind string, l location, at int64) error {
	if err := s.exec(ctx, `DELETE FROM file_object_locations WHERE object_id = ?`, object); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO file_object_locations (object_id, object_kind, storage_backend_id, storage_key, sealed, updated_at) VALUES (?,?,?,?,?,?)`,
		object, kind, l.Backend, l.Key, flag(l.Sealed), at)
}

// uploadRow is one resumable upload.
type uploadRow struct {
	domain.Meta
	OwnerUserID    domain.ID
	OwnerModuleID  string
	FileName       string
	MatterID       domain.ID
	ContactID      domain.ID
	MediaType      string
	SizeBytes      int64
	DeclaredSHA256 string
	ChunkBytes     int64
	ReceivedBytes  int64
	SHA256         string
	Status         string
	ExpiresAt      int64
	FileID         domain.ID
	VersionID      domain.ID
}

const uploadCols = `id, created_at, updated_at, created_by, owner_user_id, owner_module_id, file_name, matter_id, contact_id,
media_type, size_bytes, declared_sha256, chunk_bytes, received_bytes, sha256, status, expires_at, file_id, version_id`

func (s *store) insertUpload(ctx context.Context, u uploadRow) error {
	return s.exec(ctx, `INSERT INTO file_uploads (`+uploadCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, us(u.CreatedAt), us(u.UpdatedAt), u.CreatedBy, u.OwnerUserID, u.OwnerModuleID, u.FileName, nullID(u.MatterID),
		nullID(u.ContactID), u.MediaType, u.SizeBytes, u.DeclaredSHA256, u.ChunkBytes, u.ReceivedBytes, u.SHA256,
		u.Status, u.ExpiresAt, nullID(u.FileID), nullID(u.VersionID))
}

func (s *store) updateUpload(ctx context.Context, u uploadRow) error {
	return s.exec(ctx, `UPDATE file_uploads SET updated_at = ?, received_bytes = ?, sha256 = ?, status = ?, file_id = ?, version_id = ? WHERE id = ?`,
		us(u.UpdatedAt), u.ReceivedBytes, u.SHA256, u.Status, nullID(u.FileID), nullID(u.VersionID), u.ID)
}

// publishUpload marks a staged upload published in the caller's transaction. It
// reports false when another publish already took it: the conditional UPDATE waits on
// the row lock and then sees the committed status, so only one publish wins.
func (s *store) publishUpload(ctx context.Context, u uploadRow) (bool, error) {
	res, err := s.q(ctx).ExecContext(ctx, db.Rebind(`UPDATE file_uploads SET updated_at = ?, status = 'published', file_id = ?, version_id = ?
WHERE id = ? AND status = 'staged'`, s.dialect), us(u.UpdatedAt), nullID(u.FileID), nullID(u.VersionID), u.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > int64(zero), err
}

// lockUpload reads an upload FOR UPDATE inside a transaction, so two chunk writers
// for the same upload run one after the other.
func (s *store) getUpload(ctx context.Context, id domain.ID, lock bool) (uploadRow, error) {
	query := `SELECT ` + uploadCols + ` FROM file_uploads WHERE id = ?`
	if lock && s.dialect == "postgres" {
		query += " FOR UPDATE"
	}
	var u uploadRow
	var matter, contact, file, version nullString
	err := s.row(ctx, query, id).Scan(&u.ID, ts(&u.CreatedAt), ts(&u.UpdatedAt), &u.CreatedBy, &u.OwnerUserID, &u.OwnerModuleID,
		&u.FileName, &matter, &contact, &u.MediaType, &u.SizeBytes, &u.DeclaredSHA256, &u.ChunkBytes, &u.ReceivedBytes,
		&u.SHA256, &u.Status, &u.ExpiresAt, &file, &version)
	u.MatterID, u.ContactID, u.FileID, u.VersionID = matter.id(), contact.id(), file.id(), version.id()
	return u, notFound(err)
}

// moveRow is a storage_moves row with the total migration 600 adds.
type moveRow struct {
	domain.StorageMove
	Total int
}

const moveCols = `id, created_at, updated_at, created_by, from_backend_id, to_backend_id, scope, matter_id, status, progress, confirmed_by, confirmed_at, progress_total`

func scanMove(sc interface{ Scan(...any) error }) (moveRow, error) {
	var m moveRow
	var matter, by nullString
	err := sc.Scan(&m.ID, ts(&m.CreatedAt), ts(&m.UpdatedAt), &m.CreatedBy, &m.FromBackendID, &m.ToBackendID, &m.Scope, &matter,
		&m.Status, &m.Progress, &by, &m.ConfirmedAt, &m.Total)
	m.MatterID, m.ConfirmedBy = matter.id(), by.id()
	return m, notFound(err)
}

func (s *store) insertMove(ctx context.Context, m moveRow) error {
	return s.exec(ctx, `INSERT INTO storage_moves (`+moveCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, us(m.CreatedAt), us(m.UpdatedAt), m.CreatedBy, m.FromBackendID, m.ToBackendID, m.Scope, nullID(m.MatterID),
		m.Status, m.Progress, nullID(m.ConfirmedBy), m.ConfirmedAt, m.Total)
}

func (s *store) updateMove(ctx context.Context, m moveRow) error {
	return s.exec(ctx, `UPDATE storage_moves SET updated_at = ?, status = ?, progress = ?, confirmed_by = ?, confirmed_at = ?, progress_total = ? WHERE id = ?`,
		us(m.UpdatedAt), m.Status, m.Progress, nullID(m.ConfirmedBy), m.ConfirmedAt, m.Total, m.ID)
}

func (s *store) getMove(ctx context.Context, id domain.ID) (moveRow, error) {
	return scanMove(s.row(ctx, `SELECT `+moveCols+` FROM storage_moves WHERE id = ?`, id))
}

func (s *store) moves(ctx context.Context) ([]moveRow, error) {
	rows, err := s.query(ctx, `SELECT `+moveCols+` FROM storage_moves ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []moveRow
	for rows.Next() {
		m, err := scanMove(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type moveItem struct {
	MoveID        domain.ID
	ObjectID      domain.ID
	ObjectKind    string
	FromBackendID domain.ID
	FromKey       string
	SizeBytes     int64
	SHA256        string
	Status        string
}

func (s *store) insertMoveItem(ctx context.Context, it moveItem) error {
	return s.exec(ctx, `INSERT INTO storage_move_items (move_id, object_id, object_kind, from_backend_id, from_key, size_bytes, sha256, status) VALUES (?,?,?,?,?,?,?,?)`,
		it.MoveID, it.ObjectID, it.ObjectKind, it.FromBackendID, it.FromKey, it.SizeBytes, it.SHA256, it.Status)
}

func (s *store) setMoveItem(ctx context.Context, move, object domain.ID, status string) error {
	return s.exec(ctx, `UPDATE storage_move_items SET status = ? WHERE move_id = ? AND object_id = ?`, status, move, object)
}

func (s *store) moveItems(ctx context.Context, move domain.ID) ([]moveItem, error) {
	rows, err := s.query(ctx, `SELECT move_id, object_id, object_kind, from_backend_id, from_key, size_bytes, sha256, status FROM storage_move_items WHERE move_id = ? ORDER BY object_id`, move)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []moveItem
	for rows.Next() {
		var it moveItem
		if err := rows.Scan(&it.MoveID, &it.ObjectID, &it.ObjectKind, &it.FromBackendID, &it.FromKey, &it.SizeBytes, &it.SHA256, &it.Status); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *store) insertAlert(ctx context.Context, m domain.Meta, backend, object, file domain.ID, detail string, emailed bool) error {
	return s.exec(ctx, `INSERT INTO storage_alerts (id, created_at, updated_at, created_by, storage_backend_id, object_id, file_id, detail, emailed) VALUES (?,?,?,?,?,?,?,?,?)`,
		m.ID, us(m.CreatedAt), us(m.UpdatedAt), m.CreatedBy, backend, object, file, detail, flag(emailed))
}

// Alert is one stored integrity alert.
type Alert struct {
	ID        domain.ID `json:"id"`
	BackendID domain.ID `json:"storage_backend_id"`
	ObjectID  domain.ID `json:"object_id"`
	FileID    domain.ID `json:"file_id"`
	Detail    string    `json:"detail"`
	Emailed   bool      `json:"emailed"`
	At        int64     `json:"-"`
}

func (s *store) alerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.query(ctx, `SELECT id, storage_backend_id, object_id, file_id, detail, emailed, created_at FROM storage_alerts ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		var e int
		if err := rows.Scan(&a.ID, &a.BackendID, &a.ObjectID, &a.FileID, &a.Detail, &e, &a.At); err != nil {
			return nil, err
		}
		a.Emailed = e != int(zero)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *store) appendEvent(ctx context.Context, e domain.Event) error {
	return s.exec(ctx, `INSERT INTO events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, us(e.CreatedAt), us(e.UpdatedAt), e.CreatedBy, e.ActorKind, e.ActorID, e.Action, e.RecordType, e.RecordID,
		nullID(e.MatterID), e.Summary, flag(e.ClientVisible))
}

func (s *store) categoryExists(ctx context.Context, name string) (bool, error) {
	var n int
	err := s.row(ctx, `SELECT COUNT(*) FROM doc_categories WHERE name = ?`, name).Scan(&n)
	return n != int(zero), err
}

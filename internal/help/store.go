package help

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the help migrations (ui range, 950-999).
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// Memory is the in-process Store for unit tests.
type Memory struct {
	mu       sync.Mutex
	sections map[domain.ID]Section
	items    map[domain.ID]Item
	pages    map[string]map[string]Page
	modules  map[string]bool
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{sections: map[domain.ID]Section{}, items: map[domain.ID]Item{}, pages: map[string]map[string]Page{}, modules: map[string]bool{}}
}

func (m *Memory) Sections(context.Context) ([]Section, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Section
	for _, s := range m.sections {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) PutSection(_ context.Context, s Section) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sections[s.ID] = s
	return nil
}

func (m *Memory) DeleteSection(_ context.Context, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sections, id)
	for k, it := range m.items {
		if it.SectionID == id {
			delete(m.items, k)
		}
	}
	return nil
}

func (m *Memory) Items(_ context.Context, section domain.ID) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, it := range m.items {
		if section.IsZero() || it.SectionID == section {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *Memory) PutItem(_ context.Context, it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[it.ID] = it
	return nil
}

func (m *Memory) DeleteItem(_ context.Context, id domain.ID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, id)
	return nil
}

func (m *Memory) Pages(_ context.Context, module string) ([]Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Page
	for _, p := range m.pages[module] {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}

func (m *Memory) ReplacePages(_ context.Context, module string, pages []Page, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pages == nil {
		delete(m.pages, module)
		delete(m.modules, module)
		return nil
	}
	set := map[string]Page{}
	for _, p := range pages {
		set[p.ID] = p
	}
	m.pages[module] = set
	return nil
}

func (m *Memory) PutPage(_ context.Context, p Page) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pages[p.ModuleID] == nil {
		m.pages[p.ModuleID] = map[string]Page{}
	}
	m.pages[p.ModuleID][p.ID] = p
	return nil
}

func (m *Memory) DeletePage(_ context.Context, module, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pages[module][id]; !ok {
		return ErrNotFound
	}
	delete(m.pages[module], id)
	return nil
}

func (m *Memory) SetModuleEnabled(_ context.Context, module string, enabled bool, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modules[module] = enabled
	return nil
}

func (m *Memory) Modules(context.Context) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for k, v := range m.modules {
		out[k] = v
	}
	return out, nil
}

// PG is the Postgres Store on the core pool.
type PG struct{ DB *sql.DB }

func micro(t time.Time) int64 { return t.UTC().UnixMicro() }

func fromMicro(v int64) time.Time { return time.UnixMicro(v).UTC() }

func (s PG) Sections(ctx context.Context) ([]Section, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, title, sort_order FROM help_sections ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		var sec Section
		var id, by string
		var c, u int64
		if err := rows.Scan(&id, &c, &u, &by, &sec.Title, &sec.SortOrder); err != nil {
			return nil, err
		}
		sec.ID, sec.CreatedAt, sec.UpdatedAt, sec.CreatedBy = domain.ID(id), fromMicro(c), fromMicro(u), domain.ID(by)
		out = append(out, sec)
	}
	return out, rows.Err()
}

func (s PG) PutSection(ctx context.Context, sec Section) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO help_sections (id, created_at, updated_at, created_by, title, sort_order) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET updated_at = excluded.updated_at, title = excluded.title, sort_order = excluded.sort_order`,
		string(sec.ID), micro(sec.CreatedAt), micro(sec.UpdatedAt), string(sec.CreatedBy), sec.Title, sec.SortOrder)
	return err
}

func (s PG) DeleteSection(ctx context.Context, id domain.ID) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM help_sections WHERE id = $1`, string(id))
	return err
}

func (s PG) Items(ctx context.Context, section domain.ID) ([]Item, error) {
	q := `SELECT id, created_at, updated_at, created_by, section_id, title, kind, body, file_id, sort_order FROM help_items`
	var args []any
	if !section.IsZero() {
		q += ` WHERE section_id = $1`
		args = append(args, string(section))
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY section_id, sort_order, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var id, by, sec string
		var file sql.NullString
		var c, u int64
		if err := rows.Scan(&id, &c, &u, &by, &sec, &it.Title, &it.Kind, &it.Body, &file, &it.SortOrder); err != nil {
			return nil, err
		}
		it.ID, it.CreatedAt, it.UpdatedAt, it.CreatedBy, it.SectionID = domain.ID(id), fromMicro(c), fromMicro(u), domain.ID(by), domain.ID(sec)
		if file.Valid && file.String != "" {
			f := domain.ID(file.String)
			it.FileID = &f
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s PG) PutItem(ctx context.Context, it Item) error {
	var file any
	if it.FileID != nil && !it.FileID.IsZero() {
		file = string(*it.FileID)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO help_items (id, created_at, updated_at, created_by, section_id, title, kind, body, file_id, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET updated_at = excluded.updated_at, section_id = excluded.section_id, title = excluded.title,
		kind = excluded.kind, body = excluded.body, file_id = excluded.file_id, sort_order = excluded.sort_order`,
		string(it.ID), micro(it.CreatedAt), micro(it.UpdatedAt), string(it.CreatedBy), string(it.SectionID), it.Title, it.Kind, it.Body, file, it.SortOrder)
	return err
}

func (s PG) DeleteItem(ctx context.Context, id domain.ID) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM help_items WHERE id = $1`, string(id))
	return err
}

func (s PG) Pages(ctx context.Context, module string) ([]Page, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT page_id, updated_at, title, markdown, keywords, covers, sort_order FROM help_module_pages WHERE module_id = $1 ORDER BY sort_order, page_id`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		p := Page{ModuleID: module}
		var kw, cov string
		var at int64
		if err := rows.Scan(&p.ID, &at, &p.Title, &p.Markdown, &kw, &cov, &p.SortOrder); err != nil {
			return nil, err
		}
		p.UpdatedAt = fromMicro(at)
		if err := json.Unmarshal([]byte(kw), &p.Keywords); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cov), &p.Covers); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func putPage(ctx context.Context, x execer, p Page) error {
	kw, _ := json.Marshal(p.Keywords)
	cov, _ := json.Marshal(p.Covers)
	_, err := x.ExecContext(ctx, `INSERT INTO help_module_pages (module_id, page_id, updated_at, title, markdown, keywords, covers, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (module_id, page_id) DO UPDATE SET updated_at = excluded.updated_at, title = excluded.title, markdown = excluded.markdown,
		keywords = excluded.keywords, covers = excluded.covers, sort_order = excluded.sort_order`,
		p.ModuleID, p.ID, micro(p.UpdatedAt), p.Title, p.Markdown, string(kw), string(cov), p.SortOrder)
	return err
}

func (s PG) PutPage(ctx context.Context, p Page) error { return putPage(ctx, s.DB, p) }

func (s PG) ReplacePages(ctx context.Context, module string, pages []Page, at time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM help_module_pages WHERE module_id = $1`, module); err != nil {
		return err
	}
	if pages == nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM help_modules WHERE module_id = $1`, module); err != nil {
			return err
		}
	}
	for _, p := range pages {
		if err := putPage(ctx, tx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s PG) DeletePage(ctx context.Context, module, id string) error {
	var gone string
	err := s.DB.QueryRowContext(ctx, `DELETE FROM help_module_pages WHERE module_id = $1 AND page_id = $2 RETURNING page_id`, module, id).Scan(&gone)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (s PG) SetModuleEnabled(ctx context.Context, module string, enabled bool, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO help_modules (module_id, updated_at, enabled) VALUES ($1, $2, CASE WHEN $3 THEN 1 ELSE 0 END)
		ON CONFLICT (module_id) DO UPDATE SET updated_at = excluded.updated_at, enabled = excluded.enabled`, module, micro(at), enabled)
	return err
}

func (s PG) Modules(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT module_id, (enabled = 1) FROM help_modules`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var m string
		var on bool
		if err := rows.Scan(&m, &on); err != nil {
			return nil, err
		}
		out[m] = on
	}
	return out, rows.Err()
}

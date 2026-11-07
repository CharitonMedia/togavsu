package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

const testAdminDSN = "postgres://togavsu:togavsu@127.0.0.1:5432/togavsu?sslmode=disable"

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	return openPostgres(t, "")
}

func openCoreDB(t *testing.T) *sql.DB {
	t.Helper()
	plain := openPostgres(t, "")
	applyEmbedded(t, plain)
	_ = plain.Close()
	return openPostgresNamed(t, dbName(t), "core")
}

func openPostgres(t *testing.T, searchPath string) *sql.DB {
	t.Helper()
	admin, err := OpenPostgres(testAdminDSN, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	name := dbName(t)
	if _, err := admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := OpenPostgres(testAdminDSN, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	return openPostgresNamed(t, name, searchPath)
}

func openPostgresNamed(t *testing.T, name, searchPath string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("postgres://togavsu:togavsu@127.0.0.1:5432/%s?sslmode=disable", name)
	db, err := OpenPostgres(dsn, searchPath)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func dbName(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	return "t" + hex.EncodeToString(sum[:8])
}

func applyEmbedded(t *testing.T, db *sql.DB) {
	t.Helper()
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), db, ApplySpec{Owner: CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
}

func actor() domain.Principal {
	return domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
}

func TestSchemaContract(t *testing.T) {
	sqlText, err := SchemaSQL()
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bjsonb\b`),
		regexp.MustCompile(`(?i)\bpg_trgm\b`),
		regexp.MustCompile(`(?i)\bcreate\s+trigger\b`),
		regexp.MustCompile(`(?i)\bcreate\s+function\b`),
		regexp.MustCompile(`(?i)\bcreate\s+procedure\b`),
		regexp.MustCompile(`(?i)\bcreate\s+extension\b`),
		regexp.MustCompile(`(?i)\bcreate\s+type\b`),
		regexp.MustCompile(`(?i)\buuid_generate\b`),
		regexp.MustCompile(`(?i)\bgen_random_uuid\b`),
		regexp.MustCompile(`(?i)\bilike\b`),
		regexp.MustCompile(`(?i)\breturning\b`),
		regexp.MustCompile(`(?i)\bnow\s*\(`),
		regexp.MustCompile(`(?i)\bserial\b`),
		regexp.MustCompile(`(?i)[a-z_]+\s*\[\]`),
	} {
		if re.FindStringIndex(sqlText) != nil {
			t.Fatalf("non-portable SQL %s", re)
		}
	}
	bodies := map[string]string{}
	for _, m := range regexp.MustCompile(`(?is)create table\s+([a-z_]+)\s*\((.*?)\);`).FindAllStringSubmatch(sqlText, -1) {
		bodies[m[1]] = m[2]
	}
	seen := map[string]bool{}
	for _, rec := range domain.RecordTypes() {
		name := rec.TableName()
		if seen[name] {
			t.Fatal("duplicate", name)
		}
		seen[name] = true
		body, ok := bodies[name]
		if !ok {
			t.Fatal("missing table", name)
		}
		for _, col := range columnsOf(t, reflect.TypeOf(rec)) {
			if !regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(col) + `\s`).MatchString(body) {
				t.Errorf("%s missing column %s", name, col)
			}
		}
	}
	for name := range bodies {
		if !seen[name] {
			t.Errorf("unexpected table %s", name)
		}
	}
	if len(seen) != 54 {
		t.Fatal(len(seen))
	}
}

func columnsOf(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			out = append(out, columnsOf(t, f.Type)...)
			continue
		}
		tag := f.Tag.Get("db")
		if tag == "" {
			t.Fatalf("%s.%s missing db tag", typ.Name(), f.Name)
		}
		out = append(out, tag)
	}
	return out
}

func TestMigrationRunner(t *testing.T) {
	ctx := context.Background()
	if Rebind("SELECT ? , ?", "postgres") != "SELECT $1 , $2" {
		t.Fatal(Rebind("SELECT ? , ?", "postgres"))
	}
	if Rebind("SELECT ?", "sqlite") != "SELECT ?" {
		t.Fatal("sqlite rebind")
	}
	db := openDB(t)
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Schema: "core;drop"}); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{
		"0010_later.sql":   {Data: []byte("INSERT INTO earlier (id) VALUES ('x');\n")},
		"0002_earlier.sql": {Data: []byte("CREATE TABLE earlier (id TEXT);\n")},
	}
	ms, err := LoadFS(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal("rerun", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE owner = $1`, CoreOwner).Scan(&n); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	ms[0].SQL += "\n-- edited\n"
	ms[0].Checksum = ""
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "postgres", Migrations: ms}); !errors.Is(err, ErrChecksum) {
		t.Fatal(err)
	}
	other := []Migration{{Version: 2, Name: "0002_earlier", SQL: "CREATE TABLE IF NOT EXISTS earlier (id TEXT);\n"}}
	if err := Apply(ctx, db, ApplySpec{Owner: "mod_clock", Dialect: "postgres", Migrations: other}); err != nil {
		t.Fatal(err)
	}
	dup := []Migration{{Version: 4, SQL: "SELECT 1"}, {Version: 4, SQL: "SELECT 1"}}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "postgres", Migrations: dup}); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
	}
}

func TestMigrationRejectsNoTransaction(t *testing.T) {
	forms := []string{
		"-- +goose NO TRANSACTION\n-- +goose Up\nCREATE TABLE kept (id TEXT);\nINSERT INTO missing_table (id) VALUES ('x');\n",
		"-- +goose no transaction\nCREATE TABLE kept (id TEXT);\nINSERT INTO missing_table (id) VALUES ('x');\n",
	}
	for i, sqlText := range forms {
		t.Run(fmt.Sprintf("form%d", i), func(t *testing.T) {
			ctx := context.Background()
			db := openDB(t)
			err := Apply(ctx, db, ApplySpec{
				Owner: CoreOwner, Dialect: "postgres",
				Migrations: []Migration{{Version: 11, SQL: sqlText}},
			})
			if !errors.Is(err, ErrTransaction) || !strings.Contains(err.Error(), "NO TRANSACTION") {
				t.Fatalf("expected the directive to be rejected, got %v", err)
			}
			var n int
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'kept'`).Scan(&n); err != nil || n != 0 {
				t.Fatal("NO TRANSACTION migration left a table", n, err)
			}
			if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'schema_migrations'`).Scan(&n); err != nil || n != 0 {
				t.Fatal("rejected migration created the ledger", n, err)
			}
		})
	}
}

func TestMigrationRollback(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	bad := []Migration{{Version: 11, SQL: "CREATE TABLE kept (id TEXT); INSERT INTO missing_table (id) VALUES ('x');"}}
	err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "postgres", Migrations: bad})
	if err == nil || !strings.Contains(err.Error(), "missing_table") {
		t.Fatalf("expected the missing_table statement to fail, got %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 11`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed migration recorded", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'kept'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed migration left a table", n, err)
	}
}

func TestMigrationSearchPathFailureAndBorrower(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	migrationAfterSearchPath = func(context.Context, *sql.Conn) error {
		return errors.New("prepare failed")
	}
	t.Cleanup(func() { migrationAfterSearchPath = nil })
	err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Schema: "core", Dialect: "postgres", Migrations: []Migration{{Version: 1, SQL: "SELECT 1"}}})
	if err == nil || !strings.Contains(err.Error(), "prepare failed") {
		t.Fatal(err)
	}
	assertDefaultSearchPath(t, db)

	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_lock(424242)`); err != nil {
		t.Fatal(err)
	}
	defer holder.ExecContext(context.Background(), `SELECT pg_advisory_unlock(424242)`)
	started := make(chan struct{})
	migrationAfterSearchPath = func(context.Context, *sql.Conn) error {
		close(started)
		return nil
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- Apply(ctx, db, ApplySpec{
			Owner: CoreOwner, Schema: "core", Dialect: "postgres",
			Migrations: []Migration{{Version: 1, SQL: "SELECT pg_advisory_xact_lock(424242); CREATE TABLE borrowed_ok (id INT);"}},
		})
	}()
	<-started
	assertDefaultSearchPath(t, db)
	if _, err := holder.ExecContext(ctx, `SELECT pg_advisory_unlock(424242)`); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	migrationAfterSearchPath = nil
	assertDefaultSearchPath(t, db)
}

func assertDefaultSearchPath(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var sp string
		if err := conn.QueryRowContext(ctx, `SHOW search_path`).Scan(&sp); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		var one int
		if err := conn.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
			conn.Close()
			t.Fatal(err, one)
		}
		conn.Close()
		if sp == "core" || strings.HasPrefix(sp, "core,") || strings.HasPrefix(sp, "core ") {
			t.Fatalf("borrowed connection %d has search_path %q", i, sp)
		}
	}
}

func TestPostgresMigrationConnectionIsolation(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	ms := []Migration{{
		Version: 1,
		SQL:     "CREATE TABLE path_probe (sp TEXT);\nINSERT INTO path_probe (sp) VALUES (current_setting('search_path'));\n",
	}}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := db.QueryRowContext(ctx, `SELECT sp FROM core.path_probe`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen, "core") {
		t.Fatalf("migration connection search_path %q", seen)
	}
	for i := 0; i < 8; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var sp string
		if err := conn.QueryRowContext(ctx, `SHOW search_path`).Scan(&sp); err != nil {
			t.Fatal(err)
		}
		if sp == "core" || strings.HasPrefix(sp, "core,") || strings.HasPrefix(sp, "core ") {
			t.Fatalf("pooled connection %d kept search_path %q", i, sp)
		}
		var n int
		err = conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM path_probe`).Scan(&n)
		if err == nil {
			t.Fatalf("unqualified path_probe visible on connection %d with search_path %q", i, sp)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRangesAndEmbedded(t *testing.T) {
	if err := ValidateMigrationVersions(nil); err != nil {
		t.Fatal(err)
	}
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMigrationVersions(ms); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ms {
		task, ok := TaskForVersion(m.Version)
		if !ok || m.Version != 1 || task != "contracts" || !strings.Contains(m.Name, "core_schema") {
			t.Fatal(m.Version, task, m.Name)
		}
		found = true
	}
	if !found || len(ms) != 1 {
		t.Fatal(len(ms))
	}
	if err := ValidateMigrationVersions([]Migration{{Version: 5000}}); err == nil {
		t.Fatal("unallocated version accepted")
	}
}

func TestSeedAndSetup(t *testing.T) {
	ctx := context.Background()
	db := openCoreDB(t)
	setup, err := ReadSetup(ctx, db)
	if err != nil || setup.Complete {
		t.Fatal(setup, err)
	}
	if err := ApplyReference(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyReference(ctx, db, "postgres"); err != nil {
		t.Fatal("reference rerun", err)
	}
	var settingsN, cats int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&settingsN); err != nil {
		t.Fatal(err)
	}
	if settingsN != len(settings.Definitions()) {
		t.Fatal(settingsN, len(settings.Definitions()))
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM doc_categories`).Scan(&cats); err != nil || cats != 17 {
		t.Fatal(cats, err)
	}
	setup, err = ReadSetup(ctx, db)
	if err != nil || setup.Complete || setup.FirmName != "" {
		t.Fatal(setup, err)
	}
	if err := ApplySynthetic(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	if err := ApplySynthetic(ctx, db, "postgres"); !errors.Is(err, ErrAlreadySeeded) {
		t.Fatal(err)
	}
	for _, rec := range domain.RecordTypes() {
		var n int
		q := "SELECT COUNT(*) FROM " + rec.TableName()
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil || n < 1 {
			t.Fatalf("%s count %d err %v", rec.TableName(), n, err)
		}
	}
	setup, err = ReadSetup(ctx, db)
	if err != nil || !setup.Complete || setup.FirmName != "Seed Firm" || setup.AdminUsers != 1 || setup.AdminTOTP != 1 {
		t.Fatal(setup, err)
	}
	var migrations int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil || migrations < 1 {
		t.Fatal(migrations, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO matter_parties (id, created_at, updated_at, created_by, matter_id, contact_id, role, is_primary, role_attributes, service_address, notes) VALUES ('bad', 1, 1, 'x', 'missing-matter', 'missing-contact', 'client', 0, '{}', '', '')`); err == nil {
		t.Fatal("foreign key not enforced")
	}
	id, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, 900)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fee_agreements (id, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, status, signed_on, void_reason, file_id)
		SELECT $1, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, 'signed', signed_on, void_reason, file_id FROM fee_agreements LIMIT 1`, string(id)); err == nil {
		t.Fatal("second active fee agreement accepted")
	}
	voidID, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, 901)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fee_agreements (id, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, status, signed_on, void_reason, file_id)
		SELECT $1, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, 'void', signed_on, 'seed', file_id FROM fee_agreements LIMIT 1`, string(voidID)); err != nil {
		t.Fatal("void agreement", err)
	}
	timerID, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, 902)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timers (id, created_at, updated_at, created_by, matter_id, user_id, started_at, stopped_at, time_entry_id)
		SELECT $1, created_at, updated_at, created_by, matter_id, user_id, started_at, NULL, NULL FROM timers LIMIT 1`, string(timerID)); err == nil {
		t.Fatal("second running timer accepted")
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openCoreDB(t)
	if err := ApplyReference(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	store := SettingsStore{DB: db, Dialect: "postgres"}
	reg, err := settings.New(store, allowAudit{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get(ctx, "firm.matter_number_format")
	if err != nil || got != "YYYY-NNNN" {
		t.Fatal(got, err)
	}
	if _, err := reg.Set(ctx, "billing.time_increment_minutes", "10", actor()); err != nil {
		t.Fatal(err)
	}
	got, err = reg.Get(ctx, "billing.time_increment_minutes")
	if err != nil || got != "10" {
		t.Fatal(got, err)
	}
	if _, err := reg.Reset(ctx, "billing.time_increment_minutes", actor()); err != nil {
		t.Fatal(err)
	}
	got, err = reg.Get(ctx, "billing.time_increment_minutes")
	if err != nil || got != "6" {
		t.Fatal(got, err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	sealed, err := settings.NewSealingStore(store, key, func(k string) bool { return k == "mail.smtp_password" })
	if err != nil {
		t.Fatal(err)
	}
	secretReg, err := settings.New(sealed, allowAudit{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secretReg.Set(ctx, "mail.smtp_password", "stored-secret", actor()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'mail.smtp_password'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "stored-secret") || !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatal(raw)
	}
	got, err = secretReg.Get(ctx, "mail.smtp_password")
	if err != nil || got != "stored-secret" {
		t.Fatal(got, err)
	}
	var audits int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'setting' AND entity_id = 'billing.time_increment_minutes'`).Scan(&audits); err != nil || audits < 1 {
		t.Fatal(audits, err)
	}
	regFail, err := settings.New(store, failAudit{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := regFail.Set(ctx, "search.min_chars", "4", actor()); err == nil {
		t.Fatal("failed audit committed a setting")
	}
	got, err = reg.Get(ctx, "search.min_chars")
	if err != nil || got != "2" {
		t.Fatal(got, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity_id = 'search.min_chars'`).Scan(&audits); err != nil || audits != 0 {
		t.Fatal(audits, err)
	}
	who := domain.Principal{
		UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin, domain.RoleAttorney},
		Source: domain.SourceAPIToken, TokenID: "tok-1", ModuleID: "matter-clock",
	}
	if _, err := reg.Set(ctx, "notes.page_size", "30", who); err != nil {
		t.Fatal(err)
	}
	var role, source, reason string
	if err := db.QueryRowContext(ctx, `SELECT role, source, reason FROM audit_log WHERE entity_id = 'notes.page_size'`).Scan(&role, &source, &reason); err != nil {
		t.Fatal(err)
	}
	if role != "admin,attorney" || source != "api_token" || !strings.Contains(reason, "token=tok-1") || !strings.Contains(reason, "module=matter-clock") {
		t.Fatalf("role=%q source=%q reason=%q", role, source, reason)
	}

	var beforeValue string
	var beforeAt int64
	var auditsBefore int
	if err := db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = 'security.lockout_attempts'`).Scan(&beforeValue, &beforeAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&auditsBefore); err != nil {
		t.Fatal(err)
	}
	denied := []domain.Principal{
		{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI},
		{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAccountant}, Source: domain.SourceUI},
		{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleClient}, Source: domain.SourceUI},
		{UserID: domain.SystemUserID, Source: domain.SourceAPIToken},
	}
	for _, who := range denied {
		if _, err := reg.Set(ctx, "security.lockout_attempts", "8", who); !errors.Is(err, settings.ErrForbidden) {
			t.Fatalf("set %+v: %v", who.Roles, err)
		}
		if _, err := reg.Reset(ctx, "security.lockout_attempts", who); !errors.Is(err, settings.ErrForbidden) {
			t.Fatalf("reset %+v: %v", who.Roles, err)
		}
	}
	var afterValue string
	var afterAt int64
	var auditsAfter int
	if err := db.QueryRowContext(ctx, `SELECT value, updated_at FROM settings WHERE key = 'security.lockout_attempts'`).Scan(&afterValue, &afterAt); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log`).Scan(&auditsAfter); err != nil {
		t.Fatal(err)
	}
	if afterValue != beforeValue || afterAt != beforeAt || auditsAfter != auditsBefore {
		t.Fatalf("denied write changed settings or audits: value %s->%s at %d->%d audits %d->%d", beforeValue, afterValue, beforeAt, afterAt, auditsBefore, auditsAfter)
	}
}

type failAudit struct{}

func (failAudit) SettingChanged(context.Context, settings.Change) error {
	return errors.New("audit down")
}

func TestPostgresSettingsInterleaved(t *testing.T) {
	ctx := context.Background()
	db := openCoreDB(t)
	if err := ApplyReference(ctx, db, "postgres"); err != nil {
		t.Fatal(err)
	}
	store := SettingsStore{DB: db, Dialect: "postgres"}
	entered := make(chan struct{})
	release := make(chan struct{})
	regA, err := settings.New(store, blockAudit{entered: entered, release: release})
	if err != nil {
		t.Fatal(err)
	}
	regB, err := settings.New(store, allowAudit{})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := regA.Set(ctx, "notes.page_size", "50", actor())
		errCh <- err
	}()
	<-entered
	done := make(chan error, 1)
	go func() {
		_, err := regB.Set(ctx, "notes.page_size", "40", actor())
		done <- err
	}()
	close(release)
	if err := <-errCh; err == nil {
		t.Fatal("rolled back writer returned success")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := regB.Get(ctx, "notes.page_size")
	if err != nil || got != "40" {
		t.Fatalf("interleaved value %s %v", got, err)
	}
}

type blockAudit struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockAudit) SettingChanged(context.Context, settings.Change) error {
	b.entered <- struct{}{}
	<-b.release
	return errors.New("audit down")
}

type allowAudit struct{}

func (allowAudit) SettingChanged(context.Context, settings.Change) error { return nil }

package db

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"database/sql"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func applyEmbedded(t *testing.T, db *sql.DB) {
	t.Helper()
	ms, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
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
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: ms}); err != nil {
		t.Fatal("rerun", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE owner = ?`, CoreOwner).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	ms[0].SQL += "\n-- edited\n"
	ms[0].Checksum = ""
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: ms[:1]}); !errors.Is(err, ErrChecksum) {
		t.Fatal(err)
	}
	other := []Migration{{Version: 2, Name: "0002_earlier", SQL: "CREATE TABLE IF NOT EXISTS earlier (id TEXT);\n"}}
	if err := Apply(ctx, db, ApplySpec{Owner: "mod_clock", Dialect: "sqlite", Migrations: other}); err != nil {
		t.Fatal(err)
	}
	bad := []Migration{{Version: 3, SQL: "CREATE TABLE kept (id TEXT); INSERT INTO missing_table (id) VALUES ('x');"}}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: bad}); err == nil {
		t.Fatal("bad migration succeeded")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 3`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed migration recorded", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'kept'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("failed migration left a table", n, err)
	}
	dup := []Migration{{Version: 4, SQL: "SELECT 1"}, {Version: 4, SQL: "SELECT 1"}}
	if err := Apply(ctx, db, ApplySpec{Owner: CoreOwner, Dialect: "sqlite", Migrations: dup}); !errors.Is(err, ErrVersion) {
		t.Fatal(err)
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
	db := openDB(t)
	applyEmbedded(t, db)
	setup, err := ReadSetup(ctx, db)
	if err != nil || setup.Complete {
		t.Fatal(setup, err)
	}
	if err := ApplyReference(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyReference(ctx, db, "sqlite"); err != nil {
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
	if err := ApplySynthetic(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := ApplySynthetic(ctx, db, "sqlite"); !errors.Is(err, ErrAlreadySeeded) {
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
		SELECT ?, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, 'signed', signed_on, void_reason, file_id FROM fee_agreements LIMIT 1`, string(id)); err == nil {
		t.Fatal("second active fee agreement accepted")
	}
	voidID, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, 901)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO fee_agreements (id, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, status, signed_on, void_reason, file_id)
		SELECT ?, created_at, updated_at, created_by, matter_id, fee_type, amount_cents, services, 'void', signed_on, 'seed', file_id FROM fee_agreements LIMIT 1`, string(voidID)); err != nil {
		t.Fatal("void agreement", err)
	}
	timerID, err := domain.ComposeV7(uint64(seedAt.UnixMilli()), 1, 902)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO timers (id, created_at, updated_at, created_by, matter_id, user_id, started_at, stopped_at, time_entry_id)
		SELECT ?, created_at, updated_at, created_by, matter_id, user_id, started_at, NULL, NULL FROM timers LIMIT 1`, string(timerID)); err == nil {
		t.Fatal("second running timer accepted")
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	applyEmbedded(t, db)
	if err := ApplyReference(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	store := SettingsStore{DB: db, Dialect: "sqlite"}
	reg, err := settings.New(store, allowAudit{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg.Get(ctx, "firm.matter_number_format")
	if err != nil || got != "YYYY-NNNN" {
		t.Fatal(got, err)
	}
	if _, err := reg.Set(ctx, "billing.time_increment_minutes", "10", domain.SystemUserID); err != nil {
		t.Fatal(err)
	}
	got, err = reg.Get(ctx, "billing.time_increment_minutes")
	if err != nil || got != "10" {
		t.Fatal(got, err)
	}
	if _, err := reg.Reset(ctx, "billing.time_increment_minutes", domain.SystemUserID); err != nil {
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
	if _, err := secretReg.Set(ctx, "mail.smtp_password", "stored-secret", domain.SystemUserID); err != nil {
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
}

type allowAudit struct{}

func (allowAudit) SettingChanged(context.Context, settings.Change) error { return nil }

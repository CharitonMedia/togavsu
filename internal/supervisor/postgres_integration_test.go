//go:build integration

package supervisor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresRestoreReplacesEvolvedSchema(t *testing.T) {
	url := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	admin, err := withDatabase(url, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	psql := os.Getenv("TOGAVSU_PSQL")
	apply := ApplyCommand(psql, url)
	reset := "DROP SCHEMA IF EXISTS core CASCADE; DROP SCHEMA IF EXISTS extra_mod CASCADE;"
	if err := apply(ctx, strings.NewReader(reset)); err != nil {
		t.Fatal(err)
	}
	seed := `
CREATE SCHEMA core;
CREATE TABLE core.synthetic_items (id integer PRIMARY KEY, name text);
INSERT INTO core.synthetic_items (id, name) VALUES (1, 'alpha'), (2, 'beta'), (3, 'gamma');
`
	if err := apply(ctx, strings.NewReader(seed)); err != nil {
		t.Fatal(err)
	}
	store := &Store{
		Dir:        t.TempDir(),
		Passphrase: "synthetic-passphrase",
		Dump:       DumpCommand(os.Getenv("TOGAVSU_PG_DUMP"), url),
	}
	meta, err := store.Create(ctx, reasonPreUpdate)
	if err != nil {
		t.Fatal(err)
	}
	evolved := `
INSERT INTO core.synthetic_items (id, name) VALUES (4, 'later');
CREATE TABLE core.extra (id integer);
CREATE SCHEMA extra_mod;
CREATE TABLE extra_mod.kept (id integer);
`
	if err := apply(ctx, strings.NewReader(evolved)); err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "wrong-passphrase", func(context.Context, RestorePayload) error {
		t.Fatal("wrong passphrase reached apply")
		return nil
	})
	if err == nil {
		t.Fatal("wrong passphrase was accepted")
	}
	err = store.Restore(ctx, meta.ID, "", func(ctx context.Context, payload RestorePayload) error {
		if payload.Manifest.RecordCounts["core.synthetic_items"] != 3 {
			t.Fatalf("manifest counts %#v", payload.Manifest.RecordCounts)
		}
		return RestoreIntoCleanDatabase(ctx, psql, admin, url, payload.Dump, payload.Manifest.RecordCounts)
	})
	if err != nil {
		t.Fatal(err)
	}
	names, err := psqlLines(ctx, psql, url, "SELECT name FROM core.synthetic_items ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "alpha,beta,gamma" {
		t.Fatalf("rows %v", names)
	}
	extra, err := psqlLines(ctx, psql, url, "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'core' AND table_name = 'extra'")
	if err != nil {
		t.Fatal(err)
	}
	if len(extra) != 1 || extra[0] != "0" {
		t.Fatalf("core.extra survived: %v", extra)
	}
	mod, err := psqlLines(ctx, psql, url, "SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'extra_mod'")
	if err != nil {
		t.Fatal(err)
	}
	if len(mod) != 1 || mod[0] != "0" {
		t.Fatalf("extra_mod survived: %v", mod)
	}
}

func TestUpdateRollbackKeepsWriteAfterSnapshot(t *testing.T) {
	url := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	admin, err := withDatabase(url, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	psql := os.Getenv("TOGAVSU_PSQL")
	apply := ApplyCommand(psql, url)
	if err := apply(ctx, strings.NewReader("DROP SCHEMA IF EXISTS core CASCADE; CREATE SCHEMA core; CREATE TABLE core.synthetic_items (id integer PRIMARY KEY, name text); INSERT INTO core.synthetic_items (id, name) VALUES (1, 'before');")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ApplyCommand(psql, url)(context.Background(), strings.NewReader("DROP SCHEMA IF EXISTS core CASCADE;"))
	})
	store := &Store{
		Dir:        t.TempDir(),
		Passphrase: "synthetic-passphrase",
		Dump:       DumpCommand(os.Getenv("TOGAVSU_PG_DUMP"), url),
	}
	meta, err := store.Create(ctx, reasonPreUpdate)
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(ctx, strings.NewReader("INSERT INTO core.synthetic_items (id, name) VALUES (2, 'after-snapshot');")); err != nil {
		t.Fatal(err)
	}
	replayed := false
	var order []string
	up := &Updater{
		Now:      func() time.Time { return time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC) },
		Window:   Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: time.UTC},
		Registry: staticRegistry{Release{Image: testReleaseImage, Version: "9.0.0"}},
		Verifier: allowVerifier{},
		Runtime:  &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Labels: map[string]string{"togavsu.version": "1.0.0"}}}},
		Proxy:    scriptProxy{order: &order},
		Migrator: migratorFunc(func(_ context.Context, image string) (bool, error) {
			order = append(order, "migrate:"+image)
			return true, errMigration
		}),
		Backup: func(context.Context, string) (BackupMeta, error) { return meta, nil },
		Restore: func(ctx context.Context, id string) error {
			replayed = true
			return store.Restore(ctx, id, "", func(ctx context.Context, payload RestorePayload) error {
				return RestoreIntoCleanDatabase(ctx, psql, admin, url, payload.Dump, payload.Manifest.RecordCounts)
			})
		},
	}
	got, err := up.Apply(ctx, true)
	if !errors.Is(err, errMigration) {
		t.Fatalf("got %v", err)
	}
	if !got.MigrationRan || got.Rollback != "preserved_writes" || got.Restored {
		t.Fatalf("result %+v", got)
	}
	if !containsStep(order, "migrate:"+testReleaseImage) {
		t.Fatalf("migration did not run: %v", order)
	}
	if replayed {
		t.Fatal("update rollback replayed the pre-update dump while the old core was still the writer")
	}
	names, err := psqlLines(ctx, psql, url, "SELECT name FROM core.synthetic_items ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "before,after-snapshot" {
		t.Fatalf("rows %v", names)
	}
}

var errMigration = errString("migration failed")

type errString string

func (e errString) Error() string { return string(e) }

type migratorFunc func(context.Context, string) (bool, error)

func (f migratorFunc) Apply(ctx context.Context, image string) (bool, error) { return f(ctx, image) }

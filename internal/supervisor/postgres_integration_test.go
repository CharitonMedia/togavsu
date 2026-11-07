//go:build integration

package supervisor

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestPostgresDumpRestoreCounts(t *testing.T) {
	url := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	psql := os.Getenv("TOGAVSU_PSQL")
	apply := ApplyCommand(psql, url)
	reset := "DROP SCHEMA IF EXISTS core CASCADE;"
	if err := apply(ctx, strings.NewReader(reset)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = apply(context.Background(), strings.NewReader(reset)) })
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
	err = store.Restore(ctx, meta.ID, "wrong-passphrase", func(context.Context, RestorePayload) error {
		t.Fatal("wrong passphrase reached apply")
		return nil
	})
	if err == nil {
		t.Fatal("wrong passphrase was accepted")
	}
	// A write after the dump must not change the archived counts, and restore
	// must replace the existing tables without a manual drop.
	if err := apply(ctx, strings.NewReader(`INSERT INTO core.synthetic_items (id, name) VALUES (4, 'later');`)); err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "", func(ctx context.Context, payload RestorePayload) error {
		if payload.Manifest.RecordCounts["core.synthetic_items"] != 3 {
			t.Fatalf("manifest counts %#v", payload.Manifest.RecordCounts)
		}
		return apply(ctx, payload.Dump)
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := CountCommand(psql, url)
	after, err := counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after["core.synthetic_items"] != 3 {
		t.Fatalf("restored counts %#v", after)
	}
	names, err := psqlLines(ctx, psql, url, "SELECT name FROM core.synthetic_items ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "alpha,beta,gamma" {
		t.Fatalf("rows %v", names)
	}
}

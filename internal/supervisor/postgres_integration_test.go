//go:build integration

package supervisor

import (
	"context"
	"io"
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
	apply := ApplyCommand(os.Getenv("TOGAVSU_PSQL"), url)
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
	counts := CountCommand(os.Getenv("TOGAVSU_PSQL"), url)
	before, err := counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before["core.synthetic_items"] != 3 {
		t.Fatalf("seed counts %#v", before)
	}
	store := &Store{
		Dir:        t.TempDir(),
		Passphrase: "synthetic-passphrase",
		Dump:       DumpCommand(os.Getenv("TOGAVSU_PG_DUMP"), url),
		Counts:     counts,
	}
	meta, err := store.Create(ctx, reasonPreUpdate)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "wrong-passphrase", func(context.Context, io.Reader, Manifest) error {
		t.Fatal("wrong passphrase reached apply")
		return nil
	})
	if err == nil {
		t.Fatal("wrong passphrase was accepted")
	}
	if err := apply(ctx, strings.NewReader(reset)); err != nil {
		t.Fatal(err)
	}
	err = store.Restore(ctx, meta.ID, "", func(ctx context.Context, r io.Reader, man Manifest) error {
		if man.RecordCounts["core.synthetic_items"] != 3 {
			t.Fatalf("Manifest counts %#v", man.RecordCounts)
		}
		return apply(ctx, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after["core.synthetic_items"] != 3 {
		t.Fatalf("restored counts %#v", after)
	}
}

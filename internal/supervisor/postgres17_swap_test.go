//go:build integration

package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPostgres17Swap(t *testing.T) {
	admin := postgres17Admin(t)
	psql := os.Getenv("TOGAVSU_PSQL")
	if psql == "" {
		psql = "psql"
	}
	if path, err := exec.LookPath(psql); err == nil {
		psql = path
	}
	ctx := context.Background()
	ver, err := psqlLines(ctx, psql, admin, "SHOW server_version_num")
	if err != nil || len(ver) != 1 {
		t.Fatalf("version: %v %v", ver, err)
	}
	num, err := strconv.Atoi(ver[0])
	if err != nil || num < 170000 {
		t.Fatalf("Postgres 17 is required, got %s", ver[0])
	}

	t.Run("second rename failure rolls back the first", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "fail-between", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if err == nil || errors.Is(err, ErrRestoreCleanup) {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatalf("pair was not rolled back orig=%s fresh=%s replaced=%v", note(t, psql, admin, orig), note(t, psql, admin, fresh), databaseExists(t, psql, admin, replaced))
		}
	})

	t.Run("lost acknowledgment of only the first rename", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "lose-first", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if err == nil || errors.Is(err, ErrRestoreCleanup) || errors.Is(err, errConfiguredDatabaseAbsent) {
			t.Fatalf("got %v", err)
		}
		if !databaseExists(t, psql, admin, orig) || note(t, psql, admin, orig) != "old" {
			t.Fatal("configured database was not restored with its original writes")
		}
		if !databaseExists(t, psql, admin, fresh) || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatalf("fresh=%v replaced=%v", databaseExists(t, psql, admin, fresh), databaseExists(t, psql, admin, replaced))
		}
	})

	t.Run("lost acknowledgment of the committed pair", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "lose-commit", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if err != nil {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, fresh) || databaseExists(t, psql, admin, replaced) {
			t.Fatalf("committed pair was not published orig=%s fresh=%v replaced=%v", note(t, psql, admin, orig), databaseExists(t, psql, admin, fresh), databaseExists(t, psql, admin, replaced))
		}
	})

	t.Run("drop failure keeps the restored database", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "fail-drop", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if !errors.Is(err, ErrRestoreCleanup) {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "new" || note(t, psql, admin, replaced) != "old" || databaseExists(t, psql, admin, fresh) {
			t.Fatal("cleanup state was not truthful")
		}
	})

	t.Run("lost acknowledgment of the drop", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "lose-drop", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if err != nil {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, fresh) || databaseExists(t, psql, admin, replaced) {
			t.Fatal("a committed drop was reported as incomplete")
		}
	})

	t.Run("failure before the pair", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		bin := writeSwapWrapper(t, psql, "fail-before", orig, fresh, replaced)
		err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if err == nil || errors.Is(err, ErrRestoreCleanup) {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("databases changed before the pair")
		}
	})

	t.Run("recovery of a committed swap does not roll it back", func(t *testing.T) {
		orig, replaced := recoveryPair(t, psql, admin)
		var order []string
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		rec := journalRecord{
			Record: RestoreRecord{ID: "restore-cleanup", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: orig, Fresh: "tgswap_absent", Replaced: replaced, SwapIntended: true},
		}
		if err := ops.Journal.Put(rec); err != nil {
			t.Fatal(err)
		}
		if err := ops.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "new" || note(t, psql, admin, replaced) != "old" {
			t.Fatal("recovery renamed a committed database")
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("writers stayed stopped after the catalog check: %v", order)
		}
		got, ok, err := ops.LookupRestore(rec.Record.ID)
		if err != nil || !ok || got.Status != "completed" || got.Error != "cleanup_pending" || got.Cleanup != "pending" {
			t.Fatalf("status %+v %v %v", got, ok, err)
		}
	})

	t.Run("recovery does not start writers when the configured name is missing", func(t *testing.T) {
		_, replaced := recoveryPair(t, psql, admin)
		dropDatabaseIfExists(psql, admin, replaced)
		var order []string
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		missing := "tgswap_missing"
		rec := journalRecord{
			Record: RestoreRecord{ID: "restore-missing", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: missing, Fresh: "tgswap_fresh", Replaced: "tgswap_replaced", SwapIntended: true},
		}
		if err := ops.Journal.Put(rec); err != nil {
			t.Fatal(err)
		}
		err := ops.Recover(ctx)
		if err == nil || containsStep(order, "restart:core-1") {
			t.Fatalf("err %v order %v", err, order)
		}
		got, _, _ := ops.LookupRestore(rec.Record.ID)
		if got.Status == "completed" || got.Recovery != "configured_database_absent" {
			t.Fatalf("%+v", got)
		}
	})

	t.Run("recovery does not start writers when the catalog cannot be read", func(t *testing.T) {
		var order []string
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: "postgres://togavsu:synthetic@127.0.0.1:1/postgres?sslmode=disable"})
		rec := journalRecord{
			Record: RestoreRecord{ID: "restore-unread", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: "tgswap_cfg", Fresh: "tgswap_fresh", Replaced: "tgswap_old", SwapIntended: true},
		}
		if err := ops.Journal.Put(rec); err != nil {
			t.Fatal(err)
		}
		err := ops.Recover(ctx)
		if err == nil || containsStep(order, "restart:core-1") {
			t.Fatalf("err %v order %v", err, order)
		}
		got, _, _ := ops.LookupRestore(rec.Record.ID)
		if got.Status == "completed" || got.Status == "failed" || got.Recovery != "catalog_unobserved" {
			t.Fatalf("%+v", got)
		}
	})
}

func postgres17Admin(t *testing.T) string {
	t.Helper()
	if admin := os.Getenv("TOGAVSU_PG17_ADMIN_URL"); admin != "" {
		return admin
	}
	raw := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	admin, err := withDatabase(raw, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	return admin
}

func swapPair(t *testing.T, psql, admin string) (orig, fresh, replaced string) {
	t.Helper()
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(raw[:])
	orig = "tgswapo" + suffix
	fresh = "tgswapf" + suffix
	replaced = orig + "_replaced_" + suffix
	seedMarker(t, psql, admin, orig, "old")
	seedMarker(t, psql, admin, fresh, "new")
	t.Cleanup(func() {
		dropDatabaseIfExists(psql, admin, orig)
		dropDatabaseIfExists(psql, admin, fresh)
		dropDatabaseIfExists(psql, admin, replaced)
	})
	return orig, fresh, replaced
}

func hexSuffixOf(orig string) string {
	return strings.TrimPrefix(orig, "tgswapo")
}

func seedMarker(t *testing.T, psql, admin, name, value string) {
	t.Helper()
	if _, err := psqlLines(context.Background(), psql, admin, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	target, err := withDatabase(admin, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := psqlExec(context.Background(), psql, target, "CREATE TABLE marker(note text); INSERT INTO marker VALUES ('"+value+"');"); err != nil {
		t.Fatal(err)
	}
}

func note(t *testing.T, psql, admin, name string) string {
	t.Helper()
	target, err := withDatabase(admin, name)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := psqlLines(context.Background(), psql, target, "SELECT note FROM marker")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("%s notes %v", name, lines)
	}
	return lines[0]
}

func databaseExists(t *testing.T, psql, admin, name string) bool {
	t.Helper()
	lines, err := psqlLines(context.Background(), psql, admin, "SELECT datname FROM pg_database WHERE datname = '"+name+"'")
	if err != nil {
		t.Fatal(err)
	}
	return len(lines) == 1
}

func dropDatabaseIfExists(psql, admin, name string) {
	_, _ = psqlLines(context.Background(), psql, admin, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '"+name+"' AND pid <> pg_backend_pid()")
	_, _ = psqlLines(context.Background(), psql, admin, "DROP DATABASE IF EXISTS "+name)
}

func recoveryOps(t *testing.T, rt Runtime, catalog Catalog) *Operations {
	t.Helper()
	dir := t.TempDir()
	return &Operations{
		Gate:    &Gate{},
		Runtime: rt,
		Catalog: catalog,
		Journal: OpenJournal(filepath.Join(dir, "restore-journal.jsonl")),
		Ledger:  OpenLedger(filepath.Join(dir, "ledger.jsonl")),
	}
}

func recoveryPair(t *testing.T, psql, admin string) (orig, replaced string) {
	t.Helper()
	var raw [4]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(raw[:])
	orig = "tgrecovo" + suffix
	replaced = "tgrecor" + suffix
	seedMarker(t, psql, admin, orig, "new")
	seedMarker(t, psql, admin, replaced, "old")
	t.Cleanup(func() {
		dropDatabaseIfExists(psql, admin, orig)
		dropDatabaseIfExists(psql, admin, replaced)
	})
	return orig, replaced
}

func writeSwapWrapper(t *testing.T, real, mode, orig, fresh, replaced string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "psql")
	script := fmt.Sprintf("#!/bin/sh\nREAL=%q\nMODE=%q\nORIG=%q\nFRESH=%q\nREPLACED=%q\n", real, mode, orig, fresh, replaced) + `cmd=
prev=
dbname=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  if [ "$prev" = "--dbname" ]; then dbname=$arg; fi
  prev=$arg
done
case "$cmd" in
  BEGIN*)
    case "$MODE" in
      fail-before)
        echo refused >&2
        exit 1
        ;;
      fail-between)
        "$REAL" --set ON_ERROR_STOP=1 --dbname "$dbname" --command "BEGIN; ALTER DATABASE $ORIG RENAME TO $REPLACED; ALTER DATABASE $FRESH RENAME TO $REPLACED; COMMIT;"
        exit $?
        ;;
      lose-first)
        "$REAL" --set ON_ERROR_STOP=1 --dbname "$dbname" --command "ALTER DATABASE $ORIG RENAME TO $REPLACED" || exit 1
        echo "lost acknowledgment" >&2
        exit 1
        ;;
      lose-commit)
        "$REAL" "$@"
        status=$?
        if [ "$status" -ne 0 ]; then exit "$status"; fi
        echo "lost acknowledgment" >&2
        exit 1
        ;;
    esac
    ;;
  "DROP DATABASE "*)
    case "$MODE" in
      fail-drop)
        echo refused >&2
        exit 1
        ;;
      lose-drop)
        "$REAL" "$@"
        status=$?
        if [ "$status" -ne 0 ]; then exit "$status"; fi
        echo "lost acknowledgment" >&2
        exit 1
        ;;
    esac
    ;;
esac
exec "$REAL" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

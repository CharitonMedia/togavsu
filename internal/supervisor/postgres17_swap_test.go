//go:build integration

package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPostgres17Swap(t *testing.T) {
	switch os.Getenv("TOGAVSU_SWAP_CHILD") {
	case "1":
		runSwapCommitChild()
		return
	case "rollback":
		runSwapRollbackChild()
		return
	}
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
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("recovery rolled back the new database or left the replaced database")
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("writers stayed stopped after the catalog check: %v", order)
		}
		got, ok, err := ops.LookupRestore(rec.Record.ID)
		if err != nil || !ok || got.Status != "completed" || got.Error != "" || got.Cleanup != "" {
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

	t.Run("committed outcome is durable before the old name is dropped", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		dir := t.TempDir()
		journalPath := filepath.Join(dir, "restore-journal.jsonl")
		ready := filepath.Join(dir, "ready")
		cmd := exec.Command(os.Args[0], "-test.run=TestPostgres17Swap", "-test.count=1")
		cmd.Env = append(os.Environ(),
			"TOGAVSU_SWAP_CHILD=1",
			"TOGAVSU_SWAP_ADMIN="+admin,
			"TOGAVSU_SWAP_PSQL="+psql,
			"TOGAVSU_SWAP_ORIG="+orig,
			"TOGAVSU_SWAP_FRESH="+fresh,
			"TOGAVSU_SWAP_REPLACED="+replaced,
			"TOGAVSU_SWAP_JOURNAL="+journalPath,
			"TOGAVSU_SWAP_READY="+ready,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatal("child did not record the committed swap")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		if note(t, psql, admin, orig) != "new" || note(t, psql, admin, replaced) != "old" {
			t.Fatal("rename pair was not committed, or the old database was already dropped")
		}
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		ops.Journal = OpenJournal(journalPath)
		if err := ops.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("recovery rolled back the committed database or left the replaced database")
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("writers stayed stopped after the committed outcome: %v", order)
		}
		got, ok, err := ops.LookupRestore("restore-crash")
		if err != nil || !ok || got.Status != "completed" || got.Cleanup != "" || got.Error != "" {
			t.Fatalf("outcome %+v %v %v", got, ok, err)
		}
	})

	t.Run("rolled-back outcome is durable before the loaded name is dropped", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		dir := t.TempDir()
		journalPath := filepath.Join(dir, "restore-journal.jsonl")
		ready := filepath.Join(dir, "ready")
		cmd := exec.Command(os.Args[0], "-test.run=TestPostgres17Swap", "-test.count=1")
		cmd.Env = append(os.Environ(),
			"TOGAVSU_SWAP_CHILD=rollback",
			"TOGAVSU_SWAP_ADMIN="+admin,
			"TOGAVSU_SWAP_PSQL="+psql,
			"TOGAVSU_SWAP_ORIG="+orig,
			"TOGAVSU_SWAP_FRESH="+fresh,
			"TOGAVSU_SWAP_REPLACED="+replaced,
			"TOGAVSU_SWAP_JOURNAL="+journalPath,
			"TOGAVSU_SWAP_READY="+ready,
		)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			if time.Now().After(deadline) {
				_ = cmd.Process.Kill()
				t.Fatal("child did not record the rolled-back swap")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("rollback was not intact when the journal record became durable")
		}
		recorded, ok, err := OpenJournal(journalPath).Latest("restore-rollback")
		if err != nil || !ok || !recorded.Database.SwapRolledBack {
			t.Fatalf("journal %+v %v %v", recorded, ok, err)
		}
		dropDatabaseIfExists(psql, admin, fresh)
		if databaseExists(t, psql, admin, fresh) {
			t.Fatal("loaded database still exists")
		}
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		ops.Journal = OpenJournal(journalPath)
		if err := ops.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "old" {
			t.Fatal("recovery replaced the configured database after a recorded rollback")
		}
		if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
			t.Fatalf("writers stayed stopped after the rolled-back outcome: %v", order)
		}
		got, ok, err := ops.LookupRestore("restore-rollback")
		if err != nil || !ok || got.Status != "failed" || got.Recovery != "rolled_back" || got.Status == "running" {
			t.Fatalf("outcome %+v %v %v", got, ok, err)
		}
	})

	t.Run("journal failure after commit keeps the previous database", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		dir := t.TempDir()
		j := OpenJournal(filepath.Join(dir, "restore-journal.jsonl"))
		if err := j.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-unjournaled", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: orig, Fresh: fresh, Replaced: replaced, SwapIntended: true},
		}); err != nil {
			t.Fatal(err)
		}
		j.failRemaining = 1
		hookCtx := WithSwapCommitted(ctx, func(configured, freshName, replacedName string) error {
			return j.Put(journalRecord{
				Record: RestoreRecord{ID: "restore-unjournaled", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
				Phase:  phaseApplied,
				Containers: []containerIntent{{
					ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
				}},
				Database: &databaseIntent{
					Configured: configured, Fresh: freshName, Replaced: replacedName,
					SwapIntended: true, SwapCommitted: true,
				},
			})
		})
		err := swapDatabase(hookCtx, psql, admin, orig, fresh, hexSuffixOf(orig))
		if !errors.Is(err, errCommitUnjournaled) {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "new" || note(t, psql, admin, replaced) != "old" || databaseExists(t, psql, admin, fresh) {
			t.Fatal("committed pair was dropped or rolled back after the journal failed")
		}
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		box := OpenOutbox(filepath.Join(dir, "outbox"))
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		ops.Journal = j
		ops.Outbox = box
		if err := ops.Recover(ctx); err != nil && !errors.Is(err, ErrRestoreCleanup) {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("recovery rolled back the new database or left the replaced database")
		}
		if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
			t.Fatalf("order %v", order)
		}
		got, ok, lerr := ops.LookupRestore("restore-unjournaled")
		if lerr != nil || !ok || got.Status != "completed" || got.Cleanup != "" || got.Status == "failed" {
			t.Fatalf("%+v %v %v", got, ok, lerr)
		}
		notes := noticeBody(t, box)
		if strings.Contains(notes, "Existing data was left in place") || !strings.Contains(notes, "The restored database is live") {
			t.Fatalf("notice %q", notes)
		}
	})

	t.Run("rollback journal failure keeps the loaded database", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		dir := t.TempDir()
		j := OpenJournal(filepath.Join(dir, "restore-journal.jsonl"))
		if err := j.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-rollback-miss", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: orig, Fresh: fresh, Replaced: replaced, SwapIntended: true},
		}); err != nil {
			t.Fatal(err)
		}
		j.failRemaining = 1
		hookCtx := WithSwapRolledBack(ctx, func(configured, freshName, replacedName string) error {
			return j.Put(journalRecord{
				Record: RestoreRecord{ID: "restore-rollback-miss", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
				Phase:  phaseRolledBack,
				Containers: []containerIntent{{
					ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
				}},
				Database: &databaseIntent{
					Configured: configured, Fresh: freshName, Replaced: replacedName,
					SwapIntended: true, SwapRolledBack: true,
				},
			})
		})
		bin := writeSwapWrapper(t, psql, "fail-between", orig, fresh, replaced)
		err := swapDatabase(hookCtx, bin, admin, orig, fresh, hexSuffixOf(orig))
		if !errors.Is(err, errRollbackUnjournaled) {
			t.Fatalf("got %v", err)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("loaded database was removed before the rollback was recorded")
		}
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		box := OpenOutbox(filepath.Join(dir, "outbox"))
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		ops.Journal = j
		ops.Outbox = box
		if err := ops.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" {
			t.Fatal("recovery changed the rolled-back databases")
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("order %v", order)
		}
		got, ok, lerr := ops.LookupRestore("restore-rollback-miss")
		if lerr != nil || !ok || got.Status != "failed" || got.Recovery != "catalog_old_intact" {
			t.Fatalf("%+v %v %v", got, ok, lerr)
		}
		notes := noticeBody(t, box)
		if !strings.Contains(notes, "Existing data was left in place") || !strings.Contains(notes, "loaded database is still present") || strings.Contains(notes, "The restored database is live") {
			t.Fatalf("notice %q", notes)
		}
	})
}

func runSwapCommitChild() {
	j := OpenJournal(os.Getenv("TOGAVSU_SWAP_JOURNAL"))
	ctx := WithSwapCommitted(context.Background(), func(configured, fresh, replaced string) error {
		err := j.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-crash", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseApplied,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{
				Configured: configured, Fresh: fresh, Replaced: replaced,
				SwapIntended: true, SwapCommitted: true,
			},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("TOGAVSU_SWAP_READY"), []byte("ok"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		time.Sleep(time.Minute)
		return nil
	})
	_ = swapDatabase(ctx, os.Getenv("TOGAVSU_SWAP_PSQL"), os.Getenv("TOGAVSU_SWAP_ADMIN"), os.Getenv("TOGAVSU_SWAP_ORIG"), os.Getenv("TOGAVSU_SWAP_FRESH"), hexSuffixOf(os.Getenv("TOGAVSU_SWAP_ORIG")))
	os.Exit(0)
}

func runSwapRollbackChild() {
	j := OpenJournal(os.Getenv("TOGAVSU_SWAP_JOURNAL"))
	ctx := WithSwapRolledBack(context.Background(), func(configured, fresh, replaced string) error {
		err := j.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-rollback", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseRolledBack,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{
				Configured: configured, Fresh: fresh, Replaced: replaced,
				SwapIntended: true, SwapRolledBack: true,
			},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("TOGAVSU_SWAP_READY"), []byte("ok"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		time.Sleep(time.Minute)
		return nil
	})
	orig := os.Getenv("TOGAVSU_SWAP_ORIG")
	fresh := os.Getenv("TOGAVSU_SWAP_FRESH")
	replaced := os.Getenv("TOGAVSU_SWAP_REPLACED")
	psql := os.Getenv("TOGAVSU_SWAP_PSQL")
	admin := os.Getenv("TOGAVSU_SWAP_ADMIN")
	bin := filepath.Join(filepath.Dir(os.Getenv("TOGAVSU_SWAP_JOURNAL")), "psql-rollback")
	script := fmt.Sprintf("#!/bin/sh\nREAL=%q\nORIG=%q\nFRESH=%q\nREPLACED=%q\n", psql, orig, fresh, replaced) + `cmd=
prev=
dbname=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  if [ "$prev" = "--dbname" ]; then dbname=$arg; fi
  prev=$arg
done
case "$cmd" in
  BEGIN*)
    "$REAL" --set ON_ERROR_STOP=1 --dbname "$dbname" --command "BEGIN; ALTER DATABASE $ORIG RENAME TO $REPLACED; ALTER DATABASE $FRESH RENAME TO $REPLACED; COMMIT;"
    exit $?
    ;;
esac
exec "$REAL" "$@"
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	err := swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
	if errors.Is(err, errRollbackUnjournaled) || errors.Is(err, errCommitUnjournaled) || errors.Is(err, ErrRestoreCleanup) || errors.Is(err, errSwapUnobserved) || errors.Is(err, errConfiguredDatabaseAbsent) {
		os.Exit(0)
	}
	_ = dropDatabase(context.Background(), psql, admin, fresh)
	os.Exit(0)
}

func psqlBin(t *testing.T) string {
	t.Helper()
	psql := os.Getenv("TOGAVSU_PSQL")
	if psql == "" {
		psql = "psql"
	}
	if path, err := exec.LookPath(psql); err == nil {
		return path
	}
	return psql
}

func TestDelayedRenameCannotCommitAfterRecovery(t *testing.T) {
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	orig, fresh, replaced := swapPair(t, psql, admin)
	opID := mustOwnRestoreOp(t, psql, admin, orig, fresh, replaced)
	cluster, cfgOID, freshOID := dbIdentity(t, psql, admin, orig, fresh)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	release := filepath.Join(dir, "release")
	bin := delayBeforeRename(t, psql, ready, release)
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
	recID := "restore-delayed"
	if err := ops.Journal.Put(swapJournal(recID, orig, fresh, replaced, opID, cluster, cfgOID, freshOID)); err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() {
		ctx := WithRestoreOpID(context.Background(), opID)
		errc <- swapDatabase(ctx, bin, admin, orig, fresh, hexSuffixOf(orig))
	}()
	waitPath(t, ready)
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ops.LookupRestore(recID)
	if err != nil || !ok || got.Status != "failed" || got.Recovery != "catalog_old_intact" {
		t.Fatalf("recovery before the rename locked: %+v %v %v", got, ok, err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers were not restarted after cancel: %v", order)
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("late rename committed")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("delayed rename did not finish")
	}
	if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
		t.Fatal("late rename changed the catalog after recovery")
	}
	if got := opState(t, psql, admin, opID); got != "cancelled" {
		t.Fatalf("operation state %s", got)
	}
}

func TestInFlightRenameCannotCommitAfterAFailedRestore(t *testing.T) {
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	prev := swapFenceTimeout
	swapFenceTimeout = 2 * time.Second
	t.Cleanup(func() { swapFenceTimeout = prev })

	t.Run("commit while recovery waits", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		sleeper := startSleeper(t, psql, admin)
		opID := mustOwnRestoreOp(t, psql, admin, orig, fresh, replaced)
		cluster, cfgOID, freshOID := dbIdentity(t, psql, admin, orig, fresh)
		held := holdAfterFirstRename(t, psql, admin, opID, orig, fresh, replaced)
		ops, order := inflightOps(t, psql, admin, "restore-held-commit", orig, fresh, replaced, opID, cluster, cfgOID, freshOID)
		err := ops.Recover(context.Background())
		if err == nil || containsStep(*order, "restart:core-1") {
			held.stop()
			t.Fatalf("recovery finished while the rename was open: %v %v", err, *order)
		}
		got, _, _ := ops.LookupRestore("restore-held-commit")
		if got.Status == "failed" || got.Status == "completed" {
			held.stop()
			t.Fatalf("open rename was terminal: %+v", got)
		}
		if !backendAlive(t, psql, admin, sleeper) || !backendAlive(t, psql, admin, held.pid) {
			held.stop()
			t.Fatal("a backend was terminated")
		}
		held.finish(t, secondRenameSQL(opID, fresh, orig))
		*order = nil
		if err := ops.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, fresh) {
			t.Fatal("committed rename was not the visible database")
		}
		got, _, _ = ops.LookupRestore("restore-held-commit")
		if got.Status == "failed" || got.Recovery == "catalog_old_intact" {
			t.Fatalf("committed restore reported unchanged: %+v", got)
		}
	})

	t.Run("rollback then recovery reports unchanged", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		sleeper := startSleeper(t, psql, admin)
		opID := mustOwnRestoreOp(t, psql, admin, orig, fresh, replaced)
		cluster, cfgOID, freshOID := dbIdentity(t, psql, admin, orig, fresh)
		held := holdAfterFirstRename(t, psql, admin, opID, orig, fresh, replaced)
		ops, order := inflightOps(t, psql, admin, "restore-held-rollback", orig, fresh, replaced, opID, cluster, cfgOID, freshOID)
		if err := ops.Recover(context.Background()); err == nil || containsStep(*order, "restart:core-1") {
			held.stop()
			t.Fatalf("recovery finished while the rename was open: %v %v", err, *order)
		}
		if !backendAlive(t, psql, admin, sleeper) {
			held.stop()
			t.Fatal("unrelated backend was terminated")
		}
		held.finish(t, "ROLLBACK;")
		*order = nil
		if err := ops.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, _, _ := ops.LookupRestore("restore-held-rollback")
		if got.Status != "failed" || got.Recovery != "catalog_old_intact" {
			t.Fatalf("%+v", got)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("rollback did not leave the original database")
		}
	})
}

func TestCommittedRenameSurvivesLostJournal(t *testing.T) {
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	orig, fresh, replaced := swapPair(t, psql, admin)
	opID := mustOwnRestoreOp(t, psql, admin, orig, fresh, replaced)
	cluster, cfgOID, freshOID := dbIdentity(t, psql, admin, orig, fresh)
	sql, err := swapTransactionSQL(opID, orig, fresh, replaced)
	if err != nil {
		t.Fatal(err)
	}
	if err := psqlExec(context.Background(), psql, admin, sql); err != nil {
		t.Fatal(err)
	}
	ops, _ := inflightOps(t, psql, admin, "restore-lost-journal", orig, fresh, replaced, opID, cluster, cfgOID, freshOID)
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _, _ := ops.LookupRestore("restore-lost-journal")
	if got.Status == "failed" || got.Recovery == "catalog_old_intact" {
		t.Fatalf("committed swap was reported unchanged: %+v", got)
	}
	if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, fresh) {
		t.Fatal("recovery did not keep the committed database")
	}
}

func TestOwnedTempDatabaseSurvivesOnlyWhenVerified(t *testing.T) {
	if os.Getenv("TOGAVSU_LOAD_CHILD") != "" {
		runLoadChild()
		return
	}
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	product := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	if product == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL is required")
	}
	neighbor := "togavsu_restore_neighbor"
	dropDatabaseIfExists(psql, admin, neighbor)
	if err := psqlExec(context.Background(), psql, admin, "CREATE DATABASE "+neighbor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropDatabaseIfExists(psql, admin, neighbor) })

	t.Run("kill during restore load", func(t *testing.T) {
		killDuringLoad(t, psql, admin, product, "restore")
	})
	t.Run("kill during monthly check", func(t *testing.T) {
		killDuringLoad(t, psql, admin, product, "measure")
	})
	t.Run("lost create acknowledgment", func(t *testing.T) {
		bin := loseCreateAck(t, psql)
		err := RestoreIntoCleanDatabase(context.Background(), bin, admin, product, strings.NewReader("SELECT 1;\n"), map[string]int64{})
		if !errors.Is(err, errSwapUnobserved) {
			t.Fatalf("lost create: %v", err)
		}
		if err := (PostgresCatalog{Bin: psql, AdminURL: admin}).Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows := ownedFresh(t, psql, admin)
		for _, name := range rows {
			if strings.HasPrefix(name, "togavsu_restore_") || strings.HasPrefix(name, "togavsu_check_") {
				t.Fatalf("verified temp database remained: %s", name)
			}
		}
		if !databaseExists(t, psql, admin, neighbor) {
			t.Fatal("unrelated database was removed")
		}
	})
	t.Run("reused oid is not dropped", func(t *testing.T) {
		keep := "togavsu_restore_reused"
		dropDatabaseIfExists(psql, admin, keep)
		if err := psqlExec(context.Background(), psql, admin, "CREATE DATABASE "+keep); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dropDatabaseIfExists(psql, admin, keep) })
		oid, err := databaseOID(context.Background(), psql, admin, keep)
		if err != nil || oid == "" {
			t.Fatal(oid, err)
		}
		wrong := "1"
		if oid == "1" {
			wrong = "2"
		}
		opID, err := newRestoreOpID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureRestoreSchema(context.Background(), psql, admin); err != nil {
			t.Fatal(err)
		}
		opLit, err := sqlText(opID)
		if err != nil {
			t.Fatal(err)
		}
		cluster, err := clusterID(context.Background(), psql, admin)
		if err != nil || cluster == "" {
			t.Fatal(cluster, err)
		}
		insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, fresh_oid, configured_oid) VALUES (" +
			opLit + ", 'togavsu', '" + keep + "', 'togavsu_replaced_reused', '" + cluster + "', 'loading', '" + wrong + "', '1')"
		if err := psqlExec(context.Background(), psql, admin, insert); err != nil {
			t.Fatal(err)
		}
		if err := (PostgresCatalog{Bin: psql, AdminURL: admin}).Reconcile(context.Background()); err != nil && !errors.Is(err, errSwapUnobserved) {
			t.Fatal(err)
		}
		if !databaseExists(t, psql, admin, keep) || !databaseExists(t, psql, admin, neighbor) {
			t.Fatal("unverified database was removed")
		}
	})
}

func TestUnverifiedAllocationRestartsWriters(t *testing.T) {
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	cluster, cfgOID, _ := dbIdentity(t, psql, admin, "togavsu", "")
	neighbor := "togavsu_restore_allockeep"
	dropDatabaseIfExists(psql, admin, neighbor)
	if err := psqlExec(context.Background(), psql, admin, "CREATE DATABASE "+neighbor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropDatabaseIfExists(psql, admin, neighbor) })

	put := func(t *testing.T, id, fresh, opID string) (*Operations, *[]string) {
		t.Helper()
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		if err := ops.Journal.Put(journalRecord{
			Record: RestoreRecord{ID: id, BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseApplying,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{
				Configured: "togavsu", Fresh: fresh, Replaced: "togavsu_replaced_alloc",
				Allocated: true, OpID: opID, ClusterID: cluster, ConfiguredOID: cfgOID,
			},
		}); err != nil {
			t.Fatal(err)
		}
		return ops, &order
	}

	t.Run("before create", func(t *testing.T) {
		opID, err := newRestoreOpID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		fresh := "togavsu_restore_notcreated"
		ops, order := put(t, "restore-before-create", fresh, opID)
		if err := ops.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(*order, "restart:core-1") {
			t.Fatalf("writers stayed stopped: %v", *order)
		}
		got, ok, lerr := ops.LookupRestore("restore-before-create")
		if lerr != nil || !ok || got.Status != "failed" || got.Status == "running" {
			t.Fatalf("%+v %v %v", got, ok, lerr)
		}
		if databaseExists(t, psql, admin, fresh) || !databaseExists(t, psql, admin, neighbor) || !databaseExists(t, psql, admin, "togavsu") {
			t.Fatal("recovery changed an unrelated database")
		}
	})

	t.Run("before oid is recorded", func(t *testing.T) {
		fresh := "togavsu_restore_unverified"
		dropDatabaseIfExists(psql, admin, fresh)
		if err := psqlExec(context.Background(), psql, admin, "CREATE DATABASE "+fresh); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dropDatabaseIfExists(psql, admin, fresh) })
		opID, err := newRestoreOpID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureRestoreSchema(context.Background(), psql, admin); err != nil {
			t.Fatal(err)
		}
		opLit, err := sqlText(opID)
		if err != nil {
			t.Fatal(err)
		}
		insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, configured_oid) VALUES (" +
			opLit + ", 'togavsu', '" + fresh + "', 'togavsu_replaced_alloc', '" + cluster + "', 'allocating', '" + cfgOID + "')"
		if err := psqlExec(context.Background(), psql, admin, insert); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = psqlExec(context.Background(), psql, admin, "DELETE FROM togavsu_restore_op WHERE op_id = "+opLit)
		})
		ops, order := put(t, "restore-before-oid", fresh, opID)
		if err := ops.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(*order, "restart:core-1") {
			t.Fatalf("writers stayed stopped: %v", *order)
		}
		if !databaseExists(t, psql, admin, fresh) || !databaseExists(t, psql, admin, neighbor) {
			t.Fatal("unverified database was removed")
		}
		if opState(t, psql, admin, opID) != "cancelled" {
			t.Fatal("operation was not fenced")
		}
	})

	t.Run("oid recorded only in the operation row", func(t *testing.T) {
		fresh := "togavsu_restore_rowoid"
		dropDatabaseIfExists(psql, admin, fresh)
		if err := psqlExec(context.Background(), psql, admin, "CREATE DATABASE "+fresh); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dropDatabaseIfExists(psql, admin, fresh) })
		oid, err := databaseOID(context.Background(), psql, admin, fresh)
		if err != nil || oid == "" {
			t.Fatal(oid, err)
		}
		opID, err := newRestoreOpID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureRestoreSchema(context.Background(), psql, admin); err != nil {
			t.Fatal(err)
		}
		opLit, err := sqlText(opID)
		if err != nil {
			t.Fatal(err)
		}
		insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, fresh_oid, configured_oid) VALUES (" +
			opLit + ", 'togavsu', '" + fresh + "', 'togavsu_replaced_alloc', '" + cluster + "', 'loading', '" + oid + "', '" + cfgOID + "')"
		if err := psqlExec(context.Background(), psql, admin, insert); err != nil {
			t.Fatal(err)
		}
		ops, order := put(t, "restore-row-oid", fresh, opID)
		if err := ops.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(*order, "restart:core-1") {
			t.Fatalf("writers stayed stopped: %v", *order)
		}
		if databaseExists(t, psql, admin, fresh) || !databaseExists(t, psql, admin, neighbor) {
			t.Fatal("verified allocation was kept or an unrelated database was removed")
		}
	})
}

func TestRolledBackRecoveryKeepsRecreatedFreshDatabase(t *testing.T) {
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	orig, fresh, replaced := swapPair(t, psql, admin)
	cluster, cfgOID, oldOID := dbIdentity(t, psql, admin, orig, fresh)
	if oldOID == "" {
		t.Fatal("fresh oid was not recorded")
	}
	dropDatabaseIfExists(psql, admin, fresh)
	seedMarker(t, psql, admin, fresh, "recreated")
	newOID, err := databaseOID(context.Background(), psql, admin, fresh)
	if err != nil || newOID == "" || newOID == oldOID {
		t.Fatal(newOID, err)
	}
	opID, err := newRestoreOpID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRestoreSchema(context.Background(), psql, admin); err != nil {
		t.Fatal(err)
	}
	opLit, err := sqlText(opID)
	if err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, fresh_oid, configured_oid) VALUES (" +
		opLit + ", '" + orig + "', '" + fresh + "', '" + replaced + "', '" + cluster + "', 'loading', '" + oldOID + "', '" + cfgOID + "')"
	if err := psqlExec(context.Background(), psql, admin, insert); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
	if err := ops.Journal.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-recreated", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseRolledBack,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{
			Configured: orig, Fresh: fresh, Replaced: replaced,
			SwapIntended: true, SwapRolledBack: true, OpID: opID,
			ClusterID: cluster, ConfiguredOID: cfgOID, FreshOID: oldOID,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers stayed stopped: %v", order)
	}
	if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "recreated" {
		t.Fatal("rolled-back recovery removed the recreated database")
	}
	gotOID, err := databaseOID(context.Background(), psql, admin, fresh)
	if err != nil || gotOID != newOID {
		t.Fatalf("fresh oid changed from %s to %s (%v)", newOID, gotOID, err)
	}
}

func TestDifferentClusterIsRejected(t *testing.T) {
	other := os.Getenv("TOGAVSU_TEST_DATABASE_URL_B")
	if other == "" {
		t.Fatal("TOGAVSU_TEST_DATABASE_URL_B is required")
	}
	psql := psqlBin(t)
	admin := postgres17Admin(t)
	t.Run("before create", func(t *testing.T) {
		before, err := psqlLines(context.Background(), psql, admin, "SELECT count(*) FROM pg_database WHERE datname LIKE 'togavsu_restore_%'")
		if err != nil {
			t.Fatal(err)
		}
		err = RestoreIntoCleanDatabase(context.Background(), psql, admin, other, strings.NewReader("SELECT 1;\n"), nil)
		if err == nil || !errors.Is(err, ErrBadRequest) {
			t.Fatalf("different cluster was accepted: %v", err)
		}
		after, aerr := psqlLines(context.Background(), psql, admin, "SELECT count(*) FROM pg_database WHERE datname LIKE 'togavsu_restore_%'")
		if aerr != nil || len(after) != 1 || after[0] != before[0] {
			t.Fatalf("create ran on the admin cluster: before %v after %v %v", before, after, aerr)
		}
	})
	t.Run("recovery does not adopt the other cluster", func(t *testing.T) {
		adminB, err := withDatabase(other, "postgres")
		if err != nil {
			t.Fatal(err)
		}
		idA, err := clusterID(context.Background(), psql, admin)
		if err != nil {
			t.Fatal(err)
		}
		idB, err := clusterID(context.Background(), psql, adminB)
		if err != nil {
			t.Fatal(err)
		}
		if idA == idB {
			t.Fatal("the two connections are the same cluster")
		}
		keep := "togavsu_restore_otherkeep"
		dropDatabaseIfExists(psql, adminB, keep)
		if err := psqlExec(context.Background(), psql, adminB, "CREATE DATABASE "+keep); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { dropDatabaseIfExists(psql, adminB, keep) })
		opID, err := newRestoreOpID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := ensureRestoreSchema(context.Background(), psql, admin); err != nil {
			t.Fatal(err)
		}
		cfgOID, err := databaseOID(context.Background(), psql, admin, "togavsu")
		if err != nil || cfgOID == "" {
			t.Fatal(cfgOID, err)
		}
		opLit, err := sqlText(opID)
		if err != nil {
			t.Fatal(err)
		}
		insert := "INSERT INTO togavsu_restore_op (op_id, configured, fresh, replaced, cluster_id, state, fresh_oid, configured_oid) VALUES (" +
			opLit + ", 'togavsu', 'togavsu_restore_other', 'togavsu_replaced_other', '" + idA + "', 'owned', '11', '" + cfgOID + "')"
		if err := psqlExec(context.Background(), psql, admin, insert); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = psqlExec(context.Background(), psql, admin, "DELETE FROM togavsu_restore_op WHERE op_id = "+opLit)
		})
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: adminB})
		if err := ops.Journal.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-other-cluster", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseApplied,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{
				Configured: "togavsu", Fresh: "togavsu_restore_other", Replaced: "togavsu_replaced_other",
				SwapIntended: true, SwapCommitted: true, OpID: opID,
				ClusterID: idA, ConfiguredOID: cfgOID,
			},
		}); err != nil {
			t.Fatal(err)
		}
		if err := ops.Recover(context.Background()); err == nil {
			t.Fatal("recovery accepted the other cluster")
		}
		got, ok, lerr := ops.LookupRestore("restore-other-cluster")
		if lerr != nil || !ok || got.Status != "running" || got.Status == "completed" || got.Status == "failed" {
			t.Fatalf("outcome %+v %v %v", got, ok, lerr)
		}
		if containsStep(order, "restart:core-1") {
			t.Fatalf("writers restarted on the other cluster: %v", order)
		}
		if !databaseExists(t, psql, adminB, "togavsu") || !databaseExists(t, psql, adminB, keep) {
			t.Fatal("the other cluster was mutated")
		}
		if opState(t, psql, admin, opID) != "owned" {
			t.Fatal("the target cluster operation was cancelled")
		}
	})
}

func mustOwnRestoreOp(t *testing.T, psql, admin, orig, fresh, replaced string) string {
	t.Helper()
	opID, err := newRestoreOpID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRestoreOp(context.Background(), psql, admin, opID, orig, fresh, replaced); err != nil {
		t.Fatal(err)
	}
	return opID
}

func swapJournal(id, orig, fresh, replaced, opID, cluster, cfgOID, freshOID string) journalRecord {
	return journalRecord{
		Record: RestoreRecord{ID: id, BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseSwapIntended,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{
			Configured: orig, Fresh: fresh, Replaced: replaced,
			SwapIntended: true, OpID: opID,
			ClusterID: cluster, ConfiguredOID: cfgOID, FreshOID: freshOID,
		},
	}
}

func inflightOps(t *testing.T, psql, admin, id, orig, fresh, replaced, opID, cluster, cfgOID, freshOID string) (*Operations, *[]string) {
	t.Helper()
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
	if err := ops.Journal.Put(swapJournal(id, orig, fresh, replaced, opID, cluster, cfgOID, freshOID)); err != nil {
		t.Fatal(err)
	}
	return ops, &order
}

func dbIdentity(t *testing.T, psql, admin, configured, fresh string) (cluster, cfgOID, freshOID string) {
	t.Helper()
	var err error
	cluster, err = clusterID(context.Background(), psql, admin)
	if err != nil || cluster == "" {
		t.Fatal(cluster, err)
	}
	cfgOID, err = databaseOID(context.Background(), psql, admin, configured)
	if err != nil || cfgOID == "" {
		t.Fatal(cfgOID, err)
	}
	if fresh != "" {
		freshOID, err = databaseOID(context.Background(), psql, admin, fresh)
		if err != nil {
			t.Fatal(err)
		}
	}
	return cluster, cfgOID, freshOID
}

func opState(t *testing.T, psql, admin, opID string) string {
	t.Helper()
	opLit, err := sqlText(opID)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := psqlLines(context.Background(), psql, admin, "SELECT state FROM togavsu_restore_op WHERE op_id = "+opLit)
	if err != nil || len(lines) != 1 {
		t.Fatalf("state %v %v", lines, err)
	}
	return lines[0]
}

func ownedFresh(t *testing.T, psql, admin string) []string {
	t.Helper()
	lines, err := psqlLines(context.Background(), psql, admin, "SELECT fresh FROM togavsu_restore_op WHERE state = 'owned'")
	if err != nil {
		t.Fatal(err)
	}
	var live []string
	for _, name := range lines {
		if databaseExists(t, psql, admin, name) {
			live = append(live, name)
		}
	}
	return live
}

func delayBeforeRename(t *testing.T, real, ready, release string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "psql")
	script := fmt.Sprintf("#!/bin/sh\nREAL=%q\nREADY=%q\nRELEASE=%q\n", real, ready, release) + `cmd=
prev=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  prev=$arg
done
case "$cmd" in
  BEGIN*)
    : > "$READY"
    while [ ! -f "$RELEASE" ]; do sleep 0.05; done
    ;;
esac
exec "$REAL" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitPath(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("timed out waiting for " + path)
}

type heldRename struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	pid    int
	stderr bytes.Buffer
}

func holdAfterFirstRename(t *testing.T, psql, admin, opID, orig, fresh, replaced string) *heldRename {
	t.Helper()
	guard, err := restoreOpGuardSQL(opID)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(psql, "--set", "ON_ERROR_STOP=1", "--dbname", admin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	held := &heldRename{cmd: cmd, stdin: stdin, done: make(chan error, 1)}
	cmd.Stderr = &held.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { held.done <- cmd.Wait() }()
	if _, err := io.WriteString(stdin, "BEGIN;\n"+guard+"\nALTER DATABASE "+orig+" RENAME TO "+replaced+";\n"); err != nil {
		held.stop()
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		lines, qerr := psqlLines(context.Background(), psql, admin, "SELECT pid FROM pg_stat_activity WHERE state = 'idle in transaction' AND query LIKE 'ALTER DATABASE "+orig+" RENAME%' AND pid <> pg_backend_pid()")
		if qerr == nil && len(lines) > 0 {
			pid, conv := strconv.Atoi(lines[0])
			if conv == nil {
				held.pid = pid
				t.Cleanup(held.stop)
				return held
			}
		}
		select {
		case err := <-held.done:
			t.Fatalf("rename session ended: %v %s", err, held.stderr.String())
		default:
		}
		time.Sleep(40 * time.Millisecond)
	}
	held.stop()
	t.Fatalf("rename did not hold the operation row: %s", held.stderr.String())
	return nil
}

func (h *heldRename) finish(t *testing.T, sql string) {
	t.Helper()
	if _, err := io.WriteString(h.stdin, sql+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = h.stdin.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("%s: %v %s", sql, err, h.stderr.String())
		}
	case <-time.After(15 * time.Second):
		h.stop()
		t.Fatal("held rename did not finish")
	}
	h.cmd = nil
}

func (h *heldRename) stop() {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Kill()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
	}
	h.cmd = nil
}

func secondRenameSQL(opID, fresh, orig string) string {
	opLit, err := sqlText(opID)
	if err != nil {
		return "ROLLBACK;"
	}
	return "ALTER DATABASE " + fresh + " RENAME TO " + orig + "; UPDATE togavsu_restore_op SET state = 'committed' WHERE op_id = " + opLit + " AND state = 'owned'; COMMIT;"
}

func startSleeper(t *testing.T, psql, admin string) int {
	t.Helper()
	cmd := exec.Command(psql, "--dbname", admin, "--command", "SELECT pg_sleep(120)")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lines, err := psqlLines(context.Background(), psql, admin, "SELECT pid FROM pg_stat_activity WHERE query LIKE 'SELECT pg_sleep(120)%' AND pid <> pg_backend_pid()")
		if err == nil && len(lines) > 0 {
			pid, conv := strconv.Atoi(lines[0])
			if conv == nil {
				return pid
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatal("sleeper backend was not visible")
	return 0
}

func backendAlive(t *testing.T, psql, admin string, pid int) bool {
	t.Helper()
	lines, err := psqlLines(context.Background(), psql, admin, fmt.Sprintf("SELECT pid FROM pg_stat_activity WHERE pid = %d", pid))
	if err != nil {
		t.Fatal(err)
	}
	return len(lines) == 1
}

func killDuringLoad(t *testing.T, psql, admin, product, mode string) {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	bin := sleepOnApply(t, psql, ready)
	cmd := exec.Command(os.Args[0], "-test.run", "^TestOwnedTempDatabaseSurvivesOnlyWhenVerified$")
	cmd.Env = append(os.Environ(), "TOGAVSU_LOAD_CHILD="+mode, "TOGAVSU_LOAD_PSQL="+bin, "TOGAVSU_LOAD_READY="+ready, "TOGAVSU_TEST_DATABASE_URL="+product, "TOGAVSU_PSQL="+psql)
	if adminURL := os.Getenv("TOGAVSU_PG17_ADMIN_URL"); adminURL != "" {
		cmd.Env = append(cmd.Env, "TOGAVSU_PG17_ADMIN_URL="+adminURL)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitPath(t, ready)
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_, _ = cmd.Process.Wait()
	if err := (PostgresCatalog{Bin: psql, AdminURL: admin}).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows := ownedFresh(t, psql, admin)
	for _, name := range rows {
		if mode == "measure" && strings.HasPrefix(name, "togavsu_check_") {
			t.Fatalf("monthly database remained: %s", name)
		}
		if mode == "restore" && strings.HasPrefix(name, "togavsu_restore_") && name != "togavsu_restore_neighbor" {
			t.Fatalf("restore database remained: %s", name)
		}
	}
	if !databaseExists(t, psql, admin, "togavsu_restore_neighbor") {
		t.Fatal("unrelated database was removed")
	}
}

func runLoadChild() {
	psql := os.Getenv("TOGAVSU_LOAD_PSQL")
	product := os.Getenv("TOGAVSU_TEST_DATABASE_URL")
	admin := os.Getenv("TOGAVSU_PG17_ADMIN_URL")
	if admin == "" {
		var err error
		admin, err = withDatabase(product, "postgres")
		if err != nil {
			os.Exit(2)
		}
	}
	dump := strings.NewReader("SELECT 1;\n")
	ctx := context.Background()
	if os.Getenv("TOGAVSU_LOAD_CHILD") == "measure" {
		_, _ = MeasureCommand(psql, admin)(ctx, dump)
		return
	}
	_ = RestoreIntoCleanDatabase(ctx, psql, admin, product, dump, map[string]int64{})
}

func sleepOnApply(t *testing.T, real, ready string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "psql")
	script := fmt.Sprintf("#!/bin/sh\nREAL=%q\nREADY=%q\n", real, ready) + `cmd=
prev=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  prev=$arg
done
if [ -z "$cmd" ]; then
  : > "$READY"
  sleep 60
  exit 0
fi
exec "$REAL" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func loseCreateAck(t *testing.T, real string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "psql")
	script := fmt.Sprintf("#!/bin/sh\nREAL=%q\n", real) + `cmd=
prev=
dbname=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  if [ "$prev" = "--dbname" ]; then dbname=$arg; fi
  prev=$arg
done
case "$cmd" in
  "CREATE DATABASE "*)
    "$REAL" --set ON_ERROR_STOP=1 --dbname "$dbname" --command "$cmd" >/dev/null
    exit 1
    ;;
esac
exec "$REAL" "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
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

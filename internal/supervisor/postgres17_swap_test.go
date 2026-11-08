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

func TestInFlightRenameCannotCommitAfterAFailedRestore(t *testing.T) {
	admin := postgres17Admin(t)
	psql := os.Getenv("TOGAVSU_PSQL")
	if psql == "" {
		psql = "psql"
	}
	if path, err := exec.LookPath(psql); err == nil {
		psql = path
	}
	prev := swapFenceTimeout
	swapFenceTimeout = 2 * time.Second
	t.Cleanup(func() { swapFenceTimeout = prev })
	ctx := context.Background()

	t.Run("late commit is not reported as unchanged", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		sleeper := startSleeper(t, psql, admin)
		held := startHeldSwap(t, psql, admin, orig, fresh, replaced)
		rolled := false
		hooked := WithSwapRolledBack(ctx, func(string, string, string) error {
			rolled = true
			return nil
		})
		err := reconcileSwap(hooked, psql, admin, orig, fresh, replaced, errors.New("client lost"))
		if !errors.Is(err, errSwapUnobserved) || rolled {
			held.stop()
			t.Fatalf("in-flight rename was classified as rollback: %v rolled=%v", err, rolled)
		}
		if !backendAlive(t, psql, admin, sleeper) {
			held.stop()
			t.Fatal("an unrelated backend was terminated")
		}
		if !databaseExists(t, psql, admin, orig) || !databaseExists(t, psql, admin, fresh) || databaseExists(t, psql, admin, replaced) {
			held.stop()
			t.Fatal("catalog changed before the held transaction committed")
		}
		held.finish(t, "COMMIT;")
		err = reconcileSwap(hooked, psql, admin, orig, fresh, replaced, errors.New("client lost"))
		if err != nil || rolled {
			t.Fatalf("late commit was not accepted as the swap: %v rolled=%v", err, rolled)
		}
		if note(t, psql, admin, orig) != "new" || databaseExists(t, psql, admin, fresh) || databaseExists(t, psql, admin, replaced) {
			t.Fatal("committed rename was not the visible database")
		}
		if !backendAlive(t, psql, admin, sleeper) {
			t.Fatal("unrelated backend did not survive the commit reconciliation")
		}
	})

	t.Run("rollback is classified only after the transaction ends", func(t *testing.T) {
		orig, fresh, replaced := swapPair(t, psql, admin)
		sleeper := startSleeper(t, psql, admin)
		held := startHeldSwap(t, psql, admin, orig, fresh, replaced)
		var order []string
		rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
		ops := recoveryOps(t, rt, PostgresCatalog{Bin: psql, AdminURL: admin})
		rec := journalRecord{
			Record: RestoreRecord{ID: "restore-inflight", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
			Phase:  phaseSwapIntended,
			Containers: []containerIntent{{
				ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
			}},
			Database: &databaseIntent{Configured: orig, Fresh: fresh, Replaced: replaced, SwapIntended: true},
		}
		if err := ops.Journal.Put(rec); err != nil {
			held.stop()
			t.Fatal(err)
		}
		err := ops.Recover(ctx)
		if err == nil || containsStep(order, "restart:core-1") {
			held.stop()
			t.Fatalf("writers restarted while the rename was in flight: %v %v", err, order)
		}
		got, _, _ := ops.LookupRestore(rec.Record.ID)
		if got.Status == "failed" || got.Status == "completed" || got.Recovery == "catalog_old_intact" {
			held.stop()
			t.Fatalf("in-flight restore was reported finished: %+v", got)
		}
		if !backendAlive(t, psql, admin, sleeper) || !backendAlive(t, psql, admin, held.pid) {
			held.stop()
			t.Fatalf("backend was signaled sleeper=%d swap=%d", sleeper, held.pid)
		}
		held.finish(t, "ROLLBACK;")
		if backendAlive(t, psql, admin, held.pid) {
			t.Fatal("rollback left the rename backend running")
		}
		order = nil
		if err := ops.Recover(ctx); err != nil {
			t.Fatal(err)
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("writers stayed stopped after the transaction aborted: %v", order)
		}
		got, _, _ = ops.LookupRestore(rec.Record.ID)
		if got.Status != "failed" || got.Recovery != "catalog_old_intact" {
			t.Fatalf("%+v", got)
		}
		if note(t, psql, admin, orig) != "old" || note(t, psql, admin, fresh) != "new" || databaseExists(t, psql, admin, replaced) {
			t.Fatal("a later commit swapped a restore that was reported unchanged")
		}
	})
}

type heldSwap struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	pid    int
	stderr bytes.Buffer
}

func startHeldSwap(t *testing.T, psql, admin, orig, fresh, replaced string) *heldSwap {
	t.Helper()
	k1, k2 := swapLockKeys(orig, fresh, replaced)
	cmd := exec.Command(psql, "--set", "ON_ERROR_STOP=1", "--dbname", admin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	held := &heldSwap{cmd: cmd, stdin: stdin, done: make(chan error, 1)}
	cmd.Stderr = &held.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { held.done <- cmd.Wait() }()
	script := fmt.Sprintf("BEGIN;\nSELECT pg_advisory_xact_lock(%d, %d);\nALTER DATABASE %s RENAME TO %s;\nALTER DATABASE %s RENAME TO %s;\n", k1, k2, orig, replaced, fresh, orig)
	if _, err := io.WriteString(stdin, script); err != nil {
		held.stop()
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pid, ok := advisoryPID(psql, admin, k1, k2)
		if ok {
			held.pid = pid
			t.Cleanup(held.stop)
			return held
		}
		select {
		case err := <-held.done:
			t.Fatalf("rename session ended before the lock: %v %s", err, held.stderr.String())
		default:
		}
		time.Sleep(30 * time.Millisecond)
	}
	held.stop()
	t.Fatalf("rename transaction did not take its lock: %s", held.stderr.String())
	return nil
}

func (h *heldSwap) finish(t *testing.T, sql string) {
	t.Helper()
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		t.Fatal("rename session is not running")
	}
	if _, err := io.WriteString(h.stdin, sql+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = h.stdin.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("%s: %v %s", sql, err, h.stderr.String())
		}
	case <-time.After(10 * time.Second):
		h.stop()
		t.Fatalf("%s did not finish", sql)
	}
	h.cmd = nil
}

func (h *heldSwap) stop() {
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

func advisoryPID(psql, admin string, k1, k2 int32) (int, bool) {
	sql := fmt.Sprintf("SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND classid = %d AND objid = %d AND objsubid = 2 AND granted", k1, k2)
	lines, err := psqlLines(context.Background(), psql, admin, sql)
	if err != nil || len(lines) == 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return 0, false
	}
	return pid, true
}

func backendAlive(t *testing.T, psql, admin string, pid int) bool {
	t.Helper()
	lines, err := psqlLines(context.Background(), psql, admin, fmt.Sprintf("SELECT pid FROM pg_stat_activity WHERE pid = %d", pid))
	if err != nil {
		t.Fatal(err)
	}
	return len(lines) == 1
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

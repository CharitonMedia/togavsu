package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type scriptCatalog struct {
	present  map[string]bool
	err      error
	fenceErr error
	calls    int
	failDrop int
	drops    []string
}

func (c *scriptCatalog) Observe(context.Context, string, string, string) (map[string]bool, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	if c.present == nil {
		return nil, nil
	}
	out := map[string]bool{}
	for name, ok := range c.present {
		if ok {
			out[name] = true
		}
	}
	return out, nil
}

func (c *scriptCatalog) Fence(context.Context, string, string, string) (string, error) {
	if c.fenceErr != nil {
		return "", c.fenceErr
	}
	return "cancelled", nil
}

func (c *scriptCatalog) Drop(_ context.Context, name string) error {
	c.drops = append(c.drops, name)
	if c.failDrop > 0 {
		c.failDrop--
		return errors.New("drop failed")
	}
	if c.present != nil {
		c.present[name] = false
	}
	return nil
}

func TestBusyAdmissionDoesNotChangePassphrase(t *testing.T) {
	store := testStore(t)
	hold := make(chan struct{})
	release := make(chan struct{})
	store.Dump = func(context.Context, io.Writer) error {
		close(hold)
		<-release
		if store.Passphrase != "pass-a" {
			return fmt.Errorf("passphrase changed to %s", store.Passphrase)
		}
		_, err := io.WriteString(io.Discard, "")
		if err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}
	var current atomic.Value
	current.Store("pass-a")
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Passphrase: func() (string, error) {
			return current.Load().(string), nil
		},
	}
	errc := make(chan error, 1)
	go func() {
		_, err := ops.CreateBackup(context.Background(), reasonManual)
		errc <- err
	}()
	<-hold
	current.Store("pass-b")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			if _, err := ops.Restore(context.Background(), "missing"); !errors.Is(err, ErrBusy) {
				t.Errorf("restore %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := ops.CreateBackup(context.Background(), reasonManual); !errors.Is(err, ErrBusy) {
				t.Errorf("backup %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := ops.Update(context.Background(), true); !errors.Is(err, ErrBusy) {
				t.Errorf("update %v", err)
			}
		}()
	}
	wg.Wait()
	if store.Passphrase != "pass-a" {
		t.Fatalf("stored passphrase %q", store.Passphrase)
	}
	close(release)
	if err := <-errc; err == nil {
		t.Fatal("backup continued with a replaced passphrase")
	}
}

func TestStopLostReplyAndCancellationStillRestart(t *testing.T) {
	for _, mode := range []string{"lost", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			store := testStore(t)
			var order []string
			rt := &scriptRuntime{
				order: &order,
				cores: []ContainerInfo{{ID: "core-1"}, {ID: "idle", State: "exited"}},
			}
			if mode == "lost" {
				rt.loseStop = map[string]bool{"core-1": true}
			} else {
				rt.cancelStops = true
			}
			ops := &Operations{
				Store: store, Gate: &Gate{}, Now: store.Now, Runtime: rt,
				Passphrase: func() (string, error) { return store.Passphrase, nil },
				ApplyDump: func(_ context.Context, r io.Reader) error {
					_, err := io.Copy(io.Discard, r)
					return err
				},
			}
			meta, err := store.Create(context.Background(), reasonManual)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := ops.Restore(context.Background(), meta.ID)
			if err != nil || rec.Status != "completed" {
				t.Fatalf("%s %+v %v order %v", mode, rec, err, order)
			}
			if !containsStep(order, "restart:core-1") || containsStep(order, "restart:idle") || containsStep(order, "stop:idle") {
				t.Fatalf("order %v", order)
			}
		})
	}
}

func TestUnobservedStopIsRecoveredAfterRestart(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()
	var order []string
	rt := &scriptRuntime{
		order:      &order,
		cores:      []ContainerInfo{{ID: "core-1"}},
		inspectErr: map[string]error{"core-1": errors.New("engine timeout")},
	}
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now, Runtime: rt,
		Journal: OpenJournal(filepath.Join(dir, "restore-journal.jsonl")),
		Ledger:  OpenLedger(filepath.Join(dir, "ledger.jsonl")),
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(context.Context, io.Reader) error {
			t.Fatal("apply ran without a confirmed stop")
			return nil
		},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status == "completed" || rec.Status == "failed" || rec.Recovery != "container_unobserved" {
		t.Fatalf("%+v %v", rec, err)
	}
	if containsStep(order, "restart:core-1") {
		t.Fatalf("started an unobserved container: %v", order)
	}
	rt.inspectErr = nil
	rt.states = map[string]string{"core-1": "exited"}
	next := &Operations{
		Gate: &Gate{}, Runtime: rt, Journal: ops.Journal, Ledger: ops.Ledger,
	}
	if err := next.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("recovery did not start the stopped writer: %v", order)
	}
	got, ok, err := next.LookupRestore(rec.ID)
	if err != nil || !ok || got.Status != "failed" || got.Recovery != "restarted_writers" || got.BackupID != meta.ID {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestCatalogDecisionsDoNotGuess(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		present map[string]bool
		err     error
		restart bool
		status  string
		recover string
	}{
		{name: "old intact", present: map[string]bool{"origdb": true, "freshdb": true}, restart: true, status: "failed", recover: "catalog_old_intact"},
		{name: "missing", present: map[string]bool{}, restart: false, status: "running", recover: "configured_database_absent"},
		{name: "ambiguous", present: map[string]bool{"origdb": true}, restart: false, status: "running", recover: "catalog_ambiguous"},
		{name: "unread", err: errors.New("admin down"), restart: false, status: "running", recover: "catalog_unobserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			cat := &scriptCatalog{present: tc.present, err: tc.err}
			ops := &Operations{
				Store: store, Gate: &Gate{}, Now: store.Now, Catalog: cat,
				Journal: OpenJournal(filepath.Join(t.TempDir(), "j.jsonl")),
				Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
				Passphrase: func() (string, error) {
					return store.Passphrase, nil
				},
				ApplyRestore: func(ctx context.Context, _ RestorePayload) error {
					if hook := swapHook(ctx); hook != nil {
						if err := hook("origdb", "freshdb", "origdb_replaced_abcd"); err != nil {
							return err
						}
					}
					return errSwapUnobserved
				},
			}
			rec, err := ops.Restore(context.Background(), meta.ID)
			if !errors.Is(err, errSwapUnobserved) {
				t.Fatalf("%+v %v", rec, err)
			}
			if cat.calls == 0 {
				t.Fatal("catalog was not read")
			}
			if rec.Status != tc.status || rec.Recovery != tc.recover {
				t.Fatalf("%+v", rec)
			}
			if containsStep(order, "restart:core-1") != tc.restart {
				t.Fatalf("restart %v order %v", tc.restart, order)
			}
		})
	}
}

func TestSafetyBackupSyncFailureDoesNotSwap(t *testing.T) {
	for _, kind := range []string{"archive", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			store := testStore(t)
			meta, err := store.Create(context.Background(), reasonManual)
			if err != nil {
				t.Fatal(err)
			}
			store.failSync = kind
			var order []string
			applied := false
			ops := &Operations{
				Store: store, Gate: &Gate{}, Now: store.Now,
				Journal:    OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl")),
				Runtime:    &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
				Passphrase: func() (string, error) { return store.Passphrase, nil },
				ApplyDump: func(context.Context, io.Reader) error {
					applied = true
					return nil
				},
			}
			rec, err := ops.Restore(context.Background(), meta.ID)
			if err == nil || !strings.Contains(err.Error(), "sync") {
				t.Fatalf("%+v %v", rec, err)
			}
			if applied {
				t.Fatal("archive sync failure still applied the restore")
			}
			if rec.Status == "completed" || !containsStep(order, "restart:core-1") {
				t.Fatalf("writers or status %+v %v", rec, order)
			}
			listed, err := store.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 1 || listed[0].ID != meta.ID || listed[0].Reason == reasonSafety {
				t.Fatalf("safety backup was published: %+v", listed)
			}
		})
	}
}

func TestCleanupDropRetriesWithoutAnotherRestore(t *testing.T) {
	cat := &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}, failDrop: 1}
	var order []string
	applied := 0
	dir := t.TempDir()
	journal := OpenJournal(filepath.Join(dir, "restore-journal.jsonl"))
	rec := journalRecord{
		Record: RestoreRecord{ID: "restore-maint", BackupID: "backup", Status: "completed", Error: "cleanup_pending", Cleanup: "pending", Started: time.Now().UTC()},
		Phase:  phaseCompleted,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "running",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true, SwapCommitted: true},
		Cleanup:  "pending",
	}
	if err := journal.Put(rec); err != nil {
		t.Fatal(err)
	}
	ops := &Operations{
		Gate: &Gate{}, Journal: journal, Catalog: cat,
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1", State: "running"}}, states: map[string]string{"core-1": "running"}},
		ApplyDump: func(context.Context, io.Reader) error {
			applied++
			return nil
		},
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if applied != 0 || len(cat.drops) != 1 || !cat.present["origdb_replaced_abcd"] {
		t.Fatalf("first recovery applied %d drops %v present %v", applied, cat.drops, cat.present)
	}
	got, ok, err := ops.LookupRestore("restore-maint")
	if err != nil || !ok || got.Status != "completed" || got.Cleanup != "pending" {
		t.Fatalf("first %+v %v %v", got, ok, err)
	}
	if err := ops.blockUnresolved(); err != nil {
		t.Fatalf("maintenance blocked the app: %v", err)
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if applied != 0 || len(cat.drops) != 2 || cat.present["origdb_replaced_abcd"] || !cat.present["origdb"] {
		t.Fatalf("second recovery applied %d drops %v present %v", applied, cat.drops, cat.present)
	}
	got, ok, err = ops.LookupRestore("restore-maint")
	if err != nil || !ok || got.Status != "completed" || got.Cleanup != "" || got.Error != "" {
		t.Fatalf("cleaned %+v %v %v", got, ok, err)
	}
	cat.err = errors.New("catalog down")
	cat.present["origdb_replaced_abcd"] = true
	if err := journal.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-unread", BackupID: "backup", Status: "completed", Error: "cleanup_pending", Cleanup: "pending", Started: time.Now().UTC()},
		Phase:  phaseCompleted, Cleanup: "pending",
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true, SwapCommitted: true},
	}); err != nil {
		t.Fatal(err)
	}
	before := len(cat.drops)
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cat.drops) != before {
		t.Fatalf("unreadable catalog dropped %v", cat.drops)
	}
	cat.err = nil
	cat.present["freshdb"] = true
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cat.drops) != before || !cat.present["origdb_replaced_abcd"] {
		t.Fatalf("ambiguous catalog dropped %v present %v", cat.drops, cat.present)
	}
}

func TestCommittedCatalogMissIsNotDowngraded(t *testing.T) {
	cat := &scriptCatalog{err: errors.New("catalog down")}
	var order []string
	journal := OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	if err := journal.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-commit", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseApplied,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true, SwapCommitted: true},
	}); err != nil {
		t.Fatal(err)
	}
	ops := &Operations{
		Gate: &Gate{}, Journal: journal, Catalog: cat,
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}},
	}
	if err := ops.Recover(context.Background()); err == nil {
		t.Fatal("unreadable catalog finished the restore")
	}
	got, ok, err := journal.Latest("restore-commit")
	if err != nil || !ok || got.Phase != phaseApplied || got.Database == nil || !got.Database.SwapCommitted || got.Record.Status == "failed" {
		t.Fatalf("committed outcome was downgraded: %+v %v %v", got, ok, err)
	}
	if containsStep(order, "restart:core-1") {
		t.Fatalf("writers restarted while the catalog was down: %v", order)
	}
	cat.err = nil
	cat.present = map[string]bool{"origdb": true}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err = journal.Latest("restore-commit")
	if err != nil || !ok || got.Record.Status != "completed" || got.Phase != phaseCompleted || !got.Database.SwapCommitted {
		t.Fatalf("recovered %+v %v %v", got, ok, err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers stayed stopped after the catalog showed only the configured database: %v", order)
	}
}

func TestCrashAfterReplacedDropStillCompletes(t *testing.T) {
	cat := &scriptCatalog{present: map[string]bool{"origdb": true}}
	var order []string
	applied := 0
	journal := OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	if err := journal.Put(journalRecord{
		Record:  RestoreRecord{ID: "restore-dropped", BackupID: "backup", Status: "running", Error: "cleanup_pending", Cleanup: "pending", Started: time.Now().UTC()},
		Phase:   phaseApplied,
		Cleanup: "pending",
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true, SwapCommitted: true},
	}); err != nil {
		t.Fatal(err)
	}
	ops := &Operations{
		Gate: &Gate{}, Journal: journal, Catalog: cat,
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}},
		ApplyDump: func(context.Context, io.Reader) error {
			applied++
			return nil
		},
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if applied != 0 || len(cat.drops) != 0 || !cat.present["origdb"] {
		t.Fatalf("recovery reapplied or dropped the live database: applied %d drops %v present %v", applied, cat.drops, cat.present)
	}
	got, ok, err := journal.Latest("restore-dropped")
	if err != nil || !ok || got.Record.Status != "completed" || got.Record.Cleanup != "" || got.Phase != phaseCompleted {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers stayed stopped: %v", order)
	}
}

func TestCommitRecordPrecedesReplacedDrop(t *testing.T) {
	cat := &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}}
	journal := OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	rec := journalRecord{
		Record: RestoreRecord{ID: "restore-undurable", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseSwapIntended,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true},
	}
	if err := journal.Put(rec); err != nil {
		t.Fatal(err)
	}
	journal.failPhase = phaseApplied
	journal.failRemaining = 1
	ops := &Operations{
		Gate: &Gate{}, Journal: journal, Catalog: cat,
		Runtime: &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "core-1"}}, states: map[string]string{"core-1": "exited"}},
	}
	if _, err := ops.finishCommittedUnjournaled(rec.Record, nil, errCommitUnjournaled); err == nil {
		t.Fatal("missing commit record was ignored")
	}
	if len(cat.drops) != 0 || !cat.present["origdb_replaced_abcd"] {
		t.Fatalf("replaced database was deleted before the commit record: drops %v present %v", cat.drops, cat.present)
	}
	got, ok, err := journal.Latest("restore-undurable")
	if err != nil || !ok || got.Database == nil || got.Database.SwapCommitted || got.Record.Status == "completed" {
		t.Fatalf("journal claims a commit that was not stored: %+v %v %v", got, ok, err)
	}
}

func TestMaintenanceIgnoresReplacedCore(t *testing.T) {
	cat := &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}, failDrop: 1}
	journal := OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl"))
	if err := journal.Put(journalRecord{
		Record:  RestoreRecord{ID: "restore-maint", BackupID: "backup", Status: "completed", Error: "cleanup_pending", Cleanup: "pending", Started: time.Now().UTC(), Finished: time.Now().UTC()},
		Phase:   phaseCompleted,
		Cleanup: "pending",
		Containers: []containerIntent{{
			ID: "core-old", Prior: "running", StopIntended: true, Observed: "running",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true, SwapCommitted: true},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, cores: nil, inspectErr: map[string]error{"core-old": ErrNotFound}}
	ops := &Operations{Gate: &Gate{}, Journal: journal, Catalog: cat, Runtime: rt}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err := journal.Latest("restore-maint")
	if err != nil || !ok || got.Record.Status != "completed" || got.Record.Cleanup != "pending" {
		t.Fatalf("missing core reopened the restore: %+v %v %v", got, ok, err)
	}
	if err := ops.blockUnresolved(); err != nil {
		t.Fatalf("maintenance blocked admission: %v", err)
	}
	if len(cat.drops) != 1 {
		t.Fatalf("cleanup was skipped because the old core was gone: %v", cat.drops)
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok, err = journal.Latest("restore-maint")
	if err != nil || !ok || got.Record.Status != "completed" || got.Record.Cleanup != "" || cat.present["origdb_replaced_abcd"] {
		t.Fatalf("second maintenance %+v present %v %v %v", got, cat.present, ok, err)
	}
}

func TestCleanupPendingPollIsStableAfterRestart(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()
	var order []string
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		Journal: OpenJournal(filepath.Join(dir, "j.jsonl")),
		Ledger:  OpenLedger(filepath.Join(dir, "l.jsonl")),
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyRestore: func(context.Context, RestorePayload) error {
			return fmt.Errorf("%w: drop database: busy", ErrRestoreCleanup)
		},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, ErrRestoreCleanup) || rec.Status != "completed" || rec.Error != "cleanup_pending" || rec.Cleanup != "pending" {
		t.Fatalf("%+v %v", rec, err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("order %v", order)
	}
	raw, err := os.ReadFile(ops.Journal.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-passphrase") || strings.Contains(string(raw), "postgres://") {
		t.Fatalf("journal leaked secrets: %s", raw)
	}
	next := &Operations{Journal: ops.Journal, Ledger: ops.Ledger, Gate: &Gate{}}
	srv := httptest.NewServer(NewMux("local-token", next, nil))
	defer srv.Close()
	res, body := authJSON(t, srv.URL, http.MethodGet, "/v1/restores/"+rec.ID, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	var polled RestoreRecord
	if err := json.Unmarshal(body, &polled); err != nil {
		t.Fatal(err)
	}
	if polled.ID != rec.ID || polled.BackupID != meta.ID || polled.Status != "completed" || polled.Error != "cleanup_pending" || polled.Cleanup != "pending" {
		t.Fatalf("%+v", polled)
	}
	if strings.Contains(string(body), `"kind"`) || strings.Contains(string(body), `"fields"`) {
		t.Fatalf("raw event %s", body)
	}
}

func TestGracefulDrainRestartsWriters(t *testing.T) {
	store := testStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var order []string
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		Journal:        OpenJournal(filepath.Join(t.TempDir(), "j.jsonl")),
		RestoreTimeout: time.Minute,
		Passphrase:     func() (string, error) { return store.Passphrase, nil },
		ApplyDump: func(context.Context, io.Reader) error {
			close(started)
			<-release
			return nil
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: NewMux("local-token", ops, nil)}
	go srv.Serve(ln)
	req, err := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/v1/restores", strings.NewReader(`{"backup_id":"`+meta.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer local-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("apply did not start")
	}
	drained := make(chan error, 1)
	go func() { drained <- ops.Drain(srv) }()
	select {
	case err := <-drained:
		t.Fatalf("drain returned before the restore finished: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if containsStep(order, "restart:core-1") {
		t.Fatalf("restarted before the restore could finish: %v", order)
	}
	close(release)
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not wait for the restore")
	}
	if !containsStep(order, "stop:core-1") || !containsStep(order, "restart:core-1") {
		t.Fatalf("order %v", order)
	}
	if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("admission during shutdown: %v", err)
	}
}

func TestJournalFsyncSurvivesKill(t *testing.T) {
	if os.Getenv("TOGAVSU_JOURNAL_CHILD") == "1" {
		writeKillChild()
		return
	}
	for _, boundary := range []string{"writers_stopped", "swap_ambiguous", "swap_cleanup"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "restore-journal.jsonl")
			ready := filepath.Join(dir, "ready")
			cmd := exec.Command(os.Args[0], "-test.run=TestJournalFsyncSurvivesKill", "-test.count=1")
			cmd.Env = append(os.Environ(),
				"TOGAVSU_JOURNAL_CHILD=1",
				"TOGAVSU_JOURNAL_PATH="+path,
				"TOGAVSU_JOURNAL_READY="+ready,
				"TOGAVSU_BOUNDARY="+boundary,
			)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("child did not fsync")
				}
				time.Sleep(15 * time.Millisecond)
			}
			if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			journal := OpenJournal(path)
			got, ok, err := journal.Latest("restore-killed")
			if err != nil || !ok || got.Phase == "" {
				t.Fatalf("journal after kill %+v %v %v", got, ok, err)
			}
			var order []string
			rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
			cat := &scriptCatalog{err: errors.New("unread")}
			if boundary == "swap_cleanup" {
				cat = &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}}
			}
			ops := &Operations{Gate: &Gate{}, Runtime: rt, Catalog: cat, Journal: journal}
			err = ops.Recover(context.Background())
			switch boundary {
			case "writers_stopped":
				if err != nil || !containsStep(order, "restart:core-1") || containsStep(order, "restart:idle") {
					t.Fatalf("err %v order %v", err, order)
				}
			case "swap_ambiguous":
				if err == nil || containsStep(order, "restart:core-1") || cat.calls == 0 {
					t.Fatalf("err %v calls %d order %v", err, cat.calls, order)
				}
			case "swap_cleanup":
				if err != nil || !containsStep(order, "restart:core-1") {
					t.Fatalf("err %v order %v", err, order)
				}
				rec, ok, lerr := ops.LookupRestore("restore-killed")
				if lerr != nil || !ok || rec.Status != "completed" || rec.Cleanup != "" || len(cat.drops) != 1 || cat.drops[0] != "origdb_replaced_abcd" {
					t.Fatalf("%+v drops %v %v %v", rec, cat.drops, ok, lerr)
				}
			}
		})
	}
}

func writeKillChild() {
	path := os.Getenv("TOGAVSU_JOURNAL_PATH")
	j := OpenJournal(path)
	rec := journalRecord{
		Record: RestoreRecord{ID: "restore-killed", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Containers: []containerIntent{
			{ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited"},
			{ID: "idle", Prior: "exited"},
		},
	}
	switch os.Getenv("TOGAVSU_BOUNDARY") {
	case "swap_ambiguous", "swap_cleanup":
		rec.Phase = phaseSwapIntended
		rec.Database = &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true}
	default:
		rec.Phase = phaseWritersStopped
	}
	if err := j.Put(rec); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("TOGAVSU_JOURNAL_READY"), []byte("ok"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestSwapHookIsDurableBeforeRename(t *testing.T) {
	log := filepath.Join(t.TempDir(), "psql.log")
	bin := filepath.Join(t.TempDir(), "psql")
	script := "#!/bin/sh\necho ran >> " + log + "\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	called := false
	ctx := WithSwapHook(context.Background(), func(configured, fresh, replaced string) error {
		called = true
		if configured != "origdb" || fresh != "freshdb" || replaced != "origdb_replaced_abcd" {
			t.Fatalf("%s %s %s", configured, fresh, replaced)
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("psql ran before the hook returned")
		}
		return errors.New("stop before rename")
	})
	err := swapDatabase(ctx, bin, "postgres://example", "origdb", "freshdb", "abcd")
	if !called || err == nil || !strings.Contains(err.Error(), "stop before rename") {
		t.Fatalf("called %v err %v", called, err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("rename started")
	}
}

func TestUpdateJournalTornTailDoesNotSwallowTheNextRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-journal.jsonl")
	j := OpenUpdateJournal(path)
	if err := j.Put(updateIntent{ID: "upd-1", Status: "running", Phase: "migration_intended"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"torn"`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, ok, err := OpenUpdateJournal(path).Latest("torn"); err != nil || ok {
		t.Fatalf("unterminated tail was committed: ok %v err %v", ok, err)
	}
	if err := j.Put(updateIntent{ID: "upd-2", Status: "failed", Phase: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := j.Put(updateIntent{ID: "upd-3", Status: "completed", Phase: "completed"}); err != nil {
		t.Fatal(err)
	}
	reopened := OpenUpdateJournal(path)
	for _, id := range []string{"upd-1", "upd-2", "upd-3"} {
		if _, ok, err := reopened.Latest(id); err != nil || !ok {
			t.Fatalf("%s missing after torn tail: ok %v err %v", id, ok, err)
		}
	}
	if _, ok, err := reopened.Latest("torn"); err != nil || ok {
		t.Fatalf("torn id reappeared: ok %v err %v", ok, err)
	}
	f2, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.WriteString(`{"id":"unterminated","status":"running","phase":"candidate_intended"}`); err != nil {
		t.Fatal(err)
	}
	f2.Close()
	if _, ok, err := OpenUpdateJournal(path).Latest("unterminated"); err != nil || ok {
		t.Fatalf("valid JSON without a newline was committed: ok %v err %v", ok, err)
	}
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(raw, []byte(`{"id":""}`+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenUpdateJournal(path).Put(updateIntent{ID: "upd-4", Status: "failed", Phase: "failed"}); !errors.Is(err, errJournalCorrupt) {
		t.Fatalf("corrupt committed record: %v", err)
	}
}

func TestJournalTornTailDoesNotSwallowTheNextRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore-journal.jsonl")
	j := OpenJournal(path)
	first := journalRecord{
		Record: RestoreRecord{ID: "restore-committed", BackupID: "backup-a", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseWritersStopped,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
	}
	if err := j.Put(first); err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(committed, []byte("\n")) {
		t.Fatal("committed record was not newline terminated")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"record":{"id":"torn"`); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := OpenJournal(path)
	if _, ok, err := reopened.Latest("torn"); err != nil || ok {
		t.Fatalf("unterminated tail was treated as committed: ok %v err %v", ok, err)
	}
	got, ok, err := reopened.Latest("restore-committed")
	if err != nil || !ok || got.Record.BackupID != "backup-a" {
		t.Fatalf("committed record %+v %v %v", got, ok, err)
	}
	recovery := journalRecord{
		Record: RestoreRecord{ID: "restore-recovery", BackupID: "backup-a", Status: "failed", Recovery: "restarted_writers", Started: time.Now().UTC()},
		Phase:  phaseFailed,
	}
	later := journalRecord{
		Record: RestoreRecord{ID: "restore-later", BackupID: "backup-b", Status: "accepted", Started: time.Now().UTC()},
		Phase:  phaseAccepted,
	}
	if err := reopened.Put(recovery); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Put(later); err != nil {
		t.Fatal(err)
	}
	again := OpenJournal(path)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, committed) {
		t.Fatal("repair rewrote committed journal bytes")
	}
	if bytes.Contains(raw, []byte(`"id":"torn"`)) {
		t.Fatal("torn tail was kept or concatenated")
	}
	for _, id := range []string{"restore-committed", "restore-recovery", "restore-later"} {
		rec, ok, err := again.Latest(id)
		if err != nil || !ok || rec.Record.ID != id {
			t.Fatalf("record %s after reopen: %+v %v %v", id, rec, ok, err)
		}
	}
}

func TestValidUnterminatedTailIsNotCommitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore-journal.jsonl")
	body, err := json.Marshal(journalRecord{
		Record: RestoreRecord{ID: "restore-open", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseRunning,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	j := OpenJournal(path)
	if _, ok, err := j.Latest("restore-open"); err != nil || ok {
		t.Fatalf("unterminated JSON was committed: ok %v err %v", ok, err)
	}
	next := journalRecord{
		Record: RestoreRecord{ID: "restore-next", BackupID: "backup", Status: "accepted", Started: time.Now().UTC()},
		Phase:  phaseAccepted,
	}
	if err := j.Put(next); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("restore-open")) {
		t.Fatal("unterminated record survived the next append")
	}
	got, ok, err := OpenJournal(path).Latest("restore-next")
	if err != nil || !ok || got.Record.ID != "restore-next" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestTerminatedCorruptJournalFailsRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore-journal.jsonl")
	j := OpenJournal(path)
	if err := j.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-live", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseSwapIntended,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true},
	}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not-json}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := OpenJournal(path).Latest("restore-live"); !errors.Is(err, errJournalCorrupt) {
		t.Fatalf("read err %v", err)
	}
	var order []string
	ops := &Operations{
		Gate:    &Gate{},
		Journal: OpenJournal(path),
		Runtime: &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}},
		Catalog: &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}},
	}
	if err := ops.Recover(context.Background()); !errors.Is(err, errJournalCorrupt) {
		t.Fatalf("recover err %v", err)
	}
	if containsStep(order, "restart:core-1") {
		t.Fatalf("recovery ran against a corrupt journal: %v", order)
	}
	if err := OpenJournal(path).Put(journalRecord{
		Record: RestoreRecord{ID: "restore-hidden", BackupID: "backup", Status: "accepted", Started: time.Now().UTC()},
		Phase:  phaseAccepted,
	}); !errors.Is(err, errJournalCorrupt) {
		t.Fatalf("append after corruption err %v", err)
	}
}

func TestFailedJournalPersistDoesNotStopOrSwap(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("stop", func(t *testing.T) {
		var order []string
		dir := t.TempDir()
		ops := &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal: OpenJournal(filepath.Join(dir, "restore-journal.jsonl")),
			Passphrase: func() (string, error) {
				return store.Passphrase, nil
			},
			ApplyDump: func(context.Context, io.Reader) error {
				t.Fatal("apply ran after the journal refused the stop")
				return nil
			},
			Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		}
		ops.Runtime.(*scriptRuntime).onList = func() {
			ops.Journal = OpenJournal(t.TempDir())
		}
		if _, err := ops.Restore(context.Background(), meta.ID); err == nil {
			t.Fatal("restore succeeded without a durable stop intent")
		}
		if containsStep(order, "stop:core-1") || containsStep(order, "restart:core-1") {
			t.Fatalf("destructive runtime work after a failed persist: %v", order)
		}
	})
	t.Run("swap", func(t *testing.T) {
		var order []string
		log := filepath.Join(t.TempDir(), "psql.log")
		bin := filepath.Join(t.TempDir(), "psql")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho ran >> "+log+"\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		ops := &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal: OpenJournal(filepath.Join(t.TempDir(), "restore-journal.jsonl")),
			Passphrase: func() (string, error) {
				return store.Passphrase, nil
			},
			Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		}
		ops.ApplyRestore = func(ctx context.Context, _ RestorePayload) error {
			ops.Journal = OpenJournal(t.TempDir())
			return swapDatabase(ctx, bin, "postgres://example", "origdb", "freshdb", "abcd")
		}
		if _, err := ops.Restore(context.Background(), meta.ID); err == nil {
			t.Fatal("swap continued after the journal refused the intent")
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("rename started after the journal persist failed")
		}
	})
}

func TestStoppedWritersStayRecoverable(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("journal failure after stop", func(t *testing.T) {
		var order []string
		dir := t.TempDir()
		path := filepath.Join(dir, "restore-journal.jsonl")
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}, {ID: "idle", State: "exited"}}}
		ops := &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal:    OpenJournal(path),
			Passphrase: func() (string, error) { return store.Passphrase, nil },
			ApplyDump: func(_ context.Context, r io.Reader) error {
				_, err := io.Copy(io.Discard, r)
				return err
			},
			Runtime: rt,
		}
		rt.stopHook = func() { ops.Journal.failRemaining = 4 }
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err == nil {
			t.Fatal("persist failure was ignored")
		}
		if !containsStep(order, "restart:core-1") {
			t.Fatalf("writers were not recovered: %v", order)
		}
		if containsStep(order, "restart:idle") || containsStep(order, "stop:idle") {
			t.Fatalf("idle container was changed: %v", order)
		}
		got, ok, rerr := OpenJournal(path).Latest(rec.ID)
		if rerr != nil || !ok {
			t.Fatalf("journal lost the job: %+v %v %v", got, ok, rerr)
		}
		if (got.Record.Status == "failed" || got.Record.Status == "completed") && rt.states["core-1"] != "running" {
			t.Fatalf("terminal while core-1 is down: %+v state %s", got.Record, rt.states["core-1"])
		}
		if got.Record.Status != "failed" && got.Record.Status != "completed" {
			pending, perr := OpenJournal(path).Incomplete()
			if perr != nil || len(pending) == 0 || pending[0].Record.ID != rec.ID {
				t.Fatalf("pending job vanished: %+v %v", pending, perr)
			}
		}
	})
	t.Run("startup retries the same container", func(t *testing.T) {
		var order []string
		dir := t.TempDir()
		path := filepath.Join(dir, "restore-journal.jsonl")
		rt := &scriptRuntime{
			order:     &order,
			cores:     []ContainerInfo{{ID: "core-1"}, {ID: "idle", State: "exited"}},
			failStart: "core-1",
		}
		ops := &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal:    OpenJournal(path),
			Passphrase: func() (string, error) { return store.Passphrase, nil },
			ApplyDump: func(_ context.Context, r io.Reader) error {
				_, err := io.Copy(io.Discard, r)
				return err
			},
			Runtime: rt,
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err == nil || rec.Status == "completed" || rec.Status == "failed" {
			t.Fatalf("stranded as terminal %+v %v", rec, err)
		}
		if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("new destructive job while unresolved: %v", err)
		}
		rt.failStart = ""
		rt.states["core-1"] = "exited"
		next := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
		if err := next.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(order, "restart:core-1") || containsStep(order, "restart:idle") {
			t.Fatalf("order %v", order)
		}
		got, ok, err := next.LookupRestore(rec.ID)
		if err != nil || !ok || got.ID != rec.ID || got.Status != "completed" {
			t.Fatalf("%+v %v %v", got, ok, err)
		}
	})
	t.Run("recovery fails once then succeeds", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "restore-journal.jsonl")
		j := OpenJournal(path)
		if err := j.Put(journalRecord{
			Record: RestoreRecord{ID: "restore-retry", BackupID: meta.ID, Status: "running", Started: time.Now().UTC()},
			Phase:  phaseWritersStopped,
			Containers: []containerIntent{
				{ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited"},
				{ID: "idle", Prior: "exited"},
			},
		}); err != nil {
			t.Fatal(err)
		}
		var order []string
		rt := &scriptRuntime{order: &order, failStart: "core-1", states: map[string]string{"core-1": "exited", "idle": "exited"}}
		first := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
		if err := first.Recover(context.Background()); err == nil {
			t.Fatal("first recovery succeeded while start was failing")
		}
		got, ok, err := OpenJournal(path).Latest("restore-retry")
		if err != nil || !ok || got.Record.Status == "failed" || got.Record.Status == "completed" {
			t.Fatalf("first recovery closed the job: %+v %v %v", got, ok, err)
		}
		if _, err := first.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("admission while unresolved: %v", err)
		}
		rt.failStart = ""
		rt.states["core-1"] = "exited"
		second := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
		if err := second.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(order, "restart:core-1") || containsStep(order, "restart:idle") {
			t.Fatalf("order %v", order)
		}
		done, ok, err := second.LookupRestore("restore-retry")
		if err != nil || !ok || done.Status == "running" {
			t.Fatalf("second startup did not finish the same job: %+v %v %v", done, ok, err)
		}
	})
}

func TestUnreadableOrUnhealthyWritersStayPending(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	newOps := func(path string, rt *scriptRuntime) *Operations {
		return &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal:    OpenJournal(path),
			Passphrase: func() (string, error) { return store.Passphrase, nil },
			ApplyDump: func(_ context.Context, r io.Reader) error {
				_, err := io.Copy(io.Discard, r)
				return err
			},
			Runtime: rt,
		}
	}
	t.Run("inspect fails after stop", func(t *testing.T) {
		var order []string
		path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
		rt := &scriptRuntime{
			order: &order, cores: []ContainerInfo{{ID: "core-1"}, {ID: "idle", State: "exited"}},
			inspectErr:   map[string]error{"core-1": errors.New("engine down")},
			inspectAllow: 1,
		}
		ops := newOps(path, rt)
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err == nil || rec.Status == "completed" || rec.Status == "failed" || rec.Recovery != "container_unobserved" {
			t.Fatalf("%+v %v", rec, err)
		}
		if containsStep(order, "restart:core-1") || containsStep(order, "health") {
			t.Fatalf("treated an unreadable container as stopped or healthy: %v", order)
		}
		if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("admission while unobserved: %v", err)
		}
		persisted, ok, err := OpenJournal(path).Latest(rec.ID)
		if err != nil || !ok || persisted.Record.Status != "running" {
			t.Fatalf("journal did not keep the job pending: %+v %v %v", persisted, ok, err)
		}
		rt.inspectErr = nil
		rt.states["core-1"] = "exited"
		next := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
		if err := next.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !containsStep(order, "restart:core-1") || !containsStep(order, "health") || containsStep(order, "restart:idle") {
			t.Fatalf("order %v", order)
		}
		got, ok, err := next.LookupRestore(rec.ID)
		if err != nil || !ok || got.ID != rec.ID || got.Status != "completed" {
			t.Fatalf("%+v %v %v", got, ok, err)
		}
	})
	t.Run("already running but unhealthy", func(t *testing.T) {
		var order []string
		path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}}
		ops := newOps(path, rt)
		rt.stopHook = func() {
			rt.states["core-1"] = "running"
			rt.health = errors.New("unhealthy")
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err == nil || rec.Status == "completed" || rec.Status == "failed" || rec.Recovery != "restart_failed" {
			t.Fatalf("%+v %v", rec, err)
		}
		if containsStep(order, "restart:core-1") || !containsStep(order, "health") {
			t.Fatalf("running container was restarted or not health-checked: %v", order)
		}
		if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("admission while unhealthy: %v", err)
		}
		rt.health = nil
		next := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
		if err := next.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, ok, err := next.LookupRestore(rec.ID)
		if err != nil || !ok || got.Status != "failed" || got.ID != rec.ID {
			t.Fatalf("%+v %v %v", got, ok, err)
		}
	})
}

func TestUnknownStateIsNotTreatedAsHealthy(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, unknownAfter: 1}
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		Journal:    OpenJournal(path),
		Passphrase: func() (string, error) { return store.Passphrase, nil },
		ApplyDump: func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: rt,
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status == "completed" || rec.Status == "failed" || rec.Recovery != "container_unobserved" {
		t.Fatalf("%+v %v", rec, err)
	}
	if containsStep(order, "restart:core-1") || containsStep(order, "health") {
		t.Fatalf("unknown state was started or accepted as healthy: %v", order)
	}
	if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("admission while the container state is unknown: %v", err)
	}
	rt.unknownAfter = 0
	rt.states["core-1"] = "exited"
	next := &Operations{Gate: &Gate{}, Journal: OpenJournal(path), Runtime: rt}
	if err := next.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
		t.Fatalf("order %v", order)
	}
	got, ok, err := next.LookupRestore(rec.ID)
	if err != nil || !ok || got.Status != "completed" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestMissingJournalRecordDoesNotFinish(t *testing.T) {
	var order []string
	ops := &Operations{
		Gate:    &Gate{},
		Journal: OpenJournal(t.TempDir()),
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	got, err := ops.closeStopped(RestoreRecord{ID: "restore-missing", Status: "running", Started: time.Now().UTC()}, nil, errors.New("stop failed"), "")
	if err == nil || got.Status == "failed" || got.Status == "completed" {
		t.Fatalf("%+v %v", got, err)
	}
	if containsStep(order, "restart:core-1") || containsStep(order, "health") {
		t.Fatalf("missing journal record was treated as reconciled: %v", order)
	}
}

func TestUpdateBlockedWhileRestoreIsPending(t *testing.T) {
	dir := t.TempDir()
	journal := OpenJournal(filepath.Join(dir, "restore-journal.jsonl"))
	if err := journal.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-pending", BackupID: "backup", Status: "running", Started: time.Now().UTC(), Recovery: "restart_failed"},
		Phase:  phaseApplied,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "running",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	ops := &Operations{
		Gate: &Gate{}, Journal: journal,
		Updater: &Updater{},
		Runtime: &scriptRuntime{order: &order, health: errors.New("unhealthy")},
	}
	if _, err := ops.Update(context.Background(), true); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("update while recovery is pending: %v", err)
	}
	if containsStep(order, "start:") || containsStep(order, "switch:") {
		t.Fatalf("update started a release while recovery is pending: %v", order)
	}
	if _, err := ops.Restore(context.Background(), "backup"); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("restore while recovery is pending: %v", err)
	}
}

func TestAppliedOutcomeRestartsWhenOnlyTheConfiguredNameRemains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore-journal.jsonl")
	j := OpenJournal(path)
	if err := j.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-applied", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseApplied,
		Containers: []containerIntent{
			{ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited"},
			{ID: "idle", Prior: "exited"},
		},
		Database: &databaseIntent{
			Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd",
			SwapIntended: true, SwapCommitted: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	cat := &scriptCatalog{present: map[string]bool{"origdb": true}}
	ops := &Operations{
		Gate: &Gate{}, Journal: OpenJournal(path), Catalog: cat,
		Runtime: &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited", "idle": "exited"}},
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") || containsStep(order, "restart:idle") {
		t.Fatalf("order %v", order)
	}
	got, ok, err := ops.LookupRestore("restore-applied")
	if err != nil || !ok || got.Status != "completed" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestRecoverHoldsAdmission(t *testing.T) {
	dir := t.TempDir()
	journal := OpenJournal(filepath.Join(dir, "j.jsonl"))
	if err := journal.Put(journalRecord{
		Record:   RestoreRecord{ID: "restore-held", BackupID: "b", Status: "running", Started: time.Now().UTC()},
		Phase:    phaseSwapIntended,
		Database: &databaseIntent{Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd", SwapIntended: true},
	}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	ops := &Operations{
		Gate:    &Gate{},
		Journal: journal,
		Catalog: catalogHold{started: started, release: release},
	}
	errc := make(chan error, 1)
	go func() { errc <- ops.Recover(context.Background()) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("recover did not read the catalog")
	}
	if _, err := ops.Restore(context.Background(), "missing"); !errors.Is(err, ErrBusy) {
		t.Fatalf("admission during recovery: %v", err)
	}
	close(release)
	if err := <-errc; err == nil {
		t.Fatal("unreadable catalog was treated as recovered")
	}
}

type catalogHold struct {
	started chan struct{}
	release chan struct{}
}

func (c catalogHold) Observe(context.Context, string, string, string) (map[string]bool, error) {
	close(c.started)
	<-c.release
	return nil, errors.New("unread")
}

func TestCommitUnjournaledDoesNotClaimUnchangedData(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	newOps := func(path string, cat *scriptCatalog, rt *scriptRuntime, box *Outbox) *Operations {
		return &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal:    OpenJournal(path),
			Catalog:    cat,
			Outbox:     box,
			Passphrase: func() (string, error) { return store.Passphrase, nil },
			Runtime:    rt,
		}
	}
	psqlOK := func(t *testing.T, log string) string {
		t.Helper()
		bin := filepath.Join(t.TempDir(), "psql")
		script := "#!/bin/sh\ncmd=\nprev=\nfor arg in \"$@\"; do\n  if [ \"$prev\" = \"--command\" ]; then cmd=$arg; fi\n  prev=$arg\ndone\nprintf '%s\\n' \"$cmd\" >> " + log + "\ncase \"$cmd\" in\n  *system_identifier*) echo 100 ;;\n  *oid::text*) echo 10 ;;\nesac\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	t.Run("previous database stays and the poll says the restore is live", func(t *testing.T) {
		var order []string
		dir := t.TempDir()
		log := filepath.Join(dir, "psql.log")
		box := OpenOutbox(filepath.Join(dir, "outbox"))
		cat := &scriptCatalog{present: map[string]bool{"origdb": true, "origdb_replaced_abcd": true}}
		ops := newOps(filepath.Join(dir, "restore-journal.jsonl"), cat, &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}}, box)
		ops.Journal.failPhase = phaseApplied
		ops.Journal.failRemaining = 1
		bin := psqlOK(t, log)
		ops.ApplyRestore = func(ctx context.Context, _ RestorePayload) error {
			return swapDatabase(ctx, bin, "postgres://example/postgres", "origdb", "freshdb", "abcd")
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err != nil && !errors.Is(err, errCommitUnjournaled) && !errors.Is(err, ErrRestoreCleanup) {
			t.Fatalf("%+v %v", rec, err)
		}
		if rec.Status == "failed" || rec.Cleanup != "" || rec.Error == "cleanup_pending" {
			t.Fatalf("%+v", rec)
		}
		if len(cat.drops) != 1 || cat.drops[0] != "origdb_replaced_abcd" || cat.present["origdb_replaced_abcd"] {
			t.Fatalf("replaced database was not the only drop: %v present %v", cat.drops, cat.present)
		}
		raw, readErr := os.ReadFile(log)
		if readErr != nil || strings.Contains(string(raw), "DROP DATABASE") {
			t.Fatalf("previous name was dropped: %v %s", readErr, raw)
		}
		notes := noticeBody(t, box)
		if strings.Contains(notes, "Existing data was left in place") || !strings.Contains(notes, "The restored database is live") || !strings.Contains(notes, "commit was not recorded") {
			t.Fatalf("notice %q", notes)
		}
		if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
			t.Fatalf("order %v", order)
		}
	})
	t.Run("unreadable catalog stays pending and does not restart", func(t *testing.T) {
		var order []string
		dir := t.TempDir()
		box := OpenOutbox(filepath.Join(dir, "outbox"))
		cat := &scriptCatalog{err: errors.New("catalog down")}
		ops := newOps(filepath.Join(dir, "restore-journal.jsonl"), cat, &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}}, box)
		ops.Journal.failPhase = phaseApplied
		ops.Journal.failRemaining = 1
		bin := psqlOK(t, filepath.Join(dir, "psql.log"))
		ops.ApplyRestore = func(ctx context.Context, _ RestorePayload) error {
			return swapDatabase(ctx, bin, "postgres://example/postgres", "origdb", "freshdb", "abcd")
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if !errors.Is(err, errCommitUnjournaled) || rec.Status == "failed" || rec.Status == "completed" || rec.Recovery != "catalog_unobserved" {
			t.Fatalf("%+v %v", rec, err)
		}
		if containsStep(order, "restart:core-1") || containsStep(order, "health") {
			t.Fatalf("restarted against an unreadable catalog: %v", order)
		}
		notes := noticeBody(t, box)
		if strings.Contains(notes, "Existing data was left in place") || !strings.Contains(notes, "commit was not recorded") || !strings.Contains(notes, "Writers were not restarted") {
			t.Fatalf("notice %q", notes)
		}
		if _, err := ops.Restore(context.Background(), meta.ID); !errors.Is(err, ErrRecoveryRequired) {
			t.Fatalf("admission while the commit is unjournaled: %v", err)
		}
	})
}

func TestRollbackUnjournaledKeepsTheLoadedDatabase(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	dir := t.TempDir()
	log := filepath.Join(dir, "psql.log")
	bin := filepath.Join(dir, "psql")
	script := `#!/bin/sh
cmd=
prev=
for arg in "$@"; do
  if [ "$prev" = "--command" ]; then cmd=$arg; fi
  prev=$arg
done
printf '%s\n' "$cmd" >> "` + log + `"
case "$cmd" in
  BEGIN*) echo refused >&2; exit 1 ;;
  *system_identifier*) echo 100 ;;
  *oid::text*) echo 10 ;;
  *togavsu_fence_op*) echo cancelled ;;
  "SELECT datname"*) printf 'origdb\nfreshdb\n' ;;
esac
exit 0
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	box := OpenOutbox(filepath.Join(dir, "outbox"))
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		Journal:    OpenJournal(filepath.Join(dir, "restore-journal.jsonl")),
		Outbox:     box,
		Passphrase: func() (string, error) { return store.Passphrase, nil },
		Runtime:    &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	ops.Journal.failPhase = phaseRolledBack
	ops.Journal.failRemaining = 1
	ops.ApplyRestore = func(ctx context.Context, _ RestorePayload) error {
		return swapDatabase(ctx, bin, "postgres://example/postgres", "origdb", "freshdb", "abcd")
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, errRollbackUnjournaled) || rec.Status != "failed" || rec.Recovery != "rollback_unjournaled" {
		t.Fatalf("%+v %v", rec, err)
	}
	raw, readErr := os.ReadFile(log)
	if readErr != nil || strings.Contains(string(raw), "DROP DATABASE") {
		t.Fatalf("loaded database was dropped: %v %s", readErr, raw)
	}
	notes := noticeBody(t, box)
	if !strings.Contains(notes, "Existing data was left in place") || !strings.Contains(notes, "loaded database was kept") || strings.Contains(notes, "The restored database is live") {
		t.Fatalf("notice %q", notes)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers stayed stopped after a confirmed unchanged database: %v", order)
	}
}

func TestAllocationWithoutOIDRestartsWhenCreateDidNotHappen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
	j := OpenJournal(path)
	if err := j.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-alloc", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseApplying,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{
			Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd",
			Allocated: true, OpID: "restore-alloc",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	cat := &scriptCatalog{present: map[string]bool{"origdb": true}}
	ops := &Operations{Gate: &Gate{}, Journal: j, Runtime: rt, Catalog: cat}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") || len(cat.drops) != 0 {
		t.Fatalf("order %v drops %v", order, cat.drops)
	}
	got, ok, err := ops.LookupRestore("restore-alloc")
	if err != nil || !ok || got.Status != "failed" || got.Status == "running" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestRolledBackRecordRestartsAfterTheLoadedNameIsGone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
	j := OpenJournal(path)
	if err := j.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-rb-gone", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseRolledBack,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{
			Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd",
			SwapIntended: true, SwapRolledBack: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	ops := &Operations{
		Gate:    &Gate{},
		Journal: j,
		Runtime: rt,
		Catalog: &scriptCatalog{present: map[string]bool{"origdb": true}},
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
		t.Fatalf("order %v", order)
	}
	got, ok, err := ops.LookupRestore("restore-rb-gone")
	if err != nil || !ok || got.Status != "failed" || got.Recovery != "rolled_back" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
}

func TestRolledBackRecordRestartsWhenOnlyTheConfiguredNameRemains(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "restore-journal.jsonl")
	j := OpenJournal(path)
	if err := j.Put(journalRecord{
		Record: RestoreRecord{ID: "restore-rb", BackupID: "backup", Status: "running", Started: time.Now().UTC()},
		Phase:  phaseRolledBack,
		Containers: []containerIntent{{
			ID: "core-1", Prior: "running", StopIntended: true, Observed: "exited",
		}},
		Database: &databaseIntent{
			Configured: "origdb", Fresh: "freshdb", Replaced: "origdb_replaced_abcd",
			SwapIntended: true, SwapRolledBack: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	var order []string
	rt := &scriptRuntime{order: &order, states: map[string]string{"core-1": "exited"}}
	cat := &dropCatalog{present: map[string]bool{"origdb": true, "freshdb": true}}
	ops := &Operations{Gate: &Gate{}, Journal: j, Runtime: rt, Catalog: cat}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
		t.Fatalf("order %v", order)
	}
	got, ok, err := ops.LookupRestore("restore-rb")
	if err != nil || !ok || got.Status != "failed" || got.Recovery != "rolled_back" || got.Status == "running" {
		t.Fatalf("%+v %v %v", got, ok, err)
	}
	if len(cat.dropped) != 1 || cat.dropped[0] != "freshdb" || cat.present["origdb"] != true {
		t.Fatalf("dropped %v present %v", cat.dropped, cat.present)
	}
}

type dropCatalog struct {
	present map[string]bool
	dropped []string
}

func (d *dropCatalog) Observe(context.Context, string, string, string) (map[string]bool, error) {
	return d.present, nil
}

func (d *dropCatalog) Drop(_ context.Context, name string) error {
	d.dropped = append(d.dropped, name)
	delete(d.present, name)
	return nil
}

func TestRestoreQuiescesRestartingAndRejectsUnknown(t *testing.T) {
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	newOps := func(path string, rt *scriptRuntime) *Operations {
		return &Operations{
			Store: store, Gate: &Gate{}, Now: store.Now,
			Journal:    OpenJournal(path),
			Passphrase: func() (string, error) { return store.Passphrase, nil },
			Runtime:    rt,
		}
	}
	t.Run("restarting is stopped before the safety backup and started again", func(t *testing.T) {
		var order []string
		path := filepath.Join(t.TempDir(), "restore-journal.jsonl")
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1", State: "restarting"}}}
		ops := newOps(path, rt)
		ops.ApplyDump = func(context.Context, io.Reader) error {
			order = append(order, "apply")
			return nil
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err != nil || rec.Status != "completed" {
			t.Fatalf("%+v %v", rec, err)
		}
		stopAt, applyAt := -1, -1
		for i, step := range order {
			if step == "stop:core-1" && stopAt < 0 {
				stopAt = i
			}
			if step == "apply" {
				applyAt = i
			}
		}
		if stopAt < 0 || applyAt < 0 || stopAt > applyAt || !containsStep(order, "restart:core-1") {
			t.Fatalf("order %v", order)
		}
		item, ok, err := OpenJournal(path).Latest(rec.ID)
		if err != nil || !ok || len(item.Containers) == 0 || item.Containers[0].Prior != "restarting" || !item.Containers[0].StopIntended {
			t.Fatalf("%+v %v %v", item, ok, err)
		}
	})
	t.Run("unknown state is rejected before a safety backup", func(t *testing.T) {
		var order []string
		before, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1", State: "unknown"}}}
		ops := newOps(filepath.Join(t.TempDir(), "restore-journal.jsonl"), rt)
		ops.ApplyDump = func(context.Context, io.Reader) error {
			t.Fatal("database swap started")
			return nil
		}
		rec, err := ops.Restore(context.Background(), meta.ID)
		if err == nil || rec.Status != "failed" {
			t.Fatalf("%+v %v", rec, err)
		}
		if containsStep(order, "stop:core-1") || containsStep(order, "restart:core-1") {
			t.Fatalf("order %v", order)
		}
		after, err := store.List()
		if err != nil || len(after) != len(before) {
			t.Fatalf("safety backup was written: %d %d %v", len(before), len(after), err)
		}
	})
}

func TestZeroRowCountMismatchDoesNotReplaceDatabase(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sql.log")
	script := filepath.Join(dir, "psql")
	body := "#!/bin/sh\n" +
		"sql=\nprev=\n" +
		"for arg in \"$@\"; do\n" +
		"  printf '%s\\n' \"$arg\" >> " + logPath + "\n" +
		"  if [ \"$prev\" = \"--command\" ]; then sql=$arg; fi\n" +
		"  prev=$arg\n" +
		"done\n" +
		"case \"$sql\" in\n" +
		"  *system_identifier*) echo 100; exit 0 ;;\n" +
		"  *oid::text*) if printf '%s' \"$sql\" | grep -q togavsu_restore_; then if [ -f " + logPath + ".created ]; then echo 11; fi; exit 0; fi; echo 10; exit 0 ;;\n" +
		"  *\"ALTER DATABASE \"*|*'RENAME TO '*) echo replaced >> " + logPath + "; exit 0 ;;\n" +
		"  \"CREATE DATABASE \"*) : > " + logPath + ".created; exit 0 ;;\n" +
		"  \"DROP DATABASE \"*) exit 0 ;;\n" +
		"  *pg_class*) echo core.matters ;;\n" +
		"  \"SELECT count(*)\"*) echo 0 ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	err := RestoreIntoCleanDatabase(context.Background(), script, "postgres://togavsu@127.0.0.1/postgres", "postgres://togavsu@127.0.0.1/togavsu", strings.NewReader("SELECT 1;\n"), map[string]int64{"core.users": 0})
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("mismatch: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "RENAME TO") || strings.Contains(string(raw), "ALTER DATABASE") {
		t.Fatalf("database was replaced:\n%s", raw)
	}
}

func TestStalledPostgresCleanupStillRestartsWriters(t *testing.T) {
	old := cleanupIOTimeout
	cleanupIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { cleanupIOTimeout = old })
	dir := t.TempDir()
	dropped := filepath.Join(dir, "dropped")
	script := filepath.Join(dir, "psql")
	body := "#!/bin/sh\n" +
		"sql=\nprev=\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--command\" ]; then sql=$arg; fi\n" +
		"  prev=$arg\n" +
		"done\n" +
		"case \"$sql\" in\n" +
		"  *system_identifier*) echo 100; exit 0 ;;\n" +
		"  *oid::text*) if printf '%s' \"$sql\" | grep -q togavsu_restore_; then if [ -f " + dropped + ".created ]; then echo 11; fi; exit 0; fi; echo 10; exit 0 ;;\n" +
		"  *\"WHERE oid = \"*) if [ -f " + dropped + ".name ]; then cat " + dropped + ".name; fi; exit 0 ;;\n" +
		"  *togavsu_restore_op*) echo op; exit 0 ;;\n" +
		"  *pg_terminate_backend*) exit 0 ;;\n" +
		"  \"CREATE DATABASE \"*) printf '%s\\n' \"${sql#CREATE DATABASE }\" > " + dropped + ".name; : > " + dropped + ".created; exit 0 ;;\n" +
		"  \"DROP DATABASE \"*) echo dropped > " + dropped + "; exec sleep 30 ;;\n" +
		"esac\n" +
		"exec sleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		RestoreTimeout: 2 * time.Second,
		Runtime:        &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Passphrase:     func() (string, error) { return store.Passphrase, nil },
		ApplyRestore: func(ctx context.Context, payload RestorePayload) error {
			return RestoreIntoCleanDatabase(ctx, script, "postgres://togavsu@127.0.0.1/postgres", "postgres://togavsu@127.0.0.1/togavsu", payload.Dump, payload.Manifest.RecordCounts)
		},
	}
	start := time.Now()
	rec, err := ops.Restore(context.Background(), meta.ID)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("restore held writers for %s", elapsed)
	}
	if err == nil || rec.Status == "completed" {
		t.Fatalf("stalled load was applied: %+v %v", rec, err)
	}
	if _, statErr := os.Stat(dropped); statErr != nil {
		t.Fatalf("cleanup did not start: %v", statErr)
	}
	if !containsStep(order, "stop:core-1") || !containsStep(order, "restart:core-1") {
		t.Fatalf("writers were not recovered: %v", order)
	}
}

func TestStalledSSHHandshakeRestartsWriters(t *testing.T) {
	if _, err := exec.LookPath("sftp"); err != nil {
		t.Fatal(err)
	}
	old := destinationIOTimeout
	destinationIOTimeout = 300 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	var connMu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			connMu.Lock()
			conns = append(conns, c)
			connMu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		connMu.Lock()
		for _, c := range conns {
			c.Close()
		}
		connMu.Unlock()
	})
	port := ln.Addr().(*net.TCPAddr).Port
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-f", key, "-N", "").CombinedOutput(); err != nil {
		t.Fatalf("key: %v %s", err, out)
	}
	known := filepath.Join(dir, "known")
	if err := os.WriteFile(known, []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	store.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(store.Dir, "destinations.json")}
	if _, err := store.Book.Put(DestinationSpec{
		ID: "share", Kind: "sftp", Host: "127.0.0.1", User: "backup", Port: port,
		IdentityFile: key, KnownHosts: known, RemotePath: "/tmp/backups", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	store.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &SFTPDestination{Spec: spec, Command: "sftp"}, nil
	}
	var order []string
	ops := &Operations{
		Store: store, Gate: &Gate{}, Now: store.Now,
		RestoreTimeout: 8 * time.Second,
		Runtime:        &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Passphrase:     func() (string, error) { return store.Passphrase, nil },
		ApplyDump: func(context.Context, io.Reader) error {
			return nil
		},
	}
	start := time.Now()
	done := make(chan struct{})
	var rec RestoreRecord
	var restoreErr error
	go func() {
		rec, restoreErr = ops.Restore(context.Background(), meta.ID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled ssh handshake held writers")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stalled ssh handshake held writers for %s", elapsed)
	}
	if restoreErr == nil && rec.Status == "completed" && len(order) == 0 {
		t.Fatal("restore did not exercise the destination")
	}
	if !containsStep(order, "stop:core-1") || !containsStep(order, "restart:core-1") {
		t.Fatalf("writers were not recovered: %v err %v rec %+v", order, restoreErr, rec)
	}
}

func noticeBody(t *testing.T, box *Outbox) string {
	t.Helper()
	notes, err := box.List()
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, note := range notes {
		b.WriteString(note.Subject)
		b.WriteByte('\n')
		b.WriteString(note.Body)
		b.WriteByte('\n')
	}
	return b.String()
}

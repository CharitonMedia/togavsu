package supervisor

import (
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
	present map[string]bool
	err     error
	calls   int
}

func (c *scriptCatalog) Observe(context.Context, string, string, string) (map[string]bool, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.present, nil
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
				if lerr != nil || !ok || rec.Status != "completed" || rec.Cleanup != "pending" {
					t.Fatalf("%+v %v %v", rec, ok, lerr)
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

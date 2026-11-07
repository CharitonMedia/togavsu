package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCanceledMigrationCleanupFailureBlocksAdmission(t *testing.T) {
	var mu sync.Mutex
	gone := map[string]bool{}
	deletes := map[string]int{}
	var created string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/create", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
	})
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		created = r.URL.Query().Get("name")
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "mig-1"})
	})
	mux.HandleFunc("/v1.43/containers/mig-1/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/containers/mig-1/wait", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1.43/containers/mig-1/json", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		absent := gone["mig-1"]
		mu.Unlock()
		if absent {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "mig-1", "Name": "/" + created, "State": map[string]any{"Status": "running", "Running": true}})
	})
	mux.HandleFunc("/v1.43/containers/mig-1/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/containers/mig-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		deletes["mig-1"]++
		n := deletes["mig-1"]
		mu.Unlock()
		if n == 1 {
			http.Error(w, "busy", http.StatusInternalServerError)
			return
		}
		mu.Lock()
		gone["mig-1"] = true
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	st, err := (DockerMigrator{Docker: &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}}).RunMigration(ctx, testReleaseImage, "togavsu-migrate-once", func(phase, id string) error {
		rec := updateIntent{ID: "upd-1", Status: "running", Phase: phase, MigrationName: "togavsu-migrate-once", MigrationID: id}
		return journal.Put(rec)
	})
	if !errors.Is(err, ErrMigrationUnknown) || !st.CleanupPending {
		t.Fatalf("status %+v err %v", st, err)
	}
	_ = journal.Put(updateIntent{ID: "upd-1", Status: "running", Phase: "migration_unknown", Cleanup: "migration", MigrationName: st.Name, MigrationID: st.ID, MigrationRan: "unknown"})
	ops := &Operations{Gate: &Gate{}, UpdateJournal: journal, Updater: &Updater{Journal: journal, Runtime: &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}, Proxy: &scriptProxy{live: strPtr("core:8080")}}}
	if err := ops.blockUnresolved(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("admission while migration container remains: %v", err)
	}
	if err := ops.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := ops.blockUnresolved(); err != nil {
		t.Fatalf("cleanup did not clear: %v", err)
	}
}

func TestLostCreateACKDoesNotCreateTwice(t *testing.T) {
	var creates int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/create", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
	})
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		creates++
		http.Error(w, "lost", http.StatusInternalServerError)
	})
	mux.HandleFunc("/v1.43/containers/togavsu-migrate-lost/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "mig-lost", "Name": "/togavsu-migrate-lost", "State": map[string]any{"Status": "created", "Running": false}})
	})
	removed := false
	mux.HandleFunc("/v1.43/containers/mig-lost/json", func(w http.ResponseWriter, r *http.Request) {
		if removed {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "mig-lost", "Name": "/togavsu-migrate-lost", "State": map[string]any{"Status": "exited", "Running": false}})
	})
	mux.HandleFunc("/v1.43/containers/mig-lost/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/containers/mig-lost/wait", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": 0})
	})
	mux.HandleFunc("/v1.43/containers/mig-lost/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("migrated=true"))
	})
	mux.HandleFunc("/v1.43/containers/mig-lost/stop", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/v1.43/containers/mig-lost", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			removed = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	var phases []string
	st, err := (DockerMigrator{Docker: &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}}).RunMigration(context.Background(), testReleaseImage, "togavsu-migrate-lost", func(phase, id string) error {
		phases = append(phases, phase+":"+id)
		return nil
	})
	if err != nil || !st.Known || !st.Ran || creates != 1 {
		t.Fatalf("status %+v err %v creates %d phases %v", st, err, creates, phases)
	}
	if len(phases) == 0 || phases[0] != "migration_intended:" {
		t.Fatalf("name was not persisted before create: %v", phases)
	}
}

func TestCaddyUnknownRouteRemovesNoCore(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Proxy = &scriptProxy{order: &order, liveErr: ErrProxyUnknown}
	got, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrProxyUnknown) || got.Status == "up_to_date" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, step := range order {
		if step == "stop:old" || step == "remove:old" || step == "backup" {
			t.Fatalf("mutated while the route was unknown: %v", order)
		}
	}
}

func TestServingCoreMatchesComposeAlias(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "coreid", Name: "togavsu-core-1", Aliases: []string{"core", "togavsu-core-1"},
		Labels: map[string]string{"togavsu.version": "1.0.0"}, State: "running", Running: true,
	}}}
	up := testUpdater(t, rt, &order)
	up.Proxy = &scriptProxy{order: &order, live: strPtr("core:8080")}
	got, err := up.servingCore(context.Background())
	if err != nil || got.ID != "coreid" || got.Name != "togavsu-core-1" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, step := range order {
		if step == "stop:coreid" || step == "backup" {
			t.Fatalf("selecting the aliased core mutated it: %v", order)
		}
	}
}

func TestServingCoreRejectsAmbiguousAlias(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{
		{ID: "a", Name: "togavsu-core-1", Aliases: []string{"core"}},
		{ID: "b", Name: "togavsu-core-2", Aliases: []string{"core"}},
	}}
	up := testUpdater(t, rt, &order)
	up.Proxy = &scriptProxy{order: &order, live: strPtr("core:8080")}
	if _, err := up.servingCore(context.Background()); !errors.Is(err, ErrProxyUnknown) {
		t.Fatalf("got %v", err)
	}
	got, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrProxyUnknown) || got.Status == "up_to_date" {
		t.Fatalf("%+v %v", got, err)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "stop:") || strings.HasPrefix(step, "remove:") || step == "backup" {
			t.Fatalf("ambiguous alias was treated as a serving core: %v", order)
		}
	}
}

func TestAliasStillServingPreviousRetiresOnlyTheCandidate(t *testing.T) {
	var order []string
	current := ContainerInfo{ID: "old", Name: "togavsu-core-1", Aliases: []string{"core"}, State: "running", Running: true}
	next := ContainerInfo{ID: "new-core", Name: "togavsu-core-abcdef123456", State: "running", Running: true}
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{current, next}}
	up := &Updater{Runtime: rt, Proxy: &scriptProxy{order: &order, live: strPtr("core:8080")}}
	mode, err := up.route(context.Background(), "", current, next, "core:8080", nil)
	if err == nil || mode != "previous_serving" {
		t.Fatalf("mode %s err %v", mode, err)
	}
	if !containsStep(order, "stop:new-core") || !containsStep(order, "remove:new-core") || containsStep(order, "stop:old") {
		t.Fatalf("order %v", order)
	}
}

func TestHealthFailureThenRetryDoesNotNeedRestart(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	rt := &scriptRuntime{order: &order, health: errors.New("unhealthy"), cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	rt.stopHook = func() {
		if release, ok := ops.Gate.Try(); ok {
			release()
			t.Error("operation gate was released while cleanup was running")
		}
	}
	if _, err := ops.Update(context.Background(), true); !errors.Is(err, ErrHealth) {
		t.Fatalf("first %v", err)
	}
	if err := ops.blockUnresolved(); err != nil {
		t.Fatalf("cleaned health failure stayed pending: %v", err)
	}
	rt.health = nil
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("retry %+v %v", got, err)
	}
}

func TestKnownMigrationFailureThenRetry(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	up.Migrator = &countingMigrator{}
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	if _, err := ops.Update(context.Background(), true); err == nil || errors.Is(err, ErrMigrationUnknown) {
		t.Fatalf("first %v", err)
	}
	if err := ops.blockUnresolved(); err != nil {
		t.Fatalf("known migration failure stayed pending: %v", err)
	}
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("retry %+v %v", got, err)
	}
}

func TestCleanupFailureStaysPendingUntilRetry(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	rt := &scriptRuntime{order: &order, health: errors.New("unhealthy"), failRemove: 1, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	if _, err := ops.Update(context.Background(), true); !errors.Is(err, ErrHealth) {
		t.Fatalf("first %v", err)
	}
	if err := ops.blockUnresolved(); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("cleanup failure was not pending: %v", err)
	}
	rt.health = nil
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("reconcile retry %+v %v order %v", got, err, order)
	}
}

type countingMigrator struct{ n int }

func (m *countingMigrator) Apply(context.Context, string) (bool, error) {
	m.n++
	if m.n == 1 {
		return false, errors.New("migrate failed")
	}
	return true, nil
}

func TestLiveRouteIsPersistedBeforeEitherCoreIsRetired(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upstream")
	if err := os.WriteFile(file, []byte("core:8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current := ContainerInfo{ID: "old", Name: "togavsu-core-1", Aliases: []string{"core"}}
	next := ContainerInfo{ID: "new-core", Name: "togavsu-core-abcdef123456"}
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{current, next}}
	rt.stopHook = func() {
		body, err := os.ReadFile(file)
		if err != nil || strings.TrimSpace(string(body)) != next.Name+":8080" {
			t.Errorf("stopped a core before the restart file named the live candidate: %q %v", body, err)
		}
	}
	live := next.Name + ":8080"
	up := &Updater{Runtime: rt, UpstreamFile: file, Proxy: &scriptProxy{order: &order, live: &live}}
	if _, err := up.route(context.Background(), "op", current, next, live, nil); err != nil {
		t.Fatal(err)
	}
	if containsStep(order, "stop:old") == false || containsStep(order, "stop:new-core") {
		t.Fatalf("candidate route retired the wrong core: %v", order)
	}
	file = filepath.Join(t.TempDir(), "upstream")
	if err := os.WriteFile(file, []byte(next.Name+":8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	order = nil
	rt = &scriptRuntime{order: &order, cores: []ContainerInfo{current, next}}
	rt.stopHook = func() {
		body, _ := os.ReadFile(file)
		if strings.TrimSpace(string(body)) != "core:8080" {
			t.Errorf("stopped a core before the file was repaired to the live previous route: %q", body)
		}
	}
	old := "core:8080"
	up = &Updater{Runtime: rt, UpstreamFile: file, Proxy: &scriptProxy{order: &order, live: &old}}
	if _, err := up.route(context.Background(), "op", current, next, old, nil); err == nil || !containsStep(order, "stop:new-core") || containsStep(order, "stop:old") {
		t.Fatalf("previous route err %v order %v", err, order)
	}
	blocker := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	_ = journal.Put(updateIntent{ID: "op", Status: "running", Phase: "switch_intended", CurrentID: current.ID, CurrentName: current.Name, NextID: next.ID, NextName: next.Name})
	order = nil
	rt = &scriptRuntime{order: &order, cores: []ContainerInfo{current, next}}
	up = &Updater{Runtime: rt, Journal: journal, UpstreamFile: filepath.Join(blocker, "upstream"), Proxy: &scriptProxy{order: &order, live: &live}}
	if _, err := up.route(context.Background(), "op", current, next, live, nil); err == nil {
		t.Fatal("file failure was ignored")
	}
	if containsStep(order, "stop:old") || containsStep(order, "stop:new-core") || containsStep(order, "remove:old") || containsStep(order, "remove:new-core") {
		t.Fatalf("file failure retired a core: %v", order)
	}
	rec, ok, err := journal.Latest("op")
	if err != nil || !ok || rec.terminal() || rec.Cleanup != "route_file" {
		t.Fatalf("file failure was not pending: %+v %v %v", rec, ok, err)
	}
}

func TestLostMigrationReplyStaysPendingUntilTheNameIsRemoved(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	rt := &scriptRuntime{order: &order, failInspect: 1, failRemove: 1, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	mig := &lostMigration{rt: rt}
	up.Migrator = mig
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	if _, err := ops.Update(context.Background(), true); err == nil {
		t.Fatal("lost migration reply succeeded")
	}
	pending, err := journal.Incomplete()
	if err != nil || len(pending) != 1 || pending[0].MigrationName == "" || pending[0].terminal() || pending[0].Cleanup != "migration" {
		t.Fatalf("lost migration was terminal: %+v %v", pending, err)
	}
	name := pending[0].MigrationName
	if countStep(order, "name:"+name) != 1 || containsStep(order, "remove:"+name) {
		t.Fatalf("name was removed or created twice before it could be seen: %v", order)
	}
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("retry %+v %v %v", got, err, order)
	}
	if countStep(order, "name:"+name) != 1 || !containsStep(order, "remove:"+name) {
		t.Fatalf("migration name was not reconciled once: %v", order)
	}
}

func TestMigrationIntendedCrashRemovesByName(t *testing.T) {
	var order []string
	name := "togavsu-migrate-crash"
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Name: "core"}}}
	rt.started = []ContainerInfo{{ID: "mig-" + name, Name: name, State: "exited"}}
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	if err := journal.Put(updateIntent{
		ID: "op", Status: "running", Phase: "migration_intended", MigrationName: name,
		CurrentID: "old", CurrentName: "core",
	}); err != nil {
		t.Fatal(err)
	}
	up := &Updater{Runtime: rt, Journal: journal, Proxy: &scriptProxy{order: &order, live: strPtr("core:8080")}}
	if err := up.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !containsStep(order, "remove:"+name) {
		t.Fatalf("crash recovery did not remove the migration name: %v", order)
	}
	got, ok, err := journal.Latest("op")
	if err != nil || !ok || !got.terminal() || got.Status != "failed" {
		t.Fatalf("left the intent running after the name was gone: %+v %v %v", got, ok, err)
	}
	rt.failInspect = 1
	if err := journal.Put(updateIntent{
		ID: "op-2", Status: "running", Phase: "migration_intended", MigrationName: "togavsu-migrate-unseen",
		CurrentID: "old", CurrentName: "core",
	}); err != nil {
		t.Fatal(err)
	}
	rt.started = append(rt.started, ContainerInfo{ID: "mig-unseen", Name: "togavsu-migrate-unseen"})
	if err := up.Recover(context.Background()); err == nil {
		t.Fatal("unreadable migration name was finalized")
	}
	held, ok, err := journal.Latest("op-2")
	if err != nil || !ok || held.terminal() || held.Cleanup != "migration" {
		t.Fatalf("unreadable name was not kept pending: %+v %v %v", held, ok, err)
	}
}

type lostMigration struct {
	rt *scriptRuntime
	n  int
}

func (m *lostMigration) Apply(context.Context, string) (bool, error) {
	return false, errors.New("lost create reply")
}

func (m *lostMigration) RunMigration(_ context.Context, _, name string, before func(phase, id string) error) (migrationStatus, error) {
	if before != nil {
		if err := before("migration_intended", ""); err != nil {
			return migrationStatus{Name: name}, err
		}
	}
	m.n++
	if m.n > 1 {
		return migrationStatus{Known: true, Name: name}, nil
	}
	m.rt.started = append(m.rt.started, ContainerInfo{ID: "mig-" + name, Name: name, State: "exited"})
	m.rt.add("name:" + name)
	return migrationStatus{Name: name}, errors.New("lost create reply")
}

func TestLostCreateInspectFailureStaysPendingUntilTheNameIsRemoved(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	rt := &scriptRuntime{order: &order, loseCreate: true, failInspect: 2, failRemove: 1, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	if _, err := ops.Update(context.Background(), true); err == nil {
		t.Fatal("lost create was treated as success")
	}
	pending, err := journal.Incomplete()
	if err != nil || len(pending) != 1 || pending[0].NextName == "" || pending[0].terminal() {
		t.Fatalf("lost create was terminal: %+v %v", pending, err)
	}
	name := pending[0].NextName
	if countStep(order, "name:"+name) != 1 {
		t.Fatalf("create count %v", order)
	}
	rt.loseCreate = false
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("retry %+v %v %v", got, err, order)
	}
	if countStep(order, "name:"+name) != 1 {
		t.Fatalf("name was created again: %v", order)
	}
	if !containsStep(order, "remove:"+name) && !containsStep(order, "remove:new-"+name) {
		t.Fatalf("retry did not remove the named candidate: %v", order)
	}
}

func TestCandidateCreatedJournalFailureStillRemovesTheContainer(t *testing.T) {
	var order []string
	journal := OpenUpdateJournal(t.TempDir() + "/update-journal.jsonl")
	journal.failPhase = "candidate_created"
	journal.failRemaining = 1
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	up.Journal = journal
	ops := &Operations{Gate: &Gate{}, Updater: up, UpdateJournal: journal, Passphrase: func() (string, error) { return "synthetic", nil }}
	if _, err := ops.Update(context.Background(), true); err == nil {
		t.Fatal("journal failure was ignored")
	}
	var name string
	for _, step := range order {
		if strings.HasPrefix(step, "name:") {
			name = strings.TrimPrefix(step, "name:")
		}
	}
	if name == "" || countStep(order, "name:"+name) != 1 {
		t.Fatalf("create %q %v", name, order)
	}
	if !containsStep(order, "remove:new-"+name) {
		t.Fatalf("created container was left behind: %v", order)
	}
	if pending, err := journal.Incomplete(); err != nil || len(pending) != 0 {
		t.Fatalf("cleanup did not finish: %+v %v", pending, err)
	}
	got, err := ops.Update(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("retry %+v %v %v", got, err, order)
	}
	if countStep(order, "name:"+name) != 1 {
		t.Fatalf("same candidate was created twice: %v", order)
	}
}

func countStep(steps []string, want string) int {
	n := 0
	for _, step := range steps {
		if step == want {
			n++
		}
	}
	return n
}

func strPtr(s string) *string { return &s }

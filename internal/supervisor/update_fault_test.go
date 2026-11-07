package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"net/http/httptest"
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

func strPtr(s string) *string { return &s }

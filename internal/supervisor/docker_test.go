package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDockerStartAndMissingHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("filters"), "togavsu.module=clock") {
			http.Error(w, "filter", 400)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"Id": "mod1", "Names": []string{"/clock"}, "Image": "clock:1", "State": "exited",
		}})
	})
	mux.HandleFunc("/v1.43/containers/mod1/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "togavsu-core-next" {
			http.Error(w, "name", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "new"})
	})
	mux.HandleFunc("/v1.43/containers/new/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	})
	mux.HandleFunc("/v1.43/containers/new/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Status": "running", "Health": map[string]any{"Status": "none"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "togavsu_default"}
	if err := rt.StartModule(context.Background(), "clock"); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartModule(context.Background(), "Not_Valid"); err == nil {
		t.Fatal("accepted module name")
	}
	info, err := rt.StartCore(context.Background(), "togavsu-core:1.2.0", "togavsu-core-next")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.WaitHealthy(context.Background(), info.ID, time.Second)
	if err == nil || !strings.Contains(err.Error(), "no health check") {
		t.Fatalf("got %v", err)
	}
}

func TestReadDockerLog(t *testing.T) {
	frame := make([]byte, 8+4)
	binary.BigEndian.PutUint32(frame[4:8], 4)
	copy(frame[8:], []byte("true"))
	got, err := ReadDockerLog(bytes.NewReader(frame))
	if err != nil || got != "true" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestMigratorUnconfigured(t *testing.T) {
	_, err := (DockerMigrator{}).Apply(context.Background(), "image")
	code, _ := PublicCode(err)
	if err == nil || code != "not_configured" {
		t.Fatalf("%v", err)
	}
}

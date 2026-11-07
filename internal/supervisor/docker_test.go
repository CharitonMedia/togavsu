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

func TestStartCoreReproducesComposeSpec(t *testing.T) {
	var created map[string]any
	var pulled string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/create", func(w http.ResponseWriter, r *http.Request) {
		pulled = r.URL.Query().Get("fromImage")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
	})
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "created"})
	})
	mux.HandleFunc("/v1.43/containers/created/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	image := testReleaseImage
	t.Setenv("TOGAVSU_DOMAIN", "firm.example")
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "togavsu_default"}
	if err := rt.Pull(context.Background(), image); err != nil {
		t.Fatal(err)
	}
	if pulled != image {
		t.Fatalf("pulled %q", pulled)
	}
	if _, err := rt.StartCore(context.Background(), image, "togavsu-core-abc"); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"TOGAVSU_DOMAIN=firm.example",
		"SUPERVISOR_URL=http://supervisor:8081",
		"togavsu_appsecrets:/appsecrets",
		"65532:65532",
		"unless-stopped",
		image,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create body missing %s: %s", want, body)
		}
	}
	for _, banned := range []string{"FIELD_KEY_FILE", "DATABASE_URL_FILE", "SUPERVISOR_TOKEN_FILE", "postgres://"} {
		if strings.Contains(body, banned) {
			t.Fatalf("create body carried %s: %s", banned, body)
		}
	}
}

func TestPullConsumesStream(t *testing.T) {
	image := testReleaseImage
	t.Run("delayed completion", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"status\":\"Pulling fs layer\"}\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(200 * time.Millisecond)
			_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
		}))
		defer srv.Close()
		start := time.Now()
		err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}).Pull(context.Background(), image)
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) < 200*time.Millisecond {
			t.Fatal("returned before the stream finished")
		}
	})
	t.Run("http 200 error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{\"error\":\"manifest unknown\"}\n"))
		}))
		defer srv.Close()
		err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}).Pull(context.Background(), image)
		if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("truncated stream", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"Pulling`))
		}))
		defer srv.Close()
		err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}).Pull(context.Background(), image)
		if err == nil || !strings.Contains(err.Error(), "ended early") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestStartIsIdempotentWhenDockerSaysAlreadyRunning(t *testing.T) {
	var starts int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/core-1/start", func(w http.ResponseWriter, r *http.Request) {
		starts++
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("/v1.43/containers/core-1/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": "core-1", "Name": "/core",
			"State": map[string]any{"Status": "running", "Running": true},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}
	if err := rt.Start(context.Background(), "core-1"); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatalf("starts %d", starts)
	}
	info, err := rt.Inspect(context.Background(), "core-1")
	if err != nil || !info.Running || info.State != "running" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestMigratorUnconfigured(t *testing.T) {
	_, err := (DockerMigrator{}).Apply(context.Background(), "image")
	code, _ := PublicCode(err)
	if err == nil || code != "not_configured" {
		t.Fatalf("%v", err)
	}
}

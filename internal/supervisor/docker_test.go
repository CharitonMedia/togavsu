package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			"NetworkSettings": map[string]any{"Networks": map[string]any{"togavsu_default": map[string]any{}}},
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
		"togavsu_appsecrets:/appsecrets:ro",
		"togavsu_backupkey:/backupkey",
		"65532:65532",
		"unless-stopped",
		image,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create body missing %s: %s", want, body)
		}
	}
	// A replacement core never mounts the superuser URL (PRD section 6, AC-006).
	for _, banned := range []string{"FIELD_KEY_FILE", "DATABASE_URL_FILE", "SUPERVISOR_TOKEN_FILE", "postgres://", "adminsecrets", "ADMIN_DATABASE"} {
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

func TestDockerListReadsComposeNetworkAlias(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/togavsu-core-1"],"Image":"togavsu-core:local","Labels":{"togavsu.role":"core"},"State":"running","NetworkSettings":{"Networks":{"togavsu_default":{"Aliases":["core","togavsu-core-1"],"DNSNames":["core"]}}}}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cores, err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "togavsu_default"}).ListCores(context.Background())
	if err != nil || len(cores) != 1 || cores[0].Name != "togavsu-core-1" || !containerMatchesHost(cores[0], "core") {
		t.Fatalf("%+v %v", cores, err)
	}
}

// The engine's container list (Docker 29 here) returns null Aliases and DNSNames; the
// alias Caddy dials is only in inspect. A failed inspect fails the list.
func TestDockerListInspectsWhenListOmitsAliases(t *testing.T) {
	inspectFails := false
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/upd-load-core-1"],"Image":"upd-core:1.0.0","Labels":{"togavsu.role":"core","togavsu.version":"1.0.0"},"State":"running","NetworkSettings":{"Networks":{"upd-load_default":{"Aliases":null,"DNSNames":null}}}}]`))
	})
	mux.HandleFunc("/v1.43/containers/abc/json", func(w http.ResponseWriter, r *http.Request) {
		if inspectFails {
			http.Error(w, "engine down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"Id":"abc","Name":"/upd-load-core-1","State":{"Status":"running","Running":true},"NetworkSettings":{"Networks":{"upd-load_default":{"Aliases":["upd-load-core-1","core"],"DNSNames":["upd-load-core-1","core","abc"]}}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "upd-load_default"}
	cores, err := rt.ListCores(context.Background())
	if err != nil || len(cores) != 1 || !containerMatchesHost(cores[0], "core") || versionOf(cores[0]) != "1.0.0" {
		t.Fatalf("%+v %v", cores, err)
	}
	inspectFails = true
	if cores, err := rt.ListCores(context.Background()); err == nil {
		t.Fatalf("list without aliases succeeded on a failed inspect: %+v", cores)
	}
}

func TestStaleHealthDoesNotCountAsServing(t *testing.T) {
	for _, status := range []string{"exited", "paused", "dead"} {
		t.Run(status, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1.43/containers/core-1/json", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Id": "core-1", "State": map[string]any{
						"Status": status, "Running": false,
						"Health": map[string]any{"Status": "healthy"},
					},
				})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "togavsu_default"}).WaitHealthy(context.Background(), "core-1", time.Second)
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("stale health was accepted: %v", err)
			}
		})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/core-1/json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": "core-1", "State": map[string]any{
				"Status": "running", "Running": true,
				"Health": map[string]any{"Status": "healthy"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "togavsu_default"}).WaitHealthy(context.Background(), "core-1", time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestListCoresStayOnTheInstallationNetwork(t *testing.T) {
	var filters string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, r *http.Request) {
		filters = r.URL.Query().Get("filters")
		_, _ = w.Write([]byte(`[
			{"Id":"ours","Names":["/togavsu-core-1"],"Labels":{"togavsu.role":"core"},"State":"running","NetworkSettings":{"Networks":{"firm-a":{"Aliases":["core"],"DNSNames":["core"]}}}},
			{"Id":"theirs","Names":["/other-core-1"],"Labels":{"togavsu.role":"core"},"State":"running","NetworkSettings":{"Networks":{"firm-b":{"Aliases":["core"],"DNSNames":["core"]}}}}
		]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "firm-a"}
	cores, err := rt.ListCores(context.Background())
	if err != nil || len(cores) != 1 || cores[0].ID != "ours" {
		t.Fatalf("list %+v %v", cores, err)
	}
	all, err := rt.ListAllCores(context.Background())
	if err != nil || len(all) != 1 || all[0].ID != "ours" {
		t.Fatalf("all %+v %v", all, err)
	}
	if !strings.Contains(filters, "firm-a") || !strings.Contains(filters, "togavsu.role=core") {
		t.Fatalf("filters %s", filters)
	}
	if _, err := (&DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}).ListCores(context.Background()); err == nil {
		t.Fatal("unscoped discovery was allowed")
	}
}

func TestMigratorPullsBeforeCreate(t *testing.T) {
	var order []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/create", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "pull")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
	})
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "create")
		if len(order) == 0 || order[0] != "pull" {
			t.Fatal("create ran before pull")
		}
		var body struct {
			Cmd []string `json:"Cmd"`
			Env []string `json:"Env"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Cmd) != 1 || body.Cmd[0] != "migrate" {
			t.Fatalf("migrate command %v", body.Cmd)
		}
		// The entrypoint exports TOGAVSU_DATABASE_URL from the secret volume.
		// Neither URL name may be passed in the container environment.
		for _, kv := range body.Env {
			if strings.HasPrefix(kv, "DATABASE_URL=") || strings.HasPrefix(kv, "TOGAVSU_DATABASE_URL=") {
				t.Fatalf("database URL in migrate env: %q", kv)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "mig"})
	})
	mux.HandleFunc("/v1.43/containers/mig/start", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/containers/mig/wait", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"StatusCode": 0})
	})
	mux.HandleFunc("/v1.43/containers/mig/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("migrated=false"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ran, err := (DockerMigrator{Docker: &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL}}).Apply(context.Background(), testReleaseImage)
	if err != nil || ran {
		t.Fatalf("ran %v err %v order %v", ran, err, order)
	}
	if len(order) < 2 || order[0] != "pull" || order[1] != "create" {
		t.Fatalf("order %v", order)
	}
}

func TestStalledInspectRespectsHealthTimeout(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, buf)
		one := make([]byte, 1)
		_, _ = conn.Read(one)
	}))
	defer stall.Close()
	rt := &DockerRuntime{Client: &http.Client{}, BaseURL: stall.URL, Network: "togavsu_default"}
	start := time.Now()
	err := rt.WaitHealthy(context.Background(), "core-1", 200*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("health wait held the caller for %s", elapsed)
	}
	if err == nil || !errors.Is(err, ErrHealth) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("stalled inspect = %v", err)
	}
}

func TestStalledMigrationWaitFailsUncertain(t *testing.T) {
	old := dockerWaitLimit
	dockerWaitLimit = 200 * time.Millisecond
	t.Cleanup(func() { dockerWaitLimit = old })
	stallWait := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/create"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
		case strings.HasSuffix(r.URL.Path, "/create"):
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "mig"})
		case strings.HasSuffix(r.URL.Path, "/start"), strings.HasSuffix(r.URL.Path, "/stop"), r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "mig", "State": map[string]any{"Status": "running", "Running": true},
			})
		case strings.HasSuffix(r.URL.Path, "/wait"):
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", http.StatusInternalServerError)
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = io.Copy(io.Discard, buf)
			one := make([]byte, 1)
			_, _ = conn.Read(one)
		default:
			http.NotFound(w, r)
		}
	}))
	defer stallWait.Close()
	start := time.Now()
	st, err := (DockerMigrator{Docker: &DockerRuntime{Client: &http.Client{}, BaseURL: stallWait.URL}}).RunMigration(context.Background(), testReleaseImage, "togavsu-migrate-once", nil)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("migration wait held the caller for %s", elapsed)
	}
	if !st.CleanupPending || !errors.Is(err, ErrMigrationUnknown) {
		t.Fatalf("stalled wait %+v %v", st, err)
	}
}

func TestTwoComposeProjectsStaySeparate(t *testing.T) {
	compose := filepath.Join("..", "..", "compose.yaml")
	if _, err := os.Stat(compose); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"firma", "firmb"} {
		out, err := exec.Command("sudo", "docker", "compose", "-p", project, "-f", compose, "config").CombinedOutput()
		if err != nil {
			t.Fatalf("%s config: %v %s", project, err, out)
		}
		text := string(out)
		for _, want := range []string{
			"TOGAVSU_DOCKER_NETWORK: " + project + "_default",
			"TOGAVSU_APPSECRETS_VOLUME: " + project + "_appsecrets",
			"name: " + project + "_appsecrets",
			"name: " + project + "_default",
		} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s config missing %s", project, want)
			}
		}
		if strings.Contains(text, "togavsu_default") || strings.Contains(text, "togavsu_appsecrets") {
			t.Fatalf("%s config still names the default installation:\n%s", project, text)
		}
		cfg, err := LoadFrom(func(k string) string {
			switch k {
			case "SUPERVISOR_TOKEN":
				return "token"
			case "TOGAVSU_DOCKER_NETWORK":
				return project + "_default"
			case "TOGAVSU_APPSECRETS_VOLUME":
				return project + "_appsecrets"
			case "TOGAVSU_CORE_DATA_VOLUME":
				return project + "_core_data"
			default:
				return ""
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DockerNetwork != project+"_default" || cfg.AppSecretsVolume != project+"_appsecrets" {
			t.Fatalf("config %+v", cfg)
		}
		if got := CoreBinds(CoreVolumes{AppSecrets: cfg.AppSecretsVolume, Data: cfg.CoreDataVolume}); len(got) != 3 || got[0] != project+"_appsecrets:/appsecrets:ro" ||
			got[2] != project+"_core_data:/var/lib/togavsu-core" {
			t.Fatalf("binds %v", got)
		}
	}
	for _, project := range []string{"firma", "firmb"} {
		_ = exec.Command("sudo", "docker", "rm", "-f", project+"-core").Run()
		if out, err := exec.Command("sudo", "docker", "network", "create", project+"_default").CombinedOutput(); err != nil && !strings.Contains(string(out), "already exists") {
			t.Fatalf("network %s: %v %s", project, err, out)
		}
		if out, err := exec.Command("sudo", "docker", "volume", "create", project+"_appsecrets").CombinedOutput(); err != nil {
			t.Fatalf("volume %s: %v %s", project, err, out)
		}
		out, err := exec.Command("sudo", "docker", "run", "-d", "--name", project+"-core", "--network", project+"_default", "--label", "togavsu.role=core", "--entrypoint", "/bin/sleep", "caddy:2-alpine", "90").CombinedOutput()
		if err != nil {
			t.Fatalf("container %s: %v %s", project, err, out)
		}
	}
	t.Cleanup(func() {
		for _, project := range []string{"firma", "firmb"} {
			_ = exec.Command("sudo", "docker", "rm", "-f", project+"-core").Run()
			_ = exec.Command("sudo", "docker", "network", "rm", project+"_default").Run()
			_ = exec.Command("sudo", "docker", "volume", "rm", project+"_appsecrets").Run()
		}
	})
	if out, err := exec.Command("sudo", "docker", "stop", "firma-core").CombinedOutput(); err != nil {
		t.Fatalf("stop firma: %v %s", err, out)
	}
	out, err := exec.Command("sudo", "docker", "inspect", "-f", "{{.State.Running}}", "firmb-core").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Fatalf("other firm's core was affected: %v %s", err, out)
	}
	out, err = exec.Command("sudo", "docker", "inspect", "-f", "{{.State.Running}}", "firma-core").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "false" {
		t.Fatalf("this installation's core was not stopped: %v %s", err, out)
	}
}

func TestMigratorUnconfigured(t *testing.T) {
	_, err := (DockerMigrator{}).Apply(context.Background(), "image")
	code, _ := PublicCode(err)
	if err == nil || code != "not_configured" {
		t.Fatalf("%v", err)
	}
}

// TestCoreAndMigratorSecretMounts (PRD section 6, AC-006, AC-024): a replacement core
// mounts the core's secrets read-only and never the superuser volume; only the update
// migrator gets the superuser volume, read-only.
func TestCoreAndMigratorSecretMounts(t *testing.T) {
	v := CoreVolumes{AppSecrets: "p_appsecrets", AdminSecrets: "p_adminsecrets", BackupKey: "p_backupkey", Data: "p_core_data"}
	core := strings.Join(CoreBinds(v), " ")
	if core != "p_appsecrets:/appsecrets:ro p_backupkey:/backupkey p_core_data:/var/lib/togavsu-core" {
		t.Fatalf("core binds %s", core)
	}
	if strings.Contains(core, "adminsecrets") {
		t.Fatalf("core mounts the superuser volume: %s", core)
	}
	if got := strings.Join(MigrateBinds(v), " "); got != "p_appsecrets:/appsecrets:ro p_adminsecrets:/adminsecrets:ro" {
		t.Fatalf("migrate binds %s", got)
	}
	var created []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/create", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		created = append(created, body)
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "c" + strconv.Itoa(len(created))})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rt := &DockerRuntime{Client: srv.Client(), BaseURL: srv.URL, Network: "p_default", Volumes: v}
	if _, err := rt.CreateCore(context.Background(), testReleaseImage, "togavsu-core-next"); err != nil {
		t.Fatal(err)
	}
	if _, err := (DockerMigrator{Docker: rt}).createOrFind(context.Background(), testReleaseImage, "togavsu-migrate-x"); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d containers", len(created))
	}
	binds := func(body map[string]any) string {
		raw, _ := json.Marshal(body["HostConfig"].(map[string]any)["Binds"])
		return string(raw)
	}
	if got := binds(created[0]); strings.Contains(got, "adminsecrets") || !strings.Contains(got, "p_appsecrets:/appsecrets:ro") {
		t.Fatalf("replacement core binds %s", got)
	}
	if got := binds(created[1]); !strings.Contains(got, "p_adminsecrets:/adminsecrets:ro") {
		t.Fatalf("migrator binds %s", got)
	}
}

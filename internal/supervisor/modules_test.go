package supervisor

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testModuleDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

// fakeEngine is a minimal Docker engine for module containers: image inspect and pull,
// create, archive upload, list by label, start, stop and remove.
type fakeEngine struct {
	mu         sync.Mutex
	images     map[string]bool
	pulled     []string
	containers map[string]*fakeContainer
	order      []string
	failPut    bool
	next       int
}

type fakeContainer struct {
	id, name string
	body     map[string]any
	labels   map[string]string
	files    map[string]*tar.Header
	data     map[string]string
	state    string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{images: map[string]bool{}, containers: map[string]*fakeContainer{}}
}

func (e *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/v1.43")
	e.order = append(e.order, r.Method+" "+p)
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/images/") && strings.HasSuffix(p, "/json"):
		ref := strings.TrimSuffix(strings.TrimPrefix(p, "/images/"), "/json")
		if !e.images[ref] {
			http.Error(w, "no such image", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	case r.Method == http.MethodPost && p == "/images/create":
		ref := r.URL.Query().Get("fromImage")
		e.pulled = append(e.pulled, ref)
		e.images[ref] = true
		_, _ = w.Write([]byte("{\"status\":\"Status: downloaded\"}\n"))
	case r.Method == http.MethodPost && p == "/containers/create":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		name := r.URL.Query().Get("name")
		for _, c := range e.containers {
			if c.name == name {
				http.Error(w, "name in use", http.StatusConflict)
				return
			}
		}
		e.next++
		id := "c" + string(rune('0'+e.next))
		labels := map[string]string{}
		if raw, ok := body["Labels"].(map[string]any); ok {
			for k, v := range raw {
				labels[k], _ = v.(string)
			}
		}
		e.containers[id] = &fakeContainer{id: id, name: name, body: body, labels: labels, files: map[string]*tar.Header{}, data: map[string]string{}, state: "created"}
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": id})
	case r.Method == http.MethodGet && p == "/containers/json":
		var filters map[string][]string
		_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters)
		var out []map[string]any
		for _, c := range e.containers {
			match := true
			for _, l := range filters["label"] {
				k, v, _ := strings.Cut(l, "=")
				if c.labels[k] != v {
					match = false
				}
			}
			if match {
				out = append(out, map[string]any{"Id": c.id, "Names": []string{"/" + c.name}, "Labels": c.labels, "State": c.state,
					"NetworkSettings": map[string]any{"Networks": map[string]any{"togavsu_default": map[string]any{}}}})
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(p, "/containers/"):
		rest := strings.TrimPrefix(p, "/containers/")
		id, action, _ := strings.Cut(rest, "/")
		c := e.containers[id]
		if c == nil {
			http.Error(w, "no such container", http.StatusNotFound)
			return
		}
		switch {
		case r.Method == http.MethodDelete && action == "":
			delete(e.containers, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPut && action == "archive":
			if e.failPut {
				http.Error(w, "read-only", http.StatusInternalServerError)
				return
			}
			tr := tar.NewReader(r.Body)
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					http.Error(w, err.Error(), 400)
					return
				}
				raw, _ := io.ReadAll(tr)
				c.files[r.URL.Query().Get("path")+h.Name] = h
				c.data[r.URL.Query().Get("path")+h.Name] = string(raw)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && action == "start":
			if c.state == "running" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			c.state = "running"
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && action == "stop":
			if c.state != "running" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			c.state = "exited"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "unexpected "+r.Method+" "+p, http.StatusMethodNotAllowed)
	}
}

func (e *fakeEngine) only(t *testing.T, module string) *fakeContainer {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	var found []*fakeContainer
	for _, c := range e.containers {
		if c.labels["togavsu.module"] == module {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("module %s has %d containers", module, len(found))
	}
	return found[0]
}

func moduleAPI(t *testing.T, engine *fakeEngine) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	docker := httptest.NewServer(engine)
	t.Cleanup(docker.Close)
	rt := &DockerRuntime{Client: docker.Client(), BaseURL: docker.URL, Network: "togavsu_default"}
	var logs bytes.Buffer
	log := slog.New(ScrubHandler{Next: slog.NewTextHandler(&logs, nil)})
	srv := httptest.NewServer(NewMux("local-token", &Operations{Runtime: rt}, log))
	t.Cleanup(srv.Close)
	return srv, &logs
}

func clockDeployment(token string) string {
	raw, _ := json.Marshal(ModuleDeployment{Module: "matter-clock", Version: "1.0.0", Image: "ghcr.io/example/matter-clock", Digest: testModuleDigest,
		Port: 8080, Secrets: map[string]string{"togavsu_api_token": token, "togavsu_db_password": "synthetic-db-pass", "togavsu_db_role": "togavsu_mod_matter_clock"}})
	return string(raw)
}

func TestModuleDeployCreatesPinnedContainerWithSecretFiles(t *testing.T) {
	engine := newFakeEngine()
	srv, logs := moduleAPI(t, engine)

	res, raw := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", clockDeployment("tok-synthetic-one"))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("put %d %s", res.StatusCode, raw)
	}
	ref := "ghcr.io/example/matter-clock@" + testModuleDigest
	if len(engine.pulled) != 1 || engine.pulled[0] != ref {
		t.Fatalf("pulled %v", engine.pulled)
	}
	c := engine.only(t, "matter-clock")
	if c.name != "togavsu-mod-matter-clock" || c.state != "created" {
		t.Fatalf("container %s %s", c.name, c.state)
	}
	body, _ := json.Marshal(c.body)
	for _, want := range []string{`"Image":"` + ref + `"`, `"User":"65532:65532"`, `"Aliases":["togavsu-mod-matter-clock"]`,
		`"NetworkMode":"togavsu_default"`, `"togavsu.role":"module"`, `"togavsu.module.version":"1.0.0"`, `"CapDrop":["ALL"]`, `"8080/tcp"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("create body lacks %s: %s", want, body)
		}
	}
	if strings.Contains(string(body), "tok-synthetic-one") || strings.Contains(string(body), "synthetic-db-pass") {
		t.Fatalf("secret in container spec: %s", body)
	}
	h := c.files["/run/secrets/togavsu_api_token"]
	if h == nil || h.Mode != 0o400 || h.Uid != 65532 || c.data["/run/secrets/togavsu_api_token"] != "tok-synthetic-one" {
		t.Fatalf("token file %+v %q", h, c.data["/run/secrets/togavsu_api_token"])
	}
	if c.data["/run/secrets/togavsu_db_role"] != "togavsu_mod_matter_clock" {
		t.Fatalf("files %v", c.data)
	}
	if strings.Contains(logs.String(), "tok-synthetic-one") || strings.Contains(string(raw), "tok-synthetic-one") {
		t.Fatal("secret leaked to log or response")
	}

	// Start, stop twice (idempotent), redeploy with a rotated token: one container, new secret.
	for _, step := range []string{"start", "start", "stop", "stop"} {
		if res, raw := authJSON(t, srv.URL, http.MethodPost, "/v1/modules/matter-clock/"+step, ""); res.StatusCode != http.StatusOK {
			t.Fatalf("%s %d %s", step, res.StatusCode, raw)
		}
	}
	if res, raw := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", clockDeployment("tok-synthetic-two")); res.StatusCode != http.StatusOK {
		t.Fatalf("redeploy %d %s", res.StatusCode, raw)
	}
	c = engine.only(t, "matter-clock")
	if c.data["/run/secrets/togavsu_api_token"] != "tok-synthetic-two" {
		t.Fatal("redeploy kept the old token")
	}
	if len(engine.pulled) != 1 {
		t.Fatalf("pulled a present image again: %v", engine.pulled)
	}

	if res, raw := authJSON(t, srv.URL, http.MethodDelete, "/v1/modules/matter-clock", ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete %d %s", res.StatusCode, raw)
	}
	if len(engine.containers) != 0 {
		t.Fatalf("containers left %d", len(engine.containers))
	}
	if res, _ := authJSON(t, srv.URL, http.MethodDelete, "/v1/modules/matter-clock", ""); res.StatusCode != http.StatusNoContent {
		t.Fatalf("second delete %d", res.StatusCode)
	}
	if res, _ := authJSON(t, srv.URL, http.MethodPost, "/v1/modules/matter-clock/start", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("start after delete %d", res.StatusCode)
	}
}

func TestModuleDeployRejectsUnsafeRequests(t *testing.T) {
	engine := newFakeEngine()
	srv, _ := moduleAPI(t, engine)
	valid := ModuleDeployment{Module: "matter-clock", Version: "1.0.0", Image: "ghcr.io/example/matter-clock", Digest: testModuleDigest, Port: 8080,
		Secrets: map[string]string{"togavsu_api_token": "x"}}
	cases := map[string]func(d *ModuleDeployment){
		"name mismatch":   func(d *ModuleDeployment) { d.Module = "other" },
		"tag only":        func(d *ModuleDeployment) { d.Digest = "latest" },
		"short digest":    func(d *ModuleDeployment) { d.Digest = "sha256:abc" },
		"digest in image": func(d *ModuleDeployment) { d.Image = "ghcr.io/x@" + testModuleDigest },
		"no port":         func(d *ModuleDeployment) { d.Port = 0 },
		"path secret":     func(d *ModuleDeployment) { d.Secrets = map[string]string{"../etc/passwd": "x"} },
		"slash secret":    func(d *ModuleDeployment) { d.Secrets = map[string]string{"a/b": "x"} },
		"huge secret": func(d *ModuleDeployment) {
			d.Secrets = map[string]string{"big": strings.Repeat("x", maxModuleSecretLen+1)}
		},
	}
	for name, mutate := range cases {
		d := valid
		mutate(&d)
		raw, _ := json.Marshal(d)
		if res, body := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", string(raw)); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", name, res.StatusCode, body)
		}
	}
	if res, _ := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/Bad_Name", `{"module":"Bad_Name"}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad name %d", res.StatusCode)
	}
	if res, _ := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", `{"module":"matter-clock","env":["X=1"]}`); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field %d", res.StatusCode)
	}
	if len(engine.order) != 0 {
		t.Fatalf("rejected requests reached docker: %v", engine.order)
	}
	// Unauthenticated callers never reach the handler.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/modules/matter-clock", strings.NewReader(clockDeployment("t")))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || len(engine.containers) != 0 {
		t.Fatalf("unauthenticated put %d", res.StatusCode)
	}
}

func TestModuleDeploySecretWriteFailureLeavesNoContainer(t *testing.T) {
	engine := newFakeEngine()
	engine.failPut = true
	srv, _ := moduleAPI(t, engine)
	res, raw := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", clockDeployment("tok"))
	if res.StatusCode < 500 {
		t.Fatalf("put %d %s", res.StatusCode, raw)
	}
	if len(engine.containers) != 0 {
		t.Fatal("a container without its secrets was left behind")
	}
}

func TestModuleRoutesNeedAModuleRuntime(t *testing.T) {
	srv := httptest.NewServer(NewMux("local-token", &Operations{}, nil))
	defer srv.Close()
	if res, _ := authJSON(t, srv.URL, http.MethodPut, "/v1/modules/matter-clock", clockDeployment("t")); res.StatusCode != http.StatusNotFound {
		t.Fatalf("put %d", res.StatusCode)
	}
	if res, _ := authJSON(t, srv.URL, http.MethodDelete, "/v1/modules/matter-clock", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("delete %d", res.StatusCode)
	}
}

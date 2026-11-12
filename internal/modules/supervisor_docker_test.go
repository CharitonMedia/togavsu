package modules_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

// TestModuleContainerOnDocker is the shipped module path end to end on a real Docker
// engine: the core's supervisor client (modules.Supervisor, what core.Open wires in the
// Compose stack) calls the real supervisor API (supervisor.NewMux), whose DockerRuntime
// pulls the Matter Clock image by digest, creates togavsu-mod-matter-clock with the
// secrets as files, and starts, stops, replaces and removes it. The sample module exits
// at start if it cannot read its secret files, so a /healthz answer over the stack
// network alias proves the files are there and readable by the module user.
// scripts/ci/test-module-container.sh builds and publishes the image and sets the env.
func TestModuleContainerOnDocker(t *testing.T) {
	image, digest, network := os.Getenv("TOGAVSU_MODULE_IT_IMAGE"), os.Getenv("TOGAVSU_MODULE_IT_DIGEST"), os.Getenv("TOGAVSU_MODULE_IT_NETWORK")
	if image == "" || digest == "" || network == "" {
		t.Skip("TOGAVSU_MODULE_IT_IMAGE, _DIGEST and _NETWORK are unset; run scripts/ci/test-module-container.sh")
	}
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	rt := &supervisor.DockerRuntime{Client: supervisor.DockerSocketClient(socket), Network: network}
	sup := httptest.NewServer(supervisor.NewMux("synthetic-supervisor-token", &supervisor.Operations{Runtime: rt}, nil))
	defer sup.Close()
	client := &modules.Supervisor{URL: sup.URL, Secret: "synthetic-supervisor-token", ModuleURL: "http://togavsu-mod-%s:%d", Ports: func(string) int { return 8080 }}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const name = "togavsu-mod-matter-clock"
	t.Cleanup(func() { _ = client.Remove(context.Background(), "matter-clock") })

	dep := func(token string) modules.Deployment {
		return modules.Deployment{Module: "matter-clock", Version: "1.0.0", Image: image, Digest: digest, Port: 8080,
			Secrets: map[string]string{"togavsu_api_token": token, "togavsu_webhook_secret": "synthetic-webhook", "togavsu_db_role": "mod_matter_clock_it",
				"togavsu_db_password": "synthetic-db-password", "togavsu_db_schema": "mod_matter_clock"}}
	}

	// Refusal first: an unknown digest creates nothing.
	bad := dep("synthetic-token-0")
	bad.Digest = "sha256:" + strings.Repeat("0", 64)
	if err := client.Deploy(ctx, bad); err == nil {
		t.Fatal("deployed an image digest the registry does not have")
	}
	if out := docker(t, "ps", "-aq", "--filter", "label=togavsu.module=matter-clock"); out != "" {
		t.Fatalf("failed deploy left a container: %s", out)
	}

	if err := client.Deploy(ctx, dep("synthetic-token-1")); err != nil {
		t.Fatal(err)
	}
	if got := client.BaseURL("matter-clock"); got != "http://"+name+":8080" {
		t.Fatal(got)
	}
	probe(t, network, client.BaseURL("matter-clock")+"/healthz")
	spec := docker(t, "inspect", "--format", "{{.Config.Image}} {{.Config.User}} {{json .Config.Env}} {{json .NetworkSettings.Networks}}", name)
	for _, want := range []string{image + "@" + digest, "65532:65532", name} {
		if !strings.Contains(spec, want) {
			t.Fatalf("container spec lacks %s: %s", want, spec)
		}
	}
	if strings.Contains(spec, "synthetic-token-1") || strings.Contains(spec, "synthetic-db-password") {
		t.Fatalf("secret in the container spec: %s", spec)
	}
	if got := docker(t, "cp", name+":/run/secrets/togavsu_api_token", "-"); !strings.Contains(got, "synthetic-token-1") {
		t.Fatalf("token file: %q", got)
	}

	// A token rotation or update replaces the container: one container, new secret, running.
	if err := client.Deploy(ctx, dep("synthetic-token-2")); err != nil {
		t.Fatal(err)
	}
	if ids := strings.Fields(docker(t, "ps", "-aq", "--filter", "label=togavsu.module=matter-clock")); len(ids) != 1 {
		t.Fatalf("containers after redeploy: %v", ids)
	}
	if got := docker(t, "cp", name+":/run/secrets/togavsu_api_token", "-"); !strings.Contains(got, "synthetic-token-2") {
		t.Fatal("redeploy kept the old token")
	}
	probe(t, network, client.BaseURL("matter-clock")+"/healthz")

	if err := client.Stop(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	if st := docker(t, "inspect", "--format", "{{.State.Running}}", name); st != "false" {
		t.Fatalf("running after stop: %s", st)
	}
	if err := client.Stop(ctx, "matter-clock"); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if err := client.Remove(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	if out := docker(t, "ps", "-aq", "--filter", "label=togavsu.module=matter-clock"); out != "" {
		t.Fatalf("container left after remove: %s", out)
	}
	if err := client.Remove(ctx, "matter-clock"); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil && args[0] != "ps" {
		t.Fatalf("docker %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// probe fetches target from a throwaway container on the stack network, as the core would.
func probe(t *testing.T, network, target string) {
	t.Helper()
	var out []byte
	var err error
	for i := 0; i < 30; i++ {
		out, err = exec.Command("docker", "run", "--rm", "--network", network, "busybox:1.36", "wget", "-qO-", "-T", "3", target).CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "ok" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("module did not answer %s: %v %s", target, err, out)
}

// recordingModules is a supervisor runtime that records module calls; the embedded
// interface is nil because the core update methods are not exercised here.
type recordingModules struct {
	supervisor.Runtime
	calls []string
	dep   supervisor.ModuleDeployment
}

func (r *recordingModules) DeployModule(_ context.Context, d supervisor.ModuleDeployment) error {
	r.calls, r.dep = append(r.calls, "deploy "+d.Module), d
	return nil
}
func (r *recordingModules) RemoveModule(_ context.Context, name string) error {
	r.calls = append(r.calls, "remove "+name)
	return nil
}
func (r *recordingModules) StartModule(_ context.Context, name string) error {
	r.calls = append(r.calls, "start "+name)
	return nil
}
func (r *recordingModules) StopModule(_ context.Context, name string) error {
	r.calls = append(r.calls, "stop "+name)
	return nil
}

// TestSupervisorClientMatchesSupervisorAPI runs the core's client against the real
// supervisor routes, so a route or body the supervisor does not serve fails here.
func TestSupervisorClientMatchesSupervisorAPI(t *testing.T) {
	rec := &recordingModules{}
	sup := httptest.NewServer(supervisor.NewMux("synthetic-supervisor-token", &supervisor.Operations{Runtime: rec}, nil))
	defer sup.Close()
	client := &modules.Supervisor{URL: sup.URL, Secret: "synthetic-supervisor-token"}
	ctx := context.Background()
	d := modules.Deployment{Module: "matter-clock", Version: "1.0.0", Image: "ghcr.io/example/matter-clock", Digest: "sha256:" + strings.Repeat("a", 64),
		Port: 8080, Secrets: map[string]string{"togavsu_api_token": "synthetic", "togavsu_db_schema": "mod_matter_clock"}}
	if err := client.Deploy(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	if err := client.Remove(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.calls, ","); got != "deploy matter-clock,start matter-clock,stop matter-clock,remove matter-clock" {
		t.Fatal(got)
	}
	if rec.dep.Image != d.Image || rec.dep.Digest != d.Digest || rec.dep.Port != 8080 || rec.dep.Secrets["togavsu_api_token"] != "synthetic" {
		t.Fatalf("deployment %+v", rec.dep)
	}
	// A tag instead of a digest is refused before the runtime.
	d.Digest = "latest"
	if err := client.Deploy(ctx, d); err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("unpinned image: %v", err)
	}
	if len(rec.calls) != 4 {
		t.Fatalf("refused deploy reached the runtime: %v", rec.calls)
	}
	wrong := &modules.Supervisor{URL: sup.URL, Secret: "wrong"}
	if err := wrong.Remove(ctx, "matter-clock"); err == nil {
		t.Fatal("wrong supervisor secret accepted")
	}
}

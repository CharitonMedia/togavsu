package editor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// fakeCLI writes a shell script that stands in for wordcraft-cli, so the sandbox itself
// (folder, environment, limits, process group) is tested without LawCraft.
func fakeCLI(t *testing.T, body string) *CLI {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wordcraft-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &CLI{Path: path, TempDir: t.TempDir()}
}

// The script records what it saw, then writes the output the job asks for.
const recordAndWrite = `out=""; for a in "$@"; do out="$a"; done
{ echo "pwd=$(pwd)"; env; echo "ulimit=$(ulimit -v)"; } > "$TOGAVSU_RECORD"
cp "$TOGAVSU_RECORD" "$out"
`

func TestSandboxGivesAFreshFolderAndNoCoreEnvironment(t *testing.T) {
	t.Setenv("TOGAVSU_DATABASE_URL", "postgres://synthetic-secret@db/core")
	record := filepath.Join(t.TempDir(), "record")
	// The script cannot see TOGAVSU_RECORD (the sandbox drops the environment), so it is
	// passed through the script text instead.
	cli := fakeCLI(t, strings.ReplaceAll(recordAndWrite, "$TOGAVSU_RECORD", record))
	cli.Limits = func(context.Context) (Limits, error) {
		return Limits{Concurrent: 1, Timeout: 10 * time.Second, MemoryBytes: 512 << 20}, nil
	}
	out, err := cli.Convert(context.Background(), []byte("Synthetic"), "txt", "pdf")
	if err != nil {
		t.Fatal(err)
	}
	seen := string(out)
	if strings.Contains(seen, "synthetic-secret") || strings.Contains(seen, "TOGAVSU_DATABASE_URL") {
		t.Fatalf("the run saw the core's environment:\n%s", seen)
	}
	for _, want := range []string{"HTTPS_PROXY=" + closedProxy, "http_proxy=" + closedProxy, "WORDCRAFT_LAW=1", "ulimit=524288"} {
		if !strings.Contains(seen, want) {
			t.Errorf("the run's environment lacks %s:\n%s", want, seen)
		}
	}
	var dir string
	for _, line := range strings.Split(seen, "\n") {
		if v, ok := strings.CutPrefix(line, "pwd="); ok {
			dir = v
		}
	}
	if !strings.Contains(seen, "HOME="+dir) || !strings.HasPrefix(filepath.Base(dir), "editor-run-") {
		t.Fatalf("the run did not work in its own folder: pwd=%s\n%s", dir, seen)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the run folder %s was not deleted: %v", dir, err)
	}
}

func TestSandboxStopsARunAtItsTimeLimit(t *testing.T) {
	cli := fakeCLI(t, "sleep 30 & sleep 30\n")
	cli.Limits = func(context.Context) (Limits, error) {
		return Limits{Concurrent: 1, Timeout: 300 * time.Millisecond}, nil
	}
	start := time.Now()
	_, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf")
	if err == nil || !strings.Contains(err.Error(), "took longer than") {
		t.Fatalf("Convert = %v, want the time limit", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the run was not stopped promptly: %s", time.Since(start))
	}
}

func TestSandboxEnforcesTheMemoryLimit(t *testing.T) {
	// Allocating 64 MiB under a 16 MiB address-space limit fails.
	cli := fakeCLI(t, `x=$(head -c 67108864 /dev/zero | tr '\0' a) || exit 3; for a in "$@"; do out="$a"; done; echo ok > "$out"`+"\n")
	cli.Limits = func(context.Context) (Limits, error) {
		return Limits{Concurrent: 1, Timeout: 10 * time.Second, MemoryBytes: 16 << 20}, nil
	}
	if _, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf"); err == nil {
		t.Fatal("a run over its memory limit finished")
	}
	cli.Limits = func(context.Context) (Limits, error) {
		return Limits{Concurrent: 1, Timeout: 10 * time.Second, MemoryBytes: 1 << 30}, nil
	}
	if _, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf"); err != nil {
		t.Fatalf("the same run under 1 GiB failed: %v", err)
	}
}

func TestSandboxRunsAtMostTheConfiguredNumberAtOnce(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	cli := fakeCLI(t, `while [ ! -e "`+gate+`" ]; do sleep 0.05; done; for a in "$@"; do out="$a"; done; echo ok > "$out"`+"\n")
	cli.Limits = func(context.Context) (Limits, error) { return Limits{Concurrent: 2, Timeout: 20 * time.Second}, nil }
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = cli.Convert(context.Background(), []byte("x"), "txt", "pdf")
		}(i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cli.mu.Lock()
		n := cli.running
		cli.mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf")
	var api *domain.APIError
	if !errors.As(err, &api) || api.Code != domain.CodeRateLimited {
		t.Fatalf("a third run = %v, want rate_limited", err)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("a run within the limit failed: %v", err)
		}
	}
	if _, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf"); err != nil {
		t.Fatalf("a run after the others finished: %v", err)
	}
}

func TestSandboxNetworkIsolationIsReported(t *testing.T) {
	cli := fakeCLI(t, "exit 0\n")
	cli.RequireNetworkNamespace = true
	if cli.NetworkIsolated() {
		// The host allows namespaces: the run has no interface but a down loopback.
		probe := fakeCLI(t, `for a in "$@"; do out="$a"; done; cat /proc/net/dev > "$out"`+"\n")
		probe.RequireNetworkNamespace = true
		out, err := probe.Convert(context.Background(), []byte("x"), "txt", "pdf")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n")[2:] {
			if f := strings.TrimSpace(line); f != "" && !strings.HasPrefix(f, "lo:") {
				t.Fatalf("the run sees a network interface: %s", f)
			}
		}
		return
	}
	if _, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf"); err == nil || !strings.Contains(err.Error(), "network namespace") {
		t.Fatalf("with namespaces required and unavailable, Convert = %v", err)
	}
}

func TestSandboxErrorsDoNotNameTheRunFolder(t *testing.T) {
	cli := fakeCLI(t, `echo "wordcraft-cli: cannot read $(pwd)/document.txt: synthetic failure" >&2; exit 1`+"\n")
	_, err := cli.Convert(context.Background(), []byte("x"), "txt", "pdf")
	if err == nil || strings.Contains(err.Error(), cli.TempDir) || !strings.Contains(err.Error(), "synthetic failure") {
		t.Fatalf("Convert = %v", err)
	}
}

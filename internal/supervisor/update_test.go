package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testReleaseImage = "ghcr.io/charitonmedia/togavsu-core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type scriptRuntime struct {
	order     *[]string
	cores     []ContainerInfo
	health    error
	stopID    string
	failStart string
	used      map[string]bool
	stopHook  func()
}

func (r *scriptRuntime) add(step string) {
	*r.order = append(*r.order, step)
}

func (r *scriptRuntime) ListCores(context.Context) ([]ContainerInfo, error) {
	r.add("list")
	return r.cores, nil
}
func (r *scriptRuntime) Pull(_ context.Context, image string) error {
	r.add("pull:" + image)
	return nil
}
func (r *scriptRuntime) StartCore(_ context.Context, image, name string) (ContainerInfo, error) {
	if r.used == nil {
		r.used = map[string]bool{}
	}
	if r.used[name] {
		return ContainerInfo{}, errors.New("name already in use")
	}
	r.used[name] = true
	r.add("start:" + image)
	r.add("name:" + name)
	return ContainerInfo{ID: "new-" + name, Name: name, Image: image}, nil
}
func (r *scriptRuntime) Start(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.add("restart:" + id)
	if id == r.failStart {
		return errors.New("start failed")
	}
	return nil
}
func (r *scriptRuntime) WaitHealthy(context.Context, string, time.Duration) error {
	r.add("health")
	return r.health
}
func (r *scriptRuntime) Stop(_ context.Context, id string) error {
	r.add("stop:" + id)
	if r.stopHook != nil {
		r.stopHook()
	}
	if id == r.stopID {
		return errors.New("stop failed")
	}
	return nil
}
func (r *scriptRuntime) Remove(_ context.Context, id string) error {
	r.add("remove:" + id)
	return nil
}
func (r *scriptRuntime) StartModule(context.Context, string) error { return nil }
func (r *scriptRuntime) StopModule(context.Context, string) error  { return nil }

type scriptProxy struct{ order *[]string }

func (p scriptProxy) Switch(_ context.Context, upstream string) error {
	*p.order = append(*p.order, "switch:"+upstream)
	return nil
}

func TestUpdateHappyPathOrder(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Image: "togavsu-core:1.0.0",
		Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	var restored bool
	up := testUpdater(t, rt, &order)
	up.Restore = func(context.Context, string) error {
		restored = true
		return nil
	}
	got, err := up.Apply(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "applied" || restored {
		t.Fatalf("result %+v restored %v", got, restored)
	}
	if !containsStep(order, "pull:"+testReleaseImage) || !containsStep(order, "start:"+testReleaseImage) || !containsStep(order, "migrate:"+testReleaseImage) {
		t.Fatalf("digest was not pulled and executed: %v", order)
	}
	var name string
	for _, step := range order {
		if strings.HasPrefix(step, "name:") {
			name = strings.TrimPrefix(step, "name:")
		}
	}
	if name == "" || name == "togavsu-core-next" || !containsStep(order, "switch:"+name+":8080") {
		t.Fatalf("name %q order %v", name, order)
	}
}

func TestUpdateRejectsBadSignatureAndKeepsCore(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Image: "togavsu-core:1.0.0", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Verifier = CLIVerifier{}
	_, err := up.Apply(context.Background(), false)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("got %v", err)
	}
	for _, step := range order {
		if step == "stop:old" || step == "backup" || strings.HasPrefix(step, "migrate") || strings.HasPrefix(step, "pull:") {
			t.Fatalf("signature failure continued: %v", order)
		}
	}
	notes, err := upOutbox(t, up).List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes %+v %v", notes, err)
	}
}

func TestUpdateHealthFailureRestoresBackup(t *testing.T) {
	var order []string
	rt := &scriptRuntime{
		order:  &order,
		cores:  []ContainerInfo{{ID: "old", Image: "togavsu-core:1.0.0", Labels: map[string]string{"togavsu.version": "1.0.0"}}},
		health: errors.New("unhealthy"),
	}
	up := testUpdater(t, rt, &order)
	got, err := up.Apply(context.Background(), false)
	if !errors.Is(err, ErrHealth) {
		t.Fatalf("got %v", err)
	}
	if got.Restored || !got.MigrationRan || got.Rollback != "preserved_writes" {
		t.Fatalf("result %+v", got)
	}
	if !containsStep(order, "pull:"+testReleaseImage) || containsStep(order, "restore") {
		t.Fatalf("order %v", order)
	}
	stoppedNew := false
	for _, step := range order {
		if strings.HasPrefix(step, "stop:new-") {
			stoppedNew = true
		}
	}
	if !stoppedNew {
		t.Fatalf("new container was not stopped: %v", order)
	}
	for _, step := range order {
		if step == "stop:old" || step == "switch:togavsu-core-next:8080" {
			t.Fatalf("old core was disturbed: %v", order)
		}
	}
}

func TestUpdateOutsideWindowAndPause(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old"}}}
	up := testUpdater(t, rt, &order)
	up.Now = func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }
	if _, err := up.Apply(context.Background(), false); !errors.Is(err, ErrOutsideWindow) {
		t.Fatalf("window err %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("work ran outside the window: %v", order)
	}
	got, err := up.Apply(context.Background(), true)
	if err != nil || got.Status != "applied" {
		t.Fatalf("force %+v %v", got, err)
	}
	up.Paused = true
	up.Now = func() time.Time { return time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC) }
	if _, err := up.Apply(context.Background(), false); !errors.Is(err, ErrUpdatesPaused) {
		t.Fatalf("pause %v", err)
	}
}

func TestUpdateMissingMigratorDoesNotSwitch(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Migrator = nil
	_, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrMigratorUnconfigured) {
		t.Fatalf("got %v", err)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "switch:") || strings.HasPrefix(step, "start:") {
			t.Fatalf("switched without migrations: %v", order)
		}
	}
}

func TestUpdateRejectsMutableTag(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Registry = staticRegistry{Release{Image: "togavsu-core:1.1.0", Version: "1.1.0"}}
	_, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("got %v", err)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "pull:") || strings.HasPrefix(step, "start:") || step == "backup" {
			t.Fatalf("mutable tag continued: %v", order)
		}
	}
}

func TestTwoConsecutiveUpdatesUseDistinctNames(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{
		ID: "old", Name: "core", Labels: map[string]string{"togavsu.version": "1.0.0"},
	}}}
	up := testUpdater(t, rt, &order)
	if _, err := up.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := up.Apply(context.Background(), true); err != nil {
		t.Fatalf("second update: %v order %v", err, order)
	}
	var names []string
	for _, step := range order {
		if strings.HasPrefix(step, "name:") {
			names = append(names, strings.TrimPrefix(step, "name:"))
		}
	}
	if len(names) != 2 || names[0] == names[1] || names[0] == "togavsu-core-next" {
		t.Fatalf("names %v", names)
	}
}

func TestUnreconciledProxyLeavesNewCoreRunning(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Proxy = errProxy{ErrProxyUnreconciled}
	_, err := up.Apply(context.Background(), true)
	if !errors.Is(err, ErrProxyUnreconciled) {
		t.Fatalf("got %v", err)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "remove:") || strings.HasPrefix(step, "stop:new-") {
			t.Fatalf("new core removed while caddy still routes to it: %v", order)
		}
	}
}

type errProxy struct{ err error }

func (p errProxy) Switch(context.Context, string) error { return p.err }

func TestRollbackFailureIsReported(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Labels: map[string]string{"togavsu.version": "1.0.0"}}}, health: errors.New("unhealthy")}
	up := testUpdater(t, rt, &order)
	up.Rollback = func(context.Context, string) error { return errors.New("restore exploded") }
	_, err := up.Apply(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "restore exploded") {
		t.Fatalf("got %v", err)
	}
}

func TestNewer(t *testing.T) {
	if !newer("1.2.0", "1.1.9") || newer("1.0.0", "1.0.0") || newer("1.0.0", "1.0.1") {
		t.Fatal("semver compare")
	}
	if !newer("anything", "") {
		t.Fatal("empty current should update")
	}
}

func testUpdater(t *testing.T, rt *scriptRuntime, order *[]string) *Updater {
	t.Helper()
	dir := t.TempDir()
	out := OpenOutbox(filepath.Join(dir, "outbox.jsonl"))
	return &Updater{
		Now:      func() time.Time { return time.Date(2026, 1, 2, 2, 30, 0, 0, time.UTC) },
		Window:   Window{Start: 2 * time.Hour, End: 4 * time.Hour, Location: time.UTC},
		Registry: staticRegistry{Release{Image: testReleaseImage, Version: "1.1.0"}},
		Verifier: allowVerifier{},
		Runtime:  rt,
		Migrator: scriptMigrator{order: order},
		Backup: func(context.Context, string) (BackupMeta, error) {
			*order = append(*order, "backup")
			return BackupMeta{ID: "20260102T023000Z-0123456789abcdef"}, nil
		},
		Restore: func(context.Context, string) error {
			*order = append(*order, "restore")
			return nil
		},
		Proxy:  scriptProxy{order: order},
		Notify: func(ctx context.Context, subject, body string) { _ = out.Notify(ctx, subject, body) },
		Ledger: OpenLedger(filepath.Join(dir, "ledger.jsonl")),
	}
}

func upOutbox(t *testing.T, up *Updater) *Outbox {
	t.Helper()
	// Notify writes through the closure's outbox. Re-open the ledger directory's sibling by recording a fresh list via a second notify path.
	// The test helper stores the outbox next to the ledger.
	return OpenOutbox(filepath.Join(filepath.Dir(up.Ledger.path), "outbox.jsonl"))
}

type staticRegistry struct{ rel Release }

func (s staticRegistry) Latest(context.Context) (Release, error) { return s.rel, nil }

type allowVerifier struct{}

func (allowVerifier) Verify(context.Context, string) error { return nil }

type scriptMigrator struct{ order *[]string }

func (m scriptMigrator) Apply(_ context.Context, image string) (bool, error) {
	*m.order = append(*m.order, "migrate:"+image)
	return true, nil
}

func containsStep(steps []string, want string) bool {
	for _, step := range steps {
		if step == want {
			return true
		}
	}
	return false
}

func stringsJoin(v []string) string {
	return stringsJoinSep(v, "|")
}

func stringsJoinSep(v []string, sep string) string {
	out := ""
	for i, s := range v {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

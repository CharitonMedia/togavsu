package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testReleaseImage = "ghcr.io/charitonmedia/togavsu-core@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type scriptRuntime struct {
	order        *[]string
	cores        []ContainerInfo
	health       error
	stopID       string
	failStart    string
	used         map[string]bool
	stopHook     func()
	states       map[string]string
	inspectErr   map[string]error
	inspectAllow int
	inspectCount int
	unknownAfter int
	loseStop     map[string]bool
	cancelStops  bool
	onList       func()
	afterStart   func()
	trackStarted bool
	started      []ContainerInfo
	removed      map[string]bool
	loseCreate   bool
	failRemove   int
	failInspect  int
}

func (r *scriptRuntime) add(step string) {
	*r.order = append(*r.order, step)
}

func (r *scriptRuntime) ListCores(context.Context) ([]ContainerInfo, error) {
	r.add("list")
	var out []ContainerInfo
	for _, core := range r.started {
		if !r.removed[core.ID] {
			out = append(out, core)
		}
	}
	for _, core := range r.cores {
		if !r.removed[core.ID] {
			out = append(out, core)
		}
	}
	return out, nil
}

func (r *scriptRuntime) ListAllCores(context.Context) ([]ContainerInfo, error) {
	if r.onList != nil {
		r.onList()
	}
	r.add("list-all")
	out := make([]ContainerInfo, len(r.cores))
	copy(out, r.cores)
	for i := range out {
		if st, ok := r.states[out[i].ID]; ok && st != "" {
			out[i].State = st
		}
		if out[i].State == "" {
			out[i].State = "running"
		}
		out[i].Running = out[i].State == "running"
	}
	return out, nil
}

func (r *scriptRuntime) Inspect(_ context.Context, id string) (ContainerInfo, error) {
	if r.failInspect > 0 {
		r.failInspect--
		r.add("inspect-fail:" + id)
		return ContainerInfo{}, errors.New("inspect timeout")
	}
	if r.removed[id] {
		return ContainerInfo{}, ErrNotFound
	}
	for _, c := range r.started {
		if c.ID == id || c.Name == id {
			if r.removed[c.ID] {
				return ContainerInfo{}, ErrNotFound
			}
			return c, nil
		}
	}
	r.add("inspect:" + id)
	r.inspectCount++
	if r.unknownAfter > 0 && r.inspectCount > r.unknownAfter {
		return ContainerInfo{ID: id, State: "unknown"}, nil
	}
	if r.inspectAllow > 0 {
		r.inspectAllow--
	} else if err := r.inspectErr[id]; err != nil {
		return ContainerInfo{}, err
	}
	state := "running"
	if st, ok := r.states[id]; ok && st != "" {
		state = st
	}
	return ContainerInfo{ID: id, State: state, Running: state == "running"}, nil
}
func (r *scriptRuntime) Pull(_ context.Context, image string) error {
	r.add("pull:" + image)
	return nil
}
func (r *scriptRuntime) CreateCore(_ context.Context, image, name string) (ContainerInfo, error) {
	if r.used == nil {
		r.used = map[string]bool{}
	}
	if r.used[name] {
		return ContainerInfo{}, errors.New("name already in use")
	}
	r.used[name] = true
	r.add("start:" + image)
	r.add("name:" + name)
	info := ContainerInfo{ID: "new-" + name, Name: name, Image: image, Labels: map[string]string{"togavsu.version": "1.1.0"}, State: "running", Running: true}
	r.started = append(r.started, info)
	if r.loseCreate {
		return ContainerInfo{}, errors.New("lost create reply")
	}
	if r.afterStart != nil {
		r.afterStart()
	}
	return info, nil
}
func (r *scriptRuntime) StartCore(ctx context.Context, image, name string) (ContainerInfo, error) {
	return r.CreateCore(ctx, image, name)
}
func (r *scriptRuntime) Start(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.add("restart:" + id)
	if r.states == nil {
		r.states = map[string]string{}
	}
	if r.states[id] == "running" {
		return nil
	}
	if id == r.failStart {
		return errors.New("start failed")
	}
	r.states[id] = "running"
	return nil
}
func (r *scriptRuntime) WaitHealthy(ctx context.Context, _ string, _ time.Duration) error {
	r.add("health")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.health
}
func (r *scriptRuntime) Stop(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		r.add("stop-canceled:" + id)
		return ctx.Err()
	}
	r.add("stop:" + id)
	if r.states == nil {
		r.states = map[string]string{}
	}
	r.states[id] = "exited"
	if r.stopHook != nil {
		r.stopHook()
	}
	if r.loseStop[id] || id == r.stopID || r.cancelStops {
		if r.loseStop[id] {
			return errors.New("lost stop reply")
		}
		if r.cancelStops {
			return context.Canceled
		}
		return errors.New("stop failed")
	}
	return nil
}
func (r *scriptRuntime) Remove(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		r.add("remove-canceled:" + id)
		return ctx.Err()
	}
	if r.failRemove > 0 {
		r.failRemove--
		r.add("remove-fail:" + id)
		return errors.New("remove failed")
	}
	r.add("remove:" + id)
	if r.removed == nil {
		r.removed = map[string]bool{}
	}
	r.removed[id] = true
	return nil
}
func (r *scriptRuntime) StartModule(context.Context, string) error { return nil }
func (r *scriptRuntime) StopModule(context.Context, string) error  { return nil }

type scriptProxy struct {
	order   *[]string
	live    *string
	liveErr error
}

func (p *scriptProxy) Switch(_ context.Context, upstream string) error {
	*p.order = append(*p.order, "switch:"+upstream)
	if p.live != nil {
		*p.live = upstream
	}
	return nil
}

func (p *scriptProxy) ActiveUpstream(context.Context) (string, error) {
	if p.liveErr != nil {
		return "", p.liveErr
	}
	if p.live != nil && *p.live != "" {
		return *p.live, nil
	}
	return "core:8080", nil
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
	up.Verifier = CLIVerifier{KeyPath: "/appsecrets/release.pub", Run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("no matching signatures"), errors.New("exit status 1")
	}}
	_, err := up.Apply(context.Background(), false)
	if !errors.Is(err, ErrSignature) || errors.Is(err, ErrReleaseKeyUnconfigured) {
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

func TestUpdateWithoutReleaseKeyIsRefusedWithoutNotice(t *testing.T) {
	var order []string
	rt := &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "old", Image: "togavsu-core:1.0.0", Labels: map[string]string{"togavsu.version": "1.0.0"}}}}
	up := testUpdater(t, rt, &order)
	up.Verifier = CLIVerifier{}
	res, err := up.Apply(context.Background(), false)
	if !errors.Is(err, ErrReleaseKeyUnconfigured) || !errors.Is(err, ErrSignature) || res.Reason != "not_configured" {
		t.Fatalf("got %+v %v", res, err)
	}
	for _, step := range order {
		if step == "stop:old" || step == "backup" || strings.HasPrefix(step, "migrate") || strings.HasPrefix(step, "pull:") {
			t.Fatalf("unsigned update continued: %v", order)
		}
	}
	if notes, err := upOutbox(t, up).List(); err != nil || len(notes) != 0 {
		t.Fatalf("missing key mailed the admin: %+v %v", notes, err)
	}
	if evs, err := up.Ledger.List("update"); err != nil || len(evs) != 1 || evs[0].Fields["status"] != "refused" {
		t.Fatalf("refusal not recorded: %+v %v", evs, err)
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
	evs, err := up.Ledger.List("update")
	if err != nil || len(evs) != 1 || evs[0].Fields["status"] != "refused" || evs[0].Fields["reason"] != "health" || evs[0].Fields["image"] != testReleaseImage {
		t.Fatalf("update history does not record the health refusal: %+v %v", evs, err)
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
	if proxy, ok := up.Proxy.(*scriptProxy); ok && proxy.live != nil {
		*proxy.live = "core:8080"
	}
	rt.removed = nil
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

func TestCanceledHealthCleansUpOnALiveContext(t *testing.T) {
	var order []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &scriptRuntime{
		order:        &order,
		trackStarted: true,
		cores: []ContainerInfo{{
			ID: "old", Name: "core", Image: "togavsu-core:1.0.0",
			Labels: map[string]string{"togavsu.version": "1.0.0"}, State: "running", Running: true,
		}},
	}
	rt.afterStart = cancel
	up := testUpdater(t, rt, &order)
	got, err := up.Apply(ctx, true)
	if !errors.Is(err, ErrHealth) || got.Status != "refused" || got.Reason != "health" || got.Status == "up_to_date" {
		t.Fatalf("%+v %v", got, err)
	}
	var candidate string
	for _, step := range order {
		if strings.HasPrefix(step, "stop:new-") {
			candidate = strings.TrimPrefix(step, "stop:")
		}
		if strings.HasPrefix(step, "stop-canceled:") || strings.HasPrefix(step, "remove-canceled:") {
			t.Fatalf("cleanup used the canceled request: %v", order)
		}
	}
	if candidate == "" || !containsStep(order, "remove:"+candidate) {
		t.Fatalf("candidate was not removed: %v", order)
	}
	again, err := up.Apply(context.Background(), true)
	if err != nil || again.Status == "up_to_date" || again.From == testReleaseImage {
		t.Fatalf("aborted candidate was treated as the serving core: %+v %v order %v", again, err, order)
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

func (e errProxy) ActiveUpstream(context.Context) (string, error) {
	return "core:8080", nil
}

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
	for i := range rt.cores {
		if rt.cores[i].Name == "" {
			rt.cores[i].Name = "core"
		}
	}
	live := "core:8080"
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
		Proxy:  &scriptProxy{order: order, live: &live},
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

func TestCLIVerifierPassesAllowedFlagsBeforeImage(t *testing.T) {
	var got []string
	v := CLIVerifier{Cosign: "/usr/local/bin/cosign", KeyPath: "/appsecrets/cosign.pub",
		Flags: []string{"--insecure-ignore-tlog=true", "--allow-http-registry=true"},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			got = append([]string{name}, args...)
			return nil, nil
		}}
	img := "127.0.0.1:15000/core@sha256:" + strings.Repeat("a", 64)
	if err := v.Verify(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/local/bin/cosign", "verify", "--key", "/appsecrets/cosign.pub", "--insecure-ignore-tlog=true", "--allow-http-registry=true", img}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("args %v, want %v", got, want)
	}
	v.Run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Error: no signatures found"), errors.New("exit status 1")
	}
	if err := v.Verify(context.Background(), img); !errors.Is(err, ErrSignature) || !strings.Contains(err.Error(), "no signatures found") {
		t.Fatalf("unsigned image: %v", err)
	}
}

func TestParseCosignFlagsRefusesUnlistedFlags(t *testing.T) {
	flags, err := ParseCosignFlags("  --insecure-ignore-tlog=true\t--allow-http-registry=true ")
	if err != nil || len(flags) != 2 {
		t.Fatalf("%v %v", flags, err)
	}
	if flags, err := ParseCosignFlags(""); err != nil || flags != nil {
		t.Fatalf("empty: %v %v", flags, err)
	}
	for _, bad := range []string{"--key=/tmp/other.pub", "--insecure-ignore-tlog", "--certificate-identity-regexp=.*", "--insecure-ignore-tlog=true --output=json"} {
		if _, err := ParseCosignFlags(bad); !errors.Is(err, ErrBadRequest) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("local-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"SUPERVISOR_TOKEN_FILE": token, "COSIGN_VERIFY_FLAGS": "--insecure-ignore-tlog=true"}
	cfg, err := LoadFrom(func(k string) string { return env[k] })
	if err != nil || len(cfg.CosignFlags) != 1 || cfg.CosignFlags[0] != "--insecure-ignore-tlog=true" {
		t.Fatalf("config flags %v %v", cfg.CosignFlags, err)
	}
	env["COSIGN_VERIFY_FLAGS"] = "--key=/tmp/other.pub"
	if _, err := LoadFrom(func(k string) string { return env[k] }); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("config accepted an unlisted cosign flag: %v", err)
	}
}

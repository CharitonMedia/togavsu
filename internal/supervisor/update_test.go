package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type scriptRuntime struct {
	order  *[]string
	cores  []ContainerInfo
	health error
	stopID string
}

func (r *scriptRuntime) add(step string) {
	*r.order = append(*r.order, step)
}

func (r *scriptRuntime) ListCores(context.Context) ([]ContainerInfo, error) {
	r.add("list")
	return r.cores, nil
}
func (r *scriptRuntime) StartCore(_ context.Context, image, name string) (ContainerInfo, error) {
	r.add("start:" + image)
	return ContainerInfo{ID: "new", Name: name, Image: image}, nil
}
func (r *scriptRuntime) WaitHealthy(context.Context, string, time.Duration) error {
	r.add("health")
	return r.health
}
func (r *scriptRuntime) Stop(_ context.Context, id string) error {
	r.add("stop:" + id)
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
	want := []string{"list", "backup", "migrate", "start:togavsu-core:1.1.0", "health", "switch:togavsu-core-next:8080", "stop:old", "remove:old"}
	if stringsJoin(order) != stringsJoin(want) {
		t.Fatalf("order %v", order)
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
		if step == "stop:old" || step == "migrate" || step == "backup" {
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
	if !got.Restored || !got.MigrationRan {
		t.Fatalf("result %+v", got)
	}
	if stringsJoin(order) != stringsJoin([]string{"list", "backup", "migrate", "start:togavsu-core:1.1.0", "health", "stop:new", "remove:new", "restore"}) {
		t.Fatalf("order %v", order)
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
		if step == "switch:togavsu-core-next:8080" || step == "start:togavsu-core:1.1.0" {
			t.Fatalf("switched without migrations: %v", order)
		}
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
		Registry: staticRegistry{Release{Image: "togavsu-core:1.1.0", Version: "1.1.0"}},
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

func (m scriptMigrator) Apply(context.Context, string) (bool, error) {
	*m.order = append(*m.order, "migrate")
	return true, nil
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

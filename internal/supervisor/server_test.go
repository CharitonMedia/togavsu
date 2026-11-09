package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAPIAuthBackupAndRedaction(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	store.Dir = dir
	store.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(dir, "destinations.json")}
	var logs bytes.Buffer
	log := slog.New(ScrubHandler{Next: slog.NewTextHandler(&logs, nil)})
	ops := &Operations{
		Store:      store,
		Gate:       &Gate{},
		Ledger:     OpenLedger(filepath.Join(dir, "ledger.jsonl")),
		Passphrase: func() (string, error) { return "synthetic-passphrase", nil },
		ApplyDump: func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "core", Name: "core", Image: "togavsu-core:1"}}},
	}
	srv := httptest.NewServer(NewMux("local-token", ops, log))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/health")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("health %v %v", res, err)
	}
	res.Body.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/backups", strings.NewReader(`{"reason":"manual"}`))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/backups", strings.NewReader(`{"reason":"manual"}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/backups", strings.NewReader(`{"reason":"manual"}`))
	req.Header.Set("Authorization", "Bearer local-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("create %d %s", res.StatusCode, body)
	}
	var meta BackupMeta
	if err := json.NewDecoder(res.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "local-token") || strings.Contains(logs.String(), "synthetic-passphrase") {
		t.Fatalf("log leaked secrets: %s", logs.String())
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/v1/backups/"+meta.ID+"/archive", nil)
	req.Header.Set("Authorization", "Bearer local-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	spec := `{"id":"share","kind":"s3","endpoint":"http://127.0.0.1:9","region":"us-east-1","bucket":"b","access_key":"AKIA","secret_key":"super-secret-value"}`
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/backup-destinations", strings.NewReader(spec))
	req.Header.Set("Authorization", "Bearer local-token")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || strings.Contains(string(body), "super-secret-value") {
		t.Fatalf("destination response %d %s", res.StatusCode, body)
	}
	stored, err := os.ReadFile(store.Book.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("super-secret-value")) || !bytes.Contains(stored, []byte(destSealPrefix)) {
		t.Fatalf("destination file kept the credential: %s", stored)
	}
}

func TestRestoreFailureRollsBackToSafety(t *testing.T) {
	store := testStore(t)
	calls := 0
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(context.Context, io.Reader) error {
			calls++
			return io.ErrClosedPipe
		},
		Runtime: &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "old", Name: "core", Image: "img"}}},
		Outbox:  OpenOutbox(filepath.Join(store.Dir, "outbox.jsonl")),
		Ledger:  OpenLedger(filepath.Join(store.Dir, "ledger.jsonl")),
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status != "failed" {
		t.Fatalf("restore %+v %v", rec, err)
	}
	if calls != 1 {
		t.Fatalf("apply calls %d", calls)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes %+v %v", notes, err)
	}
}

func TestRestoreRestartsStoppedContainer(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1", Name: "togavsu-core-abc", Image: "img"}}},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err != nil || rec.Status != "completed" {
		t.Fatalf("%+v %v", rec, err)
	}
	if !containsStep(order, "restart:core-1") || !containsStep(order, "health") {
		t.Fatalf("order %v", order)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "start:") {
			t.Fatalf("created a replacement instead of restarting: %v", order)
		}
	}
}

func TestSafetyRestoreFailureDoesNotResume(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(context.Context, io.Reader) error {
			return io.ErrClosedPipe
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Outbox:  OpenOutbox(filepath.Join(store.Dir, "outbox.jsonl")),
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status == "completed" {
		t.Fatalf("%+v %v", rec, err)
	}
	if strings.Contains(err.Error(), "safety restore failed") {
		t.Fatalf("replayed recovery over unchanged data: %v", err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers were not restarted: %v", order)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes %+v %v", notes, err)
	}
	if strings.Contains(notes[0].Body, "was applied") {
		t.Fatalf("notice claimed recovery: %s", notes[0].Body)
	}
}

func TestRestoreRestartErrorIsNotSuccess(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}, failStart: "core-1"},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status == "completed" {
		t.Fatalf("%+v %v", rec, err)
	}
}

func TestRejectedRestoreLeavesWritersAndData(t *testing.T) {
	store := testStore(t)
	var order []string
	var marker int
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(context.Context, io.Reader) error {
			marker = 2
			return nil
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	marker = 1
	_, err := ops.Restore(context.Background(), "missing-backup")
	if err == nil {
		t.Fatal("accepted a missing archive")
	}
	if marker != 1 {
		t.Fatalf("data changed to %d", marker)
	}
	if len(order) != 0 {
		t.Fatalf("writers were touched: %v", order)
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	store.Passphrase = "wrong-passphrase"
	_, err = ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("got %v", err)
	}
	if marker != 1 || len(order) != 0 {
		t.Fatalf("marker %d order %v", marker, order)
	}
}

func TestFileBearingArchiveDoesNotReplaceTheDatabase(t *testing.T) {
	store := testStore(t)
	body := []byte("synthetic-file-bytes")
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	store.Files = func(context.Context) ([]FileBlob, error) {
		return []FileBlob{{
			Name:   "matter/note.txt",
			SHA256: sum,
			Open:   func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil },
		}}, nil
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	var marker int
	var order []string
	marker = 1
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyRestore: func(ctx context.Context, payload RestorePayload) error {
			marker = 2
			return ApplyProductRestore(ctx, "psql", "postgres://togavsu@127.0.0.1/postgres", "postgres://togavsu@127.0.0.1/togavsu", payload)
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, ErrFileRestoreUnsupported) || rec.Status == "completed" {
		t.Fatalf("%+v %v", rec, err)
	}
	if marker != 1 || len(order) != 0 {
		t.Fatalf("marker %d order %v", marker, order)
	}
	err = store.Restore(context.Background(), meta.ID, "", func(_ context.Context, payload RestorePayload) error {
		if len(payload.Files) != 1 || payload.Files[0].SHA256 != sum {
			t.Fatalf("payload files %+v", payload.Files)
		}
		got, err := os.ReadFile(payload.Files[0].Path)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("bytes %q", got)
		}
		return ApplyProductRestore(context.Background(), "/bin/false", "postgres://example/postgres", "postgres://example/togavsu", payload)
	})
	if !errors.Is(err, ErrFileRestoreUnsupported) {
		t.Fatalf("shipped restore %v", err)
	}
}

func TestRestoreIgnoresCallerCancelDuringApply(t *testing.T) {
	store := testStore(t)
	caller, cancel := context.WithCancel(context.Background())
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(ctx context.Context, r io.Reader) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{
			order:    &order,
			cores:    []ContainerInfo{{ID: "core-1"}},
			stopHook: cancel,
		},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(caller, meta.ID)
	if err != nil || rec.Status != "completed" {
		t.Fatalf("%+v %v caller %v", rec, err, caller.Err())
	}
}

func TestAcceptedRestoreIsPollableAfterDisconnect(t *testing.T) {
	store := testStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(ctx context.Context, r io.Reader) error {
			close(started)
			<-release
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "core-1"}}},
		Ledger:  OpenLedger(filepath.Join(store.Dir, "ledger.jsonl")),
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux("local-token", ops, nil))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/restores", strings.NewReader(`{"backup_id":"`+meta.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer local-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	var accepted RestoreRecord
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("apply did not start")
	}
	cancel()
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		req, _ = http.NewRequest(http.MethodGet, srv.URL+"/v1/restores/"+accepted.ID, nil)
		req.Header.Set("Authorization", "Bearer local-token")
		res, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ = io.ReadAll(res.Body)
		res.Body.Close()
		var polled RestoreRecord
		if err := json.Unmarshal(body, &polled); err != nil {
			t.Fatal(err)
		}
		if polled.Status == "completed" {
			break
		}
		if polled.Status == "failed" {
			t.Fatalf("restore failed after disconnect: %+v", polled)
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s", polled.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRestoreTimeoutStillRestartsCores(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store:          store,
		Gate:           &Gate{},
		Now:            store.Now,
		RestoreTimeout: 30 * time.Millisecond,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(ctx context.Context, _ io.Reader) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if err == nil || rec.Status == "completed" {
		t.Fatalf("%+v %v", rec, err)
	}
	if strings.Contains(err.Error(), "restart failed") || !containsStep(order, "restart:core-1") {
		t.Fatalf("restart used the expired restore context: %v %v", err, order)
	}
}

func TestCommittedRestoreReportsCleanupFailure(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyRestore: func(context.Context, RestorePayload) error {
			return fmt.Errorf("%w: drop database: busy", ErrRestoreCleanup)
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Outbox:  OpenOutbox(filepath.Join(store.Dir, "outbox.jsonl")),
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, ErrRestoreCleanup) || rec.Status != "completed" || rec.Error != "cleanup_pending" {
		t.Fatalf("%+v %v", rec, err)
	}
	if !containsStep(order, "restart:core-1") {
		t.Fatalf("writers left stopped: %v", order)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 || strings.Contains(notes[0].Body, "left in place") {
		t.Fatalf("notes %+v %v", notes, err)
	}
}

func TestOverlappingRestoresDoNotReplaceTheJob(t *testing.T) {
	store := testStore(t)
	started := make(chan struct{})
	releaseApply := make(chan struct{})
	var once sync.Once
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyDump: func(ctx context.Context, r io.Reader) error {
			once.Do(func() {
				close(started)
				<-releaseApply
			})
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, err := io.Copy(io.Discard, r)
			return err
		},
		Runtime: &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "core-1"}}},
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux("local-token", ops, nil))
	defer srv.Close()
	res, body := authJSON(t, srv.URL, http.MethodPost, "/v1/restores", `{"backup_id":"`+meta.ID+`"}`)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	var accepted RestoreRecord
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.ID == meta.ID+"-restore" || accepted.Status != "accepted" {
		t.Fatalf("%+v", accepted)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("apply did not start")
	}
	for _, id := range []string{meta.ID, other.ID} {
		busy, busyBody := authJSON(t, srv.URL, http.MethodPost, "/v1/restores", `{"backup_id":"`+id+`"}`)
		if busy.StatusCode != http.StatusConflict || !strings.Contains(string(busyBody), "operation_in_progress") {
			t.Fatalf("backup %s status %d %s", id, busy.StatusCode, busyBody)
		}
		if strings.Contains(string(busyBody), `"status":"failed"`) || strings.Contains(string(busyBody), accepted.ID) {
			t.Fatalf("busy response rewrote the job: %s", busyBody)
		}
	}
	if _, ok := ops.Job(meta.ID + "-restore"); ok {
		t.Fatal("legacy restore id was published")
	}
	polled, ok := ops.Job(accepted.ID)
	if !ok || polled.Status == "failed" || polled.Error != "" || polled.BackupID != meta.ID || polled.ID != accepted.ID {
		t.Fatalf("in-flight job changed: %+v %v", polled, ok)
	}
	got, gotBody := authJSON(t, srv.URL, http.MethodGet, "/v1/restores/"+accepted.ID, "")
	if got.StatusCode != http.StatusOK {
		t.Fatalf("poll %d %s", got.StatusCode, gotBody)
	}
	var visible RestoreRecord
	if err := json.Unmarshal(gotBody, &visible); err != nil {
		t.Fatal(err)
	}
	if visible.ID != accepted.ID || visible.Status == "failed" || visible.BackupID != meta.ID {
		t.Fatalf("poll replaced the job: %+v", visible)
	}
	close(releaseApply)
	waitRestoreStatus(t, srv.URL, accepted.ID, "completed")
	again, againBody := authJSON(t, srv.URL, http.MethodPost, "/v1/restores", `{"backup_id":"`+other.ID+`"}`)
	if again.StatusCode != http.StatusAccepted {
		t.Fatalf("later restore %d %s", again.StatusCode, againBody)
	}
	var second RestoreRecord
	if err := json.Unmarshal(againBody, &second); err != nil {
		t.Fatal(err)
	}
	if second.ID == accepted.ID || second.ID == other.ID+"-restore" {
		t.Fatalf("operation id was reused: %+v", second)
	}
	waitRestoreStatus(t, srv.URL, second.ID, "completed")
	first, _ := ops.Job(accepted.ID)
	if first.Status != "completed" || first.BackupID != meta.ID {
		t.Fatalf("first job after overlap: %+v", first)
	}
}

func TestDestinationHandlersSerializeMutations(t *testing.T) {
	store := testStore(t)
	dir := t.TempDir()
	store.Book = &DestinationBook{Key: testInstallerKey(), Path: filepath.Join(dir, "destinations.json")}
	ops := &Operations{Store: store, Gate: &Gate{}, Passphrase: func() (string, error) { return store.Passphrase, nil }}
	srv := httptest.NewServer(NewMux("local-token", ops, nil))
	defer srv.Close()
	for i := 0; i < 8; i++ {
		res, body := authJSON(t, srv.URL, http.MethodPost, "/v1/backup-destinations", fmt.Sprintf(`{"id":"d%d","kind":"path","path":"/tmp/dest%d","retention_days":%d}`, i, i, 11+i))
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create d%d: %d %s", i, res.StatusCode, body)
		}
	}
	call := func(method, path, payload string) (int, []byte, error) {
		var body io.Reader
		if payload != "" {
			body = strings.NewReader(payload)
		}
		req, err := http.NewRequest(method, srv.URL+path, body)
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Authorization", "Bearer local-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, raw, nil
	}
	var wg sync.WaitGroup
	errc := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				status, body, err := call(http.MethodDelete, fmt.Sprintf("/v1/backup-destinations/d%d", i), "")
				if err != nil || status != http.StatusNoContent {
					errc <- fmt.Errorf("delete d%d: %d %s %v", i, status, body, err)
				}
				return
			}
			status, body, err := call(http.MethodPost, "/v1/backup-destinations", fmt.Sprintf(`{"id":"d%d","kind":"path","path":"/tmp/upd%d","retention_days":%d}`, i, i, 100+i))
			if err != nil || status != http.StatusCreated {
				errc <- fmt.Errorf("update d%d: %d %s %v", i, status, body, err)
			}
		}(i)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, body, err := call(http.MethodPost, "/v1/backup-destinations", fmt.Sprintf(`{"id":"same","kind":"path","path":"/tmp/same%d","retention_days":%d}`, i, 200+i))
			if err != nil || status != http.StatusCreated {
				errc <- fmt.Errorf("same %d: %d %s %v", i, status, body, err)
			}
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Error(err)
	}
	specs, err := store.Book.Specs()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]DestinationSpec{}
	for _, spec := range specs {
		if _, dup := byID[spec.ID]; dup {
			t.Fatalf("duplicate %s", spec.ID)
		}
		byID[spec.ID] = spec
	}
	for i := 0; i < 8; i++ {
		spec, ok := byID[fmt.Sprintf("d%d", i)]
		if i%2 == 0 {
			if ok {
				t.Fatalf("deleted d%d survived: %+v", i, spec)
			}
			continue
		}
		if !ok || spec.Path != fmt.Sprintf("/tmp/upd%d", i) || spec.RetentionDays != 100+i {
			t.Fatalf("d%d = %+v", i, spec)
		}
	}
	same, ok := byID["same"]
	if !ok {
		t.Fatal("same destination missing")
	}
	index := same.RetentionDays - 200
	if index < 0 || index >= 12 || same.Path != fmt.Sprintf("/tmp/same%d", index) {
		t.Fatalf("same destination mixed fields: %+v", same)
	}
	partials, err := filepath.Glob(filepath.Join(dir, ".partial-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(partials) != 0 {
		t.Fatalf("partial files left behind: %v", partials)
	}
}

func TestUnobservedSwapDoesNotClaimDataLeftInPlace(t *testing.T) {
	store := testStore(t)
	var order []string
	ops := &Operations{
		Store: store,
		Gate:  &Gate{},
		Now:   store.Now,
		Passphrase: func() (string, error) {
			return store.Passphrase, nil
		},
		ApplyRestore: func(context.Context, RestorePayload) error {
			return errSwapUnobserved
		},
		Runtime: &scriptRuntime{order: &order, cores: []ContainerInfo{{ID: "core-1"}}},
		Outbox:  OpenOutbox(filepath.Join(store.Dir, "outbox.jsonl")),
	}
	meta, err := store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := ops.Restore(context.Background(), meta.ID)
	if !errors.Is(err, errSwapUnobserved) || rec.Status == "completed" || rec.Status == "failed" {
		t.Fatalf("%+v %v", rec, err)
	}
	if containsStep(order, "restart:core-1") {
		t.Fatalf("restarted writers without a live catalog: %v", order)
	}
	if rec.Recovery != "catalog_unobserved" {
		t.Fatalf("recovery %+v", rec)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 || strings.Contains(notes[0].Body, "left in place") || !strings.Contains(notes[0].Body, "not restarted") {
		t.Fatalf("notes %+v %v", notes, err)
	}
}

func authJSON(t *testing.T, base, method, path, payload string) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != "" {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer local-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, raw
}

func waitRestoreStatus(t *testing.T, base, id, status string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		res, body := authJSON(t, base, http.MethodGet, "/v1/restores/"+id, "")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("poll %d %s", res.StatusCode, body)
		}
		var polled RestoreRecord
		if err := json.Unmarshal(body, &polled); err != nil {
			t.Fatal(err)
		}
		if polled.Status == status {
			return
		}
		if polled.Status == "failed" {
			t.Fatalf("restore failed: %+v", polled)
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s", polled.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBusyRejectsSecondBackup(t *testing.T) {
	store := testStore(t)
	ops := &Operations{Store: store, Gate: &Gate{}, Passphrase: func() (string, error) { return store.Passphrase, nil }}
	release, ok := ops.Gate.Try()
	if !ok {
		t.Fatal("gate")
	}
	defer release()
	_, err := ops.CreateBackup(context.Background(), reasonManual)
	code, _ := PublicCode(err)
	if err == nil || code != "operation_in_progress" {
		t.Fatalf("got %v", err)
	}
}

// A restore must not report a terminal status while it still holds the gate,
// or a client that sees "completed" gets operation_in_progress on its next
// operation. The release hook holds the window open, so the check is
// deterministic.
func TestTerminalRestoreIsPublishedAfterGateRelease(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(context.Context, io.Reader) error
		want  string
	}{
		{"completed", func(_ context.Context, r io.Reader) error {
			_, err := io.Copy(io.Discard, r)
			return err
		}, "completed"},
		{"failed", func(context.Context, io.Reader) error {
			return errors.New("apply failed")
		}, "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := testStore(t)
			ops := &Operations{
				Store: store,
				Gate:  &Gate{},
				Now:   store.Now,
				Passphrase: func() (string, error) {
					return store.Passphrase, nil
				},
				ApplyDump: tc.apply,
				Runtime:   &scriptRuntime{order: &[]string{}, cores: []ContainerInfo{{ID: "core-1"}}},
			}
			meta, err := store.Create(context.Background(), reasonManual)
			if err != nil {
				t.Fatal(err)
			}
			rec, release, err := ops.admitRestore(context.Background(), meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			releasing := make(chan struct{})
			proceed := make(chan struct{})
			finished := make(chan RestoreRecord, 1)
			go func() {
				done, _ := ops.runRestore(rec, func() {
					close(releasing)
					<-proceed
					release()
				})
				finished <- done
			}()
			select {
			case <-releasing:
			case <-time.After(5 * time.Second):
				t.Fatal("restore did not reach release")
			}
			if _, ok := ops.Gate.Try(); ok {
				t.Fatal("gate free before release")
			}
			polled, ok, err := ops.LookupRestore(rec.ID)
			if err != nil || !ok {
				t.Fatalf("lookup %v %v", ok, err)
			}
			if terminalRestore(polled.Status) {
				t.Fatalf("terminal status %q visible while the gate is held", polled.Status)
			}
			close(proceed)
			done := <-finished
			if done.Status != tc.want {
				t.Fatalf("returned %+v", done)
			}
			polled, ok, err = ops.LookupRestore(rec.ID)
			if err != nil || !ok || polled.Status != tc.want {
				t.Fatalf("after release %+v %v %v", polled, ok, err)
			}
			free, ok := ops.Gate.Try()
			if !ok {
				t.Fatal("gate still held after a terminal status was published")
			}
			free()
		})
	}
}

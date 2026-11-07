package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIAuthBackupAndRedaction(t *testing.T) {
	dir := t.TempDir()
	store := testStore(t)
	store.Dir = dir
	store.Book = &DestinationBook{Path: filepath.Join(dir, "destinations.json")}
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
			if calls == 1 {
				return io.ErrClosedPipe
			}
			return nil
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
	if calls != 2 {
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
	if !strings.Contains(err.Error(), "safety restore failed") {
		t.Fatalf("err %v", err)
	}
	notes, err := ops.Outbox.List()
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes %+v %v", notes, err)
	}
	if strings.Contains(notes[0].Body, "was applied") {
		t.Fatalf("notice claimed recovery: %s", notes[0].Body)
	}
	for _, step := range order {
		if strings.HasPrefix(step, "restart:") {
			t.Fatalf("writers resumed: %v", order)
		}
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

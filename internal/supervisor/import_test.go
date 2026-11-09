package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sourceArchive makes one encrypted backup in its own store and returns its
// id, archive bytes, and sidecar bytes, as a download and a destination
// copy would hold them.
func sourceArchive(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	src := testStore(t)
	src.Dump = func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "COPY core.synthetic_items (id) FROM stdin;\n7\n8\n\\.\n")
		return err
	}
	meta, err := src.Create(context.Background(), reasonNightly)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(src.Dir, meta.ID+".tgbk"))
	if err != nil {
		t.Fatal(err)
	}
	side, err := os.ReadFile(filepath.Join(src.Dir, meta.ID+".sha256"))
	if err != nil {
		t.Fatal(err)
	}
	return meta.ID, archive, side
}

type importTarget struct {
	store   *Store
	ops     *Operations
	order   *[]string
	mu      sync.Mutex
	applied []string
	srv     *httptest.Server
}

func newImportTarget(t *testing.T, pass string) *importTarget {
	t.Helper()
	store := testStore(t)
	store.Passphrase = pass
	state := t.TempDir()
	tg := &importTarget{store: store, order: &[]string{}}
	tg.ops = &Operations{
		Store:      store,
		Gate:       &Gate{},
		Now:        store.Now,
		Passphrase: func() (string, error) { return store.Passphrase, nil },
		ApplyDump: func(_ context.Context, r io.Reader) error {
			raw, err := io.ReadAll(r)
			tg.mu.Lock()
			tg.applied = append(tg.applied, string(raw))
			tg.mu.Unlock()
			return err
		},
		Runtime: &scriptRuntime{order: tg.order, cores: []ContainerInfo{{ID: "core-1", Name: "core", Image: "img"}}},
		Ledger:  OpenLedger(filepath.Join(state, "ledger.jsonl")),
		Outbox:  OpenOutbox(filepath.Join(state, "outbox.jsonl")),
	}
	tg.srv = httptest.NewServer(NewMux("local-token", tg.ops, nil))
	t.Cleanup(tg.srv.Close)
	return tg
}

func (tg *importTarget) appliedDumps() []string {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	return append([]string(nil), tg.applied...)
}

// assertNothingImported checks that a refused import left no archive,
// sidecar, record, or intent on the backup volume and stopped no writer.
func (tg *importTarget) assertNothingImported(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(tg.store.Dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, ent := range entries {
		name := ent.Name()
		for _, suffix := range []string{".tgbk", ".sha256", ".json", publicationSuffix} {
			if strings.HasSuffix(name, suffix) {
				t.Fatalf("refused import left %s", name)
			}
		}
	}
	if len(*tg.order) != 0 {
		t.Fatalf("writers were touched: %v", *tg.order)
	}
	if got := tg.appliedDumps(); len(got) != 0 {
		t.Fatalf("dump applied: %v", got)
	}
}

type uploadPart struct {
	field, filename string
	body            []byte
}

func postUpload(t *testing.T, base string, parts ...uploadPart) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		disp := `form-data; name="` + p.field + `"`
		if p.filename != "" {
			disp += `; filename="` + strings.ReplaceAll(p.filename, `\`, `\\`) + `"`
		}
		h.Set("Content-Disposition", disp)
		h.Set("Content-Type", "application/octet-stream")
		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(p.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/v1/restores/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer local-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, raw
}

func apiError(t *testing.T, raw []byte) (string, RestoreRecord) {
	t.Helper()
	var body struct {
		Error  string        `json:"error"`
		Result RestoreRecord `json:"result"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return body.Error, body.Result
}

func TestUploadRestoreAppliesTheArchiveInEitherPartOrder(t *testing.T) {
	id, archive, side := sourceArchive(t)
	orders := map[string][]uploadPart{
		"checksum first": {{"checksum", id + ".sha256", side}, {"archive", id + ".tgbk", archive}},
		"archive first":  {{"archive", id + ".tgbk", archive}, {"checksum", "", side}},
	}
	for name, parts := range orders {
		t.Run(name, func(t *testing.T) {
			tg := newImportTarget(t, "synthetic-passphrase")
			res, raw := postUpload(t, tg.srv.URL, parts...)
			if res.StatusCode != http.StatusAccepted {
				t.Fatalf("status %d %s", res.StatusCode, raw)
			}
			var accepted RestoreRecord
			if err := json.Unmarshal(raw, &accepted); err != nil {
				t.Fatal(err)
			}
			if accepted.BackupID != id || accepted.Source != "upload" {
				t.Fatalf("accepted %+v", accepted)
			}
			waitRestoreStatus(t, tg.srv.URL, accepted.ID, "completed")
			got := tg.appliedDumps()
			if len(got) != 1 || !strings.Contains(got[0], "\n7\n8\n") {
				t.Fatalf("applied %q", got)
			}
			meta, err := tg.store.lookup(id)
			if err != nil || meta.Source != "upload" || meta.Reason != reasonNightly || meta.SHA256 != strings.TrimSpace(string(side)) {
				t.Fatalf("imported meta %+v %v", meta, err)
			}
			res, raw = authJSON(t, tg.srv.URL, http.MethodGet, "/v1/restores/"+accepted.ID, "")
			var polled RestoreRecord
			if err := json.Unmarshal(raw, &polled); err != nil || polled.Source != "upload" {
				t.Fatalf("polled %s %v", raw, err)
			}
			// The ledger record carries the source too.
			evs, err := tg.ops.Ledger.List("restore")
			if err != nil || len(evs) == 0 || evs[len(evs)-1].Fields["source"] != "upload" {
				t.Fatalf("ledger %+v %v", evs, err)
			}
		})
	}
}

func TestUploadRestoreRejectsBadChecksum(t *testing.T) {
	id, archive, side := sourceArchive(t)
	tampered := append([]byte(nil), archive...)
	tampered[len(tampered)/2] ^= 0xff
	cases := map[string][]uploadPart{
		"archive bytes changed": {{"checksum", "", side}, {"archive", id + ".tgbk", tampered}},
		"wrong digest":          {{"checksum", "", []byte(strings.Repeat("0", 64) + "\n")}, {"archive", id + ".tgbk", archive}},
		"not a digest":          {{"checksum", "", []byte("not-a-digest\n")}, {"archive", id + ".tgbk", archive}},
		"oversized sidecar":     {{"checksum", "", bytes.Repeat([]byte("a"), 200)}, {"archive", id + ".tgbk", archive}},
	}
	for name, parts := range cases {
		t.Run(name, func(t *testing.T) {
			tg := newImportTarget(t, "synthetic-passphrase")
			res, raw := postUpload(t, tg.srv.URL, parts...)
			code, rec := apiError(t, raw)
			if res.StatusCode != http.StatusUnprocessableEntity || code != "invalid_backup" {
				t.Fatalf("status %d %s", res.StatusCode, raw)
			}
			if rec.Status != "failed" || rec.Source != "upload" {
				t.Fatalf("record %+v", rec)
			}
			tg.assertNothingImported(t)
		})
	}
}

func TestUploadRestoreRejectsWrongPassphrase(t *testing.T) {
	id, archive, side := sourceArchive(t)
	tg := newImportTarget(t, "a-different-passphrase")
	res, raw := postUpload(t, tg.srv.URL, uploadPart{"checksum", "", side}, uploadPart{"archive", id + ".tgbk", archive})
	code, rec := apiError(t, raw)
	if res.StatusCode != http.StatusBadRequest || code != "decrypt_failed" {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	if rec.Status != "failed" || rec.BackupID != id {
		t.Fatalf("record %+v", rec)
	}
	tg.assertNothingImported(t)
}

func TestUploadRestoreRejectsOversizedArchive(t *testing.T) {
	id, archive, side := sourceArchive(t)
	tg := newImportTarget(t, "synthetic-passphrase")
	tg.store.MaxImportBytes = int64(len(archive)) - 1
	res, raw := postUpload(t, tg.srv.URL, uploadPart{"checksum", "", side}, uploadPart{"archive", id + ".tgbk", archive})
	code, _ := apiError(t, raw)
	if res.StatusCode != http.StatusRequestEntityTooLarge || code != "too_large" {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	tg.assertNothingImported(t)

	// The body bound also stops a request whose framing alone is too large.
	tg.store.MaxImportBytes = 16
	res, raw = postUpload(t, tg.srv.URL, uploadPart{"checksum", "", bytes.Repeat([]byte(" "), uploadFraming+64)}, uploadPart{"archive", id + ".tgbk", archive})
	if res.StatusCode == http.StatusAccepted {
		t.Fatalf("oversized body accepted: %s", raw)
	}
	tg.assertNothingImported(t)

	// Exactly at the bound is accepted.
	tg.store.MaxImportBytes = int64(len(archive))
	res, raw = postUpload(t, tg.srv.URL, uploadPart{"checksum", "", side}, uploadPart{"archive", id + ".tgbk", archive})
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("archive at the bound: %d %s", res.StatusCode, raw)
	}
	var accepted RestoreRecord
	if err := json.Unmarshal(raw, &accepted); err != nil {
		t.Fatal(err)
	}
	waitRestoreStatus(t, tg.srv.URL, accepted.ID, "completed")
}

func TestUploadRestoreRejectsUnsafeAndMalformedNames(t *testing.T) {
	id, archive, side := sourceArchive(t)
	other, _, _ := sourceArchive(t)
	cases := map[string]struct {
		parts []uploadPart
		code  string
	}{
		"parent traversal":      {[]uploadPart{{"checksum", "", side}, {"archive", "../" + id + ".tgbk", archive}}, "invalid_backup"},
		"absolute path":         {[]uploadPart{{"checksum", "", side}, {"archive", "/etc/" + id + ".tgbk", archive}}, "invalid_backup"},
		"windows separator":     {[]uploadPart{{"checksum", "", side}, {"archive", `..\` + id + ".tgbk", archive}}, "invalid_backup"},
		"not a backup id":       {[]uploadPart{{"checksum", "", side}, {"archive", "evil.tgbk", archive}}, "invalid_backup"},
		"no file name":          {[]uploadPart{{"checksum", "", side}, {"archive", "", archive}}, "invalid_backup"},
		"checksum traversal":    {[]uploadPart{{"checksum", "../" + id + ".sha256", side}, {"archive", id + ".tgbk", archive}}, "invalid_backup"},
		"checksum for other id": {[]uploadPart{{"checksum", other + ".sha256", side}, {"archive", id + ".tgbk", archive}}, "bad_request"},
		"renamed to another id": {[]uploadPart{{"checksum", "", side}, {"archive", other + ".tgbk", archive}}, "invalid_backup"},
		"missing checksum":      {[]uploadPart{{"archive", id + ".tgbk", archive}}, "bad_request"},
		"missing archive":       {[]uploadPart{{"checksum", "", side}}, "bad_request"},
		"unknown part":          {[]uploadPart{{"note", "", []byte("x")}, {"archive", id + ".tgbk", archive}}, "bad_request"},
		"duplicate checksum":    {[]uploadPart{{"checksum", "", side}, {"checksum", "", side}, {"archive", id + ".tgbk", archive}}, "bad_request"},
		"trailing part":         {[]uploadPart{{"checksum", "", side}, {"archive", id + ".tgbk", archive}, {"extra", "", []byte("x")}}, "bad_request"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tg := newImportTarget(t, "synthetic-passphrase")
			parent := filepath.Dir(tg.store.Dir)
			before, _ := os.ReadDir(parent)
			res, raw := postUpload(t, tg.srv.URL, tc.parts...)
			code, rec := apiError(t, raw)
			if code != tc.code || res.StatusCode == http.StatusAccepted {
				t.Fatalf("status %d %s", res.StatusCode, raw)
			}
			if rec.ID != "" && rec.Status != "failed" {
				t.Fatalf("record %+v", rec)
			}
			tg.assertNothingImported(t)
			after, _ := os.ReadDir(parent)
			if len(after) != len(before) {
				t.Fatalf("files appeared beside the backup volume: %v", after)
			}
		})
	}
	t.Run("not multipart", func(t *testing.T) {
		tg := newImportTarget(t, "synthetic-passphrase")
		req, _ := http.NewRequest(http.MethodPost, tg.srv.URL+"/v1/restores/upload", bytes.NewReader(archive))
		req.Header.Set("Authorization", "Bearer local-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d", res.StatusCode)
		}
		tg.assertNothingImported(t)
	})
	t.Run("unauthenticated", func(t *testing.T) {
		tg := newImportTarget(t, "synthetic-passphrase")
		req, _ := http.NewRequest(http.MethodPost, tg.srv.URL+"/v1/restores/upload", bytes.NewReader(archive))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d", res.StatusCode)
		}
		tg.assertNothingImported(t)
	})
}

func TestUploadRestoreRefusesAnIDAlreadyOnTheVolume(t *testing.T) {
	tg := newImportTarget(t, "synthetic-passphrase")
	meta, err := tg.store.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := os.ReadFile(filepath.Join(tg.store.Dir, meta.ID+".tgbk"))
	side, _ := os.ReadFile(filepath.Join(tg.store.Dir, meta.ID+".sha256"))
	res, raw := postUpload(t, tg.srv.URL, uploadPart{"checksum", "", side}, uploadPart{"archive", meta.ID + ".tgbk", archive})
	code, _ := apiError(t, raw)
	if res.StatusCode != http.StatusConflict || code != "backup_exists" {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	again, err := tg.store.lookup(meta.ID)
	if err != nil || again.SHA256 != meta.SHA256 || again.Source != "" {
		t.Fatalf("local archive changed: %+v %v", again, err)
	}
	if len(*tg.order) != 0 {
		t.Fatalf("writers touched: %v", *tg.order)
	}
}

func TestUploadRestoreIsRefusedWhileBusy(t *testing.T) {
	id, archive, side := sourceArchive(t)
	tg := newImportTarget(t, "synthetic-passphrase")
	release, ok := tg.ops.Gate.Try()
	if !ok {
		t.Fatal("gate")
	}
	res, raw := postUpload(t, tg.srv.URL, uploadPart{"checksum", "", side}, uploadPart{"archive", id + ".tgbk", archive})
	release()
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(raw), "operation_in_progress") {
		t.Fatalf("status %d %s", res.StatusCode, raw)
	}
	tg.assertNothingImported(t)
}

func TestFailedImportPublicationIsAbandoned(t *testing.T) {
	id, archive, side := sourceArchive(t)
	for _, step := range []string{"archive", "checksum", "metadata"} {
		t.Run(step, func(t *testing.T) {
			tg := newImportTarget(t, "synthetic-passphrase")
			tg.store.publishHook = func(got string) error {
				if got == step {
					return errors.New("disk full")
				}
				return nil
			}
			_, _, err := tg.store.Import(context.Background(), importSource{
				ID: id, Archive: bytes.NewReader(archive), Origin: "upload",
				Sidecar: func() ([]byte, error) { return side, nil },
			}, "synthetic-passphrase")
			if err == nil {
				t.Fatal("import succeeded")
			}
			tg.assertNothingImported(t)
		})
	}
}

// remoteHarness serves one destination with an archive and sidecar placed
// in this installation's namespace through the destination's own Put.
type remoteHarness struct {
	spec   DestinationSpec
	open   func(DestinationSpec) (Destination, error)
	direct Destination
}

func placeRemote(t *testing.T, h remoteHarness, id string, archive, side []byte) {
	t.Helper()
	sum, err := sha256Hex(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if archive != nil {
		if err := h.direct.Put(context.Background(), id+".tgbk", bytes.NewReader(archive), sum); err != nil {
			t.Fatal(err)
		}
	}
	if side != nil {
		if err := h.direct.Put(context.Background(), id+".sha256", bytes.NewReader(side), ""); err != nil {
			t.Fatal(err)
		}
	}
}

func pathHarness(t *testing.T) remoteHarness {
	t.Helper()
	spec := DestinationSpec{ID: "nas", Kind: "path", Path: t.TempDir(), Namespace: "nsrestore01", RetentionDays: 30}
	dest, err := OpenDestination(spec)
	if err != nil {
		t.Fatal(err)
	}
	return remoteHarness{spec: spec, direct: dest}
}

// fakeS3 is a minimal object store keyed by URL path. It answers GET, PUT,
// DELETE and ListObjectsV2 for the S3 client in this package.
func s3Harness(t *testing.T) remoteHarness {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			objects[r.URL.Path] = body
		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				var b strings.Builder
				b.WriteString("<ListBucketResult>")
				for key := range objects {
					b.WriteString("<Contents><Key>" + strings.TrimPrefix(key, "/backups/") + "</Key><LastModified>2026-03-04T05:06:07Z</LastModified></Contents>")
				}
				b.WriteString("</ListBucketResult>")
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, b.String())
				return
			}
			body, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			_, _ = w.Write(body)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	spec := DestinationSpec{
		ID: "bucket", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1", Bucket: "backups",
		Prefix: "firm", AccessKey: "AKID", SecretKey: "secret", Namespace: "nsrestore01", RetentionDays: 30,
	}
	open := func(spec DestinationSpec) (Destination, error) {
		d, err := OpenDestination(spec)
		if s3, ok := d.(*S3Destination); ok {
			s3.HTTP = srv.Client()
		}
		return d, err
	}
	dest, err := open(spec)
	if err != nil {
		t.Fatal(err)
	}
	return remoteHarness{spec: spec, open: open, direct: dest}
}

func sftpHarness(t *testing.T) remoteHarness {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	if err := os.WriteFile(script, []byte(sftpPython), 0o700); err != nil {
		t.Fatal(err)
	}
	base := sftpDest(t, dir, remote, script)
	spec := base.Spec
	spec.ID = "offsite"
	spec.Namespace = "nsrestore01"
	spec.RetentionDays = 30
	open := func(spec DestinationSpec) (Destination, error) {
		return &SFTPDestination{Spec: spec, Command: script}, nil
	}
	dest, _ := open(spec)
	return remoteHarness{spec: spec, open: open, direct: dest}
}

func (tg *importTarget) addDestination(t *testing.T, h remoteHarness) {
	t.Helper()
	tg.store.Book = &DestinationBook{Path: filepath.Join(t.TempDir(), "destinations.json"), Key: testInstallerKey()}
	tg.store.OpenDestination = h.open
	if _, err := tg.store.PutDestination(h.spec); err != nil {
		t.Fatal(err)
	}
}

func TestDestinationRestoreFromPathS3AndSFTP(t *testing.T) {
	harnesses := map[string]func(*testing.T) remoteHarness{
		"path": pathHarness,
		"s3":   s3Harness,
		"sftp": sftpHarness,
	}
	for kind, mk := range harnesses {
		t.Run(kind, func(t *testing.T) {
			id, archive, side := sourceArchive(t)
			h := mk(t)
			placeRemote(t, h, id, archive, side)
			tg := newImportTarget(t, "synthetic-passphrase")
			tg.addDestination(t, h)

			res, raw := authJSON(t, tg.srv.URL, http.MethodGet, "/v1/backup-destinations/"+h.spec.ID+"/backups", "")
			if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"id":"`+id+`"`) || !strings.Contains(string(raw), `"local":false`) {
				t.Fatalf("listing %d %s", res.StatusCode, raw)
			}

			res, raw = authJSON(t, tg.srv.URL, http.MethodPost, "/v1/restores", `{"backup_id":"`+id+`","destination_id":"`+h.spec.ID+`"}`)
			if res.StatusCode != http.StatusAccepted {
				t.Fatalf("status %d %s", res.StatusCode, raw)
			}
			var accepted RestoreRecord
			if err := json.Unmarshal(raw, &accepted); err != nil {
				t.Fatal(err)
			}
			if accepted.Source != "destination:"+h.spec.ID {
				t.Fatalf("accepted %+v", accepted)
			}
			waitRestoreStatus(t, tg.srv.URL, accepted.ID, "completed")
			got := tg.appliedDumps()
			if len(got) != 1 || !strings.Contains(got[0], "\n7\n8\n") {
				t.Fatalf("applied %q", got)
			}
			meta, err := tg.store.lookup(id)
			if err != nil || meta.Source != "destination:"+h.spec.ID {
				t.Fatalf("imported meta %+v %v", meta, err)
			}
			res, raw = authJSON(t, tg.srv.URL, http.MethodGet, "/v1/backup-destinations/"+h.spec.ID+"/backups", "")
			if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"local":true`) {
				t.Fatalf("listing after import %d %s", res.StatusCode, raw)
			}
			// A second fetch of the same id is refused; the local copy is
			// restored by backup_id instead.
			res, raw = authJSON(t, tg.srv.URL, http.MethodPost, "/v1/restores", `{"backup_id":"`+id+`","destination_id":"`+h.spec.ID+`"}`)
			if code, _ := apiError(t, raw); res.StatusCode != http.StatusConflict || code != "backup_exists" {
				t.Fatalf("second fetch %d %s", res.StatusCode, raw)
			}
		})
	}
}

func TestDestinationRestoreFailurePaths(t *testing.T) {
	id, archive, side := sourceArchive(t)
	tampered := append([]byte(nil), archive...)
	tampered[len(tampered)/3] ^= 0x01
	cases := map[string]struct {
		archive, side []byte
		backupID      string
		destID        string
		pass          string
		limit         int64
		status        int
		code          string
	}{
		"missing archive":       {side: side, status: http.StatusNotFound, code: "not_found"},
		"missing sidecar":       {archive: archive, status: http.StatusNotFound, code: "not_found"},
		"nothing there":         {status: http.StatusNotFound, code: "not_found"},
		"tampered archive":      {archive: tampered, side: side, status: http.StatusUnprocessableEntity, code: "invalid_backup"},
		"wrong remote digest":   {archive: archive, side: []byte(strings.Repeat("f", 64)), status: http.StatusUnprocessableEntity, code: "invalid_backup"},
		"oversized sidecar":     {archive: archive, side: bytes.Repeat([]byte("a"), 4096), status: http.StatusUnprocessableEntity, code: "invalid_backup"},
		"wrong passphrase":      {archive: archive, side: side, pass: "another-passphrase", status: http.StatusBadRequest, code: "decrypt_failed"},
		"oversized archive":     {archive: archive, side: side, limit: 32, status: http.StatusRequestEntityTooLarge, code: "too_large"},
		"traversal in id":       {archive: archive, side: side, backupID: "../../" + id, status: http.StatusUnprocessableEntity, code: "invalid_backup"},
		"separator in id":       {archive: archive, side: side, backupID: id + "/../x", status: http.StatusUnprocessableEntity, code: "invalid_backup"},
		"unknown destination":   {archive: archive, side: side, destID: "nowhere", status: http.StatusNotFound, code: "not_found"},
		"traversal destination": {archive: archive, side: side, destID: "../nas", status: http.StatusNotFound, code: "not_found"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := pathHarness(t)
			placeRemote(t, h, id, tc.archive, tc.side)
			pass := tc.pass
			if pass == "" {
				pass = "synthetic-passphrase"
			}
			tg := newImportTarget(t, pass)
			gets := 0
			tg.addDestination(t, h)
			tg.store.OpenDestination = func(spec DestinationSpec) (Destination, error) {
				d, err := OpenDestination(spec)
				return &countingDest{Destination: d, gets: &gets}, err
			}
			if tc.limit > 0 {
				tg.store.MaxImportBytes = tc.limit
			}
			backupID, destID := id, h.spec.ID
			if tc.backupID != "" {
				backupID = tc.backupID
			}
			if tc.destID != "" {
				destID = tc.destID
			}
			payload, _ := json.Marshal(map[string]string{"backup_id": backupID, "destination_id": destID})
			res, raw := authJSON(t, tg.srv.URL, http.MethodPost, "/v1/restores", string(payload))
			code, rec := apiError(t, raw)
			if res.StatusCode != tc.status || code != tc.code {
				t.Fatalf("status %d %s", res.StatusCode, raw)
			}
			if rec.Status != "failed" || rec.Source != "destination:"+destID {
				t.Fatalf("record %+v", rec)
			}
			if tc.backupID != "" && gets != 0 {
				t.Fatalf("unsafe id reached the destination: %d gets", gets)
			}
			tg.assertNothingImported(t)
		})
	}
}

type countingDest struct {
	Destination
	gets *int
}

func (d *countingDest) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	*d.gets++
	return d.Destination.Get(ctx, name)
}

func TestDestinationListingSkipsForeignAndIncompleteNames(t *testing.T) {
	id, archive, side := sourceArchive(t)
	other, otherArchive, _ := sourceArchive(t)
	h := pathHarness(t)
	placeRemote(t, h, id, archive, side)
	placeRemote(t, h, other, otherArchive, nil)
	if err := h.direct.Put(context.Background(), "notes.tgbk", bytes.NewReader([]byte("x")), ""); err != nil {
		t.Fatal(err)
	}
	tg := newImportTarget(t, "synthetic-passphrase")
	tg.addDestination(t, h)
	list, err := tg.store.DestinationBackups(context.Background(), h.spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Local || list[0].CreatedAt.IsZero() {
		t.Fatalf("listing %+v", list)
	}
	if _, err := tg.store.DestinationBackups(context.Background(), "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown destination: %v", err)
	}
}

func TestInterruptedImportIsRemovedOnRecovery(t *testing.T) {
	id, archive, _ := sourceArchive(t)
	tg := newImportTarget(t, "synthetic-passphrase")
	// The state a killed process leaves after streaming the archive and
	// before the sidecar and record were published.
	if err := os.MkdirAll(tg.store.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tg.store.beginPublication(id, tg.store.now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tg.store.Dir, id+".tgbk"), archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tg.store.RecoverPlaintext(); err != nil {
		t.Fatal(err)
	}
	tg.assertNothingImported(t)
}

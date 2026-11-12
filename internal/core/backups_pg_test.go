package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const supSecret = "synthetic-supervisor-secret"

// fakeSupervisor answers the supervisor routes the core fronts and records
// what reached it.
type fakeSupervisor struct {
	mu           sync.Mutex
	createdBy    string
	dests        map[string]map[string]any
	uploadSum    string
	uploadBody   string
	uploadName   string
	restoredFrom string
}

func (f *fakeSupervisor) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	meta := map[string]any{"id": "20261112T090000Z-0123456789abcdef", "created_at": "2026-11-12T09:00:00Z", "reason": "manual",
		"sha256": strings.Repeat("ab", 32), "bytes": 7, "files": true}
	mux.HandleFunc("POST /v1/backups", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.createdBy = body["created_by"]
		f.mu.Unlock()
		m := map[string]any{}
		for k, v := range meta {
			m[k] = v
		}
		m["created_by"] = body["created_by"]
		writeJSON(w, http.StatusCreated, m)
	})
	mux.HandleFunc("GET /v1/backups", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []any{meta}})
	})
	mux.HandleFunc("GET /v1/backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != meta["id"] {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "no backup"})
			return
		}
		writeJSON(w, http.StatusOK, meta)
	})
	mux.HandleFunc("GET /v1/backups/{id}/archive", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ARCHIVE")
	})
	mux.HandleFunc("DELETE /v1/backups/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /v1/backup-destinations", func(w http.ResponseWriter, r *http.Request) {
		var spec map[string]any
		_ = json.NewDecoder(r.Body).Decode(&spec)
		f.mu.Lock()
		if prev, ok := f.dests[spec["id"].(string)]; ok && spec["secret_key"] == nil {
			spec["secret_key"] = prev["secret_key"]
		}
		f.dests[spec["id"].(string)] = spec
		f.mu.Unlock()
		pub := map[string]any{}
		for k, v := range spec {
			if k != "secret_key" {
				pub[k] = v
			}
		}
		writeJSON(w, http.StatusCreated, pub)
	})
	mux.HandleFunc("GET /v1/backup-destinations", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []map[string]any{}
		for _, d := range f.dests {
			pub := map[string]any{}
			for k, v := range d {
				if k != "secret_key" {
					pub[k] = v
				}
			}
			out = append(out, pub)
		}
		writeJSON(w, http.StatusOK, map[string]any{"destinations": out})
	})
	mux.HandleFunc("POST /v1/backup-destinations/{id}/test", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		d := f.dests[r.PathValue("id")]
		f.mu.Unlock()
		if d["path"] == "/unreachable" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "destination_failed", "message": "path is not mounted"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("DELETE /v1/backup-destinations/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		delete(f.dests, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/restores/upload", func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			raw, _ := io.ReadAll(part)
			f.mu.Lock()
			switch part.FormName() {
			case "checksum":
				f.uploadSum = strings.TrimSpace(string(raw))
			case "archive":
				f.uploadBody, f.uploadName = string(raw), part.FileName()
			}
			f.mu.Unlock()
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"id": "restore-20261112T090100Z-00000000000000aa", "backup_id": strings.TrimSuffix(f.uploadName, ".tgbk"),
			"source": "upload", "status": "accepted", "started": "2026-11-12T09:01:00Z"})
	})
	mux.HandleFunc("POST /v1/restores", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.restoredFrom = body["backup_id"]
		f.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"id": "restore-20261112T090200Z-00000000000000bb", "backup_id": body["backup_id"],
			"status": "accepted", "started": "2026-11-12T09:02:00Z"})
	})
	mux.HandleFunc("GET /v1/restores/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "backup_id": meta["id"], "status": "failed", "error": "files_not_restored",
			"started": "2026-11-12T09:02:00Z"})
	})
	mux.HandleFunc("GET /v1/restores", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"restores": []any{map[string]any{"id": "restore-20261112T090200Z-00000000000000bb",
			"backup_id": meta["id"], "status": "completed", "started": "2026-11-12T09:02:00Z"}}})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+supSecret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized", "message": "token"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func auditFields(t *testing.T, admin *client, entity, id string) string {
	t.Helper()
	return admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type="+entity+"&entity_id="+id, nil).body
}

// TestBackupsAreAdminOnlyAuditedAndOnMCP covers the core's front of the
// supervisor's backup API (AC-018) and the supervisor's file-bytes route
// (AC-040, AC-050).
func TestBackupsAreAdminOnlyAuditedAndOnMCP(t *testing.T) {
	sup := &fakeSupervisor{dests: map[string]map[string]any{}}
	supSrv := httptest.NewServer(sup.handler(t))
	defer supSrv.Close()
	dsn := testDSN(t)
	passFile := filepath.Join(t.TempDir(), "backup_passphrase")
	if err := os.WriteFile(passFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := Open(context.Background(), Config{DatabaseURL: dsn, Key: testKey, Domain: "firm.example", DataDir: dataDir(t, dsn),
		SupervisorURL: supSrv.URL, SupervisorSecret: supSecret, BackupPassphraseFile: passFile, NoBackground: true, MailEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler())
	defer func() { srv.Close(); _ = app.Close() }()

	// Before setup the file-bytes route answers the supervisor (an empty export),
	// and nothing else.
	anon := newClient(t, srv.URL)
	if r := anon.do("GET", fileBytesPath, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("file bytes without the token: %d", r.code)
	}
	admin := firstRun(t, srv.URL)
	admin.must(http.StatusOK, "GET", "/api/v1/me", nil)
	if raw, _ := os.ReadFile(passFile); strings.TrimSpace(string(raw)) != "synthetic-backup-passphrase" {
		t.Fatalf("passphrase file = %q", raw)
	}

	staff, _ := enrollUser(t, app, srv.URL, admin, "Sam", "sam@firm.example", domain.RoleStaff)
	acct, _ := enrollUser(t, app, srv.URL, admin, "Ada", "ada@firm.example", domain.RoleAccountant)
	for _, c := range []*client{staff, acct} {
		for _, call := range [][2]string{{"GET", "/api/v1/backups"}, {"POST", "/api/v1/backups"}, {"GET", "/api/v1/backups/20261112T090000Z-0123456789abcdef/download"},
			{"POST", "/api/v1/backup-destinations"}, {"POST", "/api/v1/restores"}, {"POST", "/api/v1/backup-uploads"}, {"GET", "/api/v1/restores"}} {
			if r := c.do(call[0], call[1], map[string]any{}); r.code != http.StatusForbidden {
				t.Fatalf("non-admin %s %s: %d %s", call[0], call[1], r.code, r.body)
			}
		}
	}
	if sup.createdBy != "" {
		t.Fatal("a refused caller reached the supervisor")
	}
	noScope := newClient(t, srv.URL)
	noScope.bearer = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "matters", "scopes": []string{"matters:read"}}).json, "secret")
	if r := noScope.do("GET", "/api/v1/backups", nil); r.code != http.StatusForbidden {
		t.Fatalf("token without backups:admin listed backups: %d", r.code)
	}
	scoped := newClient(t, srv.URL)
	scoped.bearer = str(t, admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "backups", "scopes": []string{"backups:admin"}}).json, "secret")

	// Create, list, get, download and delete.
	made := admin.must(http.StatusAccepted, "POST", "/api/v1/backups", nil)
	id := str(t, made.json, "id")
	me := str(t, admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	if made.json["kind"] != "manual" || made.json["status"] != "completed" || made.json["created_by"] != me || sup.createdBy != me || made.json["files"] != true {
		t.Fatalf("create = %s (supervisor saw %q)", made.body, sup.createdBy)
	}
	if rows := auditFields(t, admin, "backup", id); !strings.Contains(rows, `"field":"created"`) || !strings.Contains(rows, `"source":"ui"`) || !strings.Contains(rows, me) {
		t.Fatalf("backup create not audited with actor and source: %s", rows)
	}
	if r := scoped.must(http.StatusOK, "GET", "/api/v1/backups", nil); !strings.Contains(r.body, id) || !strings.Contains(r.body, `"next_cursor":null`) {
		t.Fatal(r.body)
	}
	if r := admin.do("GET", "/api/v1/backups/20261112T090000Z-ffffffffffffffff", nil); r.code != http.StatusNotFound {
		t.Fatalf("missing backup: %d", r.code)
	}
	if r := admin.do("GET", "/api/v1/backups/..%2f..%2fetc", nil); r.code != http.StatusNotFound {
		t.Fatalf("bad backup id: %d", r.code)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/backups/"+id+"/download", nil); r.body != "ARCHIVE" {
		t.Fatalf("download = %q", r.body)
	}
	admin.must(http.StatusNoContent, "DELETE", "/api/v1/backups/"+id, nil)
	if rows := auditFields(t, admin, "backup", id); !strings.Contains(rows, `"field":"downloaded"`) || !strings.Contains(rows, `"field":"deleted"`) {
		t.Fatalf("download and delete not audited: %s", rows)
	}

	// Destinations: the secret reaches the supervisor and never the audit log or a response.
	s3 := admin.must(http.StatusCreated, "POST", "/api/v1/backup-destinations", map[string]any{"kind": "s3", "name": "Offsite", "retention_days": 30,
		"settings": map[string]any{"endpoint": "http://s3.example", "region": "us-east-1", "bucket": "b", "access_key": "AK", "secret_key": "very-secret-key"}})
	destID := str(t, s3.json, "id")
	if strings.Contains(s3.body, "very-secret-key") || s3.json["kind"] != "s3" || s3.json["name"] != "Offsite" {
		t.Fatal(s3.body)
	}
	if sup.dests[destID]["secret_key"] != "very-secret-key" || sup.dests[destID]["created_by"] != me {
		t.Fatalf("supervisor spec = %v", sup.dests[destID])
	}
	if r := admin.do("POST", "/api/v1/backup-destinations", map[string]any{"kind": "ftp", "name": "x", "retention_days": 1}); r.code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", r.code)
	}
	if r := admin.do("POST", "/api/v1/backup-destinations", map[string]any{"kind": "s3", "name": "x", "retention_days": 1, "settings": map[string]any{"rm": "-rf"}}); r.code != http.StatusBadRequest {
		t.Fatalf("unknown setting: %d", r.code)
	}
	patched := admin.must(http.StatusOK, "PATCH", "/api/v1/backup-destinations/"+destID, map[string]any{"name": "Offsite B", "retention_days": 60})
	if patched.json["name"] != "Offsite B" || sup.dests[destID]["secret_key"] != "very-secret-key" {
		t.Fatalf("patch = %s, spec %v", patched.body, sup.dests[destID])
	}
	admin.must(http.StatusOK, "POST", "/api/v1/backup-destinations/"+destID+"/test", nil)
	nas := admin.must(http.StatusCreated, "POST", "/api/v1/backup-destinations", map[string]any{"kind": "network_path", "name": "NAS", "retention_days": 7,
		"settings": map[string]any{"path": "/unreachable"}})
	nasID := str(t, nas.json, "id")
	if r := admin.do("POST", "/api/v1/backup-destinations/"+nasID+"/test", nil); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "not mounted") {
		t.Fatalf("failed test: %d %s", r.code, r.body)
	}
	rows := auditFields(t, admin, "backup_destination", destID)
	if strings.Contains(rows, "very-secret-key") || !strings.Contains(rows, `"field":"name"`) || !strings.Contains(rows, `"field":"tested"`) {
		t.Fatalf("destination audit: %s", rows)
	}
	if !strings.Contains(auditFields(t, admin, "backup_destination", nasID), `"new_value":"failed"`) {
		t.Fatal("a failed test was not audited")
	}
	admin.must(http.StatusNoContent, "DELETE", "/api/v1/backup-destinations/"+nasID, nil)

	// Upload an archive the admin downloaded, then restore it.
	up := admin.do("POST", "/api/v1/backup-uploads", nil)
	if up.code != http.StatusBadRequest {
		t.Fatalf("empty upload: %d", up.code)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/backup-uploads", strings.NewReader("ARCHIVE"))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-CSRF-Token", admin.csrf)
	res, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var upload map[string]any
	_ = json.NewDecoder(res.Body).Decode(&upload)
	res.Body.Close()
	sum := sha256.Sum256([]byte("ARCHIVE"))
	if res.StatusCode != http.StatusCreated || upload["checksum"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("upload %d %v", res.StatusCode, upload)
	}
	token := upload["upload_token"].(string)
	if r := admin.do("POST", "/api/v1/restores", map[string]any{"upload_token": token, "passphrase": "wrong-passphrase"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong passphrase: %d", r.code)
	}
	if r := admin.do("POST", "/api/v1/restores", map[string]any{"upload_token": token, "backup_id": id, "passphrase": "synthetic-backup-passphrase"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("two sources: %d", r.code)
	}
	if r := admin.do("POST", "/api/v1/restores", map[string]any{"passphrase": "synthetic-backup-passphrase"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("no source: %d", r.code)
	}
	started := admin.must(http.StatusCreated, "POST", "/api/v1/restores", map[string]any{"upload_token": token, "passphrase": "synthetic-backup-passphrase"})
	if started.json["status"] != "running" || started.json["interrupts_users"] != true || sup.uploadBody != "ARCHIVE" || sup.uploadSum != hex.EncodeToString(sum[:]) ||
		!backupID.MatchString(strings.TrimSuffix(sup.uploadName, ".tgbk")) {
		t.Fatalf("restore = %s; supervisor got %q %q %q", started.body, sup.uploadName, sup.uploadBody, sup.uploadSum)
	}
	if r := admin.do("POST", "/api/v1/restores", map[string]any{"upload_token": token, "passphrase": "synthetic-backup-passphrase"}); r.code != http.StatusUnprocessableEntity {
		t.Fatalf("a consumed upload restored again: %d", r.code)
	}
	admin.must(http.StatusCreated, "POST", "/api/v1/restores", map[string]any{"backup_id": id, "passphrase": "synthetic-backup-passphrase"})
	if sup.restoredFrom != id {
		t.Fatalf("supervisor restored %q", sup.restoredFrom)
	}
	got := admin.must(http.StatusOK, "GET", "/api/v1/restores/restore-20261112T090200Z-00000000000000bb", nil)
	if got.json["status"] != "failed" || got.json["error"] != "files_not_restored" {
		t.Fatal(got.body)
	}
	if r := admin.must(http.StatusOK, "GET", "/api/v1/restores", nil); !strings.Contains(r.body, `"status":"completed"`) {
		t.Fatal(r.body)
	}
	if !strings.Contains(auditFields(t, admin, "restore", id), `"field":"started"`) {
		t.Fatal("restore start not audited")
	}

	// MCP offers and runs the same operations under the same checks.
	list := scoped.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	for _, op := range [][2]string{{"POST", "/api/v1/backups"}, {"GET", "/api/v1/backups/{backup_id}/download"}, {"POST", "/api/v1/backup-destinations"},
		{"PATCH", "/api/v1/backup-destinations/{destination_id}"}, {"POST", "/api/v1/backup-destinations/{destination_id}/test"},
		{"DELETE", "/api/v1/backup-destinations/{destination_id}"}, {"POST", "/api/v1/restores"}, {"GET", "/api/v1/restores/{restore_id}"}, {"POST", "/api/v1/backup-uploads"}} {
		if !strings.Contains(list.body, `"name":"`+toolName(t, op[0], op[1])+`"`) {
			t.Fatalf("tools/list lacks %s %s", op[0], op[1])
		}
	}
	if call := mcpCall(scoped, toolName(t, "POST", "/api/v1/backups"), map[string]any{}); strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("MCP backup: %s", call.body)
	}
	if call := mcpCall(noScope, toolName(t, "POST", "/api/v1/backups"), map[string]any{}); !strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("MCP backup without scope: %s", call.body)
	}
	if !strings.Contains(auditFields(t, admin, "backup", id), `"source":"api_token"`) {
		t.Fatal("MCP backup not audited as api_token")
	}

	// The supervisor's file-bytes route: an export with its trailer, taken back whole.
	exp, _ := http.NewRequest("GET", srv.URL+fileBytesPath, nil)
	exp.Header.Set("Authorization", "Bearer "+supSecret)
	res, err = http.DefaultClient.Do(exp)
	if err != nil {
		t.Fatal(err)
	}
	tarBytes, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-tar" || res.Trailer.Get(exportTrailer) != "complete" || len(tarBytes) == 0 {
		t.Fatalf("export %d %q trailer %q", res.StatusCode, res.Header.Get("Content-Type"), res.Trailer.Get(exportTrailer))
	}
	put, _ := http.NewRequest("PUT", srv.URL+fileBytesPath, strings.NewReader(string(tarBytes)))
	put.Header.Set("Authorization", "Bearer "+supSecret)
	if res, err = http.DefaultClient.Do(put); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("import %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	bad, _ := http.NewRequest("PUT", srv.URL+fileBytesPath, strings.NewReader("not a tar"))
	bad.Header.Set("Authorization", "Bearer "+supSecret)
	if res, err = http.DefaultClient.Do(bad); err != nil || res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad import %v %v", res.StatusCode, err)
	}
	res.Body.Close()
	wrong, _ := http.NewRequest("GET", srv.URL+fileBytesPath, nil)
	wrong.Header.Set("Authorization", "Bearer "+supSecret+"x")
	if res, err = http.DefaultClient.Do(wrong); err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token %v %v", res.StatusCode, err)
	}
	res.Body.Close()
}

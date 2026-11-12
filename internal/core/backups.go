package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// PassphraseFile is where the supervisor reads the backup passphrase
// (compose.yaml BACKUP_PASSPHRASE_FILE). Bootstrap creates it empty and owned
// by the core; the core fills it from the backups.passphrase setting.
const PassphraseFile = "/appsecrets/backup_passphrase"

// fileBytesPath is the supervisor's route into the files service
// (supervisor.CoreFilesPath). It is not a product API: only the supervisor
// token opens it.
const fileBytesPath = "/internal/v1/file-bytes"

// exportTrailer is supervisor.ExportStatusTrailer.
const exportTrailer = "X-Togavsu-Export"

// maxBackupUpload bounds an uploaded archive. The supervisor applies its own
// import bound when the restore starts.
const maxBackupUpload = 64 << 30

// backups fronts the supervisor's backup, destination and restore API with
// the core's admin check, scopes and audit log, and serves the supervisor the
// file bytes it puts in each archive.
type backups struct {
	app      *App
	url      string
	secret   string
	passFile string
	uploads  string
	client   *http.Client
	now      func() time.Time
}

func newBackups(app *App, cfg Config, dataDir string, now func() time.Time) *backups {
	url := cfg.SupervisorURL
	if url == "" {
		url = SupervisorURL
	}
	pass := cfg.BackupPassphraseFile
	if pass == "" {
		if _, err := os.Stat(PassphraseFile); err == nil {
			pass = PassphraseFile
		}
	}
	return &backups{app: app, url: strings.TrimRight(url, "/"), secret: cfg.SupervisorSecret, passFile: pass,
		uploads: filepath.Join(dataDir, "backup-uploads"), client: &http.Client{}, now: now}
}

func (b *backups) mount(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/backups", b.list)
	m.HandleFunc("POST /api/v1/backups", b.create)
	m.HandleFunc("GET /api/v1/backups/{backup_id}", b.get)
	m.HandleFunc("DELETE /api/v1/backups/{backup_id}", b.remove)
	m.HandleFunc("GET /api/v1/backups/{backup_id}/download", b.download)
	m.HandleFunc("GET /api/v1/backup-destinations", b.listDestinations)
	m.HandleFunc("POST /api/v1/backup-destinations", b.createDestination)
	m.HandleFunc("GET /api/v1/backup-destinations/{destination_id}", b.getDestination)
	m.HandleFunc("PATCH /api/v1/backup-destinations/{destination_id}", b.updateDestination)
	m.HandleFunc("DELETE /api/v1/backup-destinations/{destination_id}", b.removeDestination)
	m.HandleFunc("POST /api/v1/backup-destinations/{destination_id}/test", b.testDestination)
	m.HandleFunc("POST /api/v1/backup-uploads", b.upload)
	m.HandleFunc("GET /api/v1/restores", b.listRestores)
	m.HandleFunc("POST /api/v1/restores", b.createRestore)
	m.HandleFunc("GET /api/v1/restores/{restore_id}", b.getRestore)
	m.HandleFunc("GET "+fileBytesPath, b.exportFiles)
	m.HandleFunc("PUT "+fileBytesPath, b.importFiles)
}

// --- supervisor wire ---

// supBackup is supervisor.BackupMeta; supDestination is supervisor.DestinationSpec;
// supRestore is supervisor.RestoreRecord.
type supBackup struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	Reason            string    `json:"reason"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"`
	DestinationErrors []string  `json:"destination_errors,omitempty"`
	Deleting          bool      `json:"deleting,omitempty"`
	Source            string    `json:"source,omitempty"`
	CreatedBy         string    `json:"created_by,omitempty"`
	Files             bool      `json:"files,omitempty"`
}

type supDestination struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	RetentionDays int    `json:"retention_days"`
	Name          string `json:"name,omitempty"`
	Label         string `json:"label,omitempty"`
	CreatedBy     string `json:"created_by,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
	Path          string `json:"path,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	Region        string `json:"region,omitempty"`
	Bucket        string `json:"bucket,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	AccessKey     string `json:"access_key,omitempty"`
	SecretKey     string `json:"secret_key,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	User          string `json:"user,omitempty"`
	IdentityFile  string `json:"identity_file,omitempty"`
	KnownHosts    string `json:"known_hosts,omitempty"`
	RemotePath    string `json:"remote_path,omitempty"`
}

type supRestore struct {
	ID       string    `json:"id"`
	BackupID string    `json:"backup_id"`
	Source   string    `json:"source,omitempty"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

// supError is a supervisor error answer, passed to the caller with its status.
type supError struct {
	Status  int
	Code    string `json:"error"`
	Message string `json:"message"`
}

func (e *supError) Error() string {
	return fmt.Sprintf("supervisor %d %s: %s", e.Status, e.Code, e.Message)
}

var errNoSupervisor = errors.New("backups: the supervisor is not configured")

func (b *backups) call(ctx context.Context, method, path, ctype string, body io.Reader, out any) error {
	res, err := b.send(ctx, method, path, ctype, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}

// send makes one supervisor request and turns an error status into supError.
func (b *backups) send(ctx context.Context, method, path, ctype string, body io.Reader) (*http.Response, error) {
	if b.secret == "" {
		return nil, errNoSupervisor
	}
	req, err := http.NewRequestWithContext(ctx, method, b.url+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+b.secret)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backups: supervisor: %w", err)
	}
	if res.StatusCode >= http.StatusMultipleChoices {
		defer res.Body.Close()
		e := &supError{Status: res.StatusCode}
		_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(e)
		return nil, e
	}
	return res, nil
}

func jsonBody(v any) io.Reader {
	raw, _ := json.Marshal(v)
	return bytes.NewReader(raw)
}

// fail answers a supervisor or validation error. A supervisor 4xx keeps its
// status and code; a 5xx or an unreachable supervisor is 502.
func fail(w http.ResponseWriter, err error) {
	var se *supError
	switch {
	case errors.As(err, &se):
		status, code := se.Status, se.Code
		if status == http.StatusUnauthorized || status >= http.StatusInternalServerError {
			status = http.StatusBadGateway
		}
		if status == http.StatusUnprocessableEntity || status == http.StatusBadRequest {
			code = "validation_failed"
		}
		if code == "" {
			code = "supervisor_error"
		}
		problem(w, status, code, se.Message)
	case errors.Is(err, errNoSupervisor):
		problem(w, http.StatusServiceUnavailable, "not_configured", "backups need the supervisor")
	default:
		var api *domain.APIError
		if errors.As(err, &api) {
			writeErr(w, err)
			return
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		problem(w, http.StatusBadGateway, "supervisor_unavailable", "the supervisor did not answer")
	}
}

func problem(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(domain.APIError{Code: code, Message: msg})
}

// admin is the caller for every route here: an admin session, or a token with
// backups:admin (authenticate checks the contract's scope).
func (b *backups) admin(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, err := b.app.admin(r)
	if err != nil {
		writeErr(w, err)
		return p, false
	}
	return p, true
}

// record writes one audit row; a write the ledger refuses fails the request.
func (b *backups) record(ctx context.Context, p domain.Principal, entity, id, field, value string) error {
	_, err := b.app.Audit.Record(ctx, p, audit.Change{Entity: entity, EntityID: domain.ID(id), Field: field, New: value})
	return err
}

// --- passphrase ---

// syncPassphrase writes the backups.passphrase setting to the file the
// supervisor reads, when it differs. No file configured is a no-op.
func (b *backups) syncPassphrase(ctx context.Context) error {
	if b == nil || b.passFile == "" {
		return nil
	}
	pass, err := b.app.Settings.Get(ctx, "backups.passphrase")
	if err != nil || pass == "" {
		return err
	}
	if cur, err := os.ReadFile(b.passFile); err == nil && strings.TrimSpace(string(cur)) == pass {
		return nil
	}
	f, err := os.OpenFile(b.passFile, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("backups: passphrase file: %w", err)
	}
	if _, err := f.WriteString(pass + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// --- backups ---

func reasonKind(m supBackup) string {
	switch {
	case m.Source != "":
		return "imported"
	case m.Reason == "pre-update":
		return "pre_update"
	case m.Reason == "nightly" || m.Reason == "manual" || m.Reason == "safety":
		return m.Reason
	}
	return "manual"
}

func backupJSON(m supBackup) map[string]any {
	by := m.CreatedBy
	if by == "" {
		by = string(domain.SystemUserID)
	}
	out := map[string]any{"id": m.ID, "created_at": m.CreatedAt.UTC(), "created_by": by, "status": "completed",
		"checksum": m.SHA256, "bytes": m.Bytes, "kind": reasonKind(m), "files": m.Files,
		"destination_errors": m.DestinationErrors}
	if m.DestinationErrors == nil {
		out["destination_errors"] = []string{}
	}
	return out
}

// page is the one page every list here returns.
// ponytail: one page per list, ceiling: the supervisor lists its whole backup volume, destination file and restore ledger on each call, upgrade: cursor pages in the supervisor's list routes, trigger: a list passes 500 rows or takes over 200ms.
func page(n int) map[string]any {
	if n < 1 {
		n = 1
	}
	return map[string]any{"limit": n, "next_cursor": nil}
}

func (b *backups) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	var got struct {
		Backups []supBackup `json:"backups"`
	}
	if err := b.call(r.Context(), http.MethodGet, "/v1/backups", "", nil, &got); err != nil {
		fail(w, err)
		return
	}
	data := make([]map[string]any, 0, len(got.Backups))
	for _, m := range got.Backups {
		data = append(data, backupJSON(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": page(len(data))})
}

func (b *backups) create(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	if err := b.syncPassphrase(r.Context()); err != nil {
		fail(w, err)
		return
	}
	var meta supBackup
	body := jsonBody(map[string]string{"reason": "manual", "created_by": string(p.UserID)})
	if err := b.call(r.Context(), http.MethodPost, "/v1/backups", "application/json", body, &meta); err != nil {
		fail(w, err)
		return
	}
	if err := b.record(r.Context(), p, "backup", meta.ID, "created", meta.SHA256); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, backupJSON(meta))
}

var backupID = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$`)

func (b *backups) id(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("backup_id")
	if !backupID.MatchString(id) {
		writeErr(w, domain.NotFound("backup"))
		return "", false
	}
	return id, true
}

func (b *backups) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	id, ok := b.id(w, r)
	if !ok {
		return
	}
	var meta supBackup
	if err := b.call(r.Context(), http.MethodGet, "/v1/backups/"+id, "", nil, &meta); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, backupJSON(meta))
}

func (b *backups) remove(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	id, ok := b.id(w, r)
	if !ok {
		return
	}
	if err := b.call(r.Context(), http.MethodDelete, "/v1/backups/"+id, "", nil, nil); err != nil {
		fail(w, err)
		return
	}
	if err := b.record(r.Context(), p, "backup", id, "deleted", "true"); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// download streams the encrypted archive to the admin's computer. Taking a
// copy of every record is recorded in the audit log.
func (b *backups) download(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	id, ok := b.id(w, r)
	if !ok {
		return
	}
	res, err := b.send(r.Context(), http.MethodGet, "/v1/backups/"+id+"/archive", "", nil)
	if err != nil {
		fail(w, err)
		return
	}
	defer res.Body.Close()
	if err := b.record(r.Context(), p, "backup", id, "downloaded", "true"); err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.tgbk"`)
	if res.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(res.ContentLength))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, res.Body)
}

// --- destinations ---

var destinationKinds = map[string]string{"local_path": "path", "network_path": "path", "s3": "s3", "sftp": "sftp"}

func destinationJSON(d supDestination) map[string]any {
	kind := d.Label
	if kind == "" {
		kind = map[string]string{"path": "network_path"}[d.Kind]
		if kind == "" {
			kind = d.Kind
		}
	}
	by := d.CreatedBy
	if by == "" {
		by = string(domain.SystemUserID)
	}
	name := d.Name
	if name == "" {
		name = d.ID
	}
	return map[string]any{"id": d.ID, "created_at": d.CreatedAt, "created_by": by, "updated_at": d.UpdatedAt,
		"kind": kind, "name": name, "retention_days": d.RetentionDays}
}

type destinationWrite struct {
	Kind          *string        `json:"kind"`
	Name          *string        `json:"name"`
	RetentionDays *int           `json:"retention_days"`
	Settings      map[string]any `json:"settings"`
}

// apply validates a write and lays it over d. A create needs kind, name and
// retention_days; an edit may leave any of them out.
func (in destinationWrite) apply(d *supDestination, create bool) error {
	if create && (in.Kind == nil || in.Name == nil || in.RetentionDays == nil) {
		return domain.Invalid("body", "kind, name and retention_days are required")
	}
	if in.Kind != nil {
		k, ok := destinationKinds[*in.Kind]
		if !ok {
			return domain.Invalid("kind", "kind is local_path, network_path, s3 or sftp")
		}
		if !create && k != d.Kind {
			return domain.Invalid("kind", "a destination's kind cannot change; add a new destination")
		}
		d.Kind, d.Label = k, *in.Kind
	}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			return domain.Invalid("name", "name is required")
		}
		d.Name = strings.TrimSpace(*in.Name)
	}
	if in.RetentionDays != nil {
		if *in.RetentionDays < 1 {
			return domain.Invalid("retention_days", "retention_days is at least 1")
		}
		d.RetentionDays = *in.RetentionDays
	}
	str := func(k string, v any) (string, error) {
		s, ok := v.(string)
		if !ok {
			return "", domain.Invalid("settings."+k, "must be a string")
		}
		return s, nil
	}
	for k, v := range in.Settings {
		var err error
		switch k {
		case "path":
			d.Path, err = str(k, v)
		case "endpoint":
			d.Endpoint, err = str(k, v)
		case "region":
			d.Region, err = str(k, v)
		case "bucket":
			d.Bucket, err = str(k, v)
		case "prefix":
			d.Prefix, err = str(k, v)
		case "access_key":
			d.AccessKey, err = str(k, v)
		case "secret_key":
			d.SecretKey, err = str(k, v)
		case "host":
			d.Host, err = str(k, v)
		case "user":
			d.User, err = str(k, v)
		case "identity_file":
			d.IdentityFile, err = str(k, v)
		case "known_hosts":
			d.KnownHosts, err = str(k, v)
		case "remote_path":
			d.RemotePath, err = str(k, v)
		case "port":
			n, ok := v.(float64)
			if !ok || n != float64(int(n)) || n < 1 || n > 65535 {
				return domain.Invalid("settings.port", "port is a whole number from 1 to 65535")
			}
			d.Port = int(n)
		default:
			return domain.Invalid("settings."+k, "unknown setting")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *backups) destinations(ctx context.Context) ([]supDestination, error) {
	var got struct {
		Destinations []supDestination `json:"destinations"`
	}
	if err := b.call(ctx, http.MethodGet, "/v1/backup-destinations", "", nil, &got); err != nil {
		return nil, err
	}
	return got.Destinations, nil
}

func (b *backups) destination(ctx context.Context, id string) (supDestination, error) {
	all, err := b.destinations(ctx)
	if err != nil {
		return supDestination{}, err
	}
	for _, d := range all {
		if d.ID == id {
			return d, nil
		}
	}
	return supDestination{}, domain.NotFound("backup destination")
}

func (b *backups) listDestinations(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	all, err := b.destinations(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	data := make([]map[string]any, 0, len(all))
	for _, d := range all {
		data = append(data, destinationJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": page(len(data))})
}

func (b *backups) getDestination(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	d, err := b.destination(r.Context(), r.PathValue("destination_id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, destinationJSON(d))
}

func decodeStrict(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "request body must be a JSON object with the documented fields")
	}
	return nil
}

func (b *backups) createDestination(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	var in destinationWrite
	if err := decodeStrict(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	id, err := domain.NewID()
	if err != nil {
		fail(w, err)
		return
	}
	now := b.now().UTC().Format(time.RFC3339Nano)
	d := supDestination{ID: string(id), CreatedBy: string(p.UserID), CreatedAt: now, UpdatedAt: now}
	if err := in.apply(&d, true); err != nil {
		writeErr(w, err)
		return
	}
	var saved supDestination
	if err := b.call(r.Context(), http.MethodPost, "/v1/backup-destinations", "application/json", jsonBody(d), &saved); err != nil {
		fail(w, err)
		return
	}
	if err := b.record(r.Context(), p, "backup_destination", saved.ID, "created", d.Label+" "+d.Name); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, destinationJSON(saved))
}

func (b *backups) updateDestination(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	var in destinationWrite
	if err := decodeStrict(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	d, err := b.destination(r.Context(), r.PathValue("destination_id"))
	if err != nil {
		fail(w, err)
		return
	}
	before := destinationJSON(d)
	if err := in.apply(&d, false); err != nil {
		writeErr(w, err)
		return
	}
	d.UpdatedAt = b.now().UTC().Format(time.RFC3339Nano)
	var saved supDestination
	if err := b.call(r.Context(), http.MethodPost, "/v1/backup-destinations", "application/json", jsonBody(d), &saved); err != nil {
		fail(w, err)
		return
	}
	after := destinationJSON(saved)
	for _, f := range []string{"name", "retention_days"} {
		if fmt.Sprint(before[f]) != fmt.Sprint(after[f]) {
			if _, err := b.app.Audit.Record(r.Context(), p, audit.Change{Entity: "backup_destination", EntityID: domain.ID(saved.ID),
				Field: f, Old: fmt.Sprint(before[f]), New: fmt.Sprint(after[f])}); err != nil {
				fail(w, err)
				return
			}
		}
	}
	if len(in.Settings) > 0 {
		// Settings can hold credentials; the row names the change, not the values.
		if err := b.record(r.Context(), p, "backup_destination", saved.ID, "settings", "changed"); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, after)
}

func (b *backups) removeDestination(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	id := r.PathValue("destination_id")
	if strings.ContainsAny(id, "/?#") || id == "" {
		writeErr(w, domain.NotFound("backup destination"))
		return
	}
	if err := b.call(r.Context(), http.MethodDelete, "/v1/backup-destinations/"+id, "", nil, nil); err != nil {
		fail(w, err)
		return
	}
	if err := b.record(r.Context(), p, "backup_destination", id, "deleted", "true"); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testDestination writes and reads a probe on the destination. The result,
// either way, is recorded.
func (b *backups) testDestination(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	d, err := b.destination(r.Context(), r.PathValue("destination_id"))
	if err != nil {
		fail(w, err)
		return
	}
	terr := b.call(r.Context(), http.MethodPost, "/v1/backup-destinations/"+d.ID+"/test", "", nil, nil)
	result := "ok"
	if terr != nil {
		result = "failed"
	}
	if err := b.record(r.Context(), p, "backup_destination", d.ID, "tested", result); err != nil {
		fail(w, err)
		return
	}
	if terr != nil {
		var se *supError
		if errors.As(terr, &se) {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "the destination test failed: "+se.Message)
			return
		}
		fail(w, terr)
		return
	}
	writeJSON(w, http.StatusOK, destinationJSON(d))
}

// --- uploads and restores ---

var uploadToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

// upload keeps an archive the admin holds (for one downloaded earlier) on
// the core volume until a restore takes it. backup_id, the archive's file
// name without .tgbk, is kept beside it: the archive's encrypted manifest
// names that id, and the supervisor checks it.
func (b *backups) upload(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	named := strings.TrimSuffix(r.URL.Query().Get("backup_id"), ".tgbk")
	if named != "" && !backupID.MatchString(named) {
		writeErr(w, domain.Invalid("backup_id", "backup_id is the archive's file name, such as 20261112T090000Z-0123456789abcdef"))
		return
	}
	if err := os.MkdirAll(b.uploads, 0o700); err != nil {
		fail(w, err)
		return
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		fail(w, err)
		return
	}
	token := hex.EncodeToString(raw[:])
	path := filepath.Join(b.uploads, token+".tgbk")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fail(w, err)
		return
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), http.MaxBytesReader(w, r.Body, maxBackupUpload))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n == 0 {
		err = domain.Invalid("body", "the archive is empty")
	}
	if err != nil {
		_ = os.Remove(path)
		var api *domain.APIError
		if !errors.As(err, &api) {
			err = domain.Invalid("body", "the archive upload did not finish")
		}
		writeErr(w, err)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if named != "" {
		err = os.WriteFile(filepath.Join(b.uploads, token+".id"), []byte(named), 0o600)
	}
	id, nerr := domain.NewID()
	if err == nil {
		err = nerr
	}
	if err == nil {
		err = b.record(r.Context(), p, "backup_upload", string(id), "uploaded", sum)
	}
	if err != nil {
		_ = os.Remove(path)
		_ = os.Remove(filepath.Join(b.uploads, token+".id"))
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"upload_token": token, "checksum": sum, "bytes": n})
}

func restoreJSON(rec supRestore, by string) map[string]any {
	status := rec.Status
	switch status {
	case "completed", "failed":
	default:
		status = "running"
	}
	if by == "" {
		by = string(domain.SystemUserID)
	}
	var backup any
	if rec.BackupID != "" {
		backup = rec.BackupID
	}
	return map[string]any{"id": rec.ID, "created_at": rec.Started.UTC(), "created_by": by, "backup_id": backup,
		"status": status, "error": rec.Error, "interrupts_users": true}
}

func (b *backups) createRestore(w http.ResponseWriter, r *http.Request) {
	p, ok := b.admin(w, r)
	if !ok {
		return
	}
	var in struct {
		BackupID    *string `json:"backup_id"`
		UploadToken string  `json:"upload_token"`
		Passphrase  string  `json:"passphrase"`
	}
	if err := decodeStrict(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	backup := ""
	if in.BackupID != nil {
		backup = *in.BackupID
	}
	if (backup == "") == (in.UploadToken == "") {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "give one source: backup_id or upload_token")
		return
	}
	if in.Passphrase == "" {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "passphrase is required")
		return
	}
	// The supervisor decrypts with this installation's passphrase; the admin
	// proves they hold it before users are interrupted.
	want, err := b.app.Settings.Get(r.Context(), "backups.passphrase")
	if err != nil {
		fail(w, err)
		return
	}
	if want == "" || subtle.ConstantTimeCompare([]byte(in.Passphrase), []byte(want)) != 1 {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "the passphrase does not match this installation's backup passphrase")
		return
	}
	if err := b.syncPassphrase(r.Context()); err != nil {
		fail(w, err)
		return
	}
	var rec supRestore
	if backup != "" {
		if !backupID.MatchString(backup) {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "backup_id is not a backup")
			return
		}
		// The restore replaces this database, so the row is written first.
		if err := b.record(r.Context(), p, "restore", backup, "started", "backup "+backup); err != nil {
			fail(w, err)
			return
		}
		err = b.call(r.Context(), http.MethodPost, "/v1/restores", "application/json", jsonBody(map[string]string{"backup_id": backup}), &rec)
	} else {
		if !uploadToken.MatchString(in.UploadToken) {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "upload_token is not an upload")
			return
		}
		path := filepath.Join(b.uploads, in.UploadToken+".tgbk")
		if _, serr := os.Stat(path); serr != nil {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "upload_token is not an upload")
			return
		}
		if err := b.record(r.Context(), p, "restore", in.UploadToken[:32], "started", "upload"); err != nil {
			fail(w, err)
			return
		}
		rec, err = b.restoreUpload(r.Context(), in.UploadToken)
		if err == nil {
			_ = os.Remove(path)
			_ = os.Remove(filepath.Join(b.uploads, in.UploadToken+".id"))
		}
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, restoreJSON(rec, string(p.UserID)))
}

// restoreUpload restores an uploaded archive. One the supervisor already
// holds byte for byte (the admin downloaded it from this installation) is
// restored by its id. Any other is sent with its checksum sidecar, under the
// backup id it was uploaded with, to the supervisor's restore upload, which
// checks, decrypts and admits it.
func (b *backups) restoreUpload(ctx context.Context, token string) (supRestore, error) {
	path := filepath.Join(b.uploads, token+".tgbk")
	sum, err := fileSHA(path)
	if err != nil {
		return supRestore{}, err
	}
	var held struct {
		Backups []supBackup `json:"backups"`
	}
	if err := b.call(ctx, http.MethodGet, "/v1/backups", "", nil, &held); err != nil {
		return supRestore{}, err
	}
	var rec supRestore
	for _, m := range held.Backups {
		if m.SHA256 == sum && !m.Deleting {
			err := b.call(ctx, http.MethodPost, "/v1/restores", "application/json", jsonBody(map[string]string{"backup_id": m.ID}), &rec)
			return rec, err
		}
	}
	raw, err := os.ReadFile(filepath.Join(b.uploads, token+".id"))
	id := strings.TrimSpace(string(raw))
	if err != nil || !backupID.MatchString(id) {
		return supRestore{}, domain.Invalid("backup_id", "this installation does not hold the archive; upload it again with backup_id set to its file name")
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			part, err := mw.CreateFormFile("checksum", id+".sha256")
			if err != nil {
				return err
			}
			if _, err := io.WriteString(part, sum+"\n"); err != nil {
				return err
			}
			part, err = mw.CreateFormFile("archive", id+".tgbk")
			if err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(part, f); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	err = b.call(ctx, http.MethodPost, "/v1/restores/upload", mw.FormDataContentType(), pr, &rec)
	_ = pr.Close()
	return rec, err
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var restoreID = regexp.MustCompile(`^restore-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{16}$`)

func (b *backups) getRestore(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	id := r.PathValue("restore_id")
	if !restoreID.MatchString(id) {
		writeErr(w, domain.NotFound("restore"))
		return
	}
	var rec supRestore
	if err := b.call(r.Context(), http.MethodGet, "/v1/restores/"+id, "", nil, &rec); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, restoreJSON(rec, ""))
}

func (b *backups) listRestores(w http.ResponseWriter, r *http.Request) {
	if _, ok := b.admin(w, r); !ok {
		return
	}
	var got struct {
		Restores []supRestore `json:"restores"`
	}
	if err := b.call(r.Context(), http.MethodGet, "/v1/restores", "", nil, &got); err != nil {
		fail(w, err)
		return
	}
	data := make([]map[string]any, 0, len(got.Restores))
	for _, rec := range got.Restores {
		data = append(data, restoreJSON(rec, ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": page(len(data))})
}

// --- the supervisor's file bytes ---

func (b *backups) supervisor(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && b.secret != "" && subtle.ConstantTimeCompare([]byte(token), []byte(b.secret)) == 1
}

// exportFiles streams every file version and rendition, Markdown copies
// included, from every backend the backup covers (files.ExportBytes). The
// trailer says the stream is whole; a failure part way cannot change the
// status already sent, so the supervisor refuses a stream without it.
func (b *backups) exportFiles(w http.ResponseWriter, r *http.Request) {
	if !b.supervisor(r) {
		writeErr(w, domain.Unauthenticated("the supervisor token is required"))
		return
	}
	w.Header().Set("Trailer", exportTrailer)
	w.Header().Set("Content-Type", "application/x-tar")
	w.WriteHeader(http.StatusOK)
	_, err := b.app.Files.ExportBytes(r.Context(), domain.SystemPrincipal(), w, nil)
	if err != nil {
		w.Header().Set(exportTrailer, "failed")
		return
	}
	w.Header().Set(exportTrailer, "complete")
}

// importFiles takes an export back after a restore: each object goes where
// the restored database says it lives and is checked there (files.RestoreBytes).
func (b *backups) importFiles(w http.ResponseWriter, r *http.Request) {
	if !b.supervisor(r) {
		writeErr(w, domain.Unauthenticated("the supervisor token is required"))
		return
	}
	rep, err := b.app.Files.RestoreBytes(r.Context(), domain.SystemPrincipal(), r.Body)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "files_not_restored", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

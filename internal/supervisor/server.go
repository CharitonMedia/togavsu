package supervisor

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// NewMux is the authenticated local API. /health is open so the container
// health check does not need the bearer token in process arguments.
func NewMux(token string, ops *Operations, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(ScrubHandler{Next: slog.NewTextHandler(io.Discard, nil)})
	}
	mux := http.NewServeMux()
	h := &handlers{ops: ops, log: log}
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /v1/updates", h.postUpdate)
	mux.HandleFunc("GET /v1/updates", h.listUpdates)
	mux.HandleFunc("POST /v1/modules/{name}/start", h.module("start"))
	mux.HandleFunc("POST /v1/modules/{name}/stop", h.module("stop"))
	mux.HandleFunc("POST /v1/backups", h.postBackup)
	mux.HandleFunc("GET /v1/backups", h.listBackups)
	mux.HandleFunc("GET /v1/backups/{id}/archive", h.downloadBackup)
	mux.HandleFunc("GET /v1/backups/{id}", h.getBackup)
	mux.HandleFunc("DELETE /v1/backups/{id}", h.deleteBackup)
	mux.HandleFunc("POST /v1/backup-destinations", h.postDestination)
	mux.HandleFunc("GET /v1/backup-destinations", h.listDestinations)
	mux.HandleFunc("POST /v1/backup-destinations/{id}/test", h.testDestination)
	mux.HandleFunc("DELETE /v1/backup-destinations/{id}", h.deleteDestination)
	mux.HandleFunc("POST /v1/restores", h.postRestore)
	mux.HandleFunc("GET /v1/restores/{id}", h.getRestore)
	mux.HandleFunc("POST /v1/test-restores", h.postTestRestore)
	return logging(log, authenticate(token, mux))
}

type handlers struct {
	ops *Operations
	log *slog.Logger
}

func (h *handlers) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) postUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Force bool `json:"force"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.ops.Update(r.Context(), body.Force)
	if err != nil {
		status := 0
		writeAPI(w, err, result, &status)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handlers) listUpdates(w http.ResponseWriter, _ *http.Request) {
	events, err := h.events("update")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updates": events})
}

func (h *handlers) module(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if h.ops.Runtime == nil {
			writeErr(w, ErrNotFound)
			return
		}
		var err error
		if action == "start" {
			err = h.ops.Runtime.StartModule(r.Context(), name)
		} else {
			err = h.ops.Runtime.StopModule(r.Context(), name)
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"module": name, "status": action})
	}
}

func (h *handlers) postBackup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Reason == "" {
		body.Reason = reasonManual
	}
	meta, err := h.ops.CreateBackup(r.Context(), body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (h *handlers) listBackups(w http.ResponseWriter, _ *http.Request) {
	all, err := h.ops.Store.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	if all == nil {
		all = []BackupMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": all})
}

func (h *handlers) getBackup(w http.ResponseWriter, r *http.Request) {
	meta, err := h.ops.Store.lookup(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (h *handlers) downloadBackup(w http.ResponseWriter, r *http.Request) {
	rc, meta, err := h.ops.Store.Open(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+meta.ID+".tgbk\"")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

func (h *handlers) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := h.ops.Store.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) postDestination(w http.ResponseWriter, r *http.Request) {
	var spec DestinationSpec
	if err := decode(r, &spec); err != nil {
		writeErr(w, err)
		return
	}
	if h.ops.Store.Book == nil {
		writeErr(w, errors.New("destinations are not configured"))
		return
	}
	saved, err := h.ops.Store.Book.Put(spec)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, saved.Public())
}

func (h *handlers) listDestinations(w http.ResponseWriter, _ *http.Request) {
	if h.ops.Store.Book == nil {
		writeJSON(w, http.StatusOK, map[string]any{"destinations": []DestinationSpec{}})
		return
	}
	specs, err := h.ops.Store.Book.Specs()
	if err != nil {
		writeErr(w, err)
		return
	}
	pub := make([]DestinationSpec, 0, len(specs))
	for _, spec := range specs {
		pub = append(pub, spec.Public())
	}
	writeJSON(w, http.StatusOK, map[string]any{"destinations": pub})
}

func (h *handlers) testDestination(w http.ResponseWriter, r *http.Request) {
	if h.ops.Store.Book == nil {
		writeErr(w, ErrNotFound)
		return
	}
	dest, err := h.ops.Store.Book.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := dest.Test(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) deleteDestination(w http.ResponseWriter, r *http.Request) {
	if h.ops.Store.Book == nil {
		writeErr(w, ErrNotFound)
		return
	}
	if err := h.ops.Store.Book.Delete(r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) postRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BackupID string `json:"backup_id"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.BackupID == "" {
		writeErr(w, fmtBad("backup_id"))
		return
	}
	if err := h.ops.rejectInvalid(r.Context(), body.BackupID); err != nil {
		rec := RestoreRecord{ID: body.BackupID + "-restore", BackupID: body.BackupID, Started: time.Now().UTC()}
		writeAPI(w, err, h.ops.finish(rec, err), nil)
		return
	}
	rec := RestoreRecord{ID: body.BackupID + "-restore", BackupID: body.BackupID, Status: "accepted", Started: time.Now().UTC()}
	h.ops.PutJob(rec)
	go func() {
		done, _ := h.ops.runAccepted(body.BackupID)
		h.ops.PutJob(done)
	}()
	writeJSON(w, http.StatusAccepted, rec)
}

func (h *handlers) getRestore(w http.ResponseWriter, r *http.Request) {
	if rec, ok := h.ops.Job(r.PathValue("id")); ok {
		writeJSON(w, http.StatusOK, rec)
		return
	}
	events, err := h.events("restore")
	if err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Fields["id"] == id {
			writeJSON(w, http.StatusOK, events[i])
			return
		}
	}
	writeErr(w, ErrNotFound)
}

func (h *handlers) postTestRestore(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BackupID string `json:"backup_id"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.ops.TestRestore(r.Context(), body.BackupID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) events(kind string) ([]Event, error) {
	if h.ops.Ledger == nil {
		return []Event{}, nil
	}
	return h.ops.Ledger.List(kind)
}

func fmtBad(field string) error {
	return errors.Join(ErrBadRequest, errors.New(field))
}

func decode(r *http.Request, dest any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil && !errors.Is(err, io.EOF) {
		return ErrBadRequest
	}
	return nil
}

func authenticate(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		got := bearer(r.Header.Get("Authorization"))
		if len(want) == 0 || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			writeErr(w, ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return header
	}
	return header[len(prefix):]
}

func logging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func writeErr(w http.ResponseWriter, err error) {
	writeAPI(w, err, nil, nil)
}

func writeAPI(w http.ResponseWriter, err error, extra any, _ *int) {
	code, status := PublicCode(err)
	body := map[string]any{"error": code, "message": publicMessage(err)}
	if extra != nil {
		body["result"] = extra
	}
	writeJSON(w, status, body)
}

func publicMessage(err error) string {
	code, status := PublicCode(err)
	if status >= 500 && code == "internal" {
		return "internal error"
	}
	return Scrub(err.Error())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

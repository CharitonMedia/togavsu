package editor

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves the editor's API operations at the contract's paths, the edit page,
// the browser editor build at /editor/ and New Pleading from a matter. Principal comes
// from identity: in the running core it authenticates the session or bearer token,
// enforces CSRF on cookie writes and the route's token scopes (files:read, files:write,
// editor:run). Every access decision is made again in the service.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
	// Pages is web/templates/editor; nil serves the JSON API only.
	Pages     fs.FS
	templates *template.Template
	// Dist is the browser editor build (trunk's dist folder); nil leaves /editor/ unserved.
	Dist   fs.FS
	assets *assets

	once sync.Once
	mux  *http.ServeMux
}

// NewHandler parses the page templates and reads the editor build's index.
func NewHandler(svc *Service, pages fs.FS, dist fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Principal: principal, Pages: pages, Dist: dist}
	if pages != nil {
		t, err := template.New("editor").ParseFS(pages, "*.html")
		if err != nil {
			return nil, err
		}
		h.templates = t
	}
	if dist != nil {
		a, err := loadAssets(dist)
		if err != nil {
			return nil, err
		}
		h.assets = a
	}
	return h, nil
}

// DistDir opens a trunk dist folder on disk, or nil when dir is empty.
func DistDir(dir string) fs.FS {
	if dir == "" {
		return nil
	}
	return os.DirFS(dir)
}

// Owns reports whether r is one of the editor's routes. The core dispatches those to
// Routes() before the files mux: the PRD's /files/{id}/edit and the files tab's
// /files/static/{name} cannot share one Go ServeMux (both match /files/static/edit).
func (h *Handler) Owns(r *http.Request) bool {
	h.once.Do(func() { h.mux = h.Routes() })
	_, pattern := h.mux.Handler(r)
	return pattern != ""
}

// Routes returns the mux.
func (h *Handler) Routes() *http.ServeMux {
	m := http.NewServeMux()
	ok, created := http.StatusOK, http.StatusCreated
	file := func(r *http.Request) domain.ID { return domain.ID(r.PathValue("file_id")) }
	profile := func(r *http.Request) domain.ID { return domain.ID(r.PathValue("court_profile_id")) }
	type conversionBody struct {
		Format string `json:"format"`
	}

	m.HandleFunc("GET /api/v1/editor", op(h, ok, func(r *http.Request, p domain.Principal) (Info, error) {
		return PinnedInfo(), nil
	}))
	m.HandleFunc("GET /api/v1/editor/commands", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		if err := requireEditor(p); err != nil {
			return nil, err
		}
		return listed(h.Svc.Commands(r.Context()))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/editor-runs", opBody(h, created, func(r *http.Request, p domain.Principal, in RunRequest) (RunResult, error) {
		return h.Svc.Run(r.Context(), p, file(r), in)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/conversions", opBody(h, created, func(r *http.Request, p domain.Principal, in conversionBody) (ConversionResult, error) {
		return h.Svc.Convert(r.Context(), p, file(r), in.Format)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/lock", op(h, ok, func(r *http.Request, p domain.Principal) (EditLock, error) {
		return h.Svc.AcquireLock(r.Context(), p, file(r))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/lock/renew", op(h, ok, func(r *http.Request, p domain.Principal) (EditLock, error) {
		return h.Svc.RenewLock(r.Context(), p, file(r))
	}))
	m.HandleFunc("DELETE /api/v1/files/{file_id}/lock", op(h, http.StatusNoContent, func(r *http.Request, p domain.Principal) (struct{}, error) {
		return struct{}{}, h.Svc.ReleaseLock(r.Context(), p, file(r))
	}))
	m.HandleFunc("GET /api/v1/court-profiles", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Profiles(r.Context(), p))
	}))
	m.HandleFunc("POST /api/v1/court-profiles", opBody(h, created, func(r *http.Request, p domain.Principal, in ProfileWrite) (CourtProfile, error) {
		return h.Svc.CreateProfile(r.Context(), p, in)
	}))
	m.HandleFunc("GET /api/v1/court-profiles/{court_profile_id}", op(h, ok, func(r *http.Request, p domain.Principal) (CourtProfile, error) {
		return h.Svc.Profile(r.Context(), p, profile(r))
	}))
	m.HandleFunc("PATCH /api/v1/court-profiles/{court_profile_id}", opBody(h, ok, func(r *http.Request, p domain.Principal, in ProfileWrite) (CourtProfile, error) {
		return h.Svc.UpdateProfile(r.Context(), p, profile(r), in)
	}))
	m.HandleFunc("POST /api/v1/court-profiles/{court_profile_id}/verify", op(h, ok, func(r *http.Request, p domain.Principal) (CourtProfile, error) {
		return h.Svc.VerifyProfile(r.Context(), p, profile(r))
	}))
	h.pageRoutes(m)
	return m
}

type handlerFunc[T any] func(r *http.Request, p domain.Principal) (T, error)

func (h *Handler) who(r *http.Request) (domain.Principal, error) {
	if h.Principal == nil {
		return domain.Principal{}, domain.Unauthenticated("sign in is required")
	}
	return h.Principal(r)
}

func op[T any](h *Handler, code int, run handlerFunc[T]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.who(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		out, err := run(r, p)
		if err != nil {
			writeErr(w, err)
			return
		}
		if code == http.StatusNoContent {
			w.WriteHeader(code)
			return
		}
		writeJSON(w, code, out)
	}
}

func opBody[In, T any](h *Handler, code int, run func(r *http.Request, p domain.Principal, in In) (T, error)) http.HandlerFunc {
	return op(h, code, func(r *http.Request, p domain.Principal) (T, error) {
		var in In
		dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			var zero T
			return zero, domain.Invalid("body", "request body must match the operation's JSON schema: "+err.Error())
		}
		return run(r, p, in)
	})
}

func listed[T any](rows []T, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []T{}
	}
	return map[string]any{"data": rows, "page": map[string]any{"next_cursor": nil}}, nil
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// lockHeldBody is the edit_lock_held problem: the holder's identity and since when.
type lockHeldBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Holder  struct {
		UserID domain.ID `json:"user_id"`
		Name   string    `json:"name"`
	} `json:"holder"`
	Since     string `json:"since"`
	RenewedAt string `json:"renewed_at"`
}

// Status maps an editor error to its HTTP status.
func Status(err error) int {
	var held *LockHeld
	if errors.As(err, &held) {
		return http.StatusConflict
	}
	var api *domain.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case CodeCommandRefused:
			return http.StatusUnprocessableEntity
		case CodeEditLockHeld:
			return http.StatusConflict
		}
		return api.HTTPStatus()
	}
	return http.StatusInternalServerError
}

func writeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	var held *LockHeld
	if errors.As(err, &held) {
		var b lockHeldBody
		b.Code, b.Message = CodeEditLockHeld, held.Error()
		b.Holder.UserID, b.Holder.Name = held.Holder, held.HolderName
		b.Since, b.RenewedAt = stamp(held.Since), stamp(held.RenewedAt)
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(b)
		return
	}
	var api *domain.APIError
	if errors.As(err, &api) {
		if api.Code == domain.CodeRateLimited {
			w.Header().Set("Retry-After", "5")
		}
		w.WriteHeader(Status(err))
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(domain.APIError{Code: "internal", Message: "internal error"})
}

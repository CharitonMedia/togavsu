package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves the files, uploads, folders, storage-backend and storage-move
// operations of the API contract, and the Files tab. Principal comes from identity:
// in the running core it authenticates the session or bearer token, enforces CSRF on
// cookie writes and the route's token scopes (files:read, files:write). Every access
// decision is made again in the service.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
	Templates *template.Template
	FS        fs.FS
	// Background runs long work, such as a storage move, after the response.
	Background func(func())
}

// NewHandler parses the Files tab templates from fsys (web/templates/files). A nil
// fsys serves the JSON API only.
func NewHandler(svc *Service, fsys fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Principal: principal, FS: fsys, Background: func(f func()) { go f() }}
	if fsys != nil {
		t, err := template.New("files").Funcs(template.FuncMap{"bytes": humanBytes}).ParseFS(fsys, "*.html")
		if err != nil {
			return nil, err
		}
		h.Templates = t
	}
	return h, nil
}

// Routes returns the mux. Paths and methods are the published contract's.
func (h *Handler) Routes() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/files", h.list)
	m.HandleFunc("POST /api/v1/files", h.publish)
	m.HandleFunc("GET /api/v1/files/{file_id}", h.get)
	m.HandleFunc("PATCH /api/v1/files/{file_id}", h.metadata)
	m.HandleFunc("POST /api/v1/files/{file_id}/rename", h.rename)
	m.HandleFunc("POST /api/v1/files/{file_id}/move", h.move)
	m.HandleFunc("POST /api/v1/files/{file_id}/status", h.status)
	m.HandleFunc("POST /api/v1/files/{file_id}/trash", h.trash)
	m.HandleFunc("POST /api/v1/files/{file_id}/restore", h.restore)
	m.HandleFunc("POST /api/v1/files/{file_id}/purge", h.purge)
	m.HandleFunc("GET /api/v1/files/{file_id}/history", h.history)
	m.HandleFunc("GET /api/v1/files/{file_id}/versions", h.versions)
	m.HandleFunc("POST /api/v1/files/{file_id}/versions", h.addVersion)
	m.HandleFunc("GET /api/v1/files/{file_id}/versions/{version_id}/download", h.download)
	m.HandleFunc("POST /api/v1/files/{file_id}/versions/{version_id}/restore", h.restoreVersion)
	m.HandleFunc("POST /api/v1/files/{file_id}/versions/{version_id}/quarantine", h.quarantine)
	m.HandleFunc("GET /api/v1/files/{file_id}/renditions", h.renditions)
	m.HandleFunc("GET /api/v1/files/{file_id}/renditions/{rendition_id}", h.rendition)
	m.HandleFunc("GET /api/v1/files/{file_id}/links", h.links)
	m.HandleFunc("POST /api/v1/files/{file_id}/links", h.link)
	m.HandleFunc("POST /api/v1/file-uploads", h.createUpload)
	m.HandleFunc("GET /api/v1/file-uploads/{upload_id}", h.getUpload)
	m.HandleFunc("PUT /api/v1/file-uploads/{upload_id}", h.putChunk)
	m.HandleFunc("POST /api/v1/file-uploads/{upload_id}/complete", h.completeUpload)
	m.HandleFunc("POST /api/v1/file-archives", h.archive)
	m.HandleFunc("GET /api/v1/folders", h.folders)
	m.HandleFunc("POST /api/v1/folders", h.createFolder)
	m.HandleFunc("GET /api/v1/folders/{folder_id}", h.getFolder)
	m.HandleFunc("PATCH /api/v1/folders/{folder_id}", h.updateFolder)
	m.HandleFunc("DELETE /api/v1/folders/{folder_id}", h.deleteFolder)
	m.HandleFunc("GET /api/v1/storage-backends", h.backends)
	m.HandleFunc("POST /api/v1/storage-backends", h.createBackend)
	m.HandleFunc("GET /api/v1/storage-backends/{storage_backend_id}", h.getBackend)
	m.HandleFunc("PATCH /api/v1/storage-backends/{storage_backend_id}", h.updateBackend)
	m.HandleFunc("POST /api/v1/storage-backends/{storage_backend_id}/test", h.testBackend)
	m.HandleFunc("GET /api/v1/storage-moves", h.moves)
	m.HandleFunc("POST /api/v1/storage-moves", h.createMove)
	m.HandleFunc("GET /api/v1/storage-moves/{storage_move_id}", h.getMove)
	m.HandleFunc("POST /api/v1/storage-moves/{storage_move_id}/confirm", h.confirmMove)
	if h.Templates != nil {
		m.HandleFunc("GET /files/static/{name}", h.static)
		m.HandleFunc("GET /practice/matters/{matter_id}/files", h.matterPage)
		m.HandleFunc("GET /files/admin/storage", h.storagePage)
	}
	return m
}

func (h *Handler) who(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	if h.Principal == nil {
		writeErr(w, domain.Unauthenticated("sign in is required"))
		return domain.Principal{}, false
	}
	p, err := h.Principal(r)
	if err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	return p, true
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		status := api.HTTPStatus()
		if api == ErrQuarantined {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(domain.APIError{Code: "internal", Message: "internal error"})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return domain.Invalid("body", "request body must match the operation's JSON schema: "+err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) domain.ID { return domain.ID(r.PathValue(name)) }

func page(data any, next domain.ID) map[string]any {
	pg := map[string]any{"next_cursor": nil}
	if next != "" {
		pg["next_cursor"] = string(next)
	}
	return map[string]any{"data": data, "page": pg}
}

func (h *Handler) limit(r *http.Request) (int, error) {
	ctx := r.Context()
	def, err := h.Svc.intSetting(ctx, "api.page_size")
	if err != nil {
		return int(zero), err
	}
	max, err := h.Svc.intSetting(ctx, "api.max_page_size")
	if err != nil {
		return int(zero), err
	}
	n := def
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err = strconv.ParseInt(raw, 10, 64); err != nil || n < zero+1 {
			return int(zero), domain.Invalid("limit", "limit must be a positive integer")
		}
	}
	if n > max {
		n = max
	}
	return int(n), nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := h.limit(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	lq := ListQuery{MatterID: domain.ID(q.Get("matter_id")), ContactID: domain.ID(q.Get("contact_id")), Text: q.Get("q"),
		Trashed: q.Get("trashed") == "true", Cursor: domain.ID(q.Get("cursor")), Limit: limit}
	if q.Has("folder_id") {
		f := domain.ID(q.Get("folder_id"))
		lq.FolderID = &f
	}
	rows, next, err := h.Svc.List(r.Context(), p, lq)
	if err != nil {
		writeErr(w, err)
		return
	}
	if rows == nil {
		rows = []FileView{}
	}
	writeJSON(w, http.StatusOK, page(rows, next))
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in Publish
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.PublishUpload(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.Get(r.Context(), p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) fileAction(w http.ResponseWriter, r *http.Request, body any, run func(p domain.Principal, id domain.ID) (FileView, error)) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if body != nil {
		if err := decode(r, body); err != nil {
			writeErr(w, err)
			return
		}
	}
	v, err := run(p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) metadata(w http.ResponseWriter, r *http.Request) {
	var in Metadata
	h.fileAction(w, r, &in, func(p domain.Principal, id domain.ID) (FileView, error) {
		return h.Svc.UpdateMetadata(r.Context(), p, id, in)
	})
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	h.fileAction(w, r, &in, func(p domain.Principal, id domain.ID) (FileView, error) {
		return h.Svc.Rename(r.Context(), p, id, in.Name)
	})
}

func (h *Handler) move(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FolderID *domain.ID `json:"folder_id"`
	}
	h.fileAction(w, r, &in, func(p domain.Principal, id domain.ID) (FileView, error) {
		var folder domain.ID
		if in.FolderID != nil {
			folder = *in.FolderID
		}
		return h.Svc.MoveToFolder(r.Context(), p, id, folder)
	})
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	h.fileAction(w, r, &in, func(p domain.Principal, id domain.ID) (FileView, error) {
		return h.Svc.MarkStatus(r.Context(), p, id, in.Status)
	})
}

func (h *Handler) trash(w http.ResponseWriter, r *http.Request) {
	h.fileAction(w, r, &struct{}{}, func(p domain.Principal, id domain.ID) (FileView, error) {
		return h.Svc.Trash(r.Context(), p, id)
	})
}

func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	h.fileAction(w, r, &struct{}{}, func(p domain.Principal, id domain.ID) (FileView, error) {
		return h.Svc.Restore(r.Context(), p, id)
	})
}

func (h *Handler) purge(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		ConfirmName string `json:"confirm_name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.Purge(r.Context(), p, pathID(r, "file_id"), in.ConfirmName); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.History(r.Context(), p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) versions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.Versions(r.Context(), p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) addVersion(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in Publish
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.AddUploadedVersion(r.Context(), p, pathID(r, "file_id"), in.UploadID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// serve streams a verified download. A failed check after the headers are out
// aborts the connection, so the client sees a broken transfer, never a clean one.
func serve(w http.ResponseWriter, r *http.Request, d *Download) {
	defer d.Close()
	disp := "attachment"
	if r.URL.Query().Get("disposition") == "inline" && previewable(d.MediaType) {
		disp = "inline"
	} else {
		// Anything that is not a plain preview type downloads in a sandbox, so an
		// uploaded HTML or SVG file never runs as this origin.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	}
	w.Header().Set("Content-Type", d.MediaType)
	w.Header().Set("Content-Length", fmt.Sprint(d.Size))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": d.Name}))
	w.Header().Set("X-Content-SHA256", d.SHA256)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, d.Body); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// previewable is what opens in the browser (PRD 8.19): PDFs, raster images, audio,
// video and plain text. With nosniff the browser treats the bytes as that type only.
func previewable(mediaType string) bool {
	t, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return false
	}
	switch {
	case t == "application/pdf", t == "text/plain", t == "text/markdown":
		return true
	case t == "image/svg+xml":
		return false
	case strings.HasPrefix(t, "image/"), strings.HasPrefix(t, "audio/"), strings.HasPrefix(t, "video/"):
		return true
	}
	return false
}

func confirmed(r *http.Request) bool {
	return r.URL.Query().Get("confirm") == "true"
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	d, err := h.Svc.OpenVersion(r.Context(), p, pathID(r, "file_id"), pathID(r, "version_id"), confirmed(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	serve(w, r, d)
}

func (h *Handler) restoreVersion(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.RestoreVersion(r.Context(), p, pathID(r, "file_id"), pathID(r, "version_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) quarantine(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Quarantined *bool `json:"quarantined"`
	}
	if err := decode(r, &in); err != nil || in.Quarantined == nil {
		writeErr(w, domain.Invalid("quarantined", "quarantined is required"))
		return
	}
	v, err := h.Svc.Quarantine(r.Context(), p, pathID(r, "file_id"), pathID(r, "version_id"), *in.Quarantined)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) renditions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.Renditions(r.Context(), p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) rendition(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	d, err := h.Svc.OpenRendition(r.Context(), p, pathID(r, "file_id"), pathID(r, "rendition_id"), confirmed(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	serve(w, r, d)
}

func (h *Handler) links(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.Links(r.Context(), p, pathID(r, "file_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) link(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		ToFileID     domain.ID `json:"to_file_id"`
		Relationship string    `json:"relationship"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.Link(r.Context(), p, pathID(r, "file_id"), in.ToFileID, in.Relationship)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in UploadRequest
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.CreateUpload(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) getUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.GetUpload(r.Context(), p, pathID(r, "upload_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) putChunk(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	start, end, total, err := ParseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.PutChunk(r.Context(), p, pathID(r, "upload_id"), start, end, total, r.Body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) completeUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.CompleteUpload(r.Context(), p, pathID(r, "upload_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// archive checks every selected file before the first byte, then streams the zip.
func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		FileIDs           []domain.ID `json:"file_ids"`
		FolderID          *domain.ID  `json:"folder_id"`
		ConfirmQuarantine bool        `json:"confirm_quarantine"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	var folder domain.ID
	if in.FolderID != nil {
		folder = *in.FolderID
	}
	pw := &pendingWriter{w: w}
	if err := h.Svc.Archive(r.Context(), p, in.FileIDs, folder, in.ConfirmQuarantine, pw); err != nil {
		if !pw.started {
			writeErr(w, err)
			return
		}
		panic(http.ErrAbortHandler)
	}
}

// pendingWriter sends the zip headers with the first byte, so a denial before any
// byte is written still answers with a problem body.
type pendingWriter struct {
	w       http.ResponseWriter
	started bool
}

func (p *pendingWriter) Write(b []byte) (int, error) {
	if !p.started {
		p.started = true
		p.w.Header().Set("Content-Type", "application/zip")
		p.w.Header().Set("Content-Disposition", `attachment; filename="files.zip"`)
		p.w.WriteHeader(http.StatusOK)
	}
	return p.w.Write(b)
}

func (h *Handler) folders(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	matter := domain.ID(r.URL.Query().Get("matter_id"))
	if matter == "" {
		writeErr(w, domain.Invalid("matter_id", "matter_id is required"))
		return
	}
	rows, err := h.Svc.Folders(r.Context(), p, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		MatterID  domain.ID `json:"matter_id"`
		Name      string    `json:"name"`
		SortOrder int       `json:"sort_order"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.CreateFolder(r.Context(), p, in.MatterID, in.Name, in.SortOrder)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) getFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.GetFolder(r.Context(), p, pathID(r, "folder_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) updateFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Name      *string    `json:"name"`
		SortOrder *int       `json:"sort_order"`
		MatterID  *domain.ID `json:"matter_id"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.UpdateFolder(r.Context(), p, pathID(r, "folder_id"), in.Name, in.SortOrder)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if err := h.Svc.DeleteFolder(r.Context(), p, pathID(r, "folder_id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) backends(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.Backends(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) createBackend(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in BackendWrite
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.CreateBackend(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) getBackend(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.GetBackend(r.Context(), p, pathID(r, "storage_backend_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) updateBackend(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in BackendWrite
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.UpdateBackend(r.Context(), p, pathID(r, "storage_backend_id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) testBackend(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.TestBackend(r.Context(), p, pathID(r, "storage_backend_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) moves(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	rows, err := h.Svc.Moves(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page(rows, ""))
}

func (h *Handler) createMove(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in MoveRequest
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	v, err := h.Svc.StartMove(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	ctx := context.WithoutCancel(r.Context())
	h.Background(func() { _ = h.Svc.RunMove(ctx, v.ID) })
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) getMove(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.GetMove(r.Context(), p, pathID(r, "storage_move_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) confirmMove(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	v, err := h.Svc.ConfirmMove(r.Context(), p, pathID(r, "storage_move_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func humanBytes(n int64) string {
	const unit = 1 << 10
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, suffix := float64(n), []string{"KB", "MB", "GB", "TB"}
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") + " " + suffix[i]
}

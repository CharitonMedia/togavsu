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
	// BackgroundContext, when set, is the context that work runs under, so the core can
	// stop a move on shutdown (a stopped move resumes where it stopped). Nil detaches
	// the work from the request instead.
	BackgroundContext context.Context
	// DocStatus, when set, is a file's Doc Tender label and flags as p sees the file
	// (PRD 8.21: a failed file carries a flag naming what failed; a closed matter's file
	// is Not processed (matter closed)). The core sets it to Doc Tender's Status; files
	// cannot import Doc Tender, which builds on files.
	DocStatus func(ctx context.Context, p domain.Principal, fileID domain.ID) (DocStatus, error)
}

// DocStatus is what the Files tab shows beside a file for Doc Tender.
type DocStatus struct {
	Label string
	Flags []DocFlag
}

// DocFlag is one open or kept review item on the file, named for people.
type DocFlag struct {
	Kind, Label, Detail string
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
	ok, created := http.StatusOK, http.StatusCreated
	id := pathID
	file := func(run func(r *http.Request, p domain.Principal, id domain.ID) (FileView, error)) http.HandlerFunc {
		return op(h, ok, func(r *http.Request, p domain.Principal) (FileView, error) { return run(r, p, id(r, "file_id")) })
	}
	type nameBody struct {
		Name string `json:"name"`
	}
	type folderBody struct {
		FolderID *domain.ID `json:"folder_id"`
	}
	type statusBody struct {
		Status string `json:"status"`
	}
	type purgeBody struct {
		ConfirmName string `json:"confirm_name"`
	}
	type quarantineBody struct {
		Quarantined *bool `json:"quarantined"`
	}
	type linkBody struct {
		ToFileID     domain.ID `json:"to_file_id"`
		Relationship string    `json:"relationship"`
	}
	type newFolderBody struct {
		MatterID  domain.ID `json:"matter_id"`
		Name      string    `json:"name"`
		SortOrder int       `json:"sort_order"`
	}
	type folderPatch struct {
		Name      *string    `json:"name"`
		SortOrder *int       `json:"sort_order"`
		MatterID  *domain.ID `json:"matter_id"`
	}
	m.HandleFunc("GET /api/v1/files", op(h, ok, h.list))
	m.HandleFunc("POST /api/v1/files", opBody(h, created, func(r *http.Request, p domain.Principal, in Publish) (FileView, error) {
		return h.Svc.PublishUpload(r.Context(), p, in)
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}", file(func(r *http.Request, p domain.Principal, f domain.ID) (FileView, error) {
		return h.Svc.Get(r.Context(), p, f)
	}))
	m.HandleFunc("PATCH /api/v1/files/{file_id}", opBody(h, ok, func(r *http.Request, p domain.Principal, in Metadata) (FileView, error) {
		return h.Svc.UpdateMetadata(r.Context(), p, id(r, "file_id"), in)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/rename", opBody(h, ok, func(r *http.Request, p domain.Principal, in nameBody) (FileView, error) {
		return h.Svc.Rename(r.Context(), p, id(r, "file_id"), in.Name)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/move", opBody(h, ok, func(r *http.Request, p domain.Principal, in folderBody) (FileView, error) {
		var folder domain.ID
		if in.FolderID != nil {
			folder = *in.FolderID
		}
		return h.Svc.MoveToFolder(r.Context(), p, id(r, "file_id"), folder)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/status", opBody(h, ok, func(r *http.Request, p domain.Principal, in statusBody) (FileView, error) {
		return h.Svc.MarkStatus(r.Context(), p, id(r, "file_id"), in.Status)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/trash", file(func(r *http.Request, p domain.Principal, f domain.ID) (FileView, error) {
		return h.Svc.Trash(r.Context(), p, f)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/restore", file(func(r *http.Request, p domain.Principal, f domain.ID) (FileView, error) {
		return h.Svc.Restore(r.Context(), p, f)
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/purge", opBody(h, http.StatusNoContent, func(r *http.Request, p domain.Principal, in purgeBody) (none, error) {
		return none{}, h.Svc.Purge(r.Context(), p, id(r, "file_id"), in.ConfirmName)
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/history", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.History(r.Context(), p, id(r, "file_id")))
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/versions", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Versions(r.Context(), p, id(r, "file_id")))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/versions", opBody(h, created, func(r *http.Request, p domain.Principal, in Publish) (VersionView, error) {
		return h.Svc.AddUploadedVersion(r.Context(), p, id(r, "file_id"), in.UploadID)
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/versions/{version_id}/download", h.download)
	m.HandleFunc("POST /api/v1/files/{file_id}/versions/{version_id}/restore", op(h, created, func(r *http.Request, p domain.Principal) (VersionView, error) {
		return h.Svc.RestoreVersion(r.Context(), p, id(r, "file_id"), id(r, "version_id"))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/versions/{version_id}/quarantine", opBody(h, ok, func(r *http.Request, p domain.Principal, in quarantineBody) (VersionView, error) {
		if in.Quarantined == nil {
			return VersionView{}, domain.Invalid("quarantined", "quarantined is required")
		}
		return h.Svc.Quarantine(r.Context(), p, id(r, "file_id"), id(r, "version_id"), *in.Quarantined)
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/renditions", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Renditions(r.Context(), p, id(r, "file_id")))
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/renditions/{rendition_id}", h.download)
	m.HandleFunc("GET /api/v1/files/{file_id}/links", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Links(r.Context(), p, id(r, "file_id")))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/links", opBody(h, created, func(r *http.Request, p domain.Principal, in linkBody) (LinkView, error) {
		return h.Svc.Link(r.Context(), p, id(r, "file_id"), in.ToFileID, in.Relationship)
	}))
	m.HandleFunc("POST /api/v1/file-uploads", opBody(h, created, func(r *http.Request, p domain.Principal, in UploadRequest) (Upload, error) {
		return h.Svc.CreateUpload(r.Context(), p, in)
	}))
	m.HandleFunc("GET /api/v1/file-uploads/{upload_id}", op(h, ok, func(r *http.Request, p domain.Principal) (Upload, error) {
		return h.Svc.GetUpload(r.Context(), p, id(r, "upload_id"))
	}))
	m.HandleFunc("PUT /api/v1/file-uploads/{upload_id}", op(h, ok, h.putChunk))
	m.HandleFunc("POST /api/v1/file-uploads/{upload_id}/complete", op(h, ok, func(r *http.Request, p domain.Principal) (Upload, error) {
		return h.Svc.CompleteUpload(r.Context(), p, id(r, "upload_id"))
	}))
	m.HandleFunc("POST /api/v1/file-archives", h.archive)
	m.HandleFunc("GET /api/v1/folders", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		matter := domain.ID(r.URL.Query().Get("matter_id"))
		if matter == "" {
			return nil, domain.Invalid("matter_id", "matter_id is required")
		}
		return listed(h.Svc.Folders(r.Context(), p, matter))
	}))
	m.HandleFunc("POST /api/v1/folders", opBody(h, created, func(r *http.Request, p domain.Principal, in newFolderBody) (FolderView, error) {
		return h.Svc.CreateFolder(r.Context(), p, in.MatterID, in.Name, in.SortOrder)
	}))
	m.HandleFunc("GET /api/v1/folders/{folder_id}", op(h, ok, func(r *http.Request, p domain.Principal) (FolderView, error) {
		return h.Svc.GetFolder(r.Context(), p, id(r, "folder_id"))
	}))
	m.HandleFunc("PATCH /api/v1/folders/{folder_id}", opBody(h, ok, func(r *http.Request, p domain.Principal, in folderPatch) (FolderView, error) {
		return h.Svc.UpdateFolder(r.Context(), p, id(r, "folder_id"), in.Name, in.SortOrder)
	}))
	m.HandleFunc("DELETE /api/v1/folders/{folder_id}", op(h, http.StatusNoContent, func(r *http.Request, p domain.Principal) (none, error) {
		return none{}, h.Svc.DeleteFolder(r.Context(), p, id(r, "folder_id"))
	}))
	m.HandleFunc("GET /api/v1/storage-backends", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Backends(r.Context(), p))
	}))
	m.HandleFunc("POST /api/v1/storage-backends", opBody(h, created, func(r *http.Request, p domain.Principal, in BackendWrite) (BackendView, error) {
		return h.Svc.CreateBackend(r.Context(), p, in)
	}))
	m.HandleFunc("GET /api/v1/storage-backends/{storage_backend_id}", op(h, ok, func(r *http.Request, p domain.Principal) (BackendView, error) {
		return h.Svc.GetBackend(r.Context(), p, id(r, "storage_backend_id"))
	}))
	m.HandleFunc("PATCH /api/v1/storage-backends/{storage_backend_id}", opBody(h, ok, func(r *http.Request, p domain.Principal, in BackendWrite) (BackendView, error) {
		return h.Svc.UpdateBackend(r.Context(), p, id(r, "storage_backend_id"), in)
	}))
	m.HandleFunc("POST /api/v1/storage-backends/{storage_backend_id}/test", op(h, ok, func(r *http.Request, p domain.Principal) (BackendView, error) {
		return h.Svc.TestBackend(r.Context(), p, id(r, "storage_backend_id"))
	}))
	m.HandleFunc("GET /api/v1/storage-moves", op(h, ok, func(r *http.Request, p domain.Principal) (map[string]any, error) {
		return listed(h.Svc.Moves(r.Context(), p))
	}))
	m.HandleFunc("POST /api/v1/storage-moves", opBody(h, created, h.createMove))
	m.HandleFunc("GET /api/v1/storage-moves/{storage_move_id}", op(h, ok, func(r *http.Request, p domain.Principal) (MoveView, error) {
		return h.Svc.GetMove(r.Context(), p, id(r, "storage_move_id"))
	}))
	m.HandleFunc("POST /api/v1/storage-moves/{storage_move_id}/confirm", op(h, ok, func(r *http.Request, p domain.Principal) (MoveView, error) {
		return h.Svc.ConfirmMove(r.Context(), p, id(r, "storage_move_id"))
	}))
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

// op answers one JSON operation: authenticate, run, write code and the result.
func op[T any](h *Handler, code int, run func(r *http.Request, p domain.Principal) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.who(w, r)
		if !ok {
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

// opBody is op with a JSON request body decoded into In first.
func opBody[In, T any](h *Handler, code int, run func(r *http.Request, p domain.Principal, in In) (T, error)) http.HandlerFunc {
	return op(h, code, func(r *http.Request, p domain.Principal) (T, error) {
		var in In
		if err := decode(r, &in); err != nil {
			var zero T
			return zero, err
		}
		return run(r, p, in)
	})
}

// listed wraps a list in the contract's page envelope.
func listed[T any](rows []T, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []T{}
	}
	return page(rows, ""), nil
}

type none struct{}

func (h *Handler) list(r *http.Request, p domain.Principal) (map[string]any, error) {
	limit, err := h.limit(r)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	if rows == nil {
		rows = []FileView{}
	}
	return page(rows, next), nil
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
	var d *Download
	var err error
	if rendition := pathID(r, "rendition_id"); rendition != "" {
		d, err = h.Svc.OpenRendition(r.Context(), p, pathID(r, "file_id"), rendition, confirmed(r))
	} else {
		d, err = h.Svc.OpenVersion(r.Context(), p, pathID(r, "file_id"), pathID(r, "version_id"), confirmed(r))
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	serve(w, r, d)
}

func (h *Handler) putChunk(r *http.Request, p domain.Principal) (Upload, error) {
	start, end, total, err := ParseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		return Upload{}, err
	}
	return h.Svc.PutChunk(r.Context(), p, pathID(r, "upload_id"), start, end, total, r.Body)
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

func (h *Handler) createMove(r *http.Request, p domain.Principal, in MoveRequest) (MoveView, error) {
	v, err := h.Svc.StartMove(r.Context(), p, in)
	if err != nil {
		return MoveView{}, err
	}
	ctx := h.BackgroundContext
	if ctx == nil {
		ctx = context.WithoutCancel(r.Context())
	}
	h.Background(func() { _ = h.Svc.RunMove(ctx, v.ID) })
	return v, nil
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

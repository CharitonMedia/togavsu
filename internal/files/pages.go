package files

import (
	"bytes"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"path"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/frame"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

type fileRowView struct {
	FileView
	Folder       string
	Size         int64
	Versions     int
	VersionID    domain.ID
	Quarantined  bool
	Markdown     domain.ID
	MarkdownName string
	Doc          *DocStatus
}

type pageData struct {
	Title        string
	CSRF         string
	Admin        bool
	Error        string
	MatterID     domain.ID
	MatterNumber string
	MatterStatus string
	FolderID     domain.ID
	Folders      []FolderView
	Files        []fileRowView
	Trashed      bool
	Query        string
	ChunkBytes   int64
	MaxBytes     int64
	Backends     []BackendView
	Moves        []moveRowView
	Alerts       []Alert
}

type moveRowView struct {
	MoveView
	From, To, Label, Started string
}

var moveLabels = map[string]string{
	"running": "Copying", "stopped": "Stopped; resumes when the core restarts", "checking": "Checking",
	"awaiting_confirmation": "Checked; waiting for your confirmation", "completed": "Completed", "failed": "Failed; see storage alerts",
}

func csrfOf(r *http.Request) string {
	if c, err := r.Cookie(practice.CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

// render draws the named files screen inside the app shell.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	var buf bytes.Buffer
	if err := h.Templates.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; frame-ancestors 'self'; base-uri 'none'")
	frame.Write(w, r, frame.Page{Title: data.Title, Area: "files", Body: template.HTML(buf.String()), Error: data.Error,
		Scripts: []string{"/files/static/files.js"}})
}

func (h *Handler) pageError(w http.ResponseWriter, r *http.Request, err error) {
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == domain.CodeUnauthenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if errors.As(err, &api) && api.Code == domain.CodeNotFound {
		http.NotFound(w, r)
		return
	}
	if errors.As(err, &api) && api.Code == domain.CodeForbidden {
		http.Error(w, api.Message, http.StatusForbidden)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// static serves files.js from the template tree.
func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	types := map[string]string{"files.js": "text/javascript; charset=utf-8"}
	ct, ok := types[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := fs.ReadFile(h.FS, path.Clean(name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(body)
}

// matterPage is the matter's Files tab. Every row comes from Service.List, so walls
// and roles apply exactly as they do to the API.
func (h *Handler) matterPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	ctx := r.Context()
	matterID := pathID(r, "matter_id")
	if _, err := domain.ParseID(string(matterID)); err != nil {
		http.NotFound(w, r)
		return
	}
	folders, err := h.Svc.Folders(ctx, p, matterID)
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	m, err := h.Svc.dir.Matter(ctx, matterID)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	q := r.URL.Query()
	data := pageData{
		Title: "Files", CSRF: csrfOf(r), Admin: p.HasRole(domain.RoleAdmin), MatterID: matterID, MatterNumber: m.Number,
		MatterStatus: m.Status, Folders: folders, Trashed: q.Get("trashed") == "true", Query: q.Get("q"),
	}
	lq := ListQuery{MatterID: matterID, Trashed: data.Trashed, Text: data.Query}
	if id := domain.ID(q.Get("folder_id")); id != "" {
		data.FolderID = id
		lq.FolderID = &id
	}
	if data.ChunkBytes, err = h.Svc.intSetting(ctx, "files.upload_chunk_bytes"); err != nil {
		h.pageError(w, r, err)
		return
	}
	if data.MaxBytes, err = h.Svc.intSetting(ctx, "files.max_upload_bytes"); err != nil {
		h.pageError(w, r, err)
		return
	}
	rows, _, err := h.Svc.List(ctx, p, lq)
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	names := map[domain.ID]string{}
	for _, f := range folders {
		names[f.ID] = f.Name
	}
	for _, f := range rows {
		row := fileRowView{FileView: f, MarkdownName: f.MarkdownName}
		if f.FolderID != nil {
			row.Folder = names[*f.FolderID]
		}
		vs, err := h.Svc.Versions(ctx, p, f.ID)
		if err != nil {
			h.pageError(w, r, err)
			return
		}
		row.Versions = len(vs)
		for _, v := range vs {
			if f.CurrentVersionID != nil && v.ID == *f.CurrentVersionID {
				row.Size, row.VersionID, row.Quarantined = v.SizeBytes, v.ID, v.Quarantined
			}
		}
		if rs, err := h.Svc.Renditions(ctx, p, f.ID); err == nil {
			for _, rd := range rs {
				if rd.Kind == "markdown" {
					row.Markdown = rd.ID
				}
			}
		}
		if h.DocStatus != nil {
			st, err := h.DocStatus(ctx, p, f.ID)
			var api *domain.APIError
			switch {
			case err == nil:
				row.Doc = &st
			case errors.As(err, &api) && api.Code == domain.CodeNotFound:
				// Doc Tender does not show this reader the file's state; the row stays.
			default:
				h.pageError(w, r, err)
				return
			}
		}
		data.Files = append(data.Files, row)
	}
	h.render(w, r, "matter_files.html", data)
}

// storagePage is the Files and storage admin tab: backends with Test, moves with
// progress and Confirm, and storage alerts.
func (h *Handler) storagePage(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	ctx := r.Context()
	data := pageData{Title: "Files and storage", CSRF: csrfOf(r), Admin: true}
	if data.Backends, err = h.Svc.Backends(ctx, p); err != nil {
		h.pageError(w, r, err)
		return
	}
	moves, err := h.Svc.Moves(ctx, p)
	if err != nil {
		h.pageError(w, r, err)
		return
	}
	names := map[domain.ID]string{}
	for _, b := range data.Backends {
		names[b.ID] = b.Name
	}
	for _, m := range moves {
		started := m.CreatedAt
		if t, err := domain.ParseUTC(m.CreatedAt); err == nil {
			started = t.Format("2006-01-02 15:04 UTC")
		}
		data.Moves = append(data.Moves, moveRowView{MoveView: m, From: names[m.FromBackendID], To: names[m.ToBackendID], Label: moveLabels[m.Status], Started: started})
	}
	if data.Alerts, err = h.Svc.Alerts(ctx, p); err != nil {
		h.pageError(w, r, err)
		return
	}
	h.render(w, r, "storage.html", data)
}

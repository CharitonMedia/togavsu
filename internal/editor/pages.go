package editor

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// assets is the browser editor build: its index page and the Content Security Policy
// that lets it run WebAssembly and nothing it did not ship.
type assets struct {
	fsys  fs.FS
	index []byte
	csp   string
}

var (
	inlineScript = regexp.MustCompile(`(?s)<script(?P<attrs>[^>]*)>(?P<body>.*?)</script>`)
	inlineStyle  = regexp.MustCompile(`(?s)<style[^>]*>(?P<body>.*?)</style>`)
	scriptAttrs  = inlineScript.SubexpIndex("attrs")
	scriptBody   = inlineScript.SubexpIndex("body")
	styleBody    = inlineStyle.SubexpIndex("body")
)

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// EditorCSP is the policy for /editor/: same-origin scripts plus the hashes of trunk's
// inline loader, WebAssembly compilation ('wasm-unsafe-eval', not 'unsafe-eval'), and
// framing only by Togavsu's own pages.
func EditorCSP(index []byte) string {
	scripts := []string{"'self'", "'wasm-unsafe-eval'"}
	for _, m := range inlineScript.FindAllSubmatch(index, -1) {
		if !bytes.Contains(m[scriptAttrs], []byte("src=")) && len(bytes.TrimSpace(m[scriptBody])) > 0 {
			scripts = append(scripts, hash(string(m[scriptBody])))
		}
	}
	styles := []string{"'self'"}
	for _, m := range inlineStyle.FindAllSubmatch(index, -1) {
		styles = append(styles, hash(string(m[styleBody])))
	}
	return "default-src 'self'; script-src " + strings.Join(scripts, " ") + "; style-src " + strings.Join(styles, " ") +
		"; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
}

func loadAssets(dist fs.FS) (*assets, error) {
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil, fmt.Errorf("editor: the browser editor build has no index.html: %w", err)
	}
	return &assets{fsys: dist, index: index, csp: EditorCSP(index)}, nil
}

// pageCSP is the edit page's policy: it frames /editor/ on the same origin only.
const pageCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; frame-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'self'"

func (h *Handler) pageRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /files/{file_id}/edit", h.editPage)
	m.HandleFunc("GET /files/{file_id}/edit/document", h.document)
	m.HandleFunc("POST /files/{file_id}/edit/save", h.save)
	m.HandleFunc("GET /files/{file_id}/edit/context", op(h, http.StatusOK, func(r *http.Request, p domain.Principal) (EditContext, error) {
		return h.Svc.Context(r.Context(), p, domain.ID(r.PathValue("file_id")))
	}))
	m.HandleFunc("GET /matters/{matter_id}/pleadings/new", h.pleadingForm)
	m.HandleFunc("POST /matters/{matter_id}/pleadings", h.pleadingPost)
	m.HandleFunc("GET /editor-host/{name}", h.hostAsset)
	m.HandleFunc("GET /editor/{rest...}", h.editorAsset)
}

type pageData struct {
	Title       string
	CSRF        string
	Error       string
	Notice      string
	FileID      domain.ID
	FileName    string
	MatterID    domain.ID
	Commit      string
	FrameURL    string
	Mode        string // edit, locked, readonly
	HolderName  string
	HolderSince string
	DownloadURL string
	PreviewURL  string
	Types       []string
	Type        string
	PleadTitle  string
}

func csrfOf(r *http.Request) string {
	if c, err := r.Cookie(practice.CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handler) render(w http.ResponseWriter, status int, name string, data pageData) {
	if h.templates == nil {
		http.Error(w, "the editor pages are not installed", http.StatusNotFound)
		return
	}
	data.Commit = Commit
	var buf bytes.Buffer
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "the page could not be drawn", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func (h *Handler) pageError(w http.ResponseWriter, r *http.Request, err error, data pageData) {
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == domain.CodeUnauthenticated {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data.Mode = "error"
	data.Error = "The file could not be opened."
	status := Status(err)
	switch {
	case status == http.StatusNotFound:
		data.Error = "That file does not exist, or you do not have access to it."
	case status == http.StatusForbidden && api != nil:
		data.Error = api.Message
	case status < http.StatusInternalServerError && api != nil:
		data.Error = api.Message
	}
	if data.Title == "" {
		data.Title = "Editor"
	}
	h.render(w, status, "edit.html", data)
}

func (h *Handler) editPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := domain.ID(r.PathValue("file_id"))
	data := pageData{CSRF: csrfOf(r), FileID: id}
	p, err := h.who(r)
	if err != nil {
		h.pageError(w, r, err, data)
		return
	}
	if err := requireEditor(p); err != nil {
		h.pageError(w, r, err, data)
		return
	}
	f, err := h.Svc.files.Get(ctx, p, id)
	if err != nil {
		h.pageError(w, r, err, data)
		return
	}
	data.Title, data.FileName = "Edit "+f.Name, f.Name
	if f.MatterID != nil {
		data.MatterID = *f.MatterID
	}
	if f.CurrentVersionID != nil {
		data.DownloadURL = "/api/v1/files/" + string(id) + "/versions/" + string(*f.CurrentVersionID) + "/download"
		if rs, err := h.Svc.files.Renditions(ctx, p, id); err == nil {
			for _, rd := range rs {
				if rd.Kind == "pdf_preview" && rd.FileVersionID == *f.CurrentVersionID {
					data.PreviewURL = "/api/v1/files/" + string(id) + "/renditions/" + string(rd.ID)
				}
			}
		}
	}
	if !Editable(f.Extension) {
		data.Mode, data.Notice = "readonly", "The editor opens DOCX, DOC, ODT, RTF, HTML, Markdown and text files. Preview or download this one."
		h.render(w, http.StatusOK, "edit.html", data)
		return
	}
	if _, err := h.Svc.AcquireLock(ctx, p, id); err != nil {
		var held *LockHeld
		if errors.As(err, &held) {
			data.Mode, data.HolderName, data.HolderSince = "locked", held.HolderName, held.Since.UTC().Format("Jan 2, 2006 at 15:04 UTC")
			if data.HolderName == "" {
				data.HolderName = "Another user"
			}
			h.render(w, http.StatusOK, "edit.html", data)
			return
		}
		h.pageError(w, r, err, data)
		return
	}
	data.Mode = "edit"
	data.FrameURL = "/editor/?host=parent&author=" + url.QueryEscape(h.Svc.userName(ctx, p.UserID))
	h.render(w, http.StatusOK, "edit.html", data)
}

func (h *Handler) document(w http.ResponseWriter, r *http.Request) {
	p, err := h.who(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	name, b, err := h.Svc.Document(r.Context(), p, domain.ID(r.PathValue("file_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-File-Name", url.PathEscape(name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (h *Handler) save(w http.ResponseWriter, r *http.Request) {
	p, err := h.who(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	name, err := url.PathUnescape(r.Header.Get("X-File-Name"))
	if err != nil {
		writeErr(w, domain.Invalid("X-File-Name", "the saved file's name is not URL-encoded"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSaveBytes+1))
	if err != nil {
		writeErr(w, domain.Invalid("body", "the document is too large to save"))
		return
	}
	v, err := h.Svc.Save(r.Context(), p, domain.ID(r.PathValue("file_id")), name, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) pleadingForm(w http.ResponseWriter, r *http.Request) {
	data := pageData{Title: "New pleading", CSRF: csrfOf(r), MatterID: domain.ID(r.PathValue("matter_id")), Types: []string{"motion", "notice", "answer", "petition"}, Type: "motion", Mode: "pleading"}
	p, err := h.who(r)
	if err == nil {
		err = h.Svc.CanPlead(r.Context(), p, data.MatterID)
	}
	if err != nil {
		h.pageError(w, r, err, data)
		return
	}
	h.render(w, http.StatusOK, "pleading.html", data)
}

func (h *Handler) pleadingPost(w http.ResponseWriter, r *http.Request) {
	matter := domain.ID(r.PathValue("matter_id"))
	data := pageData{Title: "New pleading", CSRF: csrfOf(r), MatterID: matter, Types: []string{"motion", "notice", "answer", "petition"}, Mode: "pleading"}
	p, err := h.who(r)
	if err != nil {
		h.pageError(w, r, err, data)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.pageError(w, r, domain.Invalid("form", "the form could not be read"), data)
		return
	}
	data.Type, data.PleadTitle = r.PostFormValue("type"), r.PostFormValue("title")
	f, err := h.Svc.NewPleading(r.Context(), p, matter, PleadingRequest{Type: data.Type, Title: data.PleadTitle})
	if err != nil {
		var api *domain.APIError
		if errors.As(err, &api) && api.Code == domain.CodeValidation {
			data.Error = api.Message
			h.render(w, http.StatusBadRequest, "pleading.html", data)
			return
		}
		h.pageError(w, r, err, data)
		return
	}
	http.Redirect(w, r, "/files/"+string(f.ID)+"/edit", http.StatusSeeOther)
}

func (h *Handler) hostAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if h.Pages == nil || (name != "edit.js" && name != "edit.css") {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(h.Pages, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (h *Handler) editorAsset(w http.ResponseWriter, r *http.Request) {
	if h.assets == nil {
		http.Error(w, "the browser editor is not installed in this build", http.StatusNotFound)
		return
	}
	rest := r.PathValue("rest")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if rest == "" || rest == "index.html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", h.assets.csp)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(h.assets.index)
		return
	}
	clean := path.Clean(rest)
	if clean != rest || strings.HasPrefix(clean, "..") || !fs.ValidPath(clean) {
		http.NotFound(w, r)
		return
	}
	f, err := h.assets.fsys.Open(clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(clean))
	if path.Ext(clean) == ".wasm" {
		ct = "application/wasm"
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// trunk names every built file by its content hash.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", st.ModTime(), rs)
		return
	}
	_, _ = io.Copy(w, f)
}

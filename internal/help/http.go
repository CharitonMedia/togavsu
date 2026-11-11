package help

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves the help JSON API, the guide in HTML and PDF, and /llms.txt. The help
// center screens (/help, /help/topics/{id}) are rendered inside the shell by
// internal/dashboard from this service.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
	// BaseURL is the public origin for /llms.txt links; empty uses the request host.
	BaseURL string
}

// Routes returns the help mux.
func (h *Handler) Routes() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /llms.txt", h.llms)
	m.HandleFunc("GET /api/v1/llms.txt", h.llms)
	m.HandleFunc("GET /help/guide", h.guideHTML)
	m.HandleFunc("GET /api/v1/help/guide", h.guideHTML)
	m.HandleFunc("GET /help/guide.pdf", h.guidePDF)
	m.HandleFunc("GET /api/v1/help/guide.pdf", h.guidePDF)
	m.HandleFunc("GET /api/v1/help/topics", h.with(h.topics))
	m.HandleFunc("GET /api/v1/help/sections", h.with(h.listSections))
	m.HandleFunc("POST /api/v1/help/sections", h.with(h.createSection))
	m.HandleFunc("GET /api/v1/help/sections/{section_id}", h.with(h.getSection))
	m.HandleFunc("PATCH /api/v1/help/sections/{section_id}", h.with(h.patchSection))
	m.HandleFunc("DELETE /api/v1/help/sections/{section_id}", h.with(h.deleteSection))
	m.HandleFunc("GET /api/v1/help/items", h.with(h.listItems))
	m.HandleFunc("POST /api/v1/help/items", h.with(h.createItem))
	m.HandleFunc("GET /api/v1/help/items/{item_id}", h.with(h.getItem))
	m.HandleFunc("PATCH /api/v1/help/items/{item_id}", h.with(h.patchItem))
	m.HandleFunc("DELETE /api/v1/help/items/{item_id}", h.with(h.deleteItem))
	m.HandleFunc("GET /api/v1/help/modules/{module_id}/pages", h.with(h.listPages))
	m.HandleFunc("POST /api/v1/help/modules/{module_id}/pages", h.with(h.postPage))
	m.HandleFunc("PUT /api/v1/help/modules/{module_id}/pages", h.with(h.putPages))
	m.HandleFunc("DELETE /api/v1/help/modules/{module_id}/pages/{page_id}", h.with(h.deletePage))
	return m
}

func (h *Handler) with(fn func(http.ResponseWriter, *http.Request, domain.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Principal(r)
		if err != nil {
			WriteErr(w, err)
			return
		}
		fn(w, r, p)
	}
}

// WriteErr writes a problem+json error with the shared status table.
func WriteErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if !errors.As(err, &api) {
		if errors.Is(err, context.Canceled) {
			return
		}
		api = &domain.APIError{Code: "internal", Message: "internal error"}
	}
	code := api.HTTPStatus()
	if api.Code == "layout_limit" {
		code = http.StatusConflict
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(api)
}

// WriteJSON writes body as JSON.
func WriteJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// Decode reads a JSON body, rejecting unknown fields and anything over 1 MB.
func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "request body must be a JSON object with the documented fields")
	}
	return nil
}

// ListPage writes a contract list: data and a cursor page. The cursor is an offset;
// limit defaults to api.page_size and is capped by api.max_page_size.
func ListPage[T any](w http.ResponseWriter, r *http.Request, s Settings, rows []T) {
	limit, max := positiveSetting(r, s, "api.page_size"), positiveSetting(r, s, "api.max_page_size")
	if q := r.URL.Query().Get("limit"); q != "" {
		if !positiveInt.MatchString(q) {
			WriteErr(w, domain.Invalid("limit", "limit must be a positive integer"))
			return
		}
		limit, _ = strconv.Atoi(q)
	}
	limit = min(limit, max)
	var start int
	if c := r.URL.Query().Get("cursor"); c != "" {
		n, err := strconv.ParseUint(c, 10, 31)
		if err != nil {
			WriteErr(w, domain.Invalid("cursor", "unknown cursor"))
			return
		}
		start = int(n)
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := start + limit
	var next any
	if end < len(rows) {
		next = strconv.Itoa(end)
	} else {
		end = len(rows)
	}
	data := rows[start:end]
	if data == nil {
		data = []T{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"limit": limit, "next_cursor": next}})
}

// positiveSetting reads a page-size setting, falling back to its registry default.
func positiveSetting(r *http.Request, s Settings, key string) int {
	v, err := s.Get(r.Context(), key)
	if err != nil || !positiveInt.MatchString(v) {
		v = registryDefault(key)
	}
	n, _ := strconv.Atoi(v)
	return n
}

// positiveInt is a positive whole number small enough for an int on any platform.
var positiveInt = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

func (h *Handler) base(r *http.Request) string {
	if h.BaseURL != "" {
		return strings.TrimRight(h.BaseURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (h *Handler) llms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(h.Svc.LLMsTxt(r.Context(), h.base(r))))
}

var guideTemplate = template.Must(template.New("guide").Funcs(template.FuncMap{
	"md": func(s string) template.HTML { return template.HTML(RenderMarkdown(s)) },
}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Togavsu user guide {{.Version}}</title><link rel="stylesheet" href="/ui/static/guide.css"></head>
<body><header class="guide-head"><h1>Togavsu user guide</h1><p>Version {{.Version}} · <a href="/help/guide.pdf">PDF</a> · <a href="/help">Help &amp; Training</a></p></header>
<nav class="guide-toc" aria-label="Contents"><h2>Contents</h2><ol>{{range .Chapters}}<li>{{.Title}}<ol>{{range .Topics}}<li><a href="#{{.ID}}">{{.Title}}</a></li>{{end}}</ol></li>{{end}}</ol></nav>
<main>{{range .Chapters}}<section><h2>{{.Title}}</h2>{{if .Intro}}{{md .Intro}}{{end}}
{{range .Topics}}<article id="{{.ID}}"><h3>{{.Title}}</h3>{{if .Where}}<p class="where">Where: {{.Where}}{{with .Screen}} · <a href="{{.}}">Open this screen</a>{{end}}</p>{{end}}{{md .Markdown}}</article>{{end}}</section>{{end}}</main></body></html>`))

type guideView struct {
	Version  string
	Chapters []guideChapter
}

type guideChapter struct {
	Title  string
	Intro  string
	Topics []guideTopic
}

type guideTopic struct {
	ID, Title, Where, Markdown, Screen string
}

// openable is a screen link the guide can offer: a fixed path, not a pattern or an API.
func openable(t Topic) string {
	for _, p := range t.Paths {
		if !strings.Contains(p, "{") && !strings.HasPrefix(p, "/api/") {
			return p
		}
	}
	return ""
}

func (h *Handler) guideHTML(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err == nil {
		err = h.Svc.Allow(p)
	}
	if err != nil {
		if strings.HasPrefix(r.URL.Path, "/help/") && isUnauthenticated(err) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		WriteErr(w, err)
		return
	}
	v := guideView{Version: h.Svc.Version()}
	chs := append([]Chapter{}, h.Svc.Guide().Chapters...)
	mods, err := h.Svc.ModuleTopics(r.Context())
	if err != nil {
		WriteErr(w, err)
		return
	}
	chs = append(chs, mods...)
	for _, c := range chs {
		gc := guideChapter{Title: c.Title, Intro: c.Intro}
		for _, t := range c.Topics {
			gc.Topics = append(gc.Topics, guideTopic{ID: t.ID, Title: t.Title, Where: t.Where, Markdown: t.Markdown, Screen: openable(t)})
		}
		v.Chapters = append(v.Chapters, gc)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", CSP)
	w.Header().Set("Cache-Control", "private, no-store")
	if err := guideTemplate.Execute(w, v); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// CSP is the shell's Content Security Policy: no inline script or style.
const CSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; media-src 'self'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

func isUnauthenticated(err error) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == domain.CodeUnauthenticated
}

func (h *Handler) guidePDF(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		if strings.HasPrefix(r.URL.Path, "/help/") && isUnauthenticated(err) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		WriteErr(w, err)
		return
	}
	b, err := h.Svc.GuidePDF(p)
	if err != nil {
		WriteErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="togavsu-guide-`+safeFile(h.Svc.Version())+`.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(b)
}

func safeFile(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '-'
	}, s)
}

func (h *Handler) topics(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	hits, err := h.Svc.SearchTopics(r.Context(), p, r.URL.Query().Get("q"), r.URL.Query().Get("screen"))
	if err != nil {
		WriteErr(w, err)
		return
	}
	ListPage(w, r, h.Svc.d.Settings, hits)
}

func (h *Handler) listSections(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Sections(r.Context(), p)
	if err != nil {
		WriteErr(w, err)
		return
	}
	ListPage(w, r, h.Svc.d.Settings, rows)
}

func (h *Handler) createSection(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in SectionInput
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	sec, err := h.Svc.CreateSection(r.Context(), p, in)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, sec)
}

func (h *Handler) getSection(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	sec, err := h.Svc.Section(r.Context(), p, domain.ID(r.PathValue("section_id")))
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, sec)
}

func (h *Handler) patchSection(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in SectionInput
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	sec, err := h.Svc.UpdateSection(r.Context(), p, domain.ID(r.PathValue("section_id")), in)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, sec)
}

func (h *Handler) deleteSection(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if err := h.Svc.DeleteSection(r.Context(), p, domain.ID(r.PathValue("section_id"))); err != nil {
		WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listItems(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Items(r.Context(), p, domain.ID(r.URL.Query().Get("section_id")))
	if err != nil {
		WriteErr(w, err)
		return
	}
	ListPage(w, r, h.Svc.d.Settings, rows)
}

func (h *Handler) createItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in ItemInput
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	it, err := h.Svc.CreateItem(r.Context(), p, in)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, it)
}

func (h *Handler) getItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	it, err := h.Svc.Item(r.Context(), p, domain.ID(r.PathValue("item_id")))
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) patchItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in ItemInput
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	it, err := h.Svc.UpdateItem(r.Context(), p, domain.ID(r.PathValue("item_id")), in)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, it)
}

func (h *Handler) deleteItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if err := h.Svc.DeleteItem(r.Context(), p, domain.ID(r.PathValue("item_id"))); err != nil {
		WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listPages(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.ModulePages(r.Context(), p, r.PathValue("module_id"))
	if err != nil {
		WriteErr(w, err)
		return
	}
	ListPage(w, r, h.Svc.d.Settings, rows)
}

func (h *Handler) postPage(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in PageInput
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	pg, err := h.Svc.RegisterPage(r.Context(), p, r.PathValue("module_id"), in)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, pg)
}

func (h *Handler) putPages(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in struct {
		Pages []PageInput `json:"pages"`
	}
	if err := Decode(r, &in); err != nil {
		WriteErr(w, err)
		return
	}
	if in.Pages == nil {
		WriteErr(w, domain.Invalid("pages", "pages is required"))
		return
	}
	pages, err := h.Svc.ReplacePages(r.Context(), p, r.PathValue("module_id"), in.Pages)
	if err != nil {
		WriteErr(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"data": pages})
}

func (h *Handler) deletePage(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if err := h.Svc.RemovePage(r.Context(), p, r.PathValue("module_id"), r.PathValue("page_id")); err != nil {
		WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

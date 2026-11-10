package doctender

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// Handler serves the Doc Tender operations of the API contract and its pages: the
// review queue and the Doc Tender admin tab. Principal comes from identity, which
// authenticates the session or token, enforces CSRF on cookie writes and the route's
// scopes (doc_tender:read, doc_tender:write, reports:read). Every access decision is
// made again in the service.
type Handler struct {
	Svc       *Service
	Settings  SettingsWriter
	Principal func(*http.Request) (domain.Principal, error)
	Templates *template.Template
	FS        fs.FS
}

// NewHandler parses the pages from fsys (web/templates/doctender). A nil fsys serves
// the JSON API only.
func NewHandler(svc *Service, settings SettingsWriter, fsys fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Settings: settings, Principal: principal, FS: fsys}
	if fsys != nil {
		t, err := template.New("doctender").Funcs(template.FuncMap{"kind": KindLabel, "join": strings.Join}).ParseFS(fsys, "*.html")
		if err != nil {
			return nil, err
		}
		h.Templates = t
	}
	return h, nil
}

// JobView is the DocTenderJob contract shape.
type JobView struct {
	ID            domain.ID `json:"id"`
	CreatedAt     string    `json:"created_at"`
	CreatedBy     domain.ID `json:"created_by"`
	FileVersionID domain.ID `json:"file_version_id"`
	Stage         string    `json:"stage"`
	Status        string    `json:"status"`
	Attempts      int       `json:"attempts"`
	NextAttemptAt *string   `json:"next_attempt_at"`
	LastError     string    `json:"last_error"`
	FileID        domain.ID `json:"-"`
}

func (s *Service) jobView(ctx context.Context, j Job) (JobView, error) {
	var attempts int
	if err := s.row(ctx, `SELECT COALESCE(SUM(attempts), 0) FROM doc_tender_units WHERE job_id = ?`, j.ID).Scan(&attempts); err != nil {
		return JobView{}, err
	}
	v := JobView{ID: j.ID, CreatedAt: domain.FormatUTC(j.CreatedAt), CreatedBy: s.d.System.UserID, FileVersionID: j.VersionID,
		Stage: j.Stage, Status: j.Status, Attempts: attempts, LastError: j.LastError, FileID: j.FileID}
	if j.Status == StatusQueued || j.Status == StatusHeldProvider {
		t := domain.FormatUTC(j.NextAttempt)
		v.NextAttemptAt = &t
	}
	return v, nil
}

// JobQuery filters listDocTenderJobs.
type JobQuery struct {
	MatterID domain.ID
	FileID   domain.ID
	Status   string
	Cursor   domain.ID
	Limit    int
}

// Jobs lists jobs on files the caller can see (admin, attorney, staff).
func (s *Service) Jobs(ctx context.Context, p domain.Principal, q JobQuery) ([]JobView, domain.ID, error) {
	if !staffLike(p) {
		return nil, "", domain.Forbidden("Doc Tender jobs are for firm staff")
	}
	where, args := `id > ? AND file_id IS NOT NULL`, []any{string(q.Cursor)}
	if q.MatterID != "" {
		where, args = where+` AND matter_id = ?`, append(args, string(q.MatterID))
	}
	if q.FileID != "" {
		where, args = where+` AND file_id = ?`, append(args, string(q.FileID))
	}
	if q.Status != "" {
		where, args = where+` AND status = ?`, append(args, q.Status)
	}
	jobs, err := s.jobsWhere(ctx, where+` ORDER BY id`, args...)
	if err != nil {
		return nil, "", err
	}
	visible := map[domain.ID]bool{}
	var out []JobView
	for _, j := range jobs {
		ok, seen := visible[j.FileID]
		if !seen {
			_, err := s.visibleFile(ctx, p, j.FileID)
			ok = err == nil
			visible[j.FileID] = ok
		}
		if !ok {
			continue
		}
		if q.Limit > zero && len(out) == q.Limit {
			return out, out[len(out)-one].ID, nil
		}
		v, err := s.jobView(ctx, j)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	return out, "", nil
}

// Job reads one job as p sees its file.
func (s *Service) Job(ctx context.Context, p domain.Principal, id domain.ID) (JobView, error) {
	if !staffLike(p) {
		return JobView{}, domain.Forbidden("Doc Tender jobs are for firm staff")
	}
	j, err := s.job(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	if _, err := s.visibleFile(ctx, p, j.FileID); err != nil {
		return JobView{}, domain.NotFound("doc_tender_job")
	}
	return s.jobView(ctx, j)
}

// RetryJob is retryDocTenderJob (admins and attorneys).
func (s *Service) RetryJob(ctx context.Context, p domain.Principal, id domain.ID) (JobView, error) {
	if !reviewer(p) {
		return JobView{}, domain.Forbidden("only admins and attorneys retry jobs")
	}
	j, err := s.job(ctx, id)
	if err != nil {
		return JobView{}, err
	}
	if _, err := s.d.Files.Get(ctx, p, j.FileID); err != nil {
		return JobView{}, domain.NotFound("doc_tender_job")
	}
	if j.Status == StatusRunning {
		return JobView{}, domain.Conflict("the job is running")
	}
	if j, err = s.retryJob(ctx, j); err != nil {
		return JobView{}, err
	}
	return s.jobView(ctx, j)
}

// FileResult is getDocTenderResult: the newest published labels for a file.
func (s *Service) FileResult(ctx context.Context, p domain.Principal, fileID domain.ID) (Result, error) {
	if !staffLike(p) {
		return Result{}, domain.Forbidden("Doc Tender results are for firm staff")
	}
	f, err := s.visibleFile(ctx, p, fileID)
	if err != nil {
		return Result{}, err
	}
	var r Result
	var parts, fields, conf string
	var cat, matter, rendition string
	err = s.row(ctx, `SELECT id, file_version_id, name_parts, summary, COALESCE(category, ''), category_how, critical_fields, COALESCE(matter_id, ''),
COALESCE(markdown_rendition_id, ''), confidences FROM doc_tender_results WHERE file_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, f.ID).Scan(
		&r.ID, &r.FileVersionID, &parts, &r.Summary, &cat, &r.CategorySource, &fields, &matter, &rendition, &conf)
	if err != nil {
		return Result{}, notFoundRow(err, "doc_tender_result")
	}
	r.FileID = f.ID
	_ = json.Unmarshal([]byte(parts), &r.NameParts)
	_ = json.Unmarshal([]byte(fields), &r.Fields)
	_ = json.Unmarshal([]byte(conf), &r.Confidences)
	if r.Fields == nil {
		r.Fields = []CitedField{}
	}
	if cat != "" {
		r.Category = cat
		var id domain.ID
		if err := s.row(ctx, `SELECT id FROM doc_categories WHERE name = ?`, cat).Scan(&id); err == nil {
			r.CategoryID = &id
		}
	}
	if matter != "" {
		m := domain.ID(matter)
		r.MatterID = &m
	}
	if rendition != "" {
		rid := domain.ID(rendition)
		r.MarkdownRenditionID = &rid
	}
	if f.AttorneyOnly && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleAdmin) {
		r.Summary = map[string]bool{"redacted": true}
	}
	return r, nil
}

func notFoundRow(err error, entity string) error {
	if err != nil && strings.Contains(err.Error(), "no rows") {
		return domain.NotFound(entity)
	}
	return err
}

// Routes returns the mux. Paths and methods are the published contract's, plus the
// pages and three proposed endpoints (status, reprocess, model choices).
func (h *Handler) Routes() *http.ServeMux {
	m := http.NewServeMux()
	ok, created := http.StatusOK, http.StatusCreated
	m.HandleFunc("GET /api/v1/doc-tender/jobs", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		limit, err := h.limit(r)
		if err != nil {
			return nil, err
		}
		q := r.URL.Query()
		rows, next, err := h.Svc.Jobs(r.Context(), p, JobQuery{MatterID: domain.ID(q.Get("matter_id")), Status: q.Get("status"), Cursor: domain.ID(q.Get("cursor")), Limit: limit})
		return paged(rows, next, limit), err
	}))
	m.HandleFunc("GET /api/v1/doc-tender/jobs/{job_id}", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.Job(r.Context(), p, domain.ID(r.PathValue("job_id")))
	}))
	m.HandleFunc("POST /api/v1/doc-tender/jobs/{job_id}/retry", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.RetryJob(r.Context(), p, domain.ID(r.PathValue("job_id")))
	}))
	m.HandleFunc("GET /api/v1/doc-tender/exceptions", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		limit, err := h.limit(r)
		if err != nil {
			return nil, err
		}
		q := r.URL.Query()
		rows, next, err := h.Svc.Exceptions(r.Context(), p, ExceptionQuery{Status: q.Get("status"), Kind: q.Get("kind"), Cursor: domain.ID(q.Get("cursor")), Limit: limit})
		return paged(rows, next, limit), err
	}))
	m.HandleFunc("GET /api/v1/doc-tender/exceptions/{exception_id}", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.Exception(r.Context(), p, domain.ID(r.PathValue("exception_id")))
	}))
	m.HandleFunc("POST /api/v1/doc-tender/exceptions/{exception_id}/resolve", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		var in Resolve
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return h.Svc.ResolveException(r.Context(), p, domain.ID(r.PathValue("exception_id")), in)
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/doc-tender-result", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.FileResult(r.Context(), p, domain.ID(r.PathValue("file_id")))
	}))
	m.HandleFunc("GET /api/v1/files/{file_id}/doc-tender-status", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.Status(r.Context(), p, domain.ID(r.PathValue("file_id")))
	}))
	m.HandleFunc("POST /api/v1/files/{file_id}/doc-tender-reprocess", h.op(http.StatusAccepted, func(r *http.Request, p domain.Principal) (any, error) {
		id, err := h.Svc.Reprocess(r.Context(), p, domain.ID(r.PathValue("file_id")))
		if err != nil {
			return nil, err
		}
		return h.Svc.Job(r.Context(), p, id)
	}))
	m.HandleFunc("GET /api/v1/doc-tender/categories", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		rows, err := h.Svc.Categories(r.Context(), p)
		return paged(rows, "", len(rows)), err
	}))
	m.HandleFunc("POST /api/v1/doc-tender/categories", h.op(created, func(r *http.Request, p domain.Principal) (any, error) {
		var in CategoryWrite
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return h.Svc.CreateCategory(r.Context(), p, in)
	}))
	m.HandleFunc("GET /api/v1/doc-tender/categories/{category_id}", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.GetCategory(r.Context(), p, domain.ID(r.PathValue("category_id")))
	}))
	m.HandleFunc("PATCH /api/v1/doc-tender/categories/{category_id}", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		var in CategoryWrite
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return h.Svc.UpdateCategory(r.Context(), p, domain.ID(r.PathValue("category_id")), in)
	}))
	m.HandleFunc("GET /api/v1/doc-tender/naming", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		return h.Svc.GetNaming(r.Context(), p)
	}))
	m.HandleFunc("PUT /api/v1/doc-tender/naming", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		var in Naming
		if err := decode(r, &in); err != nil {
			return nil, err
		}
		return h.Svc.PutNaming(r.Context(), p, h.Settings, in.Pattern)
	}))
	m.HandleFunc("GET /api/v1/doc-tender/model-choices", h.op(ok, func(r *http.Request, p domain.Principal) (any, error) {
		if err := adminOnly(p); err != nil {
			return nil, err
		}
		out := map[string][]string{}
		for _, t := range []string{TaskOCR, TaskPhoto, TaskGrouping, TaskTranscription, TaskLabeling, TaskCategorizing} {
			out[t] = ChoicesForTask(t)
		}
		return out, nil
	}))
	m.HandleFunc("GET /api/v1/reports/doc-tender-spend", h.spend)
	m.HandleFunc("GET /doc-tender/review", h.reviewPage)
	m.HandleFunc("GET /doc-tender/review/{exception_id}", h.itemPage)
	m.HandleFunc("POST /doc-tender/review/{exception_id}", h.resolveForm)
	m.HandleFunc("GET /admin/doc-tender", h.adminPage)
	m.HandleFunc("POST /admin/doc-tender/naming", h.namingForm)
	m.HandleFunc("POST /admin/doc-tender/categories", h.categoryForm)
	m.HandleFunc("GET /doc-tender/guide", h.guidePage)
	if h.FS != nil {
		m.Handle("GET /doc-tender/static/", http.StripPrefix("/doc-tender/static/", http.FileServerFS(h.FS)))
	}
	return m
}

func paged[T any](rows []T, next domain.ID, limit int) map[string]any {
	if rows == nil {
		rows = []T{}
	}
	pg := map[string]any{"limit": limit, "next_cursor": nil}
	if next != "" {
		pg["next_cursor"] = string(next)
	}
	return map[string]any{"data": rows, "page": pg}
}

func (h *Handler) limit(r *http.Request) (int, error) {
	ctx := r.Context()
	def, err := h.Svc.intSetting(ctx, "api.page_size")
	if err != nil {
		return zero, err
	}
	cap_, err := h.Svc.intSetting(ctx, "api.max_page_size")
	if err != nil {
		return zero, err
	}
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < one || n > cap_ {
		return zero, domain.Invalid("limit", "limit is between 1 and api.max_page_size")
	}
	return n, nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Invalid("body", "the request body is not valid JSON for this operation: "+err.Error())
	}
	return nil
}

func (h *Handler) who(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, err := h.Principal(r)
	if err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	if p.Source == domain.SourceSystem {
		writeErr(w, domain.Forbidden("requests can't act as System"))
		return domain.Principal{}, false
	}
	return p, true
}

func (h *Handler) op(code int, run func(r *http.Request, p domain.Principal) (any, error)) http.HandlerFunc {
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
		writeJSON(w, code, out)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if !errors.As(err, &api) {
		api = &domain.APIError{Code: "internal", Message: "internal error"}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": api})
		return
	}
	writeJSON(w, api.HTTPStatus(), map[string]any{"error": api})
}

func (h *Handler) spend(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	var sq SpendQuery
	if v := q.Get("from_date"); v != "" {
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			writeErr(w, domain.Invalid("from_date", "from_date is YYYY-MM-DD"))
			return
		}
		sq.From = t
	}
	if v := q.Get("to_date"); v != "" {
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			writeErr(w, domain.Invalid("to_date", "to_date is YYYY-MM-DD"))
			return
		}
		sq.To = t.AddDate(zero, zero, one)
	}
	sq.MatterID = domain.ID(q.Get("matter_id"))
	rows, err := h.Svc.Spend(r.Context(), p, sq)
	if err != nil {
		writeErr(w, err)
		return
	}
	cols := []string{"day", "matter_id", "file_type", "calls", "prompt_tokens", "completion_tokens", "cost_cents"}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		cw := csv.NewWriter(w)
		_ = cw.Write(cols)
		for _, row := range rows {
			_ = cw.Write([]string{row.Day, row.MatterID, row.FileType, strconv.Itoa(row.Calls), strconv.Itoa(row.PromptTokens),
				strconv.Itoa(row.CompletionTokens), fmt.Sprint(row.CostCents)})
		}
		cw.Flush()
		return
	}
	out := make([]map[string]any, zero, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{"day": row.Day, "matter_id": row.MatterID, "file_type": row.FileType, "calls": row.Calls,
			"prompt_tokens": row.PromptTokens, "completion_tokens": row.CompletionTokens, "cost_cents": row.CostCents})
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": "doc-tender-spend", "columns": cols, "rows": out})
}

// ---- pages ----

type pageData struct {
	Title     string
	CSRF      string
	Admin     bool
	Reviewer  bool
	Error     string
	Notice    string
	OpenCount int
	Items     []itemRow
	Item      *itemRow
	Naming    Naming
	Cats      []Category
	Spend     []SpendRow
	Enabled   bool
	Choices   map[string][]string
}

type itemRow struct {
	Exception
	FileName  string
	FileLink  string
	Download  string
	Markdown  string
	BestJSON  string
	MatterNum string
	Flags     []Flag
}

func csrfOf(r *http.Request) string {
	if c, err := r.Cookie(practice.CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

func (h *Handler) render(w http.ResponseWriter, name string, data pageData, code int) {
	if h.Templates == nil {
		writeErr(w, domain.NotFound("page"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = h.Templates.ExecuteTemplate(w, name, data)
}

func (h *Handler) pageErr(w http.ResponseWriter, r *http.Request, err error) {
	var api *domain.APIError
	code := http.StatusInternalServerError
	msg := "Something went wrong."
	if errors.As(err, &api) {
		code, msg = api.HTTPStatus(), api.Message
	}
	h.render(w, "error.html", pageData{Title: "Doc Tender", CSRF: csrfOf(r), Error: msg}, code)
}

func (h *Handler) row(r *http.Request, p domain.Principal, e Exception) itemRow {
	row := itemRow{Exception: e, FileName: string(e.FileID), FileLink: "/files/" + string(e.FileID)}
	if f, err := h.Svc.d.Files.Get(r.Context(), p, e.FileID); err == nil {
		row.FileName = f.Name
		if f.CurrentVersionID != nil {
			row.Download = "/api/v1/files/" + string(f.ID) + "/versions/" + string(*f.CurrentVersionID) + "/download"
		}
	}
	if b, err := json.MarshalIndent(e.BestAnswer, "", "  "); err == nil {
		row.BestJSON = string(b)
	}
	if st, err := h.Svc.Status(r.Context(), p, e.FileID); err == nil {
		row.Flags = st.Flags
	}
	return row
}

func (h *Handler) reviewPage(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	items, _, err := h.Svc.Exceptions(r.Context(), p, ExceptionQuery{Status: status})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	data := pageData{Title: "Doc Tender review queue", CSRF: csrfOf(r), Admin: p.HasRole(domain.RoleAdmin), Reviewer: true, OpenCount: len(items)}
	if status != "open" {
		data.OpenCount, _ = h.Svc.OpenCount(r.Context(), p)
	}
	for _, e := range items {
		data.Items = append(data.Items, h.row(r, p, e))
	}
	h.render(w, "review.html", data, http.StatusOK)
}

func (h *Handler) itemPage(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	e, err := h.Svc.Exception(r.Context(), p, domain.ID(r.PathValue("exception_id")))
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	row := h.row(r, p, e)
	cats, _ := h.Svc.Categories(r.Context(), p)
	count, _ := h.Svc.OpenCount(r.Context(), p)
	data := pageData{Title: "Review: " + row.FileName, CSRF: csrfOf(r), Admin: p.HasRole(domain.RoleAdmin), Reviewer: true, Item: &row, Cats: cats,
		OpenCount: count, Notice: r.URL.Query().Get("notice")}
	h.render(w, "item.html", data, http.StatusOK)
}

func (h *Handler) resolveForm(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.pageErr(w, r, domain.Invalid("form", "the form did not parse"))
		return
	}
	in := Resolve{Action: r.PostForm.Get("action"), Name: strings.TrimSpace(r.PostForm.Get("name")), Grouping: r.PostForm.Get("grouping"), Note: r.PostForm.Get("note")}
	if v := r.PostForm.Get("category_id"); v != "" {
		id := domain.ID(v)
		in.CategoryID = &id
	}
	if v := r.PostForm.Get("matter_id"); v != "" {
		id := domain.ID(v)
		in.MatterID = &id
	}
	id := domain.ID(r.PathValue("exception_id"))
	if _, err := h.Svc.ResolveException(r.Context(), p, id, in); err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/doc-tender/review?notice="+url.QueryEscape("Resolved"), http.StatusSeeOther)
}

func (h *Handler) adminPage(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if err := adminOnly(p); err != nil {
		h.pageErr(w, r, err)
		return
	}
	h.renderAdmin(w, r, p, "", r.URL.Query().Get("notice"), http.StatusOK)
}

func (h *Handler) renderAdmin(w http.ResponseWriter, r *http.Request, p domain.Principal, errMsg, notice string, code int) {
	ctx := r.Context()
	naming, err := h.Svc.GetNaming(ctx, p)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	cats, err := h.Svc.Categories(ctx, p)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	spend, err := h.Svc.Spend(ctx, p, SpendQuery{})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	on, _ := h.Svc.boolSetting(ctx, "doctender.enabled")
	count, _ := h.Svc.OpenCount(ctx, p)
	choices := map[string][]string{TaskLabeling: ChoicesForTask(TaskLabeling), TaskCategorizing: ChoicesForTask(TaskCategorizing)}
	h.render(w, "admin.html", pageData{Title: "Doc Tender", CSRF: csrfOf(r), Admin: true, Reviewer: true, Error: errMsg, Notice: notice,
		Naming: naming, Cats: cats, Spend: spend, Enabled: on, OpenCount: count, Choices: choices}, code)
}

func (h *Handler) namingForm(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.pageErr(w, r, domain.Invalid("form", "the form did not parse"))
		return
	}
	if _, err := h.Svc.PutNaming(r.Context(), p, h.Settings, strings.TrimSpace(r.PostForm.Get("pattern"))); err != nil {
		var api *domain.APIError
		if errors.As(err, &api) && api.Code == domain.CodeValidation {
			h.renderAdmin(w, r, p, "The pattern was not saved: "+api.Message, "", http.StatusUnprocessableEntity)
			return
		}
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/doc-tender?notice="+url.QueryEscape("Naming pattern saved"), http.StatusSeeOther)
}

func (h *Handler) categoryForm(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.pageErr(w, r, domain.Invalid("form", "the form did not parse"))
		return
	}
	f := r.PostForm
	str := func(k string) *string {
		if !f.Has(k) {
			return nil
		}
		v := f.Get(k)
		return &v
	}
	in := CategoryWrite{Key: str("key"), Description: str("description"), Tier: str("tier"), Folder: str("folder_name")}
	active := f.Get("active") != "false"
	in.Active = &active
	var err error
	if id := f.Get("id"); id != "" {
		_, err = h.Svc.UpdateCategory(r.Context(), p, domain.ID(id), in)
	} else {
		_, err = h.Svc.CreateCategory(r.Context(), p, in)
	}
	if err != nil {
		var api *domain.APIError
		if errors.As(err, &api) && (api.Code == domain.CodeValidation || api.Code == domain.CodeConflict) {
			h.renderAdmin(w, r, p, "The category was not saved: "+api.Message, "", http.StatusUnprocessableEntity)
			return
		}
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/doc-tender?notice="+url.QueryEscape("Category saved"), http.StatusSeeOther)
}

func (h *Handler) guidePage(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	count, _ := h.Svc.OpenCount(r.Context(), p)
	h.render(w, "guide.html", pageData{Title: "Doc Tender guide", CSRF: csrfOf(r), Admin: p.HasRole(domain.RoleAdmin), Reviewer: reviewer(p), OpenCount: count}, http.StatusOK)
}

package practice

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves practice HTML and the practice JSON API.
// Principal is supplied by identity. Until that integration, a nil Principal rejects every call.
type Handler struct {
	Svc       *Service
	Templates *template.Template
	Root      string
	Principal func(*http.Request) (domain.Principal, error)
}

// NewHandler parses templates in root and returns a handler. root may be empty for JSON-only tests.
func NewHandler(svc *Service, root string, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Root: root, Principal: principal}
	if root != "" {
		set, err := template.ParseGlob(filepath.Join(root, "*.html"))
		if err != nil {
			return nil, err
		}
		h.Templates = set
	}
	return h, nil
}

// Routes returns the practice mux.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /practice/static/practice.css", h.staticCSS)
	mux.HandleFunc("GET /practice/matters", h.mattersPage)
	mux.HandleFunc("GET /practice/matters/needs-case-number", h.needsCasePage)
	mux.HandleFunc("GET /practice/matters/{matter_id}", h.matterPage)
	mux.HandleFunc("GET /practice/matters/{matter_id}/close", h.closePage)
	mux.HandleFunc("POST /practice/matters/{matter_id}/close", h.closePost)
	mux.HandleFunc("POST /practice/matters/{matter_id}/custom-fields", h.customPost)
	mux.HandleFunc("GET /practice/search", h.searchPage)
	mux.HandleFunc("GET /practice/notes", h.notesPage)
	mux.HandleFunc("GET /practice/contacts", h.contactsPage)
	mux.HandleFunc("GET /practice/contacts/{contact_id}", h.contactPage)
	mux.HandleFunc("GET /practice/activity", h.activityPage)

	mux.HandleFunc("GET /api/v1/matters", h.mattersJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}", h.matterJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/history", h.historyJSON)
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/close", h.closeJSON)
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/reopen", h.reopenJSON)
	mux.HandleFunc("PUT /api/v1/matters/{matter_id}/custom-fields", h.customJSON)
	mux.HandleFunc("GET /api/v1/search", h.searchJSON)
	mux.HandleFunc("GET /api/v1/reports/matters", h.reportMatters)
	mux.HandleFunc("GET /api/v1/reports/ar-aging", h.reportAging)
	mux.HandleFunc("GET /api/v1/reports/trust-balances", h.reportTrust)
	mux.HandleFunc("GET /api/v1/widgets/my-open-matters", h.widgetOpen)
	mux.HandleFunc("GET /api/v1/widgets/pending-conflict-checks", h.widgetConflicts)
	return mux
}

func (h *Handler) principal(r *http.Request) (domain.Principal, error) {
	if h.Principal == nil {
		return domain.Principal{}, domain.Unauthenticated("sign in required")
	}
	return h.Principal(r)
}

func (h *Handler) staticCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	http.ServeFile(w, r, filepath.Join(h.Root, "practice.css"))
}

type page struct {
	Title    string
	CSRF     string
	Query    string
	Scope    string
	Notice   string
	Matters  []MatterView
	Matter   MatterView
	History  []HistoryRow
	Timeline []TimelineEntry
	Warnings []Warning
	Search   SearchResult
	Notes    []domain.Note
	Contacts []domain.Contact
	Contact  domain.Contact
	Activity []ActivityGroup
	Needs    []domain.Matter
}

func (h *Handler) render(w http.ResponseWriter, name string, data page) {
	if h.Templates == nil {
		http.Error(w, "templates are not loaded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) mattersPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListMatters(r.Context(), p, r.URL.Query().Get("status"), domain.ID(""))
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "matters.html", page{Title: "Matters", CSRF: h.csrf(w, r), Matters: rows})
}

func (h *Handler) matterPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	view, err := h.Svc.GetMatter(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	history, err := h.Svc.History(r.Context(), p, "matter", id)
	if err != nil {
		writeErr(w, err)
		return
	}
	timeline, err := h.Svc.Timeline(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "matter.html", page{
		Title: view.Matter.Number + " " + view.Matter.Name, CSRF: h.csrf(w, r),
		Matter: view, History: history, Timeline: timeline,
	})
}

func (h *Handler) closePage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	row, warnings, err := h.Svc.PreviewClose(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "close.html", page{Title: "Close " + row.Number, CSRF: h.csrf(w, r), Matter: view, Warnings: warnings})
}

func (h *Handler) closePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, domain.Invalid("form", "form is invalid"))
		return
	}
	if err := h.checkCSRF(r); err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	_, _, err = h.Svc.CloseMatter(r.Context(), p, id, CloseInput{
		Reason: r.FormValue("reason"), Acknowledge: r.FormValue("acknowledge") == "1",
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) customPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, domain.Invalid("form", "form is invalid"))
		return
	}
	if err := h.checkCSRF(r); err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	view, err := h.Svc.GetMatter(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var values []CustomInput
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "cf_") || len(vals) == 0 {
			continue
		}
		values = append(values, CustomInput{DefinitionID: domain.ID(strings.TrimPrefix(key, "cf_")), Value: vals[0]})
	}
	if err := h.Svc.SetCustomFields(r.Context(), p, RecordMatter, id, view.Matter.PracticeAreaID, values); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) searchPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query().Get("q")
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = ScopeAll
	}
	result, err := h.Svc.Search(r.Context(), p, q, scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "search.html", page{Title: "Search", CSRF: h.csrf(w, r), Query: q, Scope: scope, Search: result})
}

func (h *Handler) notesPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	notes, err := h.Svc.ListNotes(r.Context(), p, NoteFilter{Query: r.URL.Query().Get("q")})
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "notes.html", page{Title: "Notes", CSRF: h.csrf(w, r), Notes: notes, Query: r.URL.Query().Get("q")})
}

func (h *Handler) contactsPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListContacts(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "contacts.html", page{Title: "Contacts", CSRF: h.csrf(w, r), Contacts: rows})
}

func (h *Handler) contactPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	row, err := h.Svc.GetContact(r.Context(), p, domain.ID(r.PathValue("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "contact.html", page{Title: row.FullName, CSRF: h.csrf(w, r), Contact: row})
}

func (h *Handler) activityPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ActivityFeed(r.Context(), p, r.URL.Query().Get("actor"))
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "activity.html", page{Title: "Activities", CSRF: h.csrf(w, r), Activity: rows})
}

func (h *Handler) needsCasePage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.NeedsCaseNumber(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.render(w, "needs_case_number.html", page{Title: "Matters missing a case number", CSRF: h.csrf(w, r), Needs: rows})
}

func (h *Handler) mattersJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		csv, err := h.Svc.ExportMattersCSV(r.Context(), p)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write([]byte(csv))
		return
	}
	rows, err := h.Svc.ListMatters(r.Context(), p, r.URL.Query().Get("status"), domain.ID(r.URL.Query().Get("practice_area_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": matterItems(rows)})
}

func (h *Handler) matterJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matterItem(view))
}

func (h *Handler) historyJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.History(r.Context(), p, "matter", domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{
			"id": row.ID, "at": row.At.UTC().Format(timeRFC3339), "actor_id": row.ActorID,
			"role": row.Role, "source": row.Source, "entity_type": row.Entity, "field": row.Field,
			"old_value": historyValue(row, row.Old), "new_value": historyValue(row, row.New),
			"reason": historyValue(row, row.Reason), "kind": row.Kind,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func historyValue(row HistoryRow, value string) any {
	if row.Redacted {
		return map[string]bool{"redacted": true}
	}
	return value
}

func (h *Handler) closeJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Reason      string `json:"reason"`
		Acknowledge bool   `json:"acknowledge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	matter, record, err := h.Svc.CloseMatter(r.Context(), p, domain.ID(r.PathValue("matter_id")), CloseInput{
		Reason: body.Reason, Acknowledge: body.Acknowledge, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, matter.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matter": matterItem(view), "warnings": record.Warnings, "acknowledged": record.Acknowledged,
	})
}

func (h *Handler) reopenJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	matter, err := h.Svc.ReopenMatter(r.Context(), p, domain.ID(r.PathValue("matter_id")), body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, matter.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matterItem(view))
}

func (h *Handler) customJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	view, err := h.Svc.GetMatter(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	values, err := decodeCustom(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.SetCustomFields(r.Context(), p, RecordMatter, id, view.Matter.PracticeAreaID, values); err != nil {
		writeErr(w, err)
		return
	}
	view, err = h.Svc.GetMatter(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matterItem(view))
}

func (h *Handler) searchJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	result, err := h.Svc.Search(r.Context(), p, r.URL.Query().Get("q"), r.URL.Query().Get("scope"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) reportMatters(w http.ResponseWriter, r *http.Request) {
	h.report(w, r, func(p domain.Principal) (ReportTable, error) {
		return h.Svc.MattersReport(r.Context(), p)
	})
}

func (h *Handler) reportAging(w http.ResponseWriter, r *http.Request) {
	h.report(w, r, func(p domain.Principal) (ReportTable, error) {
		return h.Svc.ARAging(r.Context(), p, r.URL.Query().Get("as_of"))
	})
}

func (h *Handler) reportTrust(w http.ResponseWriter, r *http.Request) {
	h.report(w, r, func(p domain.Principal) (ReportTable, error) {
		return h.Svc.TrustBalancesReport(r.Context(), p)
	})
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request, load func(domain.Principal) (ReportTable, error)) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	table, err := load(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = w.Write([]byte(reportCSV(table)))
		return
	}
	writeJSON(w, http.StatusOK, table)
}

func (h *Handler) widgetOpen(w http.ResponseWriter, r *http.Request) {
	h.widget(w, r, func(p domain.Principal) (Widget, error) {
		return h.Svc.OpenMattersWidget(r.Context(), p)
	})
}

func (h *Handler) widgetConflicts(w http.ResponseWriter, r *http.Request) {
	h.widget(w, r, func(p domain.Principal) (Widget, error) {
		return h.Svc.ConflictWidget(r.Context(), p)
	})
}

func (h *Handler) widget(w http.ResponseWriter, r *http.Request, load func(domain.Principal) (Widget, error)) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	widget, err := load(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": widget.ID, "title": widget.Title, "page_path": widget.PagePath,
		"target": map[string]any{"path": widget.PagePath, "query": widget.Query},
		"rows":   widget.Rows,
	})
}

func matterItems(rows []MatterView) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, matterItem(row))
	}
	return out
}

func matterItem(view MatterView) map[string]any {
	fields := make([]map[string]string, 0, len(view.CustomFields))
	for _, field := range view.CustomFields {
		fields = append(fields, map[string]string{
			"definition_id": string(field.DefinitionID), "key": field.Key, "label": field.Label, "value": field.Value,
		})
	}
	markers := make([]string, 0, len(view.Markers))
	for _, marker := range view.Markers {
		markers = append(markers, marker.Label)
	}
	return map[string]any{
		"id": view.Matter.ID, "number": view.Matter.Number, "name": view.Matter.Name,
		"status": view.Matter.Status, "practice_area_id": view.Matter.PracticeAreaID,
		"closed_on": view.Matter.ClosedOn, "close_reason": view.Matter.CloseReason,
		"custom_fields": fields, "markers": markers, "conflicts_unchecked": view.Unchecked,
	}
}

func decodeCustom(r *http.Request) ([]CustomInput, error) {
	var body struct {
		Values []map[string]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, domain.Invalid("body", "body is invalid")
	}
	var out []CustomInput
	for _, raw := range body.Values {
		id, _ := raw["definition_id"].(string)
		key, _ := raw["key"].(string)
		value, err := oneCustomValue(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, CustomInput{DefinitionID: domain.ID(id), Key: key, Value: value})
	}
	return out, nil
}

func oneCustomValue(raw map[string]any) (string, error) {
	keys := []string{"text", "long_text", "number", "money_cents", "date", "boolean", "pick", "contact_id"}
	found := ""
	count := 0
	for _, key := range keys {
		val, ok := raw[key]
		if !ok {
			continue
		}
		count++
		switch v := val.(type) {
		case string:
			found = v
		case float64:
			found = strconvTrim(v)
		case bool:
			if v {
				found = "true"
			} else {
				found = "false"
			}
		default:
			return "", domain.Invalid("custom_fields", "value is invalid")
		}
	}
	if count != 1 {
		return "", domain.Invalid("custom_fields", "set exactly one value")
	}
	return found, nil
}

func strconvTrim(v float64) string {
	if v == float64(int64(v)) {
		return jsonNumber(int64(v))
	}
	return jsonNumberFloat(v)
}

func jsonNumber(v int64) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
		func() string { b, _ := json.Marshal(v); return string(b) }(), " ", ""), "\n", ""))
}

func jsonNumberFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func reportCSV(table ReportTable) string {
	rows := [][]string{table.Columns}
	for _, row := range table.Rows {
		line := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			line[i] = row[col]
		}
		rows = append(rows, line)
	}
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(csvJoin(row))
		b.WriteByte('\n')
	}
	return b.String()
}

const timeRFC3339 = "2006-01-02T15:04:05Z"

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(api.HTTPStatus())
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	token := newToken()
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: token, Path: "/practice", HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return token
}

func (h *Handler) checkCSRF(r *http.Request) error {
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" || c.Value != r.FormValue("csrf") {
		return domain.Forbidden("csrf")
	}
	return nil
}

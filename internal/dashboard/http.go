package dashboard

import (
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
)

// Handler serves the dashboard JSON API and the shell screens: home, search,
// notifications, timers, My settings, My profile, Help & Training and the admin page.
type Handler struct {
	Svc  *Service
	Help *help.Service
	// Principal authenticates the request (session cookie with CSRF on writes, or a
	// bearer token with the route's scopes). The core supplies it.
	Principal func(*http.Request) (domain.Principal, error)
	// CSRF returns the session's CSRF token for forms and the script's X-CSRF-Token.
	CSRF func(*http.Request) string
	// Static serves web/static under /ui/static/.
	Static fs.FS
	// Accounts backs Users and roles, the Agents and API token panel and My profile.
	Accounts *Accounts
	// Audit returns recent audit rows as p may read them (walls, attorney-only redaction).
	Audit func(r *http.Request, p domain.Principal, limit int) ([]AuditRow, error)
	// Modules backs the Modules and marketplace tab; nil shows its settings only.
	Modules ModulesAdmin
	// AICalls returns the AI call log panel on the AI and inference tab for p (rendered
	// by admin.html under .AICalls); the core's module runtime checks the admin role and
	// matter walls, as GET /api/v1/ai/calls does. Nil shows the tab's settings only.
	AICalls func(r *http.Request, p domain.Principal) (any, error)

	tpl *template.Template
}

// AuditRow is one audit log line on the System tab.
type AuditRow struct {
	At     time.Time
	Actor  string
	Role   string
	Source string
	Entity string
	Field  string
	Old    string
	New    string
	// Redacted marks attorney-only values the viewer may not read; Old and New are then
	// the redaction marker, never the stored values.
	Redacted bool
}

// NewHandler parses the shell, dashboard and admin templates (web/templates/shell,
// dashboard and admin). Every fs must hold *.html at its root.
func NewHandler(svc *Service, hs *help.Service, principal func(*http.Request) (domain.Principal, error), csrf func(*http.Request) string, static fs.FS, templates ...fs.FS) (*Handler, error) {
	h := &Handler{Svc: svc, Help: hs, Principal: principal, CSRF: csrf, Static: static}
	t := template.New("ui").Funcs(funcs)
	for _, f := range templates {
		var err error
		if t, err = t.ParseFS(f, "*.html"); err != nil {
			return nil, err
		}
	}
	h.tpl = t
	return h, nil
}

// Routes returns the dashboard mux. JSON routes follow the contract paths.
func (h *Handler) Routes() *http.ServeMux {
	m := http.NewServeMux()
	api := func(pattern string, fn func(http.ResponseWriter, *http.Request, domain.Principal)) {
		m.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, err := h.Principal(r)
			if err != nil {
				help.WriteErr(w, err)
				return
			}
			fn(w, r, p)
		})
	}
	api("GET /api/v1/nav/context", h.navJSON)
	api("GET /api/v1/me/sidebar", h.sidebarJSON)
	api("PUT /api/v1/me/sidebar", h.putSidebarJSON)
	api("GET /api/v1/me/settings", h.meSettingsJSON)
	api("PATCH /api/v1/me/settings", h.patchMeSettingsJSON)
	api("GET /api/v1/me/notification-preferences", h.notifyPrefsJSON)
	api("PUT /api/v1/me/notification-preferences", h.putNotifyPrefsJSON)
	api("GET /api/v1/widgets", h.widgetsJSON)
	api("GET /api/v1/widgets/{widget_id}", h.widgetJSON)
	api("GET /api/v1/me/layout", h.workingJSON)
	api("PUT /api/v1/me/layout", h.putWorkingJSON)
	api("GET /api/v1/me/layouts", h.layoutsJSON)
	api("POST /api/v1/me/layouts", h.createLayoutJSON)
	api("GET /api/v1/me/layouts/{layout_id}", h.layoutJSON)
	api("PATCH /api/v1/me/layouts/{layout_id}", h.patchLayoutJSON)
	api("DELETE /api/v1/me/layouts/{layout_id}", h.deleteLayoutJSON)
	api("POST /api/v1/me/layouts/{layout_id}/default", h.defaultLayoutJSON)
	api("POST /api/v1/me/layouts/{layout_id}/apply", h.applyLayoutJSON)
	api("GET /api/v1/me/layout-reset-options", h.resetOptionsJSON)
	api("GET /api/v1/role-layouts/{role}", h.roleLayoutJSON)
	api("PUT /api/v1/role-layouts/{role}", h.putRoleLayoutJSON)
	api("GET /api/v1/settings/appearance", h.appearanceJSON)
	api("PUT /api/v1/settings/appearance", h.putAppearanceJSON)
	api("GET /api/v1/problem-reports", h.problemsJSON)
	api("POST /api/v1/problem-reports", h.createProblemJSON)
	api("GET /api/v1/problem-reports/{problem_report_id}", h.problemJSON)
	h.pageRoutes(m)
	h.modulesRoutes(m)
	return m
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, rows any) {
	switch v := rows.(type) {
	case []Layout:
		help.ListPage(w, r, h.Svc.d.Settings, v)
	case []WidgetDef:
		help.ListPage(w, r, h.Svc.d.Settings, v)
	case []ProblemReport:
		help.ListPage(w, r, h.Svc.d.Settings, v)
	}
}

func (h *Handler) navJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	nc, err := h.Svc.Nav(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, nc)
}

func (h *Handler) sidebarJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	st, err := h.Svc.Sidebar(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) putSidebarJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in SidebarState
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	st, err := h.Svc.PutSidebar(r.Context(), p, in)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) meSettingsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	m, err := h.Svc.MySettings(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) patchMeSettingsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in MeSettingsPatch
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	m, err := h.Svc.UpdateMySettings(r.Context(), p, in)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) notifyPrefsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.NotificationPrefs(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) putNotifyPrefsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in struct {
		Items []NotificationPref `json:"items"`
	}
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	rows, err := h.Svc.PutNotificationPrefs(r.Context(), p, in.Items)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) widgetsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	defs, err := h.Svc.Widgets(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	h.list(w, r, defs)
}

func widgetFilters(r *http.Request) map[string]string {
	f := map[string]string{}
	for _, k := range []string{"from_date", "to_date", "matter_id"} {
		if v := r.URL.Query().Get(k); v != "" {
			f[k] = v
		}
	}
	return f
}

func (h *Handler) widgetJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	wd, err := h.Svc.LoadWidget(r.Context(), p, r.PathValue("widget_id"), widgetFilters(r))
	if err != nil {
		if IsUnavailable(err) {
			help.WriteErr(w, domain.NotFound("widget data"))
			return
		}
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, wd)
}

// workingLayout is the working layout in the contract HomeLayout shape.
func (h *Handler) workingLayout(r *http.Request, p domain.Principal, ws []LayoutWidget) Layout {
	return Layout{ID: RoleOptionID, Name: "Current layout", CreatedBy: p.UserID, CreatedAt: h.Svc.d.Now().UTC(), UpdatedAt: h.Svc.d.Now().UTC(), Widgets: ws}
}

func (h *Handler) workingJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	ws, err := h.Svc.Working(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, h.workingLayout(r, p, ws))
}

type layoutWrite struct {
	Name    *string         `json:"name"`
	Widgets *[]LayoutWidget `json:"widgets"`
}

func (h *Handler) putWorkingJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in layoutWrite
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	if in.Widgets == nil {
		help.WriteErr(w, domain.Invalid("widgets", "widgets is required"))
		return
	}
	ws, err := h.Svc.PutWorking(r.Context(), p, *in.Widgets)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, h.workingLayout(r, p, ws))
}

func (h *Handler) layoutsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	ls, err := h.Svc.Layouts(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	h.list(w, r, ls)
}

func (h *Handler) createLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in layoutWrite
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	if in.Name == nil || in.Widgets == nil {
		help.WriteErr(w, domain.Invalid("body", "name and widgets are required"))
		return
	}
	l, err := h.Svc.SaveLayout(r.Context(), p, *in.Name, *in.Widgets)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusCreated, l)
}

func (h *Handler) layoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := h.Svc.Layout(r.Context(), p, domain.ID(r.PathValue("layout_id")))
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) patchLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in layoutWrite
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	l, err := h.Svc.UpdateLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")), in.Name, in.Widgets)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) deleteLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	if err := h.Svc.DeleteLayout(r.Context(), p, domain.ID(r.PathValue("layout_id"))); err != nil {
		help.WriteErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) defaultLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := h.Svc.SetDefaultLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")))
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) applyLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := h.Svc.ApplyLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")))
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) resetOptionsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	ls, err := h.Svc.ResetOptions(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	h.list(w, r, ls)
}

func (h *Handler) roleLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	l, err := h.Svc.GetRoleLayout(r.Context(), p, r.PathValue("role"))
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) putRoleLayoutJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in RoleLayout
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	if in.Role != "" && string(in.Role) != r.PathValue("role") {
		help.WriteErr(w, domain.Invalid("role", "the body role must match the path"))
		return
	}
	l, err := h.Svc.SetRoleLayout(r.Context(), p, r.PathValue("role"), in.Widgets)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, l)
}

func (h *Handler) appearanceJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	a, err := h.Svc.GetAppearance(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) putAppearanceJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in Appearance
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	a, err := h.Svc.SetAppearance(r.Context(), p, in)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusOK, a)
}

func (h *Handler) problemsJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Problems(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	h.list(w, r, rows)
}

func (h *Handler) problemJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Problems(r.Context(), p)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	for _, row := range rows {
		if string(row.ID) == r.PathValue("problem_report_id") {
			help.WriteJSON(w, http.StatusOK, row)
			return
		}
	}
	help.WriteErr(w, domain.NotFound("problem report"))
}

func (h *Handler) createProblemJSON(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var in struct {
		Page    string `json:"page"`
		Message string `json:"message"`
	}
	if err := help.Decode(r, &in); err != nil {
		help.WriteErr(w, err)
		return
	}
	rep, _, err := h.Svc.ReportProblem(r.Context(), p, in.Page, in.Message)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	help.WriteJSON(w, http.StatusCreated, rep)
}

// pathOnly keeps a same-site path for redirects and the problem form's page field.
func pathOnly(s string) string {
	if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") {
		return "/"
	}
	return s
}

package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/frame"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Chrome is what the shell needs on every staff page: the sidebar, the header and the
// title bar's ? link. Other domains' screens can render inside the same shell with it.
type Chrome struct {
	Title       string
	Path        string
	Nav         NavContext
	Theme       Theme
	UserName    string
	Initials    string
	Picture     string
	RoleLabel   string
	Unread      int
	CSRF        string
	HelpPath    string
	SearchDelay int
	MinChars    int
	Scopes      []struct{ ID, Label string }
	IdleSeconds int
	CanTime     bool
	Notice      string
	Error       string
	Active      string
	// Area and Scripts come from a framed area screen (frame.Page): main's area-<Area>
	// class and the deferred scripts its content needs.
	Area    string
	Scripts []string
	// WorkInFrame (frame.Page) turns the client idle sign-out off.
	WorkInFrame bool
}

type view struct {
	Chrome
	Page any
}

var funcs = template.FuncMap{
	"url":   func(t Target) string { return t.URL() },
	"join":  strings.Join,
	"lower": strings.ToLower,
	"when": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format("Jan 2, 2006 15:04 UTC")
	},
	"md":       func(s string) template.HTML { return template.HTML(help.RenderMarkdown(s)) },
	"tabLabel": tabLabel,
	"inc":      func(i int) int { return i + 1 },
	"has": func(list []string, v string) bool {
		for _, x := range list {
			if x == v {
				return true
			}
		}
		return false
	},
	"deref": func(p *int) (n int) {
		if p != nil {
			n = *p
		}
		return n
	},
	"str":         func(id domain.ID) string { return string(id) },
	"idleMinutes": func(secs int) int { return secs / 60 },
	"json": func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
	"dict2": func(e NavEntry, active string) map[string]any {
		return map[string]any{"E": e, "Active": e.ID == active}
	},
}

// activeEntry is the sidebar entry whose path is the longest prefix of path. A screen no
// entry covers (Trust flags, the Doc Tender guide) lights the first entry of its area: the
// one whose path starts with the same first segment.
func activeEntry(nav NavContext, path string) string {
	best, bestLen := "", -1
	for _, e := range nav.Entries {
		p := e.Path
		match := path == p || (p != "/" && strings.HasPrefix(path, p+"/"))
		if p == "/" && (path == "/" || path == "/home") {
			match = true
		}
		if match && len(p) > bestLen {
			best, bestLen = e.ID, len(p)
		}
	}
	if best != "" {
		return best
	}
	seg := firstSegment(path)
	for _, e := range nav.Entries {
		if seg != "" && firstSegment(e.Path) == seg {
			return e.ID
		}
	}
	return ""
}

func firstSegment(path string) string {
	s, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	return s
}

func initials(name string) string {
	var out []rune
	for _, f := range strings.Fields(name) {
		if len(out) < fixed.Initials {
			r, _ := utf8.DecodeRuneInString(strings.ToUpper(f))
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

func roleLabel(roles []domain.Role) string {
	var parts []string
	for _, r := range roles {
		s := string(r)
		parts = append(parts, capitalize(s))
	}
	return strings.Join(parts, ", ")
}

// ChromeFor builds the shell for p on r.
func (h *Handler) ChromeFor(r *http.Request, p domain.Principal, title string) (Chrome, error) {
	ctx := r.Context()
	nav, err := h.Svc.Nav(ctx, p)
	if err != nil {
		return Chrome{}, err
	}
	c := Chrome{
		Title: title, Path: r.URL.Path, Nav: nav, Theme: h.Svc.ThemeFor(ctx, p), Unread: h.Svc.UnreadCount(ctx, p),
		SearchDelay: h.Svc.SearchDelayMS(ctx), MinChars: h.Svc.intSetting(ctx, "search.min_chars"),
		Scopes: Scopes, RoleLabel: roleLabel(p.Roles), CanTime: roleReads(p, domain.ResTime),
	}
	c.Active = activeEntry(nav, r.URL.Path)
	if h.CSRF != nil {
		c.CSRF = h.CSRF(r)
	}
	if h.Svc.d.Users != nil {
		if u, err := h.Svc.d.Users.Get(ctx, p.UserID); err == nil {
			c.UserName = u.Name
		}
	}
	c.Initials = initials(c.UserName)
	if h.Accounts != nil {
		if me, err := h.Accounts.Me(ctx, p); err == nil {
			c.Initials = me.Initials
			if me.HasPicture {
				c.Picture = "/ui/pictures/" + url.PathEscape(string(p.UserID)) + "?v=" + strconv.Itoa(int(me.PictureAt.UnixMilli()))
			}
		}
	}
	if m, err := h.Svc.MySettings(ctx, p); err == nil {
		c.IdleSeconds = m.IdleTimeoutSeconds
	}
	if h.Help != nil {
		path := r.URL.Path
		if path == "/admin" {
			tab := r.URL.Query().Get("tab")
			if tab == "" {
				tab = adminTabs[0].ID
			}
			if t, ok := h.Help.Guide().ForTab(tab); ok {
				c.HelpPath = t.Path()
			}
		} else if t, ok := h.Help.Guide().ForPath(path); ok {
			c.HelpPath = t.Path()
		}
	}
	c.Notice, c.Error = r.URL.Query().Get("notice"), r.URL.Query().Get("error")
	c.Notice, _ = truncate(c.Notice, fixed.NoticeMax)
	c.Error, _ = truncate(c.Error, fixed.NoticeMax)
	return c, nil
}

func (h *Handler) render(w http.ResponseWriter, code int, name string, v any) {
	var buf bytes.Buffer
	if err := h.tpl.ExecuteTemplate(&buf, name, v); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	// A framed area screen may have set a policy its content needs (the editor's frame);
	// every policy an area sets is at least as strict as the shell's for its own assets.
	if hdr.Get("Content-Security-Policy") == "" {
		hdr.Set("Content-Security-Policy", help.CSP)
	}
	if hdr.Get("Cache-Control") == "" {
		hdr.Set("Cache-Control", "private, no-store")
	}
	hdr.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = buf.WriteTo(w)
}

func isCode(err error, code string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == code
}

// screen wraps a page handler: sign-in redirect, client 404, then the shell.
func (h *Handler) screen(fn func(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Principal(r)
		if err != nil {
			if isCode(err, domain.CodeUnauthenticated) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			help.WriteErr(w, err)
			return
		}
		if err := staff(p); err != nil {
			http.NotFound(w, r)
			return
		}
		name, title, page, err := fn(w, r, p)
		if err != nil {
			if isCode(err, domain.CodeNotFound) {
				http.NotFound(w, r)
				return
			}
			if isCode(err, domain.CodeForbidden) {
				h.renderError(w, r, p, http.StatusForbidden, "You don't have access to this page.")
				return
			}
			help.WriteErr(w, err)
			return
		}
		if name == "" {
			return // the handler wrote a redirect or a partial
		}
		c, err := h.ChromeFor(r, p, title)
		if err != nil {
			help.WriteErr(w, err)
			return
		}
		h.render(w, http.StatusOK, name, view{Chrome: c, Page: page})
	}
}

// Frame is the core's frame.Shell: it draws an area screen (practice, billing, trust,
// calendar, files, Doc Tender, the editor, settings, a module tab) inside the shell. A
// caller the shell does not serve (a client, or no session) gets the plain document.
func (h *Handler) Frame(w http.ResponseWriter, r *http.Request, pg frame.Page) {
	p, err := h.Principal(r)
	if err != nil || staff(p) != nil {
		frame.Bare(w, pg)
		return
	}
	c, err := h.ChromeFor(r, p, pg.Title)
	if err != nil {
		frame.Bare(w, pg)
		return
	}
	c.Area, c.Scripts, c.WorkInFrame = pg.Area, pg.Scripts, pg.WorkInFrame
	if pg.Notice != "" {
		c.Notice, _ = truncate(pg.Notice, fixed.NoticeMax)
	}
	if pg.Error != "" {
		c.Error = pg.Error
	}
	h.render(w, pg.StatusCode(), "framed", view{Chrome: c, Page: pg.Body})
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, p domain.Principal, code int, msg string) {
	c, err := h.ChromeFor(r, p, "Not available")
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	c.Error = msg
	h.render(w, code, "error.html", view{Chrome: c})
}

// form wraps a POST: it authenticates (CSRF is checked by Principal), runs fn and
// redirects to the page fn names, carrying a notice or the error text.
func (h *Handler) form(fn func(r *http.Request, p domain.Principal) (string, string, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Principal(r)
		if err != nil {
			if isCode(err, domain.CodeUnauthenticated) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			help.WriteErr(w, err)
			return
		}
		if err := staff(p); err != nil {
			http.NotFound(w, r)
			return
		}
		back, notice, err := fn(r, p)
		if err != nil {
			var api *domain.APIError
			msg := "Something went wrong. Try again."
			if errors.As(err, &api) {
				msg = api.Message
			}
			if back == "" {
				back = "/"
			}
			http.Redirect(w, r, withParam(back, "error", msg), http.StatusSeeOther)
			return
		}
		if notice != "" {
			back = withParam(back, "notice", notice)
		}
		http.Redirect(w, r, pathOnly(back), http.StatusSeeOther)
	}
}

func withParam(path, k, v string) string {
	u, err := url.Parse(pathOnly(path))
	if err != nil {
		return "/"
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *Handler) pageRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /ui/static/{rest...}", h.static)
	m.HandleFunc("GET /ui/appearance.css", h.appearanceCSS)
	m.HandleFunc("GET /{$}", h.screen(h.homePage))
	m.HandleFunc("GET /home", h.screen(h.homePage))
	m.HandleFunc("POST /home/layouts", h.form(h.saveLayoutPost))
	m.HandleFunc("POST /home/layouts/{layout_id}/apply", h.form(h.applyLayoutPost))
	m.HandleFunc("POST /home/layouts/{layout_id}/default", h.form(h.defaultLayoutPost))
	m.HandleFunc("POST /home/layouts/{layout_id}/rename", h.form(h.renameLayoutPost))
	m.HandleFunc("POST /home/layouts/{layout_id}/overwrite", h.form(h.overwriteLayoutPost))
	m.HandleFunc("POST /home/layouts/{layout_id}/delete", h.form(h.deleteLayoutPost))
	m.HandleFunc("POST /home/reset", h.form(h.resetPost))
	m.HandleFunc("POST /home/widgets", h.form(h.addWidgetPost))
	m.HandleFunc("POST /home/widgets/{index}/remove", h.form(h.removeWidgetPost))
	m.HandleFunc("GET /search", h.screen(h.searchPage))
	m.HandleFunc("GET /ui/search", h.partial(h.searchPartial))
	m.HandleFunc("GET /notifications", h.screen(h.notificationsPage))
	m.HandleFunc("GET /ui/notifications", h.partial(h.notificationsPartial))
	m.HandleFunc("POST /notifications/{notification_id}/open", h.form(h.openNotificationPost))
	m.HandleFunc("POST /notifications/read-all", h.form(h.readAllPost))
	m.HandleFunc("GET /timers", h.screen(h.timersPage))
	m.HandleFunc("GET /ui/timers", h.partial(h.timersPartial))
	m.HandleFunc("GET /me/settings", h.screen(h.meSettingsPage))
	m.HandleFunc("POST /me/settings", h.form(h.meSettingsPost))
	m.HandleFunc("POST /me/notification-preferences", h.form(h.notifyPrefsPost))
	m.HandleFunc("GET /me/profile", h.screen(h.profilePage))
	h.accountRoutes(m)
	m.HandleFunc("POST /ui/sidebar", h.form(h.sidebarPost))
	m.HandleFunc("POST /problem-reports", h.form(h.problemPost))
	m.HandleFunc("GET /quick-create", h.screen(h.quickCreatePage))
	m.HandleFunc("GET /help", h.screen(h.helpPage))
	m.HandleFunc("GET /help/topics/{topic_id}", h.screen(h.topicPage))
	m.HandleFunc("GET /admin", h.screen(h.adminPage))
	m.HandleFunc("GET /admin/settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin?tab="+url.QueryEscape(r.URL.Query().Get("tab")), http.StatusSeeOther)
	})
	m.HandleFunc("POST /admin/settings/{setting_key}", h.form(h.settingPost))
	m.HandleFunc("POST /admin/settings/{setting_key}/reset", h.form(h.settingResetPost))
	m.HandleFunc("POST /admin/appearance", h.form(h.appearancePost))
	m.HandleFunc("POST /admin/role-layouts/{role}", h.form(h.roleLayoutPost))
	m.HandleFunc("POST /admin/role-layouts/{role}/widgets", h.form(h.roleAddWidgetPost))
	m.HandleFunc("POST /admin/role-layouts/{role}/widgets/{index}/remove", h.form(h.roleRemoveWidgetPost))
	m.HandleFunc("POST /admin/help/sections", h.form(h.helpSectionPost))
	m.HandleFunc("POST /admin/help/sections/{section_id}/delete", h.form(h.helpSectionDeletePost))
	m.HandleFunc("POST /admin/help/items", h.form(h.helpItemPost))
	m.HandleFunc("POST /admin/help/items/{item_id}/delete", h.form(h.helpItemDeletePost))
}

// partial serves an HTMX fragment without the shell.
func (h *Handler) partial(fn func(r *http.Request, p domain.Principal) (string, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.Principal(r)
		if err != nil {
			help.WriteErr(w, err)
			return
		}
		if err := staff(p); err != nil {
			http.NotFound(w, r)
			return
		}
		name, v, err := fn(r, p)
		if err != nil {
			help.WriteErr(w, err)
			return
		}
		c := Chrome{}
		if h.CSRF != nil {
			c.CSRF = h.CSRF(r)
		}
		h.render(w, http.StatusOK, name, view{Chrome: c, Page: v})
	}
}

func (h *Handler) static(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("rest")
	if h.Static == nil || strings.Contains(name, "..") || strings.HasSuffix(name, ".go") {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, h.Static, name)
}

// appearanceCSS is the viewer's theme as CSS custom properties. It is rebuilt on every
// request from the settings, so a change shows on the next page load.
func (h *Handler) appearanceCSS(w http.ResponseWriter, r *http.Request) {
	p, _ := h.Principal(r)
	t := h.Svc.ThemeFor(r.Context(), p)
	size := map[string]string{"sm": "14px", "md": "16px", "lg": "18px"}[t.EffectiveSize]
	if size == "" {
		size = "16px"
	}
	// ponytail: ceiling: the eight PRD fonts are named, not shipped; a browser without
	// them installed uses the serif or sans fallback. Upgrade path: vendor the WOFF2
	// files (all SIL OFL) under web/static/fonts with @font-face rules here. Trigger:
	// the release task's visual sign-off or a firm reporting the fallback.
	font := func(name string) string {
		for _, f := range Fonts {
			if f.Name == name {
				if f.Serif {
					return `"` + name + `", Georgia, "Times New Roman", serif`
				}
				return `"` + name + `", system-ui, -apple-system, "Segoe UI", sans-serif`
			}
		}
		return `system-ui, sans-serif`
	}
	pr, _ := presetByID(t.Preset)
	sidebarText, headerText := pr.SidebarText, pr.HeaderText
	if sidebarText == "" {
		sidebarText, headerText = "#ffffff", "#ffffff"
	}
	var b strings.Builder
	fmt.Fprintf(&b, ":root{--sidebar:%s;--sidebar-text:%s;--header:%s;--header-text:%s;--page:%s;--accent:%s;--client-header:%s;--font-head:%s;--font-body:%s;--text-size:%s}\n",
		t.StaffSidebar, sidebarText, t.StaffHeader, headerText, t.StaffPage, t.StaffAccent, t.ClientHeader, font(t.HeadingFont), font(t.BodyFont), size)
	dark := ":root{--page:#15181d;--surface:#1f242b;--text:#e8e6e1;--muted:#a9b0ba;--line:#343b45}\n"
	switch t.EffectiveMode {
	case "dark":
		b.WriteString(dark)
	case "device":
		b.WriteString("@media (prefers-color-scheme: dark){" + dark + "}\n")
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(b.String()))
}

// --- Home ---

type widgetView struct {
	LayoutWidget
	Index   int
	Title   string
	Target  string
	Rows    []WidgetRow
	Missing string
}

type homeView struct {
	Widgets  []widgetView
	Layouts  []Layout
	Options  []Layout
	Picker   []WidgetDef
	Limit    int
	CanSave  bool
	Editing  bool
	ErrorMsg string
}

func (h *Handler) loadWidgets(ctx context.Context, p domain.Principal, ws []LayoutWidget) []widgetView {
	var out []widgetView
	for i, lw := range ws {
		v := widgetView{LayoutWidget: lw, Index: i}
		d, _ := h.Svc.d.Catalog.Def(lw.WidgetID)
		v.Title, v.Target = d.Title, d.Page
		wd, err := h.Svc.LoadWidget(ctx, p, lw.WidgetID, lw.Filters)
		switch {
		case err == nil:
			v.Title, v.Target, v.Rows = wd.Title, wd.Target.URL(), wd.Rows
		case IsUnavailable(err):
			v.Missing = "Not available in this core yet."
		case isCode(err, domain.CodeForbidden):
			continue
		default:
			v.Missing = "Couldn't load this widget."
		}
		out = append(out, v)
	}
	return out
}

func (h *Handler) homePage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	ctx := r.Context()
	ws, err := h.Svc.Working(ctx, p)
	if err != nil {
		return "", "", nil, err
	}
	ls, err := h.Svc.Layouts(ctx, p)
	if err != nil {
		return "", "", nil, err
	}
	opts, err := h.Svc.ResetOptions(ctx, p)
	if err != nil {
		return "", "", nil, err
	}
	picker, _ := h.Svc.Widgets(ctx, p)
	limit := h.Svc.LayoutLimit(ctx)
	v := homeView{Widgets: h.loadWidgets(ctx, p, ws), Layouts: ls, Options: opts, Picker: picker, Limit: limit,
		CanSave: len(ls) < limit, Editing: r.URL.Query().Get("edit") == "1", ErrorMsg: r.URL.Query().Get("error")}
	return "home.html", "Home", v, nil
}

var jsonUnmarshal = json.Unmarshal

func formWidgets(r *http.Request) ([]LayoutWidget, error) {
	raw := r.FormValue("widgets")
	if raw == "" {
		return nil, nil
	}
	var ws []LayoutWidget
	if err := jsonUnmarshal([]byte(raw), &ws); err != nil {
		return nil, domain.Invalid("widgets", "widgets must be a JSON list")
	}
	return ws, nil
}

func (h *Handler) saveLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	ws, err := formWidgets(r)
	if err != nil {
		return "/", "", err
	}
	if ws == nil {
		if ws, err = h.Svc.Working(r.Context(), p); err != nil {
			return "/", "", err
		}
	}
	l, err := h.Svc.SaveLayout(r.Context(), p, r.FormValue("name"), ws)
	if err != nil {
		return "/", "", err
	}
	if r.FormValue("default") == "on" {
		if _, err := h.Svc.SetDefaultLayout(r.Context(), p, l.ID); err != nil {
			return "/", "", err
		}
	}
	return "/", "Saved layout " + l.Name + ".", nil
}

func (h *Handler) applyLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	l, err := h.Svc.ApplyLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")))
	if err != nil {
		return "/", "", err
	}
	return "/", "Restored " + l.Name + ".", nil
}

func (h *Handler) defaultLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	l, err := h.Svc.SetDefaultLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")))
	if err != nil {
		return "/", "", err
	}
	return "/", l.Name + " is your default layout.", nil
}

func (h *Handler) renameLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	name := r.FormValue("name")
	l, err := h.Svc.UpdateLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")), &name, nil)
	if err != nil {
		return "/", "", err
	}
	return "/", "Renamed to " + l.Name + ".", nil
}

func (h *Handler) overwriteLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	ws, err := h.Svc.Working(r.Context(), p)
	if err != nil {
		return "/", "", err
	}
	l, err := h.Svc.UpdateLayout(r.Context(), p, domain.ID(r.PathValue("layout_id")), nil, &ws)
	if err != nil {
		return "/", "", err
	}
	return "/", "Overwrote " + l.Name + " with the current layout.", nil
}

func (h *Handler) deleteLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	if err := h.Svc.DeleteLayout(r.Context(), p, domain.ID(r.PathValue("layout_id"))); err != nil {
		return "/", "", err
	}
	return "/", "Deleted the saved layout.", nil
}

// resetPost applies the chosen reset option; with no saved layouts it goes straight to
// the role default.
func (h *Handler) resetPost(r *http.Request, p domain.Principal) (string, string, error) {
	id := domain.ID(r.FormValue("layout_id"))
	if id.IsZero() {
		opts, err := h.Svc.ResetOptions(r.Context(), p)
		if err != nil {
			return "/", "", err
		}
		id = opts[0].ID
	}
	l, err := h.Svc.ApplyLayout(r.Context(), p, id)
	if err != nil {
		return "/", "", err
	}
	return "/", "Restored " + l.Name + ".", nil
}

func (h *Handler) addWidgetPost(r *http.Request, p domain.Principal) (string, string, error) {
	ws, err := h.Svc.Working(r.Context(), p)
	if err != nil {
		return "/?edit=1", "", err
	}
	ws = h.appendWidget(ws, r.FormValue("widget_id"))
	if _, err := h.Svc.PutWorking(r.Context(), p, ws); err != nil {
		return "/?edit=1", "", err
	}
	return "/?edit=1", "Added the widget.", nil
}

func (h *Handler) appendWidget(ws []LayoutWidget, id string) []LayoutWidget {
	d, ok := h.Svc.d.Catalog.Def(id)
	if !ok {
		d = WidgetDef{W: fixed.DefaultWidgetW, H: fixed.DefaultWidgetH}
	}
	var bottom int
	for _, w := range ws {
		bottom = max(bottom, w.Y+w.H)
	}
	return append(ws, LayoutWidget{WidgetID: id, Y: bottom, W: d.W, H: d.H})
}

// listIndex parses a widget's position in a layout.
func listIndex(s string, n int) (int, error) {
	i, err := strconv.ParseUint(s, 10, 32)
	if err != nil || int(i) >= n {
		return n, domain.Invalid("index", "no such widget")
	}
	return int(i), nil
}

func (h *Handler) removeWidgetPost(r *http.Request, p domain.Principal) (string, string, error) {
	ws, err := h.Svc.Working(r.Context(), p)
	if err != nil {
		return "/?edit=1", "", err
	}
	i, err := listIndex(r.PathValue("index"), len(ws))
	if err != nil {
		return "/?edit=1", "", err
	}
	ws = append(ws[:i:i], ws[i+1:]...)
	if _, err := h.Svc.PutWorking(r.Context(), p, ws); err != nil {
		return "/?edit=1", "", err
	}
	return "/?edit=1", "Removed the widget.", nil
}

// --- Search, notifications, timers ---

func (h *Handler) searchPartial(r *http.Request, p domain.Principal) (string, any, error) {
	v, err := h.Svc.Search(r.Context(), p, r.URL.Query().Get("q"), r.URL.Query().Get("scope"))
	if err != nil {
		return "", nil, err
	}
	return "search_results", v, nil
}

func (h *Handler) searchPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "all"
	}
	v := SearchView{Scope: scope}
	if q := r.URL.Query().Get("q"); q != "" {
		var err error
		if v, err = h.Svc.Search(r.Context(), p, q, scope); err != nil {
			return "", "", nil, err
		}
	}
	return "search.html", "Search", v, nil
}

func (h *Handler) notificationsPartial(r *http.Request, p domain.Principal) (string, any, error) {
	rows, err := h.Svc.RecentNotifications(r.Context(), p)
	return "notification_list", rows, err
}

func (h *Handler) notificationsPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	rows, err := h.Svc.RecentNotifications(r.Context(), p)
	return "notifications.html", "Notifications", rows, err
}

func (h *Handler) openNotificationPost(r *http.Request, p domain.Principal) (string, string, error) {
	link, err := h.Svc.OpenNotification(r.Context(), p, domain.ID(r.PathValue("notification_id")))
	if err != nil {
		return "/notifications", "", err
	}
	return link, "", nil
}

func (h *Handler) readAllPost(r *http.Request, p domain.Principal) (string, string, error) {
	n, err := h.Svc.MarkAllRead(r.Context(), p)
	back := pathOnly(r.FormValue("back"))
	if err != nil {
		return back, "", err
	}
	return back, fmt.Sprintf("Marked %d read.", n), nil
}

func (h *Handler) timersPartial(r *http.Request, p domain.Principal) (string, any, error) {
	rows, err := h.Svc.MyTimers(r.Context(), p)
	return "timer_list", rows, err
}

func (h *Handler) timersPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	rows, err := h.Svc.MyTimers(r.Context(), p)
	return "timers.html", "Timers", rows, err
}

// --- My settings and profile ---

type meView struct {
	Settings MeSettings
	Prefs    []NotificationPref
	Labels   map[string]string
	FirmIdle int
	Error    string
}

func (h *Handler) meSettingsPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	m, err := h.Svc.MySettings(r.Context(), p)
	if err != nil {
		return "", "", nil, err
	}
	prefs, err := h.Svc.NotificationPrefs(r.Context(), p)
	if err != nil {
		return "", "", nil, err
	}
	labels := map[string]string{}
	for _, k := range NotificationKinds {
		labels[k.Kind] = k.Label
	}
	return "me_settings.html", "My settings", meView{Settings: m, Prefs: prefs, Labels: labels, FirmIdle: h.Svc.FirmIdleSeconds(r.Context()) / 60, Error: r.URL.Query().Get("error")}, nil
}

func dayList(s string) (*[]int, error) {
	out := []int{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, domain.Invalid("reminders", "reminder lead times are whole days separated by commas")
		}
		out = append(out, n)
	}
	return &out, nil
}

func (h *Handler) meSettingsPost(r *http.Request, p domain.Principal) (string, string, error) {
	in := MeSettingsPatch{}
	var err error
	if in.EventReminderDays, err = dayList(r.FormValue("event_reminders")); err != nil {
		return "/me/settings", "", err
	}
	if in.DeadlineReminderDays, err = dayList(r.FormValue("deadline_reminders")); err != nil {
		return "/me/settings", "", err
	}
	tz, mode, size := r.FormValue("time_zone"), r.FormValue("mode"), r.FormValue("text_size")
	in.TimeZone, in.Mode, in.TextSize = &tz, &mode, &size
	if mins := r.FormValue("idle_minutes"); mins != "" {
		n, err := strconv.Atoi(mins)
		if err != nil {
			return "/me/settings", "", domain.Invalid("idle_minutes", "idle time is whole minutes")
		}
		secs := n * 60
		in.IdleTimeoutSeconds = &secs
	}
	if _, err := h.Svc.UpdateMySettings(r.Context(), p, in); err != nil {
		return "/me/settings", "", err
	}
	return "/me/settings", "Saved your settings.", nil
}

func (h *Handler) notifyPrefsPost(r *http.Request, p domain.Principal) (string, string, error) {
	var rows []NotificationPref
	for _, k := range NotificationKinds {
		rows = append(rows, NotificationPref{Kind: k.Kind, InApp: r.FormValue(k.Kind+".in_app") == "on", Email: r.FormValue(k.Kind+".email") == "on"})
	}
	current, err := h.Svc.NotificationPrefs(r.Context(), p)
	if err != nil {
		return "/me/settings", "", err
	}
	for i := range rows {
		for _, c := range current {
			if c.Kind == rows[i].Kind && c.EmailRequired {
				rows[i].Email = true // a disabled checkbox posts nothing
			}
		}
	}
	if _, err := h.Svc.PutNotificationPrefs(r.Context(), p, rows); err != nil {
		return "/me/settings", "", err
	}
	return "/me/settings", "Saved your notification preferences.", nil
}

func (h *Handler) sidebarPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := pathOnly(r.FormValue("back"))
	if id := r.FormValue("subgroup"); id != "" {
		_, err := h.Svc.ToggleSubgroup(r.Context(), p, id, r.FormValue("collapsed") == "true")
		return back, "", err
	}
	st, err := h.Svc.Sidebar(r.Context(), p)
	if err != nil {
		return back, "", err
	}
	st.Collapsed = r.FormValue("collapsed") == "true"
	_, err = h.Svc.PutSidebar(r.Context(), p, st)
	return back, "", err
}

func (h *Handler) problemPost(r *http.Request, p domain.Principal) (string, string, error) {
	page := pathOnly(r.FormValue("page"))
	_, mailErr, err := h.Svc.ReportProblem(r.Context(), p, page, r.FormValue("message"))
	if err != nil {
		return page, "", err
	}
	if mailErr != nil {
		return page, "Thanks. Your report was saved; the email to the admins could not be sent yet.", nil
	}
	return page, "Thanks. Your report was saved and sent to the firm's admins.", nil
}

func (h *Handler) quickCreatePage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	return "quick_create.html", "Quick create", nil, nil
}

// --- Help & Training ---

type helpSectionView struct {
	help.Section
	Items []helpItemView
}

// helpItemView is an item with its media resolved for the viewer: Media is the files
// service's versioned download route, set only when the viewer may read the file.
type helpItemView struct {
	help.Item
	Media   string
	MediaOK bool
}

type helpView struct {
	Version   string
	Tutorials []help.Topic
	Query     string
	Hits      []help.TopicHit
	Sections  []helpSectionView
	Modules   []help.Chapter
	Chapters  []help.Chapter
}

func (h *Handler) helpSections(r *http.Request, p domain.Principal) ([]helpSectionView, error) {
	secs, err := h.Help.Sections(r.Context(), p)
	if err != nil {
		return nil, err
	}
	items, err := h.Help.Items(r.Context(), p, "")
	if err != nil {
		return nil, err
	}
	var out []helpSectionView
	for _, s := range secs {
		v := helpSectionView{Section: s}
		for _, it := range items {
			if it.SectionID == s.ID {
				iv := helpItemView{Item: it}
				if iv.Media, iv.MediaOK, err = h.Help.MediaURL(r.Context(), p, it); err != nil {
					return nil, err
				}
				v.Items = append(v.Items, iv)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (h *Handler) helpPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	if err := h.Help.Allow(p); err != nil {
		return "", "", nil, err
	}
	v := helpView{Version: h.Help.Version(), Tutorials: h.Help.Guide().Tutorials(), Query: r.URL.Query().Get("q"), Chapters: h.Help.Guide().Chapters}
	var err error
	if v.Query != "" {
		if v.Hits, err = h.Help.SearchTopics(r.Context(), p, v.Query, ""); err != nil {
			return "", "", nil, err
		}
	}
	if v.Sections, err = h.helpSections(r, p); err != nil {
		return "", "", nil, err
	}
	if v.Modules, err = h.Help.ModuleTopics(r.Context()); err != nil {
		return "", "", nil, err
	}
	return "help.html", "Help & Training", v, nil
}

func (h *Handler) topicPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	t, err := h.Help.Topic(r.Context(), p, r.PathValue("topic_id"))
	if err != nil {
		return "", "", nil, err
	}
	open := ""
	for _, path := range t.Paths {
		if !strings.Contains(path, "{") && !strings.HasPrefix(path, "/api/") {
			open = path
			break
		}
	}
	if t.Tab != "" {
		open = "/admin?tab=" + t.Tab
	}
	return "help_topic.html", t.Title, map[string]any{"Topic": t, "Open": open}, nil
}

// --- Admin ---

// adminTabs is the PRD 8.7 tab order. Settings tabs match the registry's tab ids.
var adminTabs = []struct{ ID, Label string }{
	{settings.TabFirm, "Firm profile"}, {tabUsers, "Users and roles"}, {settings.TabSecurity, "Security"},
	{settings.TabPractice, "Practice"}, {settings.TabBilling, "Time and billing"}, {settings.TabTrust, "Trust"},
	{settings.TabCalendar, "Calendar"}, {settings.TabFiles, "Files and storage"}, {settings.TabEditor, "Document editor"},
	{settings.TabDocTender, "Doc Tender"}, {settings.TabHome, "Home screen"}, {settings.TabSearch, "Lists and search"},
	{settings.TabMail, "Notifications and mail"}, {settings.TabAppearance, "Appearance"}, {settings.TabAI, "AI and inference"},
	{"help", "Help"}, {settings.TabModules, "Modules and marketplace"}, {settings.TabUpdates, "Updates"},
	{settings.TabBackups, "Backups and logs"}, {settings.TabAPI, "Agents and API"}, {"system", "System"},
}

// AdminTabIDs lists every admin tab id, for the guide coverage check.
func AdminTabIDs() []string {
	out := make([]string, len(adminTabs))
	for i, t := range adminTabs {
		out[i] = t.ID
	}
	return out
}

func tabLabel(id string) string {
	for _, t := range adminTabs {
		if t.ID == id {
			return t.Label
		}
	}
	return id
}

type settingRow struct {
	settings.Entry
	Changed bool
}

type roleLayoutView struct {
	Role    string
	Widgets []LayoutWidget
	Titles  map[string]string
	Picker  []WidgetDef
}

type adminView struct {
	Tabs       []struct{ ID, Label string }
	Tab        string
	Settings   []settingRow
	Error      string
	Appearance Appearance
	Presets    []Preset
	Fonts      []string
	Roles      []roleLayoutView
	Help       []helpSectionView
	Users      []UserRow
	Roles5     []domain.Role
	Tokens     []TokenRow
	Scopes     []ScopeInfo
	Issued     *IssuedView
	Owners     []domain.User
	Seats      Seats
	Problems   []ProblemReport
	Audit      []AuditRow
	HelpPath   string
	Modules    any
}

func (h *Handler) adminPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	if err := admin(p); err != nil {
		return "", "", nil, err
	}
	ctx := r.Context()
	tab := r.URL.Query().Get("tab")
	known := false
	for _, t := range adminTabs {
		if t.ID == tab {
			known = true
		}
	}
	if !known {
		tab = adminTabs[0].ID
	}
	v := adminView{Tabs: adminTabs, Tab: tab, Error: r.URL.Query().Get("error")}
	entries, err := h.Svc.d.Settings.List(ctx)
	if err != nil {
		return "", "", nil, err
	}
	for _, e := range entries {
		if e.Tab == tab {
			v.Settings = append(v.Settings, settingRow{Entry: e, Changed: e.Overridden && e.Value != e.Default})
		}
	}
	switch tab {
	case settings.TabAppearance:
		v.Appearance = h.Svc.appearance(ctx)
		v.Presets = Presets
		for _, f := range Fonts {
			v.Fonts = append(v.Fonts, f.Name)
		}
	case settings.TabHome:
		titles := map[string]string{}
		for _, d := range h.Svc.d.Catalog.For(domain.Principal{Roles: allStaff}) {
			titles[d.ID] = d.Title
		}
		for _, role := range allStaff {
			rl, err := h.Svc.RoleDefault(ctx, role)
			if err != nil {
				return "", "", nil, err
			}
			v.Roles = append(v.Roles, roleLayoutView{Role: string(role), Widgets: rl.Widgets, Titles: titles,
				Picker: h.Svc.d.Catalog.For(domain.Principal{Roles: []domain.Role{role}})})
		}
	case "help":
		if v.Help, err = h.helpSections(r, p); err != nil {
			return "", "", nil, err
		}
	case settings.TabModules:
		if h.Modules != nil {
			if v.Modules, err = h.Modules.Panel(r, p); err != nil {
				return "", "", nil, err
			}
		}
	case settings.TabAPI:
		if h.Accounts != nil {
			if v.Tokens, err = h.Accounts.AllTokens(ctx, p); err != nil {
				return "", "", nil, err
			}
			rows, err := h.Accounts.UsersFor(ctx, p)
			if err != nil {
				return "", "", nil, err
			}
			for _, row := range rows {
				if row.User.Active {
					v.Owners = append(v.Owners, row.User)
				}
			}
			v.Scopes = TokenScopes
		}
	case tabUsers, "system":
		if tab == tabUsers && h.Accounts != nil {
			if v.Users, err = h.Accounts.UsersFor(ctx, p); err != nil {
				return "", "", nil, err
			}
			v.Roles5 = AllRoles
		}
		if v.Seats, err = h.Svc.SeatCount(ctx, p); err != nil {
			return "", "", nil, err
		}
		if tab == "system" {
			if v.Problems, err = h.Svc.Problems(ctx, p); err != nil {
				return "", "", nil, err
			}
			if h.Audit != nil {
				if v.Audit, err = h.Audit(r, p, fixed.AuditRows); err != nil {
					return "", "", nil, err
				}
			}
		}
	}
	return "admin.html", "Administrative · " + tabLabel(tab), v, nil
}

func adminBack(r *http.Request) string {
	tab := r.FormValue("tab")
	for _, t := range adminTabs {
		if t.ID == tab {
			return "/admin?tab=" + url.QueryEscape(tab)
		}
	}
	return "/admin"
}

func settingErr(err error) error {
	var api *domain.APIError
	if errors.As(err, &api) {
		return err
	}
	switch {
	case errors.Is(err, settings.ErrForbidden):
		return domain.Forbidden(domain.ReasonRole)
	case errors.Is(err, settings.ErrUnknown):
		return domain.NotFound("setting")
	case errors.Is(err, settings.ErrReadOnly):
		return domain.Invalid("value", "this setting is read only")
	case errors.Is(err, settings.ErrInvalid):
		return domain.Invalid("value", strings.TrimPrefix(err.Error(), "settings: "))
	}
	return err
}

func (h *Handler) settingPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := adminBack(r)
	if err := admin(p); err != nil {
		return back, "", err
	}
	key := r.PathValue("setting_key")
	value := r.FormValue("value")
	if r.FormValue("kind") == settings.KindBool && value == "" {
		value = "false"
	}
	ch, err := h.Svc.d.Settings.Set(r.Context(), key, value, p)
	if err != nil {
		return back, "", settingErr(err)
	}
	return back, fmt.Sprintf("Saved %s: %s → %s.", key, ch.Old, ch.New), nil
}

func (h *Handler) settingResetPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := adminBack(r)
	if err := admin(p); err != nil {
		return back, "", err
	}
	key := r.PathValue("setting_key")
	if _, err := h.Svc.d.Settings.Reset(r.Context(), key, p); err != nil {
		return back, "", settingErr(err)
	}
	return back, "Reset " + key + " to its default.", nil
}

func (h *Handler) appearancePost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + settings.TabAppearance
	a := Appearance{Preset: r.FormValue("preset"), StaffSidebar: r.FormValue("staff_sidebar"), StaffHeader: r.FormValue("staff_header"),
		StaffPage: r.FormValue("staff_page"), StaffAccent: r.FormValue("staff_accent"), ClientHeader: r.FormValue("client_header"),
		HeadingFont: r.FormValue("heading_font"), BodyFont: r.FormValue("body_font"), TextSize: r.FormValue("text_size"), Mode: r.FormValue("mode")}
	if _, err := h.Svc.SetAppearance(r.Context(), p, a); err != nil {
		return back, "", err
	}
	return back, "Saved the appearance. It shows on the next page load.", nil
}

func (h *Handler) roleLayoutPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + settings.TabHome
	ws, err := formWidgets(r)
	if err != nil {
		return back, "", err
	}
	if ws == nil {
		ws = []LayoutWidget{}
	}
	if _, err := h.Svc.SetRoleLayout(r.Context(), p, r.PathValue("role"), ws); err != nil {
		return back, "", err
	}
	return back, "Saved the " + r.PathValue("role") + " default layout.", nil
}

func (h *Handler) roleAddWidgetPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + settings.TabHome
	rl, err := h.Svc.GetRoleLayout(r.Context(), p, r.PathValue("role"))
	if err != nil {
		return back, "", err
	}
	ws := h.appendWidget(rl.Widgets, r.FormValue("widget_id"))
	if _, err := h.Svc.SetRoleLayout(r.Context(), p, r.PathValue("role"), ws); err != nil {
		return back, "", err
	}
	return back, "Added the widget to the " + r.PathValue("role") + " default.", nil
}

func (h *Handler) roleRemoveWidgetPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + settings.TabHome
	rl, err := h.Svc.GetRoleLayout(r.Context(), p, r.PathValue("role"))
	if err != nil {
		return back, "", err
	}
	i, err := listIndex(r.PathValue("index"), len(rl.Widgets))
	if err != nil {
		return back, "", err
	}
	ws := append(rl.Widgets[:i:i], rl.Widgets[i+1:]...)
	if _, err := h.Svc.SetRoleLayout(r.Context(), p, r.PathValue("role"), ws); err != nil {
		return back, "", err
	}
	return back, "Removed the widget from the " + r.PathValue("role") + " default.", nil
}

func (h *Handler) helpSectionPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=help"
	title := r.FormValue("title")
	in := help.SectionInput{Title: &title}
	if s := r.FormValue("sort_order"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return back, "", domain.Invalid("sort_order", "sort order is a whole number")
		}
		in.SortOrder = &n
	}
	sec, err := h.Help.CreateSection(r.Context(), p, in)
	if err != nil {
		return back, "", err
	}
	return back, "Added the section " + sec.Title + ".", nil
}

func (h *Handler) helpSectionDeletePost(r *http.Request, p domain.Principal) (string, string, error) {
	if err := h.Help.DeleteSection(r.Context(), p, domain.ID(r.PathValue("section_id"))); err != nil {
		return "/admin?tab=help", "", err
	}
	return "/admin?tab=help", "Removed the section.", nil
}

func (h *Handler) helpItemPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=help"
	sec := domain.ID(r.FormValue("section_id"))
	title, kind, body := r.FormValue("title"), r.FormValue("kind"), r.FormValue("body")
	in := help.ItemInput{SectionID: &sec, Title: &title, Kind: &kind, Body: &body}
	if f := strings.TrimSpace(r.FormValue("file_id")); f != "" {
		id := domain.ID(f)
		ptr := &id
		in.FileID = &ptr
	}
	if s := r.FormValue("sort_order"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return back, "", domain.Invalid("sort_order", "sort order is a whole number")
		}
		in.SortOrder = &n
	}
	it, err := h.Help.CreateItem(r.Context(), p, in)
	if err != nil {
		return back, "", err
	}
	return back, "Added the item " + it.Title + ".", nil
}

func (h *Handler) helpItemDeletePost(r *http.Request, p domain.Principal) (string, string, error) {
	if err := h.Help.DeleteItem(r.Context(), p, domain.ID(r.PathValue("item_id"))); err != nil {
		return "/admin?tab=help", "", err
	}
	return "/admin?tab=help", "Removed the item.", nil
}

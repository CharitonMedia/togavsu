package modules

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ClientOnly reports a principal that holds only the client role (CORE-10).
func ClientOnly(p domain.Principal) bool {
	return p.HasRole(domain.RoleClient) &&
		!p.HasRole(domain.RoleAdmin) &&
		!p.HasRole(domain.RoleAttorney) &&
		!p.HasRole(domain.RoleStaff) &&
		!p.HasRole(domain.RoleAccountant)
}

// assertClientHomeFree refuses a second module that declares client_home (CORE-10).
func (s *Service) assertClientHomeFree(ctx context.Context, self, home string) error {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id FROM core.modules WHERE id <> $1 AND status IN ('installed','awaiting_approval')`, self)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		in, err := s.Installed(ctx, id)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		if strings.TrimSpace(in.Manifest.ClientHome) != "" {
			return domain.Conflict("module " + id + " already declares client_home; only one module may")
		}
	}
	return rows.Err()
}

const clientFrameSandbox = staffFrameSandbox + " allow-top-navigation-by-user-activation"

// ClientNavEntry is one portal nav tab from getClientNavContext (CORE-10).
type ClientNavEntry struct {
	ModuleID string `json:"module_id"`
	TabID    string `json:"tab_id"`
	Title    string `json:"title"`
}

// ClientHome finds the enabled module that declares client_home and that tab.
func (s *Service) ClientHome(ctx context.Context) (moduleID string, tab Tab, ok bool, err error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return "", Tab{}, false, err
	}
	for _, in := range mods {
		home := strings.TrimSpace(in.Manifest.ClientHome)
		if home == "" {
			continue
		}
		t, found := in.Manifest.Tab(home)
		if !found || t.Placement != "client" {
			continue
		}
		return in.ID, t, true, nil
	}
	return "", Tab{}, false, nil
}

// ClientNav lists every enabled module's client tabs with nav true (CORE-10).
func (s *Service) ClientNav(ctx context.Context) ([]ClientNavEntry, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ClientNavEntry, 0)
	for _, in := range mods {
		for _, t := range in.Manifest.Tabs {
			if t.Placement != "client" || !t.TabNav() {
				continue
			}
			out = append(out, ClientNavEntry{ModuleID: in.ID, TabID: t.ID, Title: t.Title})
		}
	}
	return out, nil
}

func tabQuery(tab Tab, q url.Values) url.Values {
	out := url.Values{}
	for _, k := range tab.Params {
		if v := q.Get(k); v != "" {
			out.Set(k, v)
		}
	}
	return out
}

func writeClientFrame(w http.ResponseWriter, r *http.Request, title, src, moduleID string) {
	writeFrameSandbox(w, r, false, title, src, clientFrameSandbox, moduleID, false)
}

func (h *Handler) portalHome(w http.ResponseWriter, r *http.Request, c Caller) {
	if !ClientOnly(c.Principal) {
		writeErr(w, domain.NotFound("portal"))
		return
	}
	moduleID, tab, ok, err := h.Svc.ClientHome(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if ok {
		http.Redirect(w, r, "/portal/"+moduleID+"/"+tab.ID, http.StatusSeeOther)
		return
	}
	name := "the firm"
	if h.FirmName != nil {
		if n := strings.TrimSpace(h.FirmName(r.Context())); n != "" {
			name = n
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(name)
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Client portal</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem}h1{font-size:1.4rem}</style>
</head><body><h1>` + esc + `</h1><p>The client portal isn't set up yet.</p></body></html>`))
}

func (h *Handler) portalTab(w http.ResponseWriter, r *http.Request, c Caller) {
	if !ClientOnly(c.Principal) {
		writeErr(w, domain.NotFound("portal"))
		return
	}
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	tab, ok := in.Manifest.Tab(r.PathValue("tab_id"))
	if !ok || tab.Placement != "client" {
		writeErr(w, domain.NotFound("tab"))
		return
	}
	q := tabQuery(tab, r.URL.Query())
	var matter domain.ID
	if mid := q.Get("matter_id"); mid != "" {
		matter = domain.ID(mid)
	}
	tok, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeClientFrame(w, r, tab.Title, FramePath(in.ID, tab.Path, tok.Token, q), in.ID)
}

func (h *Handler) portalContextToken(w http.ResponseWriter, r *http.Request, c Caller) {
	if !ClientOnly(c.Principal) {
		writeErr(w, domain.NotFound("portal"))
		return
	}
	in, err := h.Svc.Installed(r.Context(), r.PathValue("module_id"))
	if err != nil || !in.Enabled {
		writeErr(w, domain.NotFound("module"))
		return
	}
	tab, ok := in.Manifest.Tab(r.PathValue("tab_id"))
	if !ok || tab.Placement != "client" {
		writeErr(w, domain.NotFound("tab"))
		return
	}
	q := tabQuery(tab, r.URL.Query())
	var matter domain.ID
	if mid := q.Get("matter_id"); mid != "" {
		matter = domain.ID(mid)
	}
	tok, err := h.Svc.IssueContext(r.Context(), c.Principal, in.ID, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok.Token, "expires_at": tok.ExpiresAt.UTC().Format(time.RFC3339)})
}

func (h *Handler) clientContextJSON(w http.ResponseWriter, r *http.Request, c Caller) {
	if !ClientOnly(c.Principal) {
		writeErr(w, domain.Forbidden(domain.ReasonRole))
		return
	}
	if c.Bearer && !hasScope(c.Scopes, "profile:read") {
		writeErr(w, domain.Forbidden("this token lacks the profile:read scope"))
		return
	}
	entries, err := h.Svc.ClientNav(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

package modules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// NavigateURL checks a frame navigation against current installation and link
// state. Cross-module navigation is restricted to declared staff tabs.
func (s *Service) NavigateURL(ctx context.Context, p domain.Principal, source, page string, params map[string]string) (string, bool) {
	if p.UserID.IsZero() {
		return "", false
	}
	if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) {
		return "", false
	}
	from, err := s.Installed(ctx, source)
	if err != nil || !from.Enabled {
		return "", false
	}
	if !safeNavigationPage(page) {
		return "", false
	}
	u, err := url.ParseRequestURI(page)
	if err != nil {
		return "", false
	}
	modulePath, isModule := strings.CutPrefix(u.Path, "/modules/")
	targetID, targetPath, _ := strings.Cut(modulePath, "/")
	if !isModule || targetID == source {
		return BridgeNavigateURL(source, page, params)
	}
	tabID, isTab := strings.CutPrefix(targetPath, "tabs/")
	if !isTab || strings.Contains(tabID, "/") {
		return "", false
	}
	target, err := s.Installed(ctx, targetID)
	if err != nil || !target.Enabled {
		return "", false
	}
	tab, ok := target.Manifest.Tab(tabID)
	if !ok || (tab.Placement != "home" && tab.Placement != "matter") {
		return "", false
	}
	var links int
	err = s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM core.module_contract_links
 WHERE (consumer_id = $1 AND provider_id = $2) OR (consumer_id = $2 AND provider_id = $1)`, source, target.ID).Scan(&links)
	if err != nil || links == zero {
		return "", false
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	allowed := url.Values{}
	for _, key := range tab.Params {
		if values, ok := q[key]; ok {
			allowed[key] = values
		}
	}
	if tab.Placement == "matter" {
		matter, err := domain.ParseID(q.Get("matter_id"))
		if err != nil {
			return "", false
		}
		decision, err := s.canReadMatter(ctx, p, matter)
		if err != nil || !decision.Allow {
			return "", false
		}
		var found string
		if err := s.q(ctx).QueryRowContext(ctx, `SELECT id FROM core.matters WHERE id = $1`, string(matter)).Scan(&found); err != nil {
			return "", false
		}
		allowed.Set("matter_id", string(matter))
	}
	u.RawQuery = allowed.Encode()
	return u.String(), true
}

func safeNavigationPage(page string) bool {
	u, err := url.ParseRequestURI(page)
	return err == nil && u.Host == "" && u.Scheme == "" && strings.HasPrefix(u.Path, "/") &&
		!strings.HasPrefix(u.Path, "//") && !strings.ContainsAny(page, "\\#\r\n\t") &&
		u.EscapedPath() == u.Path && (u.Path == "/" || path.Clean(u.Path) == strings.TrimSuffix(u.Path, "/"))
}

func (h *Handler) navigate(w http.ResponseWriter, r *http.Request, c Caller) {
	params := map[string]string{}
	if raw := r.URL.Query().Get("params"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			writeErr(w, domain.Invalid("params", "params must be strings"))
			return
		}
	}
	result, ok := h.Svc.NavigateURL(r.Context(), c.Principal, r.PathValue("module_id"), r.URL.Query().Get("page"), params)
	if !ok {
		writeErr(w, domain.Forbidden("navigation is not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": result})
}

package modules

import (
	"context"
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const staffBannerOrderKey = "modules.staff_banner_order"

// StaffBannerView is one banner to render above staff pages (CORE-34).
type StaffBannerView struct {
	ModuleID string `json:"module_id"`
	Path     string `json:"path"`
	Height   int    `json:"height"`
	Src      string `json:"src"`
}

// ListStaffBanners returns enabled banners in admin order (CORE-34).
func (s *Service) ListStaffBanners(ctx context.Context) ([]StaffBannerView, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Installed{}
	var unordered []StaffBannerView
	for _, m := range mods {
		if m.Manifest.StaffBanner == nil || !m.Approved["banner:show"] {
			continue
		}
		h := m.Manifest.StaffBanner.Height
		if h <= 0 {
			h = domain.StaffBannerDefaultHeight
		}
		v := StaffBannerView{
			ModuleID: m.ID,
			Path:     m.Manifest.StaffBanner.Path,
			Height:   h,
			Src:      FramePath(m.ID, m.Manifest.StaffBanner.Path, "", nil),
		}
		byID[m.ID] = m
		unordered = append(unordered, v)
	}
	orderRaw, err := s.d.Settings.Get(ctx, staffBannerOrderKey)
	if err != nil {
		return unordered, nil
	}
	var order []string
	if strings.TrimSpace(orderRaw) != "" {
		_ = json.Unmarshal([]byte(orderRaw), &order)
	}
	if len(order) == 0 {
		return unordered, nil
	}
	seen := map[string]bool{}
	var out []StaffBannerView
	for _, id := range order {
		for _, v := range unordered {
			if v.ModuleID == id && !seen[id] {
				out = append(out, v)
				seen[id] = true
			}
		}
	}
	for _, v := range unordered {
		if !seen[v.ModuleID] {
			out = append(out, v)
		}
	}
	return out, nil
}

// SetStaffBannerOrder stores the admin order of banner module ids.
func (s *Service) SetStaffBannerOrder(ctx context.Context, p domain.Principal, order []string) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	clean := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, id := range order {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return err
	}
	_, err = s.d.Settings.Set(ctx, staffBannerOrderKey, string(raw), p)
	return err
}

// BannerHTML returns the sandboxed iframe markup for staff banners (CORE-34).
func BannerHTML(banners []StaffBannerView) template.HTML {
	if len(banners) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="staff-banners" id="staff-banners" data-banners="` + strconv.Itoa(len(banners)) + `">`)
	for _, bn := range banners {
		h := bn.Height
		if h <= 0 {
			h = domain.StaffBannerDefaultHeight
		}
		b.WriteString(`<div class="staff-banner" data-module="` + html.EscapeString(bn.ModuleID) + `" style="height:` + strconv.Itoa(h) + `px" hidden>`)
		// Same-origin URL (/m/...) under the staff sandbox list; show/hide via postMessage bridge.
		b.WriteString(`<iframe class="staff-banner-frame" title="` + html.EscapeString(bn.ModuleID) + ` banner" src="` + html.EscapeString(bn.Src) +
			`" sandbox="` + staffFrameSandbox + `" referrerpolicy="no-referrer" data-module="` + html.EscapeString(bn.ModuleID) +
			`" style="width:100%;height:` + strconv.Itoa(h) + `px;border:0"></iframe>`)
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<script src="/ui/static/banner-bridge.js" defer></script>`)
	return template.HTML(b.String())
}

// bannerPage serves GET /modules/{id}/banner as a framed staff banner (for tests / direct open).
func (h *Handler) bannerPage(w http.ResponseWriter, r *http.Request, c Caller) {
	id := r.PathValue("module_id")
	in, err := h.Svc.Installed(r.Context(), id)
	if err != nil || in.Manifest.StaffBanner == nil {
		writeErr(w, domain.NotFound("banner"))
		return
	}
	if !in.Approved["banner:show"] {
		writeErr(w, &domain.APIError{Code: "scope_denied", Message: "module lacks the banner:show scope"})
		return
	}
	tok, err := h.Svc.IssueContext(r.Context(), c.Principal, id, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeFrame(w, r, false, in.Manifest.Name+" banner", FramePath(in.ID, in.Manifest.StaffBanner.Path, tok.Token, nil), in.ID)
}

// listBannersJSON is GET /api/v1/modules/banners for the shell.
func (h *Handler) listBannersJSON(w http.ResponseWriter, r *http.Request, c Caller) {
	_ = c
	list, err := h.Svc.ListStaffBanners(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "page": map[string]any{"limit": len(list) + 1, "next_cursor": nil}})
}

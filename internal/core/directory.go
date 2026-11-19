package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// directoryRoutes mounts CORE-18 firm profile and CORE-19 user directory.
func (a *App) directoryRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/firm-profile", a.firmProfileJSON)
	m.HandleFunc("GET /api/v1/user-directory", a.userDirectoryJSON)
}

var directoryHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// firmProfileJSON is getFirmProfile: firm.* settings plus client portal appearance.
func (a *App) firmProfileJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := directoryRole(p, true); err != nil {
		writeErr(w, err)
		return
	}
	ctx := r.Context()
	get := func(key string) string {
		v, err := a.Settings.Get(ctx, key)
		if err != nil {
			return ""
		}
		return v
	}
	logo := get("firm.logo_file_id")
	var logoAny any
	if strings.TrimSpace(logo) != "" {
		logoAny = logo
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": get("firm.name"), "short_name": get("firm.short_name"), "address": get("firm.address"),
		"phone": get("firm.phone"), "email": get("firm.email"), "website": get("firm.website"),
		"logo_file_id": logoAny, "time_zone": get("firm.time_zone"),
		"client_portal": map[string]any{
			"client_header": clientPortalHeader(get),
			"label":         get("appearance.client_portal_label"),
		},
	})
}

// clientPortalHeader is Appearance's client_header: preset header, or a custom color.
func clientPortalHeader(get func(string) string) string {
	pr := dashboard.Presets[0]
	if id := get("appearance.preset"); id != "" {
		for _, cand := range dashboard.Presets {
			if cand.ID == id || cand.Label == id {
				pr = cand
				break
			}
		}
	}
	header := pr.Header
	var c struct {
		ClientHeader string `json:"client_header"`
	}
	_ = json.Unmarshal([]byte(get("appearance.colors")), &c)
	if directoryHexColor.MatchString(c.ClientHeader) {
		header = c.ClientHeader
	}
	return header
}

// userDirectoryJSON is listUserDirectory: id, name, roles, active; clients see less.
func (a *App) userDirectoryJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := directoryRole(p, false); err != nil {
		writeErr(w, err)
		return
	}
	ids, err := directoryIDs(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	role, err := directoryRoleFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	active, err := directoryActive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	users, err := ListUsers(r.Context(), a)
	if err != nil {
		writeErr(w, err)
		return
	}
	matter := domain.ID(r.URL.Query().Get("matter_id"))
	if !matter.IsZero() {
		if err := a.matterAccess(r.Context(), p, matter, domain.ActionRead); err != nil {
			writeErr(w, err)
			return
		}
	}
	clientCaller := p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff)
	rows := make([]map[string]any, 0, len(users))
	for _, u := range users {
		if u.ID == domain.SystemUserID {
			continue
		}
		roles, err := domain.ParseRoles(u.Roles)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !matter.IsZero() {
			candidate := domain.Principal{UserID: u.ID, Roles: roles, Source: domain.SourceUI}
			dec, err := a.Practice.Policy.Authorize(r.Context(), candidate, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, ID: matter, MatterID: matter, SharedWithClient: true})
			if err != nil {
				writeErr(w, err)
				return
			}
			if !u.Active || !dec.Allow {
				continue
			}
		}
		if len(ids) > 0 && !ids[u.ID] {
			continue
		}
		if role != "" && !hasRole(roles, role) {
			continue
		}
		if active != nil && u.Active != *active {
			continue
		}
		if clientCaller {
			if !u.Active || !firmFacing(roles) {
				continue
			}
			rows = append(rows, map[string]any{"id": u.ID, "name": u.Name})
			continue
		}
		roleNames := make([]string, len(roles))
		for i, r := range roles {
			roleNames[i] = string(r)
		}
		rows = append(rows, map[string]any{"id": u.ID, "name": u.Name, "roles": roleNames, "active": u.Active, "work_email": u.Email})
	}
	from, limit, err := a.directoryPageArgs(r.Context(), r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if from > len(rows) {
		from = len(rows)
	}
	to := from + limit
	if to > len(rows) {
		to = len(rows)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows[from:to], "page": directoryPageInfo(limit, to, to < len(rows))})
}

// directoryRole allows every role for firm profile, or admin/attorney/staff/client for the directory.
func directoryRole(p domain.Principal, firm bool) error {
	if p.UserID.IsZero() && p.ModuleID == "" {
		return domain.Unauthenticated("sign in is required")
	}
	if firm {
		for _, r := range p.Roles {
			switch r {
			case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant, domain.RoleClient:
				return nil
			}
		}
		return domain.Forbidden(domain.ReasonRole)
	}
	for _, r := range p.Roles {
		switch r {
		case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleClient:
			return nil
		}
	}
	return domain.Forbidden(domain.ReasonRole)
}

func firmFacing(roles []domain.Role) bool {
	for _, r := range roles {
		switch r {
		case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff:
			return true
		}
	}
	return false
}

func hasRole(roles []domain.Role, want domain.Role) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func directoryIDs(r *http.Request) (map[domain.ID]bool, error) {
	raw := r.URL.Query()["ids"]
	if len(raw) == 0 {
		if v := r.URL.Query().Get("ids"); v != "" {
			raw = strings.Split(v, ",")
		}
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > domain.DirectoryIDsMax {
		return nil, domain.Invalid("ids", "ids accepts at most 100 values")
	}
	out := make(map[domain.ID]bool, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, domain.Invalid("ids", "ids are user ids")
		}
		out[domain.ID(s)] = true
	}
	return out, nil
}

func directoryRoleFilter(r *http.Request) (domain.Role, error) {
	v := r.URL.Query().Get("role")
	if v == "" {
		return "", nil
	}
	role := domain.Role(v)
	switch role {
	case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant, domain.RoleClient:
		return role, nil
	default:
		return "", domain.Invalid("role", "role is admin, attorney, staff, accountant or client")
	}
}

func directoryActive(r *http.Request) (*bool, error) {
	v := r.URL.Query().Get("active")
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, domain.Invalid("active", "active is true or false")
	}
	return &b, nil
}

func (a *App) directoryPageArgs(ctx context.Context, r *http.Request) (from, limit int, err error) {
	most, err := a.intSetting(ctx, "api.max_page_size")
	if err != nil {
		return 0, 0, err
	}
	limit, err = a.intSetting(ctx, "api.page_size")
	if err != nil {
		return 0, 0, err
	}
	if limit > most {
		limit = most
	}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n <= 0 {
			return 0, 0, domain.Invalid("limit", "limit is a positive integer")
		}
		if n > most {
			return 0, 0, domain.Invalid("limit", "limit must not exceed api.max_page_size ("+strconv.Itoa(most)+")")
		}
		limit = n
	}
	if c := q.Get("cursor"); c != "" {
		raw, decErr := base64.RawURLEncoding.DecodeString(c)
		off, convErr := strconv.Atoi(strings.TrimPrefix(string(raw), "o:"))
		if decErr != nil || convErr != nil || off < 0 || !strings.HasPrefix(string(raw), "o:") {
			return 0, 0, domain.Invalid("cursor", "cursor is not from this list")
		}
		from = off
	}
	return from, limit, nil
}

func directoryPageInfo(limit, to int, more bool) map[string]any {
	var next any
	if more {
		next = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(to)))
	}
	return map[string]any{"limit": limit, "next_cursor": next}
}

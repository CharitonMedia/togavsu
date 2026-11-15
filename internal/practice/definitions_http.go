package practice

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (h *Handler) definitionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/party-roles", h.partyRolesJSON)
	mux.HandleFunc("POST /api/v1/party-roles", h.createPartyRoleJSON)
	mux.HandleFunc("GET /api/v1/party-roles/{party_role_id}", h.partyRoleJSON)
	mux.HandleFunc("PATCH /api/v1/party-roles/{party_role_id}", h.updatePartyRoleJSON)
	mux.HandleFunc("GET /api/v1/marker-definitions", h.markerDefsJSON)
	mux.HandleFunc("POST /api/v1/marker-definitions", h.createMarkerDefJSON)
	mux.HandleFunc("GET /api/v1/marker-definitions/{marker_id}", h.markerDefJSON)
	mux.HandleFunc("PATCH /api/v1/marker-definitions/{marker_id}", h.updateMarkerDefJSON)
	mux.HandleFunc("PATCH /api/v1/matter-markers/{matter_marker_id}", h.updateMatterMarkerJSON)
	mux.HandleFunc("POST /api/v1/modules/{module_id}/markers", h.createModuleMarkerJSON)
	mux.HandleFunc("PATCH /api/v1/modules/{module_id}/markers/{marker_id}", h.updateModuleMarkerJSON)
	mux.HandleFunc("POST /api/v1/modules/{module_id}/markers/{marker_id}/revoke", h.revokeModuleMarkerJSON)
}

func markerDefDoc(row MarkerDef) map[string]any {
	row = row.shown()
	doc := map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"label": row.Label, "color": row.Color, "active": !row.Inactive, "module_id": nil,
	}
	if row.ModuleID != "" {
		doc["module_id"] = row.ModuleID
	}
	return doc
}

func moduleMarkerDoc(row MarkerDef) map[string]any {
	doc := markerDefDoc(row)
	doc["namespace"] = row.ModuleID
	doc["placement"] = "matter-header"
	doc["target"] = nil
	if row.Target != "" {
		doc["target"] = row.Target
	}
	return doc
}

func partyRoleDoc(row PartyRole) map[string]any {
	return map[string]any{
		"id": row.ID, "created_at": when(row.CreatedAt), "created_by": row.CreatedBy, "updated_at": when(row.UpdatedAt),
		"key": row.Key, "label": row.Label, "active": row.Active,
	}
}

// activeFilter reads the optional active=true|false query.
func activeFilter(r *http.Request) (*bool, error) {
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

// page slices items by cursor and limit: the default is api.page_size, the cap
// api.max_page_size, both read on every request.
func (h *Handler) page(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	from, limit, err := h.pageArgs(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if from > len(items) {
		from = len(items)
	}
	to := min(from+limit, len(items))
	writeJSON(w, http.StatusOK, map[string]any{"data": items[from:to], "page": pageInfo(limit, to, to < len(items))})
}

// pageArgs reads cursor and limit: the default is api.page_size, the cap
// api.max_page_size, both read on every request. from is the offset the cursor names.
func (h *Handler) pageArgs(r *http.Request) (from, limit int, err error) {
	ctx := r.Context()
	most, _ := strconv.Atoi(h.Svc.setting(ctx, "api.max_page_size", ""))
	limit, _ = strconv.Atoi(h.Svc.setting(ctx, "api.page_size", ""))
	if limit > most {
		limit = most
	}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= int(0) {
			return 0, 0, domain.Invalid("limit", "limit is a positive integer")
		}
		if n > most {
			return 0, 0, domain.Invalid("limit", "limit must not exceed api.max_page_size ("+strconv.Itoa(most)+")")
		}
		limit = n
	}
	if c := q.Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		off, convErr := strconv.Atoi(strings.TrimPrefix(string(raw), "o:"))
		if err != nil || convErr != nil || off < int(0) || !strings.HasPrefix(string(raw), "o:") {
			return 0, 0, domain.Invalid("cursor", "cursor is not from this list")
		}
		from = off
	}
	return from, limit, nil
}

// pageInfo is the page object: the next cursor continues at offset to when more follow.
func pageInfo(limit, to int, more bool) map[string]any {
	var next any
	if more {
		next = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(to)))
	}
	return map[string]any{"limit": limit, "next_cursor": next}
}

// idemWrite runs one write under its Idempotency-Key: the same caller, operation, target
// and body replay the stored response; a different body with the key is a conflict.
func (h *Handler) idemWrite(w http.ResponseWriter, r *http.Request, p domain.Principal, op, target string, body any, code int, run func() (map[string]any, error)) {
	var replay map[string]any
	hit, hold, err := h.Svc.idemBegin(r.Context(), p, r.Header.Get("Idempotency-Key"), op, target, body, &replay)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer hold.abort()
	if hit {
		writeJSON(w, code, replay)
		return
	}
	doc, err := run()
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := hold.finish(r.Context(), doc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, code, doc)
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return false
	}
	return true
}

type partyRoleBody struct {
	Key    *string `json:"key"`
	Label  *string `json:"label"`
	Active *bool   `json:"active"`
}

func (b partyRoleBody) write() PartyRoleWrite {
	return PartyRoleWrite{Key: b.Key, Label: b.Label, Active: b.Active}
}

func (h *Handler) partyRolesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	active, err := activeFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListPartyRoles(r.Context(), p, active)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, partyRoleDoc(row))
	}
	h.page(w, r, items)
}

func (h *Handler) partyRoleJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetPartyRole(r.Context(), p, domain.ID(r.PathValue("party_role_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, partyRoleDoc(row))
}

func (h *Handler) createPartyRoleJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body partyRoleBody
	if !decodeBody(w, r, &body) {
		return
	}
	h.idemWrite(w, r, p, "create_party_role", "", body, http.StatusCreated, func() (map[string]any, error) {
		row, err := h.Svc.CreatePartyRole(r.Context(), p, body.write())
		return partyRoleDoc(row), err
	})
}

func (h *Handler) updatePartyRoleJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body partyRoleBody
	if !decodeBody(w, r, &body) {
		return
	}
	id := r.PathValue("party_role_id")
	h.idemWrite(w, r, p, "update_party_role", id, body, http.StatusOK, func() (map[string]any, error) {
		row, err := h.Svc.UpdatePartyRole(r.Context(), p, domain.ID(id), body.write())
		return partyRoleDoc(row), err
	})
}

type markerBody struct {
	Label  *string `json:"label"`
	Color  *string `json:"color"`
	Active *bool   `json:"active"`
	Target *string `json:"target"`
}

func (b markerBody) write() MarkerWrite {
	return MarkerWrite{Label: b.Label, Color: b.Color, Active: b.Active, Target: b.Target}
}

func (h *Handler) markerDefsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	active, err := activeFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListMarkerDefinitions(r.Context(), p, active)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, markerDefDoc(row))
	}
	h.page(w, r, items)
}

func (h *Handler) markerDefJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetMarkerDefinition(r.Context(), p, domain.ID(r.PathValue("marker_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, markerDefDoc(row))
}

func (h *Handler) createMarkerDefJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body markerBody
	if !decodeBody(w, r, &body) {
		return
	}
	body.Target = nil
	h.idemWrite(w, r, p, "create_marker_definition", "", body, http.StatusCreated, func() (map[string]any, error) {
		row, err := h.Svc.CreateMarkerDefinition(r.Context(), p, body.write())
		return markerDefDoc(row), err
	})
}

func (h *Handler) updateMarkerDefJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body markerBody
	if !decodeBody(w, r, &body) {
		return
	}
	body.Target = nil
	id := r.PathValue("marker_id")
	h.idemWrite(w, r, p, "update_marker_definition", id, body, http.StatusOK, func() (map[string]any, error) {
		row, err := h.Svc.UpdateMarkerDefinition(r.Context(), p, domain.ID(id), body.write())
		return markerDefDoc(row), err
	})
}

func (h *Handler) updateMatterMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		MatterID       *domain.ID      `json:"matter_id"`
		MarkerID       *domain.ID      `json:"marker_id"`
		PracticeAreaID json.RawMessage `json:"practice_area_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	in := MatterMarkerPatch{MatterID: body.MatterID, MarkerID: body.MarkerID}
	if len(body.PracticeAreaID) > 0 {
		var area *domain.ID
		if err := json.Unmarshal(body.PracticeAreaID, &area); err != nil {
			writeErr(w, domain.Invalid("practice_area_id", "practice_area_id is an id or null"))
			return
		}
		if area == nil {
			area = new(domain.ID)
		}
		in.PracticeAreaID = area
	}
	id := r.PathValue("matter_marker_id")
	h.idemWrite(w, r, p, "update_matter_marker", id, body, http.StatusOK, func() (map[string]any, error) {
		row, err := h.Svc.UpdateMatterMarker(r.Context(), p, domain.ID(id), in)
		return markerDoc(row), err
	})
}

func (h *Handler) createModuleMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body markerBody
	if !decodeBody(w, r, &body) {
		return
	}
	body.Active = nil
	module := r.PathValue("module_id")
	h.idemWrite(w, r, p, "create_module_marker", module, body, http.StatusCreated, func() (map[string]any, error) {
		row, err := h.Svc.CreateModuleMarker(r.Context(), p, module, body.write())
		return moduleMarkerDoc(row), err
	})
}

func (h *Handler) updateModuleMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body markerBody
	if !decodeBody(w, r, &body) {
		return
	}
	body.Active = nil
	module, id := r.PathValue("module_id"), r.PathValue("marker_id")
	h.idemWrite(w, r, p, "update_module_marker", module+"/"+id, body, http.StatusOK, func() (map[string]any, error) {
		row, err := h.Svc.UpdateModuleMarker(r.Context(), p, module, domain.ID(id), body.write())
		return moduleMarkerDoc(row), err
	})
}

func (h *Handler) revokeModuleMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	module, id := r.PathValue("module_id"), r.PathValue("marker_id")
	h.idemWrite(w, r, p, "revoke_module_marker", module+"/"+id, nil, http.StatusOK, func() (map[string]any, error) {
		row, err := h.Svc.RevokeModuleMarker(r.Context(), p, module, domain.ID(id))
		return moduleMarkerDoc(row), err
	})
}

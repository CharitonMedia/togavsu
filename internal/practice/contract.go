package practice

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func customQuery(r *http.Request) map[string]string {
	out := map[string]string{}
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "custom[") && strings.HasSuffix(key, "]") && len(values) > 0 {
			out[key[len("custom["):len(key)-1]] = values[0]
		}
	}
	return out
}

func (h *Handler) contactsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListContacts(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	custom := customQuery(r)
	kind := q.Get("kind")
	tag := q.Get("tag")
	query := strings.ToLower(q.Get("q"))
	var out []domain.Contact
	for _, row := range rows {
		if kind != "" && row.Kind != kind {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.FullName), query) {
			continue
		}
		if !h.Svc.matchesCustom(row.ID, custom) {
			continue
		}
		if tag != "" {
			found := false
			for _, item := range h.Svc.db.Tags(row.ID) {
				if strings.EqualFold(item.Tag, tag) {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) contactJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetContact(r.Context(), p, domain.ID(r.PathValue("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) phonesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListPhones(r.Context(), p, domain.ID(r.URL.Query().Get("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) phoneJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetPhone(r.Context(), p, domain.ID(r.PathValue("phone_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updatePhoneJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		E164  string `json:"e164"`
		From  string `json:"effective_from"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	if body.E164 != "" {
		row, err := h.Svc.ChangePhone(r.Context(), p, domain.ID(r.PathValue("phone_id")), body.E164, body.From)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, row)
		return
	}
	row, err := h.Svc.GetPhone(r.Context(), p, domain.ID(r.PathValue("phone_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) emailsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListEmails(r.Context(), p, domain.ID(r.URL.Query().Get("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) emailJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetEmail(r.Context(), p, domain.ID(r.PathValue("email_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateEmailJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.GetEmail(r.Context(), p, domain.ID(r.PathValue("email_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) addressesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListAddresses(r.Context(), p, domain.ID(r.URL.Query().Get("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) addressJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetAddress(r.Context(), p, domain.ID(r.PathValue("address_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateAddressJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.GetAddress(r.Context(), p, domain.ID(r.PathValue("address_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) tagsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListTags(r.Context(), p, domain.ID(r.URL.Query().Get("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) tagJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetTag(r.Context(), p, domain.ID(r.PathValue("tag_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateTagJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.GetTag(r.Context(), p, domain.ID(r.PathValue("tag_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) markersJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListMarkers(r.Context(), p, domain.ID(r.URL.Query().Get("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) markerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetMarker(r.Context(), p, domain.ID(r.PathValue("matter_marker_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) wallGetJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.MatterWall(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) partiesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListParties(r.Context(), p, domain.ID(r.URL.Query().Get("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) partyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetParty(r.Context(), p, domain.ID(r.PathValue("party_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateNoteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Pinned bool     `json:"pinned"`
		Status string   `json:"status"`
		Tags   []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateNote(r.Context(), p, domain.ID(r.PathValue("note_id")), NoteInput{
		Title: body.Title, Body: body.Body, Pinned: body.Pinned, Status: body.Status, Tags: body.Tags,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) archiveNoteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.ArchiveNote(r.Context(), p, domain.ID(r.PathValue("note_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) conflictsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListConflicts(r.Context(), p, r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) conflictJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetConflict(r.Context(), p, domain.ID(r.PathValue("conflict_record_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateConflictJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name         string `json:"name"`
		Relationship string `json:"relationship"`
		Scope        string `json:"scope"`
		Reason       string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateConflict(r.Context(), p, domain.ID(r.PathValue("conflict_record_id")), ConflictInput{
		Name: body.Name, Relationship: body.Relationship, Scope: body.Scope, Reason: body.Reason,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) allChecksJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListConflictChecks(r.Context(), p, domain.ID(r.URL.Query().Get("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) checkJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetConflictCheck(r.Context(), p, domain.ID(r.PathValue("conflict_check_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) areasJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListAreas(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) createAreaJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name        string   `json:"name"`
		MatterTypes []string `json:"matter_types"`
		Stages      []string `json:"stages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateArea(r.Context(), p, body.Name, strings.Join(body.MatterTypes, ","), body.Stages)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) areaJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetArea(r.Context(), p, domain.ID(r.PathValue("practice_area_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateAreaJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name   string   `json:"name"`
		Stages []string `json:"stages"`
		Active *bool    `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	row, err := h.Svc.UpdateArea(r.Context(), p, domain.ID(r.PathValue("practice_area_id")), body.Name, body.Stages, active)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) countiesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListCounties(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) createCountyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateCounty(r.Context(), p, body.Name, body.State)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) countyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetCounty(r.Context(), p, domain.ID(r.PathValue("county_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateCountyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateCounty(r.Context(), p, domain.ID(r.PathValue("county_id")), body.Name, body.State)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) referralsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListReferrals(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) createReferralJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name      string    `json:"name"`
		Kind      string    `json:"kind"`
		ContactID domain.ID `json:"contact_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateReferral(r.Context(), p, body.Name, body.Kind, body.ContactID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) referralJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetReferral(r.Context(), p, domain.ID(r.PathValue("referral_source_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateReferralJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Active *bool  `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	row, err := h.Svc.UpdateReferral(r.Context(), p, domain.ID(r.PathValue("referral_source_id")), body.Name, body.Kind, active)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) fieldsJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ListFields(r.Context(), p, r.URL.Query().Get("record_type"), domain.ID(r.URL.Query().Get("practice_area_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) createFieldJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		RecordType      string      `json:"record_type"`
		Key             string      `json:"key"`
		Label           string      `json:"label"`
		FieldType       string      `json:"field_type"`
		SortOrder       int         `json:"sort_order"`
		Required        bool        `json:"required"`
		PracticeAreaIDs []domain.ID `json:"practice_area_ids"`
		Options         []string    `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateField(r.Context(), p, FieldDef{
		RecordType: body.RecordType, Key: body.Key, Label: body.Label, FieldType: body.FieldType,
		SortOrder: body.SortOrder, Required: body.Required, PracticeAreaIDs: body.PracticeAreaIDs, Options: body.Options,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) fieldJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetField(r.Context(), p, domain.ID(r.PathValue("definition_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateFieldJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Label           string      `json:"label"`
		Required        bool        `json:"required"`
		PracticeAreaIDs []domain.ID `json:"practice_area_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateField(r.Context(), p, domain.ID(r.PathValue("definition_id")), body.Label, body.Required, body.PracticeAreaIDs)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) retireFieldJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.RetireField(r.Context(), p, domain.ID(r.PathValue("definition_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

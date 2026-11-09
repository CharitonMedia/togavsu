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
	var out []map[string]any
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
		doc, err := h.contactDocument(r.Context(), p, row)
		if err != nil {
			writeErr(w, err)
			return
		}
		out = append(out, doc)
	}
	writeJSON(w, http.StatusOK, listEnvelope(out, len(out), ""))
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
	doc, err := h.contactDocument(r.Context(), p, row)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, phoneDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, phoneDoc(row))
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
	row, err := h.Svc.RevisePhone(r.Context(), p, domain.ID(r.PathValue("phone_id")), body.E164, body.From, body.Label)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, phoneDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, emailDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, emailDoc(row))
}

func (h *Handler) updateEmailJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Email string `json:"email"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.ReviseEmail(r.Context(), p, domain.ID(r.PathValue("email_id")), body.Email, body.Label)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, emailDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, addressDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, addressDoc(row))
}

func (h *Handler) updateAddressJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Line1 string `json:"line1"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.ReviseAddress(r.Context(), p, domain.ID(r.PathValue("address_id")), body.Line1, body.Label)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, addressDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, tagDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, tagDoc(row))
}

func (h *Handler) updateTagJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.ReviseTag(r.Context(), p, domain.ID(r.PathValue("tag_id")), body.Tag)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tagDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, markerDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, markerDoc(row))
}

func (h *Handler) wallGetJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	rows, err := h.Svc.MatterWall(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wallDoc(id, rows))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, partyDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, partyDoc(row))
}

func (h *Handler) updateNoteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body noteWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.PatchNote(r.Context(), p, domain.ID(r.PathValue("note_id")), body.patch())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.noteDoc(row))
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
	writeJSON(w, http.StatusOK, h.noteDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, conflictDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, conflictDoc(row))
}

func (h *Handler) updateConflictJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body conflictWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateConflict(r.Context(), p, domain.ID(r.PathValue("conflict_record_id")), body.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conflictDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, checkDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusOK, checkDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, areaDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusCreated, areaDoc(row))
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
	writeJSON(w, http.StatusOK, areaDoc(row))
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
	row, err := h.Svc.UpdateArea(r.Context(), p, domain.ID(r.PathValue("practice_area_id")), body.Name, body.Stages, body.Active)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, areaDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, countyDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusCreated, countyDoc(row))
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
	writeJSON(w, http.StatusOK, countyDoc(row))
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
	writeJSON(w, http.StatusOK, countyDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, referralDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusCreated, referralDoc(row))
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
	writeJSON(w, http.StatusOK, referralDoc(row))
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
	row, err := h.Svc.UpdateReferral(r.Context(), p, domain.ID(r.PathValue("referral_source_id")), body.Name, body.Kind, body.Active)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, referralDoc(row))
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
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, fieldDoc(row))
	}
	writeJSON(w, http.StatusOK, listEnvelope(items, len(items), ""))
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
	writeJSON(w, http.StatusCreated, fieldDoc(row))
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
	writeJSON(w, http.StatusOK, fieldDoc(row))
}

func (h *Handler) updateFieldJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Label           string      `json:"label"`
		Required        *bool       `json:"required"`
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
	writeJSON(w, http.StatusOK, fieldDoc(row))
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
	writeJSON(w, http.StatusOK, fieldDoc(row))
}

package practice

import (
	"encoding/json"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (h *Handler) practiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/contacts", h.contactsJSON)
	mux.HandleFunc("POST /api/v1/contacts", h.createContactJSON)
	mux.HandleFunc("GET /api/v1/contacts/{contact_id}", h.contactJSON)
	mux.HandleFunc("PATCH /api/v1/contacts/{contact_id}", h.updateContactJSON)
	mux.HandleFunc("PUT /api/v1/contacts/{contact_id}", h.updateContactJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/archive", h.archiveContactJSON)
	mux.HandleFunc("PUT /api/v1/contacts/{contact_id}/custom-fields", h.contactCustomJSON)
	mux.HandleFunc("GET /api/v1/contact-phones", h.phonesJSON)
	mux.HandleFunc("POST /api/v1/contact-phones", h.createPhoneJSON)
	mux.HandleFunc("GET /api/v1/contact-phones/{phone_id}", h.phoneJSON)
	mux.HandleFunc("PATCH /api/v1/contact-phones/{phone_id}", h.updatePhoneJSON)
	mux.HandleFunc("GET /api/v1/contact-emails", h.emailsJSON)
	mux.HandleFunc("POST /api/v1/contact-emails", h.createEmailJSON)
	mux.HandleFunc("GET /api/v1/contact-emails/{email_id}", h.emailJSON)
	mux.HandleFunc("PATCH /api/v1/contact-emails/{email_id}", h.updateEmailJSON)
	mux.HandleFunc("GET /api/v1/contact-addresses", h.addressesJSON)
	mux.HandleFunc("POST /api/v1/contact-addresses", h.createAddressJSON)
	mux.HandleFunc("GET /api/v1/contact-addresses/{address_id}", h.addressJSON)
	mux.HandleFunc("PATCH /api/v1/contact-addresses/{address_id}", h.updateAddressJSON)
	mux.HandleFunc("GET /api/v1/contact-tags", h.tagsJSON)
	mux.HandleFunc("POST /api/v1/contact-tags", h.createTagJSON)
	mux.HandleFunc("GET /api/v1/contact-tags/{tag_id}", h.tagJSON)
	mux.HandleFunc("PATCH /api/v1/contact-tags/{tag_id}", h.updateTagJSON)
	mux.HandleFunc("POST /api/v1/matters", h.createMatterJSON)
	mux.HandleFunc("PATCH /api/v1/matters/{matter_id}", h.updateMatterJSON)
	mux.HandleFunc("PUT /api/v1/matters/{matter_id}", h.updateMatterJSON)
	mux.HandleFunc("GET /api/v1/matter-markers", h.markersJSON)
	mux.HandleFunc("POST /api/v1/matter-markers", h.createMarkerJSON)
	mux.HandleFunc("GET /api/v1/matter-markers/{matter_marker_id}", h.markerJSON)
	mux.HandleFunc("DELETE /api/v1/matter-markers/{matter_marker_id}", h.removeMarkerJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/close-preview", h.closePreviewJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/timeline", h.timelineJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/wall", h.wallGetJSON)
	mux.HandleFunc("PUT /api/v1/matters/{matter_id}/wall", h.wallJSON)
	mux.HandleFunc("GET /api/v1/worklists/matters-missing-case-number", h.needsCaseJSON)
	mux.HandleFunc("GET /api/v1/matter-parties", h.partiesJSON)
	mux.HandleFunc("POST /api/v1/matter-parties", h.createPartyJSON)
	mux.HandleFunc("GET /api/v1/matter-parties/{party_id}", h.partyJSON)
	mux.HandleFunc("PATCH /api/v1/matter-parties/{party_id}", h.updatePartyJSON)
	mux.HandleFunc("PUT /api/v1/matter-parties/{party_id}", h.updatePartyJSON)
	mux.HandleFunc("GET /api/v1/notes", h.notesJSON)
	mux.HandleFunc("POST /api/v1/notes", h.createNoteJSON)
	mux.HandleFunc("GET /api/v1/notes/{note_id}", h.noteJSON)
	mux.HandleFunc("PATCH /api/v1/notes/{note_id}", h.updateNoteJSON)
	mux.HandleFunc("POST /api/v1/notes/{note_id}/archive", h.archiveNoteJSON)
	mux.HandleFunc("GET /api/v1/conflict-records", h.conflictsJSON)
	mux.HandleFunc("POST /api/v1/conflict-records", h.createConflictJSON)
	mux.HandleFunc("GET /api/v1/conflict-records/{conflict_record_id}", h.conflictJSON)
	mux.HandleFunc("PATCH /api/v1/conflict-records/{conflict_record_id}", h.updateConflictJSON)
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/conflict-checks", h.runCheckJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/conflict-checks", h.listChecksJSON)
	mux.HandleFunc("GET /api/v1/conflict-checks", h.allChecksJSON)
	mux.HandleFunc("GET /api/v1/conflict-checks/{conflict_check_id}", h.checkJSON)
	mux.HandleFunc("POST /api/v1/conflict-checks/{conflict_check_id}/decisions", h.decideJSON)
	mux.HandleFunc("GET /api/v1/practice-areas", h.areasJSON)
	mux.HandleFunc("POST /api/v1/practice-areas", h.createAreaJSON)
	mux.HandleFunc("GET /api/v1/practice-areas/{practice_area_id}", h.areaJSON)
	mux.HandleFunc("PATCH /api/v1/practice-areas/{practice_area_id}", h.updateAreaJSON)
	mux.HandleFunc("GET /api/v1/counties", h.countiesJSON)
	mux.HandleFunc("POST /api/v1/counties", h.createCountyJSON)
	mux.HandleFunc("GET /api/v1/counties/{county_id}", h.countyJSON)
	mux.HandleFunc("PATCH /api/v1/counties/{county_id}", h.updateCountyJSON)
	mux.HandleFunc("GET /api/v1/referral-sources", h.referralsJSON)
	mux.HandleFunc("POST /api/v1/referral-sources", h.createReferralJSON)
	mux.HandleFunc("GET /api/v1/referral-sources/{referral_source_id}", h.referralJSON)
	mux.HandleFunc("PATCH /api/v1/referral-sources/{referral_source_id}", h.updateReferralJSON)
	mux.HandleFunc("GET /api/v1/custom-field-definitions", h.fieldsJSON)
	mux.HandleFunc("POST /api/v1/custom-field-definitions", h.createFieldJSON)
	mux.HandleFunc("GET /api/v1/custom-field-definitions/{definition_id}", h.fieldJSON)
	mux.HandleFunc("PATCH /api/v1/custom-field-definitions/{definition_id}", h.updateFieldJSON)
	mux.HandleFunc("POST /api/v1/custom-field-definitions/{definition_id}/retire", h.retireFieldJSON)
}

func (h *Handler) caller(w http.ResponseWriter, r *http.Request, write bool) (domain.Principal, bool) {
	if write {
		if err := h.requireWriteCSRF(r); err != nil {
			writeErr(w, err)
			return domain.Principal{}, false
		}
	}
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	return p, true
}

func (h *Handler) createContactJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body contactWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	in := body.input()
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	row, err := h.Svc.CreateContact(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	shown, err := h.Svc.GetContact(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	doc, err := h.contactDocument(r.Context(), p, shown)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) updateContactJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body contactWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	var row domain.Contact
	var err error
	if r.Method == http.MethodPatch {
		row, err = h.Svc.PatchContact(r.Context(), p, id, body.patch())
	} else {
		row, err = h.Svc.UpdateContact(r.Context(), p, id, body.input())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	shown, err := h.Svc.GetContact(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	doc, err := h.contactDocument(r.Context(), p, shown)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) archiveContactJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.ArchiveContact(r.Context(), p, domain.ID(r.PathValue("contact_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	shown, err := h.Svc.GetContact(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	doc, err := h.contactDocument(r.Context(), p, shown)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) contactCustomJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	values, err := decodeCustom(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.SetCustomFields(r.Context(), p, RecordContact, id, domain.ID(""), values); err != nil {
		writeErr(w, err)
		return
	}
	row, err := h.Svc.GetContact(r.Context(), p, id)
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

func (h *Handler) createPhoneJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ContactID domain.ID `json:"contact_id"`
		Label     string    `json:"label"`
		Phone     string    `json:"phone"`
		E164      string    `json:"e164"`
		From      string    `json:"effective_from"`
		Primary   bool      `json:"primary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	phone := body.E164
	if phone == "" {
		phone = body.Phone
	}
	row, err := h.Svc.AddPhone(r.Context(), p, body.ContactID, body.Label, phone, body.From, body.Primary)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, phoneDoc(row))
}

func (h *Handler) createEmailJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ContactID domain.ID `json:"contact_id"`
		Label     string    `json:"label"`
		Email     string    `json:"email"`
		From      string    `json:"effective_from"`
		Primary   bool      `json:"primary"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddEmail(r.Context(), p, body.ContactID, body.Label, body.Email, body.From, body.Primary)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, emailDoc(row))
}

func (h *Handler) createAddressJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ContactID domain.ID `json:"contact_id"`
		Label     string    `json:"label"`
		Primary   bool      `json:"primary"`
		Line1     string    `json:"line1"`
		Line2     string    `json:"line2"`
		City      string    `json:"city"`
		Region    string    `json:"region"`
		Postal    string    `json:"postal_code"`
		Country   string    `json:"country"`
		From      string    `json:"effective_from"`
		To        string    `json:"effective_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddAddress(r.Context(), p, domain.ContactAddress{
		ContactID: body.ContactID, Label: body.Label, Primary: body.Primary, Line1: body.Line1, Line2: body.Line2,
		City: body.City, Region: body.Region, PostalCode: body.Postal, Country: body.Country,
		EffectiveFrom: body.From, EffectiveTo: body.To,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, addressDoc(row))
}

func (h *Handler) createTagJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		ContactID domain.ID `json:"contact_id"`
		Tag       string    `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddContactTag(r.Context(), p, body.ContactID, body.Tag)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tagDoc(row))
}

func (h *Handler) createMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body matterWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	in := body.input()
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	row, err := h.Svc.CreateMatter(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matterItem(view))
}

func (h *Handler) updateMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body matterWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	var row domain.Matter
	var err error
	if r.Method == http.MethodPatch {
		row, err = h.Svc.PatchMatter(r.Context(), p, id, body.patch())
	} else {
		row, err = h.Svc.UpdateMatter(r.Context(), p, id, body.input())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, row.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, matterItem(view))
}

func (h *Handler) closePreviewJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	matter, warnings, err := h.Svc.PreviewClose(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matter_id": matter.ID, "warnings": warnings})
}

func (h *Handler) timelineJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.Timeline(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, eventDoc(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pageDoc(len(data))})
}

func (h *Handler) wallJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		UserIDs []domain.ID `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	if err := h.Svc.SetWall(r.Context(), p, domain.ID(r.PathValue("matter_id")), body.UserIDs); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matter_id": r.PathValue("matter_id"), "user_ids": body.UserIDs})
}

func (h *Handler) needsCaseJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.NeedsCaseNumber(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		view, err := h.Svc.GetMatter(r.Context(), p, row.ID)
		if err != nil {
			continue
		}
		data = append(data, matterItem(view))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pageDoc(len(data))})
}

func (h *Handler) createPartyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body partyWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddParty(r.Context(), p, body.MatterID, body.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, partyDoc(row))
}

func (h *Handler) updatePartyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body partyWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	id := domain.ID(r.PathValue("party_id"))
	var row domain.MatterParty
	var err error
	if r.Method == http.MethodPatch {
		row, err = h.Svc.PatchParty(r.Context(), p, id, body.patch())
	} else {
		row, err = h.Svc.UpdateParty(r.Context(), p, id, body.input())
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, partyDoc(row))
}

func (h *Handler) notesJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	q := r.URL.Query()
	notes, next, err := h.Svc.ListNotes(r.Context(), p, NoteFilter{
		Query: q.Get("q"), MatterID: domain.ID(q.Get("matter_id")), AuthorID: domain.ID(q.Get("author_id")),
		Tag: q.Get("tag"), From: q.Get("from"), To: q.Get("to"), Cursor: q.Get("cursor"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(notes))
	for _, note := range notes {
		items = append(items, h.noteDoc(note))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (h *Handler) createNoteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body noteWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateNote(r.Context(), p, body.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.noteDoc(row))
}

func (h *Handler) noteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	row, err := h.Svc.GetNote(r.Context(), p, domain.ID(r.PathValue("note_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.noteDoc(row))
}

func (h *Handler) createConflictJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body conflictWrite
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateConflict(r.Context(), p, body.input())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conflictDoc(row))
}

func (h *Handler) runCheckJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	row, err := h.Svc.RunConflictCheck(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checkDoc(row))
}

func (h *Handler) listChecksJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, false)
	if !ok {
		return
	}
	rows, err := h.Svc.ConflictChecks(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, checkDoc(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) decideJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		HitID    domain.ID `json:"hit_id"`
		Decision string    `json:"decision"`
		Reason   string    `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	if _, err := h.Svc.DecideConflictOnCheck(r.Context(), p, domain.ID(r.PathValue("conflict_check_id")), body.HitID, body.Decision, body.Reason); err != nil {
		writeErr(w, err)
		return
	}
	run, err := h.Svc.GetConflictCheck(r.Context(), p, domain.ID(r.PathValue("conflict_check_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, checkDoc(run))
}

func (h *Handler) createMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		MatterID       domain.ID `json:"matter_id"`
		MarkerID       domain.ID `json:"marker_id"`
		Label          string    `json:"label"`
		PracticeAreaID domain.ID `json:"practice_area_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	var row MatterMarker
	var err error
	if body.MarkerID != "" {
		row, err = h.Svc.PinMarkerID(r.Context(), p, body.MatterID, body.MarkerID, body.PracticeAreaID)
	} else {
		row, err = h.Svc.PinMarker(r.Context(), p, body.MatterID, body.Label, body.PracticeAreaID)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, markerDoc(row))
}

func (h *Handler) removeMarkerJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	if err := h.Svc.RemoveMarkerID(r.Context(), p, domain.ID(r.PathValue("matter_marker_id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

package practice

import (
	"encoding/json"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (h *Handler) practiceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/contacts", h.createContactJSON)
	mux.HandleFunc("PUT /api/v1/contacts/{contact_id}", h.updateContactJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/archive", h.archiveContactJSON)
	mux.HandleFunc("PUT /api/v1/contacts/{contact_id}/custom-fields", h.contactCustomJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/phones", h.createPhoneJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/emails", h.createEmailJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/addresses", h.createAddressJSON)
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/tags", h.createTagJSON)
	mux.HandleFunc("POST /api/v1/matters", h.createMatterJSON)
	mux.HandleFunc("PUT /api/v1/matters/{matter_id}", h.updateMatterJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/close-preview", h.closePreviewJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/timeline", h.timelineJSON)
	mux.HandleFunc("PUT /api/v1/matters/{matter_id}/wall", h.wallJSON)
	mux.HandleFunc("GET /api/v1/worklists/matters-missing-case-number", h.needsCaseJSON)
	mux.HandleFunc("POST /api/v1/matter-parties", h.createPartyJSON)
	mux.HandleFunc("PUT /api/v1/matter-parties/{party_id}", h.updatePartyJSON)
	mux.HandleFunc("GET /api/v1/notes", h.notesJSON)
	mux.HandleFunc("POST /api/v1/notes", h.createNoteJSON)
	mux.HandleFunc("GET /api/v1/notes/{note_id}", h.noteJSON)
	mux.HandleFunc("POST /api/v1/conflict-records", h.createConflictJSON)
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/conflict-checks", h.runCheckJSON)
	mux.HandleFunc("GET /api/v1/matters/{matter_id}/conflict-checks", h.listChecksJSON)
	mux.HandleFunc("POST /api/v1/conflict-checks/{conflict_check_id}/decisions", h.decideJSON)
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
	var body ContactInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	body.IdempotencyKey = r.Header.Get("Idempotency-Key")
	row, err := h.Svc.CreateContact(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateContactJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body ContactInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateContact(r.Context(), p, domain.ID(r.PathValue("contact_id")), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createPhoneJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
		Phone string `json:"phone"`
		From  string `json:"effective_from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddPhone(r.Context(), p, domain.ID(r.PathValue("contact_id")), body.Label, body.Phone, body.From, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createEmailJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Label string `json:"label"`
		Email string `json:"email"`
		From  string `json:"effective_from"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddEmail(r.Context(), p, domain.ID(r.PathValue("contact_id")), body.Label, body.Email, body.From, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createAddressJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body domain.ContactAddress
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	body.ContactID = domain.ID(r.PathValue("contact_id"))
	row, err := h.Svc.AddAddress(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createTagJSON(w http.ResponseWriter, r *http.Request) {
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
	row, err := h.Svc.AddContactTag(r.Context(), p, domain.ID(r.PathValue("contact_id")), body.Tag)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body MatterInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	body.IdempotencyKey = r.Header.Get("Idempotency-Key")
	row, err := h.Svc.CreateMatter(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updateMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body MatterInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateMatter(r.Context(), p, domain.ID(r.PathValue("matter_id")), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
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
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (h *Handler) createPartyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body struct {
		MatterID domain.ID `json:"matter_id"`
		PartyInput
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.AddParty(r.Context(), p, body.MatterID, body.PartyInput)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) updatePartyJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body PartyInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.UpdateParty(r.Context(), p, domain.ID(r.PathValue("party_id")), body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": notes, "next_cursor": next})
}

func (h *Handler) createNoteJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body NoteInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateNote(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) createConflictJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	var body ConflictInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "body is invalid"))
		return
	}
	row, err := h.Svc.CreateConflict(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, row)
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
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
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
	row, err := h.Svc.DecideConflictOnCheck(r.Context(), p, domain.ID(r.PathValue("conflict_check_id")), body.HitID, body.Decision, body.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

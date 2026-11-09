package practice

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func (h *Handler) form(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	if err := r.ParseForm(); err != nil {
		writeErr(w, domain.Invalid("form", "form is invalid"))
		return domain.Principal{}, false
	}
	if err := h.checkCSRF(r); err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	return p, true
}

func (h *Handler) createMatterPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	row, err := h.Svc.CreateMatter(r.Context(), p, MatterInput{
		Name: r.FormValue("name"), PracticeAreaID: domain.ID(r.FormValue("practice_area_id")),
		BillingType: r.FormValue("billing_type"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(row.ID), http.StatusSeeOther)
}

func (h *Handler) editMatterPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	name := r.FormValue("name")
	court := r.FormValue("court")
	stage := r.FormValue("stage")
	caseNumber := r.FormValue("case_number")
	attorney := domain.ID(r.FormValue("responsible_attorney_user_id"))
	if _, err := h.Svc.PatchMatter(r.Context(), p, id, MatterPatch{
		Name: &name, Court: &court, Stage: &stage, CaseNumber: &caseNumber, ResponsibleAttorneyID: &attorney,
	}); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) partyPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	if _, err := h.Svc.AddParty(r.Context(), p, id, PartyInput{
		ContactID: domain.ID(r.FormValue("contact_id")), Role: r.FormValue("role"), Primary: r.FormValue("primary") == "1",
	}); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) matterNotePost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	if _, err := h.Svc.CreateNote(r.Context(), p, NoteInput{
		MatterID: id, Title: r.FormValue("title"), Body: r.FormValue("body"),
	}); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) checkPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	if _, err := h.Svc.RunConflictCheck(r.Context(), p, id); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(id), http.StatusSeeOther)
}

func (h *Handler) decisionPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	checkID := domain.ID(r.PathValue("conflict_check_id"))
	if _, err := h.Svc.DecideConflictOnCheck(r.Context(), p, checkID, domain.ID(r.FormValue("hit_id")), r.FormValue("decision"), r.FormValue("reason")); err != nil {
		writeErr(w, err)
		return
	}
	run, err := h.Svc.GetConflictCheck(r.Context(), p, checkID)
	if err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/matters/"+string(run.MatterID), http.StatusSeeOther)
}

func (h *Handler) createContactPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	row, err := h.Svc.CreateContact(r.Context(), p, ContactInput{
		Kind: r.FormValue("kind"), GivenName: r.FormValue("given_name"), FamilyName: r.FormValue("family_name"),
		FullName: r.FormValue("organization_name"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/contacts/"+string(row.ID), http.StatusSeeOther)
}

func (h *Handler) editContactPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	given := r.FormValue("given_name")
	family := r.FormValue("family_name")
	preferred := r.FormValue("preferred_name")
	if _, err := h.Svc.PatchContact(r.Context(), p, id, ContactPatch{
		GivenName: &given, FamilyName: &family, PreferredName: &preferred,
	}); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/contacts/"+string(id), http.StatusSeeOther)
}

func (h *Handler) archiveContactPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	if _, err := h.Svc.ArchiveContact(r.Context(), p, id); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/contacts/"+string(id), http.StatusSeeOther)
}

func (h *Handler) contactCustomPost(w http.ResponseWriter, r *http.Request) {
	p, ok := h.form(w, r)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	var values []CustomInput
	for key, vals := range r.Form {
		if len(key) < 4 || key[:3] != "cf_" || len(vals) == 0 {
			continue
		}
		values = append(values, CustomInput{DefinitionID: domain.ID(key[3:]), Value: vals[0]})
	}
	if err := h.Svc.SetCustomFields(r.Context(), p, RecordContact, id, domain.ID(""), values); err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, "/practice/contacts/"+string(id), http.StatusSeeOther)
}

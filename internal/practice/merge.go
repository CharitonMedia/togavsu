package practice

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Relinked is how many of one module's rows a merge moved to the survivor.
type Relinked struct {
	ModuleID string `json:"module_id"`
	Rows     int    `json:"rows"`
}

// Relinker moves module rows that point at core record from (in core table "contacts"
// or "matters") onto to. It runs inside the merge's transaction: the core gives it the
// module runtime's Schemas.Relink. Nil relinks nothing (no modules).
type Relinker func(ctx context.Context, table string, from, to domain.ID) ([]Relinked, error)

// MergeResult is the contract's MergeResult.
type MergeResult struct {
	SurvivorID domain.ID  `json:"survivor_id"`
	ArchivedID domain.ID  `json:"archived_id"`
	Relinked   []Relinked `json:"modules_relinked"`
}

// txStore is a store that can run a write in the same transaction as the current unit.
type txStore interface {
	InTx(ctx context.Context, fn func(context.Context) error) error
}

// mergeRoles are the roles the contract allows to merge contacts and matters.
func mergeRole(p domain.Principal) error {
	if p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) {
		return nil
	}
	return domain.Forbidden("only an attorney or admin can merge records")
}

// relinkIn runs relink in the unit's transaction and reports what it moved once that
// transaction commits.
func (s *Service) relinkIn(ctx context.Context, relink Relinker, table string, from, to domain.ID, moved *[]Relinked) error {
	*moved = []Relinked{}
	if relink == nil {
		return nil
	}
	fn := func(ctx context.Context) error {
		got, err := relink(ctx, table, from, to)
		if err == nil && got != nil {
			*moved = got
		}
		return err
	}
	if t, ok := s.db.(txStore); ok {
		return t.InTx(ctx, fn)
	}
	return fn(ctx)
}

// ArchiveMatter sets the matter's archived flag. It is not a close: the matter stays
// readable and drops out of default lists. Archiving an archived matter changes nothing.
func (s *Service) ArchiveMatter(ctx context.Context, p domain.Principal, id domain.ID) (_ domain.Matter, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, id, domain.ActionWrite); err != nil {
		return domain.Matter{}, err
	}
	return s.archiveMatter(ctx, p, id, "", "matter.archived", "Archived matter ")
}

func (s *Service) archiveMatter(ctx context.Context, p domain.Principal, id domain.ID, reason, action, summary string) (domain.Matter, error) {
	prev, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	if prev.Archived {
		return prev, nil
	}
	next := prev
	next.Archived = true
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateMatter(ctx, next); err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	if err := s.record(ctx, p, []change{{
		MatterID: id, Entity: "matter", EntityID: id, Field: "archived", Old: "false", New: "true",
		Reason: reason, Kind: "update", Action: action, Summary: summary + next.Number,
	}}); err != nil {
		return domain.Matter{}, err
	}
	return next, nil
}

// MergeMatter merges matter id into survivor: module rows that point at id move to the
// survivor and id is archived, in one transaction. The caller must be an attorney or admin
// who can write both matters; a matter the caller can't see is not_found for both, and
// nothing is relinked, archived or emitted unless both are allowed. Nothing is deleted.
func (s *Service) MergeMatter(ctx context.Context, p domain.Principal, id, survivor domain.ID, relink Relinker) (MergeResult, error) {
	var moved []Relinked
	err := func() (err error) {
		ctx, done := s.unit(ctx)
		defer func() { err = done(err) }()
		if err := mergeRole(p); err != nil {
			return err
		}
		if err := s.gateMatter(ctx, p, id, domain.ActionWrite); err != nil {
			return err
		}
		if survivor.IsZero() {
			return domain.Invalid("survivor_id", "name the matter that remains")
		}
		if err := s.gateMatter(ctx, p, survivor, domain.ActionWrite); err != nil {
			return err
		}
		if survivor == id {
			return domain.Invalid("survivor_id", "a matter can't be merged into itself")
		}
		keep, err := s.db.Matter(ctx, survivor)
		if err != nil {
			return missing(err, "matter")
		}
		if keep.Archived {
			return domain.Invalid("survivor_id", "the surviving matter is archived")
		}
		gone, err := s.db.Matter(ctx, id)
		if err != nil {
			return missing(err, "matter")
		}
		if gone.Archived {
			return domain.Conflict("matter is already archived")
		}
		if err := s.relinkIn(ctx, relink, "matters", id, survivor, &moved); err != nil {
			return err
		}
		_, err = s.archiveMatter(ctx, p, id, "merged into "+keep.Number, "matter.merged", "Merged into "+keep.Number+": ")
		return err
	}()
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{SurvivorID: survivor, ArchivedID: id, Relinked: moved}, nil
}

// MergeContact merges contact id into survivor: module rows that point at id move to the
// survivor and id is archived, in one transaction. Attorney or admin with contact write.
func (s *Service) MergeContact(ctx context.Context, p domain.Principal, id, survivor domain.ID, relink Relinker) (MergeResult, error) {
	var moved []Relinked
	err := func() (err error) {
		ctx, done := s.unit(ctx)
		defer func() { err = done(err) }()
		if err := mergeRole(p); err != nil {
			return err
		}
		if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResContact, Action: domain.ActionWrite}); err != nil {
			return err
		}
		if err := s.readableContact(ctx, p, id); err != nil {
			return err
		}
		if survivor.IsZero() {
			return domain.Invalid("survivor_id", "name the contact that remains")
		}
		if err := s.readableContact(ctx, p, survivor); err != nil {
			return err
		}
		if survivor == id {
			return domain.Invalid("survivor_id", "a contact can't be merged into itself")
		}
		keep, err := s.db.Contact(ctx, survivor)
		if err != nil {
			return missing(err, "contact")
		}
		if keep.Archived {
			return domain.Invalid("survivor_id", "the surviving contact is archived")
		}
		prev, err := s.db.Contact(ctx, id)
		if err != nil {
			return missing(err, "contact")
		}
		if prev.Archived {
			return domain.Conflict("contact is already archived")
		}
		if err := s.relinkIn(ctx, relink, "contacts", id, survivor, &moved); err != nil {
			return err
		}
		next := prev
		next.Archived = true
		next.UpdatedAt = s.clock()
		if err := s.db.UpdateContact(ctx, next); err != nil {
			return missing(err, "contact")
		}
		return s.record(ctx, p, []change{{
			ContactID: id, Entity: "contact", EntityID: id, Field: "archived", Old: "false", New: "true",
			Reason: "merged into " + string(survivor), Kind: "update", Action: "contact.merged",
			Summary: "Merged contact into " + keep.FullName,
		}})
	}()
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{SurvivorID: survivor, ArchivedID: id, Relinked: moved}, nil
}

// archivedFilter is the archived query parameter of listMatter: absent omits archived
// matters, true lists only archived ones, false only the others.
func archivedFilter(r *http.Request) (keep func(bool) bool, err error) {
	raw := r.URL.Query().Get("archived")
	if raw == "" {
		return func(a bool) bool { return !a }, nil
	}
	want, perr := strconv.ParseBool(raw)
	if perr != nil {
		return nil, domain.Invalid("archived", "use true or false")
	}
	return func(a bool) bool { return a == want }, nil
}

// keepMatters is the archived filter applied to a matter list, the one rule every
// default matter list (JSON, CSV and the Matters screen) shares.
func keepMatters(rows []MatterView, keep func(bool) bool) []MatterView {
	kept := rows[:0]
	for _, row := range rows {
		if keep(row.Matter.Archived) {
			kept = append(kept, row)
		}
	}
	return kept
}

func (h *Handler) mergeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/archive", h.archiveMatterJSON)
	mux.HandleFunc("POST /api/v1/matters/{matter_id}/merge", h.mergeJSON("matter_id", "merge_matter", h.Svc.MergeMatter))
	mux.HandleFunc("POST /api/v1/contacts/{contact_id}/merge", h.mergeJSON("contact_id", "merge_contact", h.Svc.MergeContact))
}

func (h *Handler) archiveMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, ok := h.caller(w, r, true)
	if !ok {
		return
	}
	id := domain.ID(r.PathValue("matter_id"))
	var replay map[string]any
	hit, hold, err := h.Svc.idemBegin(r.Context(), p, r.Header.Get("Idempotency-Key"), "archive_matter", string(id), nil, &replay)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer hold.abort()
	if hit {
		writeJSON(w, http.StatusOK, replay)
		return
	}
	if _, err := h.Svc.ArchiveMatter(r.Context(), p, id); err != nil {
		writeErr(w, err)
		return
	}
	view, err := h.Svc.GetMatter(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	doc := matterItem(view)
	if err := hold.finish(r.Context(), doc); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func (h *Handler) mergeJSON(param, op string, merge func(context.Context, domain.Principal, domain.ID, domain.ID, Relinker) (MergeResult, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := h.caller(w, r, true)
		if !ok {
			return
		}
		var body struct {
			SurvivorID domain.ID `json:"survivor_id"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeErr(w, domain.Invalid("body", "body must be {\"survivor_id\": \"...\"}"))
			return
		}
		id := domain.ID(r.PathValue(param))
		var replay MergeResult
		hit, hold, err := h.Svc.idemBegin(r.Context(), p, r.Header.Get("Idempotency-Key"), op, string(id), body, &replay)
		if err != nil {
			writeErr(w, err)
			return
		}
		defer hold.abort()
		if hit {
			writeJSON(w, http.StatusOK, replay)
			return
		}
		out, err := merge(r.Context(), p, id, body.SurvivorID, h.Relink)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := hold.finish(r.Context(), out); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

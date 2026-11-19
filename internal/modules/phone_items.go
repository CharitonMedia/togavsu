package modules

import (
	"context"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"net/http"
	"net/url"
)

// ContactPhoneItem is one enabled, approved module action for a readable phone.
type ContactPhoneItem struct {
	Module string `json:"module_id"`
	Name   string `json:"module_name"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Path   string `json:"path"`
}

func (s *Service) ContactPhoneItems(ctx context.Context, p domain.Principal, phone domain.ID) ([]ContactPhoneItem, error) {
	if s.d.Phone == nil {
		return nil, domain.NotFound("phone")
	}
	if _, err := s.d.Phone(ctx, p, phone); err != nil {
		return nil, err
	}
	mods, err := s.Enabled(ctx)
	if err != nil {
		return nil, err
	}
	out := []ContactPhoneItem{}
	for _, mod := range mods {
		if !mod.Approved["contacts:read"] {
			continue
		}
		for _, it := range mod.Manifest.ContactPhoneItems {
			out = append(out, ContactPhoneItem{Module: mod.ID, Name: mod.Manifest.Name, ID: it.ID, Title: it.Title, Path: "/modules/" + mod.ID + "/contact-phone-items/" + it.ID + "?phone_id=" + url.QueryEscape(string(phone))})
		}
	}
	return out, nil
}

func (h *Handler) phoneItemPage(w http.ResponseWriter, r *http.Request, c Caller) {
	phoneID := domain.ID(r.URL.Query().Get("phone_id"))
	items, err := h.Svc.ContactPhoneItems(r.Context(), c.Principal, phoneID)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, it := range items {
		if it.Module != r.PathValue("module_id") || it.ID != r.PathValue("item") {
			continue
		}
		mod, err := h.Svc.Installed(r.Context(), it.Module)
		if err != nil {
			writeErr(w, err)
			return
		}
		phone, err := h.Svc.d.Phone(r.Context(), c.Principal, phoneID)
		if err != nil {
			writeErr(w, err)
			return
		}
		tok, err := h.Svc.IssueContext(r.Context(), c.Principal, it.Module, "")
		if err != nil {
			writeErr(w, err)
			return
		}
		for _, spec := range mod.Manifest.ContactPhoneItems {
			if spec.ID == it.ID {
				writeFrame(w, r, true, it.Title, FramePath(it.Module, spec.Path, tok.Token, url.Values{"phone_id": {string(phone.ID)}, "contact_id": {string(phone.ContactID)}}), it.Module)
				return
			}
		}
	}
	writeErr(w, domain.NotFound("phone item"))
}

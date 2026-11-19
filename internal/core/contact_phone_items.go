package core

import (
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"net/http"
)

func (a *App) contactPhoneRows(r *http.Request, p domain.Principal, contact domain.ID) ([]practice.PhoneRow, error) {
	phones, err := a.Practice.ListPhones(r.Context(), p, contact)
	if err != nil {
		return nil, err
	}
	out := make([]practice.PhoneRow, 0, len(phones))
	for _, phone := range phones {
		items, err := a.ext.Modules.ContactPhoneItems(r.Context(), p, phone.ID)
		if err != nil {
			return nil, err
		}
		row := practice.PhoneRow{Phone: phone}
		for _, it := range items {
			row.Items = append(row.Items, practice.PhoneAction{Title: it.Title, Name: it.Name, Path: it.Path})
		}
		out = append(out, row)
	}
	return out, nil
}
func (a *App) contactPhoneItemsJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := a.ext.Modules.ContactPhoneItems(r.Context(), p, domain.ID(r.URL.Query().Get("phone_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	from, limit, err := a.directoryPageArgs(r.Context(), r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if from > len(items) {
		from = len(items)
	}
	to := from + limit
	if to > len(items) {
		to = len(items)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items[from:to], "page": directoryPageInfo(limit, to, to < len(items))})
}

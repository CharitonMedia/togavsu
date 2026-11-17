package core

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/practice"
)

// meContactRoutes mounts CORE-13 client contact self-edit.
func (a *App) meContactRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/me/contact", a.getMeContactJSON)
	m.HandleFunc("PATCH /api/v1/me/contact", a.patchMeContactJSON)
}

func (a *App) getMeContactJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	row, err := a.Practice.GetMeContact(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meContactJSON(row))
}

func (a *App) patchMeContactJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Email   *string `json:"email"`
		Phone   *string `json:"phone"`
		Address *struct {
			Line1      string `json:"line1"`
			Line2      string `json:"line2"`
			City       string `json:"city"`
			Region     string `json:"region"`
			PostalCode string `json:"postal_code"`
			Country    string `json:"country"`
		} `json:"address"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	in := practice.MeContactPatch{Email: body.Email, Phone: body.Phone}
	if body.Address != nil {
		in.Address = &practice.MeMailingAddress{
			Line1: body.Address.Line1, Line2: body.Address.Line2, City: body.Address.City,
			Region: body.Address.Region, PostalCode: body.Address.PostalCode, Country: body.Address.Country,
		}
	}
	row, err := a.Practice.UpdateMeContact(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meContactJSON(row))
}

func meContactJSON(row practice.MeContact) map[string]any {
	out := map[string]any{
		"contact_id": row.ContactID,
		"email":      nil,
		"phone":      nil,
		"address":    nil,
	}
	if row.Email != "" {
		out["email"] = row.Email
	}
	if row.Phone != "" {
		out["phone"] = row.Phone
	}
	if row.Address != nil {
		out["address"] = map[string]any{
			"line1": row.Address.Line1, "line2": row.Address.Line2, "city": row.Address.City,
			"region": row.Address.Region, "postal_code": row.Address.PostalCode, "country": row.Address.Country,
		}
	}
	return out
}

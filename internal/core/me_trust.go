package core

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// meTrustRoutes mounts CORE-12 client trust reads.
func (a *App) meTrustRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/me/trust", a.getMeTrustJSON)
	m.HandleFunc("GET /api/v1/me/trust-ledger", a.getMeTrustLedgerJSON)
}

func (a *App) getMeTrustJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !practice.ClientOnly(p) {
		writeErr(w, domain.Forbidden("client role is required"))
		return
	}
	views, err := a.Practice.ListMatters(r.Context(), p, "", "")
	if err != nil {
		writeErr(w, err)
		return
	}
	matters := make([]domain.ID, 0, len(views))
	for _, v := range views {
		matters = append(matters, v.Matter.ID)
	}
	rows, err := a.Trust.ClientTrust(r.Context(), p, matters)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (a *App) getMeTrustLedgerJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !practice.ClientOnly(p) {
		writeErr(w, domain.Forbidden("client role is required"))
		return
	}
	matter := domain.ID(r.URL.Query().Get("matter_id"))
	rows, err := a.Trust.ClientTrustLedger(r.Context(), p, matter)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": rows,
		"page": map[string]any{"limit": len(rows), "next_cursor": nil},
	})
}

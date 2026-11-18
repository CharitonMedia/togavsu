package modules

import (
	"io"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func scopeDenied(scope string) error {
	return &domain.APIError{Code: "scope_denied", Message: "this token lacks the " + scope + " scope"}
}

// listContractProviders is GET /api/v1/modules/contracts/{contract}/providers (CORE-31).
// Returns provider module ids the caller is linked to — never addresses.
func (h *Handler) listContractProviders(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer && !hasScope(c.Scopes, "contracts:call") {
		writeErr(w, scopeDenied("contracts:call"))
		return
	}
	if !c.Bearer || c.Principal.ModuleID == "" {
		writeErr(w, domain.Forbidden("only a module token may list linked providers"))
		return
	}
	list, err := h.Svc.ListLinkedProviders(r.Context(), c.Principal, r.PathValue("contract"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "page": map[string]any{"limit": len(list) + 1, "next_cursor": nil}})
}

// callContract is POST /api/v1/modules/contracts/{contract}/{operation} (CORE-31).
func (h *Handler) callContract(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer && !hasScope(c.Scopes, "contracts:call") {
		writeErr(w, scopeDenied("contracts:call"))
		return
	}
	if !c.Bearer || c.Principal.ModuleID == "" {
		writeErr(w, domain.Forbidden("only a module token may call a contract"))
		return
	}
	maxBytes, err := h.Svc.intSetting(r.Context(), contractCallMaxBytesKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if maxBytes <= 0 {
		maxBytes = domain.ContractCallMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(maxBytes)+1))
	if err != nil {
		writeErr(w, domain.Invalid("body", "could not read the request body"))
		return
	}
	if len(body) > maxBytes {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"code":"payload_too_large","message":"request body exceeds modules.contract_call_max_bytes"}`))
		return
	}
	status, header, resp, err := h.Svc.CallContract(r.Context(), c.Principal,
		r.PathValue("contract"), r.PathValue("operation"), body, r.Header.Get(ContextHeader), r.Header.Get(ContractProviderHeader))
	if err != nil {
		writeErr(w, err)
		return
	}
	for k, vs := range header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp)
}

// pushContractEvent is POST /api/v1/modules/contracts/{contract}/events (CORE-31).
func (h *Handler) pushContractEvent(w http.ResponseWriter, r *http.Request, c Caller) {
	if c.Bearer && !hasScope(c.Scopes, "contracts:provide") {
		writeErr(w, scopeDenied("contracts:provide"))
		return
	}
	var in ContractEventWrite
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.PushContractEvent(r.Context(), c.Principal, r.PathValue("contract"), in); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listContractLinks is GET /api/v1/modules/contract-links.
func (h *Handler) listContractLinks(w http.ResponseWriter, r *http.Request, c Caller) {
	list, err := h.Svc.ListContractLinks(r.Context(), c.Principal)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list, "page": map[string]any{"limit": len(list) + 1, "next_cursor": nil}})
}

// linkContract is POST /api/v1/modules/contract-links.
func (h *Handler) linkContract(w http.ResponseWriter, r *http.Request, c Caller) {
	var in ContractLinkWrite
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.LinkContract(r.Context(), c.Principal, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// unlinkContract is DELETE /api/v1/modules/contract-links/{link_id}.
func (h *Handler) unlinkContract(w http.ResponseWriter, r *http.Request, c Caller) {
	if err := h.Svc.UnlinkContract(r.Context(), c.Principal, r.PathValue("link_id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putBannerOrder is PUT /api/v1/modules/banner-order.
func (h *Handler) putBannerOrder(w http.ResponseWriter, r *http.Request, c Caller) {
	var in struct {
		Order []string `json:"order"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.SetStaffBannerOrder(r.Context(), c.Principal, in.Order); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

package marketplace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// Handler serves the core's marketplace routes (api/openapi/v1: listMarketplaceListings,
// getMarketplaceListing, listModuleAlert, getModuleAlert and the trusted-signing-keys
// operations). The module-alerts widget is served by the dashboard through
// WidgetSource. Every route is admin-only in the service, so the API, the MCP tools
// generated from it and the UI get the same answer.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
}

// Routes returns the mux. The core mounts it beside the other domains.
func (h *Handler) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/marketplace/listings", h.listListings)
	m.HandleFunc("GET /api/v1/marketplace/listings/{listing_id}", h.getListing)
	m.HandleFunc("GET /api/v1/module-alerts", h.listAlerts)
	m.HandleFunc("GET /api/v1/module-alerts/{alert_id}", h.getAlert)
	m.HandleFunc("GET /api/v1/trusted-signing-keys", h.listKeys)
	m.HandleFunc("POST /api/v1/trusted-signing-keys", h.createKey)
	m.HandleFunc("GET /api/v1/trusted-signing-keys/{key_id}", h.getKey)
	m.HandleFunc("PATCH /api/v1/trusted-signing-keys/{key_id}", h.patchKey)
	m.HandleFunc("DELETE /api/v1/trusted-signing-keys/{key_id}", h.deleteKey)
	return m
}

func (h *Handler) principal(r *http.Request) (domain.Principal, error) {
	if h.Principal == nil {
		return domain.Principal{}, domain.Unauthenticated("sign in")
	}
	p, err := h.Principal(r)
	if err != nil {
		return domain.Principal{}, err
	}
	if p.Source == domain.SourceSystem {
		return domain.Principal{}, domain.Unauthenticated("sign in")
	}
	return p, nil
}

// writeCaller authenticates a write. A browser session must echo the CSRF cookie in
// X-CSRF-Token; a bearer call without cookies need not.
func (h *Handler) writeCaller(r *http.Request) (domain.Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		return p, err
	}
	if r.Header.Get("Cookie") != "" {
		c, err := r.Cookie(practice.CSRFCookie)
		if err != nil || c.Value == "" || r.Header.Get("X-CSRF-Token") != c.Value {
			return domain.Principal{}, domain.Forbidden("csrf")
		}
	}
	return p, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(api.HTTPStatus())
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	var me *Error
	if errors.As(err, &me) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(me.HTTPStatus())
		_ = json.NewEncoder(w).Encode(me)
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.APIError{Code: "internal", Message: "internal error"})
}

var (
	positiveInt = regexp.MustCompile(`^[1-9][0-9]*$`)
	cursorText  = regexp.MustCompile(`^o:(0|[1-9][0-9]*)$`)
)

func (h *Handler) settingInt(ctx context.Context, key string) (int, error) {
	v, err := h.Svc.d.Settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// page applies the contract's cursor and limit to rows already filtered by the service.
// The page size is api.page_size, capped by api.max_page_size, read on every request.
func page[T any](h *Handler, r *http.Request, rows []T) ([]T, map[string]any, error) {
	size, err := h.settingInt(r.Context(), "api.page_size")
	if err != nil {
		return nil, nil, err
	}
	most, err := h.settingInt(r.Context(), "api.max_page_size")
	if err != nil {
		return nil, nil, err
	}
	limit := min(size, most)
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || !positiveInt.MatchString(v) {
			return nil, nil, domain.Invalid("limit", "limit must be a positive integer")
		}
		if n > most {
			return nil, nil, domain.Invalid("limit", "limit must not exceed api.max_page_size ("+strconv.Itoa(most)+")")
		}
		limit = n
	}
	var off int
	if c := q.Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		m := cursorText.FindStringSubmatch(string(raw))
		if err != nil || m == nil {
			return nil, nil, domain.Invalid("cursor", "cursor is not valid")
		}
		if off, err = strconv.Atoi(m[len(m)-1]); err != nil || off > len(rows) {
			return nil, nil, domain.Invalid("cursor", "cursor is not valid")
		}
	}
	end := min(off+limit, len(rows))
	next := ""
	if end < len(rows) {
		next = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(end)))
	}
	return rows[off:end], map[string]any{"next_cursor": next}, nil
}

func listingJSON(l Listing) map[string]any {
	hist := make([]map[string]any, 0, len(l.VersionHistory))
	for _, v := range l.VersionHistory {
		hist = append(hist, map[string]any{"version": v.Version, "released_at": v.ReleasedAt.UTC().Format(time.RFC3339)})
	}
	perms := l.Permissions
	if perms == nil {
		perms = []string{}
	}
	out := map[string]any{
		"id": l.ID, "name": l.Name, "author": l.Author, "version": l.Version, "signing_key": l.SigningKey,
		"permissions": perms, "version_history": hist, "price_note": l.PriceNote,
		"flagged": l.Flagged, "pulled": l.Pulled, "notice": Notice,
	}
	if l.Flagged {
		out["flag_note"] = l.FlagNote
	}
	return out
}

func (h *Handler) listListings(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	ls, err := h.Svc.Listings(r.Context(), p, r.URL.Query().Get("q"))
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h, r, ls)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, l := range items {
		data = append(data, listingJSON(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *Handler) getListing(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	l, err := h.Svc.Listing(r.Context(), p, r.PathValue("listing_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listingJSON(l))
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// alertJSON is the contract's ModuleAlert plus the warning text, the version and the
// installed version (contract extensions proposed in the PR).
func alertJSON(a ModuleAlert) map[string]any {
	return map[string]any{
		"id": a.ID, "created_at": ts(a.CreatedAt), "created_by": a.CreatedBy, "updated_at": ts(a.UpdatedAt),
		"module_id": a.ModuleID, "kind": a.Kind, "note": a.Note,
		"version": a.Version, "installed_version": a.InstalledVersion, "warning": a.Warning, "raised_at": ts(a.RaisedAt),
	}
}

// listAlerts lists delivered alerts. check=true is the modules screen opening: the core
// asks the marketplace first (PRD 10.5).
func (h *Handler) listAlerts(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var as []ModuleAlert
	var st CheckStatus
	switch r.URL.Query().Get("check") {
	case "true", "1":
		as, st, err = h.Svc.OpenModulesScreen(r.Context(), p)
	case "", "false", "0":
		if as, err = h.Svc.Alerts(r.Context(), p); err == nil {
			st, err = h.Svc.Status(r.Context(), p)
		}
	default:
		err = domain.Invalid("check", "check is true or false")
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h, r, as)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, a := range items {
		data = append(data, alertJSON(a))
	}
	check := map[string]any{"last_error": st.LastError}
	if !st.AttemptedAt.IsZero() {
		check["attempted_at"] = ts(st.AttemptedAt)
	}
	if !st.SucceededAt.IsZero() {
		check["succeeded_at"] = ts(st.SucceededAt)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg, "check": check})
}

func pathID(r *http.Request, name string) (domain.ID, error) {
	id, err := domain.ParseID(r.PathValue(name))
	if err != nil {
		return "", domain.NotFound(name)
	}
	return id, nil
}

func (h *Handler) getAlert(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := pathID(r, "alert_id")
	if err == nil {
		var a ModuleAlert
		if a, err = h.Svc.Alert(r.Context(), p, id); err == nil {
			writeJSON(w, http.StatusOK, alertJSON(a))
			return
		}
	}
	writeErr(w, err)
}

func keyJSON(k LocalKey) map[string]any {
	return map[string]any{
		"id": k.ID, "created_at": ts(k.CreatedAt), "created_by": k.CreatedBy, "updated_at": ts(k.UpdatedAt),
		"name": k.Name, "public_key_pem": k.PublicKeyPEM, "key_id": k.KeyID,
	}
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	ks, err := h.Svc.Keys(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h, r, ks)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, k := range items {
		data = append(data, keyJSON(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

type keyBody struct {
	Name         *string `json:"name"`
	PublicKeyPEM *string `json:"public_key_pem"`
}

func decodeKey(r *http.Request) (KeyInput, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, fixed.MaxManifestBytes))
	if err != nil {
		return KeyInput{}, domain.Invalid("body", "request body is too large or unreadable")
	}
	var b keyBody
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return KeyInput{}, domain.Invalid("body", "request body must be a JSON object with name and public_key_pem")
	}
	return KeyInput{Name: b.Name, PublicKeyPEM: b.PublicKeyPEM}, nil
}

func (h *Handler) createKey(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decodeKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	k, err := h.Svc.CreateKey(r.Context(), p, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, keyJSON(k))
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := pathID(r, "key_id")
	if err == nil {
		var k LocalKey
		if k, err = h.Svc.Key(r.Context(), p, id); err == nil {
			writeJSON(w, http.StatusOK, keyJSON(k))
			return
		}
	}
	writeErr(w, err)
}

func (h *Handler) patchKey(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := pathID(r, "key_id")
	if err != nil {
		writeErr(w, err)
		return
	}
	in, err := decodeKey(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	k, err := h.Svc.UpdateKey(r.Context(), p, id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keyJSON(k))
}

func (h *Handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id, err := pathID(r, "key_id")
	if err == nil {
		if err = h.Svc.DeleteKey(r.Context(), p, id); err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeErr(w, err)
}

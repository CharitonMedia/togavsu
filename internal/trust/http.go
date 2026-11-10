package trust

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves the trust JSON API (the /api/v1 contract routes) and the trust pages.
// Principal authenticates the caller (the core's session or bearer check, including
// token scopes and CSRF). Every access decision is the service's.
//
// Scopes checks bearer token scopes the route table cannot: postTrustEntry and its bulk
// form need a scope per entry_type (the contract's entry_type_scopes), so the core must
// mount those two routes without a static scope list and wire Scopes to the caller's
// RequireScopes (a session passes, a token must hold every scope named). Nil means the
// core checked scopes before the handler, which is only correct for session callers.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
	Scopes    func(r *http.Request, need ...string) error
	FS        fs.FS
	Templates *template.Template
}

// EntryTypeScopes is the contract's entry_type_scopes for postTrustEntry and
// bulkPostTrustEntries: a deposit needs trust:write, every debit or adjustment
// trust:withdraw. A trust:write-only token is scope_denied on the other four.
var EntryTypeScopes = map[string]string{
	TypeDeposit:    "trust:write",
	TypeWithdrawal: "trust:withdraw",
	TypeTransfer:   "trust:withdraw",
	TypeRefund:     "trust:withdraw",
	TypeAdjustment: "trust:withdraw",
}

// entryScopes checks the bearer scopes for every item before anything posts, so a
// batch with one scope-denied item posts nothing. An unknown entry_type needs no scope
// here; the service rejects it as a validation error.
func (h *Handler) entryScopes(r *http.Request, items ...PostInput) error {
	if h.Scopes == nil {
		return nil
	}
	seen := map[string]bool{}
	var need []string
	for _, in := range items {
		if sc, ok := EntryTypeScopes[in.EntryType]; ok && !seen[sc] {
			seen[sc] = true
			need = append(need, sc)
		}
	}
	if len(need) == 0 {
		return nil
	}
	err := h.Scopes(r, need...)
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == "scope_denied" {
		// identity's scope_denied is a 403 on the shared error table.
		return domain.Forbidden(api.Message)
	}
	return err
}

// NewHandler parses the trust templates from fsys (web/templates/trust). fsys may be nil
// for a JSON-only handler.
func NewHandler(svc *Service, fsys fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Principal: principal, FS: fsys}
	if fsys != nil {
		t, err := template.New("trust").Funcs(funcs).ParseFS(fsys, "*.html")
		if err != nil {
			return nil, err
		}
		h.Templates = t
	}
	return h, nil
}

// Routes returns the trust mux. There is no PUT, PATCH or DELETE on the ledger: the
// router answers 405 for them.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/trust-accounts", h.listAccounts)
	mux.HandleFunc("POST /api/v1/trust-accounts", h.createAccount)
	mux.HandleFunc("GET /api/v1/trust-accounts/{trust_account_id}", h.getAccount)
	mux.HandleFunc("PATCH /api/v1/trust-accounts/{trust_account_id}", h.patchAccount)
	mux.HandleFunc("GET /api/v1/trust-ledger", h.listEntries)
	mux.HandleFunc("POST /api/v1/trust-ledger", h.postEntry)
	mux.HandleFunc("POST /api/v1/trust-ledger:bulk", h.bulkEntries)
	mux.HandleFunc("GET /api/v1/trust-ledger/{trust_entry_id}", h.getEntry)
	mux.HandleFunc("POST /api/v1/trust-ledger/{trust_entry_id}/reversals", h.reverseEntry)
	mux.HandleFunc("GET /api/v1/trust/balances", h.balances)
	mux.HandleFunc("GET /api/v1/trust-flags", h.listFlags)
	mux.HandleFunc("GET /api/v1/trust-flags/{trust_flag_id}", h.getFlag)
	mux.HandleFunc("POST /api/v1/trust-flags/{trust_flag_id}/resolve", h.resolveFlag)
	mux.HandleFunc("GET /api/v1/trust-replenishment-requests", h.listRepl)
	mux.HandleFunc("POST /api/v1/trust-replenishment-requests", h.createRepl)
	mux.HandleFunc("POST /api/v1/trust-replenishment-requests:bulk", h.bulkRepl)
	mux.HandleFunc("GET /api/v1/trust-replenishment-requests/{replenishment_id}", h.getRepl)
	mux.HandleFunc("POST /api/v1/trust-replenishment-requests/{replenishment_id}/cancel", h.cancelRepl)
	mux.HandleFunc("POST /api/v1/trust-replenishment-requests/{replenishment_id}/satisfy", h.satisfyRepl)
	mux.HandleFunc("GET /api/v1/trust-reconciliations", h.listRecon)
	mux.HandleFunc("POST /api/v1/trust-reconciliations", h.createRecon)
	mux.HandleFunc("POST /api/v1/trust-reconciliations:bulk", h.bulkRecon)
	mux.HandleFunc("GET /api/v1/trust-reconciliations/{reconciliation_id}", h.getRecon)
	mux.HandleFunc("GET /api/v1/reports/trust-balances", h.reportBalances)
	mux.HandleFunc("GET /api/v1/reports/trust-client-ledger", h.reportClient)
	mux.HandleFunc("GET /api/v1/reports/trust-account-ledger", h.reportAccount)
	mux.HandleFunc("GET /api/v1/reports/reconciliations", h.reportRecon)
	mux.HandleFunc("GET /api/v1/widgets/trust-alerts", h.widgetAlerts)
	mux.HandleFunc("GET /api/v1/widgets/replenishment-requests", h.widgetRepl)
	mux.HandleFunc("GET /api/v1/notifications", h.listNotifications)
	mux.HandleFunc("POST /api/v1/notifications/{notification_id}/read", h.readNotification)
	mux.HandleFunc("POST /api/v1/notifications:read-all", h.readAll)
	h.pageRoutes(mux)
	return mux
}

func (h *Handler) who(w http.ResponseWriter, r *http.Request) (domain.Principal, bool) {
	if h.Principal == nil {
		writeErr(w, domain.Unauthenticated("sign in is required"))
		return domain.Principal{}, false
	}
	p, err := h.Principal(r)
	if err != nil {
		writeErr(w, err)
		return domain.Principal{}, false
	}
	return p, true
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
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(domain.APIError{Code: "internal", Message: "internal error"})
}

// decode reads a JSON body and rejects unknown fields (the contract's
// additionalProperties false).
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "the body is not valid JSON for this operation: "+err.Error())
	}
	return nil
}

type pageJSON struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

func listJSON[T any](w http.ResponseWriter, p Page[T]) {
	pj := pageJSON{Limit: p.Limit}
	if p.NextCursor != "" {
		c := p.NextCursor
		pj.NextCursor = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": p.Items, "page": pj})
}

func limitOf(r *http.Request) (int, error) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || !positive(int64(n)) {
		return 0, domain.Invalid("limit", "limit must be a positive integer")
	}
	return n, nil
}

func optBool(r *http.Request, key string) (*bool, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, domain.Invalid(key, key+" must be true or false")
	}
	return &b, nil
}

func idKey(r *http.Request) string { return r.Header.Get("Idempotency-Key") }

func pathID(r *http.Request, name string) domain.ID { return domain.ID(r.PathValue(name)) }

func qID(r *http.Request, name string) domain.ID { return domain.ID(r.URL.Query().Get(name)) }

// Accounts.

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := limitOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	active, err := optBool(r, "active")
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.Svc.Accounts(r.Context(), p, active, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in AccountInput
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a, err := h.Svc.CreateAccount(r.Context(), p, in, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	a, err := h.Svc.Account(r.Context(), p, pathID(r, "trust_account_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) patchAccount(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in AccountPatch
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	a, err := h.Svc.UpdateAccount(r.Context(), p, pathID(r, "trust_account_id"), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Ledger.

func entryFilter(r *http.Request) (EntryFilter, error) {
	limit, err := limitOf(r)
	q := r.URL.Query()
	return EntryFilter{Cursor: q.Get("cursor"), Limit: limit, MatterID: qID(r, "matter_id"), ClientID: qID(r, "client_id"),
		TrustAccountID: qID(r, "trust_account_id"), FromDate: q.Get("from_date"), ToDate: q.Get("to_date")}, err
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	f, err := entryFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.Svc.Entries(r.Context(), p, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) postEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in PostInput
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.entryScopes(r, in); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.Svc.Post(r.Context(), p, in, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) bulkEntries(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []PostInput `json:"items"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	if err := h.entryScopes(r, in.Items...); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.Svc.PostBulk(r.Context(), p, in.Items, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": res})
}

func (h *Handler) getEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	e, err := h.Svc.Entry(r.Context(), p, pathID(r, "trust_entry_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (h *Handler) reverseEntry(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in ReverseInput
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.Svc.Reverse(r.Context(), p, pathID(r, "trust_entry_id"), in, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (h *Handler) balances(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	f, err := entryFilter(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.Balances(r.Context(), p, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit, err := h.Svc.pageLimit(r.Context(), f.Limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Balance rows have no id; the cursor is the row's position.
	start, _ := strconv.Atoi(f.Cursor)
	if negative(int64(start)) || start > len(rows) {
		start = len(rows)
	}
	page := Page[BalanceRow]{Limit: limit, Items: rows[start:]}
	if len(page.Items) > limit {
		page.Items, page.NextCursor = page.Items[:limit], strconv.Itoa(start+limit)
	}
	if page.Items == nil {
		page.Items = []BalanceRow{}
	}
	listJSON(w, page)
}

// Flags.

func (h *Handler) listFlags(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := limitOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	page, err := h.Svc.Flags(r.Context(), p, FlagFilter{Status: q.Get("status"), MatterID: qID(r, "matter_id"), TrustAccountID: qID(r, "trust_account_id"),
		Scope: q.Get("flag_scope"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) getFlag(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	f, err := h.Svc.Flag(r.Context(), p, pathID(r, "trust_flag_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (h *Handler) resolveFlag(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	f, err := h.Svc.ResolveFlag(r.Context(), p, pathID(r, "trust_flag_id"), in.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// Replenishment.

func (h *Handler) listRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := limitOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	if s := q.Get("status"); s != "" && s != ReplenishActive && s != ReplenishSatisfied && s != ReplenishCancelled {
		writeErr(w, domain.Invalid("status", "status is active, satisfied or cancelled"))
		return
	}
	page, err := h.Svc.Replenishments(r.Context(), p, qID(r, "matter_id"), q.Get("status"), q.Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) createRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in ReplenishmentInput
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.CreateReplenishment(r.Context(), p, in, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) bulkRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []ReplenishmentInput `json:"items"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.CreateReplenishments(r.Context(), p, in.Items, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (h *Handler) getRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.Replenishment(r.Context(), p, pathID(r, "replenishment_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) cancelRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.CancelReplenishment(r.Context(), p, pathID(r, "replenishment_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) satisfyRepl(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.SatisfyReplenishment(r.Context(), p, pathID(r, "replenishment_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Reconciliation.

func (h *Handler) listRecon(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := limitOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.Svc.Reconciliations(r.Context(), p, qID(r, "trust_account_id"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) createRecon(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in ReconcileInput
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.Reconcile(r.Context(), p, in, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) bulkRecon(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	var in struct {
		Items []ReconcileInput `json:"items"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	out, err := h.Svc.ReconcileBulk(r.Context(), p, in.Items, idKey(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (h *Handler) getRecon(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	out, err := h.Svc.Reconciliation(r.Context(), p, pathID(r, "reconciliation_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// Reports and widgets.

func writeReport(w http.ResponseWriter, r *http.Request, rep Report, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	switch r.URL.Query().Get("format") {
	case "", "json":
		writeJSON(w, http.StatusOK, rep)
	case "csv":
		b, err := rep.CSV()
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+rep.Report+`.csv"`)
		_, _ = w.Write(b)
	default:
		writeErr(w, domain.Invalid("format", "format is json or csv"))
	}
}

func (h *Handler) reportBalances(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		rep, err := h.Svc.BalancesReport(r.Context(), p, qID(r, "trust_account_id"))
		writeReport(w, r, rep, err)
	}
}

func (h *Handler) reportClient(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		rep, err := h.Svc.ClientLedger(r.Context(), p, qID(r, "client_id"), qID(r, "matter_id"))
		writeReport(w, r, rep, err)
	}
}

func (h *Handler) reportAccount(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		q := r.URL.Query()
		rep, err := h.Svc.AccountLedger(r.Context(), p, qID(r, "trust_account_id"), q.Get("from_date"), q.Get("to_date"))
		writeReport(w, r, rep, err)
	}
}

func (h *Handler) reportRecon(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		rep, err := h.Svc.ReconciliationReport(r.Context(), p, qID(r, "trust_account_id"))
		writeReport(w, r, rep, err)
	}
}

func (h *Handler) widgetAlerts(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		out, err := h.Svc.TrustAlertsWidget(r.Context(), p, qID(r, "matter_id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (h *Handler) widgetRepl(w http.ResponseWriter, r *http.Request) {
	if p, ok := h.who(w, r); ok {
		out, err := h.Svc.ReplenishmentWidget(r.Context(), p, qID(r, "matter_id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// Notifications.

func (h *Handler) listNotifications(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	limit, err := limitOf(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	read, err := optBool(r, "read")
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.Svc.Notifications(r.Context(), p, read, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	listJSON(w, page)
}

func (h *Handler) readNotification(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	n, err := h.Svc.MarkRead(r.Context(), p, pathID(r, "notification_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (h *Handler) readAll(w http.ResponseWriter, r *http.Request) {
	p, ok := h.who(w, r)
	if !ok {
		return
	}
	if _, err := h.Svc.MarkAllRead(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

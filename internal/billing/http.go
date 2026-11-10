package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Handler serves the billing JSON API under /api/v1 and the billing screens under /billing.
// Principal is the core's authenticated caller; the core has already checked token scopes and
// session CSRF. Every access decision is made again in the Service.
type Handler struct {
	Svc       *Service
	Principal func(*http.Request) (domain.Principal, error)
	Templates *template.Template
	FS        fs.FS
}

// NewHandler parses the billing templates in fsys. fsys may be nil for an API-only handler.
func NewHandler(svc *Service, fsys fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, Principal: principal, FS: fsys}
	if fsys != nil {
		set, err := template.New("billing").Funcs(funcs).ParseFS(fsys, "*.html")
		if err != nil {
			return nil, err
		}
		h.Templates = set
	}
	return h, nil
}

// Routes returns the billing mux.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, fn func(http.ResponseWriter, *http.Request, domain.Principal)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, err := h.principal(r)
			if err != nil {
				writeErr(w, err)
				return
			}
			fn(w, r, p)
		})
	}
	api("GET /api/v1/fee-items", h.listFeeItems)
	api("POST /api/v1/fee-items", h.createFeeItem)
	api("GET /api/v1/fee-items/{fee_item_id}", h.getFeeItem)
	api("PATCH /api/v1/fee-items/{fee_item_id}", h.patchFeeItem)
	for _, t := range []struct {
		path, param string
		expense     bool
	}{{"/api/v1/activity-types", "activity_type_id", false}, {"/api/v1/expense-types", "expense_type_id", true}} {
		t := t
		api("GET "+t.path, func(w http.ResponseWriter, r *http.Request, p domain.Principal) { h.listTypes(w, r, p, t.expense) })
		api("POST "+t.path, func(w http.ResponseWriter, r *http.Request, p domain.Principal) { h.createType(w, r, p, t.expense) })
		api("GET "+t.path+"/{"+t.param+"}", func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
			row, err := h.Svc.Type(r.Context(), p, t.expense, domain.ID(r.PathValue(t.param)))
			respond(w, http.StatusOK, typeView(row), err)
		})
		api("PATCH "+t.path+"/{"+t.param+"}", func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
			h.patchType(w, r, p, t.expense, t.param)
		})
	}
	api("GET /api/v1/time-entries", h.listTime)
	api("POST /api/v1/time-entries", h.createTime)
	api("POST /api/v1/time-entries:bulk", h.bulkTime)
	api("GET /api/v1/time-entries/{time_entry_id}", h.getTime)
	api("PATCH /api/v1/time-entries/{time_entry_id}", h.patchTime)
	api("GET /api/v1/time-entries/{time_entry_id}/history", h.history("time_entry", "time_entry_id"))
	api("GET /api/v1/timers", h.listTimers)
	api("POST /api/v1/timers", h.startTimer)
	api("GET /api/v1/timers/{timer_id}", h.getTimer)
	api("POST /api/v1/timers/{timer_id}/pause", h.moveTimer(h.Svc.PauseTimer))
	api("POST /api/v1/timers/{timer_id}/resume", h.moveTimer(h.Svc.ResumeTimer))
	api("POST /api/v1/timers/{timer_id}/stop", h.stopTimer)
	api("GET /api/v1/widgets/running-timer", h.runningTimerWidget)
	api("GET /api/v1/expenses", h.listExpenses)
	api("POST /api/v1/expenses", h.createExpense)
	api("POST /api/v1/expenses:bulk", h.bulkExpenses)
	api("GET /api/v1/expenses/{expense_id}", h.getExpense)
	api("PATCH /api/v1/expenses/{expense_id}", h.patchExpense)
	api("GET /api/v1/expenses/{expense_id}/history", h.history("expense", "expense_id"))
	api("GET /api/v1/invoices", h.listInvoices)
	api("POST /api/v1/invoice-drafts", h.draftInvoices)
	api("GET /api/v1/invoices/{invoice_id}", h.getInvoice)
	api("PATCH /api/v1/invoices/{invoice_id}", h.patchInvoice)
	api("GET /api/v1/invoices/{invoice_id}/history", h.history("invoice", "invoice_id"))
	api("PUT /api/v1/invoices/{invoice_id}/draft", h.editDraft)
	api("POST /api/v1/invoices/{invoice_id}/approve", h.approve)
	api("POST /api/v1/invoices/{invoice_id}/send", h.send)
	api("GET /api/v1/invoices/{invoice_id}/pdf", h.pdf)
	api("POST /api/v1/invoices/{invoice_id}/void", h.void)
	api("POST /api/v1/invoices/{invoice_id}/apply-trust", h.applyTrust)
	api("POST /api/v1/invoices/{invoice_id}/write-off", h.writeOff)
	api("POST /api/v1/invoices/{invoice_id}/replenishment-requests", h.replenish)
	api("GET /api/v1/payments", h.listPayments)
	api("POST /api/v1/payments", h.createPayment)
	api("POST /api/v1/payments:bulk", h.bulkPayments)
	api("GET /api/v1/payments/{payment_id}", h.getPayment)
	api("POST /api/v1/payments/{payment_id}/status", h.paymentStatus)
	api("POST /api/v1/payments/{payment_id}/reverse", h.reversePayment)
	api("GET /api/v1/billing/worklist", h.worklist)
	api("POST /api/v1/billing/flat-fees/{matter_id}/apply", h.applyFlatFee)
	for name, fn := range map[string]func(context.Context, domain.Principal, ReportFilter) (Report, error){
		"unbilled-time": h.Svc.UnbilledReport, "time-by-user": h.Svc.TimeByUserReport, "ar-aging": h.Svc.ARAgingReport,
		"payments-received": h.Svc.PaymentsReport, "write-offs": h.Svc.WriteOffsReport,
	} {
		fn := fn
		api("GET /api/v1/reports/"+name, func(w http.ResponseWriter, r *http.Request, p domain.Principal) { h.report(w, r, p, fn) })
	}
	h.pageRoutes(mux)
	return mux
}

func (h *Handler) principal(r *http.Request) (domain.Principal, error) {
	if h.Principal == nil {
		return domain.Principal{}, domain.Unauthenticated("sign in required")
	}
	return h.Principal(r)
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(api.HTTPStatus())
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.APIError{Code: "internal", Message: "internal error"})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func respond(w http.ResponseWriter, code int, body any, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, code, body)
}

// readBody reads at most 1 MiB and decodes it strictly into dst.
func readBody(r *http.Request, dst any) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil {
		return nil, domain.Invalid("body", "too large or unreadable")
	}
	if dst == nil {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, domain.Invalid("body", "invalid JSON: "+err.Error())
	}
	return raw, nil
}

type idemRecord struct {
	Fingerprint string          `json:"fingerprint"`
	Status      int             `json:"status"`
	Body        json.RawMessage `json:"body"`
}

// write decodes the body into dst and runs fn once per Idempotency-Key: a replay with the same
// body returns the stored result; another body with that key is a conflict and writes nothing.
// ponytail: ceiling: the stored result is written after the business commit, so two concurrent
// first requests with one key can both run. Upgrade path: claim the key inside the write
// transaction. Trigger: a module or importer that retries in parallel.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, p domain.Principal, dst any, fn func() (int, any, error)) {
	raw, err := readBody(r, dst)
	if err != nil {
		writeErr(w, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		code, body, err := fn()
		respond(w, code, body, err)
		return
	}
	sumKey := sha256.Sum256([]byte(string(p.UserID) + "|" + string(p.TokenID) + "|" + r.Method + "|" + r.URL.Path + "|" + key))
	id := hex.EncodeToString(sumKey[:])
	sumBody := sha256.Sum256(raw)
	fp := hex.EncodeToString(sumBody[:])
	var rec idemRecord
	if err := h.Svc.d.Store.Get(r.Context(), kindIdem, id, &rec); err == nil {
		if rec.Fingerprint != fp {
			writeJSON(w, http.StatusConflict, domain.APIError{Code: "idempotency_conflict", Message: "this Idempotency-Key was used with a different body"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replay", "true")
		w.WriteHeader(rec.Status)
		_, _ = w.Write(rec.Body)
		return
	} else if !errors.Is(err, ErrNotFound) {
		writeErr(w, err)
		return
	}
	code, body, err := fn()
	if err != nil {
		writeErr(w, err)
		return
	}
	enc, err := json.Marshal(body)
	if err != nil {
		writeErr(w, err)
		return
	}
	_ = h.Svc.d.Store.Put(context.WithoutCancel(r.Context()), kindIdem, id, idemRecord{Fingerprint: fp, Status: code, Body: enc})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(append(enc, '\n'))
}

// pageOf slices items by the opaque cursor and limit. The cap is api.max_page_size.
func (h *Handler) pageOf(r *http.Request, n int) (from, to int, next *string, limit int, err error) {
	ctx := r.Context()
	maxSize := h.Svc.settingInt(ctx, "api.max_page_size")
	limit = h.Svc.settingInt(ctx, "api.page_size")
	if limit > maxSize {
		limit = maxSize
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		l, convErr := strconv.Atoi(v)
		if convErr != nil || l < 1 {
			return 0, 0, nil, 0, domain.Invalid("limit", "must be a positive integer")
		}
		if l > maxSize {
			return 0, 0, nil, 0, domain.Invalid("limit", "is above api.max_page_size")
		}
		limit = l
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		raw, decErr := base64.RawURLEncoding.DecodeString(c)
		off, convErr := strconv.Atoi(strings.TrimPrefix(string(raw), "o:"))
		if decErr != nil || convErr != nil || off < 0 || !strings.HasPrefix(string(raw), "o:") {
			return 0, 0, nil, 0, domain.Invalid("cursor", "is not a cursor from this list")
		}
		from = off
	}
	if from > n {
		from = n
	}
	to = from + limit
	if to < n {
		c := base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(to)))
		next = &c
	} else {
		to = n
	}
	return from, to, next, limit, nil
}

// list writes a page of JSON items, or every row as CSV when format=csv.
func (h *Handler) list(w http.ResponseWriter, r *http.Request, name string, items []any, err error) {
	if err != nil {
		writeErr(w, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		writeCSV(w, name, items)
		return
	}
	from, to, next, limit, err := h.pageOf(r, len(items))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items[from:to], "page": map[string]any{"limit": limit, "next_cursor": next}})
}

func writeCSV(w http.ResponseWriter, name string, items []any) {
	rep := Report{Report: name, Rows: []map[string]any{}}
	for _, it := range items {
		b, _ := json.Marshal(it)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k, v := range m {
			switch x := v.(type) {
			case []any, map[string]any:
				enc, _ := json.Marshal(x)
				m[k] = string(enc)
			case float64:
				m[k] = strconv.FormatFloat(x, 'f', -1, 64)
			}
		}
		if rep.Columns == nil {
			for k := range m {
				rep.Columns = append(rep.Columns, k)
			}
			sort.Strings(rep.Columns)
		}
		rep.Rows = append(rep.Rows, m)
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.csv"`)
	_ = rep.WriteCSV(w)
}

func q(r *http.Request, k string) string { return r.URL.Query().Get(k) }

func qBool(r *http.Request, k string) (*bool, error) {
	v := q(r, k)
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, domain.Invalid(k, "must be true or false")
	}
	return &b, nil
}

func anys[T any, V any](rows []T, view func(T) V) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, view(r))
	}
	return out
}

// Fee items and types.

type feeItemBody struct {
	Name               *string       `json:"name"`
	Description        *string       `json:"description"`
	DefaultAmountCents *domain.Cents `json:"default_amount_cents"`
	PracticeAreaID     *domain.ID    `json:"practice_area_id"`
	Active             *bool         `json:"active"`
}

func (b feeItemBody) input() FeeItemInput {
	return FeeItemInput{b.Name, b.Description, b.DefaultAmountCents, b.PracticeAreaID, b.Active}
}

func (h *Handler) listFeeItems(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	active, err := qBool(r, "active")
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.FeeItems(r.Context(), p, active)
	h.list(w, r, "fee-items", anys(rows, feeItemView), err)
}

func (h *Handler) createFeeItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body feeItemBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.CreateFeeItem(r.Context(), p, body.input())
		return http.StatusCreated, feeItemView(row), err
	})
}

func (h *Handler) getFeeItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.FeeItem(r.Context(), p, domain.ID(r.PathValue("fee_item_id")))
	respond(w, http.StatusOK, feeItemView(row), err)
}

func (h *Handler) patchFeeItem(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body feeItemBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.UpdateFeeItem(r.Context(), p, domain.ID(r.PathValue("fee_item_id")), body.input())
		return http.StatusOK, feeItemView(row), err
	})
}

type typeBody struct {
	Name   *string `json:"name"`
	Active *bool   `json:"active"`
}

func (h *Handler) listTypes(w http.ResponseWriter, r *http.Request, p domain.Principal, expense bool) {
	active, err := qBool(r, "active")
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.Types(r.Context(), p, expense, active)
	h.list(w, r, "types", anys(rows, typeView), err)
}

func (h *Handler) createType(w http.ResponseWriter, r *http.Request, p domain.Principal, expense bool) {
	var body typeBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.CreateType(r.Context(), p, expense, TypeInput{body.Name, body.Active})
		return http.StatusCreated, typeView(row), err
	})
}

func (h *Handler) patchType(w http.ResponseWriter, r *http.Request, p domain.Principal, expense bool, param string) {
	var body typeBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.UpdateType(r.Context(), p, expense, domain.ID(r.PathValue(param)), TypeInput{body.Name, body.Active})
		return http.StatusOK, typeView(row), err
	})
}

// Time entries.

type timeBody struct {
	MatterID       *domain.ID       `json:"matter_id"`
	UserID         *domain.ID       `json:"user_id"`
	WorkedOn       *string          `json:"worked_on"`
	Increments     *int             `json:"increments"`
	Description    *string          `json:"description"`
	ActivityTypeID *domain.ID       `json:"activity_type_id"`
	Billable       *bool            `json:"billable"`
	RateCents      *domain.Cents    `json:"rate_cents"`
	Source         *string          `json:"source"`
	CustomFields   []map[string]any `json:"custom_fields"`
}

// errCustomPending is returned while custom fields on time entries and expenses are not wired.
var errCustomPending = domain.Invalid("custom_fields", "custom fields on time entries and expenses are not available yet")

func (b timeBody) input() (TimeInput, error) {
	if len(b.CustomFields) > 0 {
		return TimeInput{}, errCustomPending
	}
	return TimeInput{b.MatterID, b.UserID, b.WorkedOn, b.Increments, b.Description, b.ActivityTypeID, b.Billable, b.RateCents, b.Source}, nil
}

func (h *Handler) listTime(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	billable, err := qBool(r, "billable")
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.TimeEntries(r.Context(), p, TimeFilter{MatterID: domain.ID(q(r, "matter_id")), UserID: domain.ID(q(r, "user_id")),
		FromDate: q(r, "from_date"), ToDate: q(r, "to_date"), Billable: billable, InvoiceID: domain.ID(q(r, "invoice_id"))})
	h.list(w, r, "time-entries", anys(rows, timeView), err)
}

func (h *Handler) createTime(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body timeBody
	h.write(w, r, p, &body, func() (int, any, error) {
		in, err := body.input()
		if err != nil {
			return 0, nil, err
		}
		row, err := h.Svc.CreateTime(r.Context(), p, in)
		return http.StatusCreated, timeView(row), err
	})
}

func (h *Handler) bulkTime(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Items []timeBody `json:"items"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		items := make([]TimeInput, 0, len(body.Items))
		for _, b := range body.Items {
			in, err := b.input()
			if err != nil {
				return 0, nil, err
			}
			items = append(items, in)
		}
		rows, err := h.Svc.BulkTime(r.Context(), p, items)
		return http.StatusCreated, map[string]any{"data": anys(rows, timeView)}, err
	})
}

func (h *Handler) getTime(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.TimeEntry(r.Context(), p, domain.ID(r.PathValue("time_entry_id")))
	respond(w, http.StatusOK, timeView(row), err)
}

func (h *Handler) patchTime(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body timeBody
	h.write(w, r, p, &body, func() (int, any, error) {
		in, err := body.input()
		if err != nil {
			return 0, nil, err
		}
		row, err := h.Svc.UpdateTime(r.Context(), p, domain.ID(r.PathValue("time_entry_id")), in)
		return http.StatusOK, timeView(row), err
	})
}

func (h *Handler) history(entity, param string) func(http.ResponseWriter, *http.Request, domain.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		rows, err := h.Svc.History(r.Context(), p, entity, domain.ID(r.PathValue(param)))
		h.list(w, r, entity+"-history", anys(rows, auditView), err)
	}
}

// Timers.

func (h *Handler) listTimers(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Timers(r.Context(), p)
	h.list(w, r, "timers", anys(rows, timerView), err)
}

func (h *Handler) startTimer(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		MatterID domain.ID `json:"matter_id"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		row, created, err := h.Svc.StartTimer(r.Context(), p, body.MatterID)
		code := http.StatusCreated
		if !created {
			code = http.StatusOK
		}
		return code, timerView(row), err
	})
}

func (h *Handler) getTimer(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.Timer(r.Context(), p, domain.ID(r.PathValue("timer_id")))
	if err == nil && row.StoppedAt != nil {
		err = domain.NotFound("timer")
	}
	respond(w, http.StatusOK, timerView(row), err)
}

func (h *Handler) moveTimer(fn func(context.Context, domain.Principal, domain.ID) (Timer, error)) func(http.ResponseWriter, *http.Request, domain.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p domain.Principal) {
		h.write(w, r, p, nil, func() (int, any, error) {
			row, err := fn(r.Context(), p, domain.ID(r.PathValue("timer_id")))
			return http.StatusOK, timerView(row), err
		})
	}
}

func (h *Handler) stopTimer(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	h.write(w, r, p, nil, func() (int, any, error) {
		tm, entry, err := h.Svc.StopTimer(r.Context(), p, domain.ID(r.PathValue("timer_id")))
		if err != nil {
			return 0, nil, err
		}
		stopped := stoppedJSON{tm.ID, tm.MatterID, tm.UserID, ts(tm.StartedAt), ts(*tm.StoppedAt), tm.AccumulatedSeconds, "stopped", tm.TimeEntryID}
		return http.StatusOK, map[string]any{"timer": stopped, "time_entry": timeView(entry)}, nil
	})
}

func (h *Handler) runningTimerWidget(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Timers(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	matter := domain.ID(q(r, "matter_id"))
	out := []any{}
	for _, t := range rows {
		if !matter.IsZero() && t.MatterID != matter {
			continue
		}
		title := "Timer"
		if m, err := h.Svc.d.Practice.Matter(r.Context(), t.MatterID); err == nil {
			title = m.Number + " " + m.Name
		}
		out = append(out, map[string]any{"id": t.ID, "title": title,
			"subtitle":    t.Status() + ", " + strconv.FormatInt(t.AccumulatedSeconds/60, 10) + " min counted",
			"target_path": "/billing/time?matter_id=" + string(t.MatterID)})
	}
	query := map[string]any{}
	if !matter.IsZero() {
		query["matter_id"] = matter
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": "running-timer", "title": "Running timer",
		"target": map[string]any{"path": "/billing/time", "query": query}, "page_path": "/billing/time", "rows": out})
}

// Expenses.

type expenseBody struct {
	MatterID      *domain.ID       `json:"matter_id"`
	IncurredOn    *string          `json:"incurred_on"`
	Description   *string          `json:"description"`
	AmountCents   *domain.Cents    `json:"amount_cents"`
	ExpenseTypeID *domain.ID       `json:"expense_type_id"`
	Billable      *bool            `json:"billable"`
	MarkupPercent *int             `json:"markup_percent"`
	ReceiptFileID *domain.ID       `json:"receipt_file_id"`
	Source        *string          `json:"source"`
	CustomFields  []map[string]any `json:"custom_fields"`
}

func (b expenseBody) input() (ExpenseInput, error) {
	if len(b.CustomFields) > 0 {
		return ExpenseInput{}, errCustomPending
	}
	return ExpenseInput{b.MatterID, b.IncurredOn, b.Description, b.AmountCents, b.ExpenseTypeID, b.Billable, b.MarkupPercent, b.ReceiptFileID, b.Source}, nil
}

func (h *Handler) listExpenses(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	billable, err := qBool(r, "billable")
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.Expenses(r.Context(), p, ExpenseFilter{MatterID: domain.ID(q(r, "matter_id")), FromDate: q(r, "from_date"),
		ToDate: q(r, "to_date"), Billable: billable, InvoiceID: domain.ID(q(r, "invoice_id"))})
	h.list(w, r, "expenses", anys(rows, expenseView), err)
}

func (h *Handler) createExpense(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body expenseBody
	h.write(w, r, p, &body, func() (int, any, error) {
		in, err := body.input()
		if err != nil {
			return 0, nil, err
		}
		row, err := h.Svc.CreateExpense(r.Context(), p, in)
		return http.StatusCreated, expenseView(row), err
	})
}

func (h *Handler) bulkExpenses(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Items []expenseBody `json:"items"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		items := make([]ExpenseInput, 0, len(body.Items))
		for _, b := range body.Items {
			in, err := b.input()
			if err != nil {
				return 0, nil, err
			}
			items = append(items, in)
		}
		rows, err := h.Svc.BulkExpenses(r.Context(), p, items)
		return http.StatusCreated, map[string]any{"data": anys(rows, expenseView)}, err
	})
}

func (h *Handler) getExpense(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.Expense(r.Context(), p, domain.ID(r.PathValue("expense_id")))
	respond(w, http.StatusOK, expenseView(row), err)
}

func (h *Handler) patchExpense(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body expenseBody
	h.write(w, r, p, &body, func() (int, any, error) {
		in, err := body.input()
		if err != nil {
			return 0, nil, err
		}
		row, err := h.Svc.UpdateExpense(r.Context(), p, domain.ID(r.PathValue("expense_id")), in)
		return http.StatusOK, expenseView(row), err
	})
}

// Invoices.

func (h *Handler) listInvoices(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Invoices(r.Context(), p, InvoiceFilter{MatterID: domain.ID(q(r, "matter_id")), Status: q(r, "status"),
		FromDate: q(r, "from_date"), ToDate: q(r, "to_date")})
	client := ClientOnly(p)
	h.list(w, r, "invoices", anys(rows, func(i Invoice) any { return invoiceView(i, client) }), err)
}

func (h *Handler) getInvoice(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.Invoice(r.Context(), p, domain.ID(r.PathValue("invoice_id")))
	respond(w, http.StatusOK, invoiceView(row, ClientOnly(p)), err)
}

func (h *Handler) draftInvoices(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		MatterID  domain.ID   `json:"matter_id"`
		MatterIDs []domain.ID `json:"matter_ids"`
		FromDate  string      `json:"from_date"`
		ToDate    string      `json:"to_date"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		rows, err := h.Svc.DraftInvoices(r.Context(), p, DraftRequest(body))
		return http.StatusCreated, map[string]any{"data": anys(rows, func(i Invoice) any { return invoiceView(i, false) })}, err
	})
}

func (h *Handler) patchInvoice(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		ClientMessage *string `json:"client_message"`
		InternalNotes *string `json:"internal_notes"`
		DisputeStatus *string `json:"dispute_status"`
		DisputeNotes  *string `json:"dispute_notes"`
		IssuedOn      *string `json:"issued_on"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.UpdateInvoice(r.Context(), p, domain.ID(r.PathValue("invoice_id")), InvoicePatch(body))
		return http.StatusOK, invoiceView(row, false), err
	})
}

func (h *Handler) editDraft(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Lines []struct {
			Kind        string        `json:"kind"`
			SourceID    *domain.ID    `json:"source_id"`
			Description string        `json:"description"`
			AmountCents *domain.Cents `json:"amount_cents"`
			FeeItemID   *domain.ID    `json:"fee_item_id"`
		} `json:"lines"`
		AdjustmentCents  *domain.Cents `json:"adjustment_cents"`
		AdjustmentReason *string       `json:"adjustment_reason"`
		ClientMessage    *string       `json:"client_message"`
		IssuedOn         *string       `json:"issued_on"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		if body.Lines == nil {
			return 0, nil, domain.Invalid("lines", "is required")
		}
		in := DraftEdit{AdjustmentCents: body.AdjustmentCents, AdjustmentReason: body.AdjustmentReason, ClientMessage: body.ClientMessage, IssuedOn: body.IssuedOn}
		for _, l := range body.Lines {
			li := LineInput{Kind: l.Kind, Description: l.Description, AmountCents: l.AmountCents}
			if l.SourceID != nil {
				li.SourceID = *l.SourceID
			}
			if l.FeeItemID != nil {
				li.FeeItemID = *l.FeeItemID
			}
			in.Lines = append(in.Lines, li)
		}
		row, err := h.Svc.EditDraft(r.Context(), p, domain.ID(r.PathValue("invoice_id")), in)
		return http.StatusOK, invoiceView(row, false), err
	})
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	h.write(w, r, p, nil, func() (int, any, error) {
		row, err := h.Svc.ApproveInvoice(r.Context(), p, domain.ID(r.PathValue("invoice_id")))
		return http.StatusOK, invoiceView(row, false), err
	})
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Deliver string `json:"deliver"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.SendInvoice(r.Context(), p, domain.ID(r.PathValue("invoice_id")), body.Deliver)
		return http.StatusOK, invoiceView(row, false), err
	})
}

func (h *Handler) pdf(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	pdf, inv, err := h.Svc.InvoicePDF(r.Context(), p, domain.ID(r.PathValue("invoice_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="invoice-`+strings.Map(safeName, inv.Number)+`.pdf"`)
	_, _ = w.Write(pdf)
}

func safeName(r rune) rune {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
		return r
	}
	return '_'
}

type reasonBody struct {
	Reason string `json:"reason"`
}

func (h *Handler) void(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body reasonBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.VoidInvoice(r.Context(), p, domain.ID(r.PathValue("invoice_id")), body.Reason)
		return http.StatusOK, invoiceView(row, false), err
	})
}

type trustBody struct {
	ClientID       domain.ID     `json:"client_id"`
	TrustAccountID domain.ID     `json:"trust_account_id"`
	AmountCents    *domain.Cents `json:"amount_cents"`
}

func (b trustBody) amount() (domain.Cents, error) {
	if b.AmountCents == nil {
		return 0, domain.Invalid("amount_cents", "an explicit amount is required")
	}
	return *b.AmountCents, nil
}

func (h *Handler) applyTrust(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body trustBody
	h.write(w, r, p, &body, func() (int, any, error) {
		amount, err := body.amount()
		if err != nil {
			return 0, nil, err
		}
		pay, _, err := h.Svc.ApplyTrust(r.Context(), p, domain.ID(r.PathValue("invoice_id")), body.ClientID, body.TrustAccountID, amount)
		return http.StatusOK, paymentView(pay), err
	})
}

func (h *Handler) writeOff(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		AmountCents *domain.Cents `json:"amount_cents"`
		Reason      string        `json:"reason"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		if body.AmountCents == nil {
			return 0, nil, domain.Invalid("amount_cents", "is required")
		}
		row, err := h.Svc.WriteOff(r.Context(), p, domain.ID(r.PathValue("invoice_id")), *body.AmountCents, body.Reason)
		return http.StatusOK, invoiceView(row, false), err
	})
}

func (h *Handler) replenish(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		MatterID  domain.ID     `json:"matter_id"`
		ClientID  domain.ID     `json:"client_id"`
		Amount    *domain.Cents `json:"amount_requested_cents"`
		InvoiceID *domain.ID    `json:"invoice_id"`
		Message   string        `json:"message"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		id := domain.ID(r.PathValue("invoice_id"))
		if body.Amount == nil {
			return 0, nil, domain.Invalid("amount_requested_cents", "an explicit amount is required")
		}
		if body.InvoiceID != nil && *body.InvoiceID != id {
			return 0, nil, domain.Invalid("invoice_id", "must match the path")
		}
		inv, err := h.Svc.Invoice(r.Context(), p, id)
		if err != nil {
			return 0, nil, err
		}
		if body.MatterID != inv.MatterID {
			return 0, nil, domain.Invalid("matter_id", "must be the invoice's matter")
		}
		out, err := h.Svc.RequestReplenishment(r.Context(), p, id, body.ClientID, *body.Amount, body.Message)
		return http.StatusCreated, out, err
	})
}

// Payments.

type paymentBody struct {
	ContactID       domain.ID     `json:"contact_id"`
	MatterID        domain.ID     `json:"matter_id"`
	AmountCents     *domain.Cents `json:"amount_cents"`
	Destination     string        `json:"destination"`
	TrustAccountID  *domain.ID    `json:"trust_account_id"`
	Method          string        `json:"method"`
	ReferenceNumber string        `json:"reference_number"`
	Status          string        `json:"status"`
	InvoiceID       *domain.ID    `json:"invoice_id"`
	Source          string        `json:"source"`
}

func (b paymentBody) input() (PaymentInput, error) {
	if b.AmountCents == nil {
		return PaymentInput{}, domain.Invalid("amount_cents", "is required")
	}
	in := PaymentInput{ContactID: b.ContactID, MatterID: b.MatterID, AmountCents: *b.AmountCents, Destination: b.Destination,
		Method: b.Method, ReferenceNumber: b.ReferenceNumber, Status: b.Status, Source: b.Source}
	if b.TrustAccountID != nil {
		in.TrustAccountID = *b.TrustAccountID
	}
	if b.InvoiceID != nil {
		in.InvoiceID = *b.InvoiceID
	}
	return in, nil
}

func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	rows, err := h.Svc.Payments(r.Context(), p, PaymentFilter{MatterID: domain.ID(q(r, "matter_id")), InvoiceID: domain.ID(q(r, "invoice_id")),
		FromDate: q(r, "from_date"), ToDate: q(r, "to_date"), Status: q(r, "status")})
	h.list(w, r, "payments", anys(rows, paymentView), err)
}

func (h *Handler) createPayment(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body paymentBody
	h.write(w, r, p, &body, func() (int, any, error) {
		in, err := body.input()
		if err != nil {
			return 0, nil, err
		}
		row, err := h.Svc.CreatePayment(r.Context(), p, in)
		return http.StatusCreated, paymentView(row), err
	})
}

func (h *Handler) bulkPayments(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Items []paymentBody `json:"items"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		items := make([]PaymentInput, 0, len(body.Items))
		for _, b := range body.Items {
			in, err := b.input()
			if err != nil {
				return 0, nil, err
			}
			items = append(items, in)
		}
		rows, err := h.Svc.BulkPayments(r.Context(), p, items)
		return http.StatusCreated, map[string]any{"data": anys(rows, paymentView)}, err
	})
}

func (h *Handler) getPayment(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	row, err := h.Svc.Payment(r.Context(), p, domain.ID(r.PathValue("payment_id")))
	respond(w, http.StatusOK, paymentView(row), err)
}

func (h *Handler) paymentStatus(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Status string `json:"status"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.UpdatePaymentStatus(r.Context(), p, domain.ID(r.PathValue("payment_id")), body.Status)
		return http.StatusOK, paymentView(row), err
	})
}

func (h *Handler) reversePayment(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body reasonBody
	h.write(w, r, p, &body, func() (int, any, error) {
		row, err := h.Svc.ReversePayment(r.Context(), p, domain.ID(r.PathValue("payment_id")), body.Reason)
		return http.StatusOK, paymentView(row), err
	})
}

// Worklist and reports.

func (h *Handler) worklist(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	wl, err := h.Svc.Worklist(r.Context(), p)
	respond(w, http.StatusOK, worklistView(wl), err)
}

func (h *Handler) applyFlatFee(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body trustBody
	h.write(w, r, p, &body, func() (int, any, error) {
		amount, err := body.amount()
		if err != nil {
			return 0, nil, err
		}
		posted, _, err := h.Svc.ApplyFlatFee(r.Context(), p, domain.ID(r.PathValue("matter_id")), body.ClientID, body.TrustAccountID, amount)
		return http.StatusOK, posted.Result, err
	})
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request, p domain.Principal, fn func(context.Context, domain.Principal, ReportFilter) (Report, error)) {
	rep, err := fn(r.Context(), p, ReportFilter{MatterID: domain.ID(q(r, "matter_id")), UserID: domain.ID(q(r, "user_id")),
		FromDate: q(r, "from_date"), ToDate: q(r, "to_date"), AsOf: q(r, "as_of")})
	if err != nil {
		writeErr(w, err)
		return
	}
	if q(r, "format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+rep.Report+`.csv"`)
		_ = rep.WriteCSV(w)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

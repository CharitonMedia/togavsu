package billing

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

var funcs = map[string]any{
	"money": func(c domain.Cents) string { return c.String() },
	"label": StatusLabel,
	"hours": func(e TimeEntry) string { return hours(e.Minutes()) },
	"amount": func(e TimeEntry) string {
		a, _ := timeAmount(e.RateCents, e.Minutes())
		return a.String()
	},
	"cell":    cell,
	"date":    dateOf,
	"shown":   shownColumns,
	"isMoney": func(col string) bool { return strings.HasSuffix(col, "_cents") },
	"heading": func(col string) string { return strings.ReplaceAll(strings.TrimSuffix(col, "_cents"), "_", " ") },
	"centsCell": func(v any) string {
		if n, ok := v.(int64); ok {
			return domain.Cents(n).String()
		}
		return cell(v)
	},
}

// shownColumns hides record ids on the report screen; CSV keeps every column.
func shownColumns(cols []string) []string {
	var out []string
	for _, c := range cols {
		if c != "id" && !strings.HasSuffix(c, "_id") {
			out = append(out, c)
		}
	}
	return out
}

// dateOf formats a stored unix-microsecond time as a calendar date. Zero is unset.
func dateOf(micros int64) string {
	if isZero(micros) {
		return ""
	}
	return domain.FromUnixMicro(micros).Format(dateLayout)
}

// page is the data every billing screen renders from.
type page struct {
	Title        string
	CSRF         string
	Notice       string
	Error        string
	CanWrite     bool
	CanApprove   bool
	CanTrust     bool
	Today        string
	Worklist     Worklist
	Invoices     []Invoice
	Overdue      []Invoice
	Invoice      Invoice
	Collections  Collections
	Clients      map[domain.ID]string
	Payments     []Payment
	History      []domain.AuditEntry
	Staff        bool
	Time         []TimeEntry
	Timers       []Timer
	Matters      []domain.Matter
	MatterNames  map[domain.ID]string
	Activities   []NamedType
	Report       Report
	Reports      []string
	ReportTitles map[string]string
}

var reportNames = []string{"unbilled-time", "time-by-user", "ar-aging", "payments-received", "write-offs"}

var reportTitles = map[string]string{"unbilled-time": "Unbilled time", "time-by-user": "Time by user", "ar-aging": "AR aging",
	"payments-received": "Payments received", "write-offs": "Write-offs"}

func (h *Handler) pageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /billing/static/billing.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeFileFS(w, r, h.FS, "billing.css")
	})
	screen := func(pattern string, fn func(http.ResponseWriter, *http.Request, domain.Principal, *page) (string, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, err := h.principal(r)
			if err != nil {
				writeErr(w, err)
				return
			}
			if h.Templates == nil {
				http.NotFound(w, r)
				return
			}
			pg := h.basePage(w, r, p)
			name, err := fn(w, r, p, pg)
			if err != nil {
				writeErr(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := h.Templates.ExecuteTemplate(w, name, pg); err != nil {
				http.Error(w, "template error", http.StatusInternalServerError)
			}
		})
	}
	post := func(pattern string, fn func(*http.Request, domain.Principal) (string, string, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			p, err := h.principal(r)
			if err != nil {
				writeErr(w, err)
				return
			}
			if err := r.ParseForm(); err != nil {
				writeErr(w, domain.Invalid("form", "unreadable"))
				return
			}
			if err := checkCSRF(r); err != nil {
				writeErr(w, err)
				return
			}
			back, notice, err := fn(r, p)
			v := url.Values{}
			if err != nil {
				var api *domain.APIError
				if !asAPI(err, &api) {
					writeErr(w, err)
					return
				}
				v.Set("error", api.Message)
			} else if notice != "" {
				v.Set("notice", notice)
			}
			if len(v) > 0 {
				back += "?" + v.Encode()
			}
			http.Redirect(w, r, back, http.StatusSeeOther)
		})
	}
	screen("GET /billing", h.worklistPage)
	screen("GET /billing/invoices", h.invoicesPage)
	screen("GET /billing/invoices/{invoice_id}", h.invoicePage)
	screen("GET /billing/time", h.timePage)
	screen("GET /billing/payments", h.paymentsPage)
	screen("GET /billing/reports/{report}", h.reportPage)
	mux.HandleFunc("GET /billing/invoices/{invoice_id}/pdf", func(w http.ResponseWriter, r *http.Request) {
		p, err := h.principal(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		h.pdf(w, r, p)
	})
	post("POST /billing/flat-fees/{matter_id}/apply", func(r *http.Request, p domain.Principal) (string, string, error) {
		amount, err := formCents(r, "amount")
		if err != nil {
			return "/billing", "", err
		}
		_, inv, err := h.Svc.ApplyFlatFee(r.Context(), p, domain.ID(r.PathValue("matter_id")), domain.ID(r.FormValue("client_id")), domain.ID(r.FormValue("trust_account_id")), amount)
		if err != nil {
			return "/billing", "", err
		}
		return "/billing", "Flat fee applied on " + inv.Number + ". Collected.", nil
	})
	post("POST /billing/generate", func(r *http.Request, p domain.Principal) (string, string, error) {
		var ids []domain.ID
		for _, v := range r.Form["matter_id"] {
			ids = append(ids, domain.ID(v))
		}
		if one := r.FormValue("one"); one != "" {
			ids = []domain.ID{domain.ID(one)}
		}
		if len(ids) == 0 {
			return "/billing", "", domain.Invalid("matter_id", "select at least one matter")
		}
		invs, err := h.Svc.DraftInvoices(r.Context(), p, DraftRequest{MatterIDs: ids})
		if err != nil {
			return "/billing", "", err
		}
		if onlyOne(len(invs)) {
			return "/billing/invoices/" + string(invs[0].ID), "Draft invoice " + invs[0].Number + " created.", nil
		}
		return "/billing/invoices", strconv.Itoa(len(invs)) + " draft invoices created.", nil
	})
	post("POST /billing/invoices/{invoice_id}/{action}", h.invoiceAction)
	post("POST /billing/time", func(r *http.Request, p domain.Principal) (string, string, error) {
		matter, act := domain.ID(r.FormValue("matter_id")), domain.ID(r.FormValue("activity_type_id"))
		date, desc := r.FormValue("worked_on"), r.FormValue("description")
		minutes, err := strconv.Atoi(r.FormValue("minutes"))
		if err != nil || !positive(int64(minutes)) {
			return "/billing/time", "", domain.Invalid("minutes", "enter the minutes worked")
		}
		inc := incrementsFor(int64(minutes)*60, h.Svc.incrementMinutes(r.Context()))
		billable := r.FormValue("billable") != ""
		_, err = h.Svc.CreateTime(r.Context(), p, TimeInput{MatterID: &matter, WorkedOn: &date, Increments: &inc, Description: &desc, ActivityTypeID: &act, Billable: &billable})
		return "/billing/time", "Time logged.", err
	})
	post("POST /billing/time/{time_entry_id}/review", func(r *http.Request, p domain.Principal) (string, string, error) {
		desc, act := r.FormValue("description"), domain.ID(r.FormValue("activity_type_id"))
		if strings.TrimSpace(desc) == "" {
			return "/billing/time", "", domain.Invalid("description", "describe the work before saving the timer entry")
		}
		_, err := h.Svc.UpdateTime(r.Context(), p, domain.ID(r.PathValue("time_entry_id")), TimeInput{Description: &desc, ActivityTypeID: &act})
		return "/billing/time", "Time entry saved.", err
	})
	post("POST /billing/timers", func(r *http.Request, p domain.Principal) (string, string, error) {
		_, _, err := h.Svc.StartTimer(r.Context(), p, domain.ID(r.FormValue("matter_id")))
		return "/billing/time", "Timer running.", err
	})
	post("POST /billing/timers/{timer_id}/{action}", func(r *http.Request, p domain.Principal) (string, string, error) {
		id := domain.ID(r.PathValue("timer_id"))
		var err error
		switch r.PathValue("action") {
		case "pause":
			_, err = h.Svc.PauseTimer(r.Context(), p, id)
		case "resume":
			_, err = h.Svc.ResumeTimer(r.Context(), p, id)
		case "stop":
			_, _, err = h.Svc.StopTimer(r.Context(), p, id)
			return "/billing/time", "Timer stopped. Review the draft time entry.", err
		default:
			return "/billing/time", "", domain.NotFound("action")
		}
		return "/billing/time", "Timer updated.", err
	})
}

func asAPI(err error, out **domain.APIError) bool {
	api, ok := err.(*domain.APIError)
	if ok {
		*out = api
	}
	return ok
}

func formCents(r *http.Request, field string) (domain.Cents, error) {
	c, err := domain.ParseCents(strings.ReplaceAll(r.FormValue(field), ",", ""))
	if err != nil {
		return 0, domain.Invalid(field, "enter an amount such as 1500.00")
	}
	return c, nil
}

func (h *Handler) basePage(w http.ResponseWriter, r *http.Request, p domain.Principal) *page {
	ctx := r.Context()
	can := func(res domain.Resource, act domain.Action) bool {
		d, err := h.Svc.gate.Decide(ctx, p, domain.ResourceRef{Resource: res, Action: act})
		return err == nil && d.Allow
	}
	return &page{
		CSRF: csrf(w, r), Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error"),
		CanWrite: can(domain.ResInvoice, domain.ActionWrite), CanApprove: can(domain.ResInvoice, domain.ActionApproveInvoice),
		CanTrust: can(domain.ResTrust, domain.ActionTrustWithdraw), Today: h.Svc.today(ctx), Reports: reportNames, ReportTitles: reportTitles,
		Clients: map[domain.ID]string{}, MatterNames: map[domain.ID]string{},
	}
}

func csrf(w http.ResponseWriter, r *http.Request) string {
	var b [32]byte // a 256-bit token, the size practice issues
	if c, err := r.Cookie(practice.CSRFCookie); err == nil && len(c.Value) >= len(b) {
		return c.Value
	}
	_, _ = rand.Read(b[:])
	token := hex.EncodeToString(b[:])
	http.SetCookie(w, &http.Cookie{Name: practice.CSRFCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")})
	return token
}

func checkCSRF(r *http.Request) error {
	c, err := r.Cookie(practice.CSRFCookie)
	if err != nil || c.Value == "" || c.Value != r.FormValue("csrf") {
		return domain.Forbidden("csrf")
	}
	return nil
}

func (h *Handler) names(r *http.Request, pg *page, matters ...domain.ID) {
	for _, id := range matters {
		if _, ok := pg.MatterNames[id]; ok {
			continue
		}
		if m, err := h.Svc.d.Practice.Matter(r.Context(), id); err == nil {
			pg.MatterNames[id] = m.Number + " " + m.Name
		}
	}
}

func (h *Handler) worklistPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	wl, err := h.Svc.Worklist(r.Context(), p)
	if err != nil {
		return "", err
	}
	pg.Title, pg.Worklist = "Billing / Generate", wl
	return "worklist.html", nil
}

func (h *Handler) invoicesPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	invs, err := h.Svc.Invoices(r.Context(), p, InvoiceFilter{Status: q(r, "status"), MatterID: domain.ID(q(r, "matter_id"))})
	if err != nil {
		return "", err
	}
	sort.SliceStable(invs, func(i, j int) bool { return invs[i].ID > invs[j].ID })
	over, err := h.Svc.Overdue(r.Context(), p)
	if err != nil {
		return "", err
	}
	pg.Title, pg.Invoices, pg.Overdue = "Invoices", invs, over
	return "invoices.html", nil
}

func (h *Handler) invoicePage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	ctx := r.Context()
	inv, err := h.Svc.Invoice(ctx, p, domain.ID(r.PathValue("invoice_id")))
	if err != nil {
		return "", err
	}
	if ClientOnly(p) {
		// A client sees the allowlisted invoice only: no reasons, notes, payments or history.
		pg.Title, pg.Invoice = "Invoice "+inv.Number, ClientInvoice(inv)
		return "invoice.html", nil
	}
	pg.Title, pg.Invoice, pg.Staff = "Invoice "+inv.Number, inv, true
	if pg.Collections, err = h.Svc.CollectionsFor(ctx, p, inv); err != nil {
		return "", err
	}
	for _, c := range h.Svc.clientParties(inv.MatterID) {
		pg.Clients[c] = h.Svc.clientName(ctx, c)
	}
	if pays, err := h.Svc.Payments(ctx, p, PaymentFilter{InvoiceID: inv.ID}); err == nil {
		pg.Payments = pays
	}
	if hist, err := h.Svc.History(ctx, p, "invoice", inv.ID); err == nil {
		pg.History = hist
	}
	return "invoice.html", nil
}

func (h *Handler) timePage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	ctx := r.Context()
	rows, err := h.Svc.TimeEntries(ctx, p, TimeFilter{MatterID: domain.ID(q(r, "matter_id"))})
	if err != nil {
		return "", err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].WorkedOn > rows[j].WorkedOn })
	if n := h.Svc.settingInt(ctx, "api.page_size"); len(rows) > n {
		rows = rows[:n]
	}
	pg.Title, pg.Time = "Time Entries", rows
	if timers, err := h.Svc.Timers(ctx, p); err == nil {
		pg.Timers = timers
	}
	if acts, err := h.Svc.Types(ctx, p, false, boolPtr(true)); err == nil {
		pg.Activities = acts
	}
	for _, m := range h.Svc.mattersByNumber() {
		if m.Status == matterClosed {
			continue
		}
		if h.Svc.allow(ctx, p, domain.ResTime, domain.ActionWrite, m.ID) == nil {
			pg.Matters = append(pg.Matters, m)
		}
	}
	for _, e := range rows {
		h.names(r, pg, e.MatterID)
	}
	for _, t := range pg.Timers {
		h.names(r, pg, t.MatterID)
	}
	return "time.html", nil
}

func boolPtr(b bool) *bool { return &b }

func (h *Handler) paymentsPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	pays, err := h.Svc.Payments(r.Context(), p, PaymentFilter{})
	if err != nil {
		return "", err
	}
	sort.SliceStable(pays, func(i, j int) bool { return pays[i].ID > pays[j].ID })
	pg.Title, pg.Payments = "Payments", pays
	for _, pay := range pays {
		h.names(r, pg, pay.MatterID)
	}
	return "payments.html", nil
}

func (h *Handler) reportPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	f := ReportFilter{FromDate: q(r, "from_date"), ToDate: q(r, "to_date"), AsOf: q(r, "as_of")}
	var rep Report
	var err error
	switch r.PathValue("report") {
	case "unbilled-time":
		rep, err = h.Svc.UnbilledReport(r.Context(), p, f)
	case "time-by-user":
		rep, err = h.Svc.TimeByUserReport(r.Context(), p, f)
	case "ar-aging":
		rep, err = h.Svc.ARAgingReport(r.Context(), p, f)
	case "payments-received":
		rep, err = h.Svc.PaymentsReport(r.Context(), p, f)
	case "write-offs":
		rep, err = h.Svc.WriteOffsReport(r.Context(), p, f)
	default:
		return "", domain.NotFound("report")
	}
	if err != nil {
		return "", err
	}
	pg.Title, pg.Report = reportTitles[rep.Report], rep
	return "report.html", nil
}

func (h *Handler) invoiceAction(r *http.Request, p domain.Principal) (string, string, error) {
	ctx := r.Context()
	id := domain.ID(r.PathValue("invoice_id"))
	back := "/billing/invoices/" + string(id)
	switch r.PathValue("action") {
	case "approve":
		_, err := h.Svc.ApproveInvoice(ctx, p, id)
		return back, "Approved.", err
	case "send":
		inv, err := h.Svc.SendInvoice(ctx, p, id, r.FormValue("deliver"))
		if err == nil && r.FormValue("deliver") == "email" {
			return back, "Sent to " + strings.Join(inv.SentTo, ", ") + ".", nil
		}
		return back, "Marked sent. Download the PDF to deliver it.", err
	case "void":
		_, err := h.Svc.VoidInvoice(ctx, p, id, r.FormValue("reason"))
		return back, "Voided. Its time and expenses are unbilled again.", err
	case "write-off":
		amount, err := formCents(r, "amount")
		if err != nil {
			return back, "", err
		}
		_, err = h.Svc.WriteOff(ctx, p, id, amount, r.FormValue("reason"))
		return back, "Written off.", err
	case "interest":
		_, err := h.Svc.ApplyLateInterest(ctx, p, id)
		return back, "Late interest updated.", err
	case "apply-trust":
		amount, err := formCents(r, "amount")
		if err != nil {
			return back, "", err
		}
		client, acct, _ := strings.Cut(r.FormValue("funds"), "|")
		_, _, err = h.Svc.ApplyTrust(ctx, p, id, domain.ID(client), domain.ID(acct), amount)
		return back, "Trust applied.", err
	case "payment":
		amount, err := formCents(r, "amount")
		if err != nil {
			return back, "", err
		}
		inv, err := h.Svc.Invoice(ctx, p, id)
		if err != nil {
			return back, "", err
		}
		_, err = h.Svc.CreatePayment(ctx, p, PaymentInput{ContactID: domain.ID(r.FormValue("contact_id")), MatterID: inv.MatterID, AmountCents: amount,
			Destination: DestOperating, Method: r.FormValue("method"), ReferenceNumber: r.FormValue("reference_number"), InvoiceID: id,
			ReceivedOn: r.FormValue("received_on")})
		return back, "Outside payment recorded.", err
	case "replenish":
		amount, err := formCents(r, "amount")
		if err != nil {
			return back, "", err
		}
		_, err = h.Svc.RequestReplenishment(ctx, p, id, domain.ID(r.FormValue("client_id")), amount, r.FormValue("message"))
		return back, "Replenishment requested.", err
	case "notes":
		msg, notes := r.FormValue("client_message"), r.FormValue("internal_notes")
		_, err := h.Svc.UpdateInvoice(ctx, p, id, InvoicePatch{ClientMessage: &msg, InternalNotes: &notes})
		return back, "Saved.", err
	}
	return back, "", domain.NotFound("action")
}

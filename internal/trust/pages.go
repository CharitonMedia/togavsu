package trust

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/frame"
)

// csrfCookie is the practice double-submit cookie; the core checks the form field.
const csrfCookie = "togavsu_practice_csrf"

// trustTabs are the tabs above every trust screen; the sidebar lists only the ledger,
// balances and replenishment.
var trustTabs = []struct{ Path, Label string }{
	{"/trust/ledger", "Ledger"}, {"/trust/balances", "Balances"}, {"/trust/replenishment", "Replenishment"},
	{"/trust/flags", "Flags"}, {"/trust/reconciliation", "Reconciliation"}, {"/trust/notifications", "Trust notifications"},
}

var funcs = template.FuncMap{
	"tabs":      func() []struct{ Path, Label string } { return trustTabs },
	"on":        func(path, tab string) bool { return path == tab || strings.HasPrefix(path, tab+"/") },
	"money":     func(c int64) string { return domain.Cents(c).String() },
	"date":      func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"label":     func(m map[string]string, k any) string { return m[fmt.Sprint(k)] },
	"flagLabel": func(m map[string]string, id domain.ID) string { return m["flag:"+string(id)] },
	"feeLabel": func(s string) string {
		return map[string]string{FeeNeedsAmount: "Needs fee amount", FeeAwaitingFunds: "Awaiting trust funds",
			FeeReadyToApply: "Ready to apply", FeeCollected: "Collected"}[s]
	},
	"derefc": func(p *int64) int64 {
		var v int64
		if p != nil {
			v = *p
		}
		return v
	},
	"deref": func(p *domain.ID) string {
		if p == nil {
			return ""
		}
		return string(*p)
	},
}

// Matters lists matters for the page pickers. practice.Memory and Durable satisfy it.
type Matters interface {
	Matters() []domain.Matter
}

type matterOption struct {
	ID    domain.ID
	Label string
	// Client is the matter's primary client, set where a page shows it (not in pickers).
	Client domain.ID
}

type view struct {
	Title, CSRF, Notice, Error string
	Path                       string // the screen's path, for the area tabs
	Unread                     int
	CanWithdraw, CanDeposit    bool
	Accounts                   []Account
	Matters                    []matterOption
	// MatterOptions is Matters as <option> elements, built once (AC-017: 20,000 matters).
	MatterOptions template.HTML
	Matter        matterOption
	Entries       []Entry
	// OlderEntries and MoreBalances link the ledger and balances screens' next pages.
	OlderEntries, MoreBalances string
	Balances                   []map[string]string
	Flags                      []Flag
	Flag                       Flag
	Repls                      []Replenishment
	Recons                     []Reconciliation
	Notes                      []Notification
	Fee                        FlatFee
	Evergreen                  string
	MatterBalance              int64
	Today, Status              string
	Labels                     map[string]string
}

func (h *Handler) pageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /trust", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/trust/ledger", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /trust/ledger", h.ledgerPage)
	mux.HandleFunc("POST /trust/ledger", h.ledgerPost)
	mux.HandleFunc("POST /trust/ledger/{trust_entry_id}/reverse", h.reversePost)
	mux.HandleFunc("GET /trust/balances", h.balancesPage)
	mux.HandleFunc("GET /trust/flags", h.flagsPage)
	mux.HandleFunc("GET /trust/flags/{trust_flag_id}", h.flagPage)
	mux.HandleFunc("POST /trust/flags/{trust_flag_id}/resolve", h.resolvePost)
	mux.HandleFunc("GET /trust/replenishment", h.replPage)
	mux.HandleFunc("POST /trust/replenishment/{replenishment_id}/{action}", h.replPost)
	mux.HandleFunc("GET /trust/reconciliation", h.reconPage)
	mux.HandleFunc("POST /trust/reconciliation", h.reconPost)
	mux.HandleFunc("GET /trust/matters/{matter_id}", h.matterPage)
	mux.HandleFunc("POST /trust/matters/{matter_id}/evergreen", h.evergreenPost)
	mux.HandleFunc("POST /trust/matters/{matter_id}/flat-fee", h.flatFeePost)
	mux.HandleFunc("GET /trust/notifications", h.notesPage)
	mux.HandleFunc("POST /trust/notifications/read-all", h.readAllPost)
	mux.HandleFunc("POST /trust/notifications/{notification_id}/open", h.openPost)
}

// base fills the shared page fields. A caller without trust read gets the error page.
func (h *Handler) base(w http.ResponseWriter, r *http.Request, title string) (domain.Principal, *view, bool) {
	p, err := h.Principal(r)
	if err == nil {
		err = h.Svc.allow(r.Context(), p, domain.ResTrust, domain.ActionRead, "")
	}
	if err != nil {
		h.fail(w, r, err)
		return p, nil, false
	}
	v := &view{Title: title, Path: r.URL.Path, Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error"), Today: h.Svc.now().Format("2006-01-02"), Labels: map[string]string{}}
	if c, err := r.Cookie(csrfCookie); err == nil {
		v.CSRF = c.Value
	}
	v.Unread, _ = h.Svc.UnreadCount(r.Context(), p)
	v.CanWithdraw = h.Svc.allow(r.Context(), p, domain.ResTrust, domain.ActionTrustWithdraw, "") == nil
	v.CanDeposit = h.Svc.allow(r.Context(), p, domain.ResTrust, domain.ActionTrustDeposit, "") == nil
	return p, v, true
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	msg := "Something went wrong."
	if api, ok := err.(*domain.APIError); ok {
		status, msg = api.HTTPStatus(), api.Message
	}
	h.write(w, r, status, "error.html", &view{Title: "Trust", Path: r.URL.Path, Error: msg})
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, v *view) {
	h.write(w, r, http.StatusOK, name, v)
}

// write draws the named trust screen inside the app shell.
func (h *Handler) write(w http.ResponseWriter, r *http.Request, status int, name string, v *view) {
	var buf bytes.Buffer
	if err := h.Templates.ExecuteTemplate(&buf, name, v); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	frame.Write(w, r, frame.Page{Title: v.Title, Area: "trust", Body: template.HTML(buf.String()), Status: status, Notice: v.Notice, Error: v.Error})
}

// back redirects after a form post with a notice or an error message.
func back(w http.ResponseWriter, r *http.Request, to string, err error, notice string) {
	u, _ := url.Parse(to)
	q := u.Query()
	if err != nil {
		msg := "The change was not saved."
		if api, ok := err.(*domain.APIError); ok {
			msg = api.Message
		}
		q.Set("error", msg)
	} else if notice != "" {
		q.Set("notice", notice)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// matterOptions lists matters the caller may see, by label, for the posting picker.
func (h *Handler) matterOptions(ctx context.Context, p domain.Principal) []matterOption {
	src, ok := h.Svc.practice.(Matters)
	if !ok {
		return nil
	}
	rd := &readable{s: h.Svc, who: p, seen: map[domain.ID]bool{}}
	var out []matterOption
	for _, m := range src.Matters() {
		if ok, _ := rd.ok(ctx, m.ID); !ok {
			continue
		}
		out = append(out, matterOption{ID: m.ID, Label: m.Number + " " + m.Name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// optionsHTML writes one <option> per matter.
func optionsHTML(list []matterOption) template.HTML {
	var b strings.Builder
	b.Grow(len(list) * 96)
	for _, m := range list {
		b.WriteString(`<option value="`)
		b.WriteString(html.EscapeString(string(m.ID)))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(m.Label))
		b.WriteString(`</option>`)
	}
	return template.HTML(b.String())
}

func (h *Handler) labels(ctx context.Context, v *view, ids ...domain.ID) {
	for _, id := range ids {
		if _, ok := v.Labels[string(id)]; ok || id.IsZero() {
			continue
		}
		if l := h.Svc.matterLabel(ctx, id); l != "" {
			v.Labels[string(id)] = l
		} else if c := h.Svc.clientName(ctx, id); c != "" {
			v.Labels[string(id)] = c
		}
	}
}

func (h *Handler) ledgerPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Ledger")
	if !ok {
		return
	}
	ctx := r.Context()
	matter := qID(r, "matter_id")
	entries, err := h.Svc.visibleEntries(ctx, p, EntryFilter{MatterID: matter})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// The screen shows a page of entries, newest first, with a link to older ones; the
	// API and the ledger exports carry every entry.
	v.Entries = newestFirst(entries)
	if n, err := h.Svc.intSetting(ctx, "api.page_size"); err == nil && positive(int64(n)) {
		var from int
		if c, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil && positive(int64(c)) && c < len(v.Entries) {
			from = c
		}
		v.Entries = v.Entries[from:]
		if len(v.Entries) > n {
			older := url.Values{"from": {strconv.Itoa(from + n)}}
			if !matter.IsZero() {
				older.Set("matter_id", string(matter))
			}
			v.Entries, v.OlderEntries = v.Entries[:n], "/trust/ledger?"+older.Encode()
		}
	}
	for _, e := range v.Entries {
		h.labels(ctx, v, e.MatterID, e.ClientID)
	}
	v.Accounts = h.activeAccounts(ctx)
	v.Matters = h.matterOptions(ctx, p)
	v.MatterOptions = optionsHTML(v.Matters)
	h.render(w, r, "ledger.html", v)
}

// newestFirst reverses posting order for the screen.
func newestFirst(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[len(entries)-1-i] = e
	}
	return out
}

// activeAccounts is for the posting pickers; base has already checked trust read.
func (h *Handler) activeAccounts(ctx context.Context) []Account {
	all, _ := h.Svc.accounts(ctx)
	var out []Account
	for _, a := range all {
		if a.Active {
			out = append(out, a)
		}
	}
	return out
}

func cents(r *http.Request, key string) (int64, error) {
	c, err := domain.ParseCents(r.FormValue(key))
	if err != nil {
		return 0, domain.Invalid(key, "enter an amount such as 125.00")
	}
	return int64(c), nil
}

func (h *Handler) ledgerPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ret := "/trust/ledger"
	if m := r.FormValue("return"); m == "matter" {
		ret = "/trust/matters/" + r.FormValue("matter_id")
	}
	amount, err := cents(r, "amount")
	if err != nil {
		back(w, r, ret, err, "")
		return
	}
	in := PostInput{TrustAccountID: domain.ID(r.FormValue("trust_account_id")), MatterID: domain.ID(r.FormValue("matter_id")),
		ClientID: domain.ID(r.FormValue("client_id")), EntryType: r.FormValue("entry_type"), AmountCents: amount,
		Method: r.FormValue("method"), Description: r.FormValue("description")}
	if in.ClientID.IsZero() {
		in.ClientID = h.Svc.primaryClient(in.MatterID)
	}
	if in.EntryType == TypeAdjustment {
		in.AdjustmentDirection = r.FormValue("adjustment_direction")
		if in.AdjustmentDirection == Debit && positive(in.AmountCents) {
			in.AmountCents = -in.AmountCents
		}
	}
	res, err := h.Svc.Post(r.Context(), p, in, r.FormValue("idempotency_key"))
	notice := "Posted."
	if len(res.Flags) > 0 {
		notice = "Posted. The balance is below zero: a trust flag was raised and the admins and the responsible attorney were emailed."
	}
	back(w, r, ret, err, notice)
}

func (h *Handler) reversePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.Svc.Reverse(r.Context(), p, pathID(r, "trust_entry_id"), ReverseInput{Reason: r.FormValue("reason")}, "")
	notice := "Reversal posted. The original entry is unchanged."
	if len(res.Flags) > 0 {
		notice += " The balance is below zero: a trust flag was raised."
	}
	back(w, r, "/trust/ledger", err, notice)
}

func (h *Handler) balancesPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Balances")
	if !ok {
		return
	}
	rep, err := h.Svc.BalancesReport(r.Context(), p, qID(r, "trust_account_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// A page of balances, by client, with a link to the next; the CSV has every row.
	v.Balances = rep.Rows
	if n, err := h.Svc.intSetting(r.Context(), "api.page_size"); err == nil && positive(int64(n)) {
		var from int
		if c, err := strconv.Atoi(r.URL.Query().Get("from")); err == nil && positive(int64(c)) && c < len(v.Balances) {
			from = c
		}
		v.Balances = v.Balances[from:]
		if len(v.Balances) > n {
			more := url.Values{"from": {strconv.Itoa(from + n)}}
			if a := qID(r, "trust_account_id"); !a.IsZero() {
				more.Set("trust_account_id", string(a))
			}
			v.Balances, v.MoreBalances = v.Balances[:n], "/trust/balances?"+more.Encode()
		}
	}
	h.render(w, r, "balances.html", v)
}

func (h *Handler) flagsPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Flags")
	if !ok {
		return
	}
	v.Status = r.URL.Query().Get("status")
	flags, err := h.Svc.visibleFlags(r.Context(), p, FlagFilter{Status: v.Status, MatterID: qID(r, "matter_id")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Flags = flags
	for _, f := range v.Flags {
		h.labels(r.Context(), v, f.entryMatter, f.entryClient)
		v.Labels["flag:"+string(f.ID)] = v.Labels[string(f.entryMatter)]
	}
	h.render(w, r, "flags.html", v)
}

func (h *Handler) flagPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Flag")
	if !ok {
		return
	}
	f, err := h.Svc.Flag(r.Context(), p, pathID(r, "trust_flag_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Flag = f
	h.labels(r.Context(), v, f.entryMatter, f.entryClient)
	v.Labels["flag:"+string(f.ID)] = v.Labels[string(f.entryMatter)]
	if e, err := h.Svc.Entry(r.Context(), p, f.TrustEntryID); err == nil {
		v.Entries = []Entry{e}
	}
	h.render(w, r, "flag.html", v)
}

func (h *Handler) resolvePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := pathID(r, "trust_flag_id")
	_, err = h.Svc.ResolveFlag(r.Context(), p, id, r.FormValue("note"))
	back(w, r, "/trust/flags/"+string(id), err, "Flag resolved. The ledger entry stays as posted.")
}

func (h *Handler) replPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Replenishment")
	if !ok {
		return
	}
	v.Status = r.URL.Query().Get("status")
	repls, err := h.Svc.visibleRepls(r.Context(), p, qID(r, "matter_id"), v.Status)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Repls = repls
	for _, x := range v.Repls {
		h.labels(r.Context(), v, x.MatterID, x.ClientID)
	}
	h.render(w, r, "replenishment.html", v)
}

func (h *Handler) replPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := pathID(r, "replenishment_id")
	switch r.PathValue("action") {
	case "cancel":
		_, err = h.Svc.CancelReplenishment(r.Context(), p, id)
	case "satisfy":
		_, err = h.Svc.SatisfyReplenishment(r.Context(), p, id)
	default:
		http.NotFound(w, r)
		return
	}
	back(w, r, "/trust/replenishment", err, "Request updated.")
}

func (h *Handler) reconPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Trust Reconciliation")
	if !ok {
		return
	}
	v.Accounts, _ = h.Svc.accounts(r.Context())
	recons, err := h.Svc.readRecons(r.Context(), p, qID(r, "trust_account_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for i := range recons {
		v.Recons = append(v.Recons, recons[len(recons)-1-i])
	}
	for _, a := range v.Accounts {
		v.Labels[string(a.ID)] = a.Name + " ****" + a.LastFour
	}
	h.render(w, r, "reconciliation.html", v)
}

func (h *Handler) reconPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	bal, err := cents(r, "statement_balance")
	if err == nil {
		var rec Reconciliation
		rec, err = h.Svc.Reconcile(r.Context(), p, ReconcileInput{TrustAccountID: domain.ID(r.FormValue("trust_account_id")), StatementOn: r.FormValue("statement_on"), StatementBalanceCents: &bal}, "")
		if err == nil {
			back(w, r, "/trust/reconciliation", nil, "Reconciliation saved. Difference: "+domain.Cents(rec.StatementDifferenceCents).String())
			return
		}
	}
	back(w, r, "/trust/reconciliation", err, "")
}

func (h *Handler) matterPage(w http.ResponseWriter, r *http.Request) {
	p, v, ok := h.base(w, r, "Matter Trust")
	if !ok {
		return
	}
	ctx := r.Context()
	id := pathID(r, "matter_id")
	if err := h.Svc.allow(ctx, p, domain.ResTrust, domain.ActionRead, id); err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.Svc.practice.Matter(ctx, id)
	if err != nil {
		h.fail(w, r, notFound(err, "matter"))
		return
	}
	v.Matter = matterOption{ID: m.ID, Label: m.Number + " " + m.Name, Client: h.Svc.primaryClient(m.ID)}
	v.Title = "Trust: " + v.Matter.Label
	if v.Fee, err = h.Svc.FlatFeeStatus(ctx, p, id); err != nil {
		h.fail(w, r, err)
		return
	}
	if v.MatterBalance, err = h.Svc.clientMatterCents(ctx, v.Matter.Client, id); err != nil {
		h.fail(w, r, err)
		return
	}
	if minimum, has, err := h.Svc.evergreen(ctx, id); err == nil && has {
		v.Evergreen = domain.Cents(minimum).String()
	}
	entries, err := h.Svc.visibleEntries(ctx, p, EntryFilter{MatterID: id})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v.Entries = newestFirst(entries)
	v.Accounts = h.activeAccounts(ctx)
	h.labels(ctx, v, v.Matter.Client)
	h.render(w, r, "matter.html", v)
}

func (h *Handler) evergreenPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := pathID(r, "matter_id")
	min, err := cents(r, "minimum")
	if err == nil {
		err = h.Svc.SetEvergreenMinimum(r.Context(), p, id, min)
	}
	back(w, r, "/trust/matters/"+string(id), err, "Evergreen minimum saved.")
}

func (h *Handler) flatFeePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	id := pathID(r, "matter_id")
	amount, err := cents(r, "amount")
	if err == nil {
		_, err = h.Svc.ApplyFlatFee(r.Context(), p, ApplyFlatFeeInput{MatterID: id, AmountCents: &amount}, r.FormValue("idempotency_key"))
	}
	back(w, r, "/trust/matters/"+string(id), err, "Flat fee moved from trust to operating.")
}

func (h *Handler) notesPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	notes, err := h.Svc.notifications(r.Context(), p, nil)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := &view{Title: "Notifications", Path: r.URL.Path, Notes: notes, Notice: r.URL.Query().Get("notice")}
	if c, err := r.Cookie(csrfCookie); err == nil {
		v.CSRF = c.Value
	}
	v.Unread, _ = h.Svc.UnreadCount(r.Context(), p)
	h.render(w, r, "notifications.html", v)
}

func (h *Handler) readAllPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	_, err = h.Svc.MarkAllRead(r.Context(), p)
	back(w, r, "/trust/notifications", err, "All notifications marked read.")
}

// openPost marks one notification read and opens its item. Only a local link is followed.
func (h *Handler) openPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.Svc.MarkRead(r.Context(), p, pathID(r, "notification_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	to := n.Link
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\") {
		to = "/trust/notifications"
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

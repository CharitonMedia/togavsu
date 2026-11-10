package billing

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Screens for one time entry, expenses, bulk entry and draft-invoice editing (PRD 5, 8.3,
// 8.14, 8.15). Every read and write goes through the Service, which applies roles and walls.

// bulkRows is how many blank rows a bulk-entry form offers. Blank rows are skipped.
const bulkRows = 5

// historyRow is one row of an Edit history card (PRD 8.14): when, who and their role, the
// field, the old and new values, the kind and any note.
type historyRow struct {
	When, Who, Role, Field, Old, New, Kind, Note string
}

// historyRows reads the edit history through Service.History (staff only, same read check
// as the record) and names each actor.
func (h *Handler) historyRows(ctx context.Context, p domain.Principal, entity string, id domain.ID) ([]historyRow, error) {
	rows, err := h.Svc.History(ctx, p, entity, id)
	if err != nil {
		return nil, err
	}
	names := map[domain.ID]string{}
	out := make([]historyRow, 0, len(rows))
	for _, a := range rows {
		v := auditView(a)
		who := v.ActorKind
		if !a.ActorID.IsZero() {
			name, ok := names[a.ActorID]
			if !ok {
				name = string(a.ActorID)
				if u, err := h.Svc.d.Practice.User(ctx, a.ActorID); err == nil && u.Name != "" {
					name = u.Name
				}
				names[a.ActorID] = name
			}
			who = name
		}
		out = append(out, historyRow{When: a.CreatedAt.Format("2006-01-02 15:04"), Who: who, Role: a.Role, Field: a.Field,
			Old: a.OldValue, New: a.NewValue, Kind: v.Kind, Note: a.Reason})
	}
	return out, nil
}

// typeNames maps type ids to names for the lists; activeOnly limits the pick lists.
func (h *Handler) typeNames(ctx context.Context, p domain.Principal, expense bool, pg *page) []NamedType {
	all, err := h.Svc.Types(ctx, p, expense, nil)
	if err != nil {
		return nil
	}
	var active []NamedType
	for _, t := range all {
		pg.TypeNames[string(t.ID)] = t.Name
		if t.Active {
			active = append(active, t)
		}
	}
	return active
}

// writableMatters lists the open matters the caller may write res on, for the pick lists.
func (h *Handler) writableMatters(ctx context.Context, p domain.Principal, res domain.Resource) []domain.Matter {
	var out []domain.Matter
	for _, m := range h.Svc.mattersByNumber() {
		if m.Status != matterClosed && h.Svc.allow(ctx, p, res, domain.ActionWrite, m.ID) == nil {
			out = append(out, m)
		}
	}
	return out
}

// editMatters is the edit form's matter list. It always holds the record's own matter, even a
// closed one, so saving the form never moves the record by accident.
func (h *Handler) editMatters(ctx context.Context, p domain.Principal, res domain.Resource, current domain.ID) []domain.Matter {
	list := h.writableMatters(ctx, p, res)
	for _, m := range list {
		if m.ID == current {
			return list
		}
	}
	if m, err := h.Svc.d.Practice.Matter(ctx, current); err == nil {
		list = append([]domain.Matter{m}, list...)
	}
	return list
}

func (h *Handler) timeEntryPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	ctx := r.Context()
	e, err := h.Svc.TimeEntry(ctx, p, domain.ID(r.PathValue("time_entry_id")))
	if err != nil {
		return "", err
	}
	if pg.History, err = h.historyRows(ctx, p, "time_entry", e.ID); err != nil {
		return "", err
	}
	pg.Title, pg.Entry = "Time entry", e
	pg.Activities = h.typeNames(ctx, p, false, pg)
	pg.CanWrite = h.Svc.allow(ctx, p, domain.ResTime, domain.ActionWrite, e.MatterID) == nil
	if pg.CanWrite {
		pg.Matters = h.editMatters(ctx, p, domain.ResTime, e.MatterID)
	}
	h.names(r, pg, e.MatterID)
	return "time_entry.html", nil
}

func (h *Handler) expensesPage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	ctx := r.Context()
	rows, err := h.Svc.Expenses(ctx, p, ExpenseFilter{MatterID: domain.ID(q(r, "matter_id"))})
	if err != nil {
		return "", err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].IncurredOn > rows[j].IncurredOn })
	if n := h.Svc.settingInt(ctx, "api.page_size"); len(rows) > n {
		rows = rows[:n]
	}
	pg.Title, pg.Expenses = "Expenses", rows
	pg.ExpenseTypes = h.typeNames(ctx, p, true, pg)
	pg.CanWrite = h.Svc.allow(ctx, p, domain.ResExpense, domain.ActionWrite, "") == nil
	if pg.CanWrite {
		pg.Matters = h.writableMatters(ctx, p, domain.ResExpense)
	}
	pg.Markup = h.Svc.settingInt(ctx, "billing.expense_markup_percent")
	pg.BulkRows = make([]int, bulkRows)
	for i := range pg.BulkRows {
		pg.BulkRows[i] = i
	}
	for _, e := range rows {
		h.names(r, pg, e.MatterID)
	}
	return "expenses.html", nil
}

func (h *Handler) expensePage(_ http.ResponseWriter, r *http.Request, p domain.Principal, pg *page) (string, error) {
	ctx := r.Context()
	e, err := h.Svc.Expense(ctx, p, domain.ID(r.PathValue("expense_id")))
	if err != nil {
		return "", err
	}
	if pg.History, err = h.historyRows(ctx, p, "expense", e.ID); err != nil {
		return "", err
	}
	pg.Title, pg.Cost = "Expense", e
	pg.ExpenseTypes = h.typeNames(ctx, p, true, pg)
	pg.CanWrite = h.Svc.allow(ctx, p, domain.ResExpense, domain.ActionWrite, e.MatterID) == nil
	if pg.CanWrite {
		pg.Matters = h.editMatters(ctx, p, domain.ResExpense, e.MatterID)
	}
	h.names(r, pg, e.MatterID)
	return "expense.html", nil
}

// timeForm reads one time row. prefix is "" for a single form or "items.N." for a bulk row.
// minutes are rounded up to whole firm increments.
func (h *Handler) timeForm(r *http.Request, prefix string) (TimeInput, error) {
	matter, act := domain.ID(r.FormValue(prefix+"matter_id")), domain.ID(r.FormValue(prefix+"activity_type_id"))
	date, desc := r.FormValue(prefix+"worked_on"), r.FormValue(prefix+"description")
	minutes, err := strconv.Atoi(strings.TrimSpace(r.FormValue(prefix + "minutes")))
	if err != nil || !positive(int64(minutes)) {
		return TimeInput{}, domain.Invalid(prefix+"minutes", "enter the minutes worked")
	}
	inc := incrementsFor(int64(minutes)*60, h.Svc.incrementMinutes(r.Context()))
	billable := r.FormValue(prefix+"billable") != ""
	return TimeInput{MatterID: &matter, WorkedOn: &date, Increments: &inc, Description: &desc, ActivityTypeID: &act, Billable: &billable}, nil
}

// expenseForm reads one expense row the same way.
func expenseForm(r *http.Request, prefix string) (ExpenseInput, error) {
	matter, typ := domain.ID(r.FormValue(prefix+"matter_id")), domain.ID(r.FormValue(prefix+"expense_type_id"))
	date, desc := r.FormValue(prefix+"incurred_on"), r.FormValue(prefix+"description")
	amount, err := formCents(r, prefix+"amount")
	if err != nil {
		return ExpenseInput{}, err
	}
	in := ExpenseInput{MatterID: &matter, IncurredOn: &date, Description: &desc, AmountCents: &amount, ExpenseTypeID: &typ}
	billable := r.FormValue(prefix+"billable") != ""
	in.Billable = &billable
	if m := strings.TrimSpace(r.FormValue(prefix + "markup_percent")); m != "" {
		n, err := strconv.Atoi(m)
		if err != nil {
			return ExpenseInput{}, domain.Invalid(prefix+"markup_percent", "enter a whole percent")
		}
		in.MarkupPercent = &n
	}
	return in, nil
}

// blankRow reports a bulk row the user left empty.
func blankRow(r *http.Request, prefix string, fields ...string) bool {
	for _, f := range fields {
		if strings.TrimSpace(r.FormValue(prefix+f)) != "" {
			return false
		}
	}
	return true
}

func (h *Handler) recordRoutes(post func(string, func(*http.Request, domain.Principal) (string, string, error))) {
	post("POST /billing/time/bulk", func(r *http.Request, p domain.Principal) (string, string, error) {
		var items []TimeInput
		for i := 0; i < bulkRows; i++ {
			prefix := fmt.Sprintf("items.%d.", i)
			if blankRow(r, prefix, "minutes", "description") {
				continue
			}
			in, err := h.timeForm(r, prefix)
			if err != nil {
				return "/billing/time", "", err
			}
			items = append(items, in)
		}
		rows, err := h.Svc.BulkTime(r.Context(), p, items)
		return "/billing/time", strconv.Itoa(len(rows)) + " time entries logged.", err
	})
	post("POST /billing/time/{time_entry_id}", func(r *http.Request, p domain.Principal) (string, string, error) {
		ctx := r.Context()
		id := domain.ID(r.PathValue("time_entry_id"))
		back := "/billing/time/" + string(id)
		before, err := h.Svc.TimeEntry(ctx, p, id)
		if err != nil {
			return back, "", err
		}
		in, err := h.timeForm(r, "")
		if err != nil {
			return back, "", err
		}
		if minutes, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("minutes"))); int64(minutes) == before.Minutes() {
			in.Increments = nil // unchanged duration keeps the increment it was written in
		}
		if rate := strings.TrimSpace(r.FormValue("rate")); rate != "" {
			c, err := formCents(r, "rate")
			if err != nil {
				return back, "", err
			}
			in.RateCents = &c
		}
		_, err = h.Svc.UpdateTime(ctx, p, id, in)
		return back, "Time entry saved.", err
	})
	post("POST /billing/expenses", func(r *http.Request, p domain.Principal) (string, string, error) {
		in, err := expenseForm(r, "")
		if err != nil {
			return "/billing/expenses", "", err
		}
		_, err = h.Svc.CreateExpense(r.Context(), p, in)
		return "/billing/expenses", "Expense recorded.", err
	})
	post("POST /billing/expenses/bulk", func(r *http.Request, p domain.Principal) (string, string, error) {
		var items []ExpenseInput
		for i := 0; i < bulkRows; i++ {
			prefix := fmt.Sprintf("items.%d.", i)
			if blankRow(r, prefix, "amount", "description") {
				continue
			}
			in, err := expenseForm(r, prefix)
			if err != nil {
				return "/billing/expenses", "", err
			}
			items = append(items, in)
		}
		rows, err := h.Svc.BulkExpenses(r.Context(), p, items)
		return "/billing/expenses", strconv.Itoa(len(rows)) + " expenses recorded.", err
	})
	post("POST /billing/expenses/{expense_id}", func(r *http.Request, p domain.Principal) (string, string, error) {
		id := domain.ID(r.PathValue("expense_id"))
		back := "/billing/expenses/" + string(id)
		in, err := expenseForm(r, "")
		if err != nil {
			return back, "", err
		}
		_, err = h.Svc.UpdateExpense(r.Context(), p, id, in)
		return back, "Expense saved.", err
	})
}

// draftLineRow is one editable row of the draft form.
type draftLineRow struct {
	Index int
	Line
}

// unbilledSource is billable, reviewed, unbilled work on the invoice's matter that the draft
// form can add back.
type unbilledSource struct {
	Kind, ID, Label, Amount string
}

// draftData fills what the draft form needs: the line rows, the active fee items, and the
// unbilled work on the matter.
func (h *Handler) draftData(ctx context.Context, p domain.Principal, inv Invoice, pg *page) error {
	for i, l := range inv.Lines {
		pg.DraftLines = append(pg.DraftLines, draftLineRow{Index: i, Line: l})
	}
	if items, err := h.Svc.FeeItems(ctx, p, boolPtr(true)); err == nil {
		pg.FeeItems = items
	}
	times, err := h.Svc.TimeEntries(ctx, p, TimeFilter{MatterID: inv.MatterID, Unbilled: true})
	if err != nil {
		return err
	}
	for _, e := range times {
		if e.Billable && e.Reviewed() {
			a, _ := timeAmount(e.RateCents, e.Minutes())
			pg.Unbilled = append(pg.Unbilled, unbilledSource{LineTime, string(e.ID), e.WorkedOn + " " + e.Description + " (" + hours(e.Minutes()) + " h)", a.String()})
		}
	}
	costs, err := h.Svc.Expenses(ctx, p, ExpenseFilter{MatterID: inv.MatterID, Unbilled: true})
	if err != nil {
		return err
	}
	for _, e := range costs {
		if e.Billable {
			a, _ := expenseAmount(e.AmountCents, e.MarkupPercent)
			pg.Unbilled = append(pg.Unbilled, unbilledSource{LineExpense, string(e.ID), e.IncurredOn + " " + e.Description, a.String()})
		}
	}
	return nil
}

// draftEdit reads the draft form into a DraftEdit: kept rows (with their edited text and
// amount), unbilled work added back, a fee-catalog pick (blank text or amount take the item's),
// one free line, and the adjustment with its reason.
func draftEdit(r *http.Request) (DraftEdit, error) {
	var in DraftEdit
	n, _ := strconv.Atoi(r.FormValue("lines"))
	for i := 0; i < n; i++ {
		prefix := fmt.Sprintf("line.%d.", i)
		if r.FormValue(prefix+"remove") != "" {
			continue
		}
		li := LineInput{Kind: r.FormValue(prefix + "kind"), SourceID: domain.ID(r.FormValue(prefix + "source_id")),
			FeeItemID: domain.ID(r.FormValue(prefix + "fee_item_id")), Description: r.FormValue(prefix + "description")}
		amount, err := formCents(r, prefix+"amount")
		if err != nil {
			return DraftEdit{}, err
		}
		li.AmountCents = &amount
		in.Lines = append(in.Lines, li)
	}
	for _, v := range r.Form["add_source"] {
		kind, id, _ := strings.Cut(v, "|")
		in.Lines = append(in.Lines, LineInput{Kind: kind, SourceID: domain.ID(id)})
	}
	if item := r.FormValue("add_fee_item"); item != "" {
		li := LineInput{Kind: LineFlatFee, FeeItemID: domain.ID(item), Description: r.FormValue("add_fee_description")}
		if strings.TrimSpace(r.FormValue("add_fee_amount")) != "" {
			amount, err := formCents(r, "add_fee_amount")
			if err != nil {
				return DraftEdit{}, err
			}
			li.AmountCents = &amount
		}
		in.Lines = append(in.Lines, li)
	}
	if kind := r.FormValue("add_kind"); kind != "" {
		amount, err := formCents(r, "add_amount")
		if err != nil {
			return DraftEdit{}, err
		}
		in.Lines = append(in.Lines, LineInput{Kind: kind, Description: r.FormValue("add_description"), AmountCents: &amount})
	}
	adj := domain.Cents(0)
	if strings.TrimSpace(r.FormValue("adjustment")) != "" {
		var err error
		if adj, err = formCents(r, "adjustment"); err != nil {
			return DraftEdit{}, err
		}
	}
	reason := r.FormValue("adjustment_reason")
	in.AdjustmentCents, in.AdjustmentReason = &adj, &reason
	if issued := r.FormValue("issued_on"); issued != "" {
		in.IssuedOn = &issued
	}
	return in, nil
}

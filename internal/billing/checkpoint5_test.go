package billing

import (
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Regression tests for the fifth Codex checkpoint findings on PR #27: expense screens, the
// time-entry screen, bulk entry, the draft-invoice editor, and complete Edit history cards.

// historyHeader is the PRD 8.14 column set every Edit history card must render.
const historyHeader = "<th>When</th><th>Who</th><th>Role</th><th>Field</th><th>Old</th><th>New</th><th>Kind</th><th>Note</th>"

// historyHas reports a history row with these cells in order (who, role, field, old, new, kind,
// note). A "*" cell matches any value (a create row's new value is the whole record).
func historyHas(body string, cells ...string) bool {
	var b strings.Builder
	b.WriteString(`<tr><td class="nowrap">[^<]*</td>`)
	for i, c := range cells {
		v := regexp.QuoteMeta(c)
		if c == "*" {
			v = `[^<]*`
		}
		if i >= 3 && i <= 4 || i == 6 {
			b.WriteString(`<td class="clip">` + v + `</td>`)
		} else {
			b.WriteString(`<td>` + v + `</td>`)
		}
	}
	return regexp.MustCompile(b.String()).MatchString(body)
}

func errOf(loc string) string {
	u, err := url.Parse(loc)
	if err != nil {
		return ""
	}
	return u.Query().Get("error")
}

func TestExpenseScreens(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Expense page", "hourly", 0, 0)
	walled, _ := f.matter("Hidden expense", "hourly", 0, 0)
	hidden := f.expense(walled.ID, 9_00, 0)
	if err := f.ps.SetWall(f.ctx, f.admin, walled.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		rows, err := f.svc.Expenses(f.ctx, f.attorney, ExpenseFilter{})
		if err != nil {
			t.Fatal(err)
		}
		return len(rows)
	}

	code, body := a.page(f.staff, "/billing/expenses")
	if code != 200 || !strings.Contains(body, "Record expense") || !strings.Contains(body, "Bulk entry") || strings.Contains(body, "Hidden expense") {
		t.Fatalf("expenses page %d", code)
	}
	if !strings.Contains(body, `href="/billing/expenses"`) {
		t.Fatal("nav has no Expenses link")
	}
	// Record one.
	code, loc := a.form(f.staff, "/billing/expenses", url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"42.50"},
		"expense_type_id": {string(f.expType)}, "markup_percent": {"10"}, "description": {"Courier"}, "billable": {"1"}}, true)
	if code != 303 || errOf(loc) != "" || !strings.Contains(loc, "Expense+recorded") {
		t.Fatalf("record %d %s", code, loc)
	}
	rows, _ := f.svc.Expenses(f.ctx, f.staff, ExpenseFilter{MatterID: m.ID})
	if len(rows) != 1 || rows[0].AmountCents != 42_50 || rows[0].MarkupPercent != 10 || !rows[0].Billable || rows[0].Description != "Courier" {
		t.Fatalf("stored expense %+v", rows)
	}
	exp := rows[0]
	_, body = a.page(f.staff, "/billing/expenses")
	if !strings.Contains(body, `href="/billing/expenses/`+string(exp.ID)+`"`) || !strings.Contains(body, "Filing fee") {
		t.Fatal("list does not link the expense or name its type")
	}
	// Bad amount writes nothing.
	before := count()
	_, loc = a.form(f.staff, "/billing/expenses", url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"lots"},
		"expense_type_id": {string(f.expType)}, "description": {"Bad"}}, true)
	if errOf(loc) == "" || count() != before {
		t.Fatalf("bad amount must be refused: %s", loc)
	}

	// The expense screen: details, edit form and Edit history with every PRD column.
	path := "/billing/expenses/" + string(exp.ID)
	code, body = a.page(f.staff, path)
	if code != 200 || !strings.Contains(body, "Edit expense") || !strings.Contains(body, "Edit history") || !strings.Contains(body, historyHeader) {
		t.Fatalf("expense page %d", code)
	}
	if !historyHas(body, "Sam Staff", "staff", "create", "", "*", "create", "") {
		t.Fatal("create row must name who, role and kind")
	}
	code, loc = a.form(f.staff, path, url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"45.00"},
		"expense_type_id": {string(f.expType)}, "markup_percent": {"10"}, "description": {"Courier"}}, true)
	if code != 303 || errOf(loc) != "" {
		t.Fatalf("edit %d %s", code, loc)
	}
	got, _ := f.svc.Expense(f.ctx, f.staff, exp.ID)
	if got.AmountCents != 45_00 || got.Billable {
		t.Fatalf("edit not saved: %+v", got)
	}
	_, body = a.page(f.attorney, path)
	if !historyHas(body, "Sam Staff", "staff", "AmountCents", "4250", "4500", "update", "") ||
		!historyHas(body, "Sam Staff", "staff", "Billable", "true", "false", "update", "") {
		t.Fatalf("update rows missing old/new values:\n%s", body)
	}
	// An edit that fails validation writes nothing.
	snap := f.snapshot()
	_, loc = a.form(f.staff, path, url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"not a date"}, "amount": {"1.00"},
		"expense_type_id": {string(f.expType)}, "description": {"Courier"}}, true)
	if errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("invalid edit must be refused with nothing written: %s", loc)
	}

	// Bulk: two rows save together; one bad row saves none.
	bulk := url.Values{}
	for i, d := range []string{"Parking", "Copies"} {
		p := "items." + itoa(i) + "."
		bulk.Set(p+"matter_id", string(m.ID))
		bulk.Set(p+"incurred_on", "2026-06-04")
		bulk.Set(p+"amount", "3.00")
		bulk.Set(p+"expense_type_id", string(f.expType))
		bulk.Set(p+"description", d)
		bulk.Set(p+"billable", "1")
	}
	bulk.Set("items.2.matter_id", string(m.ID)) // a blank row is skipped
	before = count()
	_, loc = a.form(f.staff, "/billing/expenses/bulk", bulk, true)
	if errOf(loc) != "" || count() != before+2 || !strings.Contains(loc, "2+expenses+recorded") {
		t.Fatalf("bulk %s count %d", loc, count()-before)
	}
	bulk.Set("items.1.expense_type_id", string(f.activity)) // not an expense type
	snap = f.snapshot()
	_, loc = a.form(f.staff, "/billing/expenses/bulk", bulk, true)
	if errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("bulk with one bad row must write nothing: %s", loc)
	}
	_, loc = a.form(f.staff, "/billing/expenses/bulk", url.Values{}, true)
	if errOf(loc) == "" {
		t.Fatal("empty bulk must be refused")
	}

	// Accountant: reads the list and the screen, sees no forms, and every post is refused.
	code, body = a.page(f.accountant, "/billing/expenses")
	if code != 200 || strings.Contains(body, "Record expense") || strings.Contains(body, "Bulk entry") {
		t.Fatalf("accountant expenses page %d", code)
	}
	code, body = a.page(f.accountant, path)
	if code != 200 || strings.Contains(body, "Edit expense") || !strings.Contains(body, "Edit history") {
		t.Fatalf("accountant expense page %d", code)
	}
	snap = f.snapshot()
	_, loc = a.form(f.accountant, path, url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"1.00"},
		"expense_type_id": {string(f.expType)}, "description": {"x"}}, true)
	if errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("accountant edit must be refused: %s", loc)
	}
	_, loc = a.form(f.accountant, "/billing/expenses", url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"1.00"},
		"expense_type_id": {string(f.expType)}, "description": {"x"}}, true)
	if errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("accountant record must be refused: %s", loc)
	}
	// Missing CSRF.
	if code, _ := a.form(f.staff, "/billing/expenses", url.Values{}, false); code != 403 {
		t.Fatalf("missing csrf %d", code)
	}

	// Walls: the hidden expense's screen is not found for staff outside the wall.
	if code, body := a.page(f.staff, "/billing/expenses/"+string(hidden.ID)); code != 404 || strings.Contains(body, "Hidden expense") {
		t.Fatalf("walled expense page %d", code)
	}
	if code, _ := a.page(f.attorney, "/billing/expenses/"+string(hidden.ID)); code != 200 {
		t.Fatalf("attorney inside the wall %d", code)
	}

	// A billed expense is locked on its screen and refuses an edit.
	if _, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID}); err != nil {
		t.Fatal(err)
	}
	_, body = a.page(f.staff, path)
	// exp was edited to non-billable above, so pick a billed bulk row.
	billed, _ := f.svc.Expenses(f.ctx, f.staff, ExpenseFilter{MatterID: m.ID, Billable: boolPtr(true)})
	bp := "/billing/expenses/" + string(billed[0].ID)
	_, body = a.page(f.staff, bp)
	if strings.Contains(body, "Edit expense") || !strings.Contains(body, "This expense is billed") {
		t.Fatal("billed expense must show as locked")
	}
	snap = f.snapshot()
	_, loc = a.form(f.staff, bp, url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-03"}, "amount": {"1.00"},
		"expense_type_id": {string(f.expType)}, "description": {"x"}, "billable": {"1"}}, true)
	if errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("billed expense edit must be refused: %s", loc)
	}
	_, body = a.page(f.staff, bp)
	if !strings.Contains(body, "<td>invoice_id</td>") || !strings.Contains(body, `<td class="clip">drafted on INV-`) {
		t.Fatal("history must show the drafting note")
	}
}

func TestTimeEntryScreenAndBulk(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, contact := f.matter("Time page", "hourly", 0, 0)
	other, _ := f.matter("Other time", "hourly", 0, 0)
	e := f.logTime(f.staff, m.ID, "2026-06-01", 5, nil) // 30 minutes at 90.00
	path := "/billing/time/" + string(e.ID)

	_, body := a.page(f.staff, "/billing/time")
	if !strings.Contains(body, `href="`+path+`"`) || !strings.Contains(body, "Bulk entry") {
		t.Fatal("time list must link each entry and offer bulk entry")
	}
	if strings.Contains(body, `id="new-time" open`) {
		t.Fatal("the New time entry panel must start closed")
	}
	// The shell's New > Log time link lands on the open form.
	if _, body := a.page(f.staff, "/billing/time?new=1"); !strings.Contains(body, `<details class="new" id="new-time" open>`) ||
		!strings.Contains(body, `aria-label="Log time"`) {
		t.Fatal("?new=1 must open the New time entry panel")
	}
	code, body := a.page(f.staff, path)
	if code != 200 || !strings.Contains(body, "Edit time entry") || !strings.Contains(body, historyHeader) ||
		!historyHas(body, "Sam Staff", "staff", "create", "", "*", "create", "") {
		t.Fatalf("time entry page %d:\n%s", code, body)
	}
	// Change the description and move it to another matter; the duration is unchanged.
	code, loc := a.form(f.staff, path, url.Values{"matter_id": {string(other.ID)}, "worked_on": {"2026-06-01"}, "minutes": {"30"}, "rate": {"90.00"},
		"activity_type_id": {string(f.activity)}, "description": {"Revise pleading"}, "billable": {"1"}}, true)
	if code != 303 || errOf(loc) != "" {
		t.Fatalf("edit %d %s", code, loc)
	}
	got, _ := f.svc.TimeEntry(f.ctx, f.staff, e.ID)
	if got.Description != "Revise pleading" || got.MatterID != other.ID || got.Minutes() != 30 || got.RateCents != 90_00 {
		t.Fatalf("edit not saved: %+v", got)
	}
	_, body = a.page(f.staff, path)
	if !historyHas(body, "Sam Staff", "staff", "Description", "Draft pleading", "Revise pleading", "update", "") {
		t.Fatalf("update row missing:\n%s", body)
	}
	// The form keeps the entry's matter selected.
	if !strings.Contains(body, `value="`+string(other.ID)+`" selected`) {
		t.Fatal("edit form must select the entry's own matter")
	}
	// Blank description and zero minutes are refused with nothing written.
	snap := f.snapshot()
	for _, v := range []url.Values{
		{"matter_id": {string(other.ID)}, "worked_on": {"2026-06-01"}, "minutes": {"0"}, "activity_type_id": {string(f.activity)}, "description": {"x"}},
		{"matter_id": {string(other.ID)}, "worked_on": {"2026-06-01"}, "minutes": {"30"}, "activity_type_id": {string(f.activity)}, "description": {"x"}, "rate": {"-1.00"}},
	} {
		if _, loc := a.form(f.staff, path, v, true); errOf(loc) == "" || f.snapshot() != snap {
			t.Fatalf("invalid edit must be refused: %s", loc)
		}
	}
	// Accountant reads, cannot edit. A client is refused the screen (staff history).
	code, body = a.page(f.accountant, path)
	if code != 200 || strings.Contains(body, "Edit time entry") {
		t.Fatalf("accountant time page %d", code)
	}
	if _, loc := a.form(f.accountant, path, url.Values{"matter_id": {string(other.ID)}, "worked_on": {"2026-06-01"}, "minutes": {"30"},
		"activity_type_id": {string(f.activity)}, "description": {"x"}}, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("accountant edit must be refused: %s", loc)
	}
	cl := f.clientUser(contact)
	a.who[cl.UserID] = cl
	if code, body := a.page(cl, path); code == 200 || strings.Contains(body, "Edit history") {
		t.Fatalf("client must not get the time entry screen: %d", code)
	}

	// Bulk time: two rows save; a bad row saves none.
	count := func() int { rows, _ := f.svc.TimeEntries(f.ctx, f.staff, TimeFilter{}); return len(rows) }
	bulk := url.Values{}
	for i, d := range []string{"Call client", "Review file"} {
		p := "items." + itoa(i) + "."
		bulk.Set(p+"matter_id", string(m.ID))
		bulk.Set(p+"worked_on", "2026-06-05")
		bulk.Set(p+"minutes", "7") // rounds up to two 6-minute increments
		bulk.Set(p+"activity_type_id", string(f.activity))
		bulk.Set(p+"description", d)
		bulk.Set(p+"billable", "1")
	}
	before := count()
	_, loc = a.form(f.staff, "/billing/time/bulk", bulk, true)
	if errOf(loc) != "" || count() != before+2 {
		t.Fatalf("bulk time %s", loc)
	}
	rows, _ := f.svc.TimeEntries(f.ctx, f.staff, TimeFilter{MatterID: m.ID, FromDate: "2026-06-05"})
	for _, r := range rows {
		if r.Minutes() != 12 || !r.Billable {
			t.Fatalf("bulk row %+v", r)
		}
	}
	bulk.Set("items.1.minutes", "nope")
	snap = f.snapshot()
	if _, loc := a.form(f.staff, "/billing/time/bulk", bulk, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("bulk with a bad row must write nothing: %s", loc)
	}
	bulk.Set("items.1.minutes", "7")
	bulk.Set("items.1.worked_on", "June")
	if _, loc := a.form(f.staff, "/billing/time/bulk", bulk, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("bulk with a bad date must write nothing: %s", loc)
	}
	if _, loc := a.form(f.accountant, "/billing/time/bulk", bulk, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("accountant bulk must be refused: %s", loc)
	}
}

func TestInvoiceHistoryCardShowsWhoKindAndNote(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, contact := f.matter("History cols", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	inv := f.sent(m.ID)
	cl := f.clientUser(contact)
	if _, err := f.svc.Invoice(f.ctx, cl, inv.ID); err != nil { // marks it viewed, with a note
		t.Fatal(err)
	}
	_, body := a.page(f.attorney, "/billing/invoices/"+string(inv.ID))
	if !strings.Contains(body, historyHeader) {
		t.Fatal("invoice history card is missing PRD 8.14 columns")
	}
	if !historyHas(body, "Sam Staff", "staff", "create", "", "*", "create", "") {
		t.Fatal("create row must show who drafted it")
	}
	if !historyHas(body, "Ada Attorney", "attorney", "approved_by", "", string(f.attorney.UserID), "update", "") {
		t.Fatal("approval row must show who approved")
	}
	if !historyHas(body, "Cora", "client", "status", StatusSent, StatusViewed, "update", "client viewed") {
		t.Fatalf("viewed row must carry its note:\n%s", body)
	}
	// Newest first: the client view comes before the create row.
	if strings.Index(body, "client viewed") > strings.Index(body, "<td>create</td>") {
		t.Fatal("history must be newest first")
	}
}

func TestDraftInvoiceEditor(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Draft editor", "hourly", 0, 0)
	te := f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil) // 1 h at 250.00
	ex := f.expense(m.ID, 30_00, 0)
	name := "Will package"
	item, err := f.svc.CreateFeeItem(f.ctx, f.admin, FeeItemInput{Name: &name, DefaultAmountCents: cents(450_00)})
	if err != nil {
		t.Fatal(err)
	}
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	inv := invs[0]
	path := "/billing/invoices/" + string(inv.ID)
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, inv.ID); err != nil {
		t.Fatal(err)
	}

	code, body := a.page(f.staff, path)
	if code != 200 || !strings.Contains(body, "Edit draft") || !strings.Contains(body, "Will package (450.00)") ||
		!strings.Contains(body, "Messages, notes and dispute") || !strings.Contains(body, "Saving clears the approval") {
		t.Fatalf("draft editor missing %d", code)
	}
	// Lines as rendered, so the form round-trips what the screen shows.
	form := url.Values{"lines": {"2"}}
	for i, l := range inv.Lines {
		p := "line." + itoa(i) + "."
		form.Set(p+"kind", l.Kind)
		form.Set(p+"source_id", string(l.SourceID))
		form.Set(p+"fee_item_id", string(l.FeeItemID))
		form.Set(p+"description", l.Description)
		form.Set(p+"amount", l.AmountCents.String())
		if !strings.Contains(body, `name="`+p+`description" value="`+l.Description+`"`) {
			t.Fatalf("line %d not editable on the screen", i)
		}
	}
	timeIdx := "0"
	if inv.Lines[0].Kind != LineTime {
		timeIdx = "1"
	}
	// Pick the fee item with blank text and amount: it fills both. Edit the time line's text
	// and remove the expense line. Add a courtesy adjustment with its reason.
	edit := url.Values{}
	for k, v := range form {
		edit[k] = v
	}
	edit.Set("line."+timeIdx+".description", "Strategy meeting")
	edit.Set("line."+map[string]string{"0": "1", "1": "0"}[timeIdx]+".remove", "1")
	edit.Set("add_fee_item", string(item.ID))
	edit.Set("adjustment", "-25.00")
	edit.Set("adjustment_reason", "Courtesy")
	code, loc := a.form(f.staff, path+"/draft", edit, true)
	if code != 303 || errOf(loc) != "" {
		t.Fatalf("draft save %d %s", code, loc)
	}
	got, _ := f.svc.Invoice(f.ctx, f.staff, inv.ID)
	if len(got.Lines) != 2 || got.Approved() || got.AdjustmentCents != -25_00 || got.AdjustmentReason != "Courtesy" {
		t.Fatalf("draft after edit %+v", got)
	}
	var fee Line
	for _, l := range got.Lines {
		if l.Kind == LineTime && l.Description != "Strategy meeting" {
			t.Fatalf("time line text not edited: %+v", l)
		}
		if l.FeeItemID == item.ID {
			fee = l
		}
	}
	if fee.Description != "Will package" || fee.AmountCents != 450_00 || fee.MatterFee {
		t.Fatalf("fee item did not fill text and amount: %+v", fee)
	}
	if got.TotalCents != 250_00+450_00-25_00 {
		t.Fatalf("total %s", got.TotalCents)
	}
	if e, _ := f.svc.Expense(f.ctx, f.staff, ex.ID); !e.InvoiceID.IsZero() {
		t.Fatal("removed expense must return to unbilled")
	}

	// The fee item's text and amount stay editable on its line; the expense can be added back.
	_, body = a.page(f.staff, path)
	if !strings.Contains(body, "Add unbilled work") || !strings.Contains(body, `value="expense|`+string(ex.ID)+`"`) {
		t.Fatal("unbilled expense must be offered back")
	}
	if strings.Contains(body, `value="time|`+string(te.ID)+`"`) {
		t.Fatal("a billed time entry must not be offered")
	}
	edit = url.Values{"lines": {"2"}, "adjustment": {"-25.00"}, "adjustment_reason": {"Courtesy"}, "add_source": {"expense|" + string(ex.ID)}}
	for i, l := range got.Lines {
		p := "line." + itoa(i) + "."
		edit.Set(p+"kind", l.Kind)
		edit.Set(p+"source_id", string(l.SourceID))
		edit.Set(p+"fee_item_id", string(l.FeeItemID))
		edit.Set(p+"description", l.Description)
		edit.Set(p+"amount", l.AmountCents.String())
		if l.FeeItemID == item.ID {
			edit.Set(p+"description", "Will package, two wills")
			edit.Set(p+"amount", "500.00")
		}
	}
	if _, loc := a.form(f.staff, path+"/draft", edit, true); errOf(loc) != "" {
		t.Fatalf("second save %s", loc)
	}
	got, _ = f.svc.Invoice(f.ctx, f.staff, inv.ID)
	if len(got.Lines) != 3 || got.TotalCents != 250_00+500_00+30_00-25_00 {
		t.Fatalf("after re-add %+v total %s", got.Lines, got.TotalCents)
	}
	for _, l := range got.Lines {
		if l.FeeItemID == item.ID && (l.Description != "Will package, two wills" || l.AmountCents != 500_00) {
			t.Fatalf("fee item line not editable: %+v", l)
		}
	}
	// A picked item with typed text and amount uses them.
	keep := url.Values{"lines": {"3"}, "adjustment": {"-25.00"}, "adjustment_reason": {"Courtesy"}}
	for i, l := range got.Lines {
		p := "line." + itoa(i) + "."
		keep.Set(p+"kind", l.Kind)
		keep.Set(p+"source_id", string(l.SourceID))
		keep.Set(p+"fee_item_id", string(l.FeeItemID))
		keep.Set(p+"description", l.Description)
		keep.Set(p+"amount", l.AmountCents.String())
	}

	// Failure paths write nothing: an adjustment without a reason, a negative fee line,
	// a bad amount, an unknown fee item, and an accountant or a missing CSRF token.
	snap := f.snapshot()
	bad := func(mut func(url.Values)) url.Values {
		v := url.Values{}
		for k, x := range keep {
			v[k] = append([]string(nil), x...)
		}
		mut(v)
		return v
	}
	for name, v := range map[string]url.Values{
		"no reason": bad(func(v url.Values) { v.Set("adjustment_reason", "") }),
		"negative fee": bad(func(v url.Values) {
			v.Set("add_kind", "flat_fee")
			v.Set("add_description", "x")
			v.Set("add_amount", "-1.00")
		}),
		"bad amount":   bad(func(v url.Values) { v.Set("line.0.amount", "abc") }),
		"unknown item": bad(func(v url.Values) { v.Set("add_fee_item", string(f.id())) }),
		"bad kind": bad(func(v url.Values) {
			v.Set("add_kind", "bonus")
			v.Set("add_description", "x")
			v.Set("add_amount", "1.00")
		}),
	} {
		if _, loc := a.form(f.staff, path+"/draft", v, true); errOf(loc) == "" || f.snapshot() != snap {
			t.Fatalf("%s: must be refused with nothing written: %s", name, loc)
		}
	}
	if _, loc := a.form(f.accountant, path+"/draft", keep, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("accountant draft edit must be refused: %s", loc)
	}
	noToken := bad(func(v url.Values) { v.Del("csrf") })
	if code, _ := a.form(f.staff, path+"/draft", noToken, false); code != 403 || f.snapshot() != snap {
		t.Fatalf("missing csrf %d", code)
	}
	_, body = a.page(f.accountant, path)
	if strings.Contains(body, "Edit draft") || strings.Contains(body, "Messages, notes and dispute") {
		t.Fatal("accountant must not see the draft editor")
	}
	// An adjustment line through "Add a line" works.
	ok := bad(func(v url.Values) {
		v.Set("add_kind", "adjustment")
		v.Set("add_description", "Prompt-pay credit")
		v.Set("add_amount", "-10.00")
	})
	if _, loc := a.form(f.staff, path+"/draft", ok, true); errOf(loc) != "" {
		t.Fatalf("adjustment line %s", loc)
	}
	got, _ = f.svc.Invoice(f.ctx, f.staff, inv.ID)
	if len(got.Lines) != 4 || got.TotalCents != 250_00+500_00+30_00-25_00-10_00 {
		t.Fatalf("adjustment line total %s", got.TotalCents)
	}

	// Notes and dispute, from the screen.
	if _, loc := a.form(f.staff, path+"/notes", url.Values{"client_message": {"Thank you"}, "internal_notes": {"Call first"},
		"dispute_status": {"disputed"}, "dispute_notes": {"Client questions the hours"}}, true); errOf(loc) != "" {
		t.Fatalf("notes %s", loc)
	}
	got, _ = f.svc.Invoice(f.ctx, f.staff, inv.ID)
	if got.ClientMessage != "Thank you" || got.InternalNotes != "Call first" || got.DisputeStatus != "disputed" || got.DisputeNotes != "Client questions the hours" {
		t.Fatalf("notes not saved %+v", got.Invoice)
	}
	_, body = a.page(f.staff, path)
	if !strings.Contains(body, `<option value="disputed" selected>`) || !strings.Contains(body, "Client questions the hours") {
		t.Fatal("dispute status and notes must show on the screen")
	}
	snap = f.snapshot()
	if _, loc := a.form(f.staff, path+"/notes", url.Values{"dispute_status": {"angry"}}, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("bad dispute status must be refused: %s", loc)
	}

	// Once sent, the draft editor is gone and a draft post is refused.
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendInvoice(f.ctx, f.attorney, inv.ID, "file"); err != nil {
		t.Fatal(err)
	}
	_, body = a.page(f.staff, path)
	if strings.Contains(body, "Edit draft") {
		t.Fatal("a sent invoice has no draft editor")
	}
	snap = f.snapshot()
	if _, loc := a.form(f.staff, path+"/draft", keep, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("draft edit of a sent invoice must be refused: %s", loc)
	}
}

func TestLateInterestButton(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Interest page", "hourly", 0, 0)
	f.logTime(f.attorney, m.ID, "2026-01-02", 10, nil)
	f.now = f.now.AddDate(0, -5, 0) // issue in January so it is long past due by June
	inv := f.sent(m.ID)
	f.now = f.now.AddDate(0, 5, 0)
	path := "/billing/invoices/" + string(inv.ID)

	_, body := a.page(f.attorney, path)
	if strings.Contains(body, "Apply late interest") {
		t.Fatal("late interest is off by default; no button")
	}
	snap := f.snapshot()
	if _, loc := a.form(f.attorney, path+"/interest", url.Values{}, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("interest while off must be refused: %s", loc)
	}
	restore := overrideSettings(f.svc, settingOverride{"billing.late_interest_enabled": "true", "billing.late_interest_annual_percent": "12"})
	defer restore()
	_, body = a.page(f.attorney, path)
	if !strings.Contains(body, "Apply late interest") {
		t.Fatal("attorney sees the late interest button when it is on")
	}
	_, body = a.page(f.staff, path)
	if strings.Contains(body, "Apply late interest") {
		t.Fatal("staff must not see the late interest button")
	}
	if _, loc := a.form(f.staff, path+"/interest", url.Values{}, true); errOf(loc) == "" || f.snapshot() != snap {
		t.Fatalf("staff interest must be refused: %s", loc)
	}
	if _, loc := a.form(f.attorney, path+"/interest", url.Values{}, true); errOf(loc) != "" {
		t.Fatalf("interest %s", loc)
	}
	got, _ := f.svc.Invoice(f.ctx, f.attorney, inv.ID)
	if !positive(int64(got.InterestCents)) {
		t.Fatalf("no interest charged: %+v", got.Invoice)
	}
}

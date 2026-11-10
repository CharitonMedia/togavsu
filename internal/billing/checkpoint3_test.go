package billing

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Regression tests for the third Codex checkpoint on PR #27 (review of 17e5bbd..972298a).

func csvColumn(t *testing.T, body []byte, col string) []string {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || len(recs) < 2 {
		t.Fatalf("csv %v: %s", err, body)
	}
	at := -1
	for i, h := range recs[0] {
		if h == col {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no %s column in %v", col, recs[0])
	}
	var out []string
	for _, r := range recs[1:] {
		out = append(out, r[at])
	}
	return out
}

// P2: a negative number in a list CSV stays a number; only real strings are neutralised.
func TestListCSVKeepsNegativeNumbers(t *testing.T) {
	f := newFx(t)
	a := newAPI(t, f)
	m, _ := f.matter("Courtesy", "hourly", 0, 0)
	te := f.logTime(f.attorney, m.ID, "2026-06-01", 10, nil)
	drafts, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	adj, why := domain.Cents(-10_00), "courtesy"
	if _, err := f.svc.EditDraft(f.ctx, f.staff, drafts[0].ID, DraftEdit{Lines: []LineInput{{Kind: LineTime, SourceID: te.ID}},
		AdjustmentCents: &adj, AdjustmentReason: &why}); err != nil {
		t.Fatal(err)
	}
	code, body, _ := a.do(f.staff, "GET", "/api/v1/invoices?format=csv", nil)
	if code != http.StatusOK {
		t.Fatalf("csv: %d %s", code, body)
	}
	if got := csvColumn(t, body, "adjustment_cents"); len(got) != 1 || got[0] != "-1000" {
		t.Fatalf("adjustment_cents = %q, want -1000", got)
	}

	// A formula-looking string is still neutralised in the same list export.
	date, desc, amount, markup := "2026-06-02", "-2+3 =cmd", domain.Cents(5_00), 0
	if _, err := f.svc.CreateExpense(f.ctx, f.staff, ExpenseInput{MatterID: &m.ID, IncurredOn: &date, Description: &desc,
		AmountCents: &amount, ExpenseTypeID: &f.expType, MarkupPercent: &markup}); err != nil {
		t.Fatal(err)
	}
	_, body, _ = a.do(f.staff, "GET", "/api/v1/expenses?format=csv", nil)
	if got := csvColumn(t, body, "description"); len(got) != 1 || got[0] != "'-2+3 =cmd" {
		t.Fatalf("description = %q, want neutralised", got)
	}
	if got := csvColumn(t, body, "amount_cents"); got[0] != "500" {
		t.Fatalf("amount_cents = %q", got)
	}
}

// fakeReceipts stands in for the files service: a receipt is readable on its own matter
// only by the users listed for it.
type fakeReceipts struct {
	files map[domain.ID]struct {
		matter  domain.ID
		readers map[domain.ID]bool
	}
	down bool
}

func (r *fakeReceipts) CheckReceipt(_ context.Context, p domain.Principal, matterID, fileID domain.ID) error {
	if r.down {
		return errors.New("files store unavailable")
	}
	f, ok := r.files[fileID]
	if !ok || !f.readers[p.UserID] {
		return domain.NotFound("file")
	}
	if f.matter != matterID {
		return domain.Invalid("file_id", "file is on another matter")
	}
	return nil
}

func (r *fakeReceipts) ReceiptFiles(_ context.Context, p domain.Principal, matterID domain.ID) ([]ReceiptFile, error) {
	if r.down {
		return nil, errors.New("files store unavailable")
	}
	var out []ReceiptFile
	for id, f := range r.files {
		if f.matter == matterID && f.readers[p.UserID] {
			out = append(out, ReceiptFile{ID: id, Name: string(id) + ".pdf"})
		}
	}
	return out, nil
}

func (r *fakeReceipts) AddReceipt(_ context.Context, p domain.Principal, matterID domain.ID, name string, _ io.Reader, _ int64) (domain.ID, error) {
	if r.down {
		return "", errors.New("files store unavailable")
	}
	id := domain.ID("rcpt-up-" + name)
	r.files[id] = struct {
		matter  domain.ID
		readers map[domain.ID]bool
	}{matterID, map[domain.ID]bool{p.UserID: true}}
	return id, nil
}

func receiptInput(f *fx, matter domain.ID, file domain.ID) ExpenseInput {
	date, desc, amount, markup := "2026-06-02", "Courier", domain.Cents(12_00), 0
	return ExpenseInput{MatterID: &matter, IncurredOn: &date, Description: &desc, AmountCents: &amount,
		ExpenseTypeID: &f.expType, MarkupPercent: &markup, ReceiptFileID: &file}
}

func wantInvalidField(t *testing.T, err error, field string) {
	t.Helper()
	var ae *domain.APIError
	if !errors.As(err, &ae) || ae.Code != domain.CodeValidation || ae.Field != field {
		t.Fatalf("want invalid %s, got %v", field, err)
	}
}

// P2: receipt_file_id is checked through the files seam; with no seam it is refused.
func TestExpenseReceiptIsChecked(t *testing.T) {
	t.Run("no files service refuses any receipt", func(t *testing.T) {
		f := newFx(t)
		m, _ := f.matter("No files", "hourly", 0, 0)
		_, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, "file-1"))
		wantInvalidField(t, err, "receipt_file_id")
		rows, _ := f.svc.Expenses(f.ctx, f.staff, ExpenseFilter{})
		if len(rows) != 0 {
			t.Fatalf("refused expense was stored: %+v", rows)
		}
		// An expense without a receipt still records.
		f.expense(m.ID, 3_00, 0)
	})

	t.Run("files seam decides", func(t *testing.T) {
		f := newFx(t)
		m, _ := f.matter("Receipts", "hourly", 0, 0)
		other, _ := f.matter("Other", "hourly", 0, 0)
		rc := &fakeReceipts{files: map[domain.ID]struct {
			matter  domain.ID
			readers map[domain.ID]bool
		}{
			"rcpt-ok":     {m.ID, map[domain.ID]bool{f.staff.UserID: true}},
			"rcpt-walled": {m.ID, map[domain.ID]bool{f.attorney.UserID: true}}, // walled or attorney-only for staff
			"rcpt-other":  {other.ID, map[domain.ID]bool{f.staff.UserID: true}},
		}}
		f.svc.d.Receipts = rc

		row, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, "rcpt-ok"))
		if err != nil || row.FileID != "rcpt-ok" {
			t.Fatalf("readable receipt: %+v %v", row, err)
		}
		for _, id := range []domain.ID{"rcpt-walled", "rcpt-other", "no-such-file"} {
			_, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, id))
			wantInvalidField(t, err, "receipt_file_id")
			// The patch path is checked too and leaves the row unchanged.
			_, err = f.svc.UpdateExpense(f.ctx, f.staff, row.ID, ExpenseInput{ReceiptFileID: &id})
			wantInvalidField(t, err, "receipt_file_id")
		}
		// Moving the expense to another matter re-checks the kept receipt.
		_, err = f.svc.UpdateExpense(f.ctx, f.staff, row.ID, ExpenseInput{MatterID: &other.ID})
		wantInvalidField(t, err, "receipt_file_id")
		got, err := f.svc.Expense(f.ctx, f.staff, row.ID)
		if err != nil || got.FileID != "rcpt-ok" || got.MatterID != m.ID {
			t.Fatalf("refused patch changed the row: %+v %v", got, err)
		}
		// Clearing the receipt needs no file check.
		none := domain.ID("")
		if got, err := f.svc.UpdateExpense(f.ctx, f.staff, row.ID, ExpenseInput{ReceiptFileID: &none}); err != nil || got.FileID != "" {
			t.Fatalf("clear receipt: %+v %v", got, err)
		}
		// An infrastructure failure fails closed and is not reported as a bad field.
		rc.down = true
		if _, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, "rcpt-ok")); err == nil {
			t.Fatal("receipt accepted while files store is down")
		}
	})
}

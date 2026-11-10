package billing

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ExpenseInput creates or patches an expense. Nil fields stay as they are on a patch.
type ExpenseInput struct {
	MatterID      *domain.ID
	IncurredOn    *string
	Description   *string
	AmountCents   *domain.Cents
	ExpenseTypeID *domain.ID
	Billable      *bool
	MarkupPercent *int
	ReceiptFileID *domain.ID
	Source        *string
}

// ExpenseFilter narrows an expense list.
type ExpenseFilter struct {
	MatterID  domain.ID
	FromDate  string
	ToDate    string
	Billable  *bool
	InvoiceID domain.ID
	Unbilled  bool
}

func (f ExpenseFilter) match(r Expense) bool {
	switch {
	case !f.MatterID.IsZero() && r.MatterID != f.MatterID,
		f.FromDate != "" && r.IncurredOn < f.FromDate,
		f.ToDate != "" && r.IncurredOn > f.ToDate,
		f.Billable != nil && r.Billable != *f.Billable,
		!f.InvoiceID.IsZero() && r.InvoiceID != f.InvoiceID,
		f.Unbilled && !r.InvoiceID.IsZero():
		return false
	}
	return true
}

func (s *Service) expense(ctx context.Context, id domain.ID) (Expense, error) {
	var row Expense
	if err := s.d.Store.Get(ctx, kindExpense, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Expense{}, domain.NotFound("expense")
		}
		return Expense{}, err
	}
	return row, nil
}

// Expense returns one expense the caller may read.
func (s *Service) Expense(ctx context.Context, p domain.Principal, id domain.ID) (Expense, error) {
	row, err := s.expense(ctx, id)
	if err != nil {
		return Expense{}, err
	}
	if err := s.readBilling(ctx, p, row.MatterID); err != nil {
		return Expense{}, err
	}
	return row, nil
}

// Expenses lists expenses on matters the caller can see.
func (s *Service) Expenses(ctx context.Context, p domain.Principal, f ExpenseFilter) ([]Expense, error) {
	if err := s.readBilling(ctx, p, ""); err != nil {
		return nil, err
	}
	rows, err := listOf[Expense](ctx, s.d.Store, kindExpense)
	if err != nil {
		return nil, err
	}
	seen := map[domain.ID]bool{}
	var out []Expense
	for _, r := range rows {
		if !f.match(r) {
			continue
		}
		ok, cached := seen[r.MatterID]
		if !cached {
			if ok, err = s.visible(ctx, p, r.MatterID); err != nil {
				return nil, err
			}
			seen[r.MatterID] = ok
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) buildExpense(ctx context.Context, p domain.Principal, before Expense, in ExpenseInput, create bool) (Expense, error) {
	row := before
	if create {
		if in.MatterID == nil || in.IncurredOn == nil || in.Description == nil || in.AmountCents == nil || in.ExpenseTypeID == nil {
			return Expense{}, domain.Invalid("expense", "matter_id, incurred_on, description, amount_cents and expense_type_id are required")
		}
		row.Billable = true
		row.MarkupPercent = s.settingInt(ctx, "billing.expense_markup_percent")
		row.Source = sourceOf(p, "")
	} else if !before.InvoiceID.IsZero() {
		return Expense{}, domain.Conflict("a billed expense changes only after its invoice is voided")
	}
	if in.MatterID != nil {
		row.MatterID = *in.MatterID
	}
	if in.IncurredOn != nil {
		if _, err := parseDate("incurred_on", *in.IncurredOn); err != nil {
			return Expense{}, err
		}
		row.IncurredOn = *in.IncurredOn
	}
	if in.Description != nil {
		row.Description = strings.TrimSpace(*in.Description)
	}
	if in.AmountCents != nil {
		if negative(int64(*in.AmountCents)) {
			return Expense{}, domain.Invalid("amount_cents", "must be zero or greater")
		}
		row.AmountCents = *in.AmountCents
	}
	if in.ExpenseTypeID != nil {
		if err := s.activeType(ctx, true, *in.ExpenseTypeID, "expense_type_id"); err != nil {
			return Expense{}, err
		}
		row.ExpenseType = string(*in.ExpenseTypeID)
	}
	if in.Billable != nil {
		row.Billable = *in.Billable
	}
	if in.MarkupPercent != nil {
		if negative(int64(*in.MarkupPercent)) {
			return Expense{}, domain.Invalid("markup_percent", "must be zero or greater")
		}
		row.MarkupPercent = *in.MarkupPercent
	}
	receiptChanged := false
	if in.ReceiptFileID != nil {
		receiptChanged = *in.ReceiptFileID != row.FileID
		row.FileID = *in.ReceiptFileID
	}
	if in.Source != nil && *in.Source != "" {
		row.Source = *in.Source
	}
	if err := s.allow(ctx, p, domain.ResExpense, domain.ActionWrite, row.MatterID); err != nil {
		return Expense{}, err
	}
	if _, err := s.matter(ctx, row.MatterID); err != nil {
		return Expense{}, err
	}
	if !create && before.MatterID != row.MatterID {
		if err := s.allow(ctx, p, domain.ResExpense, domain.ActionWrite, before.MatterID); err != nil {
			return Expense{}, err
		}
	}
	if !row.FileID.IsZero() && (receiptChanged || before.MatterID != row.MatterID) {
		if err := s.checkReceipt(ctx, p, row.MatterID, row.FileID); err != nil {
			return Expense{}, err
		}
	}
	if _, err := expenseAmount(row.AmountCents, row.MarkupPercent); err != nil {
		return Expense{}, domain.Invalid("amount_cents", "amount does not fit")
	}
	return row, nil
}

// CreateExpense records one expense.
func (s *Service) CreateExpense(ctx context.Context, p domain.Principal, in ExpenseInput) (Expense, error) {
	rows, err := s.BulkExpenses(ctx, p, []ExpenseInput{in})
	if err != nil {
		return Expense{}, err
	}
	return rows[0], nil
}

// BulkExpenses validates every expense, then writes them in one transaction.
func (s *Service) BulkExpenses(ctx context.Context, p domain.Principal, items []ExpenseInput) ([]Expense, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "at least one item is required")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	out := make([]Expense, 0, len(items))
	for _, in := range items {
		row, err := s.buildExpense(ctx, p, Expense{}, in, true)
		if err != nil {
			return nil, err
		}
		meta, err := s.meta(p)
		if err != nil {
			return nil, err
		}
		row.Meta = meta
		out = append(out, row)
	}
	err := s.write(ctx, p, func(t *txn) error {
		for _, row := range out {
			if err := t.put(kindExpense, row.ID, row); err != nil {
				return err
			}
			if err := t.created("expense", row.ID, row, ""); err != nil {
				return err
			}
			if err := t.event("expense.created", "expense", row.ID, row.MatterID, "Expense recorded: "+row.Description, false); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateExpense patches an unbilled expense.
func (s *Service) UpdateExpense(ctx context.Context, p domain.Principal, id domain.ID, in ExpenseInput) (Expense, error) {
	var out Expense
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.expense(t.ctx, id)
		if err != nil {
			return err
		}
		if err := s.readBilling(t.ctx, p, before.MatterID); err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResExpense, domain.ActionWrite, before.MatterID); err != nil {
			return err
		}
		after, err := s.buildExpense(t.ctx, p, before, in, false)
		if err != nil {
			return err
		}
		after.UpdatedAt = s.now()
		if err := t.put(kindExpense, id, after); err != nil {
			return err
		}
		if err := t.diff("expense", id, before, after, ""); err != nil {
			return err
		}
		out = after
		return t.event("expense.updated", "expense", id, after.MatterID, "Expense updated", false)
	})
	return out, err
}

// ReceiptFiles lists the receipt files on the matter that p may read, for the expense
// screen's picker. It is empty while Deps.Receipts is unwired.
func (s *Service) ReceiptFiles(ctx context.Context, p domain.Principal, matterID domain.ID) ([]ReceiptFile, error) {
	if s.d.Receipts == nil {
		return nil, nil
	}
	if err := s.allow(ctx, p, domain.ResExpense, domain.ActionWrite, matterID); err != nil {
		return nil, err
	}
	return s.d.Receipts.ReceiptFiles(ctx, p, matterID)
}

// AttachReceipt stores an uploaded receipt in the file store as a receipt file record on the
// expense's matter and names it as the expense's receipt. A billed expense is refused before
// any byte is stored. The upload and the expense change are two writes: when the expense
// change fails after the file is stored, the receipt stays on the matter's files, where the
// picker offers it again.
func (s *Service) AttachReceipt(ctx context.Context, p domain.Principal, id domain.ID, name string, r io.Reader, size int64) (Expense, error) {
	row, err := s.Expense(ctx, p, id)
	if err != nil {
		return Expense{}, err
	}
	if err := s.allow(ctx, p, domain.ResExpense, domain.ActionWrite, row.MatterID); err != nil {
		return Expense{}, err
	}
	if !row.InvoiceID.IsZero() {
		return Expense{}, domain.Conflict("a billed expense changes only after its invoice is voided")
	}
	if s.d.Receipts == nil {
		return Expense{}, domain.Invalid("receipt_file_id", "receipt files are not available: the file store is not connected to billing")
	}
	fileID, err := s.d.Receipts.AddReceipt(ctx, p, row.MatterID, name, r, size)
	if err != nil {
		return Expense{}, err
	}
	return s.UpdateExpense(ctx, p, id, ExpenseInput{ReceiptFileID: &fileID})
}

// checkReceipt refuses a receipt file the caller cannot read or that is not a receipt on
// the expense's matter. The files service owns that answer (FileLink), so walls, roles and
// attorney-only rules apply there, not in billing. With Deps.Receipts unwired it refuses.
func (s *Service) checkReceipt(ctx context.Context, p domain.Principal, matterID, fileID domain.ID) error {
	if s.d.Receipts == nil {
		return domain.Invalid("receipt_file_id", "receipt files are not available: the file store is not connected to billing")
	}
	if err := s.d.Receipts.CheckReceipt(ctx, p, matterID, fileID); err != nil {
		var ae *domain.APIError
		if errors.As(err, &ae) {
			return domain.Invalid("receipt_file_id", "is not a receipt file on this matter that you can read")
		}
		return err
	}
	return nil
}

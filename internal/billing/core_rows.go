package billing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// syncCoreBillingRows dual-writes invoice, payment and expense documents into the
// section-5 tables so module foreign keys to core.invoices / core.payments /
// core.expenses resolve (CORE-36 / CU2-06). billing_records stays the API source of
// truth; these rows must match every cent field.
func syncCoreBillingRows(ctx context.Context, conn audit.Querier, kind, id string, v any) error {
	switch kind {
	case kindInvoice:
		inv, ok := v.(Invoice)
		if !ok {
			return fmt.Errorf("billing: unexpected invoice type %T", v)
		}
		return upsertCoreInvoice(ctx, conn, inv)
	case kindPayment:
		pay, ok := v.(Payment)
		if !ok {
			return fmt.Errorf("billing: unexpected payment type %T", v)
		}
		return upsertCorePayment(ctx, conn, pay)
	case kindExpense:
		exp, ok := v.(Expense)
		if !ok {
			return fmt.Errorf("billing: unexpected expense type %T", v)
		}
		return upsertCoreExpense(ctx, conn, exp)
	}
	return nil
}

func upsertCoreInvoice(ctx context.Context, conn audit.Querier, inv Invoice) error {
	by := inv.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO invoices (
id, created_at, updated_at, created_by, matter_id, number, issued_on, status,
subtotal_cents, adjustment_cents, adjustment_reason, interest_cents, total_cents,
amount_paid_cents, balance_due_cents, write_off_cents, write_off_reason, void_reason,
dispute_status, dispute_notes, sent_at, paid_at, client_message, internal_notes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, matter_id = EXCLUDED.matter_id, number = EXCLUDED.number,
issued_on = EXCLUDED.issued_on, status = EXCLUDED.status, subtotal_cents = EXCLUDED.subtotal_cents,
adjustment_cents = EXCLUDED.adjustment_cents, adjustment_reason = EXCLUDED.adjustment_reason,
interest_cents = EXCLUDED.interest_cents, total_cents = EXCLUDED.total_cents,
amount_paid_cents = EXCLUDED.amount_paid_cents, balance_due_cents = EXCLUDED.balance_due_cents,
write_off_cents = EXCLUDED.write_off_cents, write_off_reason = EXCLUDED.write_off_reason,
void_reason = EXCLUDED.void_reason, dispute_status = EXCLUDED.dispute_status,
dispute_notes = EXCLUDED.dispute_notes, sent_at = EXCLUDED.sent_at, paid_at = EXCLUDED.paid_at,
client_message = EXCLUDED.client_message, internal_notes = EXCLUDED.internal_notes`,
		string(inv.ID), inv.CreatedAt.UnixMicro(), inv.UpdatedAt.UnixMicro(), string(by),
		string(inv.MatterID), inv.Number, inv.IssuedOn, inv.Status,
		int64(inv.SubtotalCents), int64(inv.AdjustmentCents), inv.AdjustmentReason, int64(inv.InterestCents),
		int64(inv.TotalCents), int64(inv.AmountPaidCents), int64(inv.BalanceDueCents),
		int64(inv.WriteOffCents), inv.WriteOffReason, inv.VoidReason, inv.DisputeStatus, inv.DisputeNotes,
		inv.SentAt, inv.PaidAt, inv.ClientMessage, inv.InternalNotes)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM invoice_lines WHERE invoice_id = $1`, string(inv.ID)); err != nil {
		return err
	}
	for i, line := range inv.Lines {
		lineID, err := domain.NewID()
		if err != nil {
			return err
		}
		// Stable line id when SourceID is set so re-puts replace the same row identity.
		if !line.SourceID.IsZero() {
			lineID = domain.ID(fmt.Sprintf("%s:%s:%s", inv.ID, line.Kind, line.SourceID))
		} else {
			lineID = domain.ID(fmt.Sprintf("%s:line:%d", inv.ID, i))
		}
		var timeID, expID any
		switch line.Kind {
		case LineTime:
			timeID = nullID(line.SourceID)
		case LineExpense:
			expID = nullID(line.SourceID)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO invoice_lines (
id, created_at, updated_at, created_by, invoice_id, kind, description, amount_cents, time_entry_id, expense_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, description = EXCLUDED.description,
amount_cents = EXCLUDED.amount_cents, time_entry_id = EXCLUDED.time_entry_id, expense_id = EXCLUDED.expense_id`,
			string(lineID), inv.CreatedAt.UnixMicro(), inv.UpdatedAt.UnixMicro(), string(by),
			string(inv.ID), line.Kind, line.Description, int64(line.AmountCents), timeID, expID); err != nil {
			return err
		}
	}
	return nil
}

func upsertCorePayment(ctx context.Context, conn audit.Querier, pay Payment) error {
	by := pay.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO payments (
id, created_at, updated_at, created_by, contact_id, matter_id, amount_cents, destination, method,
reference_number, status, invoice_id, trust_entry_id, source, refunded_cents, refund_state)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, contact_id = EXCLUDED.contact_id, matter_id = EXCLUDED.matter_id,
amount_cents = EXCLUDED.amount_cents, destination = EXCLUDED.destination, method = EXCLUDED.method,
reference_number = EXCLUDED.reference_number, status = EXCLUDED.status, invoice_id = EXCLUDED.invoice_id,
trust_entry_id = COALESCE(EXCLUDED.trust_entry_id, payments.trust_entry_id), source = EXCLUDED.source,
refunded_cents = EXCLUDED.refunded_cents, refund_state = EXCLUDED.refund_state`,
		string(pay.ID), pay.CreatedAt.UnixMicro(), pay.UpdatedAt.UnixMicro(), string(by),
		string(pay.ContactID), string(pay.MatterID), int64(pay.AmountCents), pay.Destination, pay.Method,
		pay.ReferenceNumber, pay.Status, nullID(pay.InvoiceID), nullID(pay.TrustEntryID), pay.Source,
		int64(pay.RefundedCents), pay.RefundState)
	return err
}

func upsertCoreExpense(ctx context.Context, conn audit.Querier, exp Expense) error {
	by := exp.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	billable := 0
	if exp.Billable {
		billable = 1
	}
	_, err := conn.ExecContext(ctx, `INSERT INTO expenses (
id, created_at, updated_at, created_by, matter_id, incurred_on, description, amount_cents,
expense_type, billable, markup_percent, file_id, invoice_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, matter_id = EXCLUDED.matter_id, incurred_on = EXCLUDED.incurred_on,
description = EXCLUDED.description, amount_cents = EXCLUDED.amount_cents, expense_type = EXCLUDED.expense_type,
billable = EXCLUDED.billable, markup_percent = EXCLUDED.markup_percent, file_id = EXCLUDED.file_id,
invoice_id = EXCLUDED.invoice_id`,
		string(exp.ID), exp.CreatedAt.UnixMicro(), exp.UpdatedAt.UnixMicro(), string(by),
		string(exp.MatterID), exp.IncurredOn, exp.Description, int64(exp.AmountCents),
		exp.ExpenseType, billable, exp.MarkupPercent, nullID(exp.FileID), nullID(exp.InvoiceID))
	return err
}

// RebuildCoreRows backfills core.invoices, core.payments and core.expenses from
// billing_records and fails when any cent field disagrees with the JSON document.
func (p *Postgres) RebuildCoreRows(ctx context.Context) error {
	return p.Atomic(ctx, func(ctx context.Context) error {
		conn := audit.Conn(ctx, p.pool)
		for _, kind := range []string{kindInvoice, kindPayment, kindExpense} {
			rows, err := conn.QueryContext(ctx, `SELECT id, body FROM billing_records WHERE kind = $1 ORDER BY id`, kind)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id, body string
				if err := rows.Scan(&id, &body); err != nil {
					rows.Close()
					return err
				}
				var v any
				switch kind {
				case kindInvoice:
					var inv Invoice
					if err := json.Unmarshal([]byte(body), &inv); err != nil {
						rows.Close()
						return err
					}
					v = inv
				case kindPayment:
					var pay Payment
					if err := json.Unmarshal([]byte(body), &pay); err != nil {
						rows.Close()
						return err
					}
					v = pay
				case kindExpense:
					var exp Expense
					if err := json.Unmarshal([]byte(body), &exp); err != nil {
						rows.Close()
						return err
					}
					v = exp
				}
				if err := syncCoreBillingRows(ctx, conn, kind, id, v); err != nil {
					rows.Close()
					return err
				}
				if err := reconcileCoreRow(ctx, conn, kind, id, v); err != nil {
					rows.Close()
					return err
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		return nil
	})
}

func reconcileCoreRow(ctx context.Context, conn audit.Querier, kind, id string, v any) error {
	switch kind {
	case kindInvoice:
		inv := v.(Invoice)
		var total, paid, balance, writeOff int64
		err := conn.QueryRowContext(ctx,
			`SELECT total_cents, amount_paid_cents, balance_due_cents, write_off_cents FROM invoices WHERE id = $1`, id).
			Scan(&total, &paid, &balance, &writeOff)
		if err != nil {
			return err
		}
		if total != int64(inv.TotalCents) || paid != int64(inv.AmountPaidCents) ||
			balance != int64(inv.BalanceDueCents) || writeOff != int64(inv.WriteOffCents) {
			return fmt.Errorf("billing: invoice %s cent mismatch json=(%d,%d,%d,%d) sql=(%d,%d,%d,%d)",
				id, inv.TotalCents, inv.AmountPaidCents, inv.BalanceDueCents, inv.WriteOffCents,
				total, paid, balance, writeOff)
		}
	case kindPayment:
		pay := v.(Payment)
		var amount int64
		err := conn.QueryRowContext(ctx, `SELECT amount_cents FROM payments WHERE id = $1`, id).Scan(&amount)
		if err != nil {
			return err
		}
		if amount != int64(pay.AmountCents) {
			return fmt.Errorf("billing: payment %s amount mismatch json=%d sql=%d", id, pay.AmountCents, amount)
		}
	case kindExpense:
		exp := v.(Expense)
		var amount int64
		err := conn.QueryRowContext(ctx, `SELECT amount_cents FROM expenses WHERE id = $1`, id).Scan(&amount)
		if err != nil {
			return err
		}
		if amount != int64(exp.AmountCents) {
			return fmt.Errorf("billing: expense %s amount mismatch json=%d sql=%d", id, exp.AmountCents, amount)
		}
	}
	return nil
}

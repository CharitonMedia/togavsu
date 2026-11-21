package billing

import (
	"context"
	"database/sql"
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
	imported := expenseBillableFlag(inv.Imported)
	_, err := conn.ExecContext(ctx, `INSERT INTO invoices (
id, created_at, updated_at, created_by, matter_id, number, issued_on, status,
subtotal_cents, adjustment_cents, adjustment_reason, interest_cents, total_cents,
amount_paid_cents, balance_due_cents, write_off_cents, write_off_reason, void_reason,
dispute_status, dispute_notes, sent_at, paid_at, client_message, internal_notes,
imported, import_source, external_ref, document_of_record_file_id, document_of_record_version_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, matter_id = EXCLUDED.matter_id, number = EXCLUDED.number,
issued_on = EXCLUDED.issued_on, status = EXCLUDED.status, subtotal_cents = EXCLUDED.subtotal_cents,
adjustment_cents = EXCLUDED.adjustment_cents, adjustment_reason = EXCLUDED.adjustment_reason,
interest_cents = EXCLUDED.interest_cents, total_cents = EXCLUDED.total_cents,
amount_paid_cents = EXCLUDED.amount_paid_cents, balance_due_cents = EXCLUDED.balance_due_cents,
write_off_cents = EXCLUDED.write_off_cents, write_off_reason = EXCLUDED.write_off_reason,
void_reason = EXCLUDED.void_reason, dispute_status = EXCLUDED.dispute_status,
dispute_notes = EXCLUDED.dispute_notes, sent_at = EXCLUDED.sent_at, paid_at = EXCLUDED.paid_at,
client_message = EXCLUDED.client_message, internal_notes = EXCLUDED.internal_notes,
imported = EXCLUDED.imported, import_source = EXCLUDED.import_source, external_ref = EXCLUDED.external_ref,
document_of_record_file_id = EXCLUDED.document_of_record_file_id,
document_of_record_version_id = EXCLUDED.document_of_record_version_id`,
		string(inv.ID), inv.CreatedAt.UnixMicro(), inv.UpdatedAt.UnixMicro(), string(by),
		string(inv.MatterID), inv.Number, inv.IssuedOn, inv.Status,
		int64(inv.SubtotalCents), int64(inv.AdjustmentCents), inv.AdjustmentReason, int64(inv.InterestCents),
		int64(inv.TotalCents), int64(inv.AmountPaidCents), int64(inv.BalanceDueCents),
		int64(inv.WriteOffCents), inv.WriteOffReason, inv.VoidReason, inv.DisputeStatus, inv.DisputeNotes,
		inv.SentAt, inv.PaidAt, inv.ClientMessage, inv.InternalNotes,
		imported, inv.ImportSource, inv.ExternalRef, string(inv.DocumentOfRecordFileID), string(inv.DocumentOfRecordVersionID))
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM invoice_lines WHERE invoice_id = $1`, string(inv.ID)); err != nil {
		return err
	}
	seenLineIDs := make(map[string]struct{}, len(inv.Lines))
	for i, line := range inv.Lines {
		lineID := coreInvoiceLineID(inv, i, line)
		if _, dup := seenLineIDs[string(lineID)]; dup {
			return fmt.Errorf("billing: invoice %s duplicate invoice_lines id %s", inv.ID, lineID)
		}
		seenLineIDs[string(lineID)] = struct{}{}
		var timeID, expID any
		switch line.Kind {
		case LineTime:
			timeID = nullID(line.SourceID)
		case LineExpense:
			expID = nullID(line.SourceID)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO invoice_lines (
id, created_at, updated_at, created_by, invoice_id, kind, description, amount_cents, time_entry_id, expense_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			string(lineID), inv.CreatedAt.UnixMicro(), inv.UpdatedAt.UnixMicro(), string(by),
			string(inv.ID), line.Kind, line.Description, int64(line.AmountCents), timeID, expID); err != nil {
			return err
		}
	}
	return nil
}

// coreInvoiceLineID is the stable section-5 row id for one JSON invoice line.
// Source-backed lines use invoice:kind:source; others use invoice:line:<index>.
func coreInvoiceLineID(inv Invoice, i int, line Line) domain.ID {
	if !line.SourceID.IsZero() {
		return domain.ID(fmt.Sprintf("%s:%s:%s", inv.ID, line.Kind, line.SourceID))
	}
	return domain.ID(fmt.Sprintf("%s:line:%d", inv.ID, i))
}

func upsertCorePayment(ctx context.Context, conn audit.Querier, pay Payment) error {
	by := pay.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	refundState := pay.RefundState
	if refundState == "" {
		refundState = "none"
	}
	imported := expenseBillableFlag(pay.Imported)
	_, err := conn.ExecContext(ctx, `INSERT INTO payments (
id, created_at, updated_at, created_by, contact_id, matter_id, amount_cents, destination, method,
reference_number, status, invoice_id, trust_entry_id, source, refunded_cents, refund_state, imported)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, contact_id = EXCLUDED.contact_id, matter_id = EXCLUDED.matter_id,
amount_cents = EXCLUDED.amount_cents, destination = EXCLUDED.destination, method = EXCLUDED.method,
reference_number = EXCLUDED.reference_number, status = EXCLUDED.status, invoice_id = EXCLUDED.invoice_id,
trust_entry_id = COALESCE(EXCLUDED.trust_entry_id, payments.trust_entry_id), source = EXCLUDED.source,
refunded_cents = EXCLUDED.refunded_cents, refund_state = EXCLUDED.refund_state, imported = EXCLUDED.imported`,
		string(pay.ID), pay.CreatedAt.UnixMicro(), pay.UpdatedAt.UnixMicro(), string(by),
		string(pay.ContactID), nullID(pay.MatterID), int64(pay.AmountCents), pay.Destination, pay.Method,
		pay.ReferenceNumber, pay.Status, nullID(pay.InvoiceID), nullID(pay.TrustEntryID), pay.Source,
		int64(pay.RefundedCents), refundState, imported)
	return err
}

func upsertCoreExpense(ctx context.Context, conn audit.Querier, exp Expense) error {
	by := exp.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
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
		exp.ExpenseType, expenseBillableFlag(exp.Billable), exp.MarkupPercent, nullID(exp.FileID), nullID(exp.InvoiceID))
	return err
}

// expenseBillableFlag stores a boolean expense column as 0 or 1.
func expenseBillableFlag(v bool) int {
	if v {
		return 1
	}
	return 0
}

// RebuildCoreRows backfills core.invoices, core.payments and core.expenses from
// billing_records once (billing_core_rows_backfill marker, migration 1401) and fails
// when any cent field disagrees with the JSON document after the rewrite.
// Ongoing writes stay in step through syncCoreBillingRows in Put.
//
// ponytail: ceiling: the one-time rewrite reconciles cents it has just written, so it
// catches write-path bugs in syncCoreBillingRows rather than pre-existing drift that
// Put already healed; a constraint error on that first pass still stops core start.
// Upgrade path: a SQL migration that copies billing_records into the section-5 tables
// with independent checksum verification against the JSON, then drop the Go rewrite.
// trigger: a firm reports a core that will not start on RebuildCoreRows, or a second
// core process needs the backfill without serializing on this process's first start.
func (p *Postgres) RebuildCoreRows(ctx context.Context) error {
	return p.Atomic(ctx, func(ctx context.Context) error {
		conn := audit.Conn(ctx, p.pool)
		if err := p.pinImportedDocuments(ctx); err != nil {
			return err
		}
		var done int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM billing_core_rows_backfill`).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			return nil
		}
		for _, kind := range []string{kindInvoice, kindPayment, kindExpense} {
			rows, err := conn.QueryContext(ctx, `SELECT id, body FROM billing_records WHERE kind = $1 ORDER BY id`, kind)
			if err != nil {
				return err
			}
			type doc struct {
				id, body string
			}
			var docs []doc
			for rows.Next() {
				var d doc
				if err := rows.Scan(&d.id, &d.body); err != nil {
					rows.Close()
					return err
				}
				docs = append(docs, d)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			// Load first, then write: one connection cannot Exec while Rows are open.
			for _, d := range docs {
				var v any
				switch kind {
				case kindInvoice:
					var inv Invoice
					if err := json.Unmarshal([]byte(d.body), &inv); err != nil {
						return err
					}
					v = inv
				case kindPayment:
					var pay Payment
					if err := json.Unmarshal([]byte(d.body), &pay); err != nil {
						return err
					}
					v = pay
				case kindExpense:
					var exp Expense
					if err := json.Unmarshal([]byte(d.body), &exp); err != nil {
						return err
					}
					v = exp
				}
				if err := syncCoreBillingRows(ctx, conn, kind, d.id, v); err != nil {
					return err
				}
				if err := reconcileCoreRow(ctx, conn, kind, d.id, v); err != nil {
					return err
				}
			}
		}
		_, err := conn.ExecContext(ctx, `INSERT INTO billing_core_rows_backfill (done_at) VALUES (now())`)
		return err
	})
}

func reconcileCoreRow(ctx context.Context, conn audit.Querier, kind, id string, v any) error {
	switch kind {
	case kindInvoice:
		inv := v.(Invoice)
		var subtotal, adjustment, interest, total, paid, balance, writeOff int64
		err := conn.QueryRowContext(ctx,
			`SELECT subtotal_cents, adjustment_cents, interest_cents, total_cents,
amount_paid_cents, balance_due_cents, write_off_cents FROM invoices WHERE id = $1`, id).
			Scan(&subtotal, &adjustment, &interest, &total, &paid, &balance, &writeOff)
		if err != nil {
			return err
		}
		if subtotal != int64(inv.SubtotalCents) || adjustment != int64(inv.AdjustmentCents) ||
			interest != int64(inv.InterestCents) || total != int64(inv.TotalCents) ||
			paid != int64(inv.AmountPaidCents) || balance != int64(inv.BalanceDueCents) ||
			writeOff != int64(inv.WriteOffCents) {
			return fmt.Errorf("billing: invoice %s cent mismatch json=(%d,%d,%d,%d,%d,%d,%d) sql=(%d,%d,%d,%d,%d,%d,%d)",
				id,
				inv.SubtotalCents, inv.AdjustmentCents, inv.InterestCents, inv.TotalCents,
				inv.AmountPaidCents, inv.BalanceDueCents, inv.WriteOffCents,
				subtotal, adjustment, interest, total, paid, balance, writeOff)
		}
		if err := reconcileInvoiceLines(ctx, conn, inv); err != nil {
			return err
		}
	case kindPayment:
		pay := v.(Payment)
		var amount, refunded int64
		var status string
		var invoiceID sql.NullString
		err := conn.QueryRowContext(ctx,
			`SELECT amount_cents, refunded_cents, status, invoice_id FROM payments WHERE id = $1`, id).
			Scan(&amount, &refunded, &status, &invoiceID)
		if err != nil {
			return err
		}
		wantInvoice := string(pay.InvoiceID)
		gotInvoice := ""
		if invoiceID.Valid {
			gotInvoice = invoiceID.String
		}
		if amount != int64(pay.AmountCents) || refunded != int64(pay.RefundedCents) ||
			status != pay.Status || gotInvoice != wantInvoice {
			return fmt.Errorf("billing: payment %s mismatch json=(amount=%d refunded=%d status=%s invoice=%s) sql=(amount=%d refunded=%d status=%s invoice=%s)",
				id, pay.AmountCents, pay.RefundedCents, pay.Status, wantInvoice,
				amount, refunded, status, gotInvoice)
		}
	case kindExpense:
		exp := v.(Expense)
		var amount int64
		var markup int
		err := conn.QueryRowContext(ctx,
			`SELECT amount_cents, markup_percent FROM expenses WHERE id = $1`, id).
			Scan(&amount, &markup)
		if err != nil {
			return err
		}
		if amount != int64(exp.AmountCents) || markup != exp.MarkupPercent {
			return fmt.Errorf("billing: expense %s mismatch json=(amount=%d markup=%d) sql=(amount=%d markup=%d)",
				id, exp.AmountCents, exp.MarkupPercent, amount, markup)
		}
		// Billed cents (amount plus markup) must also fit; a corrupt markup would still
		// leave amount_cents alone but change what modules bill from the row.
		if _, err := expenseAmount(exp.AmountCents, exp.MarkupPercent); err != nil {
			return fmt.Errorf("billing: expense %s billed cents: %w", id, err)
		}
	}
	return nil
}

// reconcileInvoiceLines checks core.invoice_lines against inv.Lines: row count, sum of
// amount_cents, and each stable line id's amount. A silent ON CONFLICT overwrite that
// drops a line's cents fails the count or per-line check.
func reconcileInvoiceLines(ctx context.Context, conn audit.Querier, inv Invoice) error {
	var count int
	var sum int64
	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount_cents), 0) FROM invoice_lines WHERE invoice_id = $1`,
		string(inv.ID)).Scan(&count, &sum)
	if err != nil {
		return err
	}
	var wantSum int64
	seen := make(map[string]struct{}, len(inv.Lines))
	for i, line := range inv.Lines {
		wantSum += int64(line.AmountCents)
		lineID := string(coreInvoiceLineID(inv, i, line))
		if _, dup := seen[lineID]; dup {
			return fmt.Errorf("billing: invoice %s invoice_lines mismatch duplicate line id %s", inv.ID, lineID)
		}
		seen[lineID] = struct{}{}
	}
	if count != len(inv.Lines) || sum != wantSum {
		return fmt.Errorf("billing: invoice %s invoice_lines mismatch json=(count=%d sum=%d) sql=(count=%d sum=%d)",
			inv.ID, len(inv.Lines), wantSum, count, sum)
	}
	for i, line := range inv.Lines {
		lineID := coreInvoiceLineID(inv, i, line)
		var amt int64
		err := conn.QueryRowContext(ctx,
			`SELECT amount_cents FROM invoice_lines WHERE id = $1 AND invoice_id = $2`,
			string(lineID), string(inv.ID)).Scan(&amt)
		if err != nil {
			return fmt.Errorf("billing: invoice %s invoice_lines mismatch line %s: %w", inv.ID, lineID, err)
		}
		if amt != int64(line.AmountCents) {
			return fmt.Errorf("billing: invoice %s invoice_lines mismatch line %s json=%d sql=%d",
				inv.ID, lineID, line.AmountCents, amt)
		}
	}
	return nil
}

// ReconcileCoreRowForTest exposes cent reconciliation for Postgres tests (CORE-36).
func ReconcileCoreRowForTest(ctx context.Context, conn audit.Querier, kind, id string, v any) error {
	return reconcileCoreRow(ctx, conn, kind, id, v)
}

// pinImportedDocuments upgrades invoices imported before version pinning existed.
// Select the last version saved by import time, never the current version at upgrade.
// Missing historical bytes remain unavailable rather than serving a replacement.
func (p *Postgres) pinImportedDocuments(ctx context.Context) error {
	invoices, err := listOf[Invoice](ctx, p, kindInvoice)
	if err != nil {
		return err
	}
	for _, inv := range invoices {
		if !inv.Imported || inv.DocumentOfRecordFileID.IsZero() || !inv.DocumentOfRecordVersionID.IsZero() {
			continue
		}
		err := audit.Conn(ctx, p.pool).QueryRowContext(ctx, `SELECT id FROM file_versions WHERE file_id = $1 AND created_at <= $2 ORDER BY number DESC LIMIT 1`, string(inv.DocumentOfRecordFileID), inv.CreatedAt.UnixMicro()).Scan(&inv.DocumentOfRecordVersionID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if err := p.Put(ctx, kindInvoice, string(inv.ID), inv); err != nil {
			return err
		}
	}
	return nil
}

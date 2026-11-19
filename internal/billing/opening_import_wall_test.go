package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// openingImportItem is one CORE-37 opening InvoiceImport for service-level tests.
func openingImportItem(matter domain.ID, number, ref string, due domain.Cents) InvoiceImport {
	issued := "2026-01-15"
	return InvoiceImport{
		MatterID: matter, Number: number, IssuedOn: issued, Status: StatusSent,
		Lines: []LineInput{{
			Kind: LineAdjustment, Description: "Balance forward as of " + issued, AmountCents: cents(due),
		}},
		SubtotalCents: due, TotalCents: due, BalanceDueCents: due,
		Source: ImportSourceAPI, ExternalRef: ref,
	}
}

// TestBulkInvoicesPreviewAlreadyImportedAccessParity: preview runs authorizeImport and
// the matter/wall check before marking an already-imported external_ref skipped, so a
// walled or unauthorized row reports the same error in preview as in commit (Codex P2).
func TestBulkInvoicesPreviewAlreadyImportedAccessParity(t *testing.T) {
	f := newFx(t)
	defer overrideSettings(f.svc, settingOverride{"billing.invoice_number_start": "9000"})()

	walled, _ := f.matter("Walled opening", "hourly", 0, 0)
	open, _ := f.matter("Open opening", "hourly", 0, 0)
	if _, err := f.svc.BulkInvoices(f.ctx, f.attorney, []InvoiceImport{
		openingImportItem(walled.ID, "1001", "ref-walled-seed", 100_00),
	}); err != nil {
		t.Fatalf("seed walled import: %v", err)
	}
	if _, err := f.svc.BulkInvoices(f.ctx, f.attorney, []InvoiceImport{
		openingImportItem(open.ID, "1002", "ref-open-seed", 80_00),
	}); err != nil {
		t.Fatalf("seed open import: %v", err)
	}
	if err := f.ps.SetWall(f.ctx, f.admin, walled.ID, []domain.ID{f.attorney.UserID}); err != nil {
		t.Fatal(err)
	}

	sameDenial := func(name string, p domain.Principal, item InvoiceImport, want string) {
		t.Helper()
		preview, err := f.svc.BulkInvoicesPreview(f.ctx, p, []InvoiceImport{item})
		if err != nil {
			t.Fatalf("%s preview call: %v", name, err)
		}
		if len(preview) != 1 {
			t.Fatalf("%s preview rows: %d", name, len(preview))
		}
		row := preview[0]
		if row.Skipped || row.PreviewOK || row.PreviewError == "" {
			t.Fatalf("%s preview must refuse (want %s), got skipped=%v ok=%v err=%q",
				name, want, row.Skipped, row.PreviewOK, row.PreviewError)
		}
		_, _, commitErr := f.svc.BulkInvoicesDetailed(f.ctx, p, []InvoiceImport{item})
		if commitErr == nil {
			t.Fatalf("%s commit unexpectedly succeeded", name)
		}
		if got := code(commitErr); got != want {
			t.Fatalf("%s commit code %q want %s (%v)", name, got, want, commitErr)
		}
		// Preview and commit both fail at authorizeImport for these rows.
		if got := code(f.svc.authorizeImport(f.ctx, p, item)); got != want {
			t.Fatalf("%s authorizeImport %q want %s", name, got, want)
		}
	}

	// Staff outside the wall: already-imported ref on the walled matter → not_found both ways.
	sameDenial("walled staff", f.staff, openingImportItem(walled.ID, "1001", "ref-walled-seed", 100_00), domain.CodeNotFound)
	// Accountant has no invoice write: already-imported ref on an open matter → forbidden both ways.
	sameDenial("unauthorized accountant", f.accountant, openingImportItem(open.ID, "1002", "ref-open-seed", 80_00), domain.CodeForbidden)

	// Control: attorney on the wall still sees skipped in preview and commit, not an error.
	prev, err := f.svc.BulkInvoicesPreview(f.ctx, f.attorney, []InvoiceImport{
		openingImportItem(walled.ID, "1001", "ref-walled-seed", 100_00),
	})
	if err != nil || len(prev) != 1 || !prev[0].Skipped || !prev[0].PreviewOK {
		t.Fatalf("wall member preview skip: err=%v %+v", err, prev)
	}
	_, skipped, err := f.svc.BulkInvoicesDetailed(f.ctx, f.attorney, []InvoiceImport{
		openingImportItem(walled.ID, "1001", "ref-walled-seed", 100_00),
	})
	if err != nil || len(skipped) != 1 || !skipped[0].Skipped {
		t.Fatalf("wall member commit skip: err=%v %+v", err, skipped)
	}
}

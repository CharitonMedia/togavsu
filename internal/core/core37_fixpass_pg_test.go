package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Exercise actual stores, file metadata, audit, events and outboxes, with every row
// reaching the same commit function. Staff can import legacy drafts; sent rows require
// approval, so one mixed batch also exercises a row-specific authorization refusal.
func TestCORE37PreviewCommitResultsAndRollback(t *testing.T) {
	f := newFin(t)
	ctx := context.Background()
	matter, _ := f.matter("Import parity", "parity@example.test", nil)
	wall, _ := f.matter("Import wall", "wall@example.test", nil)
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 9000})
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+wall+"/wall", map[string]any{"user_ids": []string{f.attyID}})
	staffID := str(t, f.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	staff := domain.Principal{UserID: domain.ID(staffID), Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}
	attorney := domain.Principal{UserID: domain.ID(f.attyID), Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	item := func(n, ref string) billing.InvoiceImport {
		return billing.OpeningInvoiceItem(domain.ID(matter), n, "2026-01-15", "", 10000, 0, "", billing.ImportSourceCSV, ref, "")
	}
	if _, err := f.app.Billing.BulkInvoices(ctx, attorney, []billing.InvoiceImport{item("1000", "earlier-run")}); err != nil {
		t.Fatal(err)
	}
	fileID := f.uploadPDF(matter, "parity.pdf", []byte("%PDF-1.4 parity original"))
	// A valid opening preview really shares the document and creates the payment inside
	// its transaction, but none of these changes may survive rollback.
	paid := item("1001", "preview-paid")
	paid.DocumentOfRecordFileID = domain.ID(fileID)
	paid.AmountPaidCents = 2500
	paid.BalanceDueCents = 7500
	counts := func() []int {
		var got []int
		for _, table := range []string{"billing_records", "invoices", "invoice_lines", "payments", "events", "audit_log", "notifications", "mail_outbox", "module_deliveries"} {
			got = append(got, f.count("SELECT COUNT(*) FROM core."+table))
		}
		return got
	}
	before := counts()
	preview, err := f.app.Billing.BulkInvoicesPreview(ctx, attorney, []billing.InvoiceImport{paid})
	if err != nil || len(preview) != 1 || !preview[0].PreviewOK {
		t.Fatalf("valid preview: %+v %v", preview, err)
	}
	if got := counts(); !reflect.DeepEqual(before, got) {
		t.Fatalf("preview persisted rows/events/outbox: before %v after %v", before, got)
	}
	file := f.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID, nil)
	if file.json["shared_with_client"] != false {
		t.Fatalf("preview persisted sharing: %s", file.body)
	}

	good := item("1002", "")
	good.Source = ""
	good.Status = billing.StatusDraft
	skipped := item("1000", "earlier-run")
	skipped.Status = billing.StatusDraft
	walled := good
	walled.MatterID = domain.ID(wall)
	walled.Number = "1003"
	unauthorized := item("1004", "unauthorized")
	missing := item("1005", "within-file")
	missing.Status = billing.StatusDraft
	missing.DocumentFileName = "missing.pdf"
	email := item("1006", "email")
	email.Status = billing.StatusDraft
	email.ContactEmail = "wrong@example.test"
	amount := good
	amount.Number = "1007"
	amount.TotalCents = 9999
	date := good
	date.Number = "1008"
	date.IssuedOn = "bad-date"
	duplicate := missing
	duplicate.Number = "1009"
	duplicate.DocumentFileName = ""
	mixed := []billing.InvoiceImport{good, skipped, walled, unauthorized, missing, email, amount, date, duplicate}
	prev, err := f.app.Billing.BulkInvoicesPreview(ctx, staff, mixed)
	if err != nil {
		t.Fatal(err)
	}
	written, committed, err := f.app.Billing.BulkInvoicesDetailed(ctx, staff, mixed)
	if err == nil || len(written) != 0 {
		t.Fatalf("bad batch wrote: %v %v", written, err)
	}
	if !reflect.DeepEqual(prev, committed) {
		t.Fatalf("preview/commit differ:\n%+v\n%+v", prev, committed)
	}
	if len(prev) != len(mixed) || !prev[0].PreviewOK || !prev[1].Skipped {
		t.Fatalf("mixed results: %+v", prev)
	}
	for i, want := range []string{"", "", "not found", "", "document_file_name", "contact_email", "total_cents", "issued_on", "duplicate external_ref"} {
		if prev[i].Row != i+1 || prev[i].Index != i {
			t.Fatalf("row number %+v", prev[i])
		}
		if i >= 2 && (prev[i].PreviewOK || !strings.Contains(prev[i].PreviewError, fmt.Sprintf("row %d:", i+1)) || !strings.Contains(strings.ToLower(prev[i].PreviewError), want)) {
			t.Fatalf("row %d want %q: %+v", i+1, want, prev[i])
		}
	}
	if got := counts(); !reflect.DeepEqual(before, got) {
		t.Fatalf("rejected batch persisted: %v -> %v", before, got)
	}
	// A second occurrence of an otherwise valid opening row is an error, never a skip.
	dupBatch := []billing.InvoiceImport{item("1010", "duplicate-valid"), item("1011", "duplicate-valid")}
	prev, _ = f.app.Billing.BulkInvoicesPreview(ctx, attorney, dupBatch)
	_, committed, err = f.app.Billing.BulkInvoicesDetailed(ctx, attorney, dupBatch)
	if err == nil || !reflect.DeepEqual(prev, committed) || !prev[0].PreviewOK || prev[1].Skipped || !strings.Contains(prev[1].PreviewError, "duplicate external_ref") {
		t.Fatalf("duplicate parity: %+v %+v %v", prev, committed, err)
	}
	// HTTP results must match too, even for the rejected commit. An idempotency key on
	// preview cannot persist a cached response.
	bodies := []map[string]any{openingItem(matter, "1012", "2026-01-15", 10000, 0, "csv", "http-dup"), openingItem(matter, "1013", "2026-01-15", 10000, 0, "csv", "http-dup")}
	raw, err := json.Marshal(map[string]any{"preview": true, "items": bodies})
	if err != nil {
		t.Fatal(err)
	}
	prevHTTP := f.atty.raw("POST", "/api/v1/invoices:bulk", raw, "Content-Type", "application/json", "Idempotency-Key", "preview-no-write")
	commitHTTP := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": bodies})
	if prevHTTP.code != http.StatusOK || commitHTTP.code != http.StatusUnprocessableEntity || !reflect.DeepEqual(prevHTTP.json["results"], commitHTTP.json["results"]) {
		t.Fatalf("HTTP parity: %s / %s", prevHTTP.body, commitHTTP.body)
	}
	if got := counts(); !reflect.DeepEqual(before, got) {
		t.Fatalf("HTTP preview/rejection persisted: %v -> %v", before, got)
	}
	// A clean preview and successful commit also return identical results. Only commit
	// leaves the invoice, imported payment and document sharing behind.
	cleanPreview, err := f.app.Billing.BulkInvoicesPreview(ctx, attorney, []billing.InvoiceImport{paid})
	if err != nil {
		t.Fatal(err)
	}
	saved, cleanCommit, err := f.app.Billing.BulkInvoicesDetailed(ctx, attorney, []billing.InvoiceImport{paid})
	if err != nil || len(saved) != 1 || !reflect.DeepEqual(cleanPreview, cleanCommit) {
		t.Fatalf("clean parity: %+v %+v %v", cleanPreview, cleanCommit, err)
	}
	if f.count("SELECT COUNT(*) FROM core.payments WHERE invoice_id = $1 AND imported = 1", saved[0].ID) != 1 {
		t.Fatal("clean commit lost imported payment")
	}

}

func TestCORE37CSVBadAmountsStayOnTheirRows(t *testing.T) {
	parsed, err := parseOpeningCSV(strings.NewReader("matter_id,number,issued_on,amount_due,amount_paid\nm,1,2026-01-15,10.00,0\nm,2,2026-01-15,bad,0\nm,3,2026-01-15,10.00,bad\nm,4,2026-01-15,11.00,0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.rows) != 4 || parsed.rows[0].ParseError != "" || parsed.rows[3].ParseError != "" || !strings.Contains(parsed.rows[1].ParseError, "amount_due") || !strings.Contains(parsed.rows[2].ParseError, "amount_paid") {
		t.Fatalf("CSV amount errors: %+v", parsed.rows)
	}
	// The admin preview preserves original one-based rows even though only valid rows
	// are passed to the billing transaction; covered end to end in the 500-row test.
}

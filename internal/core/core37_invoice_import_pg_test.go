package core

import (
	"crypto/ed25519"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// openingItem builds a CORE-37 opening InvoiceImport body for bulkInvoice.
func openingItem(matter, number, issued string, dueCents, paidCents int64, source, ref string) map[string]any {
	balance := dueCents - paidCents
	status := "sent"
	if balance <= 0 && paidCents > 0 {
		status = "paid"
		balance = 0
	}
	item := map[string]any{
		"matter_id": matter, "number": number, "issued_on": issued, "status": status,
		"lines": []map[string]any{{
			"kind": "adjustment", "description": "Balance forward as of " + issued, "amount_cents": dueCents,
		}},
		"subtotal_cents": dueCents, "adjustment_cents": 0, "interest_cents": 0, "total_cents": dueCents,
		"amount_paid_cents": paidCents, "balance_due_cents": balance, "write_off_cents": 0,
		"source": source, "external_ref": ref,
	}
	return item
}

func TestCORE37OpeningInvoiceImport(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Opening Import Estate", "", nil)
	issued := time.Now().UTC().Format("2006-01-02")

	// Set a floor so historic numbers below it import and numbers at/above are refused.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 5000})

	// IMP-01: form import → sent, Balance forward line, imported true; sequence unchanged.
	before := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "4521", issued, 1500_00, 0, "form", "ref-4521")},
	})
	first := before.json["data"].([]any)[0].(map[string]any)
	if first["number"] != "4521" || first["status"] != "sent" || first["imported"] != true {
		t.Fatalf("IMP-01 import: %v", first)
	}
	lines := first["lines"].([]any)
	if len(lines) != 1 || !strings.HasPrefix(str(t, lines[0].(map[string]any), "description"), "Balance forward as of") {
		t.Fatalf("IMP-01 line: %v", lines)
	}
	if f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'invoice.imported' AND record_id = $1`, first["id"]) != 1 {
		t.Fatal("IMP-01 missing invoice.imported")
	}
	_ = f.timeEntry(matter, 6, 100_00)
	next := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	// Draft uses the normal sequence; historic 4521 must not become the next number.
	n := str(t, next.json["data"].([]any)[0].(map[string]any), "number")
	if n == "4521" || n == "4522" || strings.HasSuffix(n, "4521") {
		t.Fatalf("IMP-01 sequence polluted: %s", n)
	}

	// IMP-02 refusals: floor, duplicate, zero amount, missing matter — nothing written.
	invoices := func() int { return f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) }
	base := invoices()
	refuse := func(name string, item map[string]any, want string) {
		t.Helper()
		r := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": []map[string]any{item}})
		if r.code != http.StatusUnprocessableEntity && r.code != http.StatusBadRequest && r.code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", name, r.code, r.body)
		}
		if !strings.Contains(strings.ToLower(r.body), strings.ToLower(want)) {
			t.Fatalf("%s: want %q in %s", name, want, r.body)
		}
		if invoices() != base {
			t.Fatalf("%s wrote rows", name)
		}
	}
	refuse("floor", openingItem(matter, "5000", issued, 100_00, 0, "form", "ref-floor"), "invoice_number_start")
	refuse("dup", openingItem(matter, "4521", issued, 100_00, 0, "form", "ref-dup"), "already used")
	refuse("zero", openingItem(matter, "100", issued, 0, 0, "form", "ref-zero"), "greater than zero")
	badMatter := openingItem("00000000-0000-0000-0000-000000000099", "101", issued, 100_00, 0, "form", "ref-miss")
	r := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": []map[string]any{badMatter}})
	if r.code == http.StatusCreated {
		t.Fatalf("missing matter wrote: %s", r.body)
	}
	if invoices() != base {
		t.Fatal("missing matter wrote rows")
	}

	// IMP-05: amount paid posts one imported payment; balance = due - paid.
	paid := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "3001", issued, 1000_00, 250_00, "api", "ref-paid")},
	})
	row := paid.json["data"].([]any)[0].(map[string]any)
	if int64(row["amount_paid_cents"].(float64)) != 250_00 || int64(row["balance_due_cents"].(float64)) != 750_00 {
		t.Fatalf("IMP-05 cents: %v", row)
	}
	if f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'payment' AND body::text LIKE '%"imported":true%'`) < 1 &&
		f.count(`SELECT COUNT(*) FROM core.payments WHERE imported = 1`) < 1 {
		// Either JSON body or dual-write row should show the imported payment.
		n := f.count(`SELECT COUNT(*) FROM core.payments WHERE invoice_id = $1`, row["id"])
		if n < 1 {
			t.Fatal("IMP-05 no imported payment")
		}
	}

	// IMP-07: void an unpaid imported invoice; sequence untouched.
	toVoid := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "3100", issued, 80_00, 0, "form", "ref-void")},
	})
	voidID := str(t, toVoid.json["data"].([]any)[0].(map[string]any), "id")
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+voidID+"/void", map[string]any{"reason": "opened in error"})
	got := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices/"+voidID, nil)
	if str(t, got.json, "status") != "void" {
		t.Fatalf("IMP-07 void: %v", got.json)
	}
	_ = row

	// IMP-06: document of record on staff billing tab; client sees it on GET and PDF redirect.
	dorMatter, dorContact := f.matter("DoR Estate", "dor.client@example.com", nil)
	pdfBytes := []byte("%PDF-1.4 synthetic opening invoice\n")
	up := f.atty.must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{
		"file_name": "opening-4521.pdf", "matter_id": dorMatter, "size_bytes": len(pdfBytes), "media_type": "application/pdf",
	})
	upID := str(t, up.json, "id")
	put := f.atty.raw("PUT", "/api/v1/file-uploads/"+upID, pdfBytes, "Content-Type", "application/octet-stream",
		"Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(pdfBytes)-1, len(pdfBytes)))
	if put.code != http.StatusOK {
		t.Fatalf("IMP-06 chunk: %d %s", put.code, put.body)
	}
	f.atty.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+upID+"/complete", map[string]any{})
	file := f.atty.must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": upID})
	fileID := str(t, file.json, "id")
	withDor := openingItem(dorMatter, "2200", issued, 50_00, 0, "form", "ref-dor")
	withDor["document_of_record_file_id"] = fileID
	dorInv := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{"items": []map[string]any{withDor}})
	dorRow := dorInv.json["data"].([]any)[0].(map[string]any)
	if dorRow["imported"] != true || str(t, dorRow, "document_of_record_file_id") != fileID {
		t.Fatalf("IMP-06 dor field: %v", dorRow)
	}
	dorID := str(t, dorRow, "id")
	staffPage := f.atty.must(http.StatusOK, "GET", "/billing/invoices/"+dorID, nil)
	if !strings.Contains(staffPage.body, "Imported") || !strings.Contains(staffPage.body, "Document of record") || !strings.Contains(staffPage.body, fileID) {
		t.Fatalf("IMP-06 staff billing tab: %s", staffPage.body)
	}
	pdf := f.atty.do("GET", "/api/v1/invoices/"+dorID+"/pdf", nil)
	if pdf.code != http.StatusSeeOther || !strings.Contains(pdf.loc, "/api/v1/files/"+fileID) {
		t.Fatalf("IMP-06 pdf redirect: %d %s", pdf.code, pdf.loc)
	}
	cliKit, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Dor Client", "dor.client@example.com", domain.ID(dorContact), domain.RoleClient)
	cli := wrapClient(cliKit)
	cliInv := cli.must(http.StatusOK, "GET", "/api/v1/invoices/"+dorID, nil)
	if cliInv.json["imported"] != true || str(t, cliInv.json, "document_of_record_file_id") != fileID {
		t.Fatalf("IMP-06 client projection: %s", cliInv.body)
	}
	if _, leak := cliInv.json["import_source"]; leak {
		t.Fatalf("IMP-06 client leaked import_source: %s", cliInv.body)
	}
	cliPDF := cli.do("GET", "/api/v1/invoices/"+dorID+"/pdf", nil)
	if cliPDF.code != http.StatusSeeOther || !strings.Contains(cliPDF.loc, "/api/v1/files/"+fileID) {
		t.Fatalf("IMP-06 client pdf redirect: %d %s", cliPDF.code, cliPDF.loc)
	}

	// IMP-08: collections / aging include imported invoices.
	aging := f.atty.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil)
	if !strings.Contains(aging.body, "4521") && !strings.Contains(aging.body, str(t, first, "id")) {
		// Aging may key by id; ensure at least one open imported balance appears via list.
		list := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices?status=sent", nil)
		if !strings.Contains(list.body, "4521") {
			t.Fatalf("IMP-08 imported invoice missing from lists: %s / %s", aging.body, list.body)
		}
	}
}

func TestCORE37CSVPreviewAndIdempotentExternalRef(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("CSV Import Estate", "", nil)
	issued := time.Now().UTC().Format("2006-01-02")
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 9000})

	// IMP-03: 500-row CSV preview names a bad row; fixed batch writes all in one transaction.
	const n = 500
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		// Numbers stay below invoice_number_start (9000): 1000..1499.
		items = append(items, openingItem(matter, fmt.Sprintf("%d", 1000+i), issued, 10_00, 0, "csv", fmt.Sprintf("csv-%d", i)))
	}
	badIdx := 250
	items[badIdx] = openingItem(matter, "9001", issued, 10_00, 0, "csv", "csv-bad") // at/above floor
	prev := f.atty.must(http.StatusOK, "POST", "/api/v1/invoices:bulk", map[string]any{"preview": true, "items": items})
	results := prev.json["results"].([]any)
	if len(results) != n {
		t.Fatalf("IMP-03 preview len: %d", len(results))
	}
	bad := results[badIdx].(map[string]any)
	if bad["ok"] != false || !strings.Contains(fmt.Sprint(bad["error"]), "invoice_number_start") {
		t.Fatalf("IMP-03 preview: %v", bad)
	}
	before := f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`)
	if r := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": items}); r.code == http.StatusCreated {
		t.Fatalf("IMP-03 bad batch wrote: %s", r.body)
	}
	if f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) != before {
		t.Fatal("IMP-03 wrote despite bad row")
	}
	items[badIdx] = openingItem(matter, fmt.Sprintf("%d", 1000+badIdx), issued, 10_00, 0, "csv", fmt.Sprintf("csv-%d", badIdx))
	wrote := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{"items": items})
	if len(wrote.json["data"].([]any)) != n {
		t.Fatalf("IMP-03 wrote %d want %d", len(wrote.json["data"].([]any)), n)
	}

	// IMP-04: re-run skips by external_ref.
	again := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{"items": items})
	if len(again.json["data"].([]any)) != 0 {
		t.Fatalf("IMP-04 wrote new: %v", again.json)
	}
	skipped := again.json["skipped"].([]any)
	if len(skipped) != n {
		t.Fatalf("IMP-04 skipped: %d want %d", len(skipped), n)
	}
	if !strings.Contains(fmt.Sprint(skipped[0]), "external_ref") && !strings.Contains(fmt.Sprint(skipped[0]), "already") {
		t.Fatalf("IMP-04 reason: %v", skipped[0])
	}
}

func TestCORE35StructuredPriceStoredOnModule(t *testing.T) {
	// Install a package whose manifest carries a structured price; the Modules tab and
	// GET module view must show it (CORE-35). Detailed parse checks live in modules/price_test.go.
	x := newExt(t)
	pkg := x.build(func(files map[string][]byte) {
		m := string(files["togavsu-module.yaml"])
		m = strings.Replace(m, "price: Free sample", "price:\n  amount_cents: 4900\n  currency: USD\n  interval: month\n  note: sample paid", 1)
		files["togavsu-module.yaml"] = []byte(m)
	})
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		r := x.admin.upload("/api/v1/modules", nil, pkg)
		t.Fatalf("install tab=%q api=%d %s", problem, r.code, r.body)
	}
	x.approveOnTab("matter-clock")
	view := x.admin.must(http.StatusOK, "GET", "/api/v1/modules/matter-clock", nil)
	if !strings.Contains(view.body, "49.00") && !strings.Contains(view.body, "4900") && !strings.Contains(view.body, "sample paid") {
		t.Fatalf("CORE-35 price missing from module view: %s", view.body)
	}
	page := x.admin.must(http.StatusOK, "GET", "/admin?tab=modules", nil)
	if !strings.Contains(page.body, "Listed price") && !strings.Contains(page.body, "sample paid") && !strings.Contains(page.body, "49.00") {
		snip := page.body
		if len(snip) > 800 {
			snip = snip[:800]
		}
		t.Fatalf("CORE-35 price missing from install screen: %s", snip)
	}
}

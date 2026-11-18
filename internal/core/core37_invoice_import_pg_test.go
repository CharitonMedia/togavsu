package core

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
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

func (c *client) uploadCSV(path string, fields url.Values, csvBytes []byte) resp {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	fw, _ := mw.CreateFormFile("csv", "opening.csv")
	_, _ = fw.Write(csvBytes)
	_ = mw.Close()
	r := c.raw("POST", path, buf.Bytes(), "Content-Type", mw.FormDataContentType(), "X-CSRF-Token", "")
	return r
}

func (f *fin) uploadPDF(matter, name string, pdf []byte) string {
	f.t.Helper()
	up := f.atty.must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{
		"file_name": name, "matter_id": matter, "size_bytes": len(pdf), "media_type": "application/pdf",
	})
	upID := str(f.t, up.json, "id")
	put := f.atty.raw("PUT", "/api/v1/file-uploads/"+upID, pdf, "Content-Type", "application/octet-stream",
		"Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(pdf)-1, len(pdf)))
	if put.code != http.StatusOK {
		f.t.Fatalf("chunk: %d %s", put.code, put.body)
	}
	f.atty.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+upID+"/complete", map[string]any{})
	file := f.atty.must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": upID})
	return str(f.t, file.json, "id")
}

func TestCORE37OpeningInvoiceImport(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Opening Import Estate", "opening.client@example.com", nil)
	issued := time.Now().UTC().Format("2006-01-02")

	// Use a format the historic import can collide with; start marks where normal numbering begins.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_format", map[string]any{"value": "NNNN"})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 5000})

	// IMP-01 via the Migration tab form: sent, Balance forward, imported; next normal number is 5000.
	csrf := csrfFrom(t, f.admin)
	form := f.admin.must(http.StatusSeeOther, "POST", "/admin/migration/opening-invoice", url.Values{
		"csrf": {csrf}, "matter_id": {matter}, "number": {"4521"}, "issued_on": {issued},
		"amount_due": {"1500.00"}, "external_ref": {"ref-4521"},
	})
	locUnesc, _ := url.QueryUnescape(form.loc)
	if !strings.Contains(locUnesc, "Imported opening invoice 4521") {
		t.Fatalf("IMP-01 form notice: %s", form.loc)
	}
	listed := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices?status=sent", nil)
	if !strings.Contains(listed.body, `"number":"4521"`) || !strings.Contains(listed.body, `"imported":true`) {
		t.Fatalf("IMP-01 form import missing: %s", listed.body)
	}
	if f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'invoice.imported'`) < 1 {
		t.Fatal("IMP-01 missing invoice.imported")
	}
	_ = f.timeEntry(matter, 6, 100_00)
	next := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	n := str(t, next.json["data"].([]any)[0].(map[string]any), "number")
	if n != "5000" {
		t.Fatalf("IMP-01 next number want 5000 got %s (sequence must ignore imports and honour invoice_number_start)", n)
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

	// IMP-05: amount paid posts exactly one method=other imported payment; balance = due - paid.
	paid := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "3001", issued, 1000_00, 250_00, "api", "ref-paid")},
	})
	row := paid.json["data"].([]any)[0].(map[string]any)
	if int64(row["amount_paid_cents"].(float64)) != 250_00 || int64(row["balance_due_cents"].(float64)) != 750_00 {
		t.Fatalf("IMP-05 cents: %v", row)
	}
	payN := f.count(`SELECT COUNT(*) FROM core.payments WHERE invoice_id = $1 AND imported = 1 AND method = 'other'`, row["id"])
	if payN != 1 {
		// Fall back to JSON body when dual-write projection lags in a test DB shape.
		payN = f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'payment' AND body::text LIKE '%' || $1 || '%' AND body::text LIKE '%"imported":true%' AND body::text LIKE '%"method":"other"%'`, row["id"])
		if payN != 1 {
			t.Fatalf("IMP-05 want exactly one imported method=other payment, got %d", payN)
		}
	}
	pays := f.atty.must(http.StatusOK, "GET", "/api/v1/payments?matter_id="+matter, nil)
	if !strings.Contains(pays.body, `"imported":true`) || !strings.Contains(pays.body, `"method":"other"`) {
		t.Fatalf("IMP-05 Payment JSON must expose imported: %s", pays.body[:min(600, len(pays.body))])
	}

	// IMP-07: void an unpaid imported invoice; void is logged; sequence untouched.
	toVoid := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "3100", issued, 80_00, 0, "form", "ref-void")},
	})
	voidID := str(t, toVoid.json["data"].([]any)[0].(map[string]any), "id")
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+voidID+"/void", map[string]any{"reason": "opened in error"})
	got := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices/"+voidID, nil)
	if str(t, got.json, "status") != "void" {
		t.Fatalf("IMP-07 void: %v", got.json)
	}
	if f.count(`SELECT COUNT(*) FROM core.events WHERE action = 'invoice.voided' AND record_id = $1`, voidID) != 1 {
		t.Fatal("IMP-07 missing invoice.voided event")
	}
	if f.count(`SELECT COUNT(*) FROM core.audit_log WHERE entity_id = $1 AND (field = 'status' OR reason LIKE '%opened in error%')`, voidID) < 1 &&
		f.count(`SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND (field = 'status' OR reason LIKE '%opened in error%')`, voidID) < 1 {
		t.Fatal("IMP-07 void not audited")
	}
	_ = f.timeEntry(matter, 6, 100_00)
	afterVoid := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	// Prior draft consumed 5000; next is 5001 — voided import 3100 must not move the sequence.
	if n := str(t, afterVoid.json["data"].([]any)[0].(map[string]any), "number"); n != "5001" {
		t.Fatalf("IMP-07 sequence after void: %s", n)
	}

	// IMP-06: document of record — staff link via pdf route; client downloads bytes; other matter refused.
	dorMatter, dorContact := f.matter("DoR Estate", "dor.client@example.com", nil)
	otherMatter, otherContact := f.matter("Other Estate", "other.client@example.com", nil)
	pdfBytes := []byte("%PDF-1.4 synthetic opening invoice\n")
	fileID := f.uploadPDF(dorMatter, "opening-4521.pdf", pdfBytes)
	withDor := openingItem(dorMatter, "2200", issued, 50_00, 0, "form", "ref-dor")
	withDor["document_of_record_file_id"] = fileID
	dorInv := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{"items": []map[string]any{withDor}})
	dorRow := dorInv.json["data"].([]any)[0].(map[string]any)
	if dorRow["imported"] != true || str(t, dorRow, "document_of_record_file_id") != fileID {
		t.Fatalf("IMP-06 dor field: %v", dorRow)
	}
	dorID := str(t, dorRow, "id")
	staffPage := f.atty.must(http.StatusOK, "GET", "/billing/invoices/"+dorID, nil)
	if !strings.Contains(staffPage.body, "Imported") || !strings.Contains(staffPage.body, "Document of record") || !strings.Contains(staffPage.body, "/billing/invoices/"+dorID+"/pdf") {
		t.Fatalf("IMP-06 staff billing tab: %s", staffPage.body)
	}
	pdf := f.atty.do("GET", "/api/v1/invoices/"+dorID+"/pdf", nil)
	if pdf.code != http.StatusSeeOther || !strings.Contains(pdf.loc, "/api/v1/files/"+fileID+"/versions/") || !strings.Contains(pdf.loc, "/download") {
		t.Fatalf("IMP-06 pdf redirect: %d %s", pdf.code, pdf.loc)
	}
	staffDL := f.atty.do("GET", pdf.loc, nil)
	if staffDL.code != http.StatusOK || !bytes.Contains([]byte(staffDL.body), []byte("%PDF")) {
		t.Fatalf("IMP-06 staff download: %d %s", staffDL.code, staffDL.body[:min(120, len(staffDL.body))])
	}
	cliKit, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Dor Client", "dor.client@example.com", domain.ID(dorContact), domain.RoleClient)
	cli := wrapClient(cliKit)
	cliInv := cli.must(http.StatusOK, "GET", "/api/v1/invoices/"+dorID, nil)
	if cliInv.json["imported"] != true || str(t, cliInv.json, "document_of_record_file_id") != fileID {
		t.Fatalf("IMP-06 client projection: %s", cliInv.body)
	}
	cliPDF := cli.do("GET", "/api/v1/invoices/"+dorID+"/pdf", nil)
	if cliPDF.code != http.StatusSeeOther || !strings.Contains(cliPDF.loc, "/download") {
		t.Fatalf("IMP-06 client pdf redirect: %d %s", cliPDF.code, cliPDF.loc)
	}
	cliDL := cli.do("GET", cliPDF.loc, nil)
	if cliDL.code != http.StatusOK || !bytes.Contains([]byte(cliDL.body), []byte("%PDF")) {
		t.Fatalf("IMP-06 client download: %d %s", cliDL.code, cliDL.body[:min(200, len(cliDL.body))])
	}
	otherKit, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Other Client", "other.client@example.com", domain.ID(otherContact), domain.RoleClient)
	other := wrapClient(otherKit)
	if r := other.do("GET", "/api/v1/invoices/"+dorID+"/pdf", nil); r.code != http.StatusNotFound {
		t.Fatalf("IMP-06 other-matter client pdf: %d %s", r.code, r.body)
	}
	if r := other.do("GET", pdf.loc, nil); r.code != http.StatusNotFound {
		t.Fatalf("IMP-06 other-matter client file: %d %s", r.code, r.body)
	}
	_ = otherMatter

	// Cross-matter DoR refused at import.
	alien := f.uploadPDF(otherMatter, "alien.pdf", pdfBytes)
	cross := openingItem(dorMatter, "2201", issued, 40_00, 0, "api", "ref-cross")
	cross["document_of_record_file_id"] = alien
	if r := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{"items": []map[string]any{cross}}); r.code == http.StatusCreated {
		t.Fatalf("cross-matter DoR wrote: %s", r.body)
	} else if !strings.Contains(strings.ToLower(r.body), "matter") && !strings.Contains(strings.ToLower(r.body), "document_of_record") {
		t.Fatalf("cross-matter DoR reason: %s", r.body)
	}

	// IMP-08: collections panel, aging buckets and invoice lists (statements) include imports.
	openImport := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "3300", issued, 200_00, 0, "form", "ref-aging")},
	})
	openID := str(t, openImport.json["data"].([]any)[0].(map[string]any), "id")
	colPage := f.atty.must(http.StatusOK, "GET", "/billing/invoices/"+openID, nil)
	if !strings.Contains(colPage.body, "Collections") && !strings.Contains(colPage.body, "id=\"col-h\"") {
		t.Fatalf("IMP-08 collections panel missing: %s", colPage.body[:min(600, len(colPage.body))])
	}
	aging := f.atty.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil)
	if !strings.Contains(aging.body, "3300") || !strings.Contains(aging.body, "bucket") {
		t.Fatalf("IMP-08 aging missing imported invoice/bucket: %s", aging.body)
	}
	stmts := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices?status=sent", nil)
	if !strings.Contains(stmts.body, "3300") || !strings.Contains(stmts.body, `"imported":true`) {
		t.Fatalf("IMP-08 statements/list missing imported: %s", stmts.body)
	}
	cliList := cli.must(http.StatusOK, "GET", "/api/v1/invoices", nil)
	if !strings.Contains(cliList.body, "2200") {
		t.Fatalf("IMP-08 client statement list missing DoR invoice: %s", cliList.body)
	}
}

// TestCORE37DefaultStartFloor: with billing.invoice_number_start left at default 0,
// opening import refuses until a normal invoice exists (or the setting is set); when
// normal invoices exist the floor is the lowest of them; an import at that floor is
// refused; a later normal invoice never reuses an imported number (IMP-02 / IMP-01).
func TestCORE37DefaultStartFloor(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Default Start Import", "default.start@example.com", nil)
	issued := time.Now().UTC().Format("2006-01-02")
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_format", map[string]any{"value": "NNNN"})
	// Leave invoice_number_start at default 0. With no normal invoices, refuse.
	refuse := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "0007", issued, 100_00, 0, "api", "ref-default-7")},
	})
	if refuse.code == http.StatusCreated {
		t.Fatalf("default start with no normal invoices must refuse: %s", refuse.body)
	}
	if !strings.Contains(strings.ToLower(refuse.body), "invoice numbering starts at") {
		t.Fatalf("want admin setting message, got %s", refuse.body)
	}
	// Seed one normal invoice at 0100, then clear the start setting so the derived
	// floor is the lowest non-imported number (100).
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 100})
	_ = f.timeEntry(matter, 6, 100_00)
	seed := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	if n := str(t, seed.json["data"].([]any)[0].(map[string]any), "number"); n != "0100" {
		t.Fatalf("seed draft want 0100 got %s", n)
	}
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 0})
	// Import at the floor (0100) is refused.
	atFloor := f.atty.do("POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "0100", issued, 100_00, 0, "api", "ref-at-floor")},
	})
	if atFloor.code == http.StatusCreated || !strings.Contains(strings.ToLower(atFloor.body), "0100") {
		t.Fatalf("import at floor must refuse naming the number: %d %s", atFloor.code, atFloor.body)
	}
	// Import below the floor succeeds.
	imp := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoices:bulk", map[string]any{
		"items": []map[string]any{openingItem(matter, "0050", issued, 100_00, 0, "api", "ref-below-floor")},
	})
	if !strings.Contains(imp.body, `"imported":true`) {
		t.Fatalf("import below floor missing: %s", imp.body)
	}
	// Next normal number continues from high-water 0100 → 0101, never reusing 0050.
	_ = f.timeEntry(matter, 6, 100_00)
	next := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	if n := str(t, next.json["data"].([]any)[0].(map[string]any), "number"); n != "0101" {
		t.Fatalf("next after import 0050 want 0101 got %s", n)
	}
	listed := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices", nil)
	if strings.Count(listed.body, `"number":"0050"`) != 1 {
		t.Fatalf("duplicate 0050 appeared: %s", listed.body[:min(800, len(listed.body))])
	}
}

func TestCORE37CSVPreviewAndIdempotentExternalRef(t *testing.T) {
	f := newFin(t)
	const contactEmail = "csv.client@example.com"
	matter, _ := f.matter("CSV Import Estate", contactEmail, nil)
	issued := time.Now().UTC().Format("2006-01-02")
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 9000})
	pdfBytes := []byte("%PDF-1.4 csv opening\n")
	_ = f.uploadPDF(matter, "opening-1000.pdf", pdfBytes)

	// IMP-03: 500-row CSV via Migration tab — preview names the bad row; a commit of that
	// bad file writes nothing; with the row fixed, all 500 land in one transaction.
	const n = 500
	var b strings.Builder
	b.WriteString("matter_id,number,issued_on,amount_due,due_on,message,contact_email,document_file_name,amount_paid,external_ref\n")
	for i := 0; i < n; i++ {
		num := 1000 + i
		fileName := ""
		if i == 0 {
			fileName = "opening-1000.pdf"
		}
		if i == 250 {
			num = 9001 // at/above floor — bad row
		}
		fmt.Fprintf(&b, "%s,%d,%s,10.00,%s,,%s,%s,0,csv-%d\n", matter, num, issued, issued, contactEmail, fileName, i)
	}
	badCSV := []byte(b.String())
	csrf := csrfFrom(t, f.admin)
	// First-pass Commit without a matching preview is refused (§6.3).
	blind := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}, "commit": {"1"}}, badCSV)
	if blind.code != http.StatusSeeOther && blind.code != http.StatusUnprocessableEntity && blind.code != http.StatusBadRequest {
		t.Fatalf("blind commit status: %d %s", blind.code, blind.body)
	}
	if blind.code == http.StatusSeeOther {
		bloc, _ := url.QueryUnescape(blind.loc)
		if !strings.Contains(strings.ToLower(bloc), "preview") {
			t.Fatalf("blind commit must require preview: %s", blind.loc)
		}
	} else if !strings.Contains(strings.ToLower(blind.body), "preview") {
		t.Fatalf("blind commit must name preview: %s", blind.body)
	}
	preview := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, badCSV)
	if preview.code != http.StatusSeeOther {
		t.Fatalf("IMP-03 preview post: %d %s", preview.code, preview.body)
	}
	q := ""
	if i := strings.Index(preview.loc, "?"); i >= 0 {
		q = preview.loc[i:]
	}
	page := f.admin.must(http.StatusOK, "GET", "/admin?tab=migration"+strings.Replace(q, "?", "&", 1), nil)
	if !strings.Contains(page.body, "id=\"csv-preview\"") {
		t.Fatalf("IMP-03 preview table missing: %s", page.body[max(0, strings.Index(page.body, "opening-csv")):min(len(page.body), strings.Index(page.body, "opening-csv")+800)])
	}
	if !strings.Contains(page.body, "invoice_number_start") && !strings.Contains(page.body, "9001") {
		t.Fatalf("IMP-03 preview does not name bad row: %s", page.body)
	}
	invBefore := f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`)
	payBefore := f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'payment'`)
	shareBefore := f.count(`SELECT COUNT(*) FROM core.files WHERE shared_with_client = 1`)
	// Commit of the same bad file must write nothing (preview already matched).
	badCommit := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}, "commit": {"1"}}, badCSV)
	if badCommit.code == http.StatusSeeOther {
		bloc, _ := url.QueryUnescape(badCommit.loc)
		if strings.Contains(bloc, "Wrote ") && !strings.Contains(bloc, "Wrote 0") {
			t.Fatalf("IMP-03 bad commit wrote rows: %s", badCommit.loc)
		}
	}
	if f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) != invBefore ||
		f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'payment'`) != payBefore ||
		f.count(`SELECT COUNT(*) FROM core.files WHERE shared_with_client = 1`) != shareBefore {
		t.Fatalf("IMP-03 bad commit must write nothing (invoices/payments/shares)")
	}
	// Fix the bad row: preview then commit through the admin CSV path.
	var good strings.Builder
	good.WriteString("matter_id,number,issued_on,amount_due,due_on,message,contact_email,document_file_name,amount_paid,external_ref\n")
	for i := 0; i < n; i++ {
		fileName := ""
		if i == 0 {
			fileName = "opening-1000.pdf"
		}
		fmt.Fprintf(&good, "%s,%d,%s,10.00,%s,,%s,%s,0,csv-%d\n", matter, 1000+i, issued, issued, contactEmail, fileName, i)
	}
	goodCSV := []byte(good.String())
	goodPrev := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, goodCSV)
	if goodPrev.code != http.StatusSeeOther {
		t.Fatalf("IMP-03 good preview: %d %s", goodPrev.code, goodPrev.body)
	}
	commit := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}, "commit": {"1"}}, goodCSV)
	if commit.code != http.StatusSeeOther {
		t.Fatalf("IMP-03 commit: %d %s", commit.code, commit.body)
	}
	after := f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`)
	if after-invBefore != n {
		t.Fatalf("IMP-03 wrote %d want %d (loc=%s)", after-invBefore, n, commit.loc)
	}
	// First row attached the PDF by file name.
	first := f.atty.must(http.StatusOK, "GET", "/api/v1/invoices?status=sent", nil)
	if !strings.Contains(first.body, "document_of_record_file_id") {
		t.Fatalf("IMP-03 CSV file name did not attach DoR: %s", first.body[:min(500, len(first.body))])
	}

	// IMP-04: re-upload skips by external_ref (preview then commit of the same bytes).
	againPrev := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, goodCSV)
	if againPrev.code != http.StatusSeeOther {
		t.Fatalf("IMP-04 preview: %d %s", againPrev.code, againPrev.body)
	}
	again := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}, "commit": {"1"}}, goodCSV)
	againLoc, _ := url.QueryUnescape(again.loc)
	if again.code != http.StatusSeeOther || !strings.Contains(againLoc, "skipped 500") {
		t.Fatalf("IMP-04 re-upload notice: %s", again.loc)
	}
	if f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) != after {
		t.Fatal("IMP-04 wrote new rows on re-upload")
	}

	// Bad contact_email is refused in preview.
	badEmail := fmt.Sprintf("matter_id,number,issued_on,amount_due,contact_email,external_ref\n%s,1400,%s,10.00,nobody@example.com,csv-email\n", matter, issued)
	badPrev := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, []byte(badEmail))
	if badPrev.code != http.StatusSeeOther {
		t.Fatalf("contact preview: %d", badPrev.code)
	}
	bq := ""
	if i := strings.Index(badPrev.loc, "?"); i >= 0 {
		bq = badPrev.loc[i:]
	}
	badPage := f.admin.must(http.StatusOK, "GET", "/admin?tab=migration"+strings.Replace(bq, "?", "&", 1), nil)
	if !strings.Contains(strings.ToLower(badPage.body), "contact_email") {
		t.Fatalf("contact_email refusal missing: %s", badPage.body)
	}
}

// TestCORE37PreviewMatchesCommitSideChecks: BulkInvoicesPreview runs the same
// parseDate / recompute / client-party checks as commit, so a preview that says OK
// never fails at write (IMP-03).
func TestCORE37PreviewMatchesCommitSideChecks(t *testing.T) {
	f := newFin(t)
	matter, _ := f.matter("Preview Commit Side", "preview.commit@example.com", nil)
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/billing.invoice_number_start", map[string]any{"value": 9000})
	csrf := csrfFrom(t, f.admin)

	previewNames := func(csv string, wantSubstrings ...string) {
		t.Helper()
		prev := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, []byte(csv))
		if prev.code != http.StatusSeeOther {
			t.Fatalf("preview status: %d %s", prev.code, prev.body)
		}
		q := ""
		if i := strings.Index(prev.loc, "?"); i >= 0 {
			q = prev.loc[i:]
		}
		page := f.admin.must(http.StatusOK, "GET", "/admin?tab=migration"+strings.Replace(q, "?", "&", 1), nil)
		body := strings.ToLower(page.body)
		if strings.Contains(body, "all ") && strings.Contains(body, "rows look good") {
			t.Fatalf("preview reported all good: %s", page.body)
		}
		for _, want := range wantSubstrings {
			if !strings.Contains(body, strings.ToLower(want)) {
				t.Fatalf("preview missing %q: %s", want, page.body)
			}
		}
	}

	// Bad issued_on date — previously only failed at commit.
	previewNames(fmt.Sprintf(
		"matter_id,number,issued_on,amount_due,due_on,external_ref\n%s,1500,2026-13-40,10.00,2026-01-15,ref-bad-date\n", matter),
		"issued_on", "date")
	// amount_paid greater than amount_due — OpeningInvoiceItem zeros balance; recompute refuses.
	previewNames(fmt.Sprintf(
		"matter_id,number,issued_on,amount_due,amount_paid,external_ref\n%s,1501,2026-01-15,10.00,20.00,ref-overpay\n", matter),
		"reconcile")
	// amount_paid on a matter with no client party.
	lonely := f.admin.must(http.StatusCreated, "POST", "/api/v1/matters", map[string]any{
		"name": "No Client Party", "practice_area_id": f.area, "responsible_attorney_id": f.attyID,
		"billing_type": "hourly",
	})
	lonelyID := str(t, lonely.json, "id")
	previewNames(fmt.Sprintf(
		"matter_id,number,issued_on,amount_due,amount_paid,external_ref\n%s,1502,2026-01-15,10.00,5.00,ref-no-client\n", lonelyID),
		"client party")

	// Clean file: preview OK then commit OK (same bytes).
	good := fmt.Sprintf(
		"matter_id,number,issued_on,amount_due,due_on,amount_paid,external_ref\n%s,1600,2026-01-15,10.00,2026-02-01,0,ref-clean-preview\n", matter)
	goodPrev := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}}, []byte(good))
	if goodPrev.code != http.StatusSeeOther {
		t.Fatalf("good preview: %d %s", goodPrev.code, goodPrev.body)
	}
	gq := ""
	if i := strings.Index(goodPrev.loc, "?"); i >= 0 {
		gq = goodPrev.loc[i:]
	}
	goodPage := f.admin.must(http.StatusOK, "GET", "/admin?tab=migration"+strings.Replace(gq, "?", "&", 1), nil)
	if !strings.Contains(strings.ToLower(goodPage.body), "look good") {
		t.Fatalf("good preview not clean: %s", goodPage.body)
	}
	before := f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`)
	commit := f.admin.uploadCSV("/admin/migration/opening-csv", url.Values{"csrf": {csrf}, "commit": {"1"}}, []byte(good))
	if commit.code != http.StatusSeeOther {
		t.Fatalf("good commit: %d %s", commit.code, commit.body)
	}
	if f.count(`SELECT COUNT(*) FROM core.billing_records WHERE kind = 'invoice'`) != before+1 {
		t.Fatalf("good preview/commit wrote wrong count")
	}
}

func TestCORE35StructuredPriceStoredOnModule(t *testing.T) {
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

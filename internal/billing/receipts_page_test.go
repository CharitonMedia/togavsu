package billing

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

func (a *api) multipart(p domain.Principal, path string, fields map[string]string, file, name, body string) (int, string) {
	a.f.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if file != "" {
		fw, err := mw.CreateFormFile(file, name)
		if err != nil {
			a.f.t.Fatal(err)
		}
		_, _ = fw.Write([]byte(body))
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Test-User", string(p.UserID))
	req.AddCookie(&http.Cookie{Name: practice.CSRFCookie, Value: csrfToken})
	rec := httptest.NewRecorder()
	a.srv.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Location")
}

// The expense screen picks a receipt file from the matter and uploads a new one; both go
// through the same service checks as the API.
func TestExpenseScreenReceiptPickerAndUpload(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Screen receipts", "hourly", 0, 0)
	a := newAPI(t, f)
	row, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, ""))
	if err != nil {
		t.Fatal(err)
	}
	path := "/billing/expenses/" + string(row.ID)

	// Unwired: no picker and no upload form.
	if code, body := a.page(f.staff, path); code != 200 || strings.Contains(body, `name="receipt_file_id"`) || strings.Contains(body, "/receipt") {
		t.Fatalf("unwired page offers receipts: %d", code)
	}

	rc := &fakeReceipts{files: map[domain.ID]struct {
		matter  domain.ID
		readers map[domain.ID]bool
	}{
		"rcpt-ok":     {m.ID, map[domain.ID]bool{f.staff.UserID: true}},
		"rcpt-hidden": {m.ID, map[domain.ID]bool{f.attorney.UserID: true}},
	}}
	f.svc.d.Receipts = rc
	code, body := a.page(f.staff, path)
	if code != 200 || !strings.Contains(body, `<option value="rcpt-ok"`) || strings.Contains(body, "rcpt-hidden") ||
		!strings.Contains(body, `enctype="multipart/form-data"`) {
		t.Fatalf("picker: %d\n%s", code, body)
	}
	edit := func(receipt *string) url.Values {
		v := url.Values{"matter_id": {string(m.ID)}, "incurred_on": {"2026-06-02"}, "description": {"Courier"}, "amount": {"12.00"},
			"expense_type_id": {string(f.expType)}, "billable": {"1"}}
		if receipt != nil {
			v.Set("receipt_file_id", *receipt)
		}
		return v
	}
	pick := "rcpt-ok"
	if code, loc := a.form(f.staff, path, edit(&pick), true); code != 303 || strings.Contains(loc, "error") {
		t.Fatalf("pick: %d %s", code, loc)
	}
	if got, _ := f.svc.Expense(f.ctx, f.staff, row.ID); got.FileID != "rcpt-ok" {
		t.Fatalf("picked receipt not saved: %+v", got)
	}
	// A receipt the caller cannot read is refused with the field error, and nothing changes.
	hidden := "rcpt-hidden"
	if _, loc := a.form(f.staff, path, edit(&hidden), true); !strings.Contains(loc, "error=") {
		t.Fatalf("hidden receipt accepted: %s", loc)
	}
	// A form without the field (bulk, older pages) leaves the receipt alone.
	a.form(f.staff, path, edit(nil), true)
	if got, _ := f.svc.Expense(f.ctx, f.staff, row.ID); got.FileID != "rcpt-ok" {
		t.Fatalf("receipt dropped by a form without the picker: %+v", got)
	}

	// Upload: CSRF is required, then the file is stored and attached.
	up := path + "/receipt"
	if code, _ := a.multipart(f.staff, up, nil, "receipt", "taxi.pdf", "synthetic"); code != 403 {
		t.Fatalf("upload without csrf: %d", code)
	}
	if _, loc := a.multipart(f.staff, up, map[string]string{"csrf": csrfToken}, "", "", ""); !strings.Contains(loc, "error=") {
		t.Fatalf("upload with no file: %s", loc)
	}
	if code, loc := a.multipart(f.staff, up, map[string]string{"csrf": csrfToken}, "receipt", `C:\scans\taxi.pdf`, "synthetic"); code != 303 || strings.Contains(loc, "error") {
		t.Fatalf("upload: %d %s", code, loc)
	}
	if got, _ := f.svc.Expense(f.ctx, f.staff, row.ID); got.FileID != "rcpt-up-taxi.pdf" {
		t.Fatalf("uploaded receipt not attached: %+v", got)
	}
	// The accountant may read expenses but not change them, so the upload is refused.
	if _, loc := a.multipart(f.accountant, up, map[string]string{"csrf": csrfToken}, "receipt", "x.pdf", "x"); !strings.Contains(loc, "error=") {
		t.Fatalf("accountant upload: %s", loc)
	}
	if _, ok := rc.files["rcpt-up-x.pdf"]; ok {
		t.Fatal("accountant's upload reached the file store")
	}
}

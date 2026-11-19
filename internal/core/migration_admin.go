package core

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// migrationAdmin is the Migration import tab over the billing service (CORE-37).
type migrationAdmin struct {
	app *App

	mu          sync.Mutex
	previews    map[domain.ID][]dashboard.MigrationPreviewRow
	previewHash map[domain.ID]string
}

func (m *migrationAdmin) ensure() {
	if m.previews == nil {
		m.previews = map[domain.ID][]dashboard.MigrationPreviewRow{}
	}
	if m.previewHash == nil {
		m.previewHash = map[domain.ID]string{}
	}
}

func (m *migrationAdmin) Panel(r *http.Request, p domain.Principal) (any, error) {
	out := map[string]any{}
	if n := r.URL.Query().Get("notice"); n != "" {
		out["notice"] = n
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensure()
	if rows := m.previews[p.UserID]; len(rows) > 0 {
		out["preview"] = rows
		delete(m.previews, p.UserID)
	}
	return out, nil
}

func (m *migrationAdmin) Act(r *http.Request, p domain.Principal, action string) (string, error) {
	if m.app == nil || m.app.Billing == nil {
		return "", domain.NotFound("billing")
	}
	switch action {
	case "opening-invoice":
		return m.oneForm(r, p)
	case "opening-csv":
		return m.csv(r, p)
	default:
		return "", domain.NotFound("migration action")
	}
}

func (m *migrationAdmin) oneForm(r *http.Request, p domain.Principal) (string, error) {
	matter := domain.ID(strings.TrimSpace(r.FormValue("matter_id")))
	number := strings.TrimSpace(r.FormValue("number"))
	issued := strings.TrimSpace(r.FormValue("issued_on"))
	due := strings.TrimSpace(r.FormValue("due_on"))
	msg := strings.TrimSpace(r.FormValue("message"))
	ref := strings.TrimSpace(r.FormValue("external_ref"))
	dor := domain.ID(strings.TrimSpace(r.FormValue("document_of_record_file_id")))
	fileName := strings.TrimSpace(r.FormValue("document_file_name"))
	amountDue, err := parseMoneyForm(r.FormValue("amount_due"))
	if err != nil {
		return "", domain.Invalid("amount_due", err.Error())
	}
	amountPaid, err := parseMoneyForm(r.FormValue("amount_paid"))
	if err != nil {
		return "", domain.Invalid("amount_paid", err.Error())
	}
	item := billing.OpeningInvoiceItem(matter, number, issued, due, amountDue, amountPaid, msg, billing.ImportSourceForm, ref, dor)
	item.DocumentFileName = fileName
	item.ContactEmail = strings.TrimSpace(r.FormValue("contact_email"))
	rows, results, err := m.app.Billing.BulkInvoicesDetailed(r.Context(), p, []billing.InvoiceImport{item})
	if err != nil {
		return "", err
	}
	if len(results) > 0 && results[0].Skipped {
		return "Already imported (external_ref " + results[0].ExternalRef + ").", nil
	}
	if len(rows) == 0 {
		return "Nothing written.", nil
	}
	return "Imported opening invoice " + rows[0].Number + ".", nil
}

func (m *migrationAdmin) csv(r *http.Request, p domain.Principal) (string, error) {
	f, _, err := r.FormFile("csv")
	if err != nil {
		return "", domain.Invalid("csv", "a CSV file is required")
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return "", domain.Invalid("csv", "could not read file")
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	parsed, err := parseOpeningCSV(strings.NewReader(string(raw)))
	if err != nil {
		return "", err
	}
	commit := r.FormValue("commit") == "1"
	if !commit {
		preview, bad, err := m.previewCSV(r, p, parsed)
		if err != nil {
			return "", err
		}
		m.mu.Lock()
		m.ensure()
		m.previews[p.UserID] = preview
		m.previewHash[p.UserID] = hash
		m.mu.Unlock()
		ok := len(preview) - len(bad)
		if len(bad) > 0 {
			return "Preview: " + strconv.Itoa(ok) + " ok, " + strconv.Itoa(len(bad)) + " refused. " + strings.Join(bad, "; "), nil
		}
		return "Preview: all " + strconv.Itoa(len(preview)) + " rows look good. Check Commit and upload again to write.", nil
	}
	// §6.3: the upload shows every row's result before anything is written. Refuse a
	// first-pass Commit so an admin cannot write without a matching preview.
	m.mu.Lock()
	m.ensure()
	seen := m.previewHash[p.UserID] == hash
	m.mu.Unlock()
	if !seen {
		return "", domain.Invalid("commit", "preview this CSV first (upload without Commit checked)")
	}
	// Parse-error rows never reach BulkInvoicesDetailed: refuse the commit so nothing
	// is written when the preview named a bad amount (IMP-03).
	if parsed.hasParseErrors() {
		return "", domain.Invalid("csv", "fix refused rows before committing")
	}
	rows, results, err := m.app.Billing.BulkInvoicesDetailed(r.Context(), p, parsed.items())
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	delete(m.previewHash, p.UserID)
	delete(m.previews, p.UserID)
	m.mu.Unlock()
	skipped := 0
	for _, result := range results {
		if result.Skipped {
			skipped++
		}
	}
	return "Wrote " + strconv.Itoa(len(rows)) + " invoices; skipped " + strconv.Itoa(skipped) + " already imported.", nil
}

// previewCSV builds one MigrationPreviewRow per CSV data row. Rows that failed amount
// parsing are refused with their index; the rest go through BulkInvoicesPreview.
func (m *migrationAdmin) previewCSV(r *http.Request, p domain.Principal, parsed openingCSV) ([]dashboard.MigrationPreviewRow, []string, error) {
	preview := make([]dashboard.MigrationPreviewRow, len(parsed.rows))
	var items []billing.InvoiceImport
	var itemIdx []int
	for i, row := range parsed.rows {
		if row.ParseError != "" {
			preview[i] = dashboard.MigrationPreviewRow{Index: i + 1, OK: false, Error: row.ParseError}
			continue
		}
		items = append(items, row.Item)
		itemIdx = append(itemIdx, i)
	}
	if len(items) > 0 {
		results, err := m.app.Billing.BulkInvoicesPreview(r.Context(), p, items)
		if err != nil {
			return nil, nil, err
		}
		for j, res := range results {
			i := itemIdx[j]
			row := dashboard.MigrationPreviewRow{
				Index: i + 1, OK: res.PreviewOK, Error: res.PreviewError,
				Skipped: res.Skipped, ExternalRef: res.ExternalRef,
			}
			if res.Skipped {
				row.OK = true
			}
			preview[i] = row
		}
	}
	var bad []string
	for _, row := range preview {
		if row.OK {
			continue
		}
		bad = append(bad, "row "+strconv.Itoa(row.Index)+": "+row.Error)
	}
	return preview, bad, nil
}

func parseMoneyForm(s string) (domain.Cents, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return domain.Cents(int64(0)), nil
	}
	s = strings.ReplaceAll(s, ",", "")
	c, err := domain.ParseCents(s)
	if err != nil {
		return domain.Cents(int64(0)), err
	}
	return c, nil
}

// openingCSVRow is one data row from an opening-import CSV. ParseError is set when
// amount_due or amount_paid cannot be parsed; those rows still appear in the preview
// with their index (IMP-03) instead of aborting the whole upload.
type openingCSVRow struct {
	Item       billing.InvoiceImport
	ParseError string
}

type openingCSV struct {
	rows []openingCSVRow
}

func (c openingCSV) items() []billing.InvoiceImport {
	out := make([]billing.InvoiceImport, 0, len(c.rows))
	for _, row := range c.rows {
		if row.ParseError != "" {
			continue
		}
		out = append(out, row.Item)
	}
	return out
}

func (c openingCSV) hasParseErrors() bool {
	for _, row := range c.rows {
		if row.ParseError != "" {
			return true
		}
	}
	return false
}

func parseOpeningCSV(r io.Reader) (openingCSV, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return openingCSV{}, domain.Invalid("csv", "could not read header: "+err.Error())
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	need := []string{"matter_id", "number", "issued_on", "amount_due"}
	for _, n := range need {
		if _, ok := idx[n]; !ok {
			return openingCSV{}, domain.Invalid("csv", "missing column "+n)
		}
	}
	col := func(row []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	var out openingCSV
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return openingCSV{}, domain.Invalid("csv", err.Error())
		}
		amountDue, dueErr := parseMoneyForm(col(row, "amount_due"))
		amountPaid, paidErr := parseMoneyForm(col(row, "amount_paid"))
		if dueErr != nil || paidErr != nil {
			msg := ""
			if dueErr != nil {
				msg = "amount_due: " + dueErr.Error()
			} else {
				msg = "amount_paid: " + paidErr.Error()
			}
			out.rows = append(out.rows, openingCSVRow{ParseError: msg})
			continue
		}
		ref := col(row, "external_ref")
		if ref == "" {
			ref = col(row, "number") + "|" + col(row, "matter_id") + "|" + col(row, "issued_on")
		}
		item := billing.OpeningInvoiceItem(
			domain.ID(col(row, "matter_id")), col(row, "number"), col(row, "issued_on"), col(row, "due_on"),
			amountDue, amountPaid, col(row, "message"), billing.ImportSourceCSV, ref, domain.ID(col(row, "document_of_record_file_id")),
		)
		item.ContactEmail = col(row, "contact_email")
		item.DocumentFileName = col(row, "document_file_name")
		out.rows = append(out.rows, openingCSVRow{Item: item})
	}
	if len(out.rows) == 0 {
		return openingCSV{}, domain.Invalid("csv", "no data rows")
	}
	return out, nil
}

var _ dashboard.MigrationAdmin = (*migrationAdmin)(nil)

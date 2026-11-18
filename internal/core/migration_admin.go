package core

import (
	"encoding/csv"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// migrationAdmin is the Migration import tab over the billing service (CORE-37).
type migrationAdmin struct{ app *App }

func (m migrationAdmin) Panel(r *http.Request, p domain.Principal) (any, error) {
	_ = p
	out := map[string]any{}
	if n := r.URL.Query().Get("notice"); n != "" {
		out["notice"] = n
	}
	return out, nil
}

func (m migrationAdmin) Act(r *http.Request, p domain.Principal, action string) (string, error) {
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

func (m migrationAdmin) oneForm(r *http.Request, p domain.Principal) (string, error) {
	matter := domain.ID(strings.TrimSpace(r.FormValue("matter_id")))
	number := strings.TrimSpace(r.FormValue("number"))
	issued := strings.TrimSpace(r.FormValue("issued_on"))
	due := strings.TrimSpace(r.FormValue("due_on"))
	msg := strings.TrimSpace(r.FormValue("message"))
	ref := strings.TrimSpace(r.FormValue("external_ref"))
	dor := domain.ID(strings.TrimSpace(r.FormValue("document_of_record_file_id")))
	amountDue, err := parseMoneyForm(r.FormValue("amount_due"))
	if err != nil {
		return "", domain.Invalid("amount_due", err.Error())
	}
	amountPaid, err := parseMoneyForm(r.FormValue("amount_paid"))
	if err != nil {
		return "", domain.Invalid("amount_paid", err.Error())
	}
	item := billing.OpeningInvoiceItem(matter, number, issued, due, amountDue, amountPaid, msg, billing.ImportSourceForm, ref, dor)
	rows, skipped, err := m.app.Billing.BulkInvoicesDetailed(r.Context(), p, []billing.InvoiceImport{item})
	if err != nil {
		return "", err
	}
	if len(skipped) > 0 {
		return "Already imported (external_ref " + skipped[0].ExternalRef + ").", nil
	}
	if len(rows) == 0 {
		return "Nothing written.", nil
	}
	return "Imported opening invoice " + rows[0].Number + ".", nil
}

func (m migrationAdmin) csv(r *http.Request, p domain.Principal) (string, error) {
	f, _, err := r.FormFile("csv")
	if err != nil {
		return "", domain.Invalid("csv", "a CSV file is required")
	}
	defer f.Close()
	items, err := parseOpeningCSV(f)
	if err != nil {
		return "", err
	}
	commit := r.FormValue("commit") == "1"
	if !commit {
		results, err := m.app.Billing.BulkInvoicesPreview(r.Context(), p, items)
		if err != nil {
			return "", err
		}
		var bad []string
		ok := int(0)
		for _, res := range results {
			if res.Skipped {
				ok++
				continue
			}
			if res.PreviewOK {
				ok++
				continue
			}
			bad = append(bad, "row "+strconv.Itoa(res.Index+1)+": "+res.PreviewError)
		}
		if len(bad) > 0 {
			return "Preview: " + strconv.Itoa(ok) + " ok, " + strconv.Itoa(len(bad)) + " refused. " + strings.Join(bad, "; "), nil
		}
		return "Preview: all " + strconv.Itoa(len(results)) + " rows look good. Check Commit and upload again to write.", nil
	}
	rows, skipped, err := m.app.Billing.BulkInvoicesDetailed(r.Context(), p, items)
	if err != nil {
		return "", err
	}
	return "Wrote " + strconv.Itoa(len(rows)) + " invoices; skipped " + strconv.Itoa(len(skipped)) + " already imported.", nil
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

func parseOpeningCSV(r io.Reader) ([]billing.InvoiceImport, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, domain.Invalid("csv", "could not read header: "+err.Error())
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	need := []string{"matter_id", "number", "issued_on", "amount_due"}
	for _, n := range need {
		if _, ok := idx[n]; !ok {
			return nil, domain.Invalid("csv", "missing column "+n)
		}
	}
	col := func(row []string, name string) string {
		i, ok := idx[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	var out []billing.InvoiceImport
	for {
		row, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, domain.Invalid("csv", err.Error())
		}
		amountDue, err := parseMoneyForm(col(row, "amount_due"))
		if err != nil {
			return nil, domain.Invalid("csv", "amount_due: "+err.Error())
		}
		amountPaid, err := parseMoneyForm(col(row, "amount_paid"))
		if err != nil {
			return nil, domain.Invalid("csv", "amount_paid: "+err.Error())
		}
		ref := col(row, "external_ref")
		if ref == "" {
			ref = col(row, "number") + "|" + col(row, "matter_id") + "|" + col(row, "issued_on")
		}
		out = append(out, billing.OpeningInvoiceItem(
			domain.ID(col(row, "matter_id")), col(row, "number"), col(row, "issued_on"), col(row, "due_on"),
			amountDue, amountPaid, col(row, "message"), billing.ImportSourceCSV, ref, domain.ID(col(row, "document_of_record_file_id")),
		))
	}
	if len(out) == 0 {
		return nil, domain.Invalid("csv", "no data rows")
	}
	return out, nil
}

var _ dashboard.MigrationAdmin = migrationAdmin{}

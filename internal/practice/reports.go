package practice

import (
	"context"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// OpenMattersWidget lists the caller's open matters. A walled matter is absent.
func (s *Service) OpenMattersWidget(ctx context.Context, p domain.Principal) (Widget, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return Widget{}, err
	}
	widget := Widget{
		ID: "my-open-matters", Title: "My open matters", PagePath: "/practice/matters",
		Query: map[string]string{"status": StatusOpen},
	}
	for _, row := range s.db.Matters() {
		if row.Status != StatusOpen {
			continue
		}
		if row.ResponsibleAttorneyID != p.UserID && row.AssignedStaffID != p.UserID {
			continue
		}
		if !s.visibleMatter(ctx, p, row.ID) {
			continue
		}
		widget.Rows = append(widget.Rows, WidgetRow{
			ID: string(row.ID), Title: row.Number + " " + row.Name, Subtitle: row.Status,
			TargetPath: "/practice/matters/" + string(row.ID),
		})
	}
	if widget.Rows == nil {
		widget.Rows = []WidgetRow{}
	}
	return widget, nil
}

// ConflictWidget lists matters with an unchecked party.
func (s *Service) ConflictWidget(ctx context.Context, p domain.Principal) (Widget, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return Widget{}, err
	}
	widget := Widget{
		ID: "pending-conflict-checks", Title: "Pending conflict checks", PagePath: "/practice/matters",
		Query: map[string]string{"conflicts": "unchecked"},
	}
	for _, row := range s.db.Matters() {
		if !s.unchecked(row.ID) || !s.visibleMatter(ctx, p, row.ID) {
			continue
		}
		widget.Rows = append(widget.Rows, WidgetRow{
			ID: string(row.ID), Title: row.Number + " " + row.Name, Subtitle: "Unchecked conflicts",
			TargetPath: "/practice/matters/" + string(row.ID),
		})
	}
	if widget.Rows == nil {
		widget.Rows = []WidgetRow{}
	}
	return widget, nil
}

// MattersReport lists visible matters. A walled matter is absent.
func (s *Service) MattersReport(ctx context.Context, p domain.Principal) (ReportTable, error) {
	views, err := s.ListMatters(ctx, p, "", domain.ID(""))
	if err != nil {
		return ReportTable{}, err
	}
	table := ReportTable{Report: "matters", Columns: []string{"matter_id", "number", "name", "status"}}
	for _, view := range views {
		table.Rows = append(table.Rows, map[string]string{
			"matter_id": string(view.Matter.ID),
			"number":    view.Matter.Number,
			"name":      view.Matter.Name,
			"status":    view.Matter.Status,
		})
	}
	if table.Rows == nil {
		table.Rows = []map[string]string{}
	}
	return table, nil
}

// ARAging lists unpaid invoices on matters the caller can see.
// A zero-fee matter is omitted. A walled matter is omitted.
func (s *Service) ARAging(ctx context.Context, p domain.Principal, asOf string) (ReportTable, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResBillingReport, Action: domain.ActionRead}); err != nil {
		return ReportTable{}, err
	}
	when := s.clock()
	if asOf != "" {
		parsed, err := time.Parse("2006-01-02", asOf)
		if err != nil {
			return ReportTable{}, domain.Invalid("as_of", "use YYYY-MM-DD")
		}
		when = parsed
	}
	table := ReportTable{Report: "ar-aging", Columns: []string{"matter_id", "invoice_id", "number", "balance_cents", "age_days"}}
	for _, invoice := range s.db.Invoices() {
		if invoice.Status == "void" || invoice.Status == "paid" || invoice.BalanceDueCents <= 0 {
			continue
		}
		if s.ZeroFee(invoice.MatterID) {
			continue
		}
		if s.hiddenByWall(ctx, p, invoice.MatterID) {
			continue
		}
		age := 0
		if invoice.IssuedOn != "" {
			issued, err := time.Parse("2006-01-02", invoice.IssuedOn)
			if err == nil {
				age = int(when.Sub(issued).Hours() / 24)
				if age < 0 {
					age = 0
				}
			}
		}
		table.Rows = append(table.Rows, map[string]string{
			"matter_id":     string(invoice.MatterID),
			"invoice_id":    string(invoice.ID),
			"number":        invoice.Number,
			"balance_cents": strconv.FormatInt(int64(invoice.BalanceDueCents), 10),
			"age_days":      strconv.Itoa(age),
		})
	}
	if table.Rows == nil {
		table.Rows = []map[string]string{}
	}
	return table, nil
}

func (s *Service) hiddenByWall(ctx context.Context, p domain.Principal, id domain.ID) bool {
	if id.IsZero() {
		return false
	}
	view, err := s.viewWall(ctx, p, id)
	if err != nil {
		return true
	}
	return view.Walled && !view.Allows
}

// TrustBalancesReport lists cached trust balances the caller can see. A walled matter is absent.
func (s *Service) TrustBalancesReport(ctx context.Context, p domain.Principal) (ReportTable, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResTrust, Action: domain.ActionRead}); err != nil {
		return ReportTable{}, err
	}
	table := ReportTable{Report: "trust-balances", Columns: []string{"matter_id", "client_id", "balance_cents"}}
	for _, row := range s.db.TrustBalances() {
		if s.hiddenByWall(ctx, p, row.MatterID) {
			continue
		}
		table.Rows = append(table.Rows, map[string]string{
			"matter_id":     string(row.MatterID),
			"client_id":     string(row.ClientID),
			"balance_cents": strconv.FormatInt(int64(row.BalanceCents), 10),
		})
	}
	if table.Rows == nil {
		table.Rows = []map[string]string{}
	}
	return table, nil
}

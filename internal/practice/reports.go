package practice

import (
	"container/heap"
	"context"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// OpenMattersWidget lists the caller's open matters. A walled matter is absent.
func (s *Service) OpenMattersWidget(ctx context.Context, p domain.Principal) (Widget, error) {
	var every int
	return s.OpenMattersWidgetTop(ctx, p, every)
}

// OpenMattersWidgetTop is OpenMattersWidget's first max rows in list order (zero is every
// row); it stops checking matters once it has them.
func (s *Service) OpenMattersWidgetTop(ctx context.Context, p domain.Principal, max int) (Widget, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return Widget{}, err
	}
	widget := Widget{
		ID: "my-open-matters", Title: "My open matters", PagePath: "/practice/matters",
		Query: map[string]string{"status": StatusOpen}, Rows: []WidgetRow{},
	}
	s.eachMatterInOrder(func(k *MatterKey) bool {
		if k.Status != StatusOpen || k.ResponsibleAttorneyID != p.UserID && k.AssignedStaffID != p.UserID || !s.visibleMatter(ctx, p, k.ID) {
			return true
		}
		row, err := s.db.Matter(ctx, k.ID)
		if err != nil {
			return true
		}
		widget.Rows = append(widget.Rows, WidgetRow{
			ID: string(row.ID), Title: row.Number + " " + row.Name, Subtitle: row.Status,
			TargetPath: "/practice/matters/" + string(row.ID),
		})
		return len(widget.Rows) != max
	})
	return widget, nil
}

// eachMatterInOrder calls fn with every matter's key in list order (open first, newest
// opened, number, id) until fn returns false, so a widget's rows are the same from one
// load to the next. A heap yields the order lazily: a widget that stops after a few rows
// does not sort every matter.
func (s *Service) eachMatterInOrder(fn func(*MatterKey) bool) {
	keys := s.matterKeys()
	h := &matterHeap{keys: keys, idx: make([]int, len(keys))}
	for i := range h.idx {
		h.idx[i] = i
	}
	heap.Init(h)
	for len(h.idx) > 0 {
		if !fn(&keys[heap.Pop(h).(int)]) {
			return
		}
	}
}

// matterHeap orders matter key indexes as matterOrder orders matters.
type matterHeap struct {
	keys []MatterKey
	idx  []int
}

func (h *matterHeap) Len() int           { return len(h.idx) }
func (h *matterHeap) Less(i, j int) bool { return keyOrder(&h.keys[h.idx[i]], &h.keys[h.idx[j]]) }

// keyOrder is matterOrder on keys.
func keyOrder(a, b *MatterKey) bool {
	if openRank(a.Status) != openRank(b.Status) {
		return openRank(a.Status) < openRank(b.Status)
	}
	if a.OpenedOn != b.OpenedOn {
		return a.OpenedOn > b.OpenedOn
	}
	if a.Number != b.Number {
		return a.Number > b.Number
	}
	return a.ID < b.ID
}
func (h *matterHeap) Swap(i, j int) { h.idx[i], h.idx[j] = h.idx[j], h.idx[i] }
func (h *matterHeap) Push(x any)    { h.idx = append(h.idx, x.(int)) }
func (h *matterHeap) Pop() any {
	last := h.idx[len(h.idx)-1]
	h.idx = h.idx[:len(h.idx)-1]
	return last
}

// ConflictWidget lists matters with an unchecked party.
func (s *Service) ConflictWidget(ctx context.Context, p domain.Principal) (Widget, error) {
	var every int
	return s.ConflictWidgetTop(ctx, p, every)
}

// ConflictWidgetTop is ConflictWidget's first max rows in list order (zero is every row).
func (s *Service) ConflictWidgetTop(ctx context.Context, p domain.Principal, max int) (Widget, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return Widget{}, err
	}
	widget := Widget{
		ID: "pending-conflict-checks", Title: "Pending conflict checks", PagePath: "/practice/matters",
		Query: map[string]string{"conflicts": "unchecked"}, Rows: []WidgetRow{},
	}
	s.eachMatterInOrder(func(k *MatterKey) bool {
		if !s.unchecked(k.ID) || !s.visibleMatter(ctx, p, k.ID) {
			return true
		}
		row, err := s.db.Matter(ctx, k.ID)
		if err != nil {
			return true
		}
		widget.Rows = append(widget.Rows, WidgetRow{
			ID: string(row.ID), Title: row.Number + " " + row.Name, Subtitle: "Unchecked conflicts",
			TargetPath: "/practice/matters/" + string(row.ID),
		})
		return len(widget.Rows) != max
	})
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

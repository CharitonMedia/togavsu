package billing

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// JSON shapes follow api/openapi/v1/openapi.json. Nullable ids are pointers.

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func idOrNil(id domain.ID) *domain.ID {
	if id.IsZero() {
		return nil
	}
	return &id
}

func microOrNil(v int64) *string {
	if isZero(v) {
		return nil
	}
	s := ts(domain.FromUnixMicro(v))
	return &s
}

type metaJSON struct {
	ID        domain.ID `json:"id"`
	CreatedAt string    `json:"created_at"`
	CreatedBy domain.ID `json:"created_by"`
	UpdatedAt string    `json:"updated_at"`
}

func metaOf(m domain.Meta) metaJSON {
	return metaJSON{ID: m.ID, CreatedAt: ts(m.CreatedAt), CreatedBy: m.CreatedBy, UpdatedAt: ts(m.UpdatedAt)}
}

type timeJSON struct {
	metaJSON
	MatterID       domain.ID  `json:"matter_id"`
	UserID         domain.ID  `json:"user_id"`
	WorkedOn       string     `json:"worked_on"`
	Increments     int        `json:"increments"`
	Description    string     `json:"description"`
	ActivityTypeID string     `json:"activity_type_id"`
	Billable       bool       `json:"billable"`
	RateCents      int64      `json:"rate_cents"`
	InvoiceID      *domain.ID `json:"invoice_id"`
	Source         string     `json:"source"`
	CustomFields   []any      `json:"custom_fields"`
}

func timeView(e TimeEntry) timeJSON {
	return timeJSON{metaOf(e.Meta), e.MatterID, e.UserID, e.WorkedOn, e.Increments, e.Description, e.ActivityType,
		e.Billable, int64(e.RateCents), idOrNil(e.InvoiceID), e.Source, []any{}}
}

type expenseJSON struct {
	metaJSON
	MatterID      domain.ID  `json:"matter_id"`
	IncurredOn    string     `json:"incurred_on"`
	Description   string     `json:"description"`
	AmountCents   int64      `json:"amount_cents"`
	ExpenseTypeID string     `json:"expense_type_id"`
	Billable      bool       `json:"billable"`
	MarkupPercent int        `json:"markup_percent"`
	ReceiptFileID *domain.ID `json:"receipt_file_id"`
	InvoiceID     *domain.ID `json:"invoice_id"`
	Source        string     `json:"source"`
	CustomFields  []any      `json:"custom_fields"`
}

func expenseView(e Expense) expenseJSON {
	return expenseJSON{metaOf(e.Meta), e.MatterID, e.IncurredOn, e.Description, int64(e.AmountCents), e.ExpenseType,
		e.Billable, e.MarkupPercent, idOrNil(e.FileID), idOrNil(e.InvoiceID), e.Source, []any{}}
}

type timerJSON struct {
	metaJSON
	MatterID           domain.ID `json:"matter_id"`
	UserID             domain.ID `json:"user_id"`
	StartedAt          string    `json:"started_at"`
	AccumulatedSeconds int64     `json:"accumulated_seconds"`
	Status             string    `json:"status"`
}

func timerView(t Timer) timerJSON {
	return timerJSON{metaOf(t.Meta), t.MatterID, t.UserID, ts(t.StartedAt), t.AccumulatedSeconds, t.Status()}
}

type stoppedJSON struct {
	ID                 domain.ID `json:"id"`
	MatterID           domain.ID `json:"matter_id"`
	UserID             domain.ID `json:"user_id"`
	StartedAt          string    `json:"started_at"`
	StoppedAt          string    `json:"stopped_at"`
	AccumulatedSeconds int64     `json:"accumulated_seconds"`
	Status             string    `json:"status"`
	TimeEntryID        domain.ID `json:"time_entry_id"`
}

type lineJSON struct {
	Kind        string     `json:"kind"`
	SourceID    *domain.ID `json:"source_id"`
	Description string     `json:"description"`
	AmountCents int64      `json:"amount_cents"`
	FeeItemID   *domain.ID `json:"fee_item_id"`
}

func linesView(lines []Line) []lineJSON {
	out := make([]lineJSON, 0, len(lines))
	for _, l := range lines {
		out = append(out, lineJSON{l.Kind, idOrNil(l.SourceID), l.Description, int64(l.AmountCents), idOrNil(l.FeeItemID)})
	}
	return out
}

type invoiceJSON struct {
	metaJSON
	MatterID         domain.ID  `json:"matter_id"`
	Number           string     `json:"number"`
	IssuedOn         string     `json:"issued_on"`
	Status           string     `json:"status"`
	Lines            []lineJSON `json:"lines"`
	SubtotalCents    int64      `json:"subtotal_cents"`
	AdjustmentCents  int64      `json:"adjustment_cents"`
	AdjustmentReason string     `json:"adjustment_reason"`
	InterestCents    int64      `json:"interest_cents"`
	TotalCents       int64      `json:"total_cents"`
	AmountPaidCents  int64      `json:"amount_paid_cents"`
	BalanceDueCents  int64      `json:"balance_due_cents"`
	WriteOffCents    int64      `json:"write_off_cents"`
	WriteOffReason   string     `json:"write_off_reason"`
	VoidReason       string     `json:"void_reason"`
	DisputeStatus    string     `json:"dispute_status"`
	DisputeNotes     string     `json:"dispute_notes"`
	SentAt           *string    `json:"sent_at"`
	PaidAt           *string    `json:"paid_at"`
	ClientMessage    string     `json:"client_message"`
	InternalNotes    string     `json:"internal_notes"`
}

// invoiceClientJSON is the client allowlist (InvoiceClient). Nothing else reaches a client.
type invoiceClientJSON struct {
	ID              domain.ID  `json:"id"`
	MatterID        domain.ID  `json:"matter_id"`
	Number          string     `json:"number"`
	IssuedOn        string     `json:"issued_on"`
	Status          string     `json:"status"`
	Lines           []lineJSON `json:"lines"`
	SubtotalCents   int64      `json:"subtotal_cents"`
	AdjustmentCents int64      `json:"adjustment_cents"`
	InterestCents   int64      `json:"interest_cents"`
	TotalCents      int64      `json:"total_cents"`
	AmountPaidCents int64      `json:"amount_paid_cents"`
	BalanceDueCents int64      `json:"balance_due_cents"`
	SentAt          *string    `json:"sent_at"`
	PaidAt          *string    `json:"paid_at"`
	ClientMessage   string     `json:"client_message"`
}

func invoiceView(i Invoice, client bool) any {
	if client {
		return invoiceClientJSON{i.ID, i.MatterID, i.Number, i.IssuedOn, i.Status, linesView(i.Lines), int64(i.SubtotalCents),
			int64(i.AdjustmentCents), int64(i.InterestCents), int64(i.TotalCents), int64(i.AmountPaidCents),
			int64(i.BalanceDueCents), microOrNil(i.SentAt), microOrNil(i.PaidAt), i.ClientMessage}
	}
	return invoiceJSON{metaOf(i.Meta), i.MatterID, i.Number, i.IssuedOn, i.Status, linesView(i.Lines), int64(i.SubtotalCents),
		int64(i.AdjustmentCents), i.AdjustmentReason, int64(i.InterestCents), int64(i.TotalCents), int64(i.AmountPaidCents),
		int64(i.BalanceDueCents), int64(i.WriteOffCents), i.WriteOffReason, i.VoidReason, i.DisputeStatus, i.DisputeNotes,
		microOrNil(i.SentAt), microOrNil(i.PaidAt), i.ClientMessage, i.InternalNotes}
}

type paymentJSON struct {
	metaJSON
	ContactID       domain.ID  `json:"contact_id"`
	MatterID        domain.ID  `json:"matter_id"`
	AmountCents     int64      `json:"amount_cents"`
	Destination     string     `json:"destination"`
	Method          string     `json:"method"`
	ReferenceNumber string     `json:"reference_number"`
	Status          string     `json:"status"`
	InvoiceID       *domain.ID `json:"invoice_id"`
	TrustAccountID  *domain.ID `json:"trust_account_id"`
	TrustEntryID    *domain.ID `json:"trust_entry_id"`
	Source          string     `json:"source"`
}

func paymentView(p Payment) paymentJSON {
	acct := p.TrustAccountID
	if p.Destination != DestTrust {
		acct = ""
	}
	return paymentJSON{metaOf(p.Meta), p.ContactID, p.MatterID, int64(p.AmountCents), p.Destination, p.Method, p.ReferenceNumber,
		p.Status, idOrNil(p.InvoiceID), idOrNil(acct), idOrNil(p.TrustEntryID), p.Source}
}

type feeItemJSON struct {
	metaJSON
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	DefaultAmountCents int64      `json:"default_amount_cents"`
	PracticeAreaID     *domain.ID `json:"practice_area_id"`
	Active             bool       `json:"active"`
}

func feeItemView(f FeeItem) feeItemJSON {
	return feeItemJSON{metaOf(f.Meta), f.Name, f.Description, int64(f.DefaultAmountCents), idOrNil(f.PracticeAreaID), f.Active}
}

type typeJSON struct {
	metaJSON
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

func typeView(t NamedType) typeJSON { return typeJSON{metaOf(t.Meta), t.Name, t.Active} }

type auditJSON struct {
	ID         domain.ID  `json:"id"`
	At         string     `json:"at"`
	ActorID    *domain.ID `json:"actor_id"`
	ActorKind  string     `json:"actor_kind"`
	Role       string     `json:"role"`
	Source     string     `json:"source"`
	EntityType string     `json:"entity_type"`
	EntityID   domain.ID  `json:"entity_id"`
	Field      string     `json:"field"`
	OldValue   string     `json:"old_value"`
	NewValue   string     `json:"new_value"`
	Reason     string     `json:"reason"`
	Kind       string     `json:"kind"`
}

func auditView(a domain.AuditEntry) auditJSON {
	kind := "update"
	if a.Field == "create" {
		kind = "create"
	}
	actorKind := "staff"
	switch a.Source {
	case string(domain.SourceSystem):
		actorKind = "system"
	case string(domain.SourceModule):
		actorKind = "module"
	case string(domain.SourceAPIToken):
		actorKind = "agent"
	}
	if a.Role == string(domain.RoleClient) {
		actorKind = "client"
	}
	src := a.Source
	if src == "" {
		src = string(domain.SourceUI)
	}
	return auditJSON{a.ID, ts(a.CreatedAt), idOrNil(a.ActorID), actorKind, a.Role, src, a.Entity, a.EntityID, a.Field,
		a.OldValue, a.NewValue, a.Reason, kind}
}

type flatRowJSON struct {
	MatterID     domain.ID  `json:"matter_id"`
	ClientID     *domain.ID `json:"client_id"`
	FlatFeeCents *int64     `json:"flat_fee_cents"`
	TrustCents   int64      `json:"trust_balance_cents"`
	Status       string     `json:"status"`
}

type hourlyRowJSON struct {
	MatterID      domain.ID `json:"matter_id"`
	UnbilledCents int64     `json:"unbilled_cents"`
}

func worklistView(w Worklist) map[string]any {
	flat := make([]flatRowJSON, 0, len(w.Flat))
	for _, r := range w.Flat {
		var fee *int64
		if positive(int64(r.FlatFeeCents)) {
			v := int64(r.FlatFeeCents)
			fee = &v
		}
		flat = append(flat, flatRowJSON{r.MatterID, idOrNil(r.ClientID), fee, int64(r.TrustCents), r.Status})
	}
	hourly := make([]hourlyRowJSON, 0, len(w.Hourly))
	for _, r := range w.Hourly {
		hourly = append(hourly, hourlyRowJSON{r.MatterID, int64(r.UnbilledCents)})
	}
	return map[string]any{"flat_fee": flat, "hourly": hourly}
}

type feeAgreementJSON struct {
	metaJSON
	MatterID     domain.ID  `json:"matter_id"`
	FeeType      string     `json:"fee_type"`
	AmountCents  *int64     `json:"amount_cents"`
	Services     string     `json:"services_description"`
	Status       string     `json:"status"`
	SignedOn     *string    `json:"signed_on"`
	VoidReason   string     `json:"void_reason"`
	SignedFileID *domain.ID `json:"signed_file_id"`
}

func feeAgreementView(f FeeAgreement) feeAgreementJSON {
	out := feeAgreementJSON{metaJSON: metaOf(f.Meta), MatterID: f.MatterID, FeeType: f.FeeType, Services: f.Services,
		Status: f.Status, VoidReason: f.VoidReason, SignedFileID: idOrNil(f.SignedFileID)}
	if f.AmountCents != nil {
		v := int64(*f.AmountCents)
		out.AmountCents = &v
	}
	if f.SignedOn != "" {
		out.SignedOn = &f.SignedOn
	}
	return out
}

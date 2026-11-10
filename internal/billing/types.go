package billing

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Record kinds in the store.
const (
	kindTime        = "time_entry"
	kindTimer       = "timer"
	kindExpense     = "expense"
	kindInvoice     = "invoice"
	kindPayment     = "payment"
	kindFeeItem     = "fee_item"
	kindActivity    = "activity_type"
	kindExpenseType = "expense_type"
	kindEvent       = "event"
	kindIdem        = "idempotency"
)

// Invoice statuses.
const (
	StatusDraft  = "draft"
	StatusSent   = "sent"
	StatusViewed = "viewed"
	StatusPaid   = "paid"
	StatusVoid   = "void"
)

// Line kinds.
const (
	LineTime       = "time"
	LineExpense    = "expense"
	LineFlatFee    = "flat_fee"
	LineAdjustment = "adjustment"
)

// Payment statuses, destinations and methods.
const (
	PayPending   = "pending"
	PayCompleted = "completed"
	PayFailed    = "failed"
	PayReversed  = "reversed"

	DestOperating = "operating"
	DestTrust     = "trust"

	MethodInternal = "internal_transfer"
)

// Worklist flat-fee statuses.
const (
	FlatNeedsFee    = "needs_fee_amount"
	FlatAwaiting    = "awaiting_trust_funds"
	FlatReady       = "ready_to_apply"
	FlatCollected   = "collected"
	zeroFeeMarker   = "Zero fee"
	billingFlat     = "flat"
	billingMixed    = "mixed"
	billingHourly   = "hourly"
	matterClosed    = "closed"
	partyRoleClient = "client"
)

// TimeEntry is a time entry. IncrementMinutes is the firm increment when the entry was written,
// so a later settings change does not reprice it.
type TimeEntry struct {
	domain.TimeEntry
	IncrementMinutes int `json:"increment_minutes"`
	// NeedsReview marks a draft entry from a stopped timer (PRD 8.3). It is not invoiced until
	// a person saves it with a description and an activity type.
	NeedsReview bool `json:"needs_review,omitempty"`
}

// Reviewed reports whether the entry can be invoiced.
func (t TimeEntry) Reviewed() bool { return !t.NeedsReview }

// Minutes is the billed duration.
func (t TimeEntry) Minutes() int64 { return int64(t.Increments) * int64(t.IncrementMinutes) }

// Timer is an active or stopped timer. RunningSince is set while it runs.
type Timer struct {
	domain.Meta
	MatterID           domain.ID  `json:"matter_id"`
	UserID             domain.ID  `json:"user_id"`
	StartedAt          time.Time  `json:"started_at"`
	RunningSince       *time.Time `json:"running_since,omitempty"`
	AccumulatedSeconds int64      `json:"accumulated_seconds"`
	StoppedAt          *time.Time `json:"stopped_at,omitempty"`
	TimeEntryID        domain.ID  `json:"time_entry_id,omitempty"`
}

// Status is running, paused or stopped.
func (t Timer) Status() string {
	switch {
	case t.StoppedAt != nil:
		return "stopped"
	case t.RunningSince != nil:
		return "running"
	default:
		return "paused"
	}
}

// Expense is a matter cost. Source is manual, api or a module id.
type Expense struct {
	domain.Expense
	Source string `json:"source"`
}

// Line is one invoice line. SourceID names the time entry or expense it bills.
type Line struct {
	Kind        string       `json:"kind"`
	SourceID    domain.ID    `json:"source_id,omitempty"`
	Description string       `json:"description"`
	AmountCents domain.Cents `json:"amount_cents"`
	FeeItemID   domain.ID    `json:"fee_item_id,omitempty"`
	// MatterFee marks the line that bills the matter's own flat fee. A fee-catalog item is
	// also kind flat_fee but never carries this mark.
	MatterFee bool `json:"matter_fee,omitempty"`
}

// WriteOff is one dated write-off on an invoice.
type WriteOff struct {
	On          string       `json:"on"`
	AmountCents domain.Cents `json:"amount_cents"`
	Reason      string       `json:"reason"`
	By          domain.ID    `json:"by"`
}

// Invoice is an invoice with its lines. ApprovedAt is set by an attorney or admin; editing the
// draft clears it. DueOn follows billing.payment_terms_days from IssuedOn.
type Invoice struct {
	domain.Invoice
	Lines      []Line     `json:"lines"`
	DueOn      string     `json:"due_on"`
	ApprovedBy domain.ID  `json:"approved_by,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	ViewedAt   *time.Time `json:"viewed_at,omitempty"`
	VoidedAt   *time.Time `json:"voided_at,omitempty"`
	WriteOffs  []WriteOff `json:"write_offs,omitempty"`
	SentTo     []string   `json:"sent_to,omitempty"`
	FromDate   string     `json:"from_date,omitempty"`
	ToDate     string     `json:"to_date,omitempty"`
	MatterName string     `json:"matter_name"`
	MatterNum  string     `json:"matter_number"`
	// PDFFileID is the invoice_pdf file record holding this invoice's stored PDFs, one
	// immutable version per distinct rendering; PDFSHA256 is the newest stored version's hash.
	PDFFileID domain.ID `json:"pdf_file_id,omitempty"`
	PDFSHA256 string    `json:"pdf_sha256,omitempty"`
}

// Open reports whether the invoice is sent or viewed and still owes money.
func (i Invoice) Open() bool {
	return (i.Status == StatusSent || i.Status == StatusViewed) && positive(int64(i.BalanceDueCents))
}

// Approved reports whether a draft carries a current approval.
func (i Invoice) Approved() bool { return i.ApprovedAt != nil }

// HasFlatFee reports whether a line bills the matter's flat fee. Fee-catalog items do not count.
func (i Invoice) HasFlatFee() bool {
	for _, l := range i.Lines {
		if l.MatterFee {
			return true
		}
	}
	return false
}

// matterFeeCents is the amount of the line that bills the matter's own flat fee.
func (i Invoice) matterFeeCents() domain.Cents {
	for _, l := range i.Lines {
		if l.MatterFee {
			return l.AmountCents
		}
	}
	return noCents
}

// Payment is money received. TrustAccountID is set for a trust deposit and for the account a
// trust transfer drew on. ReceivedOn is the firm-date the money arrived.
type Payment struct {
	domain.Payment
	TrustAccountID domain.ID  `json:"trust_account_id,omitempty"`
	ReceivedOn     string     `json:"received_on"`
	ReversedAt     *time.Time `json:"reversed_at,omitempty"`
	ReverseReason  string     `json:"reverse_reason,omitempty"`
	ReversalEntry  domain.ID  `json:"reversal_entry_id,omitempty"`
	// MatterFee marks the trust transfer Apply flat fee posted for the matter's own fee.
	MatterFee bool `json:"matter_fee,omitempty"`
	// TrustSynced marks a fee payment that credits a flat-fee application made on the trust
	// page. It has no trust entry of its own and is reversed only from the trust page.
	TrustSynced bool `json:"trust_synced,omitempty"`
}

// FeeItem is a catalog flat-fee item.
type FeeItem struct {
	domain.Meta
	Name               string       `json:"name"`
	Description        string       `json:"description"`
	DefaultAmountCents domain.Cents `json:"default_amount_cents"`
	PracticeAreaID     domain.ID    `json:"practice_area_id,omitempty"`
	Active             bool         `json:"active"`
}

// NamedType is an activity type or an expense type.
type NamedType struct {
	domain.Meta
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

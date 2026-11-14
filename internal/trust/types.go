package trust

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Entry types stored in trust_ledger.entry_type. A reversal keeps the original row's type
// and sets reverses_id; the API shows it as TypeReversal.
const (
	TypeDeposit    = "deposit"
	TypeWithdrawal = "withdrawal"
	TypeTransfer   = "transfer_to_operating"
	TypeRefund     = "refund"
	TypeAdjustment = "adjustment"
	TypeReversal   = "reversal"
)

// Directions name the effect on the client balance.
const (
	Credit = "credit"
	Debit  = "debit"
)

// Flag scopes.
const (
	ScopeMatter  = "matter"
	ScopeClient  = "client"
	ScopeAccount = "account"
)

// Replenishment statuses.
const (
	ReplenishActive    = "active"
	ReplenishSatisfied = "satisfied"
	ReplenishCancelled = "cancelled"
)

// Flat-fee worklist statuses (PRD 8.15 Billing / Generate).
const (
	FeeNeedsAmount    = "needs_fee_amount"
	FeeAwaitingFunds  = "awaiting_trust_funds"
	FeeReadyToApply   = "ready_to_apply"
	FeeCollected      = "collected"
	FeeStatusNotFlat  = "not_flat_fee"
	NotifyTrustFlag   = "trust_flag"
	NotifyReplenish   = "replenishment_request"
	NotifyReconcile   = "reconciliation_reminder"
	MailKindTrustFlag = "trust_flag"
)

var methods = map[string]bool{"cash": true, "check": true, "card": true, "ach": true, "internal_transfer": true, "other": true}

// Account is a firm trust bank account. The full account number is never stored.
type Account struct {
	ID        domain.ID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy domain.ID `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Bank      string    `json:"bank"`
	Name      string    `json:"name"`
	IOLTA     bool      `json:"iolta"`
	LastFour  string    `json:"last_four"`
	Active    bool      `json:"active"`
}

// AccountInput creates an account. Active defaults to true.
type AccountInput struct {
	Bank     string `json:"bank"`
	Name     string `json:"name"`
	IOLTA    bool   `json:"iolta"`
	LastFour string `json:"last_four"`
	Active   *bool  `json:"active,omitempty"`
}

// AccountPatch changes only the fields that are set.
type AccountPatch struct {
	Bank     *string `json:"bank,omitempty"`
	Name     *string `json:"name,omitempty"`
	IOLTA    *bool   `json:"iolta,omitempty"`
	LastFour *string `json:"last_four,omitempty"`
	Active   *bool   `json:"active,omitempty"`
}

// Entry is one ledger row as the API shows it. AmountCents follows the contract: a
// magnitude for deposit, withdrawal, transfer and refund; signed for an adjustment and a
// reversal. Signed is the effect on the client balance in every case.
type Entry struct {
	ID                  domain.ID  `json:"id"`
	CreatedAt           time.Time  `json:"created_at"`
	CreatedBy           domain.ID  `json:"created_by"`
	TrustAccountID      domain.ID  `json:"trust_account_id"`
	ClientID            domain.ID  `json:"client_id"`
	MatterID            domain.ID  `json:"matter_id"`
	EntryType           string     `json:"entry_type"`
	AmountCents         int64      `json:"amount_cents"`
	AdjustmentDirection string     `json:"adjustment_direction,omitempty"`
	Direction           string     `json:"direction,omitempty"`
	Method              string     `json:"method"`
	Description         string     `json:"description"`
	ReversesEntryID     *domain.ID `json:"reverses_entry_id"`

	Signed     domain.Cents `json:"-"`
	StoredType string       `json:"-"`
}

// PostInput is TrustEntryWrite. AmountCents is a magnitude except for an adjustment,
// where it is signed and AdjustmentDirection must match the sign.
type PostInput struct {
	TrustAccountID      domain.ID `json:"trust_account_id"`
	ClientID            domain.ID `json:"client_id"`
	MatterID            domain.ID `json:"matter_id"`
	EntryType           string    `json:"entry_type"`
	AmountCents         int64     `json:"amount_cents"`
	AdjustmentDirection string    `json:"adjustment_direction,omitempty"`
	Method              string    `json:"method"`
	Description         string    `json:"description"`
	// ReversesEntryID is in the contract's TrustEntryWrite. It must be null or absent:
	// a reversal is posted through /trust-ledger/{id}/reversals.
	ReversesEntryID *domain.ID `json:"reverses_entry_id,omitempty"`
}

// ReverseInput is the body of a reversal. The reason is required and becomes the
// reversal row's description; the method is the original row's.
type ReverseInput struct {
	Reason string `json:"reason"`
}

// Balances is the three-amount snapshot returned with a posted entry.
type Balances struct {
	MatterCents  int64 `json:"matter_cents"`
	ClientCents  int64 `json:"client_cents"`
	AccountCents int64 `json:"account_cents"`
}

// PostResult is TrustPostResult. Flags is non-empty when a balance went below zero.
// Events are the commit events; Service publishes them itself unless the post joined a
// caller's transaction, in which case the caller publishes them after its commit.
type PostResult struct {
	Entry    Entry    `json:"entry"`
	Balances Balances `json:"balances"`
	Flags    []Flag   `json:"flags"`
	Events   []Event  `json:"-"`

	cached []domain.TrustBalance
}

// BalanceRow is TrustBalanceRow.
type BalanceRow struct {
	Scope          string    `json:"scope"`
	ClientID       domain.ID `json:"client_id,omitempty"`
	MatterID       domain.ID `json:"matter_id,omitempty"`
	TrustAccountID domain.ID `json:"trust_account_id,omitempty"`
	BalanceCents   int64     `json:"balance_cents"`
}

// Flag is a below-zero flag. It is a warning, never a block.
type Flag struct {
	ID             domain.ID  `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	CreatedBy      domain.ID  `json:"created_by"`
	UpdatedAt      time.Time  `json:"updated_at"`
	TrustEntryID   domain.ID  `json:"trust_entry_id"`
	Scope          string     `json:"scope"`
	ClientID       domain.ID  `json:"client_id,omitempty"`
	MatterID       domain.ID  `json:"matter_id,omitempty"`
	TrustAccountID domain.ID  `json:"trust_account_id,omitempty"`
	BalanceCents   int64      `json:"balance_cents"`
	Status         string     `json:"status"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	ResolvedBy     *domain.ID `json:"resolved_by"`
	ResolvedAt     *time.Time `json:"resolved_at"`

	reason      string
	entryMatter domain.ID
	entryClient domain.ID
	// entryAccount is the posting's trust account. It filters and authorizes every
	// scope, but only an account-scope flag carries trust_account_id in its JSON: the
	// matter and client TrustFlag shapes do not allow it.
	entryAccount domain.ID
}

// Replenishment is a trust replenishment request.
type Replenishment struct {
	ID             domain.ID  `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	CreatedBy      domain.ID  `json:"created_by"`
	UpdatedAt      time.Time  `json:"updated_at"`
	MatterID       domain.ID  `json:"matter_id"`
	ClientID       domain.ID  `json:"client_id"`
	AmountCents    int64      `json:"amount_requested_cents"`
	ThresholdCents *int64     `json:"threshold_cents"`
	InvoiceID      *domain.ID `json:"invoice_id"`
	Message        string     `json:"message"`
	Status         string     `json:"status"`
}

// ReplenishmentInput is TrustReplenishmentWrite.
type ReplenishmentInput struct {
	MatterID    domain.ID `json:"matter_id"`
	ClientID    domain.ID `json:"client_id"`
	AmountCents *int64    `json:"amount_requested_cents"`
	InvoiceID   domain.ID `json:"invoice_id,omitempty"`
	Message     string    `json:"message,omitempty"`
}

// Reconciliation is one stored monthly reconciliation.
type Reconciliation struct {
	ID                       domain.ID `json:"id"`
	CreatedAt                time.Time `json:"created_at"`
	CreatedBy                domain.ID `json:"created_by"`
	UpdatedAt                time.Time `json:"updated_at"`
	TrustAccountID           domain.ID `json:"trust_account_id"`
	StatementOn              string    `json:"statement_on"`
	StatementBalanceCents    int64     `json:"statement_balance_cents"`
	LedgerTotalCents         int64     `json:"ledger_total_cents"`
	ClientBalancesSumCents   int64     `json:"client_balances_sum_cents"`
	StatementDifferenceCents int64     `json:"statement_difference_cents"`
	ClientSumDifferenceCents int64     `json:"client_sum_difference_cents"`
}

// ReconcileInput is TrustReconciliationWrite.
type ReconcileInput struct {
	TrustAccountID        domain.ID `json:"trust_account_id"`
	StatementOn           string    `json:"statement_on"`
	StatementBalanceCents *int64    `json:"statement_balance_cents"`
}

// Notification is one user's notification.
type Notification struct {
	ID        domain.ID  `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy domain.ID  `json:"created_by"`
	UpdatedAt time.Time  `json:"updated_at"`
	UserID    domain.ID  `json:"user_id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Link      string     `json:"link"`
	ReadAt    *time.Time `json:"read_at"`

	matterID domain.ID
}

// Event is a commit event such as trust.entry_posted or trust.flag_raised.
type Event struct {
	Name     string            `json:"name"`
	RecordID domain.ID         `json:"record_id"`
	MatterID domain.ID         `json:"matter_id,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}

// FlatFee is one flat-fee worklist row.
type FlatFee struct {
	MatterID       domain.ID `json:"matter_id"`
	MatterNumber   string    `json:"matter_number"`
	MatterName     string    `json:"matter_name"`
	ClientID       domain.ID `json:"client_id,omitempty"`
	FlatFeeCents   int64     `json:"flat_fee_cents"`
	TrustCents     int64     `json:"trust_balance_cents"`
	CollectedCents int64     `json:"collected_cents"`
	Status         string    `json:"status"`
}

// ApplyFlatFeeInput moves a confirmed flat fee from trust to operating.
type ApplyFlatFeeInput struct {
	MatterID       domain.ID `json:"matter_id"`
	ClientID       domain.ID `json:"client_id,omitempty"`
	TrustAccountID domain.ID `json:"trust_account_id,omitempty"`
	AmountCents    *int64    `json:"amount_cents"`
}

// Report is a report table. format=csv renders the same rows.
type Report struct {
	Report  string              `json:"report"`
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

// Widget is a home-screen widget payload.
type Widget struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Target   Target      `json:"target"`
	PagePath string      `json:"page_path"`
	Rows     []WidgetRow `json:"rows"`
}

// Target is where a widget title opens, with the same filters as the rows.
type Target struct {
	Path  string            `json:"path"`
	Query map[string]string `json:"query"`
}

// WidgetRow is one widget row; clicking it opens TargetPath.
type WidgetRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	TargetPath string `json:"target_path"`
}

// Page is a cursor page.
type Page[T any] struct {
	Items      []T
	Limit      int
	NextCursor string
}

// EntryFilter selects ledger rows. Dates are inclusive, YYYY-MM-DD, UTC.
type EntryFilter struct {
	Cursor         string
	Limit          int
	MatterID       domain.ID
	ClientID       domain.ID
	TrustAccountID domain.ID
	FromDate       string
	ToDate         string
	// newest, when set, reads that many entries newest first, before the id before.
	newest int
	before string
}

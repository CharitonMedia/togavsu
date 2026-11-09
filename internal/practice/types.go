package practice

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const (
	KindPerson       = "person"
	KindOrganization = "organization"

	StatusProspect = "prospect"
	StatusOpen     = "open"
	StatusOnHold   = "on_hold"
	StatusClosed   = "closed"

	BillingFlat        = "flat"
	BillingHourly      = "hourly"
	BillingMixed       = "mixed"
	BillingContingency = "contingency"
	BillingProBono     = "pro_bono"

	NoteActive   = "active"
	NoteArchived = "archived"

	FieldText     = "text"
	FieldLongText = "long_text"
	FieldNumber   = "number"
	FieldMoney    = "money"
	FieldDate     = "date"
	FieldBoolean  = "boolean"
	FieldPickList = "pick_list"
	FieldContact  = "contact"

	RecordMatter    = "matter"
	RecordContact   = "contact"
	RecordTimeEntry = "time_entry"
	RecordExpense   = "expense"

	WarnUnpaidInvoice = "unpaid_invoice"
	WarnNonzeroTrust  = "nonzero_trust"

	MarkerProBono   = "Pro bono"
	MarkerZeroFee   = "Zero fee"
	MarkerAlsoAsked = "Also asked about"

	DecisionClear    = "clear"
	DecisionEscalate = "escalate"
	DecisionNoGo     = "no_go"

	ScopeAll      = "all"
	ScopeMatters  = "matters"
	ScopePeople   = "people"
	ScopeInvoices = "invoices"
	ScopeFiles    = "files"
)

// Redacted is the history and search stand-in for an attorney-only value.
const Redacted = "[redacted]"

// Warning is a close-screen warning. It does not block the close.
type Warning struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// CloseRecord is the stored close, including the warning acknowledgement.
type CloseRecord struct {
	ID             domain.ID
	MatterID       domain.ID
	Reason         string
	Warnings       []Warning
	Acknowledged   bool
	PreviousStatus string
	ClosedOn       string
	At             time.Time
	ActorID        domain.ID
}

// CloseInput closes a matter. Acknowledge records that the warning was shown.
type CloseInput struct {
	Reason         string
	Acknowledge    bool
	IdempotencyKey string
}

// FieldDef is one custom field. An empty PracticeAreaIDs list applies to every area.
type FieldDef struct {
	ID              domain.ID
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CreatedBy       domain.ID
	RecordType      string
	Key             string
	Label           string
	FieldType       string
	SortOrder       int
	Required        bool
	PracticeAreaIDs []domain.ID
	Options         []string
	Retired         bool
}

// FieldValue is one saved custom-field value.
type FieldValue struct {
	DefinitionID domain.ID
	Key          string
	RecordType   string
	RecordID     domain.ID
	Value        string
}

// CustomInput is one value written with a record.
type CustomInput struct {
	DefinitionID domain.ID
	Key          string
	Value        string
}

// ShownField is a custom field visible on a record.
type ShownField struct {
	DefinitionID domain.ID
	Key          string
	Label        string
	FieldType    string
	Required     bool
	Options      []string
	Value        string
	SortOrder    int
}

// MarkerDef is a marker label the firm can pin to a matter.
type MarkerDef struct {
	ID    domain.ID
	Label string
	Color string
}

// MatterMarker is a marker pinned to one matter.
type MatterMarker struct {
	ID             domain.ID
	MatterID       domain.ID
	MarkerID       domain.ID
	Label          string
	PracticeAreaID domain.ID
	CreatedAt      time.Time
	CreatedBy      domain.ID
}

// ContactInput creates or edits a contact. Phones are E.164.
type ContactInput struct {
	Kind           string
	GivenName      string
	MiddleName     string
	FamilyName     string
	Prefix         string
	Suffix         string
	FullName       string
	PreferredName  string
	AlsoKnownAs    string
	DateOfBirth    string
	DateOfDeath    string
	SSNEIN         string
	EntityType     string
	FormationState string
	FormationDate  string
	Archived       bool
	Source         string
	Custom         []CustomInput
	IdempotencyKey string
}

// MatterInput creates or edits the editable matter fields.
type MatterInput struct {
	Name                  string
	PracticeAreaID        domain.ID
	Status                string
	Stage                 string
	CaseNumber            string
	Court                 string
	CountyID              domain.ID
	ClientPosition        string
	OpenedOn              string
	StatuteOfLimitations  string
	BillingType           string
	FlatFeeCents          domain.Cents
	MatterRateCents       domain.Cents
	ResponsibleAttorneyID domain.ID
	AssignedStaffID       domain.ID
	ReferralSourceID      domain.ID
	Custom                []CustomInput
	IdempotencyKey        string
}

// PartyInput links a contact to a matter.
type PartyInput struct {
	ContactID      domain.ID
	Role           string
	Primary        bool
	RoleAttributes string
	ServiceAddress string
	Notes          string
}

// NoteInput is a matter or contact note. At least one parent is required.
type NoteInput struct {
	MatterID  domain.ID
	ContactID domain.ID
	Title     string
	Body      string
	Pinned    bool
	Tags      []string
	Status    string
}

// NoteFilter is the firm-wide notes query.
type NoteFilter struct {
	Query    string
	MatterID domain.ID
	AuthorID domain.ID
	Tag      string
	From     string
	To       string
	Cursor   string
	Limit    int
}

// ConflictInput is a name the firm checks.
type ConflictInput struct {
	Name         string
	Aliases      string
	DateOfBirth  string
	Relationship string
	Scope        string
	Reason       string
	MatterID     domain.ID
	StartsOn     string
	EndsOn       string
	Source       string
}

// Hit is one scored conflict match.
type Hit struct {
	ID                domain.ID
	MatchedRecordType string
	MatchedID         domain.ID
	Score             int
	Name              string
	Decision          string
	Reason            string
	ReviewID          domain.ID
}

// ConflictRun is one conflict check on a matter.
type ConflictRun struct {
	ID        domain.ID
	MatterID  domain.ID
	CreatedAt time.Time
	CreatedBy domain.ID
	Hits      []Hit
}

// HistoryRow is one edit-history line.
type HistoryRow struct {
	ID       domain.ID
	At       time.Time
	ActorID  domain.ID
	Role     string
	Source   string
	Entity   string
	Field    string
	Old      string
	New      string
	Kind     string
	Reason   string
	Redacted bool
}

// TimelineEntry is one case-timeline row.
type TimelineEntry struct {
	ID            domain.ID
	At            time.Time
	ActorKind     string
	ActorID       domain.ID
	Action        string
	Summary       string
	ClientVisible bool
	RecordType    string
	RecordID      domain.ID
	MatterID      domain.ID
	ActorName     string
}

// SearchHit is one search result.
type SearchHit struct {
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
	RecordType string    `json:"record_type"`
	RecordID   domain.ID `json:"record_id"`
	TargetPath string    `json:"target_path"`
}

// SearchGroup is one scope in a search response.
type SearchGroup struct {
	Scope string      `json:"scope"`
	Hits  []SearchHit `json:"hits"`
}

// SearchResult is the grouped search response.
type SearchResult struct {
	Query  string        `json:"query"`
	Scope  string        `json:"scope"`
	Groups []SearchGroup `json:"groups"`
}

// WidgetRow is one clickable widget row.
type WidgetRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	TargetPath string `json:"target_path"`
}

// Widget is a home-screen widget payload.
type Widget struct {
	ID       string
	Title    string
	PagePath string
	Query    map[string]string
	Rows     []WidgetRow
}

// ReportTable is a report. CSV uses the same columns and rows.
type ReportTable struct {
	Report  string              `json:"report"`
	Columns []string            `json:"columns"`
	Rows    []map[string]string `json:"rows"`
}

// AgingRow is one AR aging line. Zero-fee matters are not rows.
type AgingRow struct {
	MatterID  domain.ID
	InvoiceID domain.ID
	Number    string
	Balance   domain.Cents
	AgeDays   int
}

// Glance is the matter At a Glance card.
type Glance struct {
	Stage          string
	TrustCents     domain.Cents
	TimeHours      string
	TimeEntries    int
	DaysOpen       int
	Invoices       int
	Files          int
	OpenExceptions int
}

// MatterView is a matter plus the practice cards.
type MatterView struct {
	Matter       domain.Matter
	Parties      []domain.MatterParty
	CustomFields []ShownField
	Markers      []MatterMarker
	Unchecked    bool
	Close        *CloseRecord
	Glance       Glance
	ClientName   string
}

// ActivityGroup is one Activities Feed heading.
type ActivityGroup struct {
	Label string
	Rows  []TimelineEntry
}

// ActivityChip is one staff person on the activity feed.
type ActivityChip struct {
	ID    domain.ID
	Name  string
	Count int
}

// ActivityQuery selects a tab, a person, and a page.
type ActivityQuery struct {
	Tab    string
	UserID domain.ID
	Cursor string
}

// ActivityView is the firm activity feed with tab counts and paging.
type ActivityView struct {
	Groups  []ActivityGroup
	All     int
	Clients int
	Staff   int
	Agents  int
	People  []ActivityChip
	Next    string
	Tab     string
	UserID  string
}

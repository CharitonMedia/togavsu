package domain

import "time"

// Meta is the column set every core table carries.
type Meta struct {
	ID        ID        `db:"id"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
	CreatedBy ID        `db:"created_by"`
}

// Contact is a person or an organization.
type Contact struct {
	Meta
	Kind             string `db:"kind"`
	GivenName        string `db:"given_name"`
	MiddleName       string `db:"middle_name"`
	FamilyName       string `db:"family_name"`
	Prefix           string `db:"prefix"`
	Suffix           string `db:"suffix"`
	FullName         string `db:"full_name"`
	PreferredName    string `db:"preferred_name"`
	AlsoKnownAs      string `db:"also_known_as"`
	DateOfBirth      string `db:"date_of_birth"`
	DateOfDeath      string `db:"date_of_death"`
	SSNEINCiphertext string `db:"ssn_ein_ciphertext"`
	EntityType       string `db:"entity_type"`
	FormationState   string `db:"formation_state"`
	FormationDate    string `db:"formation_date"`
	Archived         bool   `db:"archived"`
	Source           string `db:"source"`
}

func (Contact) TableName() string { return "contacts" }

// ContactAddress is one historical address.
type ContactAddress struct {
	Meta
	ContactID     ID     `db:"contact_id"`
	Label         string `db:"label"`
	Primary       bool   `db:"is_primary"`
	Line1         string `db:"line1"`
	Line2         string `db:"line2"`
	City          string `db:"city"`
	Region        string `db:"region"`
	PostalCode    string `db:"postal_code"`
	Country       string `db:"country"`
	EffectiveFrom string `db:"effective_from"`
	EffectiveTo   string `db:"effective_to"`
}

func (ContactAddress) TableName() string { return "contact_addresses" }

// ContactEmail is one historical email.
type ContactEmail struct {
	Meta
	ContactID     ID     `db:"contact_id"`
	Label         string `db:"label"`
	Primary       bool   `db:"is_primary"`
	Email         string `db:"email"`
	EffectiveFrom string `db:"effective_from"`
	EffectiveTo   string `db:"effective_to"`
}

func (ContactEmail) TableName() string { return "contact_emails" }

// ContactPhone is one historical E.164 phone.
type ContactPhone struct {
	Meta
	ContactID     ID     `db:"contact_id"`
	Label         string `db:"label"`
	Primary       bool   `db:"is_primary"`
	PhoneE164     string `db:"phone_e164"`
	EffectiveFrom string `db:"effective_from"`
	EffectiveTo   string `db:"effective_to"`
}

func (ContactPhone) TableName() string { return "contact_phones" }

// ContactTag is one free tag on a contact.
type ContactTag struct {
	Meta
	ContactID ID     `db:"contact_id"`
	Tag       string `db:"tag"`
}

func (ContactTag) TableName() string { return "contact_tags" }

// Matter is a firm matter.
type Matter struct {
	Meta
	Number                string `db:"number"`
	Name                  string `db:"name"`
	PracticeAreaID        ID     `db:"practice_area_id"`
	Status                string `db:"status"`
	Stage                 string `db:"stage"`
	CaseNumber            string `db:"case_number"`
	Court                 string `db:"court"`
	CountyID              ID     `db:"county_id"`
	ClientPosition        string `db:"client_position"`
	OpenedOn              string `db:"opened_on"`
	ClosedOn              string `db:"closed_on"`
	CloseReason           string `db:"close_reason"`
	StatuteOfLimitations  string `db:"statute_of_limitations"`
	BillingType           string `db:"billing_type"`
	FlatFeeCents          Cents  `db:"flat_fee_cents"`
	MatterRateCents       Cents  `db:"matter_rate_cents"`
	ResponsibleAttorneyID ID     `db:"responsible_attorney_id"`
	AssignedStaffID       ID     `db:"assigned_staff_id"`
	ReferralSourceID      ID     `db:"referral_source_id"`
}

func (Matter) TableName() string { return "matters" }

// MatterParty links a contact to a matter in a role.
type MatterParty struct {
	Meta
	MatterID       ID     `db:"matter_id"`
	ContactID      ID     `db:"contact_id"`
	Role           string `db:"role"`
	Primary        bool   `db:"is_primary"`
	RoleAttributes string `db:"role_attributes"`
	ServiceAddress string `db:"service_address"`
	Notes          string `db:"notes"`
}

func (MatterParty) TableName() string { return "matter_parties" }

// MatterWall names one user allowed into a walled matter.
type MatterWall struct {
	Meta
	MatterID ID `db:"matter_id"`
	UserID   ID `db:"user_id"`
}

func (MatterWall) TableName() string { return "matter_walls" }

// PracticeArea is an admin-maintained list entry.
type PracticeArea struct {
	Meta
	Name        string `db:"name"`
	MatterTypes string `db:"matter_types"`
	Stages      string `db:"stages"`
	Active      bool   `db:"active"`
}

func (PracticeArea) TableName() string { return "practice_areas" }

// County is a county the firm uses.
type County struct {
	Meta
	Name  string `db:"name"`
	State string `db:"state"`
}

func (County) TableName() string { return "counties" }

// ReferralSource is an admin-maintained referral list entry.
type ReferralSource struct {
	Meta
	Name      string `db:"name"`
	Kind      string `db:"kind"`
	ContactID ID     `db:"contact_id"`
	Active    bool   `db:"active"`
}

func (ReferralSource) TableName() string { return "referral_sources" }

// User is a person who can sign in.
type User struct {
	Meta
	Name                   string `db:"name"`
	Email                  string `db:"email"`
	Roles                  string `db:"roles"`
	Active                 bool   `db:"active"`
	DefaultHourlyRateCents Cents  `db:"default_hourly_rate_cents"`
	ContactID              ID     `db:"contact_id"`
	BarNumber              string `db:"bar_number"`
	BarState               string `db:"bar_state"`
}

func (User) TableName() string { return "users" }

// Session is a browser session.
type Session struct {
	Meta
	UserID        ID         `db:"user_id"`
	TokenHash     string     `db:"token_hash"`
	ExpiresAt     time.Time  `db:"expires_at"`
	IdleExpiresAt time.Time  `db:"idle_expires_at"`
	RevokedAt     *time.Time `db:"revoked_at"`
}

func (Session) TableName() string { return "sessions" }

// PasswordCredential stores an Argon2id PHC string. Identity computes the hash.
type PasswordCredential struct {
	Meta
	UserID     ID     `db:"user_id"`
	Argon2Hash string `db:"argon2_hash"`
}

func (PasswordCredential) TableName() string { return "password_credentials" }

// TOTPSecret stores the encrypted authenticator secret.
type TOTPSecret struct {
	Meta
	UserID     ID     `db:"user_id"`
	Ciphertext string `db:"ciphertext"`
	Confirmed  bool   `db:"confirmed"`
}

func (TOTPSecret) TableName() string { return "totp_secrets" }

// RecoveryCode is one single-use recovery code hash.
type RecoveryCode struct {
	Meta
	UserID   ID         `db:"user_id"`
	CodeHash string     `db:"code_hash"`
	UsedAt   *time.Time `db:"used_at"`
}

func (RecoveryCode) TableName() string { return "recovery_codes" }

// APIToken is a revocable bearer token for a user or a module.
type APIToken struct {
	Meta
	UserID    ID         `db:"user_id"`
	ModuleID  string     `db:"module_id"`
	Name      string     `db:"name"`
	TokenHash string     `db:"token_hash"`
	Scopes    string     `db:"scopes"`
	RevokedAt *time.Time `db:"revoked_at"`
}

func (APIToken) TableName() string { return "api_tokens" }

// SettingRow is one stored setting override or default row.
type SettingRow struct {
	Meta
	Key   string `db:"key"`
	Value string `db:"value"`
}

func (SettingRow) TableName() string { return "settings" }

// FeeAgreement is the one active fee agreement on a matter.
type FeeAgreement struct {
	Meta
	MatterID    ID     `db:"matter_id"`
	FeeType     string `db:"fee_type"`
	AmountCents Cents  `db:"amount_cents"`
	Services    string `db:"services"`
	Status      string `db:"status"`
	SignedOn    string `db:"signed_on"`
	VoidReason  string `db:"void_reason"`
	FileID      ID     `db:"file_id"`
}

func (FeeAgreement) TableName() string { return "fee_agreements" }

// TimeEntry is worked time. InvoiceID is empty until the entry is billed.
type TimeEntry struct {
	Meta
	MatterID     ID     `db:"matter_id"`
	UserID       ID     `db:"user_id"`
	WorkedOn     string `db:"worked_on"`
	Increments   int    `db:"increments"`
	Description  string `db:"description"`
	ActivityType string `db:"activity_type"`
	Billable     bool   `db:"billable"`
	RateCents    Cents  `db:"rate_cents"`
	InvoiceID    ID     `db:"invoice_id"`
	Source       string `db:"source"`
}

func (TimeEntry) TableName() string { return "time_entries" }

// Timer is a running or stopped timer. Stopping it creates a draft time entry.
type Timer struct {
	Meta
	MatterID    ID         `db:"matter_id"`
	UserID      ID         `db:"user_id"`
	StartedAt   time.Time  `db:"started_at"`
	StoppedAt   *time.Time `db:"stopped_at"`
	TimeEntryID ID         `db:"time_entry_id"`
}

func (Timer) TableName() string { return "timers" }

// Expense is a cost on a matter.
type Expense struct {
	Meta
	MatterID      ID     `db:"matter_id"`
	IncurredOn    string `db:"incurred_on"`
	Description   string `db:"description"`
	AmountCents   Cents  `db:"amount_cents"`
	ExpenseType   string `db:"expense_type"`
	Billable      bool   `db:"billable"`
	MarkupPercent int    `db:"markup_percent"`
	FileID        ID     `db:"file_id"`
	InvoiceID     ID     `db:"invoice_id"`
}

func (Expense) TableName() string { return "expenses" }

// Invoice is a bill. Line items live on InvoiceLine.
type Invoice struct {
	Meta
	MatterID         ID     `db:"matter_id"`
	Number           string `db:"number"`
	IssuedOn         string `db:"issued_on"`
	Status           string `db:"status"`
	SubtotalCents    Cents  `db:"subtotal_cents"`
	AdjustmentCents  Cents  `db:"adjustment_cents"`
	AdjustmentReason string `db:"adjustment_reason"`
	InterestCents    Cents  `db:"interest_cents"`
	TotalCents       Cents  `db:"total_cents"`
	AmountPaidCents  Cents  `db:"amount_paid_cents"`
	BalanceDueCents  Cents  `db:"balance_due_cents"`
	WriteOffCents    Cents  `db:"write_off_cents"`
	WriteOffReason   string `db:"write_off_reason"`
	VoidReason       string `db:"void_reason"`
	DisputeStatus    string `db:"dispute_status"`
	DisputeNotes     string `db:"dispute_notes"`
	SentAt           int64  `db:"sent_at"`
	PaidAt           int64  `db:"paid_at"`
	ClientMessage    string `db:"client_message"`
	InternalNotes    string `db:"internal_notes"`
}

func (Invoice) TableName() string { return "invoices" }

// InvoiceLine is one time, expense, flat-fee, or adjustment line.
type InvoiceLine struct {
	Meta
	InvoiceID   ID     `db:"invoice_id"`
	Kind        string `db:"kind"`
	Description string `db:"description"`
	AmountCents Cents  `db:"amount_cents"`
	TimeEntryID ID     `db:"time_entry_id"`
	ExpenseID   ID     `db:"expense_id"`
}

func (InvoiceLine) TableName() string { return "invoice_lines" }

// Payment is money received. Destination is operating or trust.
type Payment struct {
	Meta
	ContactID       ID     `db:"contact_id"`
	MatterID        ID     `db:"matter_id"`
	AmountCents     Cents  `db:"amount_cents"`
	Destination     string `db:"destination"`
	Method          string `db:"method"`
	ReferenceNumber string `db:"reference_number"`
	Status          string `db:"status"`
	InvoiceID       ID     `db:"invoice_id"`
	TrustEntryID    ID     `db:"trust_entry_id"`
	Source          string `db:"source"`
}

func (Payment) TableName() string { return "payments" }

// TrustAccount is one of the firm's trust bank accounts.
type TrustAccount struct {
	Meta
	Bank     string `db:"bank"`
	Name     string `db:"name"`
	IOLTA    bool   `db:"iolta"`
	LastFour string `db:"last_four"`
	Active   bool   `db:"active"`
}

func (TrustAccount) TableName() string { return "trust_accounts" }

// TrustEntry is one append-only ledger row. AmountCents is signed.
// A reversal is a new row with ReversesID set. Rows are never updated or deleted.
type TrustEntry struct {
	Meta
	TrustAccountID ID     `db:"trust_account_id"`
	ClientID       ID     `db:"client_id"`
	MatterID       ID     `db:"matter_id"`
	EntryType      string `db:"entry_type"`
	AmountCents    Cents  `db:"amount_cents"`
	Method         string `db:"method"`
	Description    string `db:"description"`
	ReversesID     ID     `db:"reverses_id"`
}

func (TrustEntry) TableName() string { return "trust_ledger" }

// TrustBalance is a cached sum. The ledger remains the source of truth.
type TrustBalance struct {
	Meta
	TrustAccountID ID    `db:"trust_account_id"`
	ClientID       ID    `db:"client_id"`
	MatterID       ID    `db:"matter_id"`
	BalanceCents   Cents `db:"balance_cents"`
}

func (TrustBalance) TableName() string { return "trust_balance_cache" }

// TrustFlag is raised when a balance goes below zero. It is not a block.
type TrustFlag struct {
	Meta
	TrustEntryID   ID     `db:"trust_entry_id"`
	MatterID       ID     `db:"matter_id"`
	ClientID       ID     `db:"client_id"`
	Reason         string `db:"reason"`
	ResolvedAt     int64  `db:"resolved_at"`
	ResolvedBy     ID     `db:"resolved_by"`
	ResolutionNote string `db:"resolution_note"`
}

func (TrustFlag) TableName() string { return "trust_flags" }

// TrustReplenishment is an evergreen request.
type TrustReplenishment struct {
	Meta
	MatterID       ID     `db:"matter_id"`
	ClientID       ID     `db:"client_id"`
	AmountCents    Cents  `db:"amount_cents"`
	ThresholdCents Cents  `db:"threshold_cents"`
	Message        string `db:"message"`
	Status         string `db:"status"`
	InvoiceID      ID     `db:"invoice_id"`
}

func (TrustReplenishment) TableName() string { return "trust_replenishment_requests" }

// TrustReconciliation is one monthly reconciliation.
type TrustReconciliation struct {
	Meta
	TrustAccountID        ID     `db:"trust_account_id"`
	StatementOn           string `db:"statement_on"`
	StatementBalanceCents Cents  `db:"statement_balance_cents"`
	LedgerTotalCents      Cents  `db:"ledger_total_cents"`
	ClientBalanceCents    Cents  `db:"client_balance_cents"`
	DifferenceCents       Cents  `db:"difference_cents"`
}

func (TrustReconciliation) TableName() string { return "trust_reconciliations" }

// Note is a matter or contact note.
type Note struct {
	Meta
	MatterID  ID     `db:"matter_id"`
	ContactID ID     `db:"contact_id"`
	AuthorID  ID     `db:"author_id"`
	Title     string `db:"title"`
	Body      string `db:"body"`
	Pinned    bool   `db:"pinned"`
	Status    string `db:"status"`
}

func (Note) TableName() string { return "notes" }

// NoteTag is one free tag on a note.
type NoteTag struct {
	Meta
	NoteID ID     `db:"note_id"`
	Tag    string `db:"tag"`
}

func (NoteTag) TableName() string { return "note_tags" }

// ConflictRecord is a name the firm checks new matters against.
type ConflictRecord struct {
	Meta
	Name         string `db:"name"`
	Aliases      string `db:"aliases"`
	DateOfBirth  string `db:"date_of_birth"`
	Relationship string `db:"relationship"`
	Scope        string `db:"scope"`
	Reason       string `db:"reason"`
	MatterID     ID     `db:"matter_id"`
	StartsOn     string `db:"starts_on"`
	EndsOn       string `db:"ends_on"`
	Source       string `db:"source"`
}

func (ConflictRecord) TableName() string { return "conflict_records" }

// ConflictReview is an attorney decision on one hit.
type ConflictReview struct {
	Meta
	MatterID         ID     `db:"matter_id"`
	ConflictRecordID ID     `db:"conflict_record_id"`
	Confidence       int    `db:"confidence"`
	Decision         string `db:"decision"`
	Reason           string `db:"reason"`
	DecidedBy        ID     `db:"decided_by"`
	DecidedAt        int64  `db:"decided_at"`
}

func (ConflictReview) TableName() string { return "conflict_reviews" }

// CalendarEvent is a firm calendar item.
type CalendarEvent struct {
	Meta
	Title         string `db:"title"`
	Kind          string `db:"kind"`
	StartsAt      int64  `db:"starts_at"`
	EndsAt        int64  `db:"ends_at"`
	AllDay        bool   `db:"all_day"`
	MatterID      ID     `db:"matter_id"`
	Location      string `db:"location"`
	TravelMinutes int    `db:"travel_minutes"`
	Notes         string `db:"notes"`
}

func (CalendarEvent) TableName() string { return "calendar_events" }

// CalendarAttendee is one user on an event.
type CalendarAttendee struct {
	Meta
	EventID ID `db:"event_id"`
	UserID  ID `db:"user_id"`
}

func (CalendarAttendee) TableName() string { return "calendar_event_attendees" }

// Deadline is a matter deadline.
type Deadline struct {
	Meta
	MatterID          ID     `db:"matter_id"`
	Title             string `db:"title"`
	DueOn             string `db:"due_on"`
	ResponsibleUserID ID     `db:"responsible_user_id"`
	Status            string `db:"status"`
	ReminderOffsets   string `db:"reminder_offsets"`
	DoneBy            ID     `db:"done_by"`
	DoneAt            int64  `db:"done_at"`
}

func (Deadline) TableName() string { return "deadlines" }

// AuditEntry is one append-only audit row.
type AuditEntry struct {
	Meta
	ActorID  ID     `db:"actor_id"`
	Role     string `db:"role"`
	Source   string `db:"source"`
	Entity   string `db:"entity"`
	EntityID ID     `db:"entity_id"`
	Field    string `db:"field"`
	OldValue string `db:"old_value"`
	NewValue string `db:"new_value"`
	Reason   string `db:"reason"`
}

func (AuditEntry) TableName() string { return "audit_log" }

// Event is one append-only timeline and activity-feed row.
type Event struct {
	Meta
	ActorKind     string `db:"actor_kind"`
	ActorID       ID     `db:"actor_id"`
	Action        string `db:"action"`
	RecordType    string `db:"record_type"`
	RecordID      ID     `db:"record_id"`
	MatterID      ID     `db:"matter_id"`
	Summary       string `db:"summary"`
	ClientVisible bool   `db:"client_visible"`
}

func (Event) TableName() string { return "events" }

// StorageBackend is one place file bytes can live.
type StorageBackend struct {
	Meta
	Kind               string `db:"kind"`
	Name               string `db:"name"`
	SettingsCiphertext string `db:"settings_ciphertext"`
	Encryption         bool   `db:"encryption"`
	IsDefault          bool   `db:"is_default"`
	Health             string `db:"health"`
	BackedUp           bool   `db:"backed_up"`
	DropFolder         bool   `db:"drop_folder"`
}

func (StorageBackend) TableName() string { return "storage_backends" }

// StorageMove is a background copy between backends.
type StorageMove struct {
	Meta
	FromBackendID ID     `db:"from_backend_id"`
	ToBackendID   ID     `db:"to_backend_id"`
	Scope         string `db:"scope"`
	MatterID      ID     `db:"matter_id"`
	Status        string `db:"status"`
	Progress      int    `db:"progress"`
	ConfirmedBy   ID     `db:"confirmed_by"`
	ConfirmedAt   int64  `db:"confirmed_at"`
}

func (StorageMove) TableName() string { return "storage_moves" }

// File is the one file record. Bytes live on a storage backend, under the version id.
type File struct {
	Meta
	Name             string `db:"name"`
	Extension        string `db:"extension"`
	MatterID         ID     `db:"matter_id"`
	ContactID        ID     `db:"contact_id"`
	Folder           string `db:"folder"`
	Category         string `db:"category"`
	Status           string `db:"status"`
	Origin           string `db:"origin"`
	AttorneyOnly     bool   `db:"attorney_only"`
	SharedWithClient bool   `db:"shared_with_client"`
	Source           string `db:"source"`
	ArrivedName      string `db:"arrived_name"`
	LockedFields     string `db:"locked_fields"`
	CurrentVersionID ID     `db:"current_version_id"`
	TrashedAt        int64  `db:"trashed_at"`
	TrashedBy        ID     `db:"trashed_by"`
}

func (File) TableName() string { return "files" }

// FileVersion is immutable once written.
type FileVersion struct {
	Meta
	FileID           ID     `db:"file_id"`
	Number           int    `db:"number"`
	StorageBackendID ID     `db:"storage_backend_id"`
	StorageKey       string `db:"storage_key"`
	SizeBytes        int64  `db:"size_bytes"`
	SHA256           string `db:"sha256"`
	MediaType        string `db:"media_type"`
	SavedBy          ID     `db:"saved_by"`
	SavedAt          int64  `db:"saved_at"`
	How              string `db:"how"`
	Note             string `db:"note"`
	Quarantined      bool   `db:"quarantined"`
}

func (FileVersion) TableName() string { return "file_versions" }

// FileRendition is a Markdown copy, PDF preview, or grouped PDF. Markdown copies are kept.
type FileRendition struct {
	Meta
	FileVersionID    ID     `db:"file_version_id"`
	Kind             string `db:"kind"`
	Number           int    `db:"number"`
	StorageBackendID ID     `db:"storage_backend_id"`
	StorageKey       string `db:"storage_key"`
	SizeBytes        int64  `db:"size_bytes"`
	SHA256           string `db:"sha256"`
	MadeBy           ID     `db:"made_by"`
	MadeAt           int64  `db:"made_at"`
}

func (FileRendition) TableName() string { return "file_renditions" }

// FileLink is a typed relationship between two files.
type FileLink struct {
	Meta
	FromFileID ID     `db:"from_file_id"`
	ToFileID   ID     `db:"to_file_id"`
	Kind       string `db:"kind"`
}

func (FileLink) TableName() string { return "file_links" }

// EditLock is the document-editor lock on one file.
type EditLock struct {
	Meta
	FileID    ID    `db:"file_id"`
	UserID    ID    `db:"user_id"`
	Since     int64 `db:"since_at"`
	RenewedAt int64 `db:"renewed_at"`
}

func (EditLock) TableName() string { return "edit_locks" }

// CourtProfile is one LawCraft court profile stored as JSON text.
type CourtProfile struct {
	Meta
	Name        string `db:"name"`
	ProfileJSON string `db:"profile_json"`
	Verified    bool   `db:"verified"`
	VerifiedBy  ID     `db:"verified_by"`
	VerifiedAt  int64  `db:"verified_at"`
}

func (CourtProfile) TableName() string { return "court_profiles" }

// DocTenderJob is the processing job for one file version.
type DocTenderJob struct {
	Meta
	FileVersionID ID     `db:"file_version_id"`
	Stage         string `db:"stage"`
	Status        string `db:"status"`
	Attempts      int    `db:"attempts"`
	NextAttemptAt int64  `db:"next_attempt_at"`
	LastError     string `db:"last_error"`
	LeaseOwner    string `db:"lease_owner"`
	LeaseUntil    int64  `db:"lease_until"`
}

func (DocTenderJob) TableName() string { return "doc_tender_jobs" }

// DocTenderUnit is one page, photo, sheet, or media window.
type DocTenderUnit struct {
	Meta
	JobID       ID     `db:"job_id"`
	Unit        string `db:"unit"`
	Method      string `db:"method"`
	Model       string `db:"model"`
	Text        string `db:"text"`
	CheckResult string `db:"check_result"`
}

func (DocTenderUnit) TableName() string { return "doc_tender_units" }

// DocTenderResult is the published name, category, and cited fields for one version.
type DocTenderResult struct {
	Meta
	FileVersionID  ID     `db:"file_version_id"`
	NameParts      string `db:"name_parts"`
	Summary        string `db:"summary"`
	Category       string `db:"category"`
	CategoryHow    string `db:"category_how"`
	CriticalFields string `db:"critical_fields"`
	MatterID       ID     `db:"matter_id"`
}

func (DocTenderResult) TableName() string { return "doc_tender_results" }

// DocTenderException is one review-queue item.
type DocTenderException struct {
	Meta
	FileID     ID     `db:"file_id"`
	Kind       string `db:"kind"`
	OpenedAt   int64  `db:"opened_at"`
	Detail     string `db:"detail"`
	Status     string `db:"status"`
	ResolvedBy ID     `db:"resolved_by"`
	ResolvedAt int64  `db:"resolved_at"`
	Resolution string `db:"resolution"`
}

func (DocTenderException) TableName() string { return "doc_tender_exceptions" }

// AICall is one model call and its cost.
type AICall struct {
	Meta
	Task             string `db:"task"`
	Provider         string `db:"provider"`
	Model            string `db:"model"`
	PromptTokens     int    `db:"prompt_tokens"`
	CompletionTokens int    `db:"completion_tokens"`
	CostCents        Cents  `db:"cost_cents"`
	FellBack         bool   `db:"fell_back"`
	FallbackProvider string `db:"fallback_provider"`
	FallbackModel    string `db:"fallback_model"`
	UserID           ID     `db:"user_id"`
	ModuleID         string `db:"module_id"`
	MatterID         ID     `db:"matter_id"`
	FileID           ID     `db:"file_id"`
}

func (AICall) TableName() string { return "ai_call_log" }

// RecordTypes returns one zero value for every section 5 table.
func RecordTypes() []interface{ TableName() string } {
	return []interface{ TableName() string }{
		Contact{}, ContactAddress{}, ContactEmail{}, ContactPhone{}, ContactTag{},
		Matter{}, MatterParty{}, MatterWall{},
		PracticeArea{}, County{}, ReferralSource{},
		User{}, Session{}, PasswordCredential{}, TOTPSecret{}, RecoveryCode{}, APIToken{},
		SettingRow{},
		FeeAgreement{}, TimeEntry{}, Timer{}, Expense{},
		Invoice{}, InvoiceLine{}, Payment{},
		TrustAccount{}, TrustEntry{}, TrustBalance{}, TrustFlag{}, TrustReplenishment{}, TrustReconciliation{},
		Note{}, NoteTag{},
		ConflictRecord{}, ConflictReview{},
		CalendarEvent{}, CalendarAttendee{}, Deadline{},
		AuditEntry{}, Event{},
		File{}, FileVersion{}, FileRendition{}, FileLink{},
		StorageBackend{}, StorageMove{}, EditLock{}, CourtProfile{},
		DocCategory{}, DocTenderJob{}, DocTenderUnit{}, DocTenderResult{}, DocTenderException{},
		AICall{},
	}
}

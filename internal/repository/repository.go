package repository

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("repository: not found")

// ErrConflict is returned when a uniqueness rule fails.
var ErrConflict = errors.New("repository: conflict")

// ErrAppendOnly is returned when a caller tries to change an append-only row.
var ErrAppendOnly = errors.New("repository: append only")

// Page is a cursor page. An empty NextCursor means there are no further rows.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// Filter is the shared list contract. Later waves add fields here rather than new list styles.
type Filter struct {
	Cursor    string
	Limit     int
	MatterID  domain.ID
	ContactID domain.ID
	UserID    domain.ID
	AccountID domain.ID
	FileID    domain.ID
	Status    string
}

// Contacts stores people and organizations.
type Contacts interface {
	Insert(ctx context.Context, row domain.Contact) error
	Update(ctx context.Context, row domain.Contact) error
	Get(ctx context.Context, id domain.ID) (domain.Contact, error)
	List(ctx context.Context, filter Filter) (Page[domain.Contact], error)
}

// ContactAddresses stores historical addresses.
type ContactAddresses interface {
	Insert(ctx context.Context, row domain.ContactAddress) error
	Update(ctx context.Context, row domain.ContactAddress) error
	Get(ctx context.Context, id domain.ID) (domain.ContactAddress, error)
	List(ctx context.Context, filter Filter) (Page[domain.ContactAddress], error)
}

// ContactEmails stores historical emails.
type ContactEmails interface {
	Insert(ctx context.Context, row domain.ContactEmail) error
	Update(ctx context.Context, row domain.ContactEmail) error
	Get(ctx context.Context, id domain.ID) (domain.ContactEmail, error)
	List(ctx context.Context, filter Filter) (Page[domain.ContactEmail], error)
}

// ContactPhones stores historical E.164 phones.
type ContactPhones interface {
	Insert(ctx context.Context, row domain.ContactPhone) error
	Update(ctx context.Context, row domain.ContactPhone) error
	Get(ctx context.Context, id domain.ID) (domain.ContactPhone, error)
	List(ctx context.Context, filter Filter) (Page[domain.ContactPhone], error)
}

// ContactTags stores free contact tags.
type ContactTags interface {
	Insert(ctx context.Context, row domain.ContactTag) error
	Delete(ctx context.Context, id domain.ID) error
	List(ctx context.Context, filter Filter) (Page[domain.ContactTag], error)
}

// Matters stores matters.
type Matters interface {
	Insert(ctx context.Context, row domain.Matter) error
	Update(ctx context.Context, row domain.Matter) error
	Get(ctx context.Context, id domain.ID) (domain.Matter, error)
	List(ctx context.Context, filter Filter) (Page[domain.Matter], error)
}

// MatterParties stores matter parties.
type MatterParties interface {
	Insert(ctx context.Context, row domain.MatterParty) error
	Update(ctx context.Context, row domain.MatterParty) error
	Get(ctx context.Context, id domain.ID) (domain.MatterParty, error)
	List(ctx context.Context, filter Filter) (Page[domain.MatterParty], error)
}

// MatterWalls stores the users allowed into a walled matter.
type MatterWalls interface {
	Insert(ctx context.Context, row domain.MatterWall) error
	Delete(ctx context.Context, id domain.ID) error
	List(ctx context.Context, filter Filter) (Page[domain.MatterWall], error)
}

// PracticeAreas stores the admin practice-area list.
type PracticeAreas interface {
	Insert(ctx context.Context, row domain.PracticeArea) error
	Update(ctx context.Context, row domain.PracticeArea) error
	Get(ctx context.Context, id domain.ID) (domain.PracticeArea, error)
	List(ctx context.Context, filter Filter) (Page[domain.PracticeArea], error)
}

// Counties stores counties.
type Counties interface {
	Insert(ctx context.Context, row domain.County) error
	Update(ctx context.Context, row domain.County) error
	Get(ctx context.Context, id domain.ID) (domain.County, error)
	List(ctx context.Context, filter Filter) (Page[domain.County], error)
}

// ReferralSources stores referral sources.
type ReferralSources interface {
	Insert(ctx context.Context, row domain.ReferralSource) error
	Update(ctx context.Context, row domain.ReferralSource) error
	Get(ctx context.Context, id domain.ID) (domain.ReferralSource, error)
	List(ctx context.Context, filter Filter) (Page[domain.ReferralSource], error)
}

// Users stores sign-in users.
type Users interface {
	Insert(ctx context.Context, row domain.User) error
	Update(ctx context.Context, row domain.User) error
	Get(ctx context.Context, id domain.ID) (domain.User, error)
	List(ctx context.Context, filter Filter) (Page[domain.User], error)
}

// Sessions stores browser sessions.
type Sessions interface {
	Insert(ctx context.Context, row domain.Session) error
	Update(ctx context.Context, row domain.Session) error
	Get(ctx context.Context, id domain.ID) (domain.Session, error)
	List(ctx context.Context, filter Filter) (Page[domain.Session], error)
}

// PasswordCredentials stores Argon2id PHC strings.
type PasswordCredentials interface {
	Insert(ctx context.Context, row domain.PasswordCredential) error
	Update(ctx context.Context, row domain.PasswordCredential) error
	GetByUser(ctx context.Context, userID domain.ID) (domain.PasswordCredential, error)
}

// TOTPSecrets stores encrypted authenticator secrets.
type TOTPSecrets interface {
	Insert(ctx context.Context, row domain.TOTPSecret) error
	Update(ctx context.Context, row domain.TOTPSecret) error
	GetByUser(ctx context.Context, userID domain.ID) (domain.TOTPSecret, error)
}

// RecoveryCodes stores single-use recovery code hashes.
type RecoveryCodes interface {
	Insert(ctx context.Context, row domain.RecoveryCode) error
	Update(ctx context.Context, row domain.RecoveryCode) error
	List(ctx context.Context, filter Filter) (Page[domain.RecoveryCode], error)
}

// APITokens stores revocable bearer tokens.
type APITokens interface {
	Insert(ctx context.Context, row domain.APIToken) error
	Update(ctx context.Context, row domain.APIToken) error
	Get(ctx context.Context, id domain.ID) (domain.APIToken, error)
	List(ctx context.Context, filter Filter) (Page[domain.APIToken], error)
}

// SettingRows reads the settings table. Writes go through settings.Registry so validation and audit stay in one place.
type SettingRows interface {
	Get(ctx context.Context, id domain.ID) (domain.SettingRow, error)
	List(ctx context.Context, filter Filter) (Page[domain.SettingRow], error)
}

// FeeAgreements stores fee agreements. One non-void agreement per matter.
type FeeAgreements interface {
	Insert(ctx context.Context, row domain.FeeAgreement) error
	Update(ctx context.Context, row domain.FeeAgreement) error
	Get(ctx context.Context, id domain.ID) (domain.FeeAgreement, error)
	List(ctx context.Context, filter Filter) (Page[domain.FeeAgreement], error)
}

// TimeEntries stores time. InvoiceID stays empty until the entry is billed.
type TimeEntries interface {
	Insert(ctx context.Context, row domain.TimeEntry) error
	Update(ctx context.Context, row domain.TimeEntry) error
	Get(ctx context.Context, id domain.ID) (domain.TimeEntry, error)
	List(ctx context.Context, filter Filter) (Page[domain.TimeEntry], error)
}

// Timers stores timers. One running timer per user per matter.
type Timers interface {
	Insert(ctx context.Context, row domain.Timer) error
	Update(ctx context.Context, row domain.Timer) error
	Get(ctx context.Context, id domain.ID) (domain.Timer, error)
	List(ctx context.Context, filter Filter) (Page[domain.Timer], error)
}

// Expenses stores expenses.
type Expenses interface {
	Insert(ctx context.Context, row domain.Expense) error
	Update(ctx context.Context, row domain.Expense) error
	Get(ctx context.Context, id domain.ID) (domain.Expense, error)
	List(ctx context.Context, filter Filter) (Page[domain.Expense], error)
}

// Invoices stores invoices.
type Invoices interface {
	Insert(ctx context.Context, row domain.Invoice) error
	Update(ctx context.Context, row domain.Invoice) error
	Get(ctx context.Context, id domain.ID) (domain.Invoice, error)
	List(ctx context.Context, filter Filter) (Page[domain.Invoice], error)
}

// InvoiceLines stores invoice lines.
type InvoiceLines interface {
	Insert(ctx context.Context, row domain.InvoiceLine) error
	Update(ctx context.Context, row domain.InvoiceLine) error
	Get(ctx context.Context, id domain.ID) (domain.InvoiceLine, error)
	List(ctx context.Context, filter Filter) (Page[domain.InvoiceLine], error)
}

// Payments stores payments.
type Payments interface {
	Insert(ctx context.Context, row domain.Payment) error
	Update(ctx context.Context, row domain.Payment) error
	Get(ctx context.Context, id domain.ID) (domain.Payment, error)
	List(ctx context.Context, filter Filter) (Page[domain.Payment], error)
}

// TrustAccounts stores trust bank accounts.
type TrustAccounts interface {
	Insert(ctx context.Context, row domain.TrustAccount) error
	Update(ctx context.Context, row domain.TrustAccount) error
	Get(ctx context.Context, id domain.ID) (domain.TrustAccount, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustAccount], error)
}

// TrustLedger is append-only. A reversal is a new row with ReversesID set.
type TrustLedger interface {
	Append(ctx context.Context, row domain.TrustEntry) error
	Get(ctx context.Context, id domain.ID) (domain.TrustEntry, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustEntry], error)
}

// TrustBalances stores cached sums. The ledger is the source of truth.
type TrustBalances interface {
	Insert(ctx context.Context, row domain.TrustBalance) error
	Update(ctx context.Context, row domain.TrustBalance) error
	Get(ctx context.Context, id domain.ID) (domain.TrustBalance, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustBalance], error)
}

// TrustFlags stores below-zero flags. Resolving a flag updates it; the ledger row does not change.
type TrustFlags interface {
	Insert(ctx context.Context, row domain.TrustFlag) error
	Update(ctx context.Context, row domain.TrustFlag) error
	Get(ctx context.Context, id domain.ID) (domain.TrustFlag, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustFlag], error)
}

// TrustReplenishments stores evergreen requests.
type TrustReplenishments interface {
	Insert(ctx context.Context, row domain.TrustReplenishment) error
	Update(ctx context.Context, row domain.TrustReplenishment) error
	Get(ctx context.Context, id domain.ID) (domain.TrustReplenishment, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustReplenishment], error)
}

// TrustReconciliations stores monthly reconciliations.
type TrustReconciliations interface {
	Insert(ctx context.Context, row domain.TrustReconciliation) error
	Update(ctx context.Context, row domain.TrustReconciliation) error
	Get(ctx context.Context, id domain.ID) (domain.TrustReconciliation, error)
	List(ctx context.Context, filter Filter) (Page[domain.TrustReconciliation], error)
}

// Notes stores notes.
type Notes interface {
	Insert(ctx context.Context, row domain.Note) error
	Update(ctx context.Context, row domain.Note) error
	Get(ctx context.Context, id domain.ID) (domain.Note, error)
	List(ctx context.Context, filter Filter) (Page[domain.Note], error)
}

// NoteTags stores free note tags.
type NoteTags interface {
	Insert(ctx context.Context, row domain.NoteTag) error
	Delete(ctx context.Context, id domain.ID) error
	List(ctx context.Context, filter Filter) (Page[domain.NoteTag], error)
}

// ConflictRecords stores conflict names.
type ConflictRecords interface {
	Insert(ctx context.Context, row domain.ConflictRecord) error
	Update(ctx context.Context, row domain.ConflictRecord) error
	Get(ctx context.Context, id domain.ID) (domain.ConflictRecord, error)
	List(ctx context.Context, filter Filter) (Page[domain.ConflictRecord], error)
}

// ConflictReviews stores attorney decisions.
type ConflictReviews interface {
	Insert(ctx context.Context, row domain.ConflictReview) error
	Update(ctx context.Context, row domain.ConflictReview) error
	Get(ctx context.Context, id domain.ID) (domain.ConflictReview, error)
	List(ctx context.Context, filter Filter) (Page[domain.ConflictReview], error)
}

// CalendarEvents stores calendar items.
type CalendarEvents interface {
	Insert(ctx context.Context, row domain.CalendarEvent) error
	Update(ctx context.Context, row domain.CalendarEvent) error
	Get(ctx context.Context, id domain.ID) (domain.CalendarEvent, error)
	List(ctx context.Context, filter Filter) (Page[domain.CalendarEvent], error)
}

// CalendarAttendees stores event attendees.
type CalendarAttendees interface {
	Insert(ctx context.Context, row domain.CalendarAttendee) error
	Delete(ctx context.Context, id domain.ID) error
	List(ctx context.Context, filter Filter) (Page[domain.CalendarAttendee], error)
}

// Deadlines stores deadlines.
type Deadlines interface {
	Insert(ctx context.Context, row domain.Deadline) error
	Update(ctx context.Context, row domain.Deadline) error
	Get(ctx context.Context, id domain.ID) (domain.Deadline, error)
	List(ctx context.Context, filter Filter) (Page[domain.Deadline], error)
}

// AuditLog is append-only.
type AuditLog interface {
	Append(ctx context.Context, row domain.AuditEntry) error
	Get(ctx context.Context, id domain.ID) (domain.AuditEntry, error)
	List(ctx context.Context, filter Filter) (Page[domain.AuditEntry], error)
}

// Events is the append-only timeline and activity stream.
type Events interface {
	Append(ctx context.Context, row domain.Event) error
	Get(ctx context.Context, id domain.ID) (domain.Event, error)
	List(ctx context.Context, filter Filter) (Page[domain.Event], error)
}

// Files stores file records. Bytes are not stored here.
type Files interface {
	Insert(ctx context.Context, row domain.File) error
	Update(ctx context.Context, row domain.File) error
	Get(ctx context.Context, id domain.ID) (domain.File, error)
	List(ctx context.Context, filter Filter) (Page[domain.File], error)
}

// FileVersions is append-only. A new save inserts a version; no version is rewritten.
type FileVersions interface {
	Append(ctx context.Context, row domain.FileVersion) error
	Get(ctx context.Context, id domain.ID) (domain.FileVersion, error)
	List(ctx context.Context, filter Filter) (Page[domain.FileVersion], error)
}

// FileRenditions is append-only. A new Markdown copy is a new rendition.
type FileRenditions interface {
	Append(ctx context.Context, row domain.FileRendition) error
	Get(ctx context.Context, id domain.ID) (domain.FileRendition, error)
	List(ctx context.Context, filter Filter) (Page[domain.FileRendition], error)
}

// FileLinks stores typed links between files.
type FileLinks interface {
	Insert(ctx context.Context, row domain.FileLink) error
	Get(ctx context.Context, id domain.ID) (domain.FileLink, error)
	List(ctx context.Context, filter Filter) (Page[domain.FileLink], error)
}

// StorageBackends stores storage adapters.
type StorageBackends interface {
	Insert(ctx context.Context, row domain.StorageBackend) error
	Update(ctx context.Context, row domain.StorageBackend) error
	Get(ctx context.Context, id domain.ID) (domain.StorageBackend, error)
	List(ctx context.Context, filter Filter) (Page[domain.StorageBackend], error)
}

// StorageMoves stores backend moves.
type StorageMoves interface {
	Insert(ctx context.Context, row domain.StorageMove) error
	Update(ctx context.Context, row domain.StorageMove) error
	Get(ctx context.Context, id domain.ID) (domain.StorageMove, error)
	List(ctx context.Context, filter Filter) (Page[domain.StorageMove], error)
}

// EditLocks stores editor locks.
type EditLocks interface {
	Insert(ctx context.Context, row domain.EditLock) error
	Update(ctx context.Context, row domain.EditLock) error
	Delete(ctx context.Context, id domain.ID) error
	GetByFile(ctx context.Context, fileID domain.ID) (domain.EditLock, error)
}

// CourtProfiles stores LawCraft court profiles as whole JSON documents.
type CourtProfiles interface {
	Insert(ctx context.Context, row domain.CourtProfile) error
	Update(ctx context.Context, row domain.CourtProfile) error
	Get(ctx context.Context, id domain.ID) (domain.CourtProfile, error)
	List(ctx context.Context, filter Filter) (Page[domain.CourtProfile], error)
}

// DocCategories stores filing categories.
type DocCategories interface {
	Insert(ctx context.Context, row domain.DocCategory) error
	Update(ctx context.Context, row domain.DocCategory) error
	Get(ctx context.Context, id domain.ID) (domain.DocCategory, error)
	List(ctx context.Context, filter Filter) (Page[domain.DocCategory], error)
}

// DocTenderJobs stores processing jobs.
type DocTenderJobs interface {
	Insert(ctx context.Context, row domain.DocTenderJob) error
	Update(ctx context.Context, row domain.DocTenderJob) error
	Get(ctx context.Context, id domain.ID) (domain.DocTenderJob, error)
	List(ctx context.Context, filter Filter) (Page[domain.DocTenderJob], error)
}

// DocTenderUnits stores pages, photos, sheets, and media windows.
type DocTenderUnits interface {
	Insert(ctx context.Context, row domain.DocTenderUnit) error
	Update(ctx context.Context, row domain.DocTenderUnit) error
	Get(ctx context.Context, id domain.ID) (domain.DocTenderUnit, error)
	List(ctx context.Context, filter Filter) (Page[domain.DocTenderUnit], error)
}

// DocTenderResults stores published names and cited fields.
type DocTenderResults interface {
	Insert(ctx context.Context, row domain.DocTenderResult) error
	Get(ctx context.Context, id domain.ID) (domain.DocTenderResult, error)
	List(ctx context.Context, filter Filter) (Page[domain.DocTenderResult], error)
}

// DocTenderExceptions stores the review queue.
type DocTenderExceptions interface {
	Insert(ctx context.Context, row domain.DocTenderException) error
	Update(ctx context.Context, row domain.DocTenderException) error
	Get(ctx context.Context, id domain.ID) (domain.DocTenderException, error)
	List(ctx context.Context, filter Filter) (Page[domain.DocTenderException], error)
}

// AICalls is the append-only model-call log.
type AICalls interface {
	Append(ctx context.Context, row domain.AICall) error
	Get(ctx context.Context, id domain.ID) (domain.AICall, error)
	List(ctx context.Context, filter Filter) (Page[domain.AICall], error)
}

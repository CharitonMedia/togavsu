package practice

import (
	"context"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Store is the practice persistence seam. Memory satisfies it.
// A Postgres adapter can implement the same methods over the tables in migration 0001.
type Store interface {
	PutUser(_ context.Context, row domain.User) error
	User(_ context.Context, id domain.ID) (domain.User, error)
	InsertContact(ctx context.Context, row domain.Contact) error
	UpdateContact(ctx context.Context, row domain.Contact) error
	Contact(_ context.Context, id domain.ID) (domain.Contact, error)
	Contacts() []domain.Contact
	InsertAddress(ctx context.Context, row domain.ContactAddress) error
	UpdateAddress(ctx context.Context, row domain.ContactAddress) error
	Addresses(contact domain.ID) []domain.ContactAddress
	InsertEmail(ctx context.Context, row domain.ContactEmail) error
	UpdateEmail(ctx context.Context, row domain.ContactEmail) error
	Emails(contact domain.ID) []domain.ContactEmail
	InsertPhone(ctx context.Context, row domain.ContactPhone) error
	UpdatePhone(ctx context.Context, row domain.ContactPhone) error
	Phone(_ context.Context, id domain.ID) (domain.ContactPhone, error)
	Phones(contact domain.ID) []domain.ContactPhone
	InsertTag(ctx context.Context, row domain.ContactTag) error
	UpdateTag(ctx context.Context, row domain.ContactTag) error
	DeleteTag(ctx context.Context, id domain.ID) error
	Tags(contact domain.ID) []domain.ContactTag
	InsertMatter(ctx context.Context, row domain.Matter, assign func([]string) (string, error)) (domain.Matter, error)
	UpdateMatter(ctx context.Context, row domain.Matter) error
	Matter(_ context.Context, id domain.ID) (domain.Matter, error)
	Matters() []domain.Matter
	InsertParty(ctx context.Context, row domain.MatterParty) error
	UpdateParty(ctx context.Context, row domain.MatterParty) error
	Party(_ context.Context, id domain.ID) (domain.MatterParty, error)
	Parties(matter, contact domain.ID) []domain.MatterParty
	ReplaceWalls(ctx context.Context, matter domain.ID, rows []domain.MatterWall) error
	Walls(matter domain.ID) []domain.MatterWall
	InsertArea(ctx context.Context, row domain.PracticeArea) error
	UpdateArea(ctx context.Context, row domain.PracticeArea) error
	Area(_ context.Context, id domain.ID) (domain.PracticeArea, error)
	Areas() []domain.PracticeArea
	InsertCounty(ctx context.Context, row domain.County) error
	UpdateCounty(ctx context.Context, row domain.County) error
	Counties() []domain.County
	County(_ context.Context, id domain.ID) (domain.County, error)
	InsertReferral(ctx context.Context, row domain.ReferralSource) error
	UpdateReferral(ctx context.Context, row domain.ReferralSource) error
	Referrals() []domain.ReferralSource
	Referral(_ context.Context, id domain.ID) (domain.ReferralSource, error)
	InsertNote(ctx context.Context, row domain.Note) error
	UpdateNote(ctx context.Context, row domain.Note) error
	Note(_ context.Context, id domain.ID) (domain.Note, error)
	Notes() []domain.Note
	InsertNoteTag(ctx context.Context, row domain.NoteTag) error
	DeleteNoteTag(ctx context.Context, id domain.ID) error
	NoteTags(note domain.ID) []domain.NoteTag
	ReplaceNoteTags(ctx context.Context, note domain.ID, rows []domain.NoteTag) error
	InsertConflict(ctx context.Context, row domain.ConflictRecord) error
	UpdateConflict(ctx context.Context, row domain.ConflictRecord) error
	Conflict(_ context.Context, id domain.ID) (domain.ConflictRecord, error)
	ConflictRecords() []domain.ConflictRecord
	InsertReview(ctx context.Context, row domain.ConflictReview) error
	PutInvoice(ctx context.Context, row domain.Invoice) error
	Invoices() []domain.Invoice
	PutTrust(ctx context.Context, row domain.TrustBalance) error
	TrustBalances() []domain.TrustBalance
	PutTime(ctx context.Context, row domain.TimeEntry) error
	TimeEntries() []domain.TimeEntry
	AppendAudit(ctx context.Context, rows []storedAudit, events []domain.Event) error
	Audit(_ context.Context, id domain.ID) (storedAudit, error)
	Audits() []storedAudit
	UpdateAudit(context.Context, domain.AuditEntry) error
	DeleteAudit(context.Context, domain.ID) error
	Events() []domain.Event
	Event(_ context.Context, id domain.ID) (domain.Event, error)
	UpdateEvent(context.Context, domain.Event) error
	DeleteEvent(context.Context, domain.ID) error
	PutField(ctx context.Context, row FieldDef) error
	Field(_ context.Context, id domain.ID) (FieldDef, error)
	Fields() []FieldDef
	PutValue(ctx context.Context, row FieldValue) error
	Values(record domain.ID) []FieldValue
	EnsureMarkers(ctx context.Context, labels []string, at time.Time, actor domain.ID, ids func() (domain.ID, error)) error
	Marker(id domain.ID) (MarkerDef, bool)
	MarkerByLabel(label string) (MarkerDef, bool)
	PutMatterMarker(ctx context.Context, row MatterMarker) error
	UpdateMatterMarker(ctx context.Context, row MatterMarker) error
	PutMarker(ctx context.Context, row MarkerDef) error
	Markers() []MarkerDef
	PutPartyRole(ctx context.Context, row PartyRoleDef) error
	PartyRoles() []PartyRoleDef
	MatterMarkers(matter domain.ID) []MatterMarker
	AppendClose(ctx context.Context, row CloseRecord) error
	LatestClose(matter domain.ID) (CloseRecord, bool)
	AppendRun(ctx context.Context, row ConflictRun) error
	Runs(matter domain.ID) []ConflictRun
	FindHit(id domain.ID) (ConflictRun, Hit, bool)
	SaveHit(ctx context.Context, hit Hit) error
	MarkChecked(ctx context.Context, party domain.ID, at time.Time) error
	Checked(party domain.ID) (time.Time, bool)
	TakeMatterMarker(ctx context.Context, id domain.ID) (MatterMarker, error)
	DeleteMatterMarker(ctx context.Context, matter domain.ID, label string, area domain.ID) (MatterMarker, error)
	IdemGet(key string) (idemRecord, bool)
	IdemClaim(ctx context.Context, key string, rec idemRecord) (idemRecord, bool, error)
	IdemComplete(ctx context.Context, key string, rec idemRecord) error
	IdemAbort(key string, rec idemRecord)
	IdemPut(ctx context.Context, key string, rec idemRecord) error
}

var _ Store = (*Memory)(nil)

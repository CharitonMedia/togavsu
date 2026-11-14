package practice

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// ErrAppendOnly is returned when a caller tries to change an audit or timeline row.
var ErrAppendOnly = errors.New("practice: append only")

type storedAudit struct {
	Entry        domain.AuditEntry
	MatterID     domain.ID
	ContactID    domain.ID
	AttorneyOnly bool
}

// Memory is the process-local practice store.
// ponytail: ceiling: one mutex and a linear scan. Upgrade path: a Postgres Store over the contacts, matters, matter_parties, notes, and audit_log tables already created by migration 0001. trigger: identity passes a Postgres pool into practice.New.
type Memory struct {
	mu sync.Mutex

	contacts  map[domain.ID]domain.Contact
	addresses map[domain.ID]domain.ContactAddress
	emails    map[domain.ID]domain.ContactEmail
	phones    map[domain.ID]domain.ContactPhone
	tags      map[domain.ID]domain.ContactTag
	matters   map[domain.ID]domain.Matter
	parties   map[domain.ID]domain.MatterParty
	walls     map[domain.ID]domain.MatterWall
	areas     map[domain.ID]domain.PracticeArea
	counties  map[domain.ID]domain.County
	referrals map[domain.ID]domain.ReferralSource
	notes     map[domain.ID]domain.Note
	noteTags  map[domain.ID]domain.NoteTag
	conflicts map[domain.ID]domain.ConflictRecord
	reviews   map[domain.ID]domain.ConflictReview
	invoices  map[domain.ID]domain.Invoice
	trust     map[domain.ID]domain.TrustBalance
	times     map[domain.ID]domain.TimeEntry
	users     map[domain.ID]domain.User

	audits []storedAudit
	events []domain.Event

	fields  map[domain.ID]FieldDef
	values  map[string]FieldValue
	markers map[domain.ID]MarkerDef
	pinned  map[domain.ID]MatterMarker
	roles   map[domain.ID]PartyRoleDef
	closes  []CloseRecord
	runs    []ConflictRun
	checked map[domain.ID]time.Time
	idem    map[string]idemRecord

	// idx is the child-row index (memindex.go); nil until first use and after a write.
	idx *childIndex
	// gidx is the per-matter glance index (memindex.go), dropped on invoice, trust and
	// time writes.
	gidx *glanceIndex
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		contacts:  map[domain.ID]domain.Contact{},
		addresses: map[domain.ID]domain.ContactAddress{},
		emails:    map[domain.ID]domain.ContactEmail{},
		phones:    map[domain.ID]domain.ContactPhone{},
		tags:      map[domain.ID]domain.ContactTag{},
		matters:   map[domain.ID]domain.Matter{},
		parties:   map[domain.ID]domain.MatterParty{},
		walls:     map[domain.ID]domain.MatterWall{},
		areas:     map[domain.ID]domain.PracticeArea{},
		counties:  map[domain.ID]domain.County{},
		referrals: map[domain.ID]domain.ReferralSource{},
		notes:     map[domain.ID]domain.Note{},
		noteTags:  map[domain.ID]domain.NoteTag{},
		conflicts: map[domain.ID]domain.ConflictRecord{},
		reviews:   map[domain.ID]domain.ConflictReview{},
		invoices:  map[domain.ID]domain.Invoice{},
		trust:     map[domain.ID]domain.TrustBalance{},
		times:     map[domain.ID]domain.TimeEntry{},
		users:     map[domain.ID]domain.User{},
		fields:    map[domain.ID]FieldDef{},
		values:    map[string]FieldValue{},
		markers:   map[domain.ID]MarkerDef{},
		pinned:    map[domain.ID]MatterMarker{},
		roles:     map[domain.ID]PartyRoleDef{},
		checked:   map[domain.ID]time.Time{},
		idem:      map[string]idemRecord{},
	}
}

func ready(ctx context.Context) error {
	return ctx.Err()
}

func (m *Memory) PutUser(_ context.Context, row domain.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[row.ID] = row
	return nil
}

func (m *Memory) User(_ context.Context, id domain.ID) (domain.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.users[id]
	if !ok {
		return domain.User{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) InsertContact(ctx context.Context, row domain.Contact) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.contacts[row.ID]; ok {
		return repository.ErrConflict
	}
	m.contacts[row.ID] = row
	return nil
}

func (m *Memory) UpdateContact(ctx context.Context, row domain.Contact) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.contacts[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.contacts[row.ID] = row
	return nil
}

func (m *Memory) Contact(_ context.Context, id domain.ID) (domain.Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.contacts[id]
	if !ok {
		return domain.Contact{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) Contacts() []domain.Contact {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Contact, 0, len(m.contacts))
	for _, row := range m.contacts {
		out = append(out, row)
	}
	return out
}

func (m *Memory) InsertAddress(ctx context.Context, row domain.ContactAddress) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addresses[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) UpdateAddress(ctx context.Context, row domain.ContactAddress) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.addresses[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.addresses[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) Addresses(contact domain.ID) []domain.ContactAddress {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !contact.IsZero() {
		return pick(m.addresses, m.children().addresses[contact])
	}
	var out []domain.ContactAddress
	for _, row := range m.addresses {
		if contact.IsZero() || row.ContactID == contact {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) InsertEmail(ctx context.Context, row domain.ContactEmail) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emails[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) UpdateEmail(ctx context.Context, row domain.ContactEmail) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.emails[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.emails[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) Emails(contact domain.ID) []domain.ContactEmail {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !contact.IsZero() {
		return pick(m.emails, m.children().emails[contact])
	}
	var out []domain.ContactEmail
	for _, row := range m.emails {
		if contact.IsZero() || row.ContactID == contact {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) InsertPhone(ctx context.Context, row domain.ContactPhone) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phones[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) UpdatePhone(ctx context.Context, row domain.ContactPhone) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.phones[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.phones[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) Phone(_ context.Context, id domain.ID) (domain.ContactPhone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.phones[id]
	if !ok {
		return domain.ContactPhone{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) Phones(contact domain.ID) []domain.ContactPhone {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !contact.IsZero() {
		return pick(m.phones, m.children().phones[contact])
	}
	var out []domain.ContactPhone
	for _, row := range m.phones {
		if contact.IsZero() || row.ContactID == contact {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) InsertTag(ctx context.Context, row domain.ContactTag) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tags[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) UpdateTag(ctx context.Context, row domain.ContactTag) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tags[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.tags[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) DeleteTag(ctx context.Context, id domain.ID) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tags[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.tags, id)
	m.idx = nil
	return nil
}

func (m *Memory) Tags(contact domain.ID) []domain.ContactTag {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !contact.IsZero() {
		return pick(m.tags, m.children().tags[contact])
	}
	var out []domain.ContactTag
	for _, row := range m.tags {
		if contact.IsZero() || row.ContactID == contact {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) InsertMatter(ctx context.Context, row domain.Matter, assign func([]string) (string, error)) (domain.Matter, error) {
	if err := ready(ctx); err != nil {
		return domain.Matter{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.matters[row.ID]; ok {
		return domain.Matter{}, repository.ErrConflict
	}
	if row.Number == "" {
		nums := make([]string, 0, len(m.matters))
		for _, existing := range m.matters {
			nums = append(nums, existing.Number)
		}
		number, err := assign(nums)
		if err != nil {
			return domain.Matter{}, err
		}
		row.Number = number
	}
	for _, existing := range m.matters {
		if existing.Number == row.Number {
			return domain.Matter{}, repository.ErrConflict
		}
	}
	m.matters[row.ID] = row
	return row, nil
}

func (m *Memory) UpdateMatter(ctx context.Context, row domain.Matter) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.matters[row.ID]; !ok {
		return repository.ErrNotFound
	}
	for _, existing := range m.matters {
		if existing.ID != row.ID && existing.Number == row.Number {
			return repository.ErrConflict
		}
	}
	m.matters[row.ID] = row
	return nil
}

func (m *Memory) Matter(_ context.Context, id domain.ID) (domain.Matter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.matters[id]
	if !ok {
		return domain.Matter{}, repository.ErrNotFound
	}
	return row, nil
}

// SearchText is the searched text of one matter (Name, Number, CaseNumber) or contact
// (FullName, GivenName, FamilyName, PreferredName, AlsoKnownAs).
type SearchText struct {
	ID     domain.ID
	Fields [5]string
}

// MatterSearchText lists every matter's searched fields: a search over 20,000 matters
// copies these and reads whole matters only for its hits.
func (m *Memory) MatterSearchText() []SearchText {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SearchText, 0, len(m.matters))
	for id, row := range m.matters {
		out = append(out, SearchText{ID: id, Fields: [5]string{row.Name, row.Number, row.CaseNumber}})
	}
	return out
}

// ContactsMatching returns the contacts whose search text matches, building each text
// in one locked pass: the name fields, then the contact's emails and phones in the child
// index's order, lowercased and joined by spaces. match must not call the store.
func (m *Memory) ContactsMatching(match func(hay []byte) bool) []domain.ID {
	m.mu.Lock()
	defer m.mu.Unlock()
	x := m.children()
	var hay []byte
	var out []domain.ID
	for id, row := range m.contacts {
		hay = appendLower(hay[:0], row.FullName)
		for _, f := range []string{row.GivenName, row.FamilyName, row.PreferredName, row.AlsoKnownAs} {
			hay = appendLower(append(hay, ' '), f)
		}
		for _, e := range x.emails[id] {
			if r, ok := m.emails[e]; ok {
				hay = appendLower(append(hay, ' '), r.Email)
			}
		}
		for _, ph := range x.phones[id] {
			if r, ok := m.phones[ph]; ok {
				hay = appendLower(append(hay, ' '), r.PhoneE164)
			}
		}
		if match(hay) {
			out = append(out, id)
		}
	}
	return out
}

// MatterKey is the part of a matter a count filters on.
type MatterKey struct {
	ID, PracticeAreaID                     domain.ID
	ResponsibleAttorneyID, AssignedStaffID domain.ID
	Status, OpenedOn, Number               string
}

func matterKeyOf(id domain.ID, row *domain.Matter) MatterKey {
	return MatterKey{ID: id, PracticeAreaID: row.PracticeAreaID, ResponsibleAttorneyID: row.ResponsibleAttorneyID,
		AssignedStaffID: row.AssignedStaffID, Status: row.Status, OpenedOn: row.OpenedOn, Number: row.Number}
}

// MatterKeys lists every matter's key: a count over 20,000 matters copies these, not
// whole matters.
func (m *Memory) MatterKeys() []MatterKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MatterKey, 0, len(m.matters))
	for id, row := range m.matters {
		out = append(out, matterKeyOf(id, &row))
	}
	return out
}

func (m *Memory) Matters() []domain.Matter {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Matter, 0, len(m.matters))
	for _, row := range m.matters {
		out = append(out, row)
	}
	return out
}

func (m *Memory) InsertParty(ctx context.Context, row domain.MatterParty) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parties[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) UpdateParty(ctx context.Context, row domain.MatterParty) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.parties[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.parties[row.ID] = row
	m.idx = nil
	return nil
}

func (m *Memory) Party(_ context.Context, id domain.ID) (domain.MatterParty, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.parties[id]
	if !ok {
		return domain.MatterParty{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) Parties(matter, contact domain.ID) []domain.MatterParty {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.parties
	switch {
	case !matter.IsZero():
		src = indexed(m.parties, m.children().partiesByMatter[matter])
	case !contact.IsZero():
		src = indexed(m.parties, m.children().partiesByCon[contact])
	}
	var out []domain.MatterParty
	for _, row := range src {
		if !matter.IsZero() && row.MatterID != matter {
			continue
		}
		if !contact.IsZero() && row.ContactID != contact {
			continue
		}
		out = append(out, row)
	}
	return out
}

func (m *Memory) ReplaceWalls(ctx context.Context, matter domain.ID, rows []domain.MatterWall) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, row := range m.walls {
		if row.MatterID == matter {
			delete(m.walls, id)
			m.idx = nil
		}
	}
	for _, row := range rows {
		m.walls[row.ID] = row
		m.idx = nil
	}
	return nil
}

func (m *Memory) Walls(matter domain.ID) []domain.MatterWall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return pick(m.walls, m.children().walls[matter])
}

func (m *Memory) InsertArea(ctx context.Context, row domain.PracticeArea) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.areas[row.ID] = row
	return nil
}

func (m *Memory) UpdateArea(ctx context.Context, row domain.PracticeArea) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.areas[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.areas[row.ID] = row
	return nil
}

func (m *Memory) Area(_ context.Context, id domain.ID) (domain.PracticeArea, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.areas[id]
	if !ok {
		return domain.PracticeArea{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) Areas() []domain.PracticeArea {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.PracticeArea, 0, len(m.areas))
	for _, row := range m.areas {
		out = append(out, row)
	}
	return out
}

func (m *Memory) InsertCounty(ctx context.Context, row domain.County) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counties[row.ID] = row
	return nil
}

func (m *Memory) UpdateCounty(ctx context.Context, row domain.County) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.counties[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.counties[row.ID] = row
	return nil
}

func (m *Memory) Counties() []domain.County {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.County, 0, len(m.counties))
	for _, row := range m.counties {
		out = append(out, row)
	}
	return out
}

func (m *Memory) County(_ context.Context, id domain.ID) (domain.County, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.counties[id]
	if !ok {
		return domain.County{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) InsertReferral(ctx context.Context, row domain.ReferralSource) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.referrals[row.ID] = row
	return nil
}

func (m *Memory) UpdateReferral(ctx context.Context, row domain.ReferralSource) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.referrals[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.referrals[row.ID] = row
	return nil
}

func (m *Memory) Referrals() []domain.ReferralSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.ReferralSource, 0, len(m.referrals))
	for _, row := range m.referrals {
		out = append(out, row)
	}
	return out
}

func (m *Memory) Referral(_ context.Context, id domain.ID) (domain.ReferralSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.referrals[id]
	if !ok {
		return domain.ReferralSource{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) InsertNote(ctx context.Context, row domain.Note) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notes[row.ID] = row
	return nil
}

func (m *Memory) UpdateNote(ctx context.Context, row domain.Note) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.notes[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.notes[row.ID] = row
	return nil
}

func (m *Memory) Note(_ context.Context, id domain.ID) (domain.Note, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.notes[id]
	if !ok {
		return domain.Note{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) Notes() []domain.Note {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Note, 0, len(m.notes))
	for _, row := range m.notes {
		out = append(out, row)
	}
	return out
}

func (m *Memory) InsertNoteTag(ctx context.Context, row domain.NoteTag) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.noteTags[row.ID] = row
	return nil
}

func (m *Memory) DeleteNoteTag(ctx context.Context, id domain.ID) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.noteTags[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.noteTags, id)
	return nil
}

func (m *Memory) NoteTags(note domain.ID) []domain.NoteTag {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.NoteTag
	for _, row := range m.noteTags {
		if note.IsZero() || row.NoteID == note {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) ReplaceNoteTags(ctx context.Context, note domain.ID, rows []domain.NoteTag) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, row := range m.noteTags {
		if row.NoteID == note {
			delete(m.noteTags, id)
		}
	}
	for _, row := range rows {
		m.noteTags[row.ID] = row
	}
	return nil
}

func (m *Memory) InsertConflict(ctx context.Context, row domain.ConflictRecord) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conflicts[row.ID] = row
	return nil
}

func (m *Memory) UpdateConflict(ctx context.Context, row domain.ConflictRecord) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conflicts[row.ID]; !ok {
		return repository.ErrNotFound
	}
	m.conflicts[row.ID] = row
	return nil
}

func (m *Memory) Conflict(_ context.Context, id domain.ID) (domain.ConflictRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.conflicts[id]
	if !ok {
		return domain.ConflictRecord{}, repository.ErrNotFound
	}
	return row, nil
}

func (m *Memory) ConflictRecords() []domain.ConflictRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.ConflictRecord, 0, len(m.conflicts))
	for _, row := range m.conflicts {
		out = append(out, row)
	}
	return out
}

func (m *Memory) InsertReview(ctx context.Context, row domain.ConflictReview) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reviews[row.ID] = row
	return nil
}

func (m *Memory) PutInvoice(ctx context.Context, row domain.Invoice) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invoices[row.ID] = row
	m.gidx = nil
	return nil
}

func (m *Memory) Invoices() []domain.Invoice {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Invoice, 0, len(m.invoices))
	for _, row := range m.invoices {
		out = append(out, row)
	}
	return out
}

func (m *Memory) PutTrust(ctx context.Context, row domain.TrustBalance) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trust[row.ID] = row
	m.gidx = nil
	return nil
}

func (m *Memory) TrustBalances() []domain.TrustBalance {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.TrustBalance, 0, len(m.trust))
	for _, row := range m.trust {
		out = append(out, row)
	}
	return out
}

func (m *Memory) PutTime(ctx context.Context, row domain.TimeEntry) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.times[row.ID] = row
	m.gidx = nil
	return nil
}

func (m *Memory) TimeEntries() []domain.TimeEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.TimeEntry, 0, len(m.times))
	for _, row := range m.times {
		out = append(out, row)
	}
	return out
}

func (m *Memory) AppendAudit(ctx context.Context, rows []storedAudit, events []domain.Event) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, rows...)
	m.events = append(m.events, events...)
	return nil
}

func (m *Memory) Audit(_ context.Context, id domain.ID) (storedAudit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.audits {
		if row.Entry.ID == id {
			return row, nil
		}
	}
	return storedAudit{}, repository.ErrNotFound
}

func (m *Memory) Audits() []storedAudit {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storedAudit, len(m.audits))
	copy(out, m.audits)
	return out
}

// UpdateAudit refuses the change. The stored row stays byte for byte.
func (m *Memory) UpdateAudit(context.Context, domain.AuditEntry) error {
	return ErrAppendOnly
}

// DeleteAudit refuses the change. The stored row stays byte for byte.
func (m *Memory) DeleteAudit(context.Context, domain.ID) error {
	return ErrAppendOnly
}

func (m *Memory) Events() []domain.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.Event, len(m.events))
	copy(out, m.events)
	return out
}

func (m *Memory) Event(_ context.Context, id domain.ID) (domain.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.events {
		if row.ID == id {
			return row, nil
		}
	}
	return domain.Event{}, repository.ErrNotFound
}

// UpdateEvent refuses the change.
func (m *Memory) UpdateEvent(context.Context, domain.Event) error {
	return ErrAppendOnly
}

// DeleteEvent refuses the change.
func (m *Memory) DeleteEvent(context.Context, domain.ID) error {
	return ErrAppendOnly
}

func (m *Memory) PutField(ctx context.Context, row FieldDef) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields[row.ID] = copyField(row)
	return nil
}

func (m *Memory) Field(_ context.Context, id domain.ID) (FieldDef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.fields[id]
	if !ok {
		return FieldDef{}, repository.ErrNotFound
	}
	return copyField(row), nil
}

func (m *Memory) Fields() []FieldDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FieldDef, 0, len(m.fields))
	for _, row := range m.fields {
		out = append(out, copyField(row))
	}
	return out
}

func valueKey(def, record domain.ID) string {
	return string(def) + "\x00" + string(record)
}

func (m *Memory) PutValue(ctx context.Context, row FieldValue) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[valueKey(row.DefinitionID, row.RecordID)] = row
	return nil
}

func (m *Memory) Values(record domain.ID) []FieldValue {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []FieldValue
	for _, row := range m.values {
		if row.RecordID == record {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) EnsureMarkers(ctx context.Context, labels []string, at time.Time, actor domain.ID, ids func() (domain.ID, error)) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	have := map[string]bool{}
	for _, row := range m.markers {
		have[row.Label] = true
	}
	for _, label := range labels {
		if have[label] {
			continue
		}
		id, err := ids()
		if err != nil {
			return err
		}
		m.markers[id] = MarkerDef{ID: id, Label: label, CreatedAt: at, CreatedBy: actor, UpdatedAt: at}
		have[label] = true
	}
	return nil
}

func (m *Memory) Marker(id domain.ID) (MarkerDef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.markers[id]
	return row, ok
}

// MarkerByLabel finds the firm marker with label, preferring an active one. Module
// markers are pinned by id only.
func (m *Memory) MarkerByLabel(label string) (MarkerDef, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found MarkerDef
	ok := false
	for _, row := range m.markers {
		if row.Label != label || row.ModuleID != "" {
			continue
		}
		if !ok || (found.Inactive && !row.Inactive) || (found.Inactive == row.Inactive && row.ID < found.ID) {
			found, ok = row, true
		}
	}
	return found, ok
}

func (m *Memory) PutMarker(ctx context.Context, row MarkerDef) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markers[row.ID] = row
	return nil
}

// Markers returns every marker definition in id (creation) order.
func (m *Memory) Markers() []MarkerDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MarkerDef, 0, len(m.markers))
	for _, row := range m.markers {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Memory) PutPartyRole(ctx context.Context, row PartyRoleDef) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, other := range m.roles {
		if other.ID != row.ID && other.Key == row.Key {
			return repository.ErrConflict
		}
	}
	m.roles[row.ID] = row
	return nil
}

// PartyRoles returns every party-role record in id (creation) order.
func (m *Memory) PartyRoles() []PartyRoleDef {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PartyRoleDef, 0, len(m.roles))
	for _, row := range m.roles {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// UpdateMatterMarker replaces a pinned marker; the same marker twice on a matter is a conflict.
func (m *Memory) UpdateMatterMarker(ctx context.Context, row MatterMarker) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pinned[row.ID]; !ok {
		return repository.ErrNotFound
	}
	for id, existing := range m.pinned {
		if id != row.ID && existing.MatterID == row.MatterID && existing.MarkerID == row.MarkerID && existing.PracticeAreaID == row.PracticeAreaID {
			return repository.ErrConflict
		}
	}
	m.pinned[row.ID] = row
	return nil
}

func (m *Memory) PutMatterMarker(ctx context.Context, row MatterMarker) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.pinned {
		if existing.MatterID == row.MatterID && existing.MarkerID == row.MarkerID && existing.PracticeAreaID == row.PracticeAreaID {
			return repository.ErrConflict
		}
	}
	m.pinned[row.ID] = row
	return nil
}

func (m *Memory) MatterMarkers(matter domain.ID) []MatterMarker {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MatterMarker
	for _, row := range m.pinned {
		if matter.IsZero() || row.MatterID == matter {
			out = append(out, row)
		}
	}
	return out
}

func (m *Memory) AppendClose(ctx context.Context, row CloseRecord) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row.Warnings = append([]Warning(nil), row.Warnings...)
	m.closes = append(m.closes, row)
	return nil
}

func (m *Memory) LatestClose(matter domain.ID) (CloseRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found CloseRecord
	ok := false
	for _, row := range m.closes {
		if row.MatterID == matter {
			found = row
			found.Warnings = append([]Warning(nil), row.Warnings...)
			ok = true
		}
	}
	return found, ok
}

func (m *Memory) AppendRun(ctx context.Context, row ConflictRun) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row.Hits = append([]Hit(nil), row.Hits...)
	m.runs = append(m.runs, row)
	return nil
}

func (m *Memory) Runs(matter domain.ID) []ConflictRun {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []ConflictRun
	for _, row := range m.runs {
		if !matter.IsZero() && row.MatterID != matter {
			continue
		}
		copyRun := row
		copyRun.Hits = append([]Hit(nil), row.Hits...)
		out = append(out, copyRun)
	}
	return out
}

func (m *Memory) FindHit(id domain.ID) (ConflictRun, Hit, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.runs {
		for _, hit := range run.Hits {
			if hit.ID == id {
				return run, hit, true
			}
		}
	}
	return ConflictRun{}, Hit{}, false
}

func (m *Memory) SaveHit(ctx context.Context, hit Hit) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.runs {
		for j := range m.runs[i].Hits {
			if m.runs[i].Hits[j].ID == hit.ID {
				m.runs[i].Hits[j] = hit
				return nil
			}
		}
	}
	return repository.ErrNotFound
}

func (m *Memory) MarkChecked(ctx context.Context, party domain.ID, at time.Time) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked[party] = at
	return nil
}

func (m *Memory) Checked(party domain.ID) (time.Time, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.checked[party]
	return at, ok
}

func (m *Memory) TakeMatterMarker(ctx context.Context, id domain.ID) (MatterMarker, error) {
	if err := ready(ctx); err != nil {
		return MatterMarker{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pinned[id]
	if !ok {
		return MatterMarker{}, repository.ErrNotFound
	}
	delete(m.pinned, id)
	return row, nil
}

func (m *Memory) DeleteMatterMarker(ctx context.Context, matter domain.ID, label string, area domain.ID) (MatterMarker, error) {
	if err := ready(ctx); err != nil {
		return MatterMarker{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, row := range m.pinned {
		if row.MatterID == matter && row.Label == label && row.PracticeAreaID == area {
			delete(m.pinned, id)
			return row, nil
		}
	}
	return MatterMarker{}, repository.ErrNotFound
}

func (m *Memory) IdemGet(key string) (idemRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.idem[key]
	if !ok {
		return idemRecord{}, false
	}
	rec.Body = append([]byte(nil), rec.Body...)
	return rec, true
}

// IdemClaim reserves a key before the operation runs. A second caller with the same
// key waits out the first reservation instead of creating another row.
func (m *Memory) IdemClaim(ctx context.Context, key string, rec idemRecord) (idemRecord, bool, error) {
	if err := ready(ctx); err != nil {
		return idemRecord{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if prev, ok := m.idem[key]; ok {
		if prev.Actor != rec.Actor || prev.Op != rec.Op || prev.Target != rec.Target || prev.Fingerprint != rec.Fingerprint {
			return idemRecord{}, false, domain.Conflict("idempotency key was already used")
		}
		if prev.Pending {
			return idemRecord{}, false, domain.Conflict("idempotency key is in progress")
		}
		prev.Body = append([]byte(nil), prev.Body...)
		return prev, true, nil
	}
	rec.Pending = true
	rec.Body = nil
	m.idem[key] = rec
	return idemRecord{}, false, nil
}

// IdemComplete stores the result of a reservation made by IdemClaim.
func (m *Memory) IdemComplete(ctx context.Context, key string, rec idemRecord) error {
	if err := ready(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.idem[key]
	if !ok || prev.Actor != rec.Actor || prev.Op != rec.Op || prev.Target != rec.Target || prev.Fingerprint != rec.Fingerprint {
		return domain.Conflict("idempotency key was already used")
	}
	if !prev.Pending {
		return nil
	}
	rec.Pending = false
	rec.Body = append([]byte(nil), rec.Body...)
	m.idem[key] = rec
	return nil
}

// IdemAbort drops a reservation when the operation did not finish.
func (m *Memory) IdemAbort(key string, rec idemRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev, ok := m.idem[key]
	if !ok || !prev.Pending {
		return
	}
	if prev.Actor != rec.Actor || prev.Op != rec.Op || prev.Target != rec.Target || prev.Fingerprint != rec.Fingerprint {
		return
	}
	delete(m.idem, key)
}

func (m *Memory) IdemPut(ctx context.Context, key string, rec idemRecord) error {
	return m.IdemComplete(ctx, key, rec)
}

func copyField(row FieldDef) FieldDef {
	row.PracticeAreaIDs = append([]domain.ID(nil), row.PracticeAreaIDs...)
	row.Options = append([]string(nil), row.Options...)
	return row
}

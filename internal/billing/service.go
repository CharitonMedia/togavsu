package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/access"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Practice is the part of the practice store billing reads. practice.Memory and
// practice.Durable satisfy it.
type Practice interface {
	Matter(ctx context.Context, id domain.ID) (domain.Matter, error)
	Matters() []domain.Matter
	Contact(ctx context.Context, id domain.ID) (domain.Contact, error)
	Parties(matter, contact domain.ID) []domain.MatterParty
	Emails(contact domain.ID) []domain.ContactEmail
	User(ctx context.Context, id domain.ID) (domain.User, error)
	MatterMarkers(matter domain.ID) []practice.MatterMarker
}

// InvoiceMirror receives every invoice after it commits, so the practice close screen and
// At a Glance see unpaid balances. practice.Store's PutInvoice satisfies it.
type InvoiceMirror interface {
	PutInvoice(ctx context.Context, row domain.Invoice) error
}

// TrustBalance is one client's balance on a matter in one trust account.
type TrustBalance struct {
	ClientID     domain.ID    `json:"client_id"`
	AccountID    domain.ID    `json:"trust_account_id"`
	BalanceCents domain.Cents `json:"balance_cents"`
}

// TrustPosting asks the trust service to append one ledger entry. Amount is positive; the
// entry type gives the direction. PaymentID and InvoiceID link the entry back to billing.
type TrustPosting struct {
	EntryType   string
	MatterID    domain.ID
	ClientID    domain.ID
	AccountID   domain.ID
	AmountCents domain.Cents
	Method      string
	Description string
	InvoiceID   domain.ID
	PaymentID   domain.ID
}

// TrustPosted is the trust service's answer: the entry id and its TrustPostResult body.
// Pending is set when the trust write joined billing's transaction, so its after-commit
// effects (balance sink push, commit events) wait for Trust.Committed; billing calls it
// after its own commit and never after a rollback. Effects is the trust service's own
// result for that call.
type TrustPosted struct {
	EntryID domain.ID
	Result  any
	Pending bool
	Effects any
}

// ReplenishmentInput is a collections-panel replenishment request linked to an invoice.
type ReplenishmentInput struct {
	MatterID    domain.ID
	ClientID    domain.ID
	AmountCents domain.Cents
	InvoiceID   domain.ID
	Message     string
}

// FlatFeeApplication asks the trust service to apply a flat fee from trust to operating and
// record it as the matter's flat-fee application, the record the trust page reads too.
type FlatFeeApplication struct {
	MatterID    domain.ID
	ClientID    domain.ID
	AccountID   domain.ID
	AmountCents domain.Cents
}

// Trust is the trust service seam. The trust package owns the append-only ledger, the
// below-zero flags and their email; billing never writes a ledger row itself. Post, Reverse,
// ApplyFlatFee and RequestReplenishment must join the transaction carried by ctx, so a
// payment and its ledger row commit together; Committed then runs their after-commit effects.
// TrustLink adapts *trust.Service.
//
// Balances and FlatFeeCollected are read after billing has authorized the caller on the
// matter. FlatFeeCollected is the amount the matter's flat-fee applications still cover (not
// reversed), whether they were applied from billing or from the trust page, so one record
// backs the worklist status and the second-apply refusal on both pages.
type Trust interface {
	Balances(ctx context.Context, matterID domain.ID) ([]TrustBalance, error)
	FlatFeeCollected(ctx context.Context, matterID domain.ID) (domain.Cents, error)
	Post(ctx context.Context, p domain.Principal, in TrustPosting) (TrustPosted, error)
	ApplyFlatFee(ctx context.Context, p domain.Principal, in FlatFeeApplication) (TrustPosted, error)
	Reverse(ctx context.Context, p domain.Principal, entryID domain.ID, reason string) (TrustPosted, error)
	// Reversal returns the entry that reverses entryID on the matter, or zero when none does.
	Reversal(ctx context.Context, matterID, entryID domain.ID) (domain.ID, error)
	RequestReplenishment(ctx context.Context, p domain.Principal, in ReplenishmentInput) (TrustPosted, error)
	Committed(ctx context.Context, posted ...TrustPosted)
}

// Attachment is one mail attachment.
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// Message is one system mail.
type Message struct {
	To          []string
	Subject     string
	Text        string
	Attachments []Attachment
}

// Mailer sends system mail (PRD 8.8).
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Receipts checks a file before an expense names it as its receipt. FileLink provides it over
// the files service: CheckReceipt returns nil only when fileID is an untrashed receipt file
// record on matterID that p may read under roles, matter walls and attorney-only rules.
// ReceiptFiles lists the receipt files on a matter that p may read, for the expense screen.
// AddReceipt stores an uploaded receipt as a new receipt file record on matterID, written as
// p so the files service's write rules apply, and returns its id.
type Receipts interface {
	CheckReceipt(ctx context.Context, p domain.Principal, matterID, fileID domain.ID) error
	ReceiptFiles(ctx context.Context, p domain.Principal, matterID domain.ID) ([]ReceiptFile, error)
	AddReceipt(ctx context.Context, p domain.Principal, matterID domain.ID, name string, r io.Reader, size int64) (domain.ID, error)
}

// ReceiptFile is one receipt file an expense may name.
type ReceiptFile struct {
	ID   domain.ID
	Name string
}

// InvoiceFiles keeps invoice PDFs in the one file store (PRD 5) as invoice_pdf file records
// on the invoice's matter. FileLink provides it over the files service. SaveInvoicePDF adds
// pdf as the next immutable version of fileID, or creates the file when fileID is zero or
// gone, and returns the file id. by is the person whose send or download stored it.
type InvoiceFiles interface {
	SaveInvoicePDF(ctx context.Context, by domain.ID, inv Invoice, fileID domain.ID, pdf []byte) (domain.ID, error)
}

// Deps wires a Service. Store, Practice, Walls and Audit are required.
type Deps struct {
	Store    Store
	Practice Practice
	Walls    domain.WallReader
	Audit    *audit.Service
	Settings *settings.Registry
	Trust    Trust
	Mail     Mailer
	Mirror   InvoiceMirror
	// Receipts checks expense receipts against the files service (FileLink). While it is
	// nil, expenses refuse receipt_file_id.
	Receipts Receipts
	// InvoiceFiles stores sent invoices' PDFs as invoice_pdf file records (FileLink). While
	// it is nil, PDFs are rendered on demand only.
	InvoiceFiles InvoiceFiles
	// Custom holds time entry and expense custom field values (practice's custom
	// fields). While it is nil, records show no custom fields and the PUT routes refuse.
	Custom CustomFields
	// Logo returns the bytes of the firm logo file. A JPEG is drawn on the invoice PDF.
	Logo  func(ctx context.Context, fileID domain.ID) ([]byte, error)
	Now   func() time.Time
	NewID func() (domain.ID, error)
}

// Service is the billing application service. Every read and write checks the caller's
// roles and the matter wall here, so the API, the UI, MCP and modules get the same answers.
type Service struct {
	d    Deps
	gate access.Gate
	mu   sync.Mutex
	// processors is the module runtime's payment processor seam (processor.go).
	processors Processors
	// pdfMu serializes storing invoice PDFs, so two downloads of one invoice add one file.
	pdfMu sync.Mutex
}

// New returns a service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Practice == nil || d.Walls == nil || d.Audit == nil {
		return nil, errors.New("billing: store, practice, walls and audit are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = domain.NewID
	}
	return &Service{d: d, gate: access.New(d.Walls)}, nil
}

func (s *Service) now() time.Time { return s.d.Now().UTC() }

// today is the firm-local calendar date.
func (s *Service) today(ctx context.Context) string { return s.firmDate(ctx, s.now()) }

// firmDate is t's calendar date in the firm's time zone, so a timestamp compares with
// report dates (which are firm dates) on the day the firm saw it happen.
func (s *Service) firmDate(ctx context.Context, t time.Time) string {
	if zone := s.setting(ctx, "firm.time_zone"); zone != "" {
		if in, err := domain.InZone(t, zone); err == nil {
			t = in
		}
	}
	return t.Format(dateLayout)
}

const dateLayout = "2006-01-02"

func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		return time.Time{}, domain.Invalid(field, "must be a date YYYY-MM-DD")
	}
	return t, nil
}

func addDays(date string, days int) string {
	t, err := time.Parse(dateLayout, date)
	if err != nil {
		return date
	}
	return time.Date(t.Year(), t.Month(), t.Day()+days, 0, 0, 0, 0, time.UTC).Format(dateLayout)
}

func daysBetween(from, to string) int {
	a, err1 := time.Parse(dateLayout, from)
	b, err2 := time.Parse(dateLayout, to)
	if err1 != nil || err2 != nil {
		b = a // an unreadable date counts as no days
	}
	return int(b.Sub(a).Hours() / 24)
}

// setting reads a registry value, or the registry default when no registry is wired.
func (s *Service) setting(ctx context.Context, key string) string {
	if s.d.Settings != nil {
		if v, err := s.d.Settings.Get(ctx, key); err == nil {
			return v
		}
	}
	return defaultOf(key)
}

// settingInt reads an integer setting. An unreadable value falls back to the registry default,
// and a key the registry does not declare reads as ok false.
func (s *Service) settingInt(ctx context.Context, key string) int {
	n, _ := s.settingIntOK(ctx, key)
	return n
}

func (s *Service) settingIntOK(ctx context.Context, key string) (int, bool) {
	if n, err := strconv.Atoi(s.setting(ctx, key)); err == nil {
		return n, true
	}
	n, err := strconv.Atoi(defaultOf(key))
	return n, err == nil
}

func defaultOf(key string) string {
	for _, def := range settings.Definitions() {
		if def.Key == key {
			return def.Default
		}
	}
	return ""
}

func (s *Service) settingCents(ctx context.Context, key string) domain.Cents {
	c, err := domain.ParseCents(s.setting(ctx, key))
	if err != nil {
		return noCents
	}
	return c
}

func (s *Service) incrementMinutes(ctx context.Context) int {
	return s.settingInt(ctx, "billing.time_increment_minutes")
}

func (s *Service) meta(p domain.Principal) (domain.Meta, error) {
	id, err := s.d.NewID()
	if err != nil {
		return domain.Meta{}, err
	}
	now := s.now()
	return domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor(p)}, nil
}

func actor(p domain.Principal) domain.ID {
	if p.UserID.IsZero() {
		return domain.SystemUserID
	}
	return p.UserID
}

// allow checks one action on a resource of a matter. A wall answers not_found.
func (s *Service) allow(ctx context.Context, p domain.Principal, res domain.Resource, act domain.Action, matter domain.ID) error {
	return s.gate.Allow(ctx, p, domain.ResourceRef{Resource: res, Action: act, MatterID: matter})
}

// readBilling is the read check for time, expenses, worklist and reports: every role that may
// read billing reports (admin, attorney, staff, accountant), and never a client.
// ponytail: ceiling: domain.Policy gives accountants no ResTime/ResExpense read, so billing maps
// those reads to ResBillingReport. Upgrade path: the lead adds accountant reads of time and
// expenses to domain.Policy. Trigger: the next shared-contract PR touching domain/access.go.
func (s *Service) readBilling(ctx context.Context, p domain.Principal, matter domain.ID) error {
	return s.allow(ctx, p, domain.ResBillingReport, domain.ActionRead, matter)
}

// visible reports whether p may read billing for matter. Errors other than a denial are returned.
func (s *Service) visible(ctx context.Context, p domain.Principal, matter domain.ID) (bool, error) {
	err := s.readBilling(ctx, p, matter)
	if err == nil {
		return true, nil
	}
	if access.IsNotFound(err) || access.IsForbidden(err) {
		return false, nil
	}
	return false, err
}

func (s *Service) matter(ctx context.Context, id domain.ID) (domain.Matter, error) {
	if id.IsZero() {
		return domain.Matter{}, domain.Invalid("matter_id", "is required")
	}
	m, err := s.d.Practice.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, domain.NotFound("matter")
	}
	return m, nil
}

// clientParties returns the matter's client contact ids, primary first.
func (s *Service) clientParties(matter domain.ID) []domain.ID {
	parties := s.d.Practice.Parties(matter, "")
	sort.SliceStable(parties, func(i, j int) bool { return parties[i].Primary && !parties[j].Primary })
	var out []domain.ID
	for _, party := range parties {
		if party.MatterID == matter && party.Role == partyRoleClient {
			out = append(out, party.ContactID)
		}
	}
	return out
}

func (s *Service) isParty(matter, contact domain.ID) bool {
	for _, party := range s.d.Practice.Parties(matter, contact) {
		if party.MatterID == matter && party.ContactID == contact {
			return true
		}
	}
	return false
}

func (s *Service) isClient(matter, contact domain.ID) bool {
	for _, id := range s.clientParties(matter) {
		if id == contact {
			return true
		}
	}
	return false
}

func (s *Service) zeroFee(matter domain.ID) bool {
	for _, m := range s.d.Practice.MatterMarkers(matter) {
		if m.MatterID == matter && m.Label == zeroFeeMarker {
			return true
		}
	}
	return false
}

// txn is one billing write: store rows, audit rows and events that commit together.
type txn struct {
	ctx     context.Context
	s       *Service
	p       domain.Principal
	changes []audit.Change
	events  []domain.Event
	mirror  map[domain.ID]Invoice
	trust   []TrustPosted
}

// write serializes billing writes, runs fn in one store transaction, appends the audit rows
// and events fn queued, and mirrors changed invoices to practice after the commit.
func (s *Service) write(ctx context.Context, p domain.Principal, fn func(t *txn) error) error {
	t, err := s.commit(ctx, p, fn)
	if err != nil {
		return err
	}
	// Trust's after-commit effects run outside billing's lock: they publish trust events, and
	// TrustEvents answers those with a billing write of its own (SyncFlatFee).
	if len(t.trust) > 0 && s.d.Trust != nil {
		s.d.Trust.Committed(context.WithoutCancel(ctx), t.trust...)
	}
	return nil
}

// commit runs fn in one billing transaction under the write lock and mirrors the invoices
// it changed.
func (s *Service) commit(ctx context.Context, p domain.Principal, fn func(t *txn) error) (*txn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &txn{s: s, p: p, mirror: map[domain.ID]Invoice{}}
	err := s.d.Store.Atomic(ctx, func(ctx context.Context) error {
		return s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
			t.ctx = ctx
			if err := fn(t); err != nil {
				return err
			}
			for _, e := range t.events {
				if err := s.d.Store.Put(ctx, kindEvent, string(e.ID), e); err != nil {
					return err
				}
			}
			// Audit rows go last, so a failed store write appends none even where the audit
			// store cannot roll back (audit.Memory).
			for _, c := range t.changes {
				if _, err := s.d.Audit.Record(ctx, p, c); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	s.mirrorInvoices(context.WithoutCancel(ctx), t.mirror)
	return t, nil
}

// trusted keeps a trust result whose after-commit effects wait for billing's commit.
func (t *txn) trusted(posted TrustPosted) TrustPosted {
	if posted.Pending {
		t.trust = append(t.trust, posted)
	}
	return posted
}

func (s *Service) mirrorInvoices(ctx context.Context, rows map[domain.ID]Invoice) {
	if s.d.Mirror == nil {
		return
	}
	for _, inv := range rows {
		row := inv.Invoice
		if row.Status == StatusDraft {
			row.BalanceDueCents = noCents
		}
		// ponytail: ceiling: the mirror runs after the billing commit, so a failed mirror write
		// leaves the close screen stale until Resync. Upgrade path: practice reads billing's
		// balances through an interface. Trigger: practice close warnings move to a BalanceSource.
		_ = s.d.Mirror.PutInvoice(ctx, row)
	}
}

// Resync mirrors every invoice to practice. The core calls it once at start.
// It first brings flat matters' fee invoices in line with trust's flat-fee record, so a
// trust-page application whose commit event was lost is credited.
func (s *Service) Resync(ctx context.Context) error {
	for _, m := range s.d.Practice.Matters() {
		if m.BillingType == billingFlat {
			if err := s.SyncFlatFee(ctx, m.ID); err != nil {
				return err
			}
		}
	}
	rows, err := listOf[Invoice](ctx, s.d.Store, kindInvoice)
	if err != nil {
		return err
	}
	m := map[domain.ID]Invoice{}
	for _, r := range rows {
		m[r.ID] = r
	}
	s.mirrorInvoices(ctx, m)
	return nil
}

func (t *txn) put(kind string, id domain.ID, v any) error {
	return t.s.d.Store.Put(t.ctx, kind, string(id), v)
}

func (t *txn) putInvoice(inv Invoice) error {
	inv.UpdatedAt = t.s.now()
	if err := t.put(kindInvoice, inv.ID, inv); err != nil {
		return err
	}
	t.mirror[inv.ID] = inv
	return nil
}

func (t *txn) audit(entity string, id domain.ID, field, old, next, reason string) {
	t.changes = append(t.changes, audit.Change{Entity: entity, EntityID: id, Field: field, Old: old, New: next, Reason: reason})
}

// diff records one audit row per top-level JSON field that changed between before and after.
func (t *txn) diff(entity string, id domain.ID, before, after any, reason string) error {
	a, err := flat(before)
	if err != nil {
		return err
	}
	b, err := flat(after)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(b))
	for k := range b {
		keys = append(keys, k)
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "UpdatedAt" || k == "updated_at" || a[k] == b[k] {
			continue
		}
		t.audit(entity, id, k, a[k], b[k], reason)
	}
	return nil
}

// created records a create as one row with the whole record as the new value.
func (t *txn) created(entity string, id domain.ID, v any, reason string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.audit(entity, id, "create", "", string(b), reason)
	return nil
}

func flat(v any) (map[string]string, error) {
	out := map[string]string{}
	if v == nil {
		return out, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, raw := range m {
		var str string
		if json.Unmarshal(raw, &str) == nil {
			out[k] = str
			continue
		}
		out[k] = string(raw)
	}
	return out, nil
}

func (t *txn) event(action, recordType string, recordID, matter domain.ID, summary string, clientVisible bool) error {
	meta, err := t.s.meta(t.p)
	if err != nil {
		return err
	}
	t.events = append(t.events, domain.Event{
		Meta: meta, ActorKind: actorKind(t.p), ActorID: actor(t.p), Action: action,
		RecordType: recordType, RecordID: recordID, MatterID: matter, Summary: summary, ClientVisible: clientVisible,
	})
	return nil
}

func actorKind(p domain.Principal) string {
	switch p.Source {
	case domain.SourceSystem:
		return "system"
	case domain.SourceModule:
		return "module"
	case domain.SourceAPIToken:
		return "agent"
	}
	if p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) && !p.HasRole(domain.RoleAccountant) {
		return "client"
	}
	return "staff"
}

func sourceOf(p domain.Principal, given string) string {
	if given != "" {
		return given
	}
	switch p.Source {
	case domain.SourceModule:
		if p.ModuleID != "" {
			return p.ModuleID
		}
		return "module"
	case domain.SourceAPIToken:
		return "api"
	}
	return "manual"
}

// Events returns billing events for a matter the caller can see, newest first. A client-only
// principal gets only client-visible events, so every surface built on this read (timeline,
// MCP, portal) is filtered here rather than in its handler.
func (s *Service) Events(ctx context.Context, p domain.Principal, matter domain.ID) ([]domain.Event, error) {
	client := ClientOnly(p)
	// A client reads the matter as shared; the policy still checks the client is a party on it.
	if err := s.gate.Allow(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead,
		MatterID: matter, SharedWithClient: client}); err != nil {
		return nil, err
	}
	rows, err := listOf[domain.Event](ctx, s.d.Store, kindEvent)
	if err != nil {
		return nil, err
	}
	var out []domain.Event
	for i := len(rows) - 1; i >= 0; i-- {
		if client && !rows[i].ClientVisible {
			continue
		}
		if rows[i].MatterID == matter {
			out = append(out, rows[i])
		}
	}
	return out, nil
}

func amountField(field string, v domain.Cents) error {
	if !positive(int64(v)) {
		return domain.Invalid(field, "must be greater than zero")
	}
	return nil
}

func money(c domain.Cents) string { return c.String() }

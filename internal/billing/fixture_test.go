package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// fakeTrust is a contract stand-in for the trust package. It keeps an append-only ledger in
// the billing store, so a rolled-back billing write rolls its ledger rows back too, as the real
// trust service must by joining the transaction in ctx. Amounts are positive with a direction.
type fakeTrust struct {
	st   Store
	fail error
	// committed counts the results billing handed to Committed after its commit.
	committed []TrustPosted
}

type fakeEntry struct {
	ID       domain.ID
	Type     string
	Matter   domain.ID
	Client   domain.ID
	Account  domain.ID
	Signed   domain.Cents
	Reverses domain.ID
	Payment  domain.ID
	Invoice  domain.ID
	Flagged  bool
	FlatFee  bool
}

func (f *fakeTrust) entries(ctx context.Context) []fakeEntry {
	rows, err := listOf[fakeEntry](ctx, f.st, "fake_trust")
	if err != nil {
		panic(err)
	}
	return rows
}

func (f *fakeTrust) Balances(ctx context.Context, matter domain.ID) ([]TrustBalance, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	by := map[[2]domain.ID]domain.Cents{}
	var order [][2]domain.ID
	for _, e := range f.entries(ctx) {
		if e.Matter != matter {
			continue
		}
		k := [2]domain.ID{e.Client, e.Account}
		if _, ok := by[k]; !ok {
			order = append(order, k)
		}
		by[k] += e.Signed
	}
	var out []TrustBalance
	for _, k := range order {
		out = append(out, TrustBalance{ClientID: k[0], AccountID: k[1], BalanceCents: by[k]})
	}
	return out, nil
}

func (f *fakeTrust) balance(ctx context.Context, matter, client domain.ID) domain.Cents {
	var b domain.Cents
	for _, e := range f.entries(ctx) {
		if e.Matter == matter && e.Client == client {
			b += e.Signed
		}
	}
	return b
}

func (f *fakeTrust) Post(ctx context.Context, p domain.Principal, in TrustPosting) (TrustPosted, error) {
	if f.fail != nil {
		return TrustPosted{}, f.fail
	}
	if in.AmountCents <= 0 {
		return TrustPosted{}, domain.Invalid("amount_cents", "positive")
	}
	id, _ := domain.NewID()
	signed := in.AmountCents
	if in.EntryType != "deposit" {
		signed = -signed
	}
	e := fakeEntry{ID: id, Type: in.EntryType, Matter: in.MatterID, Client: in.ClientID, Account: in.AccountID, Signed: signed, Payment: in.PaymentID, Invoice: in.InvoiceID}
	e.Flagged = f.balance(ctx, in.MatterID, in.ClientID)+signed < 0
	if err := f.st.Put(ctx, "fake_trust", string(id), e); err != nil {
		return TrustPosted{}, err
	}
	return TrustPosted{EntryID: id, Pending: true, Result: map[string]any{"entry": map[string]any{"id": id, "entry_type": in.EntryType, "amount_cents": int64(in.AmountCents)}, "flags": []any{}}}, nil
}

// ApplyFlatFee posts the transfer and marks it as the matter's flat-fee application, as the
// trust package records it in trust_flat_fee_applications.
func (f *fakeTrust) ApplyFlatFee(ctx context.Context, p domain.Principal, in FlatFeeApplication) (TrustPosted, error) {
	posted, err := f.Post(ctx, p, TrustPosting{EntryType: "transfer_to_operating", MatterID: in.MatterID, ClientID: in.ClientID, AccountID: in.AccountID, AmountCents: in.AmountCents})
	if err != nil {
		return TrustPosted{}, err
	}
	var e fakeEntry
	if err := f.st.Get(ctx, "fake_trust", string(posted.EntryID), &e); err != nil {
		return TrustPosted{}, err
	}
	e.FlatFee = true
	return posted, f.st.Put(ctx, "fake_trust", string(e.ID), e)
}

func (f *fakeTrust) FlatFeeCollected(ctx context.Context, matter domain.ID) (domain.Cents, error) {
	reversed := map[domain.ID]bool{}
	for _, e := range f.entries(ctx) {
		reversed[e.Reverses] = true
	}
	var sum domain.Cents
	for _, e := range f.entries(ctx) {
		if e.FlatFee && e.Matter == matter && !reversed[e.ID] {
			sum -= e.Signed
		}
	}
	return sum, nil
}

func (f *fakeTrust) Committed(_ context.Context, posted ...TrustPosted) {
	f.committed = append(f.committed, posted...)
}

func (f *fakeTrust) Reverse(ctx context.Context, p domain.Principal, entry domain.ID, reason string) (TrustPosted, error) {
	var orig *fakeEntry
	for _, e := range f.entries(ctx) {
		if e.Reverses == entry {
			return TrustPosted{}, domain.Conflict("already reversed")
		}
		if e.ID == entry {
			c := e
			orig = &c
		}
	}
	if orig == nil {
		return TrustPosted{}, domain.NotFound("trust entry")
	}
	id, _ := domain.NewID()
	e := fakeEntry{ID: id, Type: "reversal", Matter: orig.Matter, Client: orig.Client, Account: orig.Account, Signed: -orig.Signed, Reverses: entry}
	if err := f.st.Put(ctx, "fake_trust", string(id), e); err != nil {
		return TrustPosted{}, err
	}
	return TrustPosted{EntryID: id, Pending: true}, nil
}

func (f *fakeTrust) RequestReplenishment(ctx context.Context, p domain.Principal, in ReplenishmentInput) (TrustPosted, error) {
	id, _ := domain.NewID()
	row := map[string]any{"id": id, "matter_id": in.MatterID, "client_id": in.ClientID, "amount_requested_cents": int64(in.AmountCents), "invoice_id": in.InvoiceID, "message": in.Message, "status": "active"}
	return TrustPosted{EntryID: id, Result: row, Pending: true}, f.st.Put(ctx, "fake_replenish", string(id), row)
}

type fakeMail struct {
	sent []Message
	fail error
}

func (m *fakeMail) Send(_ context.Context, msg Message) error {
	if m.fail != nil {
		return m.fail
	}
	m.sent = append(m.sent, msg)
	return nil
}

type fx struct {
	t     *testing.T
	ctx   context.Context
	pm    *practice.Memory
	ps    *practice.Service
	svc   *Service
	st    Store
	aud   *audit.Memory
	trust *fakeTrust
	// link, when set, replaces the stand-in with the real trust service.
	link Trust
	mail *fakeMail
	now  time.Time
	// auditLen counts audit rows; Postgres tests point it at audit_log.
	auditLen func() int

	admin, attorney, staff, staff2, accountant domain.Principal
	activity, expType, area, account           domain.ID
	n                                          uint64
}

func tid(n uint64) domain.ID {
	id, err := domain.ComposeV7(1_767_000_000_000+n, uint16(n%4096), n+7)
	if err != nil {
		panic(err)
	}
	return id
}

func person(id domain.ID, roles ...domain.Role) domain.Principal {
	return domain.Principal{UserID: id, Roles: roles, Source: domain.SourceUI}
}

func newFx(t *testing.T) *fx {
	return newFxStore(t, NewMemory())
}

func newFxStore(t *testing.T, st Store) *fx {
	t.Helper()
	f := &fx{t: t, ctx: context.Background(), pm: practice.NewMemory(), st: st, mail: &fakeMail{},
		now: time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)}
	ps, err := practice.NewWithSettings(f.pm)
	if err != nil {
		t.Fatal(err)
	}
	f.ps = ps
	f.admin = f.user("Ann Admin", 0, domain.RoleAdmin)
	f.attorney = f.user("Ada Attorney", 250_00, domain.RoleAttorney)
	f.staff = f.user("Sam Staff", 90_00, domain.RoleStaff)
	f.staff2 = f.user("Sue Staff", 0, domain.RoleStaff)
	f.accountant = f.user("Al Accountant", 0, domain.RoleAccountant)
	f.aud = audit.NewMemory()
	f.trust = &fakeTrust{st: st}
	f.newService(nil)
	area, err := ps.CreateArea(f.ctx, f.admin, "Probate", "", []string{"Intake"})
	if err != nil {
		t.Fatal(err)
	}
	f.area = area.ID
	name := "Drafting"
	at, err := f.svc.CreateType(f.ctx, f.admin, false, TypeInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	f.activity = at.ID
	name = "Filing fee"
	et, err := f.svc.CreateType(f.ctx, f.admin, true, TypeInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	f.expType = et.ID
	f.account = f.id()
	return f
}

func (f *fx) newService(a audit.Store) {
	f.t.Helper()
	if a == nil {
		a = f.aud
	}
	aud, err := audit.New(a, func() time.Time { return f.now })
	if err != nil {
		f.t.Fatal(err)
	}
	var tr Trust = f.trust
	if f.link != nil {
		tr = f.link
	}
	svc, err := New(Deps{Store: f.st, Practice: f.pm, Walls: f.ps.Policy.Walls, Audit: aud, Trust: tr,
		Mail: f.mail, Mirror: f.pm, Now: func() time.Time { return f.now }})
	if err != nil {
		f.t.Fatal(err)
	}
	f.svc = svc
}

func (f *fx) id() domain.ID {
	f.n++
	return tid(f.n)
}

func (f *fx) user(name string, rate domain.Cents, roles ...domain.Role) domain.Principal {
	f.t.Helper()
	p := person(f.id(), roles...)
	enc, err := domain.MarshalRoles(roles)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.pm.PutUser(f.ctx, domain.User{Meta: domain.Meta{ID: p.UserID}, Name: name, Roles: enc, Active: true, DefaultHourlyRateCents: rate}); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// matter creates a matter with one client party who has an email address.
func (f *fx) matter(name, billing string, flat, rate domain.Cents) (domain.Matter, domain.ID) {
	f.t.Helper()
	m, err := f.ps.CreateMatter(f.ctx, f.attorney, practice.MatterInput{Name: name, PracticeAreaID: f.area, BillingType: billing,
		FlatFeeCents: flat, MatterRateCents: rate, ResponsibleAttorneyID: f.attorney.UserID})
	if err != nil {
		f.t.Fatal(err)
	}
	c, err := f.ps.CreateContact(f.ctx, f.staff, practice.ContactInput{Kind: "person", GivenName: "Cora", FamilyName: "Client " + name})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.ps.AddParty(f.ctx, f.staff, m.ID, practice.PartyInput{ContactID: c.ID, Role: "client", Primary: true}); err != nil {
		f.t.Fatal(err)
	}
	if err := f.pm.InsertEmail(f.ctx, domain.ContactEmail{Meta: domain.Meta{ID: f.id()}, ContactID: c.ID, Primary: true, Email: "cora@example.test"}); err != nil {
		f.t.Fatal(err)
	}
	return m, c.ID
}

func (f *fx) logTime(p domain.Principal, matter domain.ID, date string, increments int, rate *domain.Cents) TimeEntry {
	f.t.Helper()
	desc := "Draft pleading"
	row, err := f.svc.CreateTime(f.ctx, p, TimeInput{MatterID: &matter, WorkedOn: &date, Increments: &increments,
		Description: &desc, ActivityTypeID: &f.activity, RateCents: rate})
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fx) expense(matter domain.ID, amount domain.Cents, markup int) Expense {
	f.t.Helper()
	date, desc := "2026-06-02", "Court filing fee"
	row, err := f.svc.CreateExpense(f.ctx, f.staff, ExpenseInput{MatterID: &matter, IncurredOn: &date, Description: &desc,
		AmountCents: &amount, ExpenseTypeID: &f.expType, MarkupPercent: &markup})
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

func (f *fx) deposit(matter, client domain.ID, amount domain.Cents) Payment {
	f.t.Helper()
	pay, err := f.svc.CreatePayment(f.ctx, f.staff, PaymentInput{ContactID: client, MatterID: matter, AmountCents: amount,
		Destination: DestTrust, TrustAccountID: f.account, Method: "check", ReferenceNumber: "1001"})
	if err != nil {
		f.t.Fatal(err)
	}
	return pay
}

// sent drafts, approves and sends the matter's unbilled work as a file delivery.
func (f *fx) sent(matter domain.ID) Invoice {
	f.t.Helper()
	invs, err := f.svc.DraftInvoices(f.ctx, f.staff, DraftRequest{MatterID: matter})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.svc.ApproveInvoice(f.ctx, f.attorney, invs[0].ID); err != nil {
		f.t.Fatal(err)
	}
	inv, err := f.svc.SendInvoice(f.ctx, f.attorney, invs[0].ID, "file")
	if err != nil {
		f.t.Fatal(err)
	}
	return inv
}

// snapshot is every stored billing row plus the audit row count, for nothing-written checks.
func (f *fx) snapshot() string {
	f.t.Helper()
	out := map[string][]json.RawMessage{}
	for _, k := range []string{kindTime, kindTimer, kindExpense, kindInvoice, kindPayment, kindFeeItem, kindActivity, kindExpenseType, kindEvent, "fake_trust", "fake_replenish"} {
		rows, err := f.st.List(f.ctx, k)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, r := range rows {
			out[k] = append(out[k], r)
		}
	}
	b, _ := json.Marshal(out)
	n := f.aud.Len()
	if f.auditLen != nil {
		n = f.auditLen()
	}
	return string(b) + "|audit=" + jsonInt(n)
}

// snapshotWithout is snapshot with one record kind left out.
func (f *fx) snapshotWithout(skip string) string {
	f.t.Helper()
	out := map[string][]json.RawMessage{}
	for _, k := range []string{kindTime, kindTimer, kindExpense, kindInvoice, kindPayment, kindFeeItem, kindActivity, kindExpenseType, kindEvent, "fake_trust", "fake_replenish"} {
		if k == skip {
			continue
		}
		rows, err := f.st.List(f.ctx, k)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, r := range rows {
			out[k] = append(out[k], r)
		}
	}
	b, _ := json.Marshal(out)
	return string(b) + "|audit=" + jsonInt(f.aud.Len())
}

// eventCounts counts the stored events on a record by action.
func (f *fx) eventCounts(record domain.ID) map[string]int {
	f.t.Helper()
	rows, err := listOf[domain.Event](f.ctx, f.st, kindEvent)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]int{}
	for _, e := range rows {
		if e.RecordID == record {
			out[e.Action]++
		}
	}
	return out
}

func jsonInt(n int) string { b, _ := json.Marshal(n); return string(b) }

func code(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func wantCode(t *testing.T, err error, want string) {
	t.Helper()
	if code(err) != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func cents(c domain.Cents) *domain.Cents { return &c }

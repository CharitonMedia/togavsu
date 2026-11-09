package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

const (
	openMatter   = domain.ID("matter-open")
	walledMatter = domain.ID("matter-walled")
	staffID      = domain.ID("user-staff")
	adminID      = domain.ID("user-admin")
	clientID     = domain.ID("user-client")
	contactID    = domain.ID("contact-client")
)

func staff() domain.Principal {
	return domain.Principal{UserID: staffID, Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceUI}
}

func admin() domain.Principal {
	return domain.Principal{UserID: adminID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
}

func accountant() domain.Principal {
	return domain.Principal{UserID: "user-accountant", Roles: []domain.Role{domain.RoleAccountant}, Source: domain.SourceUI}
}

func client() domain.Principal {
	return domain.Principal{UserID: clientID, Roles: []domain.Role{domain.RoleClient}, Source: domain.SourceUI}
}

type fake struct {
	walls   []domain.MatterWall
	parties []domain.MatterParty
	users   map[domain.ID]domain.User
}

func (f *fake) seed() {
	f.users = map[domain.ID]domain.User{
		staffID:  {Meta: domain.Meta{ID: staffID}, Active: true},
		adminID:  {Meta: domain.Meta{ID: adminID}, Active: true},
		clientID: {Meta: domain.Meta{ID: clientID}, ContactID: contactID, Active: true},
	}
	f.walls = []domain.MatterWall{{Meta: domain.Meta{ID: "wall-1"}, MatterID: walledMatter, UserID: adminID}}
	f.parties = []domain.MatterParty{{
		Meta: domain.Meta{ID: "party-1"}, MatterID: openMatter, ContactID: contactID, Role: "client",
	}}
}

func (f *fake) Insert(_ context.Context, row domain.MatterWall) error {
	f.walls = append(f.walls, row)
	return nil
}
func (f *fake) Delete(_ context.Context, id domain.ID) error {
	var next []domain.MatterWall
	for _, row := range f.walls {
		if row.ID != id {
			next = append(next, row)
		}
	}
	f.walls = next
	return nil
}
func (f *fake) List(_ context.Context, filter repository.Filter) (repository.Page[domain.MatterWall], error) {
	var items []domain.MatterWall
	for _, row := range f.walls {
		if filter.MatterID != "" && row.MatterID != filter.MatterID {
			continue
		}
		items = append(items, row)
	}
	return repository.Page[domain.MatterWall]{Items: items}, nil
}

func (f *fake) InsertParty(_ context.Context, row domain.MatterParty) error {
	f.parties = append(f.parties, row)
	return nil
}
func (f *fake) Update(context.Context, domain.MatterParty) error { return errors.New("not used") }
func (f *fake) GetParty(_ context.Context, id domain.ID) (domain.MatterParty, error) {
	for _, row := range f.parties {
		if row.ID == id {
			return row, nil
		}
	}
	return domain.MatterParty{}, repository.ErrNotFound
}
func (f *fake) ListParties(_ context.Context, filter repository.Filter) (repository.Page[domain.MatterParty], error) {
	var items []domain.MatterParty
	for _, row := range f.parties {
		if filter.MatterID != "" && row.MatterID != filter.MatterID {
			continue
		}
		items = append(items, row)
	}
	return repository.Page[domain.MatterParty]{Items: items}, nil
}

func (f *fake) InsertUser(context.Context, domain.User) error { return errors.New("not used") }
func (f *fake) UpdateUser(context.Context, domain.User) error { return errors.New("not used") }
func (f *fake) Get(_ context.Context, id domain.ID) (domain.User, error) {
	row, ok := f.users[id]
	if !ok {
		return domain.User{}, repository.ErrNotFound
	}
	return row, nil
}
func (f *fake) ListUsers(context.Context, repository.Filter) (repository.Page[domain.User], error) {
	return repository.Page[domain.User]{}, errors.New("not used")
}

type wallsFake struct{ *fake }
type partyFake struct{ *fake }
type userFake struct{ *fake }

func (w wallsFake) Insert(ctx context.Context, row domain.MatterWall) error {
	return w.fake.Insert(ctx, row)
}
func (w wallsFake) Delete(ctx context.Context, id domain.ID) error { return w.fake.Delete(ctx, id) }
func (w wallsFake) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.MatterWall], error) {
	return w.fake.List(ctx, filter)
}
func (p partyFake) Insert(ctx context.Context, row domain.MatterParty) error {
	return p.fake.InsertParty(ctx, row)
}
func (p partyFake) Update(ctx context.Context, row domain.MatterParty) error {
	return p.fake.Update(ctx, row)
}
func (p partyFake) Get(ctx context.Context, id domain.ID) (domain.MatterParty, error) {
	return p.fake.GetParty(ctx, id)
}
func (p partyFake) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.MatterParty], error) {
	return p.fake.ListParties(ctx, filter)
}
func (u userFake) Insert(ctx context.Context, row domain.User) error {
	return u.fake.InsertUser(ctx, row)
}
func (u userFake) Update(ctx context.Context, row domain.User) error {
	return u.fake.UpdateUser(ctx, row)
}
func (u userFake) Get(ctx context.Context, id domain.ID) (domain.User, error) {
	return u.fake.Get(ctx, id)
}
func (u userFake) List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error) {
	return u.fake.ListUsers(ctx, filter)
}

func newGate(t *testing.T) (Gate, *fake) {
	t.Helper()
	f := &fake{}
	f.seed()
	g := New(Reader{Walls: wallsFake{f}, Parties: partyFake{f}, Users: userFake{f}})
	return g, f
}

func TestStaffCannotSeeWalledMatterOnAnySurface(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	matters := []domain.Matter{
		{Meta: domain.Meta{ID: openMatter}, Name: "Open"},
		{Meta: domain.Meta{ID: walledMatter}, Name: "Walled"},
	}
	visible, err := g.FilterMatters(ctx, staff(), matters)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].ID != openMatter {
		t.Fatalf("list: %+v", visible)
	}
	for _, surface := range []string{"list", "search", "widget", "report", "api"} {
		err := g.VisibleMatter(ctx, staff(), walledMatter)
		if !IsNotFound(err) {
			t.Fatalf("%s: %v", surface, err)
		}
	}
	if err := g.VisibleMatter(ctx, admin(), walledMatter); err != nil {
		t.Fatal(err)
	}
	off := admin()
	off.UserID = "user-other-admin"
	if err := g.VisibleMatter(ctx, off, walledMatter); !IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestAccountantReadsBillingAndCannotWrite(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	log, err := audit.New(audit.NewMemory(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reads := []domain.ResourceRef{
		{Resource: domain.ResInvoice, Action: domain.ActionRead},
		{Resource: domain.ResPayment, Action: domain.ActionRead},
		{Resource: domain.ResTrust, Action: domain.ActionRead},
		{Resource: domain.ResBillingReport, Action: domain.ActionRead},
		{Resource: domain.ResFile, Action: domain.ActionRead, FileClass: domain.FileReceipt},
		{Resource: domain.ResFile, Action: domain.ActionRead, FileClass: domain.FileInvoicePDF},
	}
	for _, ref := range reads {
		if err := g.Allow(ctx, accountant(), ref); err != nil {
			t.Fatalf("read %+v: %v", ref, err)
		}
	}
	denied := []domain.ResourceRef{
		{Resource: domain.ResInvoice, Action: domain.ActionWrite},
		{Resource: domain.ResPayment, Action: domain.ActionWrite},
		{Resource: domain.ResTrust, Action: domain.ActionWrite},
		{Resource: domain.ResTrust, Action: domain.ActionTrustWithdraw},
		{Resource: domain.ResNote, Action: domain.ActionRead},
		{Resource: domain.ResMatter, Action: domain.ActionRead},
		{Resource: domain.ResFile, Action: domain.ActionRead, FileClass: domain.FileGeneral},
		{Resource: domain.ResFile, Action: domain.ActionWrite, FileClass: domain.FileReceipt},
	}
	for _, ref := range denied {
		called := false
		err := g.Apply(ctx, accountant(), ref, audit.Change{
			Entity: "invoice", EntityID: openMatter, Field: "status", New: "sent",
		}, log, func() error {
			called = true
			return nil
		})
		if err == nil || called {
			t.Fatalf("write ran %+v err=%v", ref, err)
		}
	}
	if log == nil {
		t.Fatal("log")
	}
	rows, err := log.ForEntity(ctx, "invoice", openMatter)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

func TestAttorneyOnlyRedactionAndHistory(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	d, err := g.Decide(ctx, staff(), domain.ResourceRef{Resource: domain.ResSensitiveContact, Action: domain.ActionRead})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := Project(d, "123-45-6789")
	if err != nil || !shown.Redacted || shown.Value != "" {
		t.Fatal(shown, err)
	}
	d, err = g.Decide(ctx, admin(), domain.ResourceRef{Resource: domain.ResSensitiveContact, Action: domain.ActionRead})
	if err != nil {
		t.Fatal(err)
	}
	shown, err = Project(d, "123-45-6789")
	if err != nil || shown.Redacted || shown.Value != "123-45-6789" {
		t.Fatal(shown, err)
	}
	if err := g.Allow(ctx, staff(), domain.ResourceRef{
		Resource: domain.ResFile, Action: domain.ActionRead, MatterID: openMatter, AttorneyOnly: true,
	}); !IsNotFound(err) {
		t.Fatal(err)
	}
	rows := []domain.AuditEntry{{
		Meta: domain.Meta{ID: "audit-1"}, Entity: "matter", EntityID: openMatter,
		Field: "conflict_reason", OldValue: "prior client", NewValue: "opposing party",
	}, {
		Meta: domain.Meta{ID: "audit-2"}, Entity: "matter", EntityID: openMatter,
		Field: "name", OldValue: "A", NewValue: "B",
	}}
	staffView, err := g.History(ctx, staff(), openMatter, rows)
	if err != nil || len(staffView) != 2 {
		t.Fatal(staffView, err)
	}
	if !staffView[0].Old.Redacted || staffView[0].OldValue != audit.Redacted || staffView[1].New.Value != "B" {
		t.Fatalf("staff history %+v %+v", staffView[0], staffView[1])
	}
	attorney := domain.Principal{UserID: "user-attorney", Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}
	full, err := g.History(ctx, attorney, openMatter, rows)
	if err != nil || full[0].New.Value != "opposing party" || full[0].New.Redacted {
		t.Fatal(full, err)
	}
	if _, err := g.History(ctx, staff(), walledMatter, rows); !IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestClientSeesOnlySharedMatter(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	shared := domain.ResourceRef{
		Resource: domain.ResFile, Action: domain.ActionRead, MatterID: openMatter, SharedWithClient: true,
	}
	if err := g.Allow(ctx, client(), shared); err != nil {
		t.Fatal(err)
	}
	hidden := shared
	hidden.SharedWithClient = false
	if err := g.Allow(ctx, client(), hidden); !IsNotFound(err) {
		t.Fatal(err)
	}
	other := shared
	other.MatterID = walledMatter
	if err := g.Allow(ctx, client(), other); !IsNotFound(err) {
		t.Fatal(err)
	}
}

func TestDeniedWriteDoesNotMutate(t *testing.T) {
	ctx := context.Background()
	g, _ := newGate(t)
	log, err := audit.New(audit.NewMemory(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	status := "draft"
	err = g.Apply(ctx, staff(), domain.ResourceRef{
		Resource: domain.ResInvoice, Action: domain.ActionApproveInvoice, MatterID: openMatter,
	}, audit.Change{Entity: "invoice", EntityID: openMatter, Field: "status", Old: "draft", New: "approved"}, log, func() error {
		status = "approved"
		return nil
	})
	if !IsForbidden(err) || status != "draft" {
		t.Fatal(err, status)
	}
	err = g.Apply(ctx, domain.Principal{UserID: "atty", Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceUI}, domain.ResourceRef{
		Resource: domain.ResInvoice, Action: domain.ActionApproveInvoice, MatterID: openMatter,
	}, audit.Change{Entity: "invoice", EntityID: openMatter, Field: "status", Old: "draft", New: "approved"}, log, func() error {
		status = "approved"
		return nil
	})
	if err != nil || status != "approved" {
		t.Fatal(err, status)
	}
	rows, err := log.ForEntity(ctx, "invoice", openMatter)
	if err != nil || len(rows) != 1 || rows[0].Role != "attorney" || rows[0].Source != "ui" {
		t.Fatal(rows, err)
	}
}

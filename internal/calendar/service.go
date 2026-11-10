package calendar

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Party is one named party on a matter, for the available-dates caption.
type Party struct {
	Role string
	Name string
}

// MatterInfo is the slice of a matter the calendar needs.
type MatterInfo struct {
	ID             domain.ID
	Number         string
	Name           string
	Court          string
	CaseNumber     string
	ClientPosition string
	Parties        []Party
}

// Matters reads a matter as the caller. It must return a not_found APIError when
// the matter is missing or behind a wall the caller is not on. Practice satisfies it
// through PracticeMatters.
type Matters interface {
	Matter(ctx context.Context, p domain.Principal, id domain.ID) (MatterInfo, error)
	// Search finds matters the caller can see by name, number or case number. It
	// must omit walled matters; the calendar rechecks each hit anyway.
	Search(ctx context.Context, p domain.Principal, query string) ([]MatterChoice, error)
}

// Users resolves firm users. identity's user repository satisfies it.
type Users interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
}

// UsersFunc adapts a lookup function, such as practice.Memory.User or the identity
// user repository's Get, to Users.
type UsersFunc func(ctx context.Context, id domain.ID) (domain.User, error)

// Get calls f.
func (f UsersFunc) Get(ctx context.Context, id domain.ID) (domain.User, error) { return f(ctx, id) }

// Mailer sends one system mail. Nil leaves reminders in-app only.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Deps wires the service. Policy must carry the practice wall reader so that the
// calendar sees exactly the walls practice enforces.
type Deps struct {
	Store    Store
	Policy   domain.Policy
	Matters  Matters
	Users    Users
	Settings *settings.Registry
	Audit    *audit.Service
	Mailer   Mailer
	// BaseURL prefixes iCal links, such as https://firm.example. Empty gives a path.
	BaseURL string
	Now     func() time.Time
	IDs     func() (domain.ID, error)
	Secret  func() (string, error)
}

// Service is the calendar application service.
type Service struct {
	d Deps
	// wmu serializes calendar writes in commit, so an undo never overwrites a later write.
	wmu sync.Mutex
}

// New checks the required dependencies and returns a service.
func New(d Deps) (*Service, error) {
	switch {
	case d.Store == nil:
		return nil, errors.New("calendar: store is required")
	case d.Policy.Walls == nil:
		return nil, errors.New("calendar: wall reader is required")
	case d.Matters == nil:
		return nil, errors.New("calendar: matter reader is required")
	case d.Users == nil:
		return nil, errors.New("calendar: user reader is required")
	case d.Settings == nil:
		return nil, errors.New("calendar: settings registry is required")
	case d.Audit == nil:
		return nil, errors.New("calendar: audit service is required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDs == nil {
		d.IDs = domain.NewID
	}
	if d.Secret == nil {
		d.Secret = newSecret
	}
	return &Service{d: d}, nil
}

func (s *Service) now() time.Time { return s.d.Now().UTC() }

func (s *Service) meta(p domain.Principal) (domain.Meta, error) {
	id, err := s.d.IDs()
	if err != nil {
		return domain.Meta{}, err
	}
	now := s.now()
	actor := p.UserID
	if actor.IsZero() {
		actor = domain.SystemUserID
	}
	return domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor}, nil
}

// Zone is the firm time zone. An unset or unreadable zone is UTC.
func (s *Service) Zone(ctx context.Context) *time.Location {
	name, err := s.d.Settings.Get(ctx, settingZone)
	if err != nil {
		return time.UTC
	}
	loc, err := domain.LoadZone(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Today is the firm-local calendar date.
func (s *Service) Today(ctx context.Context) time.Time { return localDate(s.now(), s.Zone(ctx)) }

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	return s.d.Settings.Get(ctx, key)
}

func (s *Service) settingInt(ctx context.Context, key string) (int, error) {
	v, err := s.setting(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(v))
}

// DefaultDeadlineOffsets reads calendar.deadline_reminder_days (default 7 and 1).
func (s *Service) DefaultDeadlineOffsets(ctx context.Context) ([]int, error) {
	v, err := s.setting(ctx, settingDeadlineOffsets)
	if err != nil {
		return nil, err
	}
	return parseOffsets(v)
}

func parseOffsets(v string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, domain.Invalid("reminder_offsets_days", "offsets are whole days")
		}
		out = append(out, n)
	}
	return normalizeOffsets(out)
}

// offsetBounds are the reminder lead-time bounds the admin page enforces for
// calendar.event_reminder_days; per-deadline offsets use the same range.
func offsetBounds() (int, int) {
	for _, def := range settings.Definitions() {
		if def.Key == settingEventOffset {
			return int(def.Min), int(def.Max)
		}
	}
	panic("calendar: settings registry lacks " + settingEventOffset)
}

// normalizeOffsets validates the day range, drops duplicates, and sorts largest first.
func normalizeOffsets(in []int) ([]int, error) {
	lo, hi := offsetBounds()
	seen := map[int]bool{}
	out := make([]int, 0, len(in))
	for _, n := range in {
		if n < lo || n > hi {
			return nil, domain.Invalid("reminder_offsets_days", fmt.Sprintf("offsets must be between %d and %d days", lo, hi))
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}

// gate checks a calendar action. A wall denial is not_found so a walled row is
// indistinguishable from a missing one.
func (s *Service) gate(ctx context.Context, p domain.Principal, action domain.Action, matter domain.ID) error {
	dec, err := s.d.Policy.Authorize(ctx, p, domain.ResourceRef{Resource: domain.ResCalendar, Action: action, MatterID: matter})
	if err != nil {
		return err
	}
	if dec.Allow {
		return nil
	}
	switch dec.Reason {
	case domain.ReasonUnauthenticated:
		return domain.Unauthenticated("sign in")
	case domain.ReasonMatterWall:
		return domain.NotFound("record")
	}
	return domain.Forbidden("calendar access denied")
}

// visible reports whether p may read a row on matter. Errors hide the row.
func (s *Service) visible(ctx context.Context, p domain.Principal, matter domain.ID) bool {
	return s.gate(ctx, p, domain.ActionRead, matter) == nil
}

// checkMatter requires a matter the caller can read and write.
func (s *Service) checkMatter(ctx context.Context, p domain.Principal, matter domain.ID) error {
	if err := s.gate(ctx, p, domain.ActionWrite, matter); err != nil {
		return err
	}
	if _, err := s.d.Matters.Matter(ctx, p, matter); err != nil {
		var api *domain.APIError
		if errors.As(err, &api) && (api.Code == domain.CodeNotFound || api.Code == domain.CodeForbidden) {
			return domain.Invalid("matter_id", "matter not found")
		}
		return err
	}
	return nil
}

func (s *Service) activeUser(ctx context.Context, field string, id domain.ID) (domain.User, error) {
	if id.IsZero() {
		return domain.User{}, domain.Invalid(field, "user is required")
	}
	u, err := s.d.Users.Get(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || isCode(err, domain.CodeNotFound) {
			return domain.User{}, domain.Invalid(field, "user not found")
		}
		return domain.User{}, err
	}
	if !u.Active {
		return domain.User{}, domain.Invalid(field, "user is not active")
	}
	return u, nil
}

func isCode(err error, code string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == code
}

func missing(err error, what string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound(what)
	}
	return err
}

// change is one audited field value pair.
type change struct{ Field, Old, New string }

// commit applies one calendar write with its audit rows and activity row as one unit.
// write and the audit rows run inside audit.Service.Atomic, so on Postgres the audit rows
// commit or roll back together. The calendar store does not join that transaction, so
// when an audit row or the commit fails after write succeeded, undo puts the store back
// as write found it: the caller gets the error, nothing is left half-recorded, and a
// retry starts clean instead of creating a second event.
//
// ponytail: ceiling: the activity row is appended to the calendar store after the audit
// commit; Memory.AppendEvent cannot fail, so the two cannot disagree today. Upgrade path:
// a Postgres calendar Store whose writes and AppendEvent join WithTx inside Atomic, and
// undo goes away. Trigger: the Postgres calendar Store lands.
func (s *Service) commit(ctx context.Context, p domain.Principal, write, undo func(context.Context) error, entity string, id, matter domain.ID, action, summary string, changes []change) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	wrote := false
	err := s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		if err := write(ctx); err != nil {
			return err
		}
		wrote = true
		return s.auditRows(ctx, p, entity, id, changes)
	})
	if err != nil {
		if wrote {
			if uerr := undo(context.WithoutCancel(ctx)); uerr != nil {
				return errors.Join(err, fmt.Errorf("calendar: undo %s %s: %w", entity, id, uerr))
			}
		}
		return err
	}
	return s.activity(ctx, p, entity, id, matter, action, summary)
}

func (s *Service) auditRows(ctx context.Context, p domain.Principal, entity string, id domain.ID, changes []change) error {
	for _, c := range changes {
		if c.Old == c.New {
			continue
		}
		if _, err := s.d.Audit.Record(ctx, p, audit.Change{Entity: entity, EntityID: id, Field: c.Field, Old: c.Old, New: c.New}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) activity(ctx context.Context, p domain.Principal, entity string, id, matter domain.ID, action, summary string) error {
	m, err := s.meta(p)
	if err != nil {
		return err
	}
	return s.d.Store.AppendEvent(ctx, domain.Event{
		Meta: m, ActorKind: actorKind(p), ActorID: m.CreatedBy, Action: action,
		RecordType: entity, RecordID: id, MatterID: matter, Summary: summary,
	})
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
	return "staff"
}

func fmtTime(v int64) string {
	if !isSet(v) {
		return ""
	}
	return domain.FromUnixMicro(v).Format(time.RFC3339)
}

func fmtBool(b bool) string { return strconv.FormatBool(b) }

func joinIDs(ids []domain.ID) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = string(id)
	}
	return strings.Join(parts, ",")
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

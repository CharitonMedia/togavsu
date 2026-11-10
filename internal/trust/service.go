package trust

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the trust migrations (range 400-499). The core applies them after
// db.Embedded() with owner core.
// ponytail: ceiling: the trust migration ships from this package because internal/db is
// the integration lead's path. Upgrade path: move the file into internal/db/migrations
// unchanged (same version and checksum). Trigger: the finance integration.
func Migrations() ([]db.Migration, error) {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	return db.LoadFS(sub)
}

// Practice is the part of the practice store trust reads. practice.Memory and
// practice.Durable satisfy it. These reads do not check access; the service does.
type Practice interface {
	Matter(ctx context.Context, id domain.ID) (domain.Matter, error)
	Contact(ctx context.Context, id domain.ID) (domain.Contact, error)
	Parties(matter, contact domain.ID) []domain.MatterParty
}

// Users resolves admins and the responsible attorney for flags and notifications.
type Users interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
	List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error)
}

// Publisher receives commit events. It may be nil.
type Publisher interface {
	Publish(ctx context.Context, events []Event) error
}

// Deps wires the service. DB must be the same pool the audit service writes through so
// a trust write and its audit rows commit together.
type Deps struct {
	DB       *sql.DB
	Audit    *audit.Service
	Settings *settings.Registry
	Practice Practice
	Users    Users
	Walls    domain.WallReader
	Events   Publisher
	// BalanceSink, when set, gets each changed per-matter cache row after commit, so the
	// practice close warning and At a Glance read the same balance.
	BalanceSink func(ctx context.Context, row domain.TrustBalance) error
	Now         func() time.Time
}

// Service is the trust application service.
type Service struct {
	db       *sql.DB
	audit    *audit.Service
	settings *settings.Registry
	practice Practice
	users    Users
	policy   domain.Policy
	events   Publisher
	sink     func(context.Context, domain.TrustBalance) error
	now      func() time.Time
	newID    func() (domain.ID, error)
}

// New checks the required dependencies.
func New(d Deps) (*Service, error) {
	if d.DB == nil || d.Audit == nil || d.Settings == nil || d.Practice == nil || d.Users == nil || d.Walls == nil {
		return nil, errors.New("trust: DB, Audit, Settings, Practice, Users and Walls are required")
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		db: d.DB, audit: d.Audit, settings: d.Settings, practice: d.Practice, users: d.Users,
		policy: domain.Policy{Walls: d.Walls}, events: d.Events, sink: d.BalanceSink,
		now: func() time.Time { return now().UTC().Truncate(time.Microsecond) }, newID: domain.NewID,
	}, nil
}

func actorID(p domain.Principal) domain.ID {
	if p.UserID.IsZero() {
		return domain.SystemUserID
	}
	return p.UserID
}

// allow checks a trust action. matter may be empty for account-level checks.
func (s *Service) allow(ctx context.Context, who domain.Principal, res domain.Resource, action domain.Action, matter domain.ID) error {
	ref := domain.ResourceRef{Resource: res, Action: action, MatterID: matter}
	d, err := s.policy.Authorize(ctx, who, ref)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return domain.NotFound(string(res))
		}
		return err
	}
	if d.Allow {
		return nil
	}
	switch d.Reason {
	case domain.ReasonUnauthenticated:
		return domain.Unauthenticated("sign in is required")
	case domain.ReasonMatterWall, domain.ReasonClientScope:
		return domain.NotFound(string(res))
	}
	return domain.Forbidden("role does not allow this trust operation")
}

// readable is one caller's matter-wall view for list filtering.
type readable struct {
	s    *Service
	who  domain.Principal
	seen map[domain.ID]bool
}

func (s *Service) reader(ctx context.Context, who domain.Principal) (*readable, error) {
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	return &readable{s: s, who: who, seen: map[domain.ID]bool{}}, nil
}

func (r *readable) ok(ctx context.Context, matter domain.ID) (bool, error) {
	if matter.IsZero() {
		return true, nil
	}
	if v, hit := r.seen[matter]; hit {
		return v, nil
	}
	err := r.s.allow(ctx, r.who, domain.ResTrust, domain.ActionRead, matter)
	v := err == nil
	if err != nil {
		var api *domain.APIError
		if !errors.As(err, &api) {
			return false, err
		}
	}
	r.seen[matter] = v
	return v, nil
}

// tx runs fn in one transaction on the trust pool. It reports whether it joined an
// outer transaction, so commit events are left to that caller.
func (s *Service) tx(ctx context.Context, fn func(ctx context.Context) error) (joined bool, err error) {
	joined = audit.Tx(ctx, s.db) != nil
	return joined, audit.WithTx(ctx, s.db, fn)
}

func (s *Service) q(ctx context.Context) audit.Querier { return audit.Conn(ctx, s.db) }

// lock serializes trust writes: every account row is locked in id order, so balance
// reads inside the transaction see every earlier committed post.
// ponytail: ceiling: one trust write at a time for the whole firm. Upgrade path: lock
// only the client's rows by a per-client lock table. Trigger: trust posts queue for more
// than a second at the firm's busiest hour.
func (s *Service) lock(ctx context.Context) error {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id FROM trust_accounts ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	return rows.Close()
}

func (s *Service) record(ctx context.Context, who domain.Principal, entity string, id domain.ID, field, old, nv, reason string) error {
	_, err := s.audit.Record(ctx, who, audit.Change{Entity: entity, EntityID: id, Field: field, Old: old, New: nv, Reason: reason})
	return err
}

// publish sends commit events. A failure is logged: the write already committed.
// ponytail: ceiling: events are delivered in process after commit, not from a durable
// outbox. Upgrade path: write events to the events outbox in the same transaction.
// Trigger: module webhooks subscribe to trust events.
func (s *Service) publish(ctx context.Context, joined bool, events []Event) {
	if joined || s.events == nil || len(events) == 0 {
		return
	}
	if err := s.events.Publish(ctx, events); err != nil {
		log.Printf("trust: publish events: %v", err)
	}
}

func (s *Service) setting(ctx context.Context, key string) string {
	v, err := s.settings.Get(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

// pageLimit applies api.page_size and api.max_page_size. A zero ask is the default.
func (s *Service) pageLimit(ctx context.Context, asked int) (int, error) {
	def, err := s.intSetting(ctx, "api.page_size")
	if err != nil {
		return 0, err
	}
	max, err := s.intSetting(ctx, "api.max_page_size")
	if err != nil {
		return 0, err
	}
	if def > max {
		def = max
	}
	if asked == int(0) {
		return def, nil
	}
	if !positive(int64(asked)) || asked > max {
		return 0, domain.Invalid("limit", "limit must be between 1 and "+strconv.Itoa(max))
	}
	return asked, nil
}

// paginate pages rows ordered by id. The cursor is the last id of the previous page.
func paginate[T any](rows []T, id func(T) domain.ID, cursor string, limit int) Page[T] {
	var start int
	if cursor != "" {
		start = len(rows)
		for i, r := range rows {
			if string(id(r)) > cursor {
				start = i
				break
			}
		}
	}
	end := start + limit
	page := Page[T]{Limit: limit}
	if end < len(rows) {
		page.Items = rows[start:end]
		page.NextCursor = string(id(rows[end-1]))
	} else {
		page.Items = rows[start:]
	}
	if page.Items == nil {
		page.Items = []T{}
	}
	return page
}

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound(what)
	}
	return err
}

func micro(t time.Time) int64 { return domain.UnixMicroUTC(t) }

func fromMicro(v int64) time.Time { return domain.FromUnixMicro(v) }

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// dayEnd is the first microsecond after the UTC date d.
// ponytail: ceiling: report and reconciliation dates are UTC days, not firm-time-zone
// days. Upgrade path: convert with the firm.time_zone setting. Trigger: a firm west of
// UTC posts entries in the evening of a statement date.
func dayEnd(d string) int64 {
	t, _ := time.Parse("2006-01-02", d)
	return micro(time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC))
}

func dayStart(d string) int64 {
	t, _ := time.Parse("2006-01-02", d)
	return micro(t)
}

func trimmed(s string) string { return strings.TrimSpace(s) }

package calendar

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the calendar migrations (range 500-599), applied in schema core
// after the core migrations.
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// Postgres is the Store on the core pool: events, attendees and deadlines in the
// section 5 tables, feeds, reminder claims, notices and idempotency in migration 0500,
// and activity rows in the shared events stream behind the case timeline.
type Postgres struct {
	DB *sql.DB
}

var _ Store = (*Postgres)(nil)

// NewPostgres returns the store on pool, whose search_path is schema core.
func NewPostgres(pool *sql.DB) *Postgres { return &Postgres{DB: pool} }

func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (p *Postgres) tx(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// PutEvent upserts the event and replaces its attendees in one transaction.
func (p *Postgres) PutEvent(ctx context.Context, row Event) error {
	return p.tx(ctx, func(tx *sql.Tx) error {
		e := row.CalendarEvent
		if _, err := tx.ExecContext(ctx, `INSERT INTO calendar_events (id, created_at, updated_at, created_by, title, kind, starts_at, ends_at,
all_day, matter_id, location, travel_minutes, notes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, title = EXCLUDED.title, kind = EXCLUDED.kind,
starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, all_day = EXCLUDED.all_day, matter_id = EXCLUDED.matter_id,
location = EXCLUDED.location, travel_minutes = EXCLUDED.travel_minutes, notes = EXCLUDED.notes`,
			string(e.ID), domain.UnixMicroUTC(e.CreatedAt), domain.UnixMicroUTC(e.UpdatedAt), string(e.CreatedBy), e.Title, e.Kind,
			e.StartsAt, e.EndsAt, bit(e.AllDay), nullID(e.MatterID), e.Location, e.TravelMinutes, e.Notes); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM calendar_event_attendees WHERE event_id = $1`, string(e.ID)); err != nil {
			return err
		}
		for _, u := range row.Attendees {
			aid, err := domain.NewID()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO calendar_event_attendees (id, created_at, updated_at, created_by, event_id, user_id)
VALUES ($1,$2,$3,$4,$5,$6)`, string(aid), domain.UnixMicroUTC(e.UpdatedAt), domain.UnixMicroUTC(e.UpdatedAt), string(e.CreatedBy), string(e.ID), string(u)); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteEvent removes an event and its attendees.
func (p *Postgres) DeleteEvent(ctx context.Context, id domain.ID) error {
	return p.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM calendar_event_attendees WHERE event_id = $1`, string(id)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM calendar_events WHERE id = $1`, string(id))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return repository.ErrNotFound
		}
		return nil
	})
}

const eventCols = `id, created_at, updated_at, created_by, title, kind, starts_at, ends_at, all_day, matter_id, location, travel_minutes, notes`

func (p *Postgres) events(ctx context.Context, where string, args ...any) ([]Event, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT `+eventCols+` FROM calendar_events `+where+` ORDER BY starts_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	idx := map[domain.ID]int{}
	for rows.Next() {
		var e domain.CalendarEvent
		var created, updated int64
		var allDay int
		var matter sql.NullString
		if err := rows.Scan(&e.ID, &created, &updated, &e.CreatedBy, &e.Title, &e.Kind, &e.StartsAt, &e.EndsAt, &allDay, &matter,
			&e.Location, &e.TravelMinutes, &e.Notes); err != nil {
			return nil, err
		}
		e.CreatedAt, e.UpdatedAt, e.AllDay, e.MatterID = domain.FromUnixMicro(created), domain.FromUnixMicro(updated), allDay == 1, domain.ID(matter.String)
		idx[e.ID] = len(out)
		out = append(out, Event{CalendarEvent: e})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	att, err := p.DB.QueryContext(ctx, `SELECT event_id, user_id FROM calendar_event_attendees ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer att.Close()
	for att.Next() {
		var ev, user domain.ID
		if err := att.Scan(&ev, &user); err != nil {
			return nil, err
		}
		if i, ok := idx[ev]; ok {
			out[i].Attendees = append(out[i].Attendees, user)
		}
	}
	return out, att.Err()
}

// Event reads one event with its attendees.
func (p *Postgres) Event(ctx context.Context, id domain.ID) (Event, error) {
	list, err := p.events(ctx, `WHERE id = $1`, string(id))
	if err != nil {
		return Event{}, err
	}
	if len(list) == 0 {
		return Event{}, repository.ErrNotFound
	}
	return list[0], nil
}

// Events lists every event in start order. The service filters them by access.
// ponytail: ceiling: every list reads all events and filters walls and windows in Go
// (the service's existing contract). Upgrade path: push the window and matter filter
// into the query. Trigger: 50k calendar rows or a calendar page slower than 200 ms.
func (p *Postgres) Events(ctx context.Context) ([]Event, error) { return p.events(ctx, "") }

// PutDeadline upserts a deadline.
func (p *Postgres) PutDeadline(ctx context.Context, row Deadline) error {
	d := row.Deadline
	offsets, err := json.Marshal(row.Offsets)
	if err != nil {
		return err
	}
	if row.Offsets == nil {
		offsets = []byte("[]")
	}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO deadlines (id, created_at, updated_at, created_by, matter_id, title, due_on,
responsible_user_id, status, reminder_offsets, done_by, done_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, matter_id = EXCLUDED.matter_id, title = EXCLUDED.title,
due_on = EXCLUDED.due_on, responsible_user_id = EXCLUDED.responsible_user_id, status = EXCLUDED.status,
reminder_offsets = EXCLUDED.reminder_offsets, done_by = EXCLUDED.done_by, done_at = EXCLUDED.done_at`,
		string(d.ID), domain.UnixMicroUTC(d.CreatedAt), domain.UnixMicroUTC(d.UpdatedAt), string(d.CreatedBy), string(d.MatterID), d.Title,
		d.DueOn, string(d.ResponsibleUserID), d.Status, string(offsets), nullID(d.DoneBy), d.DoneAt)
	return err
}

// DeleteDeadline removes a deadline; it only undoes a create whose audit failed.
func (p *Postgres) DeleteDeadline(ctx context.Context, id domain.ID) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM deadlines WHERE id = $1`, string(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (p *Postgres) deadlines(ctx context.Context, where string, args ...any) ([]Deadline, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, matter_id, title, due_on, responsible_user_id, status,
reminder_offsets, done_by, done_at FROM deadlines `+where+` ORDER BY due_on, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deadline
	for rows.Next() {
		var d domain.Deadline
		var created, updated int64
		var doneBy sql.NullString
		if err := rows.Scan(&d.ID, &created, &updated, &d.CreatedBy, &d.MatterID, &d.Title, &d.DueOn, &d.ResponsibleUserID, &d.Status,
			&d.ReminderOffsets, &doneBy, &d.DoneAt); err != nil {
			return nil, err
		}
		d.CreatedAt, d.UpdatedAt, d.DoneBy = domain.FromUnixMicro(created), domain.FromUnixMicro(updated), domain.ID(doneBy.String)
		row := Deadline{Deadline: d}
		if err := json.Unmarshal([]byte(d.ReminderOffsets), &row.Offsets); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Deadline reads one deadline.
func (p *Postgres) Deadline(ctx context.Context, id domain.ID) (Deadline, error) {
	list, err := p.deadlines(ctx, `WHERE id = $1`, string(id))
	if err != nil {
		return Deadline{}, err
	}
	if len(list) == 0 {
		return Deadline{}, repository.ErrNotFound
	}
	return list[0], nil
}

// Deadlines lists every deadline by due date; the service filters them by access.
func (p *Postgres) Deadlines(ctx context.Context) ([]Deadline, error) { return p.deadlines(ctx, "") }

// PutFeed upserts a feed.
func (p *Postgres) PutFeed(ctx context.Context, row Feed) error {
	var revokedAt int64
	if !row.RevokedAt.IsZero() {
		revokedAt = domain.UnixMicroUTC(row.RevokedAt)
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO ical_feeds (id, created_at, updated_at, created_by, owner_user_id, secret_hash, revoked,
revoked_at, revoked_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at,
owner_user_id = EXCLUDED.owner_user_id, secret_hash = EXCLUDED.secret_hash, revoked = EXCLUDED.revoked,
revoked_at = EXCLUDED.revoked_at, revoked_by = EXCLUDED.revoked_by`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy), nullID(row.OwnerUserID),
		row.SecretHash, bit(row.Revoked), revokedAt, nullID(row.RevokedBy))
	return err
}

// DeleteFeed removes a feed; it only undoes a create whose audit failed.
func (p *Postgres) DeleteFeed(ctx context.Context, id domain.ID) error {
	res, err := p.DB.ExecContext(ctx, `DELETE FROM ical_feeds WHERE id = $1`, string(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (p *Postgres) feeds(ctx context.Context, where string, args ...any) ([]Feed, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, owner_user_id, secret_hash, revoked, revoked_at, revoked_by
FROM ical_feeds `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Feed
	for rows.Next() {
		var f Feed
		var created, updated, revokedAt int64
		var owner, by sql.NullString
		var revoked int
		if err := rows.Scan(&f.ID, &created, &updated, &f.CreatedBy, &owner, &f.SecretHash, &revoked, &revokedAt, &by); err != nil {
			return nil, err
		}
		f.CreatedAt, f.UpdatedAt = domain.FromUnixMicro(created), domain.FromUnixMicro(updated)
		f.OwnerUserID, f.Revoked, f.RevokedBy = domain.ID(owner.String), revoked == 1, domain.ID(by.String)
		if revokedAt != 0 {
			f.RevokedAt = domain.FromUnixMicro(revokedAt)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func one[T any](list []T, err error) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	if len(list) == 0 {
		return zero, repository.ErrNotFound
	}
	return list[0], nil
}

// Feed reads one feed.
func (p *Postgres) Feed(ctx context.Context, id domain.ID) (Feed, error) {
	return one(p.feeds(ctx, `WHERE id = $1`, string(id)))
}

// FeedBySecret finds the feed whose secret hashes to hash.
func (p *Postgres) FeedBySecret(ctx context.Context, hash string) (Feed, error) {
	return one(p.feeds(ctx, `WHERE secret_hash = $1`, hash))
}

// Feeds lists every feed.
func (p *Postgres) Feeds(ctx context.Context) ([]Feed, error) { return p.feeds(ctx, "") }

// ClaimReminder records that key was sent; false when another run already claimed it.
func (p *Postgres) ClaimReminder(ctx context.Context, key string) (bool, error) {
	res, err := p.DB.ExecContext(ctx, `INSERT INTO calendar_reminders_sent (reminder_key, created_at) VALUES ($1, $2)
ON CONFLICT (reminder_key) DO NOTHING`, key, domain.UnixMicroUTC(time.Now()))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReleaseReminder drops a claim whose delivery failed.
func (p *Postgres) ReleaseReminder(ctx context.Context, key string) error {
	_, err := p.DB.ExecContext(ctx, `DELETE FROM calendar_reminders_sent WHERE reminder_key = $1`, key)
	return err
}

// PutNotice stores one in-app reminder.
func (p *Postgres) PutNotice(ctx context.Context, n Notice) error {
	if n.ID.IsZero() {
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		n.ID = id
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO calendar_notices (id, created_at, user_id, kind, title, body, path, matter_id, record_id, emailed)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, string(n.ID), domain.UnixMicroUTC(n.At), string(n.UserID), n.Kind, n.Title, n.Body, n.Path,
		nullID(n.MatterID), nullID(n.RecordID), bit(n.Emailed))
	return err
}

// Notices lists a user's reminders in the order they were made.
func (p *Postgres) Notices(ctx context.Context, user domain.ID) ([]Notice, error) {
	rows, err := p.DB.QueryContext(ctx, `SELECT id, created_at, user_id, kind, title, body, path, matter_id, record_id, emailed
FROM calendar_notices WHERE user_id = $1 ORDER BY created_at, id`, string(user))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notice
	for rows.Next() {
		var n Notice
		var at int64
		var matter, record sql.NullString
		var emailed int
		if err := rows.Scan(&n.ID, &at, &n.UserID, &n.Kind, &n.Title, &n.Body, &n.Path, &matter, &record, &emailed); err != nil {
			return nil, err
		}
		n.At, n.MatterID, n.RecordID, n.Emailed = domain.FromUnixMicro(at), domain.ID(matter.String), domain.ID(record.String), emailed == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// AppendEvent adds an activity row to the shared events stream (case timeline).
func (p *Postgres) AppendEvent(ctx context.Context, row domain.Event) error {
	kind := row.ActorKind
	switch kind {
	case "staff", "client", "agent", "module", "system":
	default:
		kind = "staff"
	}
	_, err := p.DB.ExecContext(ctx, `INSERT INTO events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type,
record_id, matter_id, summary, client_visible) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,(SELECT id FROM matters WHERE id = $10),$11,$12)`,
		string(row.ID), domain.UnixMicroUTC(row.CreatedAt), domain.UnixMicroUTC(row.UpdatedAt), string(row.CreatedBy), kind, string(row.ActorID),
		row.Action, row.RecordType, string(row.RecordID), nullID(row.MatterID), row.Summary, bit(row.ClientVisible))
	return err
}

// IdemGet reads a stored Idempotency-Key response.
func (p *Postgres) IdemGet(key string) (IdemRecord, bool) {
	var rec IdemRecord
	var body string
	err := p.DB.QueryRowContext(context.Background(), `SELECT request_hash, status, body FROM calendar_idempotency WHERE idem_key = $1`, key).
		Scan(&rec.Hash, &rec.Status, &body)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("calendar: idempotency read: %v", err)
		}
		return IdemRecord{}, false
	}
	rec.Body = []byte(body)
	return rec, true
}

// IdemPut stores a response for key; a second put for the same key keeps the first.
func (p *Postgres) IdemPut(key string, rec IdemRecord) {
	if _, err := p.DB.ExecContext(context.Background(), `INSERT INTO calendar_idempotency (idem_key, request_hash, status, body)
VALUES ($1,$2,$3,$4) ON CONFLICT (idem_key) DO NOTHING`, key, rec.Hash, rec.Status, string(rec.Body)); err != nil {
		log.Printf("calendar: idempotency write: %v", err)
	}
}

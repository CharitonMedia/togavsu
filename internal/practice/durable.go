package practice

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Durable is the Postgres practice store. Reads come from the embedded Memory. A service
// mutation runs as one unit (Begin): its practice_records rows (migration 200) and its rows
// in the shared append-only audit_log, written through the writer role, commit in one
// transaction when the service call returns without error. Units are serialized, so a
// check-then-write such as closing an open matter cannot interleave with another write.
// A failed call or commit reloads Memory from Postgres, so the process never keeps a write
// that was not stored.
// ponytail: ceiling: one core process per database and a full load at start; the section 5
// relational tables are not yet the practice source of truth. Concurrent readers can see a
// unit's Memory changes before it commits, and briefly until the reload if the commit fails.
// Upgrade path: typed repositories over contacts, matters, matter_parties and notes with SQL
// filters, read inside the same transaction. Trigger: a second core replica, a firm above
// 100k practice rows, or a reader that must never see an uncommitted write.
type Durable struct {
	*Memory
	db     *sql.DB
	ledger *audit.Postgres
	users  func(context.Context, domain.ID) (domain.User, error)

	wmu sync.Mutex
	seq int64
}

var _ Store = (*Durable)(nil)

type record struct {
	kind string
	id   string
	body any
	del  bool
}

func put(kind string, id domain.ID, body any) record {
	return record{kind: kind, id: string(id), body: body}
}
func drop(kind string, id domain.ID) record { return record{kind: kind, id: string(id), del: true} }

// OpenDurable loads every practice row. users resolves identity users for display and
// ownership; it may be nil in tests.
func OpenDurable(ctx context.Context, pool *sql.DB, users func(context.Context, domain.ID) (domain.User, error)) (*Durable, error) {
	d := &Durable{db: pool, ledger: audit.NewPostgres(pool), users: users}
	if err := d.load(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Durable) load(ctx context.Context) error {
	m := NewMemory()
	rows, err := d.db.QueryContext(ctx, `SELECT kind, id, seq, body FROM practice_records ORDER BY seq`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var maxSeq int64
	for rows.Next() {
		var kind, id, body string
		var seq int64
		if err := rows.Scan(&kind, &id, &seq, &body); err != nil {
			return err
		}
		if seq > maxSeq {
			maxSeq = seq
		}
		if err := m.restore(kind, id, []byte(body)); err != nil {
			return fmt.Errorf("practice: load %s %s: %w", kind, id, err)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if d.Memory == nil {
		d.Memory = m
	} else {
		d.Memory.adopt(m)
	}
	d.seq = maxSeq
	return nil
}

// adopt replaces every collection with f's under m's lock, so readers never see a torn swap.
func (m *Memory) adopt(f *Memory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contacts, m.addresses, m.emails, m.phones, m.tags = f.contacts, f.addresses, f.emails, f.phones, f.tags
	m.matters, m.parties, m.walls, m.areas, m.counties = f.matters, f.parties, f.walls, f.areas, f.counties
	m.referrals, m.notes, m.noteTags, m.conflicts, m.reviews = f.referrals, f.notes, f.noteTags, f.conflicts, f.reviews
	m.invoices, m.trust, m.times, m.users = f.invoices, f.trust, f.times, f.users
	m.audits, m.events = f.audits, f.events
	m.fields, m.values, m.markers, m.pinned = f.fields, f.values, f.markers, f.pinned
	m.closes, m.runs, m.checked = f.closes, f.runs, f.checked
	// Pending idempotency reservations belong to requests still running in this process.
	for k, rec := range m.idem {
		if rec.Pending {
			f.idem[k] = rec
		}
	}
	m.idem = f.idem
}

func into[T any](raw []byte, dst map[domain.ID]T) error {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	var probe struct{ ID domain.ID }
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	dst[probe.ID] = v
	return nil
}

func appendTo[T any](raw []byte, dst *[]T) error {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*dst = append(*dst, v)
	return nil
}

// restore puts one stored document back in place. It runs before the store is shared.
func (m *Memory) restore(kind, id string, raw []byte) error {
	switch kind {
	case "contact":
		return into(raw, m.contacts)
	case "address":
		return into(raw, m.addresses)
	case "email":
		return into(raw, m.emails)
	case "phone":
		return into(raw, m.phones)
	case "tag":
		return into(raw, m.tags)
	case "matter":
		return into(raw, m.matters)
	case "party":
		return into(raw, m.parties)
	case "wall":
		return into(raw, m.walls)
	case "area":
		return into(raw, m.areas)
	case "county":
		return into(raw, m.counties)
	case "referral":
		return into(raw, m.referrals)
	case "note":
		return into(raw, m.notes)
	case "note_tag":
		return into(raw, m.noteTags)
	case "conflict":
		return into(raw, m.conflicts)
	case "review":
		return into(raw, m.reviews)
	case "invoice":
		return into(raw, m.invoices)
	case "trust":
		return into(raw, m.trust)
	case "time":
		return into(raw, m.times)
	case "user":
		return into(raw, m.users)
	case "field":
		return into(raw, m.fields)
	case "marker":
		return into(raw, m.markers)
	case "matter_marker":
		return into(raw, m.pinned)
	case "value":
		var v FieldValue
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		m.values[valueKey(v.DefinitionID, v.RecordID)] = v
	case "audit":
		return appendTo(raw, &m.audits)
	case "event":
		return appendTo(raw, &m.events)
	case "close":
		return appendTo(raw, &m.closes)
	case "run":
		return appendTo(raw, &m.runs)
	case "checked":
		var at time.Time
		if err := json.Unmarshal(raw, &at); err != nil {
			return err
		}
		m.checked[domain.ID(id)] = at
	case "idem":
		var rec idemRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return err
		}
		// A reservation that never completed died with its process; release the key.
		if !rec.Pending {
			m.idem[id] = rec
		}
	default:
		return fmt.Errorf("unknown kind")
	}
	return nil
}

// unit collects the rows of one service mutation until it commits.
type unit struct {
	d      *Durable
	recs   []record
	audits []domain.AuditEntry
}

type unitKey struct{}

func (d *Durable) unitOf(ctx context.Context) *unit {
	u, _ := ctx.Value(unitKey{}).(*unit)
	if u == nil || u.d != d {
		return nil
	}
	return u
}

// Begin starts a unit of work for one service mutation. Store writes made with the
// returned context are applied to Memory and buffered; done(err) commits them all in one
// transaction when err is nil, and otherwise discards them by reloading Memory. done
// returns the commit error, or err. A nested Begin joins the outer unit.
func (d *Durable) Begin(ctx context.Context) (context.Context, func(error) error) {
	if d.unitOf(ctx) != nil {
		return ctx, func(err error) error { return err }
	}
	d.wmu.Lock()
	u := &unit{d: d}
	return context.WithValue(ctx, unitKey{}, u), func(err error) error {
		defer d.wmu.Unlock()
		if err != nil {
			if len(u.recs) > 0 || len(u.audits) > 0 {
				d.reload(ctx)
			}
			return err
		}
		return d.commit(ctx, u.recs, u.audits)
	}
}

func (d *Durable) reload(ctx context.Context) {
	if err := d.load(context.WithoutCancel(ctx)); err != nil {
		log.Printf("practice: reload after failed write: %v", err)
	}
}

// write runs op on Memory, then commits recs (computed after op) and any audit rows. Inside
// a unit the rows are buffered for the unit's commit instead.
func (d *Durable) write(ctx context.Context, op func() error, recs func() []record, audits []domain.AuditEntry) error {
	u := d.unitOf(ctx)
	if u == nil {
		d.wmu.Lock()
		defer d.wmu.Unlock()
	}
	if err := op(); err != nil {
		return err
	}
	var list []record
	if recs != nil {
		list = recs()
	}
	if u != nil {
		u.recs = append(u.recs, list...)
		u.audits = append(u.audits, audits...)
		return nil
	}
	return d.commit(ctx, list, audits)
}

// commit stores list and appends audits to audit_log in one transaction. The caller holds wmu.
func (d *Durable) commit(ctx context.Context, list []record, audits []domain.AuditEntry) error {
	if len(list) == 0 && len(audits) == 0 {
		return nil
	}
	err := audit.WithTx(ctx, d.db, func(ctx context.Context) error {
		tx := audit.Tx(ctx, d.db)
		for _, r := range list {
			if r.del {
				if _, err := tx.ExecContext(ctx, `DELETE FROM practice_records WHERE kind = $1 AND id = $2`, r.kind, r.id); err != nil {
					return err
				}
				continue
			}
			body, err := json.Marshal(r.body)
			if err != nil {
				return err
			}
			d.seq++
			if _, err := tx.ExecContext(ctx, `INSERT INTO practice_records (kind, id, seq, body) VALUES ($1, $2, $3, $4)
				ON CONFLICT (kind, id) DO UPDATE SET body = EXCLUDED.body`, r.kind, r.id, d.seq, string(body)); err != nil {
				return err
			}
		}
		for _, row := range audits {
			if err := d.ledger.Append(ctx, row); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		d.reload(ctx)
		return err
	}
	return nil
}

func simple(ctx context.Context, d *Durable, op func() error, recs ...record) error {
	return d.write(ctx, op, func() []record { return recs }, nil)
}

// User prefers the identity users table, so practice sees the signed-in staff.
func (d *Durable) User(ctx context.Context, id domain.ID) (domain.User, error) {
	if d.users != nil {
		if row, err := d.users(ctx, id); err == nil {
			return row, nil
		}
	}
	return d.Memory.User(ctx, id)
}

func (d *Durable) PutUser(ctx context.Context, row domain.User) error {
	return simple(ctx, d, func() error { return d.Memory.PutUser(ctx, row) }, put("user", row.ID, row))
}
func (d *Durable) InsertContact(ctx context.Context, row domain.Contact) error {
	return simple(ctx, d, func() error { return d.Memory.InsertContact(ctx, row) }, put("contact", row.ID, row))
}
func (d *Durable) UpdateContact(ctx context.Context, row domain.Contact) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateContact(ctx, row) }, put("contact", row.ID, row))
}
func (d *Durable) InsertAddress(ctx context.Context, row domain.ContactAddress) error {
	return simple(ctx, d, func() error { return d.Memory.InsertAddress(ctx, row) }, put("address", row.ID, row))
}
func (d *Durable) UpdateAddress(ctx context.Context, row domain.ContactAddress) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateAddress(ctx, row) }, put("address", row.ID, row))
}
func (d *Durable) InsertEmail(ctx context.Context, row domain.ContactEmail) error {
	return simple(ctx, d, func() error { return d.Memory.InsertEmail(ctx, row) }, put("email", row.ID, row))
}
func (d *Durable) UpdateEmail(ctx context.Context, row domain.ContactEmail) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateEmail(ctx, row) }, put("email", row.ID, row))
}
func (d *Durable) InsertPhone(ctx context.Context, row domain.ContactPhone) error {
	return simple(ctx, d, func() error { return d.Memory.InsertPhone(ctx, row) }, put("phone", row.ID, row))
}
func (d *Durable) UpdatePhone(ctx context.Context, row domain.ContactPhone) error {
	return simple(ctx, d, func() error { return d.Memory.UpdatePhone(ctx, row) }, put("phone", row.ID, row))
}
func (d *Durable) InsertTag(ctx context.Context, row domain.ContactTag) error {
	return simple(ctx, d, func() error { return d.Memory.InsertTag(ctx, row) }, put("tag", row.ID, row))
}
func (d *Durable) UpdateTag(ctx context.Context, row domain.ContactTag) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateTag(ctx, row) }, put("tag", row.ID, row))
}
func (d *Durable) DeleteTag(ctx context.Context, id domain.ID) error {
	return simple(ctx, d, func() error { return d.Memory.DeleteTag(ctx, id) }, drop("tag", id))
}

func (d *Durable) InsertMatter(ctx context.Context, row domain.Matter, assign func([]string) (string, error)) (domain.Matter, error) {
	var out domain.Matter
	err := d.write(ctx, func() error {
		var err error
		out, err = d.Memory.InsertMatter(ctx, row, assign)
		return err
	}, func() []record { return []record{put("matter", out.ID, out)} }, nil)
	return out, err
}
func (d *Durable) UpdateMatter(ctx context.Context, row domain.Matter) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateMatter(ctx, row) }, put("matter", row.ID, row))
}
func (d *Durable) InsertParty(ctx context.Context, row domain.MatterParty) error {
	return simple(ctx, d, func() error { return d.Memory.InsertParty(ctx, row) }, put("party", row.ID, row))
}
func (d *Durable) UpdateParty(ctx context.Context, row domain.MatterParty) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateParty(ctx, row) }, put("party", row.ID, row))
}

func (d *Durable) ReplaceWalls(ctx context.Context, matter domain.ID, rows []domain.MatterWall) error {
	var recs []record
	return d.write(ctx, func() error {
		for _, old := range d.Memory.Walls(matter) {
			recs = append(recs, drop("wall", old.ID))
		}
		return d.Memory.ReplaceWalls(ctx, matter, rows)
	}, func() []record {
		for _, row := range rows {
			recs = append(recs, put("wall", row.ID, row))
		}
		return recs
	}, nil)
}

func (d *Durable) InsertArea(ctx context.Context, row domain.PracticeArea) error {
	return simple(ctx, d, func() error { return d.Memory.InsertArea(ctx, row) }, put("area", row.ID, row))
}
func (d *Durable) UpdateArea(ctx context.Context, row domain.PracticeArea) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateArea(ctx, row) }, put("area", row.ID, row))
}
func (d *Durable) InsertCounty(ctx context.Context, row domain.County) error {
	return simple(ctx, d, func() error { return d.Memory.InsertCounty(ctx, row) }, put("county", row.ID, row))
}
func (d *Durable) UpdateCounty(ctx context.Context, row domain.County) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateCounty(ctx, row) }, put("county", row.ID, row))
}
func (d *Durable) InsertReferral(ctx context.Context, row domain.ReferralSource) error {
	return simple(ctx, d, func() error { return d.Memory.InsertReferral(ctx, row) }, put("referral", row.ID, row))
}
func (d *Durable) UpdateReferral(ctx context.Context, row domain.ReferralSource) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateReferral(ctx, row) }, put("referral", row.ID, row))
}
func (d *Durable) InsertNote(ctx context.Context, row domain.Note) error {
	return simple(ctx, d, func() error { return d.Memory.InsertNote(ctx, row) }, put("note", row.ID, row))
}
func (d *Durable) UpdateNote(ctx context.Context, row domain.Note) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateNote(ctx, row) }, put("note", row.ID, row))
}
func (d *Durable) InsertNoteTag(ctx context.Context, row domain.NoteTag) error {
	return simple(ctx, d, func() error { return d.Memory.InsertNoteTag(ctx, row) }, put("note_tag", row.ID, row))
}
func (d *Durable) DeleteNoteTag(ctx context.Context, id domain.ID) error {
	return simple(ctx, d, func() error { return d.Memory.DeleteNoteTag(ctx, id) }, drop("note_tag", id))
}

func (d *Durable) ReplaceNoteTags(ctx context.Context, note domain.ID, rows []domain.NoteTag) error {
	var recs []record
	return d.write(ctx, func() error {
		for _, old := range d.Memory.NoteTags(note) {
			recs = append(recs, drop("note_tag", old.ID))
		}
		return d.Memory.ReplaceNoteTags(ctx, note, rows)
	}, func() []record {
		for _, row := range rows {
			recs = append(recs, put("note_tag", row.ID, row))
		}
		return recs
	}, nil)
}

func (d *Durable) InsertConflict(ctx context.Context, row domain.ConflictRecord) error {
	return simple(ctx, d, func() error { return d.Memory.InsertConflict(ctx, row) }, put("conflict", row.ID, row))
}
func (d *Durable) UpdateConflict(ctx context.Context, row domain.ConflictRecord) error {
	return simple(ctx, d, func() error { return d.Memory.UpdateConflict(ctx, row) }, put("conflict", row.ID, row))
}
func (d *Durable) InsertReview(ctx context.Context, row domain.ConflictReview) error {
	return simple(ctx, d, func() error { return d.Memory.InsertReview(ctx, row) }, put("review", row.ID, row))
}
func (d *Durable) PutInvoice(ctx context.Context, row domain.Invoice) error {
	return simple(ctx, d, func() error { return d.Memory.PutInvoice(ctx, row) }, put("invoice", row.ID, row))
}
func (d *Durable) PutTrust(ctx context.Context, row domain.TrustBalance) error {
	return simple(ctx, d, func() error { return d.Memory.PutTrust(ctx, row) }, put("trust", row.ID, row))
}
func (d *Durable) PutTime(ctx context.Context, row domain.TimeEntry) error {
	return simple(ctx, d, func() error { return d.Memory.PutTime(ctx, row) }, put("time", row.ID, row))
}

// AppendAudit stores the practice view of each row and appends the entry to audit_log
// in the same transaction, so every practice write is in the shared ledger.
func (d *Durable) AppendAudit(ctx context.Context, rows []storedAudit, events []domain.Event) error {
	recs := make([]record, 0, len(rows)+len(events))
	entries := make([]domain.AuditEntry, 0, len(rows))
	for _, row := range rows {
		recs = append(recs, put("audit", row.Entry.ID, row))
		entries = append(entries, row.Entry)
	}
	for _, ev := range events {
		recs = append(recs, put("event", ev.ID, ev))
	}
	return d.write(ctx, func() error { return d.Memory.AppendAudit(ctx, rows, events) }, func() []record { return recs }, entries)
}

func (d *Durable) PutField(ctx context.Context, row FieldDef) error {
	return simple(ctx, d, func() error { return d.Memory.PutField(ctx, row) }, put("field", row.ID, row))
}
func (d *Durable) PutValue(ctx context.Context, row FieldValue) error {
	return simple(ctx, d, func() error { return d.Memory.PutValue(ctx, row) },
		record{kind: "value", id: string(row.DefinitionID) + "|" + string(row.RecordID), body: row})
}

func (d *Durable) EnsureMarkers(ctx context.Context, labels []string, at time.Time, actor domain.ID, ids func() (domain.ID, error)) error {
	return d.write(ctx, func() error { return d.Memory.EnsureMarkers(ctx, labels, at, actor, ids) }, func() []record {
		var recs []record
		for _, label := range labels {
			if row, ok := d.Memory.MarkerByLabel(label); ok {
				recs = append(recs, put("marker", row.ID, row))
			}
		}
		return recs
	}, nil)
}

func (d *Durable) PutMatterMarker(ctx context.Context, row MatterMarker) error {
	return simple(ctx, d, func() error { return d.Memory.PutMatterMarker(ctx, row) }, put("matter_marker", row.ID, row))
}
func (d *Durable) AppendClose(ctx context.Context, row CloseRecord) error {
	return simple(ctx, d, func() error { return d.Memory.AppendClose(ctx, row) }, put("close", row.ID, row))
}
func (d *Durable) AppendRun(ctx context.Context, row ConflictRun) error {
	return simple(ctx, d, func() error { return d.Memory.AppendRun(ctx, row) }, put("run", row.ID, row))
}

func (d *Durable) SaveHit(ctx context.Context, hit Hit) error {
	return d.write(ctx, func() error { return d.Memory.SaveHit(ctx, hit) }, func() []record {
		run, _, ok := d.Memory.FindHit(hit.ID)
		if !ok {
			return nil
		}
		return []record{put("run", run.ID, run)}
	}, nil)
}

func (d *Durable) MarkChecked(ctx context.Context, party domain.ID, at time.Time) error {
	return simple(ctx, d, func() error { return d.Memory.MarkChecked(ctx, party, at) }, record{kind: "checked", id: string(party), body: at})
}

func (d *Durable) TakeMatterMarker(ctx context.Context, id domain.ID) (MatterMarker, error) {
	var out MatterMarker
	err := d.write(ctx, func() error {
		var err error
		out, err = d.Memory.TakeMatterMarker(ctx, id)
		return err
	}, func() []record { return []record{drop("matter_marker", out.ID)} }, nil)
	return out, err
}

func (d *Durable) DeleteMatterMarker(ctx context.Context, matter domain.ID, label string, area domain.ID) (MatterMarker, error) {
	var out MatterMarker
	err := d.write(ctx, func() error {
		var err error
		out, err = d.Memory.DeleteMatterMarker(ctx, matter, label, area)
		return err
	}, func() []record { return []record{drop("matter_marker", out.ID)} }, nil)
	return out, err
}

// IdemClaim keeps reservations in Memory only; a crash releases them on the next load.
func (d *Durable) IdemComplete(ctx context.Context, key string, rec idemRecord) error {
	return d.write(ctx, func() error { return d.Memory.IdemComplete(ctx, key, rec) }, func() []record {
		stored, ok := d.Memory.IdemGet(key)
		if !ok {
			return nil
		}
		return []record{{kind: "idem", id: key, body: stored}}
	}, nil)
}

func (d *Durable) IdemPut(ctx context.Context, key string, rec idemRecord) error {
	return d.IdemComplete(ctx, key, rec)
}

// Kinds reports stored row counts per kind, for restore and acceptance checks.
func (d *Durable) Kinds(ctx context.Context) (map[string]int, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT kind, count(*) FROM practice_records GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	return out, rows.Err()
}

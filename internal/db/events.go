package db

import (
	"context"
	"database/sql"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Execer runs one statement: a *sql.DB, a *sql.Tx, or audit.Conn's querier.
type Execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// PublishEvent writes ev to the shared core.events table (PRD 5, section 6 events), in
// the caller's transaction, so the module runtime's pump delivers every domain's writes
// to the modules that subscribe. A domain that keeps its own copy of its events (billing
// and practice keep documents) calls this beside that write. Writing the same event
// twice is a no-op. pool must have search_path core, as the core's pool does.
func PublishEvent(ctx context.Context, q Execer, ev domain.Event) error {
	by := ev.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	kind := ev.ActorKind
	switch kind {
	case "staff", "client", "agent", "module", "system":
	default:
		kind = "staff"
	}
	var matter any
	if !ev.MatterID.IsZero() {
		matter = string(ev.MatterID)
	}
	var visible int // client_visible is 0 or 1
	if ev.ClientVisible {
		visible++
	}
	_, err := q.ExecContext(ctx, `INSERT INTO events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (id) DO NOTHING`,
		string(ev.ID), domain.UnixMicroUTC(ev.CreatedAt), domain.UnixMicroUTC(ev.UpdatedAt), string(by), kind, string(ev.ActorID),
		ev.Action, ev.RecordType, string(ev.RecordID), matter, ev.Summary, visible)
	return err
}

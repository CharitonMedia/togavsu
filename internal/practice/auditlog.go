package practice

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// matterScoped are the practice entities whose rows belong to a matter or a contact.
// A ledger row of one of these kinds that practice did not record cannot be placed
// behind its wall, so AuditLog drops it rather than guess.
var matterScoped = map[string]bool{
	"matter": true, "matter_party": true, "note": true, "contact": true, "contact_phone": true,
	"contact_email": true, "contact_address": true, "contact_tag": true, "conflict_record": true,
	"conflict_check": true,
}

// AuditLog applies the practice read scope to shared ledger rows, in the given order.
// A row about a matter the caller cannot read (wall, role) is dropped, as is a row about
// a contact who exists only on such matters. Attorney-only values are redacted exactly as
// History redacts them. Rows from other services (settings, users, tokens) pass through.
func (s *Service) AuditLog(ctx context.Context, p domain.Principal, entries []domain.AuditEntry) []HistoryRow {
	stored := map[domain.ID]storedAudit{}
	for _, row := range s.db.Audits() {
		stored[row.Entry.ID] = row
	}
	matters := map[domain.ID]bool{}
	contacts := map[domain.ID]bool{}
	canMatter := func(id domain.ID) bool {
		ok, seen := matters[id]
		if !seen {
			ok = s.visibleMatter(ctx, p, id)
			matters[id] = ok
		}
		return ok
	}
	canContact := func(id domain.ID) bool {
		ok, seen := contacts[id]
		if !seen {
			ok = s.readableContact(ctx, p, id) == nil
			contacts[id] = ok
		}
		return ok
	}
	out := make([]HistoryRow, 0, len(entries))
	for _, e := range entries {
		row, ok := stored[e.ID]
		if !ok {
			switch {
			case e.Entity == "matter":
				row.MatterID = e.EntityID
			case e.Entity == "contact":
				row.ContactID = e.EntityID
			case matterScoped[e.Entity]:
				continue
			}
		}
		// The ledger row is authoritative for the values; practice supplies only the scope.
		row.Entry = e
		if !row.MatterID.IsZero() && !canMatter(row.MatterID) {
			continue
		}
		if !row.ContactID.IsZero() && !canContact(row.ContactID) {
			continue
		}
		out = append(out, s.historyRow(ctx, p, row))
	}
	return out
}

// HistoryItem is the published AuditEntry shape of one history or audit-log row.
func HistoryItem(row HistoryRow) map[string]any {
	return map[string]any{
		"id": row.ID, "at": row.At.UTC().Format(timeRFC3339), "actor_id": row.ActorID,
		"actor_kind": historyActorKind(row.Source, row.Role), "role": row.Role, "source": row.Source,
		"entity_type": row.Entity, "entity_id": row.EntityID, "field": row.Field,
		"old_value": historyValue(row, row.Old), "new_value": historyValue(row, row.New),
		"reason": historyValue(row, row.Reason), "kind": row.Kind,
	}
}

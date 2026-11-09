package practice

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// History returns edit history for a matter or contact, newest first.
// Attorney-only values are redacted for staff.
func (s *Service) History(ctx context.Context, p domain.Principal, entity string, id domain.ID) ([]HistoryRow, error) {
	switch entity {
	case "matter":
		// History is not a shared client item. A client read of the matter does not include it.
		if err := s.gateMatterShared(ctx, p, id, domain.ActionRead, false); err != nil {
			return nil, err
		}
	case "contact":
		if err := s.readableContact(ctx, p, id); err != nil {
			return nil, err
		}
	default:
		return nil, domain.Invalid("entity", "history is available for a matter or contact")
	}
	rows := s.db.Audits()
	var out []HistoryRow
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if entity == "matter" && row.MatterID != id && row.Entry.EntityID != id {
			continue
		}
		if entity == "contact" && row.Entry.EntityID != id {
			continue
		}
		out = append(out, s.historyRow(ctx, p, row))
	}
	return out, nil
}

// Timeline returns matter events, newest first. Walled matters are absent.
func (s *Service) Timeline(ctx context.Context, p domain.Principal, matter domain.ID) ([]TimelineEntry, error) {
	if err := s.gateMatter(ctx, p, matter, domain.ActionRead); err != nil {
		return nil, err
	}
	// A client who can read the shared matter still only sees client-visible events.
	clientView := s.gateMatterShared(ctx, p, matter, domain.ActionRead, false) != nil
	events := s.db.Events()
	var out []TimelineEntry
	for i := len(events) - 1; i >= 0; i-- {
		row := events[i]
		if row.MatterID != matter {
			continue
		}
		if clientView && !row.ClientVisible {
			continue
		}
		out = append(out, TimelineEntry{
			ID: row.ID, At: row.CreatedAt, ActorKind: row.ActorKind, ActorID: row.ActorID,
			Action: row.Action, Summary: row.Summary, ClientVisible: row.ClientVisible,
			RecordType: row.RecordType, RecordID: row.RecordID,
		})
	}
	return out, nil
}

// ActivityFeed returns the firm activity stream the caller can see.
func (s *Service) ActivityFeed(ctx context.Context, p domain.Principal, actorKindFilter string) ([]ActivityGroup, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	window := s.intSetting(ctx, "activity.window_days", 30)
	limit := s.intSetting(ctx, "activity.page_size", 100)
	cutoff := s.clock().AddDate(0, 0, -window)
	events := s.db.Events()
	var rows []TimelineEntry
	for i := len(events) - 1; i >= 0 && len(rows) < limit; i-- {
		row := events[i]
		if row.CreatedAt.Before(cutoff) {
			continue
		}
		if actorKindFilter != "" && actorKindFilter != "all" && row.ActorKind != actorKindFilter {
			continue
		}
		if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
			continue
		}
		rows = append(rows, TimelineEntry{
			ID: row.ID, At: row.CreatedAt, ActorKind: row.ActorKind, ActorID: row.ActorID,
			Action: row.Action, Summary: row.Summary, ClientVisible: row.ClientVisible,
			RecordType: row.RecordType, RecordID: row.RecordID,
		})
	}
	return groupActivity(rows, s.clock(), s.setting(ctx, "firm.time_zone", "UTC")), nil
}

func (s *Service) historyRow(ctx context.Context, p domain.Principal, row storedAudit) HistoryRow {
	out := HistoryRow{
		ID: row.Entry.ID, At: row.Entry.CreatedAt, ActorID: row.Entry.ActorID, Role: row.Entry.Role,
		Source: row.Entry.Source, Entity: row.Entry.Entity, Field: row.Entry.Field,
		Old: row.Entry.OldValue, New: row.Entry.NewValue, Kind: kindOf(row.Entry.OldValue, row.Entry.NewValue),
		Reason: row.Entry.Reason,
	}
	if row.AttorneyOnly || row.Entry.Field == "conflict_reason" || row.Entry.Field == "ssn_ein_ciphertext" {
		d, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResConflictReason, Action: domain.ActionRead, MatterID: row.MatterID})
		if err != nil || d.Redact || !d.Allow {
			out.Old = Redacted
			out.New = Redacted
			if out.Reason != "" {
				out.Reason = Redacted
			}
			out.Redacted = true
		}
	}
	return out
}

// UpdateAudit refuses to change an audit row.
func (s *Service) UpdateAudit(ctx context.Context, row domain.AuditEntry) error {
	if err := s.db.UpdateAudit(ctx, row); err != nil {
		return domain.AppendOnly("audit_log")
	}
	return nil
}

// DeleteAudit refuses to delete an audit row.
func (s *Service) DeleteAudit(ctx context.Context, id domain.ID) error {
	if err := s.db.DeleteAudit(ctx, id); err != nil {
		return domain.AppendOnly("audit_log")
	}
	return nil
}

// UpdateEvent refuses to change a timeline row.
func (s *Service) UpdateEvent(ctx context.Context, row domain.Event) error {
	if err := s.db.UpdateEvent(ctx, row); err != nil {
		return domain.AppendOnly("events")
	}
	return nil
}

// DeleteEvent refuses to delete a timeline row.
func (s *Service) DeleteEvent(ctx context.Context, id domain.ID) error {
	if err := s.db.DeleteEvent(ctx, id); err != nil {
		return domain.AppendOnly("events")
	}
	return nil
}

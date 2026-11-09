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
		if entity == "contact" && row.ContactID != id && row.Entry.EntityID != id {
			continue
		}
		if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
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
		out = append(out, s.timelineRow(ctx, row))
	}
	return out, nil
}

// ActivityFeed returns the firm activity stream the caller can see.
func (s *Service) ActivityFeed(ctx context.Context, p domain.Principal, actorKindFilter string) ([]ActivityGroup, error) {
	view, err := s.Activity(ctx, p, ActivityQuery{Tab: actorKindFilter})
	if err != nil {
		return nil, err
	}
	return view.Groups, nil
}

// Activity returns the feed with tab counts, staff chips, and a page.
// The Agents tab is API tokens and modules together.
func (s *Service) Activity(ctx context.Context, p domain.Principal, q ActivityQuery) (ActivityView, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return ActivityView{}, err
	}
	window := s.intSetting(ctx, "activity.window_days", domain.ActivityWindowDays)
	limit := s.intSetting(ctx, "activity.page_size", domain.ActivityPageSize)
	cutoff := s.clock().AddDate(0, 0, -window)
	events := s.db.Events()
	var all []TimelineEntry
	for i := len(events) - 1; i >= 0; i-- {
		row := events[i]
		if row.CreatedAt.Before(cutoff) {
			continue
		}
		if !s.feedAllows(ctx, p, row) {
			continue
		}
		all = append(all, s.timelineRow(ctx, row))
	}
	view := ActivityView{Tab: q.Tab, UserID: string(q.UserID), People: []ActivityChip{}}
	people := map[domain.ID]int{}
	for _, row := range all {
		view.All++
		switch row.ActorKind {
		case "client":
			view.Clients++
		case "staff":
			view.Staff++
			people[row.ActorID]++
		case "agent", "module":
			view.Agents++
		}
	}
	for id, count := range people {
		name := string(id)
		if user, err := s.db.User(ctx, id); err == nil && user.Name != "" {
			name = user.Name
		}
		view.People = append(view.People, ActivityChip{ID: id, Name: name, Count: count})
	}
	var rows []TimelineEntry
	for _, row := range all {
		if !activityTab(q.Tab, row.ActorKind) {
			continue
		}
		if !q.UserID.IsZero() && row.ActorID != q.UserID {
			continue
		}
		rows = append(rows, row)
	}
	if q.Cursor != "" {
		cut := 0
		for cut < len(rows) && string(rows[cut].ID) != q.Cursor {
			cut++
		}
		if cut < len(rows) {
			rows = rows[cut+1:]
		}
	}
	if len(rows) > limit {
		view.Next = string(rows[limit-1].ID)
		rows = rows[:limit]
	}
	view.Groups = groupActivity(rows, s.clock(), s.setting(ctx, "firm.time_zone", "UTC"))
	if view.Groups == nil {
		view.Groups = []ActivityGroup{}
	}
	return view, nil
}

func activityTab(tab, kind string) bool {
	switch tab {
	case "", "all":
		return true
	case "clients":
		return kind == "client"
	case "staff":
		return kind == "staff"
	case "agents":
		return kind == "agent" || kind == "module"
	default:
		return kind == tab
	}
}

func (s *Service) timelineRow(ctx context.Context, row domain.Event) TimelineEntry {
	name := row.ActorKind
	if user, err := s.db.User(ctx, row.ActorID); err == nil && user.Name != "" {
		name = user.Name
	}
	return TimelineEntry{
		ID: row.ID, At: row.CreatedAt, ActorKind: row.ActorKind, ActorID: row.ActorID,
		ActorName: name, Action: row.Action, Summary: row.Summary, ClientVisible: row.ClientVisible,
		RecordType: row.RecordType, RecordID: row.RecordID, MatterID: row.MatterID,
	}
}

// feedAllows hides a walled matter and a contact who exists only on walled matters.
func (s *Service) feedAllows(ctx context.Context, p domain.Principal, row domain.Event) bool {
	if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
		return false
	}
	contactID, aboutContact := s.eventContact(ctx, row)
	if !aboutContact {
		return true
	}
	if contactID.IsZero() || s.readableContact(ctx, p, contactID) != nil {
		return false
	}
	return true
}

func (s *Service) eventContact(ctx context.Context, row domain.Event) (domain.ID, bool) {
	switch row.RecordType {
	case "contact":
		return row.RecordID, true
	case "contact_phone":
		phone, err := s.db.Phone(ctx, row.RecordID)
		if err != nil {
			return "", true
		}
		return phone.ContactID, true
	case "contact_email":
		for _, email := range s.db.Emails(domain.ID("")) {
			if email.ID == row.RecordID {
				return email.ContactID, true
			}
		}
		return "", true
	case "contact_address":
		for _, address := range s.db.Addresses(domain.ID("")) {
			if address.ID == row.RecordID {
				return address.ContactID, true
			}
		}
		return "", true
	case "contact_tag":
		for _, tag := range s.db.Tags(domain.ID("")) {
			if tag.ID == row.RecordID {
				return tag.ContactID, true
			}
		}
		return "", true
	case "note":
		note, err := s.db.Note(ctx, row.RecordID)
		if err != nil || note.ContactID.IsZero() {
			return "", false
		}
		return note.ContactID, true
	default:
		return "", false
	}
}

func (s *Service) historyRow(ctx context.Context, p domain.Principal, row storedAudit) HistoryRow {
	name := string(row.Entry.ActorID)
	if user, err := s.db.User(ctx, row.Entry.ActorID); err == nil && user.Name != "" {
		name = user.Name
	}
	out := HistoryRow{
		ID: row.Entry.ID, At: row.Entry.CreatedAt, ActorID: row.Entry.ActorID, ActorName: name, Role: row.Entry.Role,
		Source: row.Entry.Source, Entity: row.Entry.Entity, EntityID: row.Entry.EntityID, Field: row.Entry.Field,
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

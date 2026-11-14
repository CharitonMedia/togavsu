package practice

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CreateMatter assigns the next matter number and writes the audit log.
func (s *Service) CreateMatter(ctx context.Context, p domain.Principal, in MatterInput) (_ domain.Matter, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionWrite}); err != nil {
		return domain.Matter{}, err
	}
	var replay domain.Matter
	hit, hold, err := s.idemBegin(ctx, p, in.IdempotencyKey, "create_matter", "", in, &replay)
	if err != nil {
		return domain.Matter{}, err
	}
	defer hold.abort()
	if hit {
		return replay, nil
	}
	row, err := s.prepareMatter(ctx, p, domain.Matter{}, in, true)
	if err != nil {
		return domain.Matter{}, err
	}
	format := s.setting(ctx, "firm.matter_number_format", domain.MatterNumberFormat)
	year := s.clock().Year()
	stored, err := s.db.InsertMatter(ctx, row, func(existing []string) (string, error) {
		return nextNumber(format, year, existing)
	})
	if err != nil {
		return domain.Matter{}, conflict(err)
	}
	if err := s.saveCustom(ctx, p, RecordMatter, stored.ID, stored.PracticeAreaID, in.Custom); err != nil {
		return domain.Matter{}, err
	}
	if err := s.record(ctx, p, matterChanges(domain.Matter{}, stored, "", "matter.created", "Opened matter "+stored.Number)); err != nil {
		return domain.Matter{}, err
	}
	if err := hold.finish(ctx, stored); err != nil {
		return domain.Matter{}, err
	}
	return stored, nil
}

// UpdateMatter edits fields other than number and close state.
func (s *Service) UpdateMatter(ctx context.Context, p domain.Principal, id domain.ID, in MatterInput) (_ domain.Matter, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, id, domain.ActionWrite); err != nil {
		return domain.Matter{}, err
	}
	prev, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	row, err := s.prepareMatter(ctx, p, prev, in, false)
	if err != nil {
		return domain.Matter{}, err
	}
	if err := s.db.UpdateMatter(ctx, row); err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	if err := s.saveCustom(ctx, p, RecordMatter, row.ID, row.PracticeAreaID, in.Custom); err != nil {
		return domain.Matter{}, err
	}
	if err := s.record(ctx, p, matterChanges(prev, row, "", "matter.updated", "Updated matter "+row.Number)); err != nil {
		return domain.Matter{}, err
	}
	return row, nil
}

// GetMatter returns a matter the caller can see.
func (s *Service) GetMatter(ctx context.Context, p domain.Principal, id domain.ID) (MatterView, error) {
	if err := s.gateMatter(ctx, p, id, domain.ActionRead); err != nil {
		return MatterView{}, err
	}
	row, err := s.db.Matter(ctx, id)
	if err != nil {
		return MatterView{}, missing(err, "matter")
	}
	return s.matterView(ctx, p, row)
}

// ListMatters returns matters the caller can see. Walled matters are absent.
func (s *Service) ListMatters(ctx context.Context, p domain.Principal, status string, area domain.ID) ([]MatterView, error) {
	rows, _, err := s.ListMattersPage(ctx, p, MatterQuery{Status: status, Area: area})
	return rows, err
}

// MatterQuery narrows and pages ListMattersPage.
type MatterQuery struct {
	Status string
	Area   domain.ID
	// Keep, when set, drops matters before the wall check.
	Keep func(domain.Matter) bool
	// Offset skips that many readable matters; Max returns at most that many (zero is
	// every one).
	Offset, Max int
	// Bare views carry only the matter, for lists that show no parties, fields or glance.
	Bare bool
}

// ListMattersPage lists the matters p can read in list order (open first, newest opened,
// then number), checking the wall of each matter it reaches and building views only for
// the page it returns. more reports whether readable matters follow the page.
func (s *Service) ListMattersPage(ctx context.Context, p domain.Principal, q MatterQuery) (out []MatterView, more bool, err error) {
	ctx = withSettingsMemo(ctx)
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return nil, false, err
	}
	all := s.db.Matters()
	idx := make([]int, 0, len(all))
	for i := range all {
		row := &all[i]
		if q.Status != "" && row.Status != q.Status || !q.Area.IsZero() && row.PracticeAreaID != q.Area || q.Keep != nil && !q.Keep(*row) {
			continue
		}
		idx = append(idx, i)
	}
	sort.Slice(idx, func(i, j int) bool { return matterOrder(&all[idx[i]], &all[idx[j]]) })
	skip := q.Offset
	for _, i := range idx {
		row := all[i]
		if !s.visibleMatter(ctx, p, row.ID) {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		if q.Max > 0 && len(out) == q.Max {
			return out, true, nil
		}
		view := MatterView{Matter: row}
		if !q.Bare {
			if view, err = s.matterView(ctx, p, row); err != nil {
				return nil, false, err
			}
		}
		out = append(out, view)
	}
	return out, false, nil
}

// CountMatters counts the matters ListMatters would return, without building their views.
func (s *Service) CountMatters(ctx context.Context, p domain.Principal, status string, area domain.ID) (int, error) {
	ctx = withSettingsMemo(ctx)
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return 0, err
	}
	var n int
	for _, row := range s.matterKeys() {
		if (status == "" || row.Status == status) && (area.IsZero() || row.PracticeAreaID == area) && s.visibleMatter(ctx, p, row.ID) {
			n++
		}
	}
	return n, nil
}

// matterKeys is every matter's key, through the memory store's light list when it has
// one.
func (s *Service) matterKeys() []MatterKey {
	if k, ok := s.db.(interface{ MatterKeys() []MatterKey }); ok {
		return k.MatterKeys()
	}
	rows := s.db.Matters()
	keys := make([]MatterKey, len(rows))
	for i := range rows {
		keys[i] = matterKeyOf(rows[i].ID, &rows[i])
	}
	return keys
}

// PreviewClose returns warnings and writes nothing. It is a staff close screen, not a shared client item.
func (s *Service) PreviewClose(ctx context.Context, p domain.Principal, id domain.ID) (domain.Matter, []Warning, error) {
	if err := s.gateMatterShared(ctx, p, id, domain.ActionRead, false); err != nil {
		return domain.Matter{}, nil, err
	}
	row, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, nil, missing(err, "matter")
	}
	warnings, err := s.closeWarnings(row.ID)
	if err != nil {
		return domain.Matter{}, nil, err
	}
	return row, warnings, nil
}

// CloseMatter closes a matter. Unpaid invoices and leftover trust warn and do not block.
func (s *Service) CloseMatter(ctx context.Context, p domain.Principal, id domain.ID, in CloseInput) (_ domain.Matter, _ CloseRecord, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, id, domain.ActionWrite); err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	var replay replayMatter
	hit, hold, err := s.idemBegin(ctx, p, in.IdempotencyKey, "close_matter", string(id), in, &replay)
	if err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	defer hold.abort()
	if hit {
		return replay.Matter, replay.Close, nil
	}
	prev, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, CloseRecord{}, missing(err, "matter")
	}
	if prev.Status == StatusClosed {
		return domain.Matter{}, CloseRecord{}, domain.Conflict("matter is already closed")
	}
	warnings, err := s.closeWarnings(id)
	if err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	closedOn := s.clock().Format("2006-01-02")
	next := prev
	next.Status = StatusClosed
	next.ClosedOn = closedOn
	next.CloseReason = strings.TrimSpace(in.Reason)
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateMatter(ctx, next); err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	record := CloseRecord{
		ID: meta.ID, MatterID: id, Reason: next.CloseReason, Warnings: warnings,
		Acknowledged: in.Acknowledge, PreviousStatus: prev.Status, ClosedOn: closedOn,
		At: meta.CreatedAt, ActorID: actorID(p),
	}
	if err := s.db.AppendClose(ctx, record); err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	changes := matterChanges(prev, next, in.Reason, "matter.closed", "Closed matter "+next.Number)
	if err := s.record(ctx, p, changes); err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	if err := hold.finish(ctx, replayMatter{Matter: next, Close: record}); err != nil {
		return domain.Matter{}, CloseRecord{}, err
	}
	return next, record, nil
}

type replayMatter struct {
	Matter domain.Matter
	Close  CloseRecord
}

// ReopenMatter restores the pre-close status. The close date and reason stay.
// PRD 8.12 and the OpenAPI reopen roles are admin and attorney. domain.Policy has no
// reopen action. ActionConflictDecide is the existing decision that allows those two
// roles, denies staff, and still applies the matter wall.
func (s *Service) ReopenMatter(ctx context.Context, p domain.Principal, id domain.ID, reason string) (_ domain.Matter, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{
		Resource: domain.ResMatter, Action: domain.ActionConflictDecide, MatterID: id,
	}); err != nil {
		return domain.Matter{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return domain.Matter{}, domain.Invalid("reason", "a reason is required")
	}
	prev, err := s.db.Matter(ctx, id)
	if err != nil {
		return domain.Matter{}, missing(err, "matter")
	}
	if prev.Status != StatusClosed {
		return domain.Matter{}, domain.Conflict("matter is not closed")
	}
	closeRow, ok := s.db.LatestClose(id)
	restored := StatusOpen
	if ok && closeRow.PreviousStatus != "" {
		restored = closeRow.PreviousStatus
	}
	next := prev
	next.Status = restored
	next.UpdatedAt = s.clock()
	if err := s.db.UpdateMatter(ctx, next); err != nil {
		return domain.Matter{}, err
	}
	if err := s.record(ctx, p, []change{{
		MatterID: id, Entity: "matter", EntityID: id, Field: "status",
		Old: prev.Status, New: restored, Reason: reason, Kind: "update",
		Action: "matter.reopened", Summary: "Reopened matter " + next.Number + ": " + reason,
	}}); err != nil {
		return domain.Matter{}, err
	}
	return next, nil
}

// SetWall replaces the users allowed into a matter. An empty list removes the wall. Admin only.
func (s *Service) SetWall(ctx context.Context, p domain.Principal, matter domain.ID, users []domain.ID) (err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSettings, Action: domain.ActionWrite}); err != nil {
		return err
	}
	if _, err := s.db.Matter(ctx, matter); err != nil {
		return missing(err, "matter")
	}
	rows := make([]domain.MatterWall, 0, len(users))
	seen := map[domain.ID]bool{}
	for _, user := range users {
		if user.IsZero() || seen[user] {
			continue
		}
		seen[user] = true
		if _, err := s.db.User(ctx, user); err != nil {
			return domain.Invalid("user_id", "unknown user")
		}
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		rows = append(rows, domain.MatterWall{Meta: meta, MatterID: matter, UserID: user})
	}
	if err := s.db.ReplaceWalls(ctx, matter, rows); err != nil {
		return err
	}
	return s.record(ctx, p, []change{{
		MatterID: matter, Entity: "matter", EntityID: matter, Field: "wall",
		New: fmt.Sprintf("%d", len(rows)), Kind: "update", Action: "matter.wall_set",
		Summary: "Updated the matter wall",
	}})
}

// NeedsCaseNumber lists open matters with a court or a docket-shaped name and no case number.
func (s *Service) NeedsCaseNumber(ctx context.Context, p domain.Principal) ([]domain.Matter, error) {
	patterns := s.listSetting(ctx, "practice.case_number_patterns", []string{domain.DefaultCasePattern})
	var out []domain.Matter
	for _, row := range s.db.Matters() {
		if row.Status != StatusOpen || strings.TrimSpace(row.CaseNumber) != "" {
			continue
		}
		if !s.visibleMatter(ctx, p, row.ID) {
			continue
		}
		matched := false
		if strings.TrimSpace(row.Court) != "" {
			matched = true
		}
		for _, pattern := range patterns {
			ok, err := domain.MatchCasePattern(pattern, row.Name)
			if err != nil {
				return nil, domain.Invalid("case_number_pattern", "pattern is invalid")
			}
			if ok {
				matched = true
			}
		}
		if matched {
			out = append(out, row)
		}
	}
	return out, nil
}

// PinMarker pins a firm marker. Also asked about names a practice area.
func (s *Service) PinMarker(ctx context.Context, p domain.Principal, matter domain.ID, label string, area domain.ID) (_ MatterMarker, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, matter, domain.ActionWrite); err != nil {
		return MatterMarker{}, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MatterMarker{}, err
	}
	def, ok := s.db.MarkerByLabel(label)
	if !ok {
		return MatterMarker{}, domain.Invalid("marker", "unknown marker")
	}
	return s.pinDef(ctx, p, matter, def, area)
}

// PinMarkerID pins a firm or module marker by its definition id.
func (s *Service) PinMarkerID(ctx context.Context, p domain.Principal, matter, markerID, area domain.ID) (_ MatterMarker, err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, matter, domain.ActionWrite); err != nil {
		return MatterMarker{}, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MatterMarker{}, err
	}
	def, ok := s.db.Marker(markerID)
	if !ok {
		return MatterMarker{}, domain.Invalid("marker_id", "unknown marker")
	}
	return s.pinDef(ctx, p, matter, def, area)
}

// pinDef pins def on a matter the caller may write. An inactive marker is not pinned.
func (s *Service) pinDef(ctx context.Context, p domain.Principal, matter domain.ID, def MarkerDef, area domain.ID) (MatterMarker, error) {
	if def.Inactive {
		return MatterMarker{}, domain.Invalid("marker", "this marker is inactive")
	}
	label := def.Label
	if def.ModuleID == "" && label == MarkerAlsoAsked {
		if area.IsZero() {
			return MatterMarker{}, domain.Invalid("practice_area_id", "name the practice area")
		}
		if _, err := s.db.Area(ctx, area); err != nil {
			return MatterMarker{}, domain.Invalid("practice_area_id", "unknown practice area")
		}
	} else {
		area = domain.ID("")
	}
	meta, err := s.meta(p)
	if err != nil {
		return MatterMarker{}, err
	}
	row := MatterMarker{
		ID: meta.ID, MatterID: matter, MarkerID: def.ID, Label: def.Label,
		PracticeAreaID: area, CreatedAt: meta.CreatedAt, CreatedBy: actorID(p),
	}
	if err := s.db.PutMatterMarker(ctx, row); err != nil {
		return MatterMarker{}, conflict(err)
	}
	if err := s.record(ctx, p, []change{{
		MatterID: matter, Entity: "matter", EntityID: matter, Field: "marker",
		New: label, Kind: "create", Action: "matter.marker_set", Summary: "Marked the matter " + label,
	}}); err != nil {
		return MatterMarker{}, err
	}
	return row, nil
}

// RemoveMarker takes a pinned marker off a matter.
func (s *Service) RemoveMarker(ctx context.Context, p domain.Principal, matter domain.ID, label string, area domain.ID) (err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	if err := s.gateMatter(ctx, p, matter, domain.ActionWrite); err != nil {
		return err
	}
	row, err := s.db.DeleteMatterMarker(ctx, matter, label, area)
	if err != nil {
		return missing(err, "marker")
	}
	return s.record(ctx, p, []change{{
		MatterID: matter, Entity: "matter", EntityID: matter, Field: "marker",
		Old: row.Label, Kind: "delete", Action: "matter.marker_removed", Summary: "Removed marker " + row.Label,
	}})
}

// RemoveMarkerID takes one pinned marker off by its row id.
func (s *Service) RemoveMarkerID(ctx context.Context, p domain.Principal, id domain.ID) (err error) {
	ctx, done := s.unit(ctx)
	defer func() { err = done(err) }()
	row, ok := s.findMarker(id)
	if !ok {
		return domain.NotFound("marker")
	}
	if err := s.gateMatter(ctx, p, row.MatterID, domain.ActionWrite); err != nil {
		return err
	}
	removed, err := s.db.TakeMatterMarker(ctx, id)
	if err != nil {
		return missing(err, "marker")
	}
	return s.record(ctx, p, []change{{
		MatterID: removed.MatterID, Entity: "matter", EntityID: removed.MatterID, Field: "marker",
		Old: removed.Label, Kind: "delete", Action: "matter.marker_removed", Summary: "Removed marker " + removed.Label,
	}})
}

// GetMarker returns one pinned marker the caller can see.
func (s *Service) GetMarker(ctx context.Context, p domain.Principal, id domain.ID) (MatterMarker, error) {
	row, ok := s.findMarker(id)
	if !ok {
		return MatterMarker{}, domain.NotFound("marker")
	}
	if err := s.gateMatter(ctx, p, row.MatterID, domain.ActionRead); err != nil {
		return MatterMarker{}, err
	}
	return row, nil
}

// ListMarkers returns pinned markers on matters the caller can see.
func (s *Service) ListMarkers(ctx context.Context, p domain.Principal, matter domain.ID) ([]MatterMarker, error) {
	if !matter.IsZero() {
		if err := s.gateMatter(ctx, p, matter, domain.ActionRead); err != nil {
			return nil, err
		}
		return s.db.MatterMarkers(matter), nil
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead}); err != nil {
		return nil, err
	}
	var out []MatterMarker
	for _, row := range s.db.MatterMarkers(domain.ID("")) {
		if s.visibleMatter(ctx, p, row.MatterID) {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *Service) findMarker(id domain.ID) (MatterMarker, bool) {
	for _, row := range s.db.MatterMarkers(domain.ID("")) {
		if row.ID == id {
			return row, true
		}
	}
	return MatterMarker{}, false
}

// ZeroFee reports whether the matter carries the zero-fee marker.
func (s *Service) ZeroFee(matter domain.ID) bool {
	for _, row := range s.db.MatterMarkers(matter) {
		if row.Label == MarkerZeroFee {
			return true
		}
	}
	return false
}

func (s *Service) prepareMatter(ctx context.Context, p domain.Principal, prev domain.Matter, in MatterInput, create bool) (domain.Matter, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.Matter{}, domain.Invalid("name", "name is required")
	}
	if in.PracticeAreaID.IsZero() {
		return domain.Matter{}, domain.Invalid("practice_area_id", "practice area is required")
	}
	area, err := s.db.Area(ctx, in.PracticeAreaID)
	if err != nil {
		return domain.Matter{}, domain.Invalid("practice_area_id", "unknown practice area")
	}
	if !area.Active && create {
		return domain.Matter{}, domain.Invalid("practice_area_id", "practice area is inactive")
	}
	status := in.Status
	if status == "" {
		if create {
			status = StatusOpen
		} else {
			status = prev.Status
		}
	}
	if status == StatusClosed && prev.Status != StatusClosed {
		return domain.Matter{}, domain.Invalid("status", "close the matter with a close")
	}
	if prev.Status == StatusClosed {
		status = StatusClosed
	}
	if status != StatusProspect && status != StatusOpen && status != StatusOnHold && status != StatusClosed {
		return domain.Matter{}, domain.Invalid("status", "unknown status")
	}
	if in.Stage != "" && area.Stages != "" {
		var stages []string
		if err := unmarshalList(area.Stages, &stages); err == nil && len(stages) > 0 && !containsString(stages, in.Stage) {
			return domain.Matter{}, domain.Invalid("stage", "stage is not on this practice area")
		}
	}
	billing := in.BillingType
	if billing == "" {
		billing = prev.BillingType
	}
	if billing == "" {
		billing = BillingHourly
	}
	switch billing {
	case BillingFlat, BillingHourly, BillingMixed, BillingContingency, BillingProBono:
	default:
		return domain.Matter{}, domain.Invalid("billing_type", "unknown billing type")
	}
	if !validDate(in.OpenedOn) || !validDate(in.StatuteOfLimitations) {
		return domain.Matter{}, domain.Invalid("date", "use YYYY-MM-DD")
	}
	if !in.CountyID.IsZero() {
		if _, err := s.db.County(ctx, in.CountyID); err != nil {
			return domain.Matter{}, domain.Invalid("county_id", "unknown county")
		}
	}
	if !in.ReferralSourceID.IsZero() {
		if _, err := s.db.Referral(ctx, in.ReferralSourceID); err != nil {
			return domain.Matter{}, domain.Invalid("referral_source_id", "unknown referral source")
		}
	}
	if !in.ResponsibleAttorneyID.IsZero() {
		if err := s.requireLawyer(ctx, in.ResponsibleAttorneyID); err != nil {
			return domain.Matter{}, err
		}
	}
	if !in.AssignedStaffID.IsZero() {
		if _, err := s.db.User(ctx, in.AssignedStaffID); err != nil {
			return domain.Matter{}, domain.Invalid("assigned_staff_id", "unknown user")
		}
	}
	if err := s.requireCustom(ctx, p, RecordMatter, in.PracticeAreaID, prev.ID, in.Custom); err != nil {
		return domain.Matter{}, err
	}
	meta := prev.Meta
	if create {
		meta, err = s.meta(p)
		if err != nil {
			return domain.Matter{}, err
		}
	} else {
		meta.UpdatedAt = s.clock()
	}
	opened := in.OpenedOn
	if opened == "" {
		if prev.OpenedOn != "" {
			opened = prev.OpenedOn
		} else {
			opened = s.clock().Format("2006-01-02")
		}
	}
	return domain.Matter{
		Meta: meta, Number: prev.Number, Name: name, PracticeAreaID: in.PracticeAreaID, Status: status,
		Stage: in.Stage, CaseNumber: in.CaseNumber, Court: in.Court, CountyID: in.CountyID,
		ClientPosition: in.ClientPosition, OpenedOn: opened, ClosedOn: prev.ClosedOn, CloseReason: prev.CloseReason,
		StatuteOfLimitations: in.StatuteOfLimitations, BillingType: billing, FlatFeeCents: in.FlatFeeCents,
		MatterRateCents: in.MatterRateCents, ResponsibleAttorneyID: in.ResponsibleAttorneyID,
		AssignedStaffID: in.AssignedStaffID, ReferralSourceID: in.ReferralSourceID, Archived: prev.Archived,
	}, nil
}

func (s *Service) requireLawyer(ctx context.Context, id domain.ID) error {
	user, err := s.db.User(ctx, id)
	if err != nil {
		return domain.Invalid("responsible_attorney_id", "unknown user")
	}
	roles, err := domain.ParseRoles(user.Roles)
	if err != nil {
		return domain.Invalid("responsible_attorney_id", "user has no role")
	}
	for _, role := range roles {
		if role == domain.RoleAttorney || role == domain.RoleAdmin {
			return nil
		}
	}
	return domain.Invalid("responsible_attorney_id", "responsible attorney must be an attorney or admin")
}

func (s *Service) closeWarnings(matter domain.ID) ([]Warning, error) {
	var unpaid int
	var cents domain.Cents
	for _, invoice := range s.db.Invoices() {
		if invoice.MatterID != matter || invoice.Status == "void" || invoice.Status == "paid" {
			continue
		}
		if invoice.BalanceDueCents > 0 {
			unpaid++
			sum, err := cents.Add(invoice.BalanceDueCents)
			if err != nil {
				return nil, err
			}
			cents = sum
		}
	}
	var warnings []Warning
	if unpaid > 0 {
		warnings = append(warnings, Warning{Code: WarnUnpaidInvoice, Detail: fmt.Sprintf("%d unpaid invoice balance %s", unpaid, cents)})
	}
	var trust domain.Cents
	for _, row := range s.db.TrustBalances() {
		if row.MatterID != matter {
			continue
		}
		sum, err := trust.Add(row.BalanceCents)
		if err != nil {
			return nil, err
		}
		trust = sum
	}
	if trust != 0 {
		warnings = append(warnings, Warning{Code: WarnNonzeroTrust, Detail: "trust balance " + trust.String()})
	}
	if warnings == nil {
		warnings = []Warning{}
	}
	return warnings, nil
}

func (s *Service) matterView(ctx context.Context, p domain.Principal, row domain.Matter) (MatterView, error) {
	fields, err := s.shownFields(ctx, p, RecordMatter, row.ID, row.PracticeAreaID)
	if err != nil {
		return MatterView{}, err
	}
	if err := s.ensureMarkers(ctx); err != nil {
		return MatterView{}, err
	}
	view := MatterView{
		Matter: row, Parties: s.db.Parties(row.ID, domain.ID("")), CustomFields: fields,
		Markers: s.db.MatterMarkers(row.ID), Unchecked: s.unchecked(row.ID),
	}
	if closeRow, ok := s.db.LatestClose(row.ID); ok {
		view.Close = &closeRow
	}
	view.Glance = s.glance(ctx, row)
	for _, party := range view.Parties {
		name := string(party.ContactID)
		if contact, err := s.db.Contact(ctx, party.ContactID); err == nil && contact.FullName != "" {
			name = contact.FullName
		}
		view.PartyLines = append(view.PartyLines, PartyLine{Role: party.Role, Name: name, Primary: party.Primary})
		if party.Role == "client" && party.Primary {
			view.ClientName = name
		}
	}
	return view, nil
}

func (s *Service) glance(ctx context.Context, row domain.Matter) Glance {
	g := Glance{Stage: row.Stage, Files: 0, OpenExceptions: 0}
	minutes := s.intSetting(ctx, "billing.time_increment_minutes", domain.BillingTimeIncrementMinutes)
	increments := 0
	if gl, ok := s.db.(interface {
		MatterGlance(domain.ID) (int, []domain.Cents, int, int)
	}); ok {
		var balances []domain.Cents
		g.Invoices, balances, g.TimeEntries, increments = gl.MatterGlance(row.ID)
		for _, b := range balances {
			if sum, err := g.TrustCents.Add(b); err == nil {
				g.TrustCents = sum
			}
		}
	} else {
		for _, invoice := range s.db.Invoices() {
			if invoice.MatterID == row.ID {
				g.Invoices++
			}
		}
		for _, bal := range s.db.TrustBalances() {
			if bal.MatterID != row.ID {
				continue
			}
			sum, err := g.TrustCents.Add(bal.BalanceCents)
			if err == nil {
				g.TrustCents = sum
			}
		}
		for _, entry := range s.db.TimeEntries() {
			if entry.MatterID != row.ID {
				continue
			}
			g.TimeEntries++
			increments += entry.Increments
		}
	}
	hours := float64(increments*minutes) / 60
	g.TimeHours = strconv.FormatFloat(hours, 'f', 1, 64)
	if row.OpenedOn != "" {
		opened, err := time.Parse("2006-01-02", row.OpenedOn)
		if err == nil {
			end := s.clock()
			if row.Status == StatusClosed && row.ClosedOn != "" {
				if closed, err := time.Parse("2006-01-02", row.ClosedOn); err == nil {
					end = closed
				}
			}
			days := int(end.Sub(opened).Hours() / 24)
			if days < 0 {
				days = 0
			}
			g.DaysOpen = days
		}
	}
	return g
}

func (s *Service) ensureMarkers(ctx context.Context) error {
	labels := s.listSetting(ctx, "practice.markers", domain.DefaultMarkers())
	return s.db.EnsureMarkers(ctx, labels, s.clock(), domain.SystemUserID, s.id)
}

func nextNumber(format string, year int, existing []string) (string, error) {
	if !strings.Contains(format, "YYYY") || !strings.Contains(format, "NNNN") {
		return "", domain.Invalid("matter_number_format", "format needs YYYY and NNNN")
	}
	prefix := strings.Replace(format, "YYYY", fmt.Sprintf("%04d", year), 1)
	head, tail, _ := strings.Cut(prefix, "NNNN")
	max := 0
	for _, number := range existing {
		if !strings.HasPrefix(number, head) || !strings.HasSuffix(number, tail) {
			continue
		}
		mid := strings.TrimSuffix(strings.TrimPrefix(number, head), tail)
		if len(mid) != 4 {
			continue
		}
		v, err := strconv.Atoi(mid)
		if err != nil {
			continue
		}
		if v > max {
			max = v
		}
	}
	if max >= 9999 {
		return "", domain.Conflict("matter numbers exhausted for the year")
	}
	return head + fmt.Sprintf("%04d", max+1) + tail, nil
}

func matterChanges(prev, next domain.Matter, reason, action, summary string) []change {
	type pair struct{ field, old, neu string }
	rows := []pair{
		{"number", prev.Number, next.Number},
		{"name", prev.Name, next.Name},
		{"practice_area_id", string(prev.PracticeAreaID), string(next.PracticeAreaID)},
		{"status", prev.Status, next.Status},
		{"stage", prev.Stage, next.Stage},
		{"case_number", prev.CaseNumber, next.CaseNumber},
		{"court", prev.Court, next.Court},
		{"county_id", string(prev.CountyID), string(next.CountyID)},
		{"client_position", prev.ClientPosition, next.ClientPosition},
		{"opened_on", prev.OpenedOn, next.OpenedOn},
		{"statute_of_limitations", prev.StatuteOfLimitations, next.StatuteOfLimitations},
		{"billing_type", prev.BillingType, next.BillingType},
		{"flat_fee_cents", prev.FlatFeeCents.String(), next.FlatFeeCents.String()},
		{"matter_rate_cents", prev.MatterRateCents.String(), next.MatterRateCents.String()},
		{"responsible_attorney_id", string(prev.ResponsibleAttorneyID), string(next.ResponsibleAttorneyID)},
		{"assigned_staff_id", string(prev.AssignedStaffID), string(next.AssignedStaffID)},
		{"referral_source_id", string(prev.ReferralSourceID), string(next.ReferralSourceID)},
		{"closed_on", prev.ClosedOn, next.ClosedOn},
		{"close_reason", prev.CloseReason, next.CloseReason},
	}
	var out []change
	first := true
	for _, row := range rows {
		if row.old == row.neu {
			continue
		}
		ch := change{
			MatterID: next.ID, Entity: "matter", EntityID: next.ID, Field: row.field,
			Old: row.old, New: row.neu, Reason: reason, Kind: kindOf(row.old, row.neu),
		}
		if first {
			ch.Action = action
			ch.Summary = summary
			first = false
		} else {
			ch.SkipEvent = true
		}
		out = append(out, ch)
	}
	return out
}

// sortMatters is a stable sort, as the insertion sort it replaced was; that one was
// quadratic and took over a minute on 20,000 matters.
func sortMatters(rows []MatterView) {
	sort.SliceStable(rows, func(i, j int) bool { return matterLess(rows[i], rows[j]) })
}

func matterLess(a, b MatterView) bool { return matterOrder(&a.Matter, &b.Matter) }

// matterOrder is matterRowLess on pointers, with the id last so ties are stable.
func matterOrder(a, b *domain.Matter) bool {
	if openRank(a.Status) != openRank(b.Status) {
		return openRank(a.Status) < openRank(b.Status)
	}
	if a.OpenedOn != b.OpenedOn {
		return a.OpenedOn > b.OpenedOn
	}
	if a.Number != b.Number {
		return a.Number > b.Number
	}
	return a.ID < b.ID
}

func openRank(status string) int {
	if status == StatusClosed {
		return 1
	}
	return 0
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func unmarshalList(raw string, dest *[]string) error {
	return jsonUnmarshal([]byte(raw), dest)
}

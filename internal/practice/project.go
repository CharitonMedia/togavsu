package practice

import (
	"context"
	"database/sql"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Projection keeps the section 5 tables other domains point at by foreign key
// (practice_areas, counties, contacts, referral_sources, matters) in step with
// practice_records. Files, deadlines, Doc Tender jobs and calendar events reference
// matters(id) and contacts(id); with the projection on, every practice write that
// stores one of those rows upserts its relational copy in the same transaction, so a
// committed matter is always a valid foreign key and a rolled-back one never is.
//
// practice_records stays the practice source of truth. The relational rows are a
// copy for referential integrity, written only here.
//
// ponytail: ceiling: the projection carries the columns other domains reference; a
// responsible attorney, assigned staff, county or referral source that has no row of
// its own is written as the System user or NULL. Upgrade path: practice reads and
// writes the section 5 tables directly (the Durable ponytail). Trigger: that move.
func (d *Durable) EnableProjection(ctx context.Context) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	d.project = true
	var list []record
	d.Memory.mu.Lock()
	for _, r := range d.Memory.areas {
		list = append(list, put("area", r.ID, r))
	}
	for _, r := range d.Memory.counties {
		list = append(list, put("county", r.ID, r))
	}
	for _, r := range d.Memory.contacts {
		list = append(list, put("contact", r.ID, r))
	}
	for _, r := range d.Memory.referrals {
		list = append(list, put("referral", r.ID, r))
	}
	for _, r := range d.Memory.matters {
		list = append(list, put("matter", r.ID, r))
	}
	d.Memory.mu.Unlock()
	if len(list) == 0 {
		return nil
	}
	return audit.WithTx(ctx, d.db, func(ctx context.Context) error {
		return projectRows(ctx, audit.Tx(ctx, d.db), list)
	})
}

// projectOrder writes referenced rows before the rows that reference them.
var projectOrder = map[string]int{"area": 1, "county": 2, "contact": 3, "referral": 4, "matter": 5}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func projectRows(ctx context.Context, tx execer, list []record) error {
	var rows []record
	for _, r := range list {
		if projectOrder[r.kind] > 0 && !r.del {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return projectOrder[rows[i].kind] < projectOrder[rows[j].kind] })
	for _, r := range rows {
		if err := projectOne(ctx, tx, r.body); err != nil {
			return err
		}
	}
	return nil
}

func bit(b bool) int {
	if b {
		return 1
	}
	return 0
}

func meta(m domain.Meta) []any {
	by := m.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	return []any{string(m.ID), domain.UnixMicroUTC(m.CreatedAt), domain.UnixMicroUTC(m.UpdatedAt), string(by)}
}

func optional(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return string(id)
}

func projectOne(ctx context.Context, tx execer, body any) error {
	switch r := body.(type) {
	case domain.PracticeArea:
		_, err := tx.ExecContext(ctx, `INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, name = EXCLUDED.name,
matter_types = EXCLUDED.matter_types, stages = EXCLUDED.stages, active = EXCLUDED.active`,
			append(meta(r.Meta), r.Name, orJSON(r.MatterTypes), orJSON(r.Stages), bit(r.Active))...)
		return err
	case domain.County:
		_, err := tx.ExecContext(ctx, `INSERT INTO counties (id, created_at, updated_at, created_by, name, state)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, name = EXCLUDED.name, state = EXCLUDED.state`,
			append(meta(r.Meta), r.Name, r.State)...)
		return err
	case domain.Contact:
		kind := r.Kind
		if kind != "organization" {
			kind = "person"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO contacts (id, created_at, updated_at, created_by, kind, given_name, middle_name, family_name,
prefix, suffix, full_name, preferred_name, also_known_as, date_of_birth, date_of_death, ssn_ein_ciphertext, entity_type, formation_state,
formation_date, archived, source)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21) ON CONFLICT (id) DO UPDATE SET
updated_at = EXCLUDED.updated_at, kind = EXCLUDED.kind, given_name = EXCLUDED.given_name, middle_name = EXCLUDED.middle_name,
family_name = EXCLUDED.family_name, prefix = EXCLUDED.prefix, suffix = EXCLUDED.suffix, full_name = EXCLUDED.full_name,
preferred_name = EXCLUDED.preferred_name, also_known_as = EXCLUDED.also_known_as, date_of_birth = EXCLUDED.date_of_birth,
date_of_death = EXCLUDED.date_of_death, ssn_ein_ciphertext = EXCLUDED.ssn_ein_ciphertext, entity_type = EXCLUDED.entity_type,
formation_state = EXCLUDED.formation_state, formation_date = EXCLUDED.formation_date, archived = EXCLUDED.archived, source = EXCLUDED.source`,
			append(meta(r.Meta), kind, r.GivenName, r.MiddleName, r.FamilyName, r.Prefix, r.Suffix, r.FullName, r.PreferredName,
				r.AlsoKnownAs, r.DateOfBirth, r.DateOfDeath, r.SSNEINCiphertext, r.EntityType, r.FormationState, r.FormationDate,
				bit(r.Archived), r.Source)...)
		return err
	case domain.ReferralSource:
		_, err := tx.ExecContext(ctx, `INSERT INTO referral_sources (id, created_at, updated_at, created_by, name, kind, contact_id, active)
VALUES ($1,$2,$3,$4,$5,$6,(SELECT id FROM contacts WHERE id = $7),$8) ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at,
name = EXCLUDED.name, kind = EXCLUDED.kind, contact_id = EXCLUDED.contact_id, active = EXCLUDED.active`,
			append(meta(r.Meta), r.Name, r.Kind, optional(r.ContactID), bit(r.Active))...)
		return err
	case domain.Matter:
		_, err := tx.ExecContext(ctx, `INSERT INTO matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage,
case_number, court, county_id, client_position, opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents,
matter_rate_cents, responsible_attorney_id, assigned_staff_id, referral_source_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,(SELECT id FROM counties WHERE id = $12),$13,$14,$15,$16,$17,$18,$19,$20,
COALESCE((SELECT id FROM users WHERE id = $21), $22),(SELECT id FROM users WHERE id = $23),(SELECT id FROM referral_sources WHERE id = $24))
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, number = EXCLUDED.number, name = EXCLUDED.name,
practice_area_id = EXCLUDED.practice_area_id, status = EXCLUDED.status, stage = EXCLUDED.stage, case_number = EXCLUDED.case_number,
court = EXCLUDED.court, county_id = EXCLUDED.county_id, client_position = EXCLUDED.client_position, opened_on = EXCLUDED.opened_on,
closed_on = EXCLUDED.closed_on, close_reason = EXCLUDED.close_reason, statute_of_limitations = EXCLUDED.statute_of_limitations,
billing_type = EXCLUDED.billing_type, flat_fee_cents = EXCLUDED.flat_fee_cents, matter_rate_cents = EXCLUDED.matter_rate_cents,
responsible_attorney_id = EXCLUDED.responsible_attorney_id, assigned_staff_id = EXCLUDED.assigned_staff_id,
referral_source_id = EXCLUDED.referral_source_id`,
			append(meta(r.Meta), r.Number, r.Name, string(r.PracticeAreaID), r.Status, r.Stage, r.CaseNumber, r.Court, optional(r.CountyID),
				r.ClientPosition, r.OpenedOn, r.ClosedOn, r.CloseReason, r.StatuteOfLimitations, r.BillingType, int64(r.FlatFeeCents),
				int64(r.MatterRateCents), optional(r.ResponsibleAttorneyID), string(domain.SystemUserID), optional(r.AssignedStaffID),
				optional(r.ReferralSourceID))...)
		return err
	}
	return nil
}

func orJSON(s string) string {
	if s == "" {
		return "[]"
	}
	return s
}

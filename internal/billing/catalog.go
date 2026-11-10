package billing

import (
	"context"
	"errors"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Fee items, activity types and expense types are firm lists the admin keeps (PRD 8.15, 5).
// Reads need settings read (admin, attorney, staff); writes need settings write (admin only).

func (s *Service) settingsRead(ctx context.Context, p domain.Principal) error {
	return s.allow(ctx, p, domain.ResSettings, domain.ActionRead, "")
}

func (s *Service) settingsWrite(ctx context.Context, p domain.Principal) error {
	return s.allow(ctx, p, domain.ResSettings, domain.ActionWrite, "")
}

// FeeItemInput creates or patches a fee item. Nil fields stay as they are on a patch.
type FeeItemInput struct {
	Name               *string
	Description        *string
	DefaultAmountCents *domain.Cents
	PracticeAreaID     *domain.ID
	Active             *bool
}

// FeeItems lists the catalog. active filters when non-nil.
func (s *Service) FeeItems(ctx context.Context, p domain.Principal, active *bool) ([]FeeItem, error) {
	if err := s.settingsRead(ctx, p); err != nil {
		return nil, err
	}
	rows, err := listOf[FeeItem](ctx, s.d.Store, kindFeeItem)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if active == nil || r.Active == *active {
			out = append(out, r)
		}
	}
	return out, nil
}

// FeeItem returns one catalog item.
func (s *Service) FeeItem(ctx context.Context, p domain.Principal, id domain.ID) (FeeItem, error) {
	if err := s.settingsRead(ctx, p); err != nil {
		return FeeItem{}, err
	}
	return s.feeItem(ctx, id)
}

func (s *Service) feeItem(ctx context.Context, id domain.ID) (FeeItem, error) {
	var row FeeItem
	if err := s.d.Store.Get(ctx, kindFeeItem, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return FeeItem{}, domain.NotFound("fee_item")
		}
		return FeeItem{}, err
	}
	return row, nil
}

// CreateFeeItem adds a catalog item. Admin only.
func (s *Service) CreateFeeItem(ctx context.Context, p domain.Principal, in FeeItemInput) (FeeItem, error) {
	if err := s.settingsWrite(ctx, p); err != nil {
		return FeeItem{}, err
	}
	row := FeeItem{Active: true}
	if in.Name == nil || in.DefaultAmountCents == nil {
		return FeeItem{}, domain.Invalid("name", "name and default_amount_cents are required")
	}
	if err := applyFeeItem(&row, in); err != nil {
		return FeeItem{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return FeeItem{}, err
	}
	row.Meta = meta
	err = s.write(ctx, p, func(t *txn) error {
		if err := t.put(kindFeeItem, row.ID, row); err != nil {
			return err
		}
		if err := t.created("fee_item", row.ID, row, ""); err != nil {
			return err
		}
		return t.event("setting.updated", "fee_item", row.ID, "", "Fee item "+row.Name+" added", false)
	})
	return row, err
}

// UpdateFeeItem patches a catalog item. A deactivated item stays on past invoices.
func (s *Service) UpdateFeeItem(ctx context.Context, p domain.Principal, id domain.ID, in FeeItemInput) (FeeItem, error) {
	if err := s.settingsWrite(ctx, p); err != nil {
		return FeeItem{}, err
	}
	var out FeeItem
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.feeItem(t.ctx, id)
		if err != nil {
			return err
		}
		after := before
		if err := applyFeeItem(&after, in); err != nil {
			return err
		}
		after.UpdatedAt = s.now()
		if err := t.put(kindFeeItem, id, after); err != nil {
			return err
		}
		if err := t.diff("fee_item", id, before, after, ""); err != nil {
			return err
		}
		out = after
		return t.event("setting.updated", "fee_item", id, "", "Fee item "+after.Name+" updated", false)
	})
	return out, err
}

func applyFeeItem(row *FeeItem, in FeeItemInput) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return domain.Invalid("name", "is required")
		}
		row.Name = name
	}
	if in.Description != nil {
		row.Description = *in.Description
	}
	if in.DefaultAmountCents != nil {
		if negative(int64(*in.DefaultAmountCents)) {
			return domain.Invalid("default_amount_cents", "must be zero or greater")
		}
		row.DefaultAmountCents = *in.DefaultAmountCents
	}
	if in.PracticeAreaID != nil {
		row.PracticeAreaID = *in.PracticeAreaID
	}
	if in.Active != nil {
		row.Active = *in.Active
	}
	return nil
}

// TypeInput creates or patches an activity or expense type.
type TypeInput struct {
	Name   *string
	Active *bool
}

func typeKind(expense bool) (string, string) {
	if expense {
		return kindExpenseType, "expense_type"
	}
	return kindActivity, "activity_type"
}

// Types lists activity types, or expense types when expense is true.
func (s *Service) Types(ctx context.Context, p domain.Principal, expense bool, active *bool) ([]NamedType, error) {
	if err := s.settingsRead(ctx, p); err != nil {
		return nil, err
	}
	kind, _ := typeKind(expense)
	rows, err := listOf[NamedType](ctx, s.d.Store, kind)
	if err != nil {
		return nil, err
	}
	out := rows[:0]
	for _, r := range rows {
		if active == nil || r.Active == *active {
			out = append(out, r)
		}
	}
	return out, nil
}

// Type returns one activity or expense type.
func (s *Service) Type(ctx context.Context, p domain.Principal, expense bool, id domain.ID) (NamedType, error) {
	if err := s.settingsRead(ctx, p); err != nil {
		return NamedType{}, err
	}
	return s.namedType(ctx, expense, id)
}

func (s *Service) namedType(ctx context.Context, expense bool, id domain.ID) (NamedType, error) {
	kind, entity := typeKind(expense)
	var row NamedType
	if err := s.d.Store.Get(ctx, kind, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return NamedType{}, domain.NotFound(entity)
		}
		return NamedType{}, err
	}
	return row, nil
}

// CreateType adds an activity or expense type. Admin only.
func (s *Service) CreateType(ctx context.Context, p domain.Principal, expense bool, in TypeInput) (NamedType, error) {
	if err := s.settingsWrite(ctx, p); err != nil {
		return NamedType{}, err
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return NamedType{}, domain.Invalid("name", "is required")
	}
	meta, err := s.meta(p)
	if err != nil {
		return NamedType{}, err
	}
	row := NamedType{Meta: meta, Name: strings.TrimSpace(*in.Name), Active: in.Active == nil || *in.Active}
	kind, entity := typeKind(expense)
	err = s.write(ctx, p, func(t *txn) error {
		if err := t.put(kind, row.ID, row); err != nil {
			return err
		}
		if err := t.created(entity, row.ID, row, ""); err != nil {
			return err
		}
		return t.event("setting.updated", entity, row.ID, "", entity+" "+row.Name+" added", false)
	})
	return row, err
}

// UpdateType patches an activity or expense type.
func (s *Service) UpdateType(ctx context.Context, p domain.Principal, expense bool, id domain.ID, in TypeInput) (NamedType, error) {
	if err := s.settingsWrite(ctx, p); err != nil {
		return NamedType{}, err
	}
	kind, entity := typeKind(expense)
	var out NamedType
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.namedType(t.ctx, expense, id)
		if err != nil {
			return err
		}
		after := before
		if in.Name != nil {
			if strings.TrimSpace(*in.Name) == "" {
				return domain.Invalid("name", "is required")
			}
			after.Name = strings.TrimSpace(*in.Name)
		}
		if in.Active != nil {
			after.Active = *in.Active
		}
		after.UpdatedAt = s.now()
		if err := t.put(kind, id, after); err != nil {
			return err
		}
		out = after
		if err := t.diff(entity, id, before, after, ""); err != nil {
			return err
		}
		return t.event("setting.updated", entity, id, "", entity+" "+after.Name+" updated", false)
	})
	return out, err
}

// activeType checks that a time entry or expense names an active type.
func (s *Service) activeType(ctx context.Context, expense bool, id domain.ID, field string) error {
	if id.IsZero() {
		return domain.Invalid(field, "is required")
	}
	row, err := s.namedType(ctx, expense, id)
	if err != nil {
		return domain.Invalid(field, "unknown type")
	}
	if !row.Active {
		return domain.Invalid(field, "type is not active")
	}
	return nil
}

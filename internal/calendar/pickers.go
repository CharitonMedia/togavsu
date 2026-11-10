package calendar

import (
	"context"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// MatterChoice is one matter offered by the available-dates matter picker.
type MatterChoice struct {
	ID    domain.ID `json:"matter_id"`
	Label string    `json:"label"`
}

// AttorneyChoice is one attorney offered by the available-dates attorney picker.
type AttorneyChoice struct {
	ID   domain.ID `json:"user_id"`
	Name string    `json:"name"`
}

// UserLister lists firm users. identity's user repository satisfies it; when Deps.Users
// also implements it, the available-dates page offers an attorney picker.
type UserLister interface {
	List(ctx context.Context, filter repository.Filter) (repository.Page[domain.User], error)
}

// SearchMatters finds matters for the available-dates picker (PRD 8.16, "picked by
// search"). It goes through practice search as the caller, then rechecks each hit
// against the calendar gate, so a walled matter never appears.
func (s *Service) SearchMatters(ctx context.Context, p domain.Principal, query string) ([]MatterChoice, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	hits, err := s.d.Matters.Search(ctx, p, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	out := make([]MatterChoice, 0, len(hits))
	for _, h := range hits {
		if s.visible(ctx, p, h.ID) {
			out = append(out, h)
		}
	}
	return out, nil
}

// PickedMatter labels the chosen matter for the picker. A walled or missing matter
// is not_found.
func (s *Service) PickedMatter(ctx context.Context, p domain.Principal, id domain.ID) (MatterChoice, error) {
	if err := s.gate(ctx, p, domain.ActionRead, id); err != nil {
		return MatterChoice{}, err
	}
	m, err := s.d.Matters.Matter(ctx, p, id)
	if err != nil {
		return MatterChoice{}, err
	}
	return MatterChoice{ID: m.ID, Label: strings.TrimSpace(m.Number + " " + m.Name)}, nil
}

// Attorneys lists active attorneys by name for the attorney picker. ok is false when
// the user reader cannot list users, and the page falls back to a user id field.
func (s *Service) Attorneys(ctx context.Context, p domain.Principal) (out []AttorneyChoice, ok bool, err error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, false, err
	}
	lister, ok := s.d.Users.(UserLister)
	if !ok {
		return nil, false, nil
	}
	f := repository.Filter{}
	for {
		page, err := lister.List(ctx, f)
		if err != nil {
			return nil, false, err
		}
		for _, u := range page.Items {
			if !u.Active {
				continue
			}
			roles, err := domain.ParseRoles(u.Roles)
			if err != nil {
				continue
			}
			if (domain.Principal{Roles: roles}).HasRole(domain.RoleAttorney) {
				out = append(out, AttorneyChoice{ID: u.ID, Name: u.Name})
			}
		}
		if page.NextCursor == "" || page.NextCursor == f.Cursor {
			break
		}
		f.Cursor = page.NextCursor
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, true, nil
}

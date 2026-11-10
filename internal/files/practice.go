package files

import (
	"context"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// PracticeDirectory reads matters and client contacts from the practice store. Pair
// it with practice.Service.Policy.Walls so file access uses the same matter walls as
// every practice screen.
type PracticeDirectory struct {
	Store practice.Store
}

// Matter implements Directory.
func (d PracticeDirectory) Matter(ctx context.Context, id domain.ID) (Matter, error) {
	m, err := d.Store.Matter(ctx, id)
	if err != nil {
		var api *domain.APIError
		if errors.Is(err, repository.ErrNotFound) || (errors.As(err, &api) && api.Code == domain.CodeNotFound) {
			return Matter{}, repository.ErrNotFound
		}
		return Matter{}, err
	}
	return Matter{ID: m.ID, Number: m.Number, Status: m.Status, PracticeAreaID: m.PracticeAreaID}, nil
}

// OpenMatters implements Directory. Prospect, open and on-hold matters get drop
// folder subfolders; closed ones do not.
func (d PracticeDirectory) OpenMatters(context.Context) ([]Matter, error) {
	var out []Matter
	for _, m := range d.Store.Matters() {
		if m.Status != "closed" {
			out = append(out, Matter{ID: m.ID, Number: m.Number, Status: m.Status, PracticeAreaID: m.PracticeAreaID})
		}
	}
	return out, nil
}

// UserContact implements Directory.
func (d PracticeDirectory) UserContact(ctx context.Context, user domain.ID) (domain.ID, error) {
	u, err := d.Store.User(ctx, user)
	if err != nil {
		return "", repository.ErrNotFound
	}
	return u.ContactID, nil
}

package access

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Reader loads matter walls and whether a client user is a party on the matter.
type Reader struct {
	Walls   repository.MatterWalls
	Parties repository.MatterParties
	Users   repository.Users
}

// View implements domain.WallReader. A matter with any wall row is walled.
// An empty wall list is open to every role the policy allows.
func (r Reader) View(ctx context.Context, p domain.Principal, matterID domain.ID) (domain.WallView, error) {
	if r.Walls == nil {
		return domain.WallView{}, domain.ErrWallReader
	}
	walls, err := r.Walls.List(ctx, repository.Filter{MatterID: matterID})
	if err != nil {
		return domain.WallView{}, err
	}
	view := domain.WallView{}
	for _, wall := range walls.Items {
		if wall.MatterID != matterID {
			continue
		}
		view.Walled = true
		if wall.UserID == p.UserID {
			view.Allows = true
		}
	}
	view.ClientMatter, err = r.clientMatter(ctx, p, matterID)
	if err != nil {
		return domain.WallView{}, err
	}
	return view, nil
}

func (r Reader) clientMatter(ctx context.Context, p domain.Principal, matterID domain.ID) (bool, error) {
	if r.Users == nil || r.Parties == nil || p.UserID.IsZero() || !p.HasRole(domain.RoleClient) {
		return false, nil
	}
	user, err := r.Users.Get(ctx, p.UserID)
	if err != nil {
		return false, err
	}
	if user.ContactID.IsZero() {
		return false, nil
	}
	parties, err := r.Parties.List(ctx, repository.Filter{MatterID: matterID})
	if err != nil {
		return false, err
	}
	for _, party := range parties.Items {
		if party.MatterID == matterID && party.ContactID == user.ContactID && party.Role == "client" {
			return true, nil
		}
	}
	return false, nil
}

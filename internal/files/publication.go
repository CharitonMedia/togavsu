package files

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func publicationStatus(status string) error {
	switch status {
	case "", "draft", "final", "executed", "filed", "received":
		return nil
	default:
		return domain.Invalid("status", "status is draft, final, executed, filed or received")
	}
}

// publicationAuthor checks the named person without substituting that person for
// the module principal. The module's independent wall and file checks still apply.
func (s *Service) publicationAuthor(ctx context.Context, p domain.Principal, id, matter, contact domain.ID, attorneyOnly bool) (domain.ID, error) {
	if id == "" {
		return "", nil
	}
	invalid := domain.Invalid("on_behalf_of_user_id", "author must be an active firm user with access to this publication")
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return "", invalid
	}
	directory, ok := s.dir.(interface {
		PublicationUser(context.Context, domain.ID) (domain.User, error)
	})
	if !ok {
		return "", invalid
	}
	user, err := directory.PublicationUser(ctx, id)
	if err != nil || !user.Active {
		return "", invalid
	}
	roles, err := domain.ParseRoles(user.Roles)
	if err != nil {
		return "", invalid
	}
	author := domain.Principal{UserID: user.ID, Roles: roles, Source: domain.SourceAPIToken}
	if !(author.HasRole(domain.RoleAdmin) || author.HasRole(domain.RoleAttorney) || author.HasRole(domain.RoleStaff)) {
		return "", invalid
	}
	if err := s.canWriteTo(ctx, author, matter, contact, attorneyOnly); err != nil {
		return "", invalid
	}
	return id, nil
}

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
func (s *Service) publicationAuthor(ctx context.Context, p domain.Principal, id, matter domain.ID) (domain.ID, error) {
	if id == "" {
		return "", nil
	}
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return "", domain.Invalid("on_behalf_of_user_id", "only a module may name a publication author")
	}
	directory, ok := s.dir.(interface {
		PublicationUser(context.Context, domain.ID) (domain.User, error)
	})
	if !ok {
		return "", domain.Invalid("on_behalf_of_user_id", "author cannot be verified")
	}
	user, err := directory.PublicationUser(ctx, id)
	if err != nil || !user.Active {
		return "", domain.Invalid("on_behalf_of_user_id", "author must be an active firm user with access to the matter")
	}
	roles, err := domain.ParseRoles(user.Roles)
	if err != nil {
		return "", domain.Invalid("on_behalf_of_user_id", "author must have active firm roles")
	}
	author := domain.Principal{UserID: user.ID, Roles: roles, Source: domain.SourceAPIToken}
	if !(author.HasRole(domain.RoleAdmin) || author.HasRole(domain.RoleAttorney) || author.HasRole(domain.RoleStaff)) {
		return "", domain.Invalid("on_behalf_of_user_id", "author must be an active firm user with access to the matter")
	}
	if err := s.canWriteTo(ctx, author, matter, "", false); err != nil {
		return "", domain.Invalid("on_behalf_of_user_id", "author must have access to the matter")
	}
	return id, nil
}

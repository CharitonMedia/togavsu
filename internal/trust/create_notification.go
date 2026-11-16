package trust

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// NotificationWrite is createNotification's body (CORE-03).
type NotificationWrite struct {
	Kind           string    `json:"kind"`
	Title          string    `json:"title"`
	Link           string    `json:"link"`
	MatterID       domain.ID `json:"matter_id"`
	UserID         domain.ID `json:"user_id"`
	Audience       string    `json:"audience"`
	RecipientRoles []string  `json:"recipient_roles"`
}

// CreateNotificationResult is the createNotification response.
type CreateNotificationResult struct {
	Created         int         `json:"created"`
	NotificationIDs []domain.ID `json:"notification_ids"`
}

// DeclaredModuleKind reports whether fullKind ({module_id}.{short}) is declared for
// the calling module. Nil skips the check (non-module callers).
type DeclaredModuleKind func(ctx context.Context, moduleID, fullKind string) (bool, error)

// NotificationEmailWanted reports whether user wants email for kind. Nil skips email.
type NotificationEmailWanted func(ctx context.Context, user domain.ID, kind string) (bool, error)

// CreateNotification resolves recipients, drops wall-excluded users, creates one row
// per recipient and emails when the preference has email on (CORE-03).
func (s *Service) CreateNotification(ctx context.Context, who domain.Principal, in NotificationWrite) (CreateNotificationResult, error) {
	if who.Source != domain.SourceModule && who.Source != domain.SourceService && !who.HasRole(domain.RoleAdmin) {
		return CreateNotificationResult{}, domain.Forbidden("only a module or an admin sends notifications")
	}
	kind := strings.TrimSpace(in.Kind)
	title := strings.TrimSpace(in.Title)
	link := strings.TrimSpace(in.Link)
	switch {
	case kind == "" || strings.ContainsAny(kind, " \r\n"):
		return CreateNotificationResult{}, domain.Invalid("kind", "kind is required")
	case title == "" || utf8.RuneCountInString(title) > domain.NotificationTitleMaxChars || strings.ContainsAny(title, "\r\n"):
		return CreateNotificationResult{}, domain.Invalid("title", "title is 1 to 200 characters on one line")
	case !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") || strings.ContainsAny(link, "\\\r\n"):
		return CreateNotificationResult{}, domain.Invalid("link", "link is a path on this site")
	}
	moduleCall := who.ModuleID != "" && (who.Source == domain.SourceModule || who.Source == domain.SourceService)
	if moduleCall {
		prefixModules := "/modules/" + who.ModuleID + "/"
		prefixPortal := "/portal/" + who.ModuleID + "/"
		if !strings.HasPrefix(link, prefixModules) && !strings.HasPrefix(link, prefixPortal) {
			return CreateNotificationResult{}, domain.Invalid("link", "link must start with "+prefixModules+" or "+prefixPortal)
		}
		if s.DeclaredKind != nil {
			ok, err := s.DeclaredKind(ctx, who.ModuleID, kind)
			if err != nil {
				return CreateNotificationResult{}, err
			}
			if !ok {
				return CreateNotificationResult{}, domain.Invalid("kind", "kind is not declared by this module")
			}
		}
	}
	hasUser := !in.UserID.IsZero()
	hasAudience := strings.TrimSpace(in.Audience) != ""
	hasRoles := len(in.RecipientRoles) > 0
	n := 0
	if hasUser {
		n++
	}
	if hasAudience {
		n++
	}
	if hasRoles {
		n++
	}
	if n != 1 {
		return CreateNotificationResult{}, domain.Invalid("recipient", "exactly one of user_id, audience or recipient_roles is required")
	}
	serviceCall := who.Source == domain.SourceService && who.UserID.IsZero() && who.ModuleID != ""
	if hasRoles && !serviceCall {
		return CreateNotificationResult{}, domain.Forbidden("recipient_roles is only for a service call")
	}
	if !in.MatterID.IsZero() && !who.UserID.IsZero() {
		if err := s.allow(ctx, who, domain.ResMatter, domain.ActionRead, in.MatterID); err != nil {
			return CreateNotificationResult{}, err
		}
	}
	recipients, err := s.resolveNotificationRecipients(ctx, in)
	if err != nil {
		return CreateNotificationResult{}, err
	}
	var ids []domain.ID
	_, err = s.tx(ctx, func(ctx context.Context) error {
		for _, u := range recipients {
			id, err := s.newID()
			if err != nil {
				return err
			}
			now := s.now()
			var matter any
			if !in.MatterID.IsZero() {
				matter = string(in.MatterID)
			}
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO notifications (id, created_at, updated_at, created_by, user_id, kind, title, link, matter_id, read_at) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,0)`,
				string(id), micro(now), string(actorID(who)), string(u.ID), kind, title, link, matter); err != nil {
				return err
			}
			if err := s.record(ctx, who, "notification", id, "title", "", title, "notification.created"); err != nil {
				return err
			}
			ids = append(ids, id)
			if s.EmailWanted != nil {
				want, err := s.EmailWanted(ctx, u.ID, kind)
				if err != nil {
					return err
				}
				if want && strings.Contains(u.Email, "@") {
					body := title + "\n\n" + link + "\n"
					if err := s.queueMail(ctx, who, kind, id, u.Email, title, body); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return CreateNotificationResult{}, err
	}
	return CreateNotificationResult{Created: len(ids), NotificationIDs: ids}, nil
}

func (s *Service) resolveNotificationRecipients(ctx context.Context, in NotificationWrite) ([]domain.User, error) {
	if !in.UserID.IsZero() {
		u, ok, err := s.activeUser(ctx, in.UserID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, domain.Invalid("user_id", "the recipient is not an active user")
		}
		if !in.MatterID.IsZero() {
			sees, err := s.matterVisible(ctx, u, in.MatterID)
			if err != nil {
				return nil, err
			}
			if !sees {
				return nil, domain.Invalid("user_id", "the recipient cannot read the matter")
			}
		}
		return []domain.User{u}, nil
	}
	if len(in.RecipientRoles) > 0 {
		want := map[string]bool{}
		for i, r := range in.RecipientRoles {
			switch r {
			case "admin", "accountant":
				if want[r] {
					return nil, domain.Invalid(fmt.Sprintf("recipient_roles[%d]", i), "roles are unique")
				}
				want[r] = true
			default:
				return nil, domain.Invalid(fmt.Sprintf("recipient_roles[%d]", i), "recipient_roles is a non-empty subset of admin and accountant")
			}
		}
		if len(want) == 0 {
			return nil, domain.Invalid("recipient_roles", "recipient_roles is a non-empty subset of admin and accountant")
		}
		return s.usersWithRoles(ctx, want, in.MatterID)
	}
	audience := strings.TrimSpace(in.Audience)
	switch audience {
	case "admins":
		return s.usersWithRoles(ctx, map[string]bool{"admin": true}, in.MatterID)
	case "responsible_attorney", "matter_team", "admins_and_responsible_attorney":
		if in.MatterID.IsZero() {
			return nil, domain.Invalid("matter_id", "matter_id is required for this audience")
		}
	default:
		return nil, domain.Invalid("audience", "audience is responsible_attorney, matter_team, admins or admins_and_responsible_attorney")
	}
	matter, err := s.practice.Matter(ctx, in.MatterID)
	if err != nil {
		return nil, notFound(err, "matter")
	}
	seen := map[domain.ID]bool{}
	var out []domain.User
	add := func(id domain.ID) error {
		if id.IsZero() || seen[id] {
			return nil
		}
		u, ok, err := s.activeUser(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		sees, err := s.matterVisible(ctx, u, in.MatterID)
		if err != nil {
			return err
		}
		if !sees {
			return nil
		}
		seen[id] = true
		out = append(out, u)
		return nil
	}
	switch audience {
	case "responsible_attorney":
		if err := add(matter.ResponsibleAttorneyID); err != nil {
			return nil, err
		}
	case "matter_team":
		if err := add(matter.ResponsibleAttorneyID); err != nil {
			return nil, err
		}
		if err := add(matter.AssignedStaffID); err != nil {
			return nil, err
		}
	case "admins_and_responsible_attorney":
		admins, err := s.usersWithRoles(ctx, map[string]bool{"admin": true}, in.MatterID)
		if err != nil {
			return nil, err
		}
		for _, u := range admins {
			seen[u.ID] = true
			out = append(out, u)
		}
		if err := add(matter.ResponsibleAttorneyID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) usersWithRoles(ctx context.Context, want map[string]bool, matter domain.ID) ([]domain.User, error) {
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	var out []domain.User
	for _, u := range page.Items {
		if !u.Active {
			continue
		}
		roles, err := domain.ParseRoles(u.Roles)
		if err != nil {
			continue
		}
		p := domain.Principal{Roles: roles}
		match := false
		if want["admin"] && p.HasRole(domain.RoleAdmin) {
			match = true
		}
		if want["accountant"] && p.HasRole(domain.RoleAccountant) {
			match = true
		}
		if !match {
			continue
		}
		if !matter.IsZero() {
			sees, err := s.matterVisible(ctx, u, matter)
			if err != nil {
				return nil, err
			}
			if !sees {
				continue
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// matterVisible is whether the matter wall lets u read the matter (admins included).
func (s *Service) matterVisible(ctx context.Context, u domain.User, matter domain.ID) (bool, error) {
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil {
		return false, nil
	}
	view, err := s.policy.Walls.View(ctx, domain.Principal{UserID: u.ID, Roles: roles}, matter)
	if err != nil {
		return false, err
	}
	if !view.Walled {
		return true, nil
	}
	return view.Allows, nil
}

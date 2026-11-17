package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// AssertReadableClientParty returns nil when contact is a client party on a matter
// the caller can read (CORE-14). Otherwise not_found.
func (s *Service) AssertReadableClientParty(ctx context.Context, p domain.Principal, contact domain.ID) error {
	if contact.IsZero() {
		return domain.NotFound("contact")
	}
	if _, err := s.db.Contact(ctx, contact); err != nil {
		return missing(err, "contact")
	}
	for _, party := range s.db.Parties(domain.ID(""), contact) {
		if party.Role != "client" {
			continue
		}
		if s.visibleMatter(ctx, p, party.MatterID) {
			return nil
		}
	}
	return domain.NotFound("contact")
}

// PrimaryContactEmail is the contact's current primary email, or empty.
func (s *Service) PrimaryContactEmail(contact domain.ID) string {
	today := s.clock().Format("2006-01-02")
	if e := currentPrimaryEmailRow(s.db.Emails(contact), today); e != nil {
		return e.Email
	}
	return ""
}

// ContactDisplayName is the contact's full name for a new portal user.
func (s *Service) ContactDisplayName(ctx context.Context, contact domain.ID) (string, error) {
	row, err := s.db.Contact(ctx, contact)
	if err != nil {
		return "", missing(err, "contact")
	}
	if row.FullName != "" {
		return row.FullName, nil
	}
	name := strings.TrimSpace(row.GivenName + " " + row.FamilyName)
	if name != "" {
		return name, nil
	}
	return "Client", nil
}

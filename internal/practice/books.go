package practice

import (
	"context"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// BooksMatter is the CORE-02 getBooksMatter projection.
type BooksMatter struct {
	ID                        domain.ID `json:"id"`
	Number                    string    `json:"number"`
	Name                      string    `json:"name"`
	Status                    string    `json:"status"`
	ClientContactID           any       `json:"client_contact_id"`
	ResponsibleAttorneyUserID any       `json:"responsible_attorney_user_id"`
}

// BooksContact is the CORE-02 getBooksContact projection.
type BooksContact struct {
	ID               domain.ID            `json:"id"`
	Kind             string               `json:"kind"`
	DisplayName      string               `json:"display_name"`
	GivenName        string               `json:"given_name"`
	FamilyName       string               `json:"family_name"`
	OrganizationName string               `json:"organization_name"`
	Email            string               `json:"email"`
	Phone            string               `json:"phone"`
	BillingAddress   *BooksBillingAddress `json:"billing_address"`
	Archived         bool                 `json:"archived"`
}

// BooksBillingAddress is the contact's primary current mailing address.
type BooksBillingAddress struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}

// BooksMatter returns a matter the books reader may see. A walled matter is not_found;
// the accountant role alone does not grant ResMatter, so only the wall is checked.
func (s *Service) BooksMatter(ctx context.Context, p domain.Principal, id domain.ID) (BooksMatter, error) {
	if id.IsZero() {
		return BooksMatter{}, domain.NotFound("matter")
	}
	row, err := s.db.Matter(ctx, id)
	if err != nil {
		return BooksMatter{}, missing(err, "matter")
	}
	view, err := s.viewWall(ctx, p, id)
	if err != nil {
		return BooksMatter{}, err
	}
	if view.Walled && !view.Allows {
		return BooksMatter{}, domain.NotFound("matter")
	}
	var client any
	for _, party := range s.db.Parties(id, domain.ID("")) {
		if party.Role == "client" && party.Primary {
			client = string(party.ContactID)
			break
		}
	}
	if client == nil {
		for _, party := range s.db.Parties(id, domain.ID("")) {
			if party.Role == "client" {
				client = string(party.ContactID)
				break
			}
		}
	}
	var attorney any
	if !row.ResponsibleAttorneyID.IsZero() {
		attorney = string(row.ResponsibleAttorneyID)
	}
	return BooksMatter{
		ID: row.ID, Number: row.Number, Name: row.Name, Status: row.Status,
		ClientContactID: client, ResponsibleAttorneyUserID: attorney,
	}, nil
}

// UnwalledClientContact reports a contact that is a client party on at least one matter
// with no wall (CORE-02 getBooksContact).
func (s *Service) UnwalledClientContact(ctx context.Context, contact domain.ID) (bool, error) {
	if contact.IsZero() {
		return false, nil
	}
	if _, err := s.db.Contact(ctx, contact); err != nil {
		return false, missing(err, "contact")
	}
	for _, party := range s.db.Parties(domain.ID(""), contact) {
		if party.Role != "client" {
			continue
		}
		view, err := s.viewWall(ctx, domain.Principal{}, party.MatterID)
		if err != nil {
			return false, err
		}
		if !view.Walled {
			return true, nil
		}
	}
	return false, nil
}

// BooksContact returns the billing fields of a contact for the books reader. The caller
// must already have checked readability (CORE-02).
func (s *Service) BooksContact(ctx context.Context, id domain.ID) (BooksContact, error) {
	row, err := s.db.Contact(ctx, id)
	if err != nil {
		return BooksContact{}, missing(err, "contact")
	}
	today := s.clock().Format("2006-01-02")
	out := BooksContact{
		ID: row.ID, Kind: row.Kind, GivenName: row.GivenName, FamilyName: row.FamilyName,
		Archived: row.Archived,
	}
	if row.Kind == KindOrganization {
		out.OrganizationName = row.FullName
	}
	out.DisplayName = row.FullName
	if out.DisplayName == "" {
		out.DisplayName = strings.TrimSpace(row.GivenName + " " + row.FamilyName)
	}
	if e := currentPrimaryEmailRow(s.db.Emails(id), today); e != nil {
		out.Email = e.Email
	}
	if ph := currentPrimaryPhoneRow(s.db.Phones(id), today); ph != nil {
		out.Phone = ph.PhoneE164
	}
	if addr := currentPrimaryAddressRow(s.db.Addresses(id), today); addr != nil {
		out.BillingAddress = &BooksBillingAddress{
			Line1: addr.Line1, Line2: addr.Line2, City: addr.City,
			Region: addr.Region, PostalCode: addr.PostalCode, Country: addr.Country,
		}
	}
	_ = ctx
	return out, nil
}

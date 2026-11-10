package doctender

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// PracticeMatters reads matters, their primary client and parties from the practice
// store, for naming tokens and matter matching. It does no access checks: Doc Tender
// reads files through the files service, which applies walls and roles.
type PracticeMatters struct {
	Store practice.Store
}

// Matter implements Matters.
func (m PracticeMatters) Matter(ctx context.Context, id domain.ID) (MatterInfo, error) {
	row, err := m.Store.Matter(ctx, id)
	if err != nil {
		return MatterInfo{}, err
	}
	return m.info(ctx, row), nil
}

// All implements Matters.
func (m PracticeMatters) All(ctx context.Context) ([]MatterInfo, error) {
	var out []MatterInfo
	for _, row := range m.Store.Matters() {
		out = append(out, m.info(ctx, row))
	}
	return out, nil
}

func (m PracticeMatters) info(ctx context.Context, row domain.Matter) MatterInfo {
	info := MatterInfo{ID: row.ID, Number: row.Number, Name: row.Name, Status: row.Status, CaseNumber: row.CaseNumber}
	var client *domain.Contact
	for _, primary := range []bool{true, false} {
		for _, p := range m.Store.Parties(row.ID, "") {
			if p.Primary != primary {
				continue
			}
			c, err := m.Store.Contact(ctx, p.ContactID)
			if err != nil {
				continue
			}
			if primary {
				info.Parties = append(info.Parties, c.FullName)
			} else if !contains(info.Parties, c.FullName) {
				info.Parties = append(info.Parties, c.FullName)
			}
			if p.Role == "client" && client == nil {
				cc := c
				client = &cc
			}
		}
	}
	if client != nil {
		if client.Kind == "organization" {
			info.ClientOrg = client.FullName
		} else {
			info.ClientFirst, info.ClientLast = client.GivenName, client.FamilyName
		}
	}
	return info
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

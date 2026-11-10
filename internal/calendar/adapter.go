package calendar

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// PracticeMatters reads matters through the practice service, so the calendar gets
// practice's wall and role answer for every matter it touches.
type PracticeMatters struct{ Svc *practice.Service }

// Matter returns the matter as p sees it; a walled or missing matter is not_found.
func (m PracticeMatters) Matter(ctx context.Context, p domain.Principal, id domain.ID) (MatterInfo, error) {
	view, err := m.Svc.GetMatter(ctx, p, id)
	if err != nil {
		return MatterInfo{}, err
	}
	row := view.Matter
	info := MatterInfo{
		ID: row.ID, Number: row.Number, Name: row.Name, Court: row.Court,
		CaseNumber: row.CaseNumber, ClientPosition: row.ClientPosition,
	}
	// Primary parties first, so the caption names the lead party for each role.
	for _, primary := range []bool{true, false} {
		for _, line := range view.PartyLines {
			if line.Primary == primary {
				info.Parties = append(info.Parties, Party{Role: line.Role, Name: line.Name})
			}
		}
	}
	return info, nil
}

// Search runs practice matter search as p, so walls and the minimum query length
// are practice's.
func (m PracticeMatters) Search(ctx context.Context, p domain.Principal, query string) ([]MatterChoice, error) {
	res, err := m.Svc.Search(ctx, p, query, practice.ScopeMatters)
	if err != nil {
		return nil, err
	}
	var out []MatterChoice
	for _, g := range res.Groups {
		for _, h := range g.Hits {
			if h.RecordType == "matter" {
				out = append(out, MatterChoice{ID: h.RecordID, Label: h.Title})
			}
		}
	}
	return out, nil
}

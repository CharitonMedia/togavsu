package editor

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// PracticeMatters reads matters through practice as the caller, so walls and roles are
// practice's own; the county name comes from the counties reference table.
type PracticeMatters struct {
	Svc  *practice.Service
	Pool *sql.DB
}

// Matter implements Matters.
func (m PracticeMatters) Matter(ctx context.Context, p domain.Principal, id domain.ID) (MatterInfo, error) {
	view, err := m.Svc.GetMatter(ctx, p, id)
	if err != nil {
		return MatterInfo{}, err
	}
	row := view.Matter
	info := MatterInfo{
		ID: row.ID, Number: row.Number, Name: row.Name, Court: row.Court, CaseNumber: row.CaseNumber,
		ClientPosition: row.ClientPosition, ResponsibleAttorneyID: row.ResponsibleAttorneyID,
	}
	if !row.CountyID.IsZero() && m.Pool != nil {
		err := m.Pool.QueryRowContext(ctx, `SELECT name FROM counties WHERE id = $1`, row.CountyID).Scan(&info.County)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return MatterInfo{}, err
		}
	}
	// Primary parties first, so the caption names the lead party on each side first.
	for _, primary := range []bool{true, false} {
		for _, line := range view.PartyLines {
			if line.Primary == primary {
				info.Parties = append(info.Parties, Party{Role: line.Role, Name: line.Name})
			}
		}
	}
	return info, nil
}

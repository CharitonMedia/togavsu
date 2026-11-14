package core

import (
	"context"
	"database/sql"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// ModuleRelinker is the merge's Relinker (mergeContact, mergeMatter): it moves every
// module row that points at the merged record onto the survivor inside the practice
// unit's transaction on pool, so the relink, the archive, the audit rows and the event
// commit together or not at all (PRD 9.9).
func ModuleRelinker(schemas *modules.Schemas, pool *sql.DB) practice.Relinker {
	return func(ctx context.Context, table string, from, to domain.ID) ([]practice.Relinked, error) {
		tx := audit.Tx(ctx, pool)
		if tx == nil {
			return nil, errors.New("core: a merge relinks module rows only inside its transaction")
		}
		got, err := schemas.Relink(ctx, tx, table, from, to)
		if err != nil {
			return nil, err
		}
		out := make([]practice.Relinked, 0, len(got))
		for _, g := range got {
			out = append(out, practice.Relinked{ModuleID: g.Module, Rows: g.Rows})
		}
		return out, nil
	}
}

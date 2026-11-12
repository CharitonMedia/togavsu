// Package compose is where the ui assignment first mounted internal/dashboard and
// internal/help on a running core from outside it. The integration lead folded that
// wiring into internal/core (ui.go), which owns router composition; core.Open mounts the
// ui. This package keeps the ui's end-to-end tests and visual QC, and Mount builds a
// second ui over a running core for tests that need different options (help media over
// a fake file store).
package compose

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/core"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Options configures Mount (core.UIOptions).
type Options = core.UIOptions

// UI is a mounted ui (core.UI).
type UI = core.UI

// FileVersions adapts the files service to help.Files (core.FileVersions).
type FileVersions = core.FileVersions

// Mount builds a ui on app with opt (core.MountUI).
func Mount(ctx context.Context, app *core.App, opt Options) (*UI, error) {
	return core.MountUI(ctx, app, opt)
}

func listUsers(ctx context.Context, app *core.App) ([]domain.User, error) {
	return core.ListUsers(ctx, app)
}

func auditRows(ctx context.Context, p domain.Principal, ledger *audit.Postgres, app *core.App, limit int) ([]dashboard.AuditRow, error) {
	return core.AuditRows(ctx, p, ledger, app, limit)
}

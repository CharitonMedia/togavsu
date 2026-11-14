package core

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/editor"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/trust"
	"github.com/CharitonMedia/togavsu/web"
)

// migrations is every migration the core applies: internal/db's core files, billing
// (300-399), trust (400-499), calendar (500-599), files (600-699), editor (700-799) and
// Doc Tender (800-899), in version order.
func migrations() ([]db.Migration, error) {
	ms, err := db.Embedded()
	if err != nil {
		return nil, err
	}
	for _, more := range []func() ([]db.Migration, error){billing.Migrations, trust.Migrations,
		calendar.Migrations, files.Migrations, editor.Migrations, doctender.Migrations,
		dashboard.Migrations, help.Migrations, modules.Migrations, marketplace.Migrations, coreMigrations} {
		add, err := more()
		if err != nil {
			return nil, err
		}
		ms = append(ms, add...)
	}
	if err := db.ValidateMigrationVersions(ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// finance is the billing and trust half of the running core.
type finance struct {
	Trust   *trust.Service
	Billing *billing.Service
	trustH  http.Handler
	billH   http.Handler
}

// openFinance wires trust and billing on the core pool, so a billing write and the trust
// ledger rows it asks for commit in one transaction with their audit rows. Trust balances
// flow to the practice store through the balance sink, invoices through the mirror, and
// committed trust entries sync flat-fee invoices through billing.TrustEvents.
func openFinance(ctx context.Context, pool *sql.DB, aud *audit.Service, reg *settings.Registry, store *practice.Durable,
	users trust.Users, walls domain.WallReader, fsvc *files.Service, custom billing.CustomFields, p func(*http.Request) (domain.Principal, error), scopes func(*http.Request, ...string) error, now func() time.Time) (*finance, error) {
	if err := trust.InstallPrivileges(ctx, pool); err != nil {
		return nil, fmt.Errorf("core: trust privileges: %w", err)
	}
	events := &billing.TrustEvents{}
	tr, err := trust.New(trust.Deps{
		DB: pool, Audit: aud, Settings: reg, Practice: store, Users: users, Walls: walls,
		Events: events, BalanceSink: store.PutTrust, Now: now,
	})
	if err != nil {
		return nil, err
	}
	link, err := billing.NewTrustLink(tr, pool)
	if err != nil {
		return nil, err
	}
	// Receipts and sent invoices' PDFs are file records in the one file store (PRD 5).
	fileLink, err := billing.NewFileLink(fsvc)
	if err != nil {
		return nil, err
	}
	bstore := billing.NewPostgres(pool).PublishEvents()
	if err := bstore.IndexRows(ctx); err != nil {
		return nil, fmt.Errorf("core: billing index: %w", err)
	}
	bs, err := billing.New(billing.Deps{
		Store: bstore, Practice: store, Walls: walls, Audit: aud, Settings: reg,
		Trust: link, Mail: smtpMailer{Settings: reg}, Mirror: store, Receipts: fileLink, InvoiceFiles: fileLink, Custom: custom, Now: now,
	})
	if err != nil {
		return nil, err
	}
	events.Bind(bs)
	// Repair the practice copies a crash between a commit and its after-commit push left
	// stale: trust balances first, then invoices and flat-fee credits.
	if err := tr.RebuildBalances(ctx); err != nil {
		return nil, fmt.Errorf("core: trust balances: %w", err)
	}
	if err := bs.Resync(ctx); err != nil {
		return nil, fmt.Errorf("core: billing resync: %w", err)
	}
	f := &finance{Trust: tr, Billing: bs}
	sub, err := fs.Sub(web.Trust, "templates/trust")
	if err != nil {
		return nil, err
	}
	th, err := trust.NewHandler(tr, sub, p)
	if err != nil {
		return nil, err
	}
	th.Scopes = scopes
	f.trustH = th.Routes()
	sub, err = fs.Sub(web.Billing, "templates/billing")
	if err != nil {
		return nil, err
	}
	bh, err := billing.NewHandler(bs, sub, p)
	if err != nil {
		return nil, err
	}
	f.billH = bh.Routes()
	return f, nil
}

// deliverTrustMail sends the trust outbox (below-zero flag mail and replenishment
// requests) through the admin's SMTP settings until ctx ends. A failed row stays queued
// with its error and is retried on the next tick.
// ponytail: ceiling: one in-process ticker every 30 seconds, no backoff. Upgrade path: the
// Postgres job queue (PRD section 6) owns retries with backoff. Trigger: the job queue ships.
func (a *App) deliverTrustMail(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		// Each send is bounded by mail.smtp_timeout_seconds and closed when ctx is cancelled.
		if _, err := a.DeliverMail(ctx); err != nil && ctx.Err() == nil {
			log.Printf("core: trust mail: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// DeliverMail sends every queued trust mail now and returns how many went out.
func (a *App) DeliverMail(ctx context.Context) (int, error) {
	a.mailMu.Lock()
	defer a.mailMu.Unlock()
	return a.Trust.DeliverMail(ctx, trust.SMTP{Settings: a.Settings})
}

// entryTypeScoped are the contract operations whose scopes depend on the body's
// entry_type (entry_type_scopes), or on each item's record_type for
// bulkCustomFieldValue (record_type_scopes): their handlers check them per item through
// App.scopes, so the route check only authenticates the token.
var entryTypeScoped = map[string]bool{"postTrustEntry": true, "bulkTrustEntry": true, "bulkCustomFieldValue": true}

// scopes is the trust handler's per-entry scope check: a session passes, a token must
// hold every scope named.
func (a *App) scopes(r *http.Request, need ...string) error {
	c, err := a.caller(r)
	if err != nil {
		return err
	}
	return c.RequireScopes(need...)
}

var _ trust.Users = (&identity.PG{}).Users()

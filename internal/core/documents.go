package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/editor"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/web"
	editorpages "github.com/CharitonMedia/togavsu/web/templates/editor"
)

// DataDir is where the core keeps its own files in the image: upload staging and the
// default local-disk storage backend (a Docker volume in compose.yaml).
const DataDir = "/var/lib/togavsu-core"

// WordcraftCLI and EditorDist are where the core image puts LawCraft's headless build
// and its browser editor, both built at the pinned commit (PRD 8.20).
const (
	WordcraftCLI = "/usr/local/bin/wordcraft-cli"
	EditorDist   = "/usr/share/togavsu/editor"
)

// documents is the calendar, file store, editor and Doc Tender half of the running core.
type documents struct {
	Calendar  *calendar.Service
	Files     *files.Service
	Editor    *editor.Service
	DocTender *doctender.Service
	editorH   *editor.Handler
	handlers  []http.Handler
	mail      adminMail
	// bg is work started by a request that outlives it (a storage move); Close cancels
	// it and waits.
	bgCtx  context.Context
	stopBg context.CancelFunc
	bg     sync.WaitGroup
}

// close stops request-started background work and waits for it.
func (d *documents) close() {
	if d.stopBg != nil {
		d.stopBg()
	}
	d.bg.Wait()
}

// systemPrincipal is the actor for work no person started: Doc Tender's saves, drop
// folder takes and background moves. Its user row exists (ensureSystemUser) so
// saved_by and made_by keep their foreign keys; it has no roles and cannot sign in.
func systemPrincipal() domain.Principal {
	return domain.Principal{UserID: domain.SystemUserID, Source: domain.SourceSystem}
}

// ensureSystemUser writes the System user row once. It is inactive with no roles, so
// it never signs in, counts as a seat or reaches a role check.
func ensureSystemUser(ctx context.Context, pool *sql.DB, now time.Time) error {
	at := domain.UnixMicroUTC(now)
	_, err := pool.ExecContext(ctx, `INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active,
default_hourly_rate_cents, contact_id, bar_number, bar_state) VALUES ($1,$2,$2,$1,'Togavsu system','system@togavsu.invalid','[]',0,0,NULL,'','')
ON CONFLICT (id) DO NOTHING`, string(domain.SystemUserID), at)
	return err
}

// adminMail is system mail to people: the admins (storage alerts, Doc Tender
// exceptions and the daily summary) or one address (calendar reminders).
type adminMail struct {
	pool *sql.DB
	smtp smtpMailer
}

func (m adminMail) Send(ctx context.Context, to, subject, body string) error {
	return m.smtp.Send(ctx, billing.Message{To: []string{to}, Subject: subject, Text: body})
}

func (m adminMail) MailAdmins(ctx context.Context, subject, body string) error {
	to, err := usersWithRole(ctx, m.pool, domain.RoleAdmin)
	if err != nil {
		return err
	}
	var list []string
	for _, u := range to {
		list = append(list, u.Email)
	}
	if len(list) == 0 {
		return errors.New("no active admin to mail")
	}
	return m.smtp.Send(ctx, billing.Message{To: list, Subject: subject, Text: body})
}

type person struct {
	ID    domain.ID
	Email string
}

// usersWithRole lists active users holding any of roles. Roles are stored whole as JSON
// text and parsed here (PRD 6: no JSON operators in queries).
func usersWithRole(ctx context.Context, pool *sql.DB, roles ...domain.Role) ([]person, error) {
	rows, err := pool.QueryContext(ctx, `SELECT id, email, roles FROM users WHERE active = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []person
	for rows.Next() {
		var p person
		var raw string
		if err := rows.Scan(&p.ID, &p.Email, &raw); err != nil {
			return nil, err
		}
		have, err := domain.ParseRoles(raw)
		if err != nil {
			continue
		}
		pr := domain.Principal{UserID: p.ID, Roles: have}
		for _, r := range roles {
			if pr.HasRole(r) {
				out = append(out, p)
				break
			}
		}
	}
	return out, rows.Err()
}

// reviewNotices raises Doc Tender's review-queue notification for each admin and
// attorney in the shared notifications table (the bell and GET /api/v1/notifications).
// A notice about a walled matter goes only to readers inside the wall.
type reviewNotices struct {
	pool  *sql.DB
	walls domain.WallReader
	now   func() time.Time
}

func (n reviewNotices) NotifyReviewers(ctx context.Context, kind, title, link string, matter domain.ID) error {
	people, err := usersWithRole(ctx, n.pool, domain.RoleAdmin, domain.RoleAttorney)
	if err != nil {
		return err
	}
	at := domain.UnixMicroUTC(n.now())
	for _, p := range people {
		if !matter.IsZero() {
			v, err := n.walls.View(ctx, domain.Principal{UserID: p.ID}, matter)
			if err != nil || (v.Walled && !v.Allows) {
				continue
			}
		}
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		var m any
		if !matter.IsZero() {
			m = string(matter)
		}
		if _, err := n.pool.ExecContext(ctx, `INSERT INTO notifications (id, created_at, updated_at, created_by, user_id, kind, title, link, matter_id, read_at)
VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,0)`, string(id), at, string(domain.SystemUserID), string(p.ID), kind, title, link, m); err != nil {
			return err
		}
	}
	return nil
}

// documentsConfig is what openDocuments needs beyond the shared services.
type documentsConfig struct {
	DataDir      string // for editor run folders
	WordcraftCLI string
	EditorDist   string
	ProviderURLs map[string]string
	Worker       string
}

// openFiles wires the one file store on the core pool before finance, so billing keeps
// receipts and invoice PDFs in it (billing.FileLink). It writes the System user and turns
// on practice's relational projection first: file rows reference matters, contacts and
// users by foreign key. Each new file version queues its Doc Tender job through
// OnVersion once openDocuments has made Doc Tender.
func openFiles(ctx context.Context, pool *sql.DB, aud *audit.Service, reg *settings.Registry, store *practice.Durable,
	walls domain.WallReader, key []byte, dataDir string, now func() time.Time) (*documents, error) {
	if err := ensureSystemUser(ctx, pool, now()); err != nil {
		return nil, fmt.Errorf("core: system user: %w", err)
	}
	if err := store.EnableProjection(ctx); err != nil {
		return nil, fmt.Errorf("core: practice projection: %w", err)
	}
	d := &documents{mail: adminMail{pool: pool, smtp: smtpMailer{Settings: reg}}}
	fsvc, err := files.New(files.Config{
		Pool: pool, Audit: aud, Settings: reg, Directory: files.PracticeDirectory{Store: store}, Walls: walls, Mail: d.mail, Key: key,
		StagingDir: filepath.Join(dataDir, "staging"), Now: now,
		OnVersion: func(ctx context.Context, ev files.VersionCreated) {
			if d.DocTender != nil {
				d.DocTender.OnVersion(ctx, ev)
			}
		},
	})
	if err != nil {
		return nil, err
	}
	d.Files = fsvc
	// The first local-disk backend is the default until the admin picks another; an
	// install that already has backends keeps them.
	if _, err := fsvc.EnsureLocalDisk(ctx, domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceSystem},
		filepath.Join(dataDir, "files")); err != nil {
		return nil, fmt.Errorf("core: local disk storage: %w", err)
	}
	return d, nil
}

// openDocuments wires the calendar, the editor and Doc Tender beside the file store d
// holds, with practice's wall reader everywhere, so a matter wall hides a file, its
// Markdown copy, its Doc Tender result, its deadlines and its events in every surface.
func openDocuments(ctx context.Context, d *documents, pool *sql.DB, aud *audit.Service, reg *settings.Registry, store *practice.Durable,
	svc *practice.Service, users calendar.Users, domainName string, cfg documentsConfig,
	p func(*http.Request) (domain.Principal, error), now func() time.Time) error {
	fsvc, mail, walls := d.Files, d.mail, svc.Policy.Walls
	cal, err := calendar.New(calendar.Deps{
		Store: calendar.NewPostgres(pool), Policy: svc.Policy, Matters: calendar.PracticeMatters{Svc: svc}, Users: users,
		Settings: reg, Audit: aud, Mailer: mail, BaseURL: "https://" + domainName, Now: now,
	})
	if err != nil {
		return err
	}
	d.Calendar = cal
	cli := &editor.CLI{Path: cfg.WordcraftCLI, TempDir: filepath.Join(cfg.DataDir, "runs")}
	if err := os.MkdirAll(cli.TempDir, os.ModePerm&0o700); err != nil {
		return err
	}
	ed, err := editor.New(editor.Config{Files: fsvc, Pool: pool, Audit: aud, Settings: reg, Users: users,
		Matters: editor.PracticeMatters{Svc: svc, Pool: pool}, CLI: cli, Now: now})
	if err != nil {
		return err
	}
	d.Editor = ed
	router := &doctender.Router{Settings: reg, BaseURLs: cfg.ProviderURLs}
	deps := doctender.Deps{
		Pool: pool, Files: fsvc, Settings: reg, Models: router, Matters: doctender.PracticeMatters{Store: store}, Walls: walls,
		Mail: mail, Audit: aud, Notify: reviewNotices{pool: pool, walls: walls, now: now}, Docs: doctender.WordcraftCLI{Path: cfg.WordcraftCLI},
		System: systemPrincipal(), Worker: cfg.Worker, Now: now,
	}
	if _, err := exec.LookPath("tesseract"); err == nil {
		deps.Second = doctender.Tesseract{}
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		deps.Media = doctender.FFmpeg{}
	}
	dt, err := doctender.New(deps)
	if err != nil {
		return err
	}
	router.Log = dt.CallLog()
	d.DocTender = dt

	sub, err := fs.Sub(web.Calendar, "templates/calendar")
	if err != nil {
		return err
	}
	ch, err := calendar.NewHandler(cal, sub, p)
	if err != nil {
		return err
	}
	if d.editorH, err = editor.NewHandler(ed, editorpages.FS, editor.DistDir(cfg.EditorDist), p); err != nil {
		return err
	}
	if sub, err = fs.Sub(web.Files, "templates/files"); err != nil {
		return err
	}
	fh, err := files.NewHandler(fsvc, sub, p)
	if err != nil {
		return err
	}
	d.bgCtx, d.stopBg = context.WithCancel(context.Background())
	fh.BackgroundContext = d.bgCtx
	fh.Background = func(f func()) { spawn(&d.bg, f) }
	fh.DocStatus = func(ctx context.Context, p domain.Principal, id domain.ID) (files.DocStatus, error) {
		st, err := dt.Status(ctx, p, id)
		if err != nil {
			return files.DocStatus{}, err
		}
		out := files.DocStatus{Label: st.Label}
		for _, f := range st.Flags {
			out.Flags = append(out.Flags, files.DocFlag{Kind: f.Kind, Label: f.Label, Detail: f.Detail})
		}
		return out, nil
	}
	if sub, err = fs.Sub(web.DocTender, "templates/doctender"); err != nil {
		return err
	}
	dh, err := doctender.NewHandler(dt, reg, sub, p)
	if err != nil {
		return err
	}
	// The editor's routes go before files': /files/{id}/edit and the Files tab's
	// /files/static/{name} cannot share one ServeMux (editor.Handler.Owns).
	d.handlers = []http.Handler{ch.Routes(), d.editorH.Routes(), fh.Routes(), dh.Routes()}
	return nil
}

// runDocuments is the documents' background work until ctx ends: Doc Tender's worker
// (each file as it arrives, retries, the nightly sweep and the daily summary), the drop
// folder scan, resuming storage moves a stopped core left running, and calendar
// reminders.
// ponytail: ceiling: in-process tickers (drop folder every 10 seconds, reminders every
// minute) beside Doc Tender's Postgres-leased jobs. Upgrade path: the shared Postgres
// job queue (PRD 6) runs drop scans, moves and reminders as jobs. Trigger: a second
// core process, or the job-queue task.
func (a *App) runDocuments(ctx context.Context, dropEvery, remindEvery time.Duration) {
	d := a.docs
	var wg sync.WaitGroup
	spawn(&wg, func() {
		if err := d.DocTender.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("core: doc tender worker: %v", err)
		}
	})
	spawn(&wg, func() {
		if err := d.Files.ResumeMoves(ctx); err != nil && ctx.Err() == nil {
			log.Printf("core: storage moves: %v", err)
		}
		tick(ctx, dropEvery, func() {
			if _, err := d.Files.ScanDrop(ctx); err != nil && ctx.Err() == nil {
				log.Printf("core: drop folder: %v", err)
			}
		})
	})
	spawn(&wg, func() {
		tick(ctx, remindEvery, func() {
			if _, err := d.Calendar.RunReminders(ctx); err != nil && ctx.Err() == nil {
				log.Printf("core: calendar reminders: %v", err)
			}
		})
	})
	wg.Wait()
}

// oneTask is one goroutine on a WaitGroup.
const oneTask = int(0) + 1

// spawn runs f on its own goroutine, counted on wg.
func spawn(wg *sync.WaitGroup, f func()) {
	wg.Add(oneTask)
	go func() {
		defer wg.Done()
		f()
	}()
}

func tick(ctx context.Context, every time.Duration, f func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		f()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

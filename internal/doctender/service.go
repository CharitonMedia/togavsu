package doctender

import (
	"bytes"
	"compress/zlib"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrations returns the Doc Tender migrations (range 800-899). They run after the
// core migrations in internal/db and the files migrations (600-699).
func Migrations() ([]db.Migration, error) { return db.LoadFS(migrationFS) }

// Migrate applies the Doc Tender migrations to plain, a connection without a
// search_path, under the core owner.
func Migrate(ctx context.Context, plain *sql.DB) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	if err := db.ValidateMigrationVersions(ms); err != nil {
		return err
	}
	return db.Apply(ctx, plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms})
}

// Settings is the registry read Doc Tender needs. *settings.Registry implements it.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
}

// Mailer sends system mail.
type Mailer interface {
	MailAdmins(ctx context.Context, subject, body string) error
	Send(ctx context.Context, to, subject, body string) error
}

// Notifier raises an in-app notification for the review queue's readers (admins and
// attorneys). Nil leaves mail only.
type Notifier interface {
	NotifyReviewers(ctx context.Context, kind, title, link string, matter domain.ID) error
}

// MatterInfo is what Doc Tender reads about a matter: status (closed spends nothing),
// the naming tokens, and what matter matching compares.
type MatterInfo struct {
	ID          domain.ID
	Number      string
	Name        string
	Status      string
	CaseNumber  string
	ClientFirst string
	ClientLast  string
	// ClientOrg is the primary client's organization name, when the client is one.
	ClientOrg string
	// Parties are the full names of the matter's parties.
	Parties []string
}

// Matters reads matters without access checks; access is applied through files.
type Matters interface {
	Matter(ctx context.Context, id domain.ID) (MatterInfo, error)
	All(ctx context.Context) ([]MatterInfo, error)
}

// Deps wires the service.
type Deps struct {
	Pool    *sql.DB
	Dialect string
	Files   *files.Service
	// Settings is the live registry; every tunable is read from it per use.
	Settings Settings
	Models   Models
	Matters  Matters
	// Walls resolves matter walls for what Doc Tender says about a matter that is not
	// the file's own (a matter-matching suggestion, a grouping call's spend row). It is
	// the same reader files uses (practice.Service.Policy.Walls in the running core).
	Walls domain.WallReader
	Mail  Mailer
	// Audit records category edits; the settings registry audits its own writes.
	Audit  *audit.Service
	Notify Notifier
	// Second is the independent OCR read of scanned pages and photos (Tesseract).
	Second SecondReader
	// Media reads audio and video the native readers can't (ffmpeg).
	Media MediaTool
	// Docs converts Word-family documents (wordcraft-cli).
	Docs DocConverter
	// System is Doc Tender's principal: Source system with the user id written to
	// saved_by and made_by.
	System domain.Principal
	// Worker names this process's leases.
	Worker string
	Now    func() time.Time
}

// Service is Doc Tender.
type Service struct {
	d     Deps
	now   func() time.Time
	wake  chan struct{}
	mu    sync.Mutex
	newID func() (domain.ID, error)
	// health caches provider checks for one worker pass.
	health map[string]error
}

// New validates the dependencies.
func New(d Deps) (*Service, error) {
	switch {
	case d.Pool == nil, d.Files == nil, d.Settings == nil, d.Models == nil, d.Matters == nil, d.Walls == nil:
		return nil, errors.New("doctender: pool, files, settings, models, matters and walls are required")
	case d.System.Source != domain.SourceSystem || d.System.UserID.IsZero():
		return nil, errors.New("doctender: a system principal with a user id is required")
	}
	if d.Dialect == "" {
		d.Dialect = "postgres"
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Worker == "" {
		d.Worker = "core"
	}
	return &Service{d: d, now: func() time.Time { return d.Now().UTC() }, wake: make(chan struct{}, one), newID: domain.NewID}, nil
}

// Wake is signalled when a job is queued; the worker loop waits on it.
func (s *Service) Wake() <-chan struct{} { return s.wake }

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// matterReadable reports whether p may read the matter itself (its number, name and
// parties): the matter wall, then the role and client rules, as files and practice
// decide them. Any error is a no.
func (s *Service) matterReadable(ctx context.Context, p domain.Principal, matter domain.ID) bool {
	if matter == "" {
		return false
	}
	if p.Source == domain.SourceSystem {
		return true
	}
	view, err := s.d.Walls.View(ctx, p, matter)
	if err != nil {
		return false
	}
	return domain.Decide(p, domain.AccessInput{Resource: domain.ResMatter, Action: domain.ActionRead, MatterID: matter,
		Walled: view.Walled, WallAllows: view.Allows, ClientMatter: view.ClientMatter, SharedWithClient: view.ClientMatter}).Allow
}

// matterWalled reports whether the matter has a wall at all. Mail to a list (admins,
// addresses) can't be checked per reader, so nothing about a walled matter goes in it.
// Any error counts as walled.
func (s *Service) matterWalled(ctx context.Context, matter domain.ID) bool {
	if matter == "" {
		return false
	}
	view, err := s.d.Walls.View(ctx, domain.Principal{}, matter)
	return err != nil || view.Walled
}

// ---- small helpers ----

func us(t time.Time) int64 { return domain.UnixMicroUTC(t) }

func fromUS(v int64) time.Time { return domain.FromUnixMicro(v) }

func nullID(id domain.ID) any {
	if id == "" {
		return nil
	}
	return string(id)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func zlibBytes(raw []byte) []byte {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	return buf.Bytes()
}

func (s *Service) q(ctx context.Context) audit.Querier { return audit.Conn(ctx, s.d.Pool) }

func (s *Service) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.q(ctx).ExecContext(ctx, db.Rebind(query, s.d.Dialect), args...)
}

func (s *Service) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.q(ctx).QueryContext(ctx, db.Rebind(query, s.d.Dialect), args...)
}

func (s *Service) row(ctx context.Context, query string, args ...any) *sql.Row {
	return s.q(ctx).QueryRowContext(ctx, db.Rebind(query, s.d.Dialect), args...)
}

func (s *Service) tx(ctx context.Context, fn func(ctx context.Context) error) error {
	return audit.WithTx(ctx, s.d.Pool, fn)
}

func (s *Service) id() (domain.ID, error) { return s.newID() }

// setting reads one registry value.
func (s *Service) setting(ctx context.Context, key string) (string, error) {
	return s.d.Settings.Get(ctx, key)
}

func (s *Service) duration(ctx context.Context, key string) (time.Duration, error) {
	raw, err := s.setting(ctx, key)
	if err != nil {
		return zeroDuration, err
	}
	return time.ParseDuration(raw)
}

func (s *Service) intSetting(ctx context.Context, key string) (int, error) {
	raw, err := s.setting(ctx, key)
	if err != nil {
		return zero, err
	}
	return strconv.Atoi(strings.TrimSpace(raw))
}

func (s *Service) boolSetting(ctx context.Context, key string) (bool, error) {
	raw, err := s.setting(ctx, key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(raw)
}

// threshold is doctender.review_confidence: below it an item goes to review, and a
// check whose agreement score is below it fails.
func (s *Service) threshold(ctx context.Context) (float64, error) {
	raw, err := s.setting(ctx, "doctender.review_confidence")
	if err != nil {
		return float64(zero), err
	}
	return strconv.ParseFloat(strings.TrimSpace(raw), 64)
}

func (s *Service) retrySchedule(ctx context.Context) ([]time.Duration, error) {
	raw, err := s.setting(ctx, "doctender.retry_schedule")
	if err != nil {
		return nil, err
	}
	var parts []string
	if err := json.Unmarshal([]byte(raw), &parts); err != nil {
		return nil, fmt.Errorf("doctender.retry_schedule: %w", err)
	}
	out := make([]time.Duration, zero, len(parts))
	for _, p := range parts {
		d, err := time.ParseDuration(p)
		if err != nil {
			return nil, fmt.Errorf("doctender.retry_schedule: %w", err)
		}
		out = append(out, d)
	}
	return out, nil
}

// appendEvent adds a timeline row in the caller's transaction.
func (s *Service) appendEvent(ctx context.Context, action, recordType string, record, matter domain.ID, summary string) error {
	id, err := s.id()
	if err != nil {
		return err
	}
	at := us(s.now())
	_, err = s.exec(ctx, `INSERT INTO events (id, created_at, updated_at, created_by, actor_kind, actor_id, action, record_type, record_id, matter_id, summary, client_visible)
VALUES (?,?,?,?,'system',?,?,?,?,?,?,0)`, id, at, at, string(s.d.System.UserID), string(s.d.System.UserID), action, recordType, string(record), nullID(matter), summary)
	return err
}

// logCall is the Router's CallLog: every model call goes to the AI call log.
func (s *Service) logCall(ctx context.Context, task string, resp Response, fallback Route, fileID, matterID domain.ID) error {
	id, err := s.id()
	if err != nil {
		return err
	}
	at := us(s.now())
	fell := zero
	if resp.FellBack {
		fell = one
	}
	// Cost comes from the AI tab's model prices (ai.model_prices), read on every call so
	// a price change applies to the next call.
	raw, err := s.setting(ctx, "ai.model_prices")
	if err != nil {
		return err
	}
	prices, err := domain.ParseModelPrices(raw)
	if err != nil {
		return err
	}
	cost := prices.CostCents(resp.Model, resp.PromptTokens, resp.CompletionTokens)
	_, err = s.d.Pool.ExecContext(ctx, db.Rebind(`INSERT INTO ai_call_log (id, created_at, updated_at, created_by, task, provider, model, prompt_tokens, completion_tokens,
cost_cents, fell_back, fallback_provider, fallback_model, user_id, module_id, matter_id, file_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,'',?,?)`, s.d.Dialect),
		id, at, at, string(s.d.System.UserID), task, resp.Provider, resp.Model, resp.PromptTokens, resp.CompletionTokens,
		cost, fell, fallback.Provider, fallback.Model, nullID(matterID), nullID(fileID))
	return err
}

// CallLog returns the function a Router logs through.
func (s *Service) CallLog() CallLog { return s.logCall }

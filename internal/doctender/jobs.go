package doctender

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// Job statuses (the API contract's enum).
const (
	StatusQueued        = "queued"
	StatusRunning       = "running"
	StatusPublished     = "published"
	StatusException     = "exception"
	StatusMatterClosed  = "not_processed_matter_closed"
	StatusHeldProvider  = "held_for_provider"
	StatusHeldAISetup   = "held_for_ai_setup"
	unitPending         = "pending"
	unitPassed          = "passed"
	unitFailed          = "failed"
	kindFile            = "file"
	kindGroup           = "group"
	stageCheck          = "check"
	stageExtract        = "extract"
	stageAwaitingGroup  = "awaiting_group"
	stageGrouped        = "grouped"
	stageLabel          = "label"
	stagePublish        = "publish"
	stageDone           = "done"
	reasonArrival       = "arrival"
	reasonSweep         = "sweep"
	reasonReopen        = "reopen"
	reasonReprocess     = "reprocess"
	methodCheckFallback = "check_method"
)

// Job is one doc_tender_jobs row.
type Job struct {
	ID          domain.ID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	FileID      domain.ID
	VersionID   domain.ID
	MatterID    domain.ID
	Kind        string
	Stage       string
	Status      string
	Reason      string
	Attempts    int
	NextAttempt time.Time
	LastError   string
	LeaseOwner  string
	LeaseUntil  time.Time
	HeldSince   time.Time
	FinishedAt  time.Time
}

// Unit is one doc_tender_units row: a page, photo, sheet, media window or step.
type Unit struct {
	ID          domain.ID
	JobID       domain.ID
	Name        string
	Ordinal     int
	Method      string
	Model       string
	Text        string
	CheckMethod string
	CheckModel  string
	CheckResult string
	Status      string
	Attempts    int
	NextAttempt time.Time
	LastError   string
	StartedAt   time.Time
	FinishedAt  time.Time
}

const jobCols = `id, created_at, updated_at, file_id, file_version_id, COALESCE(matter_id, ''), kind, stage, status, reason, attempts,
next_attempt_at, last_error, lease_owner, lease_until, held_since, finished_at`

func scanJob(sc interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var created, updated, next, lease, held, fin int64
	var file sql.NullString
	err := sc.Scan(&j.ID, &created, &updated, &file, &j.VersionID, &j.MatterID, &j.Kind, &j.Stage, &j.Status, &j.Reason, &j.Attempts,
		&next, &j.LastError, &j.LeaseOwner, &lease, &held, &fin)
	j.FileID = domain.ID(file.String)
	j.CreatedAt, j.UpdatedAt, j.NextAttempt, j.LeaseUntil = fromUS(created), fromUS(updated), fromUS(next), fromUS(lease)
	if held != int64(zero) {
		j.HeldSince = fromUS(held)
	}
	if fin != int64(zero) {
		j.FinishedAt = fromUS(fin)
	}
	return j, err
}

func (s *Service) job(ctx context.Context, id domain.ID) (Job, error) {
	j, err := scanJob(s.row(ctx, `SELECT `+jobCols+` FROM doc_tender_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, domain.NotFound("doc_tender_job")
	}
	return j, err
}

func (s *Service) jobsWhere(ctx context.Context, where string, args ...any) ([]Job, error) {
	rows, err := s.query(ctx, `SELECT `+jobCols+` FROM doc_tender_jobs WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Service) saveJob(ctx context.Context, j Job) error {
	held, fin := int64(zero), int64(zero)
	if !j.HeldSince.IsZero() {
		held = us(j.HeldSince)
	}
	if !j.FinishedAt.IsZero() {
		fin = us(j.FinishedAt)
	}
	_, err := s.exec(ctx, `UPDATE doc_tender_jobs SET updated_at = ?, stage = ?, status = ?, attempts = ?, next_attempt_at = ?, last_error = ?,
lease_owner = ?, lease_until = ?, held_since = ?, finished_at = ?, matter_id = ? WHERE id = ?`,
		us(s.now()), j.Stage, j.Status, j.Attempts, us(j.NextAttempt), j.LastError, j.LeaseOwner, us(j.LeaseUntil), held, fin, nullID(j.MatterID), j.ID)
	return err
}

const unitCols = `id, job_id, unit, ordinal, method, model, text, check_method, check_model, check_result, status, attempts,
next_attempt_at, last_error, started_at, finished_at`

func scanUnit(sc interface{ Scan(...any) error }) (Unit, error) {
	var u Unit
	var next, start, fin int64
	err := sc.Scan(&u.ID, &u.JobID, &u.Name, &u.Ordinal, &u.Method, &u.Model, &u.Text, &u.CheckMethod, &u.CheckModel, &u.CheckResult,
		&u.Status, &u.Attempts, &next, &u.LastError, &start, &fin)
	u.NextAttempt = fromUS(next)
	if start != int64(zero) {
		u.StartedAt = fromUS(start)
	}
	if fin != int64(zero) {
		u.FinishedAt = fromUS(fin)
	}
	return u, err
}

// Units lists a job's units in order.
func (s *Service) units(ctx context.Context, job domain.ID) ([]Unit, error) {
	rows, err := s.query(ctx, `SELECT `+unitCols+` FROM doc_tender_units WHERE job_id = ? ORDER BY ordinal`, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		u, err := scanUnit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) insertUnit(ctx context.Context, job domain.ID, name string, ordinal int, preset *Unit) error {
	id, err := s.id()
	if err != nil {
		return err
	}
	at := us(s.now())
	u := Unit{Status: unitPending}
	if preset != nil {
		u = *preset
	}
	fin := int64(zero)
	if !u.FinishedAt.IsZero() {
		fin = us(u.FinishedAt)
	}
	_, err = s.exec(ctx, `INSERT INTO doc_tender_units (id, created_at, updated_at, created_by, job_id, unit, ordinal, method, model, text, check_method,
check_model, check_result, status, attempts, next_attempt_at, last_error, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,'',?,?) ON CONFLICT DO NOTHING`, id, at, at, string(s.d.System.UserID), job, name, ordinal,
		u.Method, u.Model, u.Text, u.CheckMethod, u.CheckModel, u.CheckResult, u.Status, u.Attempts, fin, fin)
	return err
}

func (s *Service) saveUnit(ctx context.Context, u Unit) error {
	start, fin := int64(zero), int64(zero)
	if !u.StartedAt.IsZero() {
		start = us(u.StartedAt)
	}
	if !u.FinishedAt.IsZero() {
		fin = us(u.FinishedAt)
	}
	_, err := s.exec(ctx, `UPDATE doc_tender_units SET updated_at = ?, method = ?, model = ?, text = ?, check_method = ?, check_model = ?,
check_result = ?, status = ?, attempts = ?, next_attempt_at = ?, last_error = ?, started_at = ?, finished_at = ? WHERE id = ?`,
		us(s.now()), u.Method, u.Model, u.Text, u.CheckMethod, u.CheckModel, u.CheckResult, u.Status, u.Attempts, us(u.NextAttempt),
		u.LastError, start, fin, u.ID)
	return err
}

// Enqueue queues a job for a file version at once. A version that already has an
// unfinished job keeps that job.
func (s *Service) Enqueue(ctx context.Context, fileID, versionID, matterID domain.ID, reason string) (domain.ID, error) {
	return s.enqueue(ctx, kindFile, fileID, versionID, matterID, reason)
}

func (s *Service) enqueue(ctx context.Context, kind string, fileID, versionID, matterID domain.ID, reason string) (domain.ID, error) {
	id, err := s.id()
	if err != nil {
		return "", err
	}
	at := us(s.now())
	res, err := s.exec(ctx, `INSERT INTO doc_tender_jobs (id, created_at, updated_at, created_by, file_version_id, stage, status, attempts,
next_attempt_at, last_error, lease_owner, lease_until, file_id, matter_id, kind, reason)
VALUES (?,?,?,?,?,?,?,0,?,'','',0,?,?,?,?) ON CONFLICT DO NOTHING`, id, at, at, string(s.d.System.UserID), versionID, stageCheck, StatusQueued, at,
		nullID(fileID), nullID(matterID), kind, reason)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == int64(zero) {
		var existing domain.ID
		err := s.row(ctx, `SELECT id FROM doc_tender_jobs WHERE file_version_id = ? AND kind = ? AND status NOT IN ('published', 'exception')`,
			versionID, kind).Scan(&existing)
		return existing, err
	}
	s.signal()
	return id, nil
}

// OnVersion is files.Config.OnVersion: file.version_created queues the version's job
// at once (PRD 8.21 "each file as it arrives"). A grouped PDF Doc Tender made is queued
// by the grouping step itself, with its pages already read.
func (s *Service) OnVersion(ctx context.Context, ev files.VersionCreated) {
	if ev.How == "grouping" {
		return
	}
	_, _ = s.Enqueue(context.WithoutCancel(ctx), ev.FileID, ev.VersionID, ev.MatterID, reasonArrival)
}

// errDeferred stops a job until its next attempt time; it is not a failure.
var errDeferred = errors.New("deferred")

// claim leases the next due job. A lease that lapsed (a worker that stopped) can be
// taken by the next worker, which resumes at the first unit without a passing check.
func (s *Service) claim(ctx context.Context) (Job, bool, error) {
	ready, err := s.d.Models.Ready(ctx)
	if err != nil {
		return Job{}, false, err
	}
	lease, err := s.duration(ctx, "doctender.lease_timeout")
	if err != nil {
		return Job{}, false, err
	}
	now := us(s.now())
	var id domain.ID
	err = s.row(ctx, `UPDATE doc_tender_jobs SET lease_owner = ?, lease_until = ?, updated_at = ?
WHERE id = (SELECT id FROM doc_tender_jobs WHERE status IN (`+claimable(ready)+`) AND stage <> 'awaiting_group' AND next_attempt_at <= ? AND lease_until < ?`+notPurged+`
            ORDER BY next_attempt_at, created_at, id LIMIT 1)
  AND lease_until < ? RETURNING id`, s.d.Worker, now+int64(lease/microUnit), now, now, now, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	j, err := s.job(ctx, id)
	return j, err == nil, err
}

// notPurged leaves out jobs whose file an admin emptied from the trash: there is nothing
// left to process, so such a job is never claimed and never wakes the worker. The
// nightly sweep already skips purged files.
const notPurged = ` AND NOT EXISTS (SELECT 1 FROM files pf WHERE pf.id = doc_tender_jobs.file_id AND pf.purged_at <> 0)`

// claimable is the job statuses claim takes: held_for_ai_setup only once AI is set up.
func claimable(ready bool) string {
	statuses := `'queued', 'running', 'held_for_provider'`
	if ready {
		statuses += `, 'held_for_ai_setup'`
	}
	return statuses
}

// renew extends this worker's lease; false means another worker took the job.
func (s *Service) renew(ctx context.Context, j *Job) (bool, error) {
	lease, err := s.duration(ctx, "doctender.lease_timeout")
	if err != nil {
		return false, err
	}
	until := s.now().Add(lease)
	res, err := s.exec(ctx, `UPDATE doc_tender_jobs SET lease_until = ? WHERE id = ? AND lease_owner = ?`, us(until), j.ID, s.d.Worker)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	j.LeaseUntil = until
	return n == int64(one), nil
}

func (s *Service) release(ctx context.Context, j Job) error {
	j.LeaseOwner, j.LeaseUntil = "", time.Time{}
	return s.saveJob(ctx, j)
}

// RunOnce processes the next due job, if any. It reports whether one ran.
func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	on, err := s.boolSetting(ctx, "doctender.enabled")
	if err != nil || !on {
		return false, err
	}
	if err := s.queueReopened(ctx); err != nil {
		return false, err
	}
	if err := s.groupDue(ctx); err != nil {
		return false, err
	}
	j, ok, err := s.claim(ctx)
	if err != nil || !ok {
		return false, err
	}
	s.mu.Lock()
	s.health = map[string]error{}
	s.mu.Unlock()
	if j.Kind == kindGroup {
		err = s.runGroup(ctx, j)
	} else {
		err = s.runFile(ctx, j)
	}
	if errors.Is(err, errDeferred) {
		err = nil
	}
	if ferr := s.FlushExceptionMail(ctx); err == nil {
		err = ferr
	}
	return true, err
}

// Drain runs jobs until none is due.
func (s *Service) Drain(ctx context.Context) error {
	for {
		ran, err := s.RunOnce(ctx)
		if err != nil || !ran {
			return err
		}
	}
}

// Run is the background worker: it processes jobs as they arrive (Wake), sleeps until
// the next due attempt, runs the nightly sweep and sends the daily summary.
func (s *Service) Run(ctx context.Context) error {
	for {
		if err := s.Drain(ctx); err != nil && ctx.Err() == nil {
			// The failing job keeps its lease until it lapses; the loop carries on.
			_ = err
		}
		if err := s.dailyWork(ctx); err != nil && ctx.Err() == nil {
			_ = err
		}
		wait, err := s.nextWait(ctx)
		if err != nil {
			wait = s.waitDelay(ctx)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-s.wake:
		case <-t.C:
		}
		t.Stop()
	}
}

// nextWait is the time until claim or groupDue next has work, at most one lease period.
// It counts only what claim would take (no held_for_ai_setup job while no AI is set
// up, no photo waiting for its group), each at the later of its next attempt and its
// lease, so a job that cannot run yet never makes the worker spin.
func (s *Service) nextWait(ctx context.Context) (time.Duration, error) {
	lease, err := s.duration(ctx, "doctender.lease_timeout")
	if err != nil {
		return zeroDuration, err
	}
	ready, err := s.d.Models.Ready(ctx)
	if err != nil {
		return zeroDuration, err
	}
	d := lease
	var next sql.NullInt64
	err = s.row(ctx, `SELECT MIN(CASE WHEN lease_until > next_attempt_at THEN lease_until ELSE next_attempt_at END) FROM doc_tender_jobs
WHERE status IN (`+claimable(ready)+`) AND stage <> 'awaiting_group'`+notPurged).Scan(&next)
	if err != nil {
		return zeroDuration, err
	}
	if next.Valid {
		d = min(d, max(fromUS(next.Int64).Sub(s.now()), zeroDuration))
	}
	due, ok, err := s.groupDueAt(ctx)
	if err != nil {
		return zeroDuration, err
	}
	if ok {
		// A due scope is grouped by groupDue on the next pass, so it no longer waits.
		d = min(d, max(due.Sub(s.now()), zeroDuration))
	}
	return d, nil
}

// unitWork is what running one unit produced.
type unitWork struct {
	Text        string
	Method      string
	Model       string
	CheckMethod string
	CheckModel  string
	CheckResult string
	// Passed is the independent check's verdict. A read that fails its check is
	// retried and never published as correct.
	Passed bool
}

// unitSpec runs one unit. checkMethod is set on the last attempt, after the retry
// schedule is spent: the unit is read by its check method instead.
type unitSpec struct {
	Name      string
	Tasks     []string
	Run       func(ctx context.Context, checkMethod bool) (unitWork, error)
	Exception string
}

// runUnit drives one unit through its attempts. It returns errDeferred when the job must
// wait (a later attempt, a provider that is down), and the unit's final state otherwise.
func (s *Service) runUnit(ctx context.Context, j *Job, u *Unit, spec unitSpec) error {
	if u.Status != unitPending {
		return nil
	}
	maxAttempts, err := s.intSetting(ctx, "doctender.max_attempts")
	if err != nil {
		return err
	}
	schedule, err := s.retrySchedule(ctx)
	if err != nil {
		return err
	}
	for u.Status == unitPending {
		if ok, err := s.renew(ctx, j); err != nil || !ok {
			if err == nil {
				err = errors.New("doctender: lease lost")
			}
			return err
		}
		if u.NextAttempt.After(s.now()) {
			return s.defer_(ctx, j, u.NextAttempt)
		}
		if err := s.providersUp(ctx, j, spec.Tasks); err != nil {
			return err
		}
		checkMethod := u.Attempts >= maxAttempts
		u.Attempts++
		u.StartedAt = s.now()
		u.LastError = ""
		if err := s.saveUnit(ctx, *u); err != nil {
			return err
		}
		work, err := safeRun(withCost(ctx, j.ID, u.ID), spec, checkMethod)
		if errors.Is(err, ErrNoAI) {
			u.Attempts--
			_ = s.saveUnit(ctx, *u)
			j.Status, j.NextAttempt = StatusHeldAISetup, s.now()
			_ = s.release(ctx, *j)
			return errDeferred
		}
		var down *ProviderDown
		if errors.As(err, &down) {
			u.Attempts--
			_ = s.saveUnit(ctx, *u)
			return s.holdForProvider(ctx, j, down)
		}
		if ctx.Err() != nil {
			// The worker stopped mid-unit: the attempt is not saved as passed, and the
			// next worker resumes here once the lease lapses.
			return ctx.Err()
		}
		u.Method, u.Model, u.CheckMethod, u.CheckModel, u.CheckResult = work.Method, work.Model, work.CheckMethod, work.CheckModel, work.CheckResult
		if checkMethod {
			u.Method = methodCheckFallback + ":" + work.Method
		}
		u.FinishedAt = s.now()
		if errors.Is(err, errDecodeLimit) {
			// A decompression or pixel bomb: the file is damaged, and a retry would only
			// expand it again.
			u.Status, u.LastError = unitFailed, err.Error()
			if err := s.saveUnit(ctx, *u); err != nil {
				return err
			}
			return s.openException(ctx, j, "damaged", u.Name+": "+err.Error(), map[string]any{"unit": u.Name})
		}
		if err == nil && work.Passed {
			u.Text, u.Status = work.Text, unitPassed
			return s.saveUnit(ctx, *u)
		}
		if err != nil {
			u.LastError = err.Error()
		} else {
			u.LastError = "check failed: " + work.CheckResult
		}
		if checkMethod {
			u.Status, u.Text = unitFailed, work.Text
			if err := s.saveUnit(ctx, *u); err != nil {
				return err
			}
			kind := spec.Exception
			if kind == "" {
				kind = "extraction_failed"
			}
			return s.openException(ctx, j, kind, fmt.Sprintf("%s failed after %d attempts: %s", u.Name, u.Attempts, u.LastError),
				map[string]any{"unit": u.Name, "text": work.Text})
		}
		delay := zeroDuration
		if u.Attempts < len(schedule) {
			delay = schedule[u.Attempts]
		}
		u.NextAttempt = s.now().Add(delay)
		if err := s.saveUnit(ctx, *u); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) defer_(ctx context.Context, j *Job, at time.Time) error {
	j.Status, j.NextAttempt = StatusQueued, at
	if err := s.release(ctx, *j); err != nil {
		return err
	}
	return errDeferred
}

// providersUp checks each provider the unit calls. While one is down no attempt is
// spent; the job waits, and after doctender.provider_down_threshold it also goes to
// the review queue.
func (s *Service) providersUp(ctx context.Context, j *Job, tasks []string) error {
	seen := map[string]bool{}
	for _, t := range tasks {
		for _, check := range []bool{false, true} {
			rt, err := s.d.Models.Route(ctx, t, check)
			if errors.Is(err, ErrNoCheckModel) || (check && err != nil) {
				continue
			}
			if errors.Is(err, ErrNoAI) {
				j.Status, j.NextAttempt = StatusHeldAISetup, s.now()
				_ = s.release(ctx, *j)
				return errDeferred
			}
			if err != nil {
				return s.holdForSetup(ctx, j, err)
			}
			if seen[rt.Provider] {
				continue
			}
			seen[rt.Provider] = true
			s.mu.Lock()
			herr, cached := s.health[rt.Provider]
			s.mu.Unlock()
			if !cached {
				herr = s.d.Models.Health(ctx, rt.Provider)
				s.mu.Lock()
				s.health[rt.Provider] = herr
				s.mu.Unlock()
				if err := s.recordHealth(ctx, rt.Provider, herr); err != nil {
					return err
				}
			}
			if herr != nil {
				return s.holdForProvider(ctx, j, &ProviderDown{Provider: rt.Provider, Err: herr})
			}
		}
	}
	if j.Status == StatusHeldProvider || j.Status == StatusHeldAISetup || !j.HeldSince.IsZero() {
		j.Status, j.HeldSince = StatusRunning, time.Time{}
		if err := s.saveJob(ctx, *j); err != nil {
			return err
		}
		if err := s.autoResolve(ctx, j.FileID, "provider_down", "the provider recovered"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordHealth(ctx context.Context, provider string, herr error) error {
	now := us(s.now())
	if herr == nil {
		_, err := s.exec(ctx, `INSERT INTO doc_tender_providers (provider, down_since, checked_at, last_error) VALUES (?,0,?,'')
ON CONFLICT (provider) DO UPDATE SET down_since = 0, checked_at = excluded.checked_at, last_error = ''`, provider, now)
		return err
	}
	_, err := s.exec(ctx, `INSERT INTO doc_tender_providers (provider, down_since, checked_at, last_error) VALUES (?,?,?,?)
ON CONFLICT (provider) DO UPDATE SET down_since = CASE WHEN doc_tender_providers.down_since = 0 THEN excluded.down_since ELSE doc_tender_providers.down_since END,
checked_at = excluded.checked_at, last_error = excluded.last_error`, provider, now, now, herr.Error())
	return err
}

func (s *Service) holdForProvider(ctx context.Context, j *Job, down *ProviderDown) error {
	schedule, err := s.retrySchedule(ctx)
	if err != nil {
		return err
	}
	wait := firstDelay(schedule)
	if j.HeldSince.IsZero() {
		j.HeldSince = s.now()
	}
	j.Status, j.NextAttempt, j.LastError = StatusHeldProvider, s.now().Add(wait), down.Error()
	limit, err := s.duration(ctx, "doctender.provider_down_threshold")
	if err != nil {
		return err
	}
	if s.now().Sub(j.HeldSince) >= limit {
		if err := s.openException(ctx, j, "provider_down", down.Error(), map[string]any{"provider": down.Provider}); err != nil {
			return err
		}
	}
	if err := s.release(ctx, *j); err != nil {
		return err
	}
	return errDeferred
}

// holdForSetup holds a job whose AI route can't be built from the AI tab: a model
// left empty, or labeling sent to a local endpoint (ErrLocalRefused). The job waits
// on the retry schedule, so a fixed setting picks it up, and the file goes to the
// review queue with the reason, so the firm sees why it is not processed. The item
// closes itself once the route works (autoResolve on provider_down).
func (s *Service) holdForSetup(ctx context.Context, j *Job, cause error) error {
	if j.HeldSince.IsZero() {
		j.HeldSince = s.now()
	}
	j.Status, j.NextAttempt, j.LastError = StatusHeldAISetup, s.now().Add(s.waitDelay(ctx)), trimErr(cause)
	if err := s.openException(ctx, j, "provider_down", "the AI tab's setup does not work for this file: "+trimErr(cause), map[string]any{"setup": true}); err != nil {
		return err
	}
	if err := s.release(ctx, *j); err != nil {
		return err
	}
	return errDeferred
}

// queueReopened turns a closed matter's held jobs back into queued work once the matter
// is no longer closed (PRD 8.21: reopening the matter queues its files).
func (s *Service) queueReopened(ctx context.Context) error {
	held, err := s.jobsWhere(ctx, `status = ?`, StatusMatterClosed)
	if err != nil {
		return err
	}
	for _, j := range held {
		if err := s.requeueIfOpen(ctx, j); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requeueIfOpen(ctx context.Context, j Job) error {
	if j.MatterID == "" {
		return nil
	}
	m, err := s.d.Matters.Matter(ctx, j.MatterID)
	if err != nil || m.Status == "closed" {
		return err
	}
	j.Status, j.Stage, j.NextAttempt = StatusQueued, stageCheck, s.now()
	return s.saveJob(ctx, j)
}

// MatterReopened queues a reopened matter's files at once: held jobs return to the
// queue, and live files whose current version has no job get one. Practice calls it on a status change; the
// worker also notices on its own.
func (s *Service) MatterReopened(ctx context.Context, matter domain.ID) error {
	held, err := s.jobsWhere(ctx, `status = ? AND matter_id = ?`, StatusMatterClosed, matter)
	if err != nil {
		return err
	}
	for _, j := range held {
		if err := s.requeueIfOpen(ctx, j); err != nil {
			return err
		}
	}
	fs, err := s.d.Files.MatterFiles(ctx, s.d.System, matter)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if f.CurrentVersionID == nil {
			continue
		}
		// Settled stays settled: a version that already has a job (published, in the
		// review queue, or grouped) is not processed again, as Sweep also checks.
		var has bool
		if err := s.row(ctx, `SELECT EXISTS (SELECT 1 FROM doc_tender_jobs WHERE file_version_id = ?)
  OR EXISTS (SELECT 1 FROM doc_tender_group_members WHERE file_id = ?)`, *f.CurrentVersionID, f.ID).Scan(&has); err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := s.Enqueue(ctx, f.ID, *f.CurrentVersionID, matter, reasonReopen); err != nil {
			return err
		}
	}
	s.signal()
	return nil
}

// Sweep is the nightly check: every live file's current version has a finished job or
// an open exception, and any that has neither is queued. It processes nothing itself.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	rows, err := s.query(ctx, `SELECT f.id, f.current_version_id, COALESCE(f.matter_id, '') FROM files f
WHERE f.trashed_at = 0 AND f.purged_at = 0 AND f.current_version_id <> ''
  AND NOT EXISTS (SELECT 1 FROM doc_tender_jobs j WHERE j.file_version_id = f.current_version_id)
  AND NOT EXISTS (SELECT 1 FROM doc_tender_group_members g WHERE g.file_id = f.id)
  AND NOT EXISTS (SELECT 1 FROM doc_tender_exceptions e WHERE e.file_id = f.id AND e.status = 'open')`)
	if err != nil {
		return zero, err
	}
	type miss struct{ file, version, matter domain.ID }
	var todo []miss
	for rows.Next() {
		var m miss
		if err := rows.Scan(&m.file, &m.version, &m.matter); err != nil {
			rows.Close()
			return zero, err
		}
		todo = append(todo, m)
	}
	rows.Close()
	for _, m := range todo {
		if _, err := s.Enqueue(ctx, m.file, m.version, m.matter, reasonSweep); err != nil {
			return zero, err
		}
	}
	return len(todo), rows.Err()
}

// Retry puts a job's failed units and the job back in the queue. Passed units stay
// passed and are not redone.
func (s *Service) retryJob(ctx context.Context, j Job) (Job, error) {
	err := s.tx(ctx, func(ctx context.Context) error {
		if _, err := s.exec(ctx, `UPDATE doc_tender_units SET status = 'pending', attempts = 0, next_attempt_at = 0, last_error = '', updated_at = ?
WHERE job_id = ? AND status = 'failed'`, us(s.now()), j.ID); err != nil {
			return err
		}
		j.Status, j.NextAttempt, j.FinishedAt, j.LastError = StatusQueued, s.now(), time.Time{}, ""
		if j.Stage == stageDone || j.Stage == stagePublish {
			j.Stage = stageExtract
		}
		if err := s.saveJob(ctx, j); err != nil {
			return err
		}
		return s.appendEvent(ctx, "doc_tender.retry_scheduled", "doc_tender_job", j.ID, j.MatterID, "Doc Tender retry scheduled")
	})
	s.signal()
	return j, err
}

// Reprocess queues a fresh job for a file's current version. Its Markdown copy is
// added as a new version; fields a person locked are not changed.
func (s *Service) Reprocess(ctx context.Context, p domain.Principal, fileID domain.ID) (domain.ID, error) {
	if !reviewer(p) {
		return "", domain.Forbidden("only admins and attorneys reprocess files")
	}
	f, err := s.d.Files.Get(ctx, p, fileID)
	if err != nil {
		return "", err
	}
	if f.CurrentVersionID == nil {
		return "", domain.Invalid("file_id", "the file has no version")
	}
	matter := domain.ID("")
	if f.MatterID != nil {
		matter = *f.MatterID
	}
	return s.Enqueue(ctx, f.ID, *f.CurrentVersionID, matter, reasonReprocess)
}

// firstDelay is the retry schedule's first wait; a job that must wait (a provider that
// is down, a read that failed outside a unit) waits that long.
func firstDelay(schedule []time.Duration) time.Duration {
	for _, d := range schedule {
		if d > zeroDuration {
			return d
		}
	}
	return minuteUnit
}

func (s *Service) waitDelay(ctx context.Context) time.Duration {
	schedule, _ := s.retrySchedule(ctx)
	return firstDelay(schedule)
}

func reviewer(p domain.Principal) bool {
	return p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney)
}

func trimErr(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

// safeRun runs a unit; a reader that panics on a hostile file fails the attempt
// instead of stopping the worker.
func safeRun(ctx context.Context, spec unitSpec, checkMethod bool) (work unitWork, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s could not be read: %v", spec.Name, r)
		}
	}()
	return spec.Run(ctx, checkMethod)
}

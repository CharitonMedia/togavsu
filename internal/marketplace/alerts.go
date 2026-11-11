package marketplace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// Firms already running a module (PRD 10.5). The core checks the marketplace on the
// modules.alert_check schedule (default daily) and when an admin opens the modules
// screen. A new flag or pull on an installed module becomes a module alert on the admin
// dashboard, a notification for every admin and an email to every admin, each carrying
// the warning and the operator's note. Nothing is switched off: the admin decides.

// NotificationKind is the notification and preference kind (PRD 8.17).
const NotificationKind = "module_alert"

// AlertsLink is where an alert opens: the Modules and marketplace admin tab.
const AlertsLink = "/admin?tab=modules"

const alertEntity = "module_alerts"

// decision is the firm-control sentence every alert carries (PRD 10.5).
const decision = "Nothing has been switched off. You decide: keep running it, disable it, update it, remove it, or patch it and run your own signed build."

// ModuleAlert is one delivered alert.
type ModuleAlert struct {
	domain.Meta
	SourceAlertID    string
	ModuleID         string
	Version          string
	InstalledVersion string
	Kind             string
	Note             string
	Warning          string
	RaisedAt         time.Time
}

// CheckResult is what one check found.
type CheckResult struct {
	Checked int
	New     []ModuleAlert
}

// CheckStatus is the last check's outcome, for the modules screen.
type CheckStatus struct {
	AttemptedAt time.Time
	SucceededAt time.Time
	LastError   string
}

// applies reports whether marketplace alert a concerns the installed module m: a flag
// or pull on the listing applies to every version, a version flag only to that version.
func applies(a Alert, m InstalledModule) bool {
	if a.ModuleID != m.ID {
		return false
	}
	return a.Version == "" || a.Version == m.Version
}

// Check asks the marketplace about every installed module and delivers each alert it has
// not delivered before. A failure is recorded for the modules screen and returned; the
// next scheduled check tries again.
func (s *Service) Check(ctx context.Context) (CheckResult, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	res, err := s.check(ctx)
	s.recordCheck(ctx, err)
	return res, err
}

func (s *Service) check(ctx context.Context) (CheckResult, error) {
	if s.d.Installed == nil {
		return CheckResult{}, errors.New("marketplace: the module runtime is not composed")
	}
	installed, err := s.d.Installed.InstalledModules(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{Checked: len(installed)}
	if len(installed) == 0 {
		return res, nil
	}
	ids := make([]string, 0, len(installed))
	byID := map[string]InstalledModule{}
	for _, m := range installed {
		if _, dup := byID[m.ID]; !dup {
			ids = append(ids, m.ID)
		}
		byID[m.ID] = m
	}
	sort.Strings(ids)
	alerts, err := s.d.Client.Alerts(ctx, ids, time.Time{})
	if err != nil {
		return res, err
	}
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].CreatedAt.Before(alerts[j].CreatedAt) })
	for _, a := range alerts {
		m, ok := byID[a.ModuleID]
		if !ok || !applies(a, m) || strings.TrimSpace(a.ID) == "" || (a.Kind != KindFlag && a.Kind != KindPull) {
			continue
		}
		got, delivered, err := s.deliver(ctx, a, m)
		if err != nil {
			return res, err
		}
		if delivered {
			res.New = append(res.New, got)
		}
	}
	return res, nil
}

// system is the actor for alerts the core raises itself.
var system = domain.Principal{Source: domain.SourceSystem}

// deliver stores one alert and, when it is new, notifies and emails the admins in the same
// transaction, with its audit row. A second check sees the stored source id and does
// nothing.
func (s *Service) deliver(ctx context.Context, a Alert, m InstalledModule) (ModuleAlert, bool, error) {
	id, err := domain.NewID()
	if err != nil {
		return ModuleAlert{}, false, err
	}
	now := s.now()
	note := clip(strings.TrimSpace(a.Note))
	out := ModuleAlert{
		Meta:          domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: domain.SystemUserID},
		SourceAlertID: a.ID, ModuleID: a.ModuleID, Version: a.Version, InstalledVersion: m.Version,
		Kind: a.Kind, Note: note, Warning: Warning(a.Kind, note), RaisedAt: a.CreatedAt.UTC(),
	}
	if out.RaisedAt.IsZero() {
		out.RaisedAt = now
	}
	admins, err := s.admins(ctx)
	if err != nil {
		return ModuleAlert{}, false, err
	}
	var delivered bool
	err = s.d.Audit.Atomic(ctx, func(ctx context.Context) error {
		q := audit.Conn(ctx, s.d.DB)
		r, err := q.ExecContext(ctx, `INSERT INTO module_alerts (id, created_at, updated_at, created_by, source_alert_id, module_id, version, installed_version, kind, note, warning, raised_at)
			VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (source_alert_id) DO NOTHING`,
			string(id), micro(now), string(domain.SystemUserID), out.SourceAlertID, out.ModuleID, out.Version, out.InstalledVersion, out.Kind, out.Note, out.Warning, micro(out.RaisedAt))
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == int64(0) {
			return nil
		}
		delivered = true
		if _, err := s.d.Audit.Record(ctx, system, audit.Change{Entity: alertEntity, EntityID: id, Field: "kind", New: out.Kind, Reason: "marketplace " + out.Kind + " on installed module " + out.ModuleID}); err != nil {
			return err
		}
		title, subject, body := alertText(out)
		for _, u := range admins {
			inApp, email, err := s.channels(ctx, u.ID)
			if err != nil {
				return err
			}
			if inApp {
				if err := insertNotification(ctx, q, u.ID, title, now); err != nil {
					return err
				}
			}
			if email && strings.TrimSpace(u.Email) != "" {
				if err := queueMail(ctx, q, id, u.Email, subject, body, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return out, delivered, err
}

func clip(s string) string {
	r := []rune(s)
	if len(r) > fixed.MaxNoteRunes {
		return string(r[:fixed.MaxNoteRunes])
	}
	return s
}

func alertText(a ModuleAlert) (title, subject, body string) {
	what := "flagged"
	if a.Kind == KindPull {
		what = "pulled from the marketplace"
	}
	name := a.ModuleID
	if a.Version != "" {
		name += " " + a.Version
	}
	title = "Module " + name + " was " + what
	subject = "Togavsu module alert: " + name + " was " + what
	var b strings.Builder
	fmt.Fprintf(&b, "The Togavsu marketplace reports that the module %s, which this firm runs (version %s), was %s.\n\n", a.ModuleID, a.InstalledVersion, what)
	b.WriteString(a.Warning + "\n\n")
	if a.Note != "" {
		b.WriteString("Operator's note: " + a.Note + "\n\n")
	}
	b.WriteString(decision + "\n\n")
	b.WriteString("Open Admin > Modules and marketplace: " + AlertsLink + "\n")
	return title, subject, b.String()
}

func insertNotification(ctx context.Context, q audit.Querier, user domain.ID, title string, now time.Time) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO notifications (id, created_at, updated_at, created_by, user_id, kind, title, link, matter_id, read_at) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,NULL,0)`,
		string(id), micro(now), string(domain.SystemUserID), string(user), NotificationKind, title, AlertsLink)
	return err
}

func queueMail(ctx context.Context, q audit.Querier, record domain.ID, to, subject, body string, now time.Time) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO mail_outbox (id, created_at, updated_at, created_by, kind, record_id, to_address, subject, body) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8)`,
		string(id), micro(now), string(domain.SystemUserID), NotificationKind, string(record), to, subject, body)
	return err
}

// admins is every active admin, through the user repository's pages.
func (s *Service) admins(ctx context.Context) ([]domain.User, error) {
	var out []domain.User
	cursor := ""
	for {
		page, err := s.d.Users.List(ctx, repository.Filter{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, u := range page.Items {
			roles, err := domain.ParseRoles(u.Roles)
			if err != nil || !u.Active {
				continue
			}
			if (domain.Principal{Roles: roles}).HasRole(domain.RoleAdmin) {
				out = append(out, u)
			}
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// channels is the user's module-alert channels (PRD 8.17): the firm defaults in
// notifications.defaults, with the user's own choice on top. Email to the admins is the
// default; an admin may turn it off for themself.
func (s *Service) channels(ctx context.Context, user domain.ID) (inApp, email bool, err error) {
	var f struct {
		Email []string `json:"email"`
		InApp []string `json:"in_app"`
	}
	if json.Unmarshal([]byte(s.setting(ctx, "notifications.defaults")), &f) != nil {
		_ = json.Unmarshal([]byte(domain.DefaultNotificationPrefs), &f)
	}
	inApp, email = contains(f.InApp, NotificationKind), contains(f.Email, NotificationKind)
	var a, e int
	err = audit.Conn(ctx, s.d.DB).QueryRowContext(ctx, `SELECT in_app, email FROM notification_preferences WHERE user_id = $1 AND kind = $2`, string(user), NotificationKind).Scan(&a, &e)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return inApp, email, nil
	case err != nil:
		return false, false, err
	}
	return a != int(0), e != int(0), nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) recordCheck(ctx context.Context, failure error) {
	now := micro(s.now())
	ctx = context.WithoutCancel(ctx)
	var err error
	if failure == nil {
		_, err = s.d.DB.ExecContext(ctx, `INSERT INTO marketplace_checks (id, attempted_at, succeeded_at, last_error) VALUES (1, $1, $1, '')
			ON CONFLICT (id) DO UPDATE SET attempted_at = excluded.attempted_at, succeeded_at = excluded.succeeded_at, last_error = ''`, now)
	} else {
		_, err = s.d.DB.ExecContext(ctx, `INSERT INTO marketplace_checks (id, attempted_at, succeeded_at, last_error) VALUES (1, $1, 0, $2)
			ON CONFLICT (id) DO UPDATE SET attempted_at = excluded.attempted_at, last_error = excluded.last_error`, now, clip(failure.Error()))
	}
	if err != nil && s.d.Logf != nil {
		s.d.Logf("marketplace: record check: %v", err)
	}
}

func (s *Service) status(ctx context.Context) (CheckStatus, error) {
	var a, ok int64
	var st CheckStatus
	err := s.d.DB.QueryRowContext(ctx, `SELECT attempted_at, succeeded_at, last_error FROM marketplace_checks WHERE id = 1`).Scan(&a, &ok, &st.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return CheckStatus{}, nil
	}
	if err != nil {
		return CheckStatus{}, err
	}
	if a != int64(0) {
		st.AttemptedAt = fromMicro(a)
	}
	if ok != int64(0) {
		st.SucceededAt = fromMicro(ok)
	}
	return st, nil
}

// Status is the last check's outcome.
func (s *Service) Status(ctx context.Context, p domain.Principal) (CheckStatus, error) {
	if err := admin(p); err != nil {
		return CheckStatus{}, err
	}
	return s.status(ctx)
}

// Due reports whether the scheduled check should run now: one period of
// modules.alert_check after the last success, or a short retry after a failure.
func (s *Service) Due(ctx context.Context) (bool, error) {
	st, err := s.status(ctx)
	if err != nil {
		return false, err
	}
	now := s.now()
	if st.AttemptedAt.IsZero() {
		return true, nil
	}
	if st.LastError != "" {
		retry := minutes(fixed.RetryAfterFailureMin)
		if period := AlertPeriod(s.setting(ctx, "modules.alert_check")); period < retry {
			retry = period
		}
		return !now.Before(st.AttemptedAt.Add(retry)), nil
	}
	return !now.Before(st.SucceededAt.Add(AlertPeriod(s.setting(ctx, "modules.alert_check")))), nil
}

// RunDue runs the scheduled check when it is due.
func (s *Service) RunDue(ctx context.Context) (bool, CheckResult, error) {
	due, err := s.Due(ctx)
	if err != nil || !due {
		return false, CheckResult{}, err
	}
	res, err := s.Check(ctx)
	return true, res, err
}

// Start runs RunDue in the background until ctx ends. The schedule is read on every
// tick, so a change to modules.alert_check applies with no restart. logf may be nil.
//
// ponytail: ceiling: an in-process ticker per core, not the Postgres job queue; two cores
// during an update swap may both check, and the unique source id keeps delivery once.
// Upgrade path: enqueue a marketplace-poll job. trigger: the core job queue ships
// (PRD section 6).
func (s *Service) Start(ctx context.Context, logf func(format string, args ...any)) {
	go func() {
		t := time.NewTicker(seconds(fixed.DuePollSeconds))
		defer t.Stop()
		for {
			if _, _, err := s.RunDue(ctx); err != nil && ctx.Err() == nil && logf != nil {
				logf("marketplace: alert check: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// OpenModulesScreen is what the modules screen calls when an admin opens it: a check
// (unless one ran moments ago), then the alerts. A failed check does not hide the stored
// alerts; it comes back in the status.
func (s *Service) OpenModulesScreen(ctx context.Context, p domain.Principal) ([]ModuleAlert, CheckStatus, error) {
	if err := admin(p); err != nil {
		return nil, CheckStatus{}, err
	}
	st, err := s.status(ctx)
	if err != nil {
		return nil, CheckStatus{}, err
	}
	if st.AttemptedAt.IsZero() || !s.now().Before(st.AttemptedAt.Add(seconds(fixed.CheckOnOpenSeconds))) {
		_, _ = s.Check(ctx)
	}
	if st, err = s.status(ctx); err != nil {
		return nil, CheckStatus{}, err
	}
	alerts, err := s.alerts(ctx, "")
	return alerts, st, err
}

// Alerts lists the delivered alerts, newest first.
func (s *Service) Alerts(ctx context.Context, p domain.Principal) ([]ModuleAlert, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	return s.alerts(ctx, "")
}

// Alert reads one delivered alert.
func (s *Service) Alert(ctx context.Context, p domain.Principal, id domain.ID) (ModuleAlert, error) {
	if err := admin(p); err != nil {
		return ModuleAlert{}, err
	}
	as, err := s.alerts(ctx, id)
	if err != nil {
		return ModuleAlert{}, err
	}
	if len(as) == 0 {
		return ModuleAlert{}, domain.NotFound("module alert")
	}
	return as[0], nil
}

func (s *Service) alerts(ctx context.Context, id domain.ID) ([]ModuleAlert, error) {
	rows, err := s.d.DB.QueryContext(ctx, `SELECT id, created_at, updated_at, created_by, source_alert_id, module_id, version, installed_version, kind, note, warning, raised_at
		FROM module_alerts WHERE ($1 = '' OR id = $1) ORDER BY id DESC`, string(id))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModuleAlert
	for rows.Next() {
		var a ModuleAlert
		var c, u, r int64
		if err := rows.Scan(&a.ID, &c, &u, &a.CreatedBy, &a.SourceAlertID, &a.ModuleID, &a.Version, &a.InstalledVersion, &a.Kind, &a.Note, &a.Warning, &r); err != nil {
			return nil, err
		}
		a.CreatedAt, a.UpdatedAt, a.RaisedAt = fromMicro(c), fromMicro(u), fromMicro(r)
		out = append(out, a)
	}
	return out, rows.Err()
}

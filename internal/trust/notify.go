package trust

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/smtpsend"
)

// recipient is one person a trust event tells. sees is false when the matter's wall (or
// the person's role) keeps them from reading the matter's trust records.
type recipient struct {
	user domain.User
	sees bool
}

// canSee applies the service's own trust read rule, walls included, to u as if u made
// the request. A user whose roles do not parse sees nothing.
func (s *Service) canSee(ctx context.Context, u domain.User, matter domain.ID) (bool, error) {
	roles, err := domain.ParseRoles(u.Roles)
	if err != nil {
		return false, nil
	}
	r := &readable{s: s, who: domain.Principal{UserID: u.ID, Roles: roles}, seen: map[domain.ID]bool{}}
	return r.ok(ctx, matter)
}

// activeUser reads id; ok is false when the user is missing or deactivated.
func (s *Service) activeUser(ctx context.Context, id domain.ID) (domain.User, bool, error) {
	if id.IsZero() {
		return domain.User{}, false, nil
	}
	u, err := s.users.Get(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return domain.User{}, false, nil
		}
		return domain.User{}, false, err
	}
	return u, u.Active, nil
}

// flagRecipients is every active admin and the matter's active responsible attorney,
// each marked with whether the matter's wall lets them read it. PRD 8.4 and 8.17: the
// below-zero email to the admins and the responsible attorney always goes out, so a
// walled-out recipient is kept, but raise sends them a notice without the matter's
// identifiers or balances and no in-app notification (PRD 3: walls apply on every
// surface).
func (s *Service) flagRecipients(ctx context.Context, matter domain.Matter) ([]recipient, error) {
	page, err := s.users.List(ctx, repository.Filter{})
	if err != nil {
		return nil, err
	}
	seen := map[domain.ID]bool{}
	var out []recipient
	add := func(u domain.User) error {
		ok, err := s.canSee(ctx, u, matter.ID)
		if err != nil {
			return err
		}
		seen[u.ID] = true
		out = append(out, recipient{user: u, sees: ok})
		return nil
	}
	for _, u := range page.Items {
		if !u.Active || seen[u.ID] {
			continue
		}
		roles, err := domain.ParseRoles(u.Roles)
		if err != nil {
			continue
		}
		if (domain.Principal{Roles: roles}).HasRole(domain.RoleAdmin) {
			if err := add(u); err != nil {
				return nil, err
			}
		}
	}
	if !seen[matter.ResponsibleAttorneyID] {
		// A deactivated attorney gets neither the notification nor the email.
		u, ok, err := s.activeUser(ctx, matter.ResponsibleAttorneyID)
		if err != nil {
			return nil, err
		}
		if ok {
			if err := add(u); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func isNotFound(err error) bool {
	return notFound(err, "") != err
}

// mailTo is one below-zero email address and the people behind it.
type mailTo struct {
	addr  string
	users []domain.User
	// extra is a trust.flag_email_extra address: not a user the wall can name.
	extra bool
}

// addrSees reports whether the address may get matter m's trust amounts. An address
// shared by several users gets a matter's amounts when any of them reads it; an extra
// address gets them only for a matter with no wall.
func (s *Service) addrSees(t *mailTo) func(context.Context, domain.ID) (bool, error) {
	seen := map[domain.ID]bool{}
	return func(ctx context.Context, m domain.ID) (bool, error) {
		if v, hit := seen[m]; hit {
			return v, nil
		}
		v := false
		if t.extra {
			ok, err := s.unwalled(ctx, m)
			if err != nil {
				return false, err
			}
			v = ok
		}
		for _, u := range t.users {
			if v {
				break
			}
			ok, err := s.canSee(ctx, u, m)
			if err != nil {
				return false, err
			}
			v = ok
		}
		seen[m] = v
		return v, nil
	}
}

// unwalled reports whether matter m has no wall: the rule for a reader no wall names
// (an extra email address, a commit event built before delivery).
func (s *Service) unwalled(ctx context.Context, m domain.ID) (bool, error) {
	view, err := s.policy.Walls.View(ctx, domain.Principal{}, m)
	if err != nil {
		return false, err
	}
	return !view.Walled, nil
}

// flagAddresses is the recipients' email plus trust.flag_email_extra, once each, in
// order. addrSees decides per address and per matter what the email may show.
func (s *Service) flagAddresses(ctx context.Context, people []recipient) []*mailTo {
	by := map[string]*mailTo{}
	var order []*mailTo
	add := func(a string) *mailTo {
		a = trimmed(a)
		k := strings.ToLower(a)
		if a == "" || !strings.Contains(a, "@") {
			return nil
		}
		t, dup := by[k]
		if !dup {
			t = &mailTo{addr: a}
			by[k] = t
			order = append(order, t)
		}
		return t
	}
	for _, r := range people {
		if t := add(r.user.Email); t != nil {
			t.users = append(t.users, r.user)
		}
	}
	for _, a := range strings.FieldsFunc(s.setting(ctx, "trust.flag_email_extra"), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n'
	}) {
		if t := add(a); t != nil {
			t.extra = true
		}
	}
	return order
}

func (s *Service) notify(ctx context.Context, who domain.Principal, user domain.ID, kind, title, link string, matter domain.ID) error {
	id, err := s.newID()
	if err != nil {
		return err
	}
	now := s.now()
	var m any
	if !matter.IsZero() {
		m = string(matter)
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO notifications (id, created_at, updated_at, created_by, user_id, kind, title, link, matter_id, read_at) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8,0)`,
		string(id), micro(now), string(actorID(who)), string(user), kind, title, link, m)
	return err
}

func (s *Service) queueMail(ctx context.Context, who domain.Principal, kind string, record domain.ID, to, subject, body string) error {
	id, err := s.newID()
	if err != nil {
		return err
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO mail_outbox (id, created_at, updated_at, created_by, kind, record_id, to_address, subject, body) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8)`,
		string(id), micro(s.now()), string(actorID(who)), kind, string(record), to, subject, body)
	return err
}

// Section 8.9 notifications live here because trust is the first core event that notifies
// (AC-021).
// ponytail: ceiling: the notifications table, its routes and Mark all read sit in the trust
// package; the shell bell and nav/context badge are not wired. Upgrade path: move
// notify, Notifications, UnreadCount, MarkRead and MarkAllRead to the shell package
// unchanged. Trigger: the ui assignment adds the header bell or a second notifying domain.

// Notifications lists the caller's own notifications, newest first. read filters by read
// state when set. A notification about a matter the caller can no longer see is absent.
func (s *Service) Notifications(ctx context.Context, who domain.Principal, read *bool, cursor string, limit int) (Page[Notification], error) {
	if who.UserID.IsZero() {
		return Page[Notification]{}, domain.Unauthenticated("sign in is required")
	}
	limit, err := s.pageLimit(ctx, limit)
	if err != nil {
		return Page[Notification]{}, err
	}
	rows, err := s.notifications(ctx, who, read)
	if err != nil {
		return Page[Notification]{}, err
	}
	var start int
	if cursor != "" {
		start = len(rows)
		for i, n := range rows {
			if string(n.ID) < cursor {
				start = i
				break
			}
		}
	}
	page := Page[Notification]{Limit: limit, Items: []Notification{}}
	end := start + limit
	if end < len(rows) {
		page.Items, page.NextCursor = rows[start:end], string(rows[end-1].ID)
	} else if start < len(rows) {
		page.Items = rows[start:]
	}
	return page, nil
}

// notifications is the caller's visible notifications, newest first, filtered by read
// state when read is set.
func (s *Service) notifications(ctx context.Context, who domain.Principal, read *bool) ([]Notification, error) {
	all, err := s.ownNotifications(ctx, who)
	if err != nil {
		return nil, err
	}
	var rows []Notification
	for i := len(all) - 1; i >= 0; i-- {
		if n := all[i]; read == nil || (n.ReadAt != nil) == *read {
			rows = append(rows, n)
		}
	}
	return rows, nil
}

// UnreadCount is the bell count: the caller's own unread notifications they may see.
func (s *Service) UnreadCount(ctx context.Context, who domain.Principal) (int, error) {
	if who.UserID.IsZero() {
		return 0, domain.Unauthenticated("sign in is required")
	}
	unread := false
	rows, err := s.notifications(ctx, who, &unread)
	return len(rows), err
}

func (s *Service) ownNotifications(ctx context.Context, who domain.Principal) ([]Notification, error) {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, created_at, created_by, updated_at, user_id, kind, title, link, matter_id, read_at FROM notifications WHERE user_id = $1 ORDER BY id`, string(who.UserID))
	if err != nil {
		return nil, err
	}
	var all []Notification
	for rows.Next() {
		var n Notification
		var created, updated, read int64
		var matter *string
		if err := rows.Scan(&n.ID, &created, &n.CreatedBy, &updated, &n.UserID, &n.Kind, &n.Title, &n.Link, &matter, &read); err != nil {
			rows.Close()
			return nil, err
		}
		n.CreatedAt, n.UpdatedAt = fromMicro(created), fromMicro(updated)
		if read != int64(0) {
			t := fromMicro(read)
			n.ReadAt = &t
		}
		if matter != nil {
			n.matterID = domain.ID(*matter)
		}
		all = append(all, n)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// A notification about a walled matter is absent. Trust kinds use the trust read rule,
	// so an accountant keeps a trust flag notification it was sent.
	r := &readable{s: s, who: who, seen: map[domain.ID]bool{}}
	var out []Notification
	for _, n := range all {
		if !n.matterID.IsZero() {
			ok, err := r.ok(ctx, n.matterID)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		out = append(out, n)
	}
	return out, nil
}

// MarkRead marks one of the caller's notifications read. Another user's id is not found.
func (s *Service) MarkRead(ctx context.Context, who domain.Principal, id domain.ID) (Notification, error) {
	all, err := s.ownNotifications(ctx, who)
	if err != nil {
		return Notification{}, err
	}
	for _, n := range all {
		if n.ID != id {
			continue
		}
		if n.ReadAt == nil {
			now := s.now()
			if _, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = $1, updated_at = $1 WHERE id = $2 AND user_id = $3`, micro(now), string(id), string(who.UserID)); err != nil {
				return Notification{}, err
			}
			n.ReadAt, n.UpdatedAt = &now, now
		}
		return n, nil
	}
	return Notification{}, domain.NotFound("notification")
}

// MarkAllRead clears the caller's unread count and returns how many it marked.
func (s *Service) MarkAllRead(ctx context.Context, who domain.Principal) (int, error) {
	if who.UserID.IsZero() {
		return 0, domain.Unauthenticated("sign in is required")
	}
	now := micro(s.now())
	res, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = $1, updated_at = $1 WHERE user_id = $2 AND read_at = 0`, now, string(who.UserID))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Sender delivers one system mail.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Mail is one outbox row.
type Mail struct {
	ID       domain.ID
	Kind     string
	RecordID domain.ID
	To       string
	Subject  string
	Body     string
	SentAt   *time.Time
	Attempts int
	Error    string
}

// Outbox lists queued system mail for record (all records when empty), oldest first.
// It is for the admin problem view and tests; it does not check a principal.
func (s *Service) Outbox(ctx context.Context, record domain.ID) ([]Mail, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, record_id, to_address, subject, body, sent_at, attempts, last_error FROM mail_outbox WHERE ($1 = '' OR record_id = $1) ORDER BY id`, string(record))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mail
	for rows.Next() {
		var m Mail
		var sent int64
		if err := rows.Scan(&m.ID, &m.Kind, &m.RecordID, &m.To, &m.Subject, &m.Body, &sent, &m.Attempts, &m.Error); err != nil {
			return nil, err
		}
		if sent != int64(0) {
			t := fromMicro(sent)
			m.SentAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeliverMail sends every unsent outbox row through sender. A failure stays queued with
// its error and is retried on the next run. It returns the number sent.
// ponytail: ceiling: no backoff and no attempt cap; the core job runner calls it on its
// schedule. Upgrade path: the Postgres job queue owns retries with backoff. Trigger: the
// job queue ships (PRD section 6).
func (s *Service) DeliverMail(ctx context.Context, sender Sender) (int, error) {
	pending, err := s.Outbox(ctx, "")
	if err != nil {
		return 0, err
	}
	var sent int
	for _, m := range pending {
		if m.SentAt != nil {
			continue
		}
		now := micro(s.now())
		if err := sender.Send(ctx, m.To, m.Subject, m.Body); err != nil {
			// A send cut off by ctx's deadline or cancellation is still recorded, so the
			// admin sees why the row is waiting.
			if _, err := s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE mail_outbox SET attempts = attempts + 1, last_error = $1, updated_at = $2 WHERE id = $3`, err.Error(), now, string(m.ID)); err != nil {
				return sent, err
			}
			continue
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE mail_outbox SET attempts = attempts + 1, sent_at = $1, last_error = '', updated_at = $1 WHERE id = $2`, now, string(m.ID)); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// SMTP sends through the admin's mail.smtp_* settings, read on every send so a change
// applies without a restart.
type SMTP struct {
	Settings *settings.Registry
}

// Send implements Sender.
func (m SMTP) Send(ctx context.Context, to, subject, body string) error {
	get := func(k string) string {
		v, _ := m.Settings.Get(ctx, k)
		return v
	}
	host, from := get("mail.smtp_host"), get("mail.smtp_from")
	if host == "" || from == "" {
		return fmt.Errorf("trust: system mail is not configured (mail.smtp_host and mail.smtp_from)")
	}
	port := get("mail.smtp_port")
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("trust: system mail is not configured (mail.smtp_port)")
	}
	var auth smtp.Auth
	if user := get("mail.smtp_username"); user != "" {
		auth = smtp.PlainAuth("", user, get("mail.smtp_password"), host)
	}
	msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + headerSafe(subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	limit, err := time.ParseDuration(get("mail.smtp_timeout"))
	if err != nil {
		return fmt.Errorf("trust: system mail is not configured (mail.smtp_timeout)")
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return smtpsend.Send(ctx, net.JoinHostPort(host, port), auth, from, []string{to}, []byte(msg))
}

func headerSafe(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

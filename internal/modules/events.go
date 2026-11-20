package modules

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// SignatureHeader carries t=<unix>,v1=<hex>: HMAC-SHA256 of timestamp + "." + raw body,
// keyed with the nonempty module webhook secret.
const SignatureHeader = "X-Togavsu-Signature"

// EventPayload is what a module receives for one event. It names the record; the module
// reads the record through the API, where access applies.
type EventPayload struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	RecordType string `json:"record_type"`
	RecordID   string `json:"record_id"`
	MatterID   string `json:"matter_id,omitempty"`
	OccurredAt string `json:"occurred_at"`
	Module     string `json:"module"`
	// BackgroundFileAuthorization is set on file.background_authorized, which goes only
	// to the module the grant names.
	BackgroundFileAuthorization json.RawMessage `json:"background_file_authorization,omitempty"`
}

// grantEvent is the action whose delivery carries a background file publication grant.
const grantEvent = "file.background_authorized"

// grantRecipient reads the grant behind a file.background_authorized event: the one
// module it is delivered to, and the authorization it carries.
func (s *Service) grantRecipient(ctx context.Context, grant domain.ID) (string, json.RawMessage, error) {
	var module, body string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT module_id, authorization_body FROM core.file_publication_grants WHERE id = $1`, string(grant)).Scan(&module, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	return module, json.RawMessage(body), err
}

// Subscribed reports whether a manifest subscription list matches action.
func Subscribed(subs []string, action string) bool {
	for _, s := range subs {
		if s == action {
			return true
		}
		if prefix, ok := strings.CutSuffix(s, ".*"); ok && strings.HasPrefix(action, prefix+".") {
			return true
		}
	}
	return false
}

const cursorID = "module-events"

// Pump reads events from core.events and queues one delivery per subscribed, enabled
// module with events:read approved. Every write in the core adds an event row, so this
// delivers every subscribed write without each domain calling the module runtime. A
// module's own lifecycle events are not sent to modules.
//
// core.events.created_at is set by the writer before its transaction commits, so an
// event can become visible after a later one. The cursor is therefore a safe horizon,
// not the last row seen: each pump rescans every event at or after the horizon, and the
// (module_id, event_id) unique index keeps a rescanned event from being queued twice.
// The horizon only moves up to the start of the oldest open transaction in this
// database, less a lookback margin for writers that stamp created_at just before BEGIN
// or whose sessions this role can't see.
//
// ponytail: ceiling: each pump rescans the events of the lookback window plus the
// oldest open transaction; a long open transaction (a pg_dump) widens the rescan.
// Upgrade path: a commit-ordered outbox written in the writer's transaction. Trigger:
// a pump takes longer than its tick or rescans more than a few thousand events.
func (s *Service) Pump(ctx context.Context) (int, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return 0, err
	}
	queued := zero
	err = s.commit(ctx, func(ctx context.Context) error {
		horizon, err := s.pumpHorizon(ctx)
		if err != nil {
			return err
		}
		safe, err := s.safeHorizon(ctx)
		if err != nil {
			return err
		}
		pageAt, pageID := horizon, "" // id > '' takes every event stamped at the horizon
		for {
			rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, created_at, action, record_type, record_id, COALESCE(matter_id, '') FROM core.events
WHERE created_at > $1 OR (created_at = $1 AND id > $2) ORDER BY created_at, id LIMIT $3`, pageAt, pageID, limits().PumpBatch)
			if err != nil {
				return err
			}
			type ev struct {
				id, action, rtype, rid, matter string
				at                             int64
			}
			var evs []ev
			for rows.Next() {
				var e ev
				if err := rows.Scan(&e.id, &e.at, &e.action, &e.rtype, &e.rid, &e.matter); err != nil {
					rows.Close()
					return err
				}
				evs = append(evs, e)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, e := range evs {
				n, err := s.enqueue(ctx, mods, domain.Event{Meta: domain.Meta{ID: domain.ID(e.id), CreatedAt: time.UnixMicro(e.at).UTC()}, Action: e.action,
					RecordType: e.rtype, RecordID: domain.ID(e.rid), MatterID: domain.ID(e.matter)})
				if err != nil {
					return err
				}
				queued += n
			}
			if len(evs) < limits().PumpBatch {
				break
			}
			last := evs[len(evs)-1]
			pageAt, pageID = last.at, last.id
		}
		// Every event stamped before safe has committed and was visible to this scan.
		next := horizon
		if safe > next {
			next = safe
		}
		_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.module_event_cursor SET last_created_at = $1, last_event_id = '', updated_at = $2 WHERE id = $3`,
			next, s.now().UnixMicro(), cursorID)
		return err
	})
	return queued, err
}

// pumpHorizon locks and returns the pump cursor, creating it on the first pump.
func (s *Service) pumpHorizon(ctx context.Context) (int64, error) {
	var at int64
	err := s.q(ctx).QueryRowContext(ctx, `SELECT last_created_at FROM core.module_event_cursor WHERE id = $1 FOR UPDATE`, cursorID).Scan(&at)
	if !errors.Is(err, sql.ErrNoRows) {
		return at, err
	}
	now := s.now().UnixMicro()
	if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_event_cursor (id, created_at, updated_at, created_by, last_created_at, last_event_id) VALUES ($1,$2,$2,'',0,'') ON CONFLICT (id) DO NOTHING`, cursorID, now); err != nil {
		return 0, err
	}
	err = s.q(ctx).QueryRowContext(ctx, `SELECT last_created_at FROM core.module_event_cursor WHERE id = $1 FOR UPDATE`, cursorID).Scan(&at)
	return at, err
}

// safeHorizon is the earliest created_at a not-yet-visible event could still carry:
// the start of the oldest other open transaction in this database, or now, less the
// lookback margin.
func (s *Service) safeHorizon(ctx context.Context) (int64, error) {
	var oldest sql.NullInt64
	if err := s.q(ctx).QueryRowContext(ctx, `SELECT (EXTRACT(EPOCH FROM MIN(xact_start)) * 1000000)::BIGINT FROM pg_stat_activity
WHERE datname = current_database() AND pid <> pg_backend_pid() AND xact_start IS NOT NULL`).Scan(&oldest); err != nil {
		return 0, err
	}
	at := s.now().UnixMicro()
	if oldest.Valid && oldest.Int64 < at {
		at = oldest.Int64
	}
	lookback, err := time.ParseDuration(strconv.Itoa(int(limits().PumpLookbackSeconds)) + "s")
	if err != nil {
		return 0, err
	}
	return at - lookback.Microseconds(), nil
}

// enqueue queues one delivery of ev per subscribed, enabled module with events:read.
// A module's own lifecycle events are not sent to modules. A repeated event id is
// queued once per module.
func (s *Service) enqueue(ctx context.Context, mods []*Installed, ev domain.Event) (int, error) {
	if ev.RecordType == "module" || ev.ID.IsZero() || directEvents[ev.Action] {
		return zero, nil
	}
	queued := zero
	now := s.now().UnixMicro()
	at := ev.CreatedAt
	if at.IsZero() {
		at = s.now()
	}
	var recipient string
	var grant json.RawMessage
	if ev.Action == grantEvent {
		var err error
		if recipient, grant, err = s.grantRecipient(ctx, ev.RecordID); err != nil || recipient == "" {
			return zero, err
		}
	}
	for _, m := range mods {
		switch {
		case recipient != "":
			// The grant goes to the module it names, and to no other.
			if m.ID != recipient {
				continue
			}
		case !m.Approved["events:read"] || !Subscribed(m.Manifest.Events, ev.Action):
			continue
		}
		if m.Approved["books:read"] {
			drop, err := s.dropBooksEvent(ctx, m.ID, ev)
			if err != nil {
				return queued, err
			}
			if drop {
				continue
			}
		}
		body, err := json.Marshal(EventPayload{ID: string(ev.ID), Action: ev.Action, RecordType: ev.RecordType, RecordID: string(ev.RecordID),
			MatterID: string(ev.MatterID), OccurredAt: at.UTC().Format(time.RFC3339Nano), Module: m.ID, BackgroundFileAuthorization: grant})
		if err != nil {
			return queued, err
		}
		id, err := domain.NewID()
		if err != nil {
			return queued, err
		}
		res, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id, created_at, updated_at, created_by, module_id, event_id, action, payload, status, attempts, next_attempt_at, last_error, delivered_at)
VALUES ($1,$2,$2,'',$3,$4,$5,$6,'pending',0,$2,'',0) ON CONFLICT (module_id, event_id) DO NOTHING`, string(id), now, m.ID, string(ev.ID), ev.Action, string(body))
		if err != nil {
			return queued, err
		}
		if n, _ := res.RowsAffected(); n > int64(zero) {
			queued++
		}
	}
	return queued, nil
}

// dropBooksEvent reports whether a books:read module must not receive ev (CORE-02):
// money events on a walled matter, and contact.updated for a contact getBooksContact
// would not return. moduleID authenticates the books-reader principal used for contact
// payment/trust lookups so an Unauthenticated error cannot stall the pump cursor.
func (s *Service) dropBooksEvent(ctx context.Context, moduleID string, ev domain.Event) (bool, error) {
	if booksMoneyEvent(ev.Action) && !ev.MatterID.IsZero() {
		if s.d.Walls == nil {
			return true, nil
		}
		view, err := s.d.Walls.View(ctx, domain.Principal{
			Source: domain.SourceService, ModuleID: moduleID, Roles: []domain.Role{domain.RoleAccountant},
		}, ev.MatterID)
		if err != nil {
			return false, err
		}
		return view.Walled && !view.Allows, nil
	}
	if ev.Action == "contact.updated" {
		if s.d.BooksContactOK == nil {
			return true, nil
		}
		ok, err := s.d.BooksContactOK(ctx, moduleID, ev.RecordID)
		if err != nil {
			return false, err
		}
		return !ok, nil
	}
	return false, nil
}

func booksMoneyEvent(action string) bool {
	for _, prefix := range []string{"invoice.", "payment.", "expense.", "trust."} {
		if strings.HasPrefix(action, prefix) {
			return true
		}
	}
	return false
}

// Publish queues deliveries for events a domain keeps outside core.events (billing and
// practice keep theirs in their record stores today). The core calls it after the
// write commits; Pump covers the rows in core.events.
func (s *Service) Publish(ctx context.Context, events ...domain.Event) (int, error) {
	mods, err := s.Enabled(ctx)
	if err != nil {
		return 0, err
	}
	total := zero
	err = s.commit(ctx, func(ctx context.Context) error {
		for _, ev := range events {
			n, err := s.enqueue(ctx, mods, ev)
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	return total, err
}

// retryDelay spreads attempts over the webhook window: delays double, and all of them
// together add up to the window (PRD 8.7 default: 8 attempts over 24 hours).
func retryDelay(attempt, attempts int, window time.Duration) time.Duration {
	if attempts <= one {
		return window
	}
	total := float64(uint64(one)<<uint(attempts-one)) - float64(one)
	return time.Duration(float64(window) * float64(uint64(one)<<uint(attempt-one)) / total)
}

func (s *Service) intSetting(ctx context.Context, key string) (int, error) {
	v, err := s.d.Settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(v)
}

// TimestampHeader carries the decimal Unix delivery time (batch 1 wire format).
const TimestampHeader = "X-Togavsu-Timestamp"

// SignWebhook preserves the batch 2 raw-body signature API. For timestamped
// core deliveries use SignTimestampedWebhook or SignWebhookHeaders.
func SignWebhook(secret string, at time.Time, body []byte) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// SignTimestampedWebhook signs the v1.0.0 wire format. The timestamp is inside
// the HMAC, so changing it cannot extend the receiver's five-minute window.
func SignTimestampedWebhook(secret string, at time.Time, body []byte) string {
	if secret == "" {
		return ""
	}
	stamp := WebhookTimestamp(at)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook accepts the timestamped v1.0.0 signature and batch 2's raw-body
// sha256 signature. Use VerifyWebhookHeaders to check a separate timestamp too.
func VerifyWebhook(secret, header string, body []byte, now time.Time, tolerance time.Duration) error {
	return verifyWebhook(secret, header, "", body, now, tolerance)
}

func verifyWebhook(secret, header, stamp string, body []byte, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return errors.New("modules: empty webhook secret")
	}
	if tolerance == 0 {
		tolerance = 5 * time.Minute
	}
	if tolerance < 0 || tolerance > 5*time.Minute {
		return errors.New("modules: invalid webhook tolerance")
	}
	var digest, expected string
	if strings.HasPrefix(header, "t=") {
		signedStamp, sig, ok := strings.Cut(strings.TrimPrefix(header, "t="), ",v1=")
		if !ok || (stamp != "" && stamp != signedStamp) {
			return errors.New("modules: malformed or mismatched webhook timestamp")
		}
		stamp, digest = signedStamp, sig
		at, err := webhookTime(stamp)
		if err != nil {
			return err
		}
		_, expected, _ = strings.Cut(SignTimestampedWebhook(secret, at, body), ",v1=")
	} else if strings.HasPrefix(header, "sha256=") {
		digest = strings.TrimPrefix(header, "sha256=")
		expected = strings.TrimPrefix(SignWebhook(secret, now, body), "sha256=")
	} else {
		return errors.New("modules: malformed webhook signature")
	}
	if stamp != "" {
		at, err := webhookTime(stamp)
		if err != nil {
			return err
		}
		delta := now.Sub(at)
		if delta > tolerance || delta < -tolerance {
			return errors.New("modules: webhook timestamp outside window")
		}
	}
	provided, err := hex.DecodeString(digest)
	if err != nil || len(provided) != sha256.Size {
		return errors.New("modules: malformed webhook signature")
	}
	want, _ := hex.DecodeString(expected)
	if !hmac.Equal(want, provided) {
		return errors.New("modules: webhook signature doesn't match")
	}
	return nil
}

func webhookTime(stamp string) (time.Time, error) {
	at, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || strconv.FormatInt(at, 10) != stamp {
		return time.Time{}, errors.New("modules: missing or malformed webhook timestamp")
	}
	return time.Unix(at, 0), nil
}

// SignWebhookHeaders returns the core's timestamp-authenticated signature and the
// matching timestamp header. An empty secret returns nil; senders refuse delivery.
func SignWebhookHeaders(secret string, at time.Time, body []byte) http.Header {
	if secret == "" {
		return nil
	}
	return http.Header{SignatureHeader: {SignTimestampedWebhook(secret, at, body)}, TimestampHeader: {WebhookTimestamp(at)}}
}

// VerifyWebhookHeaders accepts both signature forms, checking the separate timestamp
// when present. For t=,v1= it must match the authenticated timestamp. Legacy sha256=
// timestamps are unsigned; durable event-id deduplication remains required.
func VerifyWebhookHeaders(secret string, headers http.Header, body []byte, now time.Time, tolerance time.Duration) error {
	if stamps, present := headers[http.CanonicalHeaderKey(TimestampHeader)]; present && (len(stamps) != 1 || stamps[0] == "") {
		return errors.New("modules: malformed webhook timestamp")
	}
	return verifyWebhook(secret, headers.Get(SignatureHeader), headers.Get(TimestampHeader), body, now, tolerance)
}

// Deliver sends every due delivery once, by signed webhook. A failure is retried on the
// schedule from api.webhook_attempts and api.webhook_window_hours; the last failure
// marks the delivery failed.
func (s *Service) Deliver(ctx context.Context) (sent int, err error) {
	attempts, err := s.intSetting(ctx, "api.webhook_attempts")
	if err != nil {
		return 0, err
	}
	hours, err := s.intSetting(ctx, "api.webhook_window_hours")
	if err != nil {
		return 0, err
	}
	window, err := time.ParseDuration(strconv.Itoa(hours) + "h")
	if err != nil {
		return 0, err
	}
	now := s.now()
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT d.id, d.module_id, d.payload, d.attempts, m.webhook_secret, d.task_authorization, d.created_by FROM core.module_deliveries d
JOIN core.modules m ON m.id = d.module_id WHERE d.status = 'pending' AND d.next_attempt_at <= $1 AND m.enabled = 1 ORDER BY d.next_attempt_at, d.id LIMIT $2`,
		now.UnixMicro(), limits().PumpBatch)
	if err != nil {
		return 0, err
	}
	type due struct {
		id, module, payload, secret, grant, sender string
		attempts                                   int
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.module, &d.payload, &d.attempts, &d.secret, &d.grant, &d.sender); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		secret, err := s.open(d.secret)
		if err != nil {
			return sent, err
		}
		grant, err := s.open(d.grant)
		if err != nil {
			return sent, err
		}
		derr := s.post(ctx, d.module, secret, grant, d.sender, []byte(d.payload))
		n := d.attempts + 1
		at := s.now()
		switch {
		case derr == nil:
			_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.module_deliveries SET status = 'delivered', attempts = $1, delivered_at = $2, last_error = '', updated_at = $2 WHERE id = $3`, n, at.UnixMicro(), d.id)
			sent++
		case n >= attempts:
			_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.module_deliveries SET status = 'failed', attempts = $1, last_error = $2, updated_at = $3 WHERE id = $4`, n, derr.Error(), at.UnixMicro(), d.id)
		default:
			next := at.Add(retryDelay(n, attempts, window))
			_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.module_deliveries SET attempts = $1, next_attempt_at = $2, last_error = $3, updated_at = $4 WHERE id = $5`, n, next.UnixMicro(), derr.Error(), at.UnixMicro(), d.id)
		}
		if err != nil {
			return sent, err
		}
	}
	return sent, nil
}

func (s *Service) post(ctx context.Context, module, secret, grant, sender string, body []byte) error {
	if secret == "" {
		return errors.New("modules: empty webhook secret")
	}
	in, err := s.Installed(ctx, module)
	if err != nil {
		return err
	}
	if s.d.Runtime == nil {
		return errors.New("no module runtime")
	}
	path := in.Manifest.Container.Events
	if path == "" {
		path = "/events"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.d.Runtime.BaseURL(module), "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	at := s.now()
	headers := SignWebhookSenderHeaders(secret, at, sender, body)
	// Only empty created_by identifies a core emission. A module-originated row
	// claiming a reserved identity must fail, including on retries of old rows.
	if sender == "" {
		headers = SignCoreWebhookHeaders(secret, at, body)
	}
	if headers == nil {
		return errors.New("modules: invalid or reserved webhook sender")
	}
	for key, values := range headers {
		req.Header[key] = values
	}
	if grant != "" {
		req.Header.Set(TaskAuthorizationHeader, grant)
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, limits().WebhookBodyBytes))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("module answered %s", resp.Status)
	}
	return nil
}

// Delivery is one webhook delivery for the API.
type Delivery struct {
	ID        string `json:"id"`
	Module    string `json:"module_id"`
	EventID   string `json:"event_id"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error"`
}

// Deliveries lists a module's deliveries, newest first (admin).
func (s *Service) Deliveries(ctx context.Context, p domain.Principal, module string) ([]Delivery, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, module_id, event_id, action, status, attempts, last_error FROM core.module_deliveries
WHERE ($1 = '' OR module_id = $1) ORDER BY created_at DESC, id DESC`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delivery{}
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.Module, &d.EventID, &d.Action, &d.Status, &d.Attempts, &d.LastError); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Run pumps and delivers until ctx ends, every interval. The core starts it beside its
// other background jobs.
// ponytail: ceiling: one in-process ticker, like the trust mail loop. Upgrade path: the
// Postgres job queue (PRD 6) owns delivery and retries. Trigger: the job queue ships.
func (s *Service) Run(ctx context.Context, every time.Duration, logf func(string, ...any)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.Pump(ctx); err != nil && ctx.Err() == nil {
			logf("modules: event pump: %v", err)
		}
		if _, err := s.Deliver(ctx); err != nil && ctx.Err() == nil {
			logf("modules: webhooks: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// WebhookTimestamp is the canonical decimal value for the separate timestamp header.
func WebhookTimestamp(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }

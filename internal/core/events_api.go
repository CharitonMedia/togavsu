package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/access"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// eventRoutes mount the firm's event stream (PRD 5 events, 8.13 Activities Feed): the
// feed every domain writes to core.events, and a module's own timeline and feed rows.
func (a *App) eventRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/events", a.listEventsJSON)
	m.HandleFunc("POST /api/v1/events", a.createEventJSON)
	m.HandleFunc("POST /api/v1/events:bulk", a.bulkEventJSON)
}

// eventWrite is the contract's EventWrite.
type eventWrite struct {
	Action        string     `json:"action"`
	RecordType    string     `json:"record_type"`
	RecordID      string     `json:"record_id"`
	MatterID      *domain.ID `json:"matter_id"`
	Summary       string     `json:"summary"`
	ClientVisible bool       `json:"client_visible"`
}

// staffRole is the events routes' role rule: admin, attorney or staff. An accountant
// or a client is refused, whatever else the caller holds.
func staffRole(p domain.Principal) bool {
	return p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff)
}

// moduleOwn is a module's own token with no viewer (PRD 9.3).
func moduleOwn(p domain.Principal) bool {
	return p.Source == domain.SourceModule && p.ModuleID != "" && p.UserID.IsZero()
}

// moduleEnabled reports whether module is installed and enabled.
func (a *App) moduleEnabled(ctx context.Context, module string) (bool, error) {
	if a.ext == nil || a.ext.Modules == nil {
		return false, nil
	}
	in, err := a.ext.Modules.Installed(ctx, module)
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == domain.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return in.Enabled, nil
}

// matterAccess applies the matter wall and role to one matter. A missing matter and a
// wall the caller is not on are both not_found. A module's own token (noninteractive)
// may reach an unwalled matter only, and only while the module is installed and
// enabled; an admin is not let past a wall.
func (a *App) matterAccess(ctx context.Context, p domain.Principal, matter domain.ID, action domain.Action) error {
	if _, err := a.Store.Matter(ctx, matter); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return domain.NotFound("matter")
		}
		return err
	}
	if moduleOwn(p) {
		view, err := a.Practice.Policy.Walls.View(ctx, p, matter)
		if err != nil {
			return err
		}
		if view.Walled {
			return domain.NotFound("matter")
		}
		return nil
	}
	return access.Gate{Policy: a.Practice.Policy}.Allow(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: action, MatterID: matter})
}

// eventWriter checks the caller of createEvent and bulkEvent: a staff role (a module's
// call through a viewer's context token runs as that viewer), or a module's own
// token while the module is installed and enabled.
func (a *App) eventWriter(ctx context.Context, p domain.Principal) error {
	if moduleOwn(p) {
		on, err := a.moduleEnabled(ctx, p.ModuleID)
		if err != nil {
			return err
		}
		if !on {
			return domain.Forbidden("the module is not installed and enabled")
		}
		return nil
	}
	if !staffRole(p) {
		return domain.Forbidden("admin, attorney or staff role is required")
	}
	return nil
}

var (
	coreActionsOnce sync.Once
	coreActions     map[string]bool
)

// reservedAction reports an action the core itself emits (every x-togavsu-events name in
// the contract). An appended event may not take one, so a module or token cannot make a
// feed row or a webhook that looks like the core's own matter.created or
// trust.flag_raised.
func reservedAction(action string) bool {
	coreActionsOnce.Do(func() {
		coreActions = map[string]bool{}
		var doc struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if json.Unmarshal(api.OpenAPI, &doc) != nil {
			return
		}
		for _, item := range doc.Paths {
			for _, raw := range item {
				var op struct {
					Events []string `json:"x-togavsu-events"`
				}
				if json.Unmarshal(raw, &op) == nil {
					for _, e := range op.Events {
						coreActions[e] = true
					}
				}
			}
		}
	})
	return coreActions[action]
}

// validID is a UUIDv7.
func validID(s string) bool {
	_, err := domain.ParseID(s)
	return err == nil
}

// plain is non-blank single-line text.
func plain(v string) bool {
	return strings.TrimSpace(v) != "" && !strings.ContainsFunc(v, unicode.IsControl)
}

// validate checks one EventWrite. field prefixes the problem's field for a batch item.
func (e eventWrite) validate(field string) error {
	switch {
	case !plain(e.Action):
		return domain.Invalid(field+"action", "action is required on one line")
	case reservedAction(e.Action):
		return domain.Invalid(field+"action", "action "+e.Action+" is emitted by the core and cannot be appended")
	case !plain(e.RecordType):
		return domain.Invalid(field+"record_type", "record_type is required on one line")
	case !plain(e.RecordID):
		return domain.Invalid(field+"record_id", "record_id is required on one line")
	case strings.TrimSpace(e.Summary) == "":
		return domain.Invalid(field+"summary", "summary is required")
	case e.MatterID != nil && !validID(string(*e.MatterID)):
		return domain.Invalid(field+"matter_id", "matter_id is not a UUID")
	}
	return nil
}

// appendEvents authorizes every item's matter first, then writes the events and their
// audit rows in the transaction on ctx. One inaccessible matter is not_found and writes
// none of them.
func (a *App) appendEvents(ctx context.Context, p domain.Principal, items []eventWrite, prefix func(int) string) ([]map[string]any, error) {
	for i, it := range items {
		if err := it.validate(prefix(i)); err != nil {
			return nil, err
		}
		if it.MatterID != nil {
			if err := a.matterAccess(ctx, p, *it.MatterID, domain.ActionWrite); err != nil {
				return nil, err
			}
		}
	}
	kind := eventActorKind(p)
	actor := p.UserID
	if actor.IsZero() {
		actor = domain.SystemUserID
	}
	tx := audit.Tx(ctx, a.Pool)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		id, err := domain.NewID()
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		ev := domain.Event{
			Meta:      domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor},
			ActorKind: kind, ActorID: actor, Action: it.Action, RecordType: it.RecordType,
			RecordID: domain.ID(it.RecordID), Summary: it.Summary, ClientVisible: it.ClientVisible,
		}
		if it.MatterID != nil {
			ev.MatterID = *it.MatterID
		}
		if err := db.PublishEvent(ctx, tx, ev); err != nil {
			return nil, err
		}
		if _, err := a.Audit.Record(ctx, p, audit.Change{Entity: "event", EntityID: id, Field: "action", New: it.Action, Reason: "event.appended"}); err != nil {
			return nil, err
		}
		out = append(out, eventJSON(ev))
	}
	return out, nil
}

// eventActorKind is set from the token, never from the body.
func eventActorKind(p domain.Principal) string {
	switch p.Source {
	case domain.SourceSystem:
		return "system"
	case domain.SourceModule:
		return "module"
	case domain.SourceAPIToken:
		return "agent"
	}
	return "staff"
}

func eventJSON(ev domain.Event) map[string]any {
	var actor, matter any
	if !ev.ActorID.IsZero() && ev.ActorID != domain.SystemUserID {
		actor = ev.ActorID
	}
	if !ev.MatterID.IsZero() {
		matter = ev.MatterID
	}
	by := ev.CreatedBy
	if by.IsZero() {
		by = domain.SystemUserID
	}
	return map[string]any{
		"id": ev.ID, "created_at": ev.CreatedAt.UTC().Format(time.RFC3339Nano), "created_by": by,
		"actor_kind": ev.ActorKind, "actor_id": actor, "action": ev.Action, "record_type": ev.RecordType,
		"record_id": ev.RecordID, "matter_id": matter, "summary": ev.Summary, "client_visible": ev.ClientVisible,
	}
}

// readEvents reads the write body for createEvent and bulkEvent, keeping the raw bytes
// for the Idempotency-Key fingerprint.
func readStrict(w http.ResponseWriter, r *http.Request, dst any) ([]byte, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if err != nil {
		return nil, domain.Invalid("body", "the request body is unreadable")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, domain.Invalid("body", "request body must match the contract schema")
	}
	return raw, nil
}

func (a *App) createEventJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.eventWriter(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	var body eventWrite
	raw, err := readStrict(w, r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	res, _, err := a.idempotentTx(r.Context(), p, "createEvent", r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		out, err := a.appendEvents(ctx, p, []eventWrite{body}, func(int) string { return "" })
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusCreated, Body: out[0]}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

func (a *App) bulkEventJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := a.eventWriter(r.Context(), p); err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Items []eventWrite `json:"items"`
	}
	raw, err := readStrict(w, r, &body)
	if err != nil {
		writeErr(w, err)
		return
	}
	max, err := a.intSetting(r.Context(), "api.bulk_batch_max")
	if err != nil {
		writeErr(w, err)
		return
	}
	switch {
	case len(body.Items) == 0:
		writeErr(w, domain.Invalid("items", "items needs at least one event"))
		return
	case len(body.Items) > max:
		writeErr(w, domain.Invalid("items", "batch is larger than api.bulk_batch_max ("+strconv.Itoa(max)+")"))
		return
	}
	res, _, err := a.idempotentTx(r.Context(), p, "bulkEvent", r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		out, err := a.appendEvents(ctx, p, body.Items, func(i int) string { return "items[" + strconv.Itoa(i) + "]." })
		if err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusCreated, Body: map[string]any{"data": out}}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

// none is zero for the feed's comparisons.
const none = int(0)

// eventCursor is the opaque listEvents cursor: the last row's created_at and id.
type eventCursor struct {
	At int64     `json:"t"`
	ID domain.ID `json:"i"`
}

func (c eventCursor) String() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseEventCursor(s string) (eventCursor, error) {
	var c eventCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.ID.IsZero() {
		return c, domain.Invalid("cursor", "cursor is not one this list returned")
	}
	return c, nil
}

// eventQuery is listEvents' filters.
type eventQuery struct {
	ActorKind string
	ActorID   domain.ID
	MatterID  domain.ID
	From      int64
	To        *int64
	After     *eventCursor
	Limit     int
}

func (a *App) parseEventQuery(ctx context.Context, r *http.Request) (eventQuery, error) {
	q := r.URL.Query()
	var out eventQuery
	switch k := q.Get("actor_kind"); k {
	case "", "staff", "client", "agent", "module", "system":
		out.ActorKind = k
	default:
		return out, domain.Invalid("actor_kind", "actor_kind is staff, client, agent, module or system")
	}
	for name, dst := range map[string]*domain.ID{"actor_id": &out.ActorID, "matter_id": &out.MatterID} {
		if v := q.Get(name); v != "" {
			if !validID(v) {
				return out, domain.Invalid(name, name+" is not a UUID")
			}
			*dst = domain.ID(v)
		}
	}
	window, err := a.intSetting(ctx, "activity.window_days")
	if err != nil {
		return out, err
	}
	out.From = domain.UnixMicroUTC(time.Now().UTC().AddDate(none, none, -window))
	if v := q.Get("from_time"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return out, domain.Invalid("from_time", "from_time is an RFC 3339 timestamp")
		}
		out.From = domain.UnixMicroUTC(t)
	}
	if v := q.Get("to_time"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return out, domain.Invalid("to_time", "to_time is an RFC 3339 timestamp")
		}
		to := domain.UnixMicroUTC(t)
		out.To = &to
	}
	if out.Limit, err = a.intSetting(ctx, "activity.page_size"); err != nil {
		return out, err
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= none {
			return out, domain.Invalid("limit", "limit is a whole number of at least 1")
		}
		cap, err := a.intSetting(ctx, "api.max_page_size")
		if err != nil {
			return out, err
		}
		if n > cap {
			return out, domain.Invalid("limit", "limit is above api.max_page_size ("+strconv.Itoa(cap)+")")
		}
		out.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		c, err := parseEventCursor(v)
		if err != nil {
			return out, err
		}
		out.After = &c
	}
	return out, nil
}

// listEventsJSON is the Activities feed over core.events, newest first. A row on a
// walled matter the caller is not on, an event about an attorney-only file for a caller
// who is neither attorney nor admin, and a contact event about a contact the caller
// cannot read (one who exists only on walled matters) are omitted, as practice's feed
// omits them. A matter filter the caller cannot see is not_found.
func (a *App) listEventsJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if moduleOwn(p) {
		writeErr(w, domain.Forbidden("the Activities feed is read through a viewer's context token"))
		return
	}
	if !staffRole(p) {
		writeErr(w, domain.Forbidden("admin, attorney or staff role is required"))
		return
	}
	ctx := r.Context()
	q, err := a.parseEventQuery(ctx, r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !q.MatterID.IsZero() {
		if err := a.matterAccess(ctx, p, q.MatterID, domain.ActionRead); err != nil {
			writeErr(w, err)
			return
		}
	}
	rows, next, err := a.feed(ctx, p, q)
	if err != nil {
		writeErr(w, err)
		return
	}
	var cursor any
	if next != nil {
		cursor = next.String()
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows, "page": map[string]any{"limit": q.Limit, "next_cursor": cursor}})
}

// feed reads pages of core.events until it has q.Limit rows the caller may see.
func (a *App) feed(ctx context.Context, p domain.Principal, q eventQuery) ([]map[string]any, *eventCursor, error) {
	attorney := p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney)
	out := []map[string]any{}
	after := q.After
	batch := q.Limit + 1
	for {
		sql := `SELECT e.id, e.created_at, e.created_by, e.actor_kind, e.actor_id, e.action, e.record_type, e.record_id,
COALESCE(e.matter_id, ''), e.summary, e.client_visible, COALESCE(f.attorney_only, 0)
FROM events e LEFT JOIN files f ON e.record_type = 'file' AND f.id = e.record_id
WHERE e.created_at >= $1`
		args := []any{q.From}
		add := func(cond string, v any) {
			args = append(args, v)
			sql += " AND " + strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args)))
		}
		if q.To != nil {
			add("e.created_at < ?", *q.To)
		}
		if q.ActorKind != "" {
			add("e.actor_kind = ?", q.ActorKind)
		}
		if !q.ActorID.IsZero() {
			add("e.actor_id = ?", string(q.ActorID))
		}
		if !q.MatterID.IsZero() {
			add("e.matter_id = ?", string(q.MatterID))
		}
		if after != nil {
			args = append(args, after.At, string(after.ID))
			n := strconv.Itoa(len(args) - 1)
			sql += " AND (e.created_at < $" + n + " OR (e.created_at = $" + n + " AND e.id < $" + strconv.Itoa(len(args)) + "))"
		}
		args = append(args, batch)
		sql += " ORDER BY e.created_at DESC, e.id DESC LIMIT $" + strconv.Itoa(len(args))
		rows, err := a.Pool.QueryContext(ctx, sql, args...)
		if err != nil {
			return nil, nil, err
		}
		var page []domain.Event
		var hidden []bool
		for rows.Next() {
			var ev domain.Event
			var at int64
			var visible, attorneyOnly int
			if err := rows.Scan(&ev.ID, &at, &ev.CreatedBy, &ev.ActorKind, &ev.ActorID, &ev.Action, &ev.RecordType, &ev.RecordID,
				&ev.MatterID, &ev.Summary, &visible, &attorneyOnly); err != nil {
				rows.Close()
				return nil, nil, err
			}
			ev.CreatedAt, ev.UpdatedAt = domain.FromUnixMicro(at), domain.FromUnixMicro(at)
			ev.ClientVisible = visible != none
			page = append(page, ev)
			hidden = append(hidden, attorneyOnly != none && !attorney)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		for i, ev := range page {
			last := &eventCursor{At: domain.UnixMicroUTC(ev.CreatedAt), ID: ev.ID}
			if hidden[i] || !a.Practice.FeedAllows(ctx, p, ev) {
				after = last
				continue
			}
			if len(out) == q.Limit {
				// A further visible row exists: the page ends at the previous row.
				return out, after, nil
			}
			out = append(out, eventJSON(ev))
			after = last
		}
		if len(page) < batch {
			return out, nil, nil
		}
	}
}

package calendar

import (
	"context"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// csrfCookie is the double-submit cookie the core sets at sign-in; practice forms
// read the same one.
const csrfCookie = practice.CSRFCookie

// Handler serves the calendar pages, the calendar JSON API and the iCal feed.
type Handler struct {
	Svc       *Service
	Templates *template.Template
	FS        fs.FS
	Principal func(*http.Request) (domain.Principal, error)
	idemMu    sync.Mutex
}

// NewHandler parses templates from fsys (web/templates/calendar). A nil fsys
// serves only the JSON API and the feed.
func NewHandler(svc *Service, fsys fs.FS, principal func(*http.Request) (domain.Principal, error)) (*Handler, error) {
	h := &Handler{Svc: svc, FS: fsys, Principal: principal}
	if fsys != nil {
		set, err := template.New("calendar").Funcs(funcs).ParseFS(fsys, "*.html")
		if err != nil {
			return nil, err
		}
		h.Templates = set
	}
	return h, nil
}

// Routes returns the calendar mux. The core mounts it beside practice.
func (h *Handler) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/calendar-events", h.listEvents)
	m.HandleFunc("POST /api/v1/calendar-events", h.createEvent)
	m.HandleFunc("POST /api/v1/calendar-events:bulk", h.bulkEvents)
	m.HandleFunc("GET /api/v1/calendar-events/{calendar_event_id}", h.getEvent)
	m.HandleFunc("PATCH /api/v1/calendar-events/{calendar_event_id}", h.patchEvent)
	m.HandleFunc("DELETE /api/v1/calendar-events/{calendar_event_id}", h.deleteEvent)
	m.HandleFunc("GET /api/v1/deadlines", h.listDeadlines)
	m.HandleFunc("POST /api/v1/deadlines", h.createDeadline)
	m.HandleFunc("POST /api/v1/deadlines:bulk", h.bulkDeadlines)
	m.HandleFunc("GET /api/v1/deadlines/{deadline_id}", h.getDeadline)
	m.HandleFunc("PATCH /api/v1/deadlines/{deadline_id}", h.patchDeadline)
	m.HandleFunc("POST /api/v1/calendar/available-dates", h.availableJSON)
	m.HandleFunc("GET /api/v1/ical-feeds", h.listFeeds)
	m.HandleFunc("POST /api/v1/ical-feeds", h.createFeed)
	m.HandleFunc("GET /api/v1/ical-feeds/{feed_id}", h.getFeed)
	m.HandleFunc("POST /api/v1/ical-feeds/{feed_id}/revoke", h.revokeFeed)
	m.HandleFunc("GET /ical/{file}", h.download)
	m.HandleFunc("GET /api/v1/widgets/upcoming-deadlines", h.widgetDeadlines)
	m.HandleFunc("GET /api/v1/widgets/todays-calendar", h.widgetToday)
	if h.Templates != nil {
		h.pageRoutes(m)
	}
	return m
}

func (h *Handler) principal(r *http.Request) (domain.Principal, error) {
	if h.Principal == nil {
		return domain.Principal{}, domain.Unauthenticated("sign in")
	}
	p, err := h.Principal(r)
	if err != nil {
		return domain.Principal{}, err
	}
	if p.Source == domain.SourceSystem {
		// A request never acts as the system job runner.
		return domain.Principal{}, domain.Unauthenticated("sign in")
	}
	return p, nil
}

// writeCaller authenticates a write. A browser session (any Cookie header) must
// echo the CSRF cookie in X-CSRF-Token; a bearer call without cookies need not.
func (h *Handler) writeCaller(r *http.Request) (domain.Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		return p, err
	}
	if r.Header.Get("Cookie") != "" {
		c, err := r.Cookie(csrfCookie)
		token := r.Header.Get("X-CSRF-Token")
		if err != nil || c.Value == "" || token != c.Value {
			return domain.Principal{}, domain.Forbidden("csrf")
		}
	}
	return p, nil
}

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	f := EventFilter{MatterID: domain.ID(q.Get("matter_id")), UserID: domain.ID(q.Get("user_id")), Kind: q.Get("kind")}
	if v := q.Get("from_time"); v != "" {
		if f.From, err = parseInstant("from_time", v); err != nil {
			writeErr(w, err)
			return
		}
	}
	if v := q.Get("to_time"); v != "" {
		if f.To, err = parseInstant("to_time", v); err != nil {
			writeErr(w, err)
			return
		}
	}
	if v := q.Get("view"); v != "" {
		// view=day|week|month with from_time sets the window when to_time is absent.
		if f.From.IsZero() {
			writeErr(w, domain.Invalid("from_time", "view needs from_time"))
			return
		}
		if f.To.IsZero() {
			start, end, err := viewWindow(v, localDate(f.From, h.Svc.Zone(r.Context())), h.Svc.Zone(r.Context()))
			if err != nil {
				writeErr(w, err)
				return
			}
			f.From, f.To = start, end
		}
	}
	rows, err := h.Svc.ListEvents(r.Context(), p, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h.Svc, r, rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, e := range items {
		data = append(data, eventJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b eventBody
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		in, err := b.input()
		if err != nil {
			return 0, nil, err
		}
		e, err := h.Svc.CreateEvent(ctx, p, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, eventJSON(e), nil
	})
}

func (h *Handler) bulkEvents(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b struct {
			Items []eventBody `json:"items"`
		}
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		ins := make([]EventInput, len(b.Items))
		for i, item := range b.Items {
			if ins[i], err = item.input(); err != nil {
				return 0, nil, indexed(i, err)
			}
		}
		rows, err := h.Svc.BulkEvents(ctx, p, ins)
		if err != nil {
			return 0, nil, err
		}
		data := make([]map[string]any, 0, len(rows))
		for _, e := range rows {
			data = append(data, eventJSON(e))
		}
		return http.StatusCreated, map[string]any{"data": data}, nil
	})
}

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	e, err := h.Svc.GetEvent(r.Context(), p, domain.ID(r.PathValue("calendar_event_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, eventJSON(e))
}

func (h *Handler) patchEvent(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b eventBody
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		patch, err := b.patch()
		if err != nil {
			return 0, nil, err
		}
		e, err := h.Svc.UpdateEvent(ctx, p, domain.ID(r.PathValue("calendar_event_id")), patch)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, eventJSON(e), nil
	})
}

func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.Svc.DeleteEvent(r.Context(), p, domain.ID(r.PathValue("calendar_event_id"))); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDeadlines(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListDeadlines(r.Context(), p, deadlineFilter(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h.Svc, r, rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, d := range items {
		data = append(data, deadlineJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func deadlineFilter(r *http.Request) DeadlineFilter {
	q := r.URL.Query()
	return DeadlineFilter{
		From: q.Get("from_date"), To: q.Get("to_date"), Status: q.Get("status"),
		MatterID: domain.ID(q.Get("matter_id")), ResponsibleUserID: domain.ID(q.Get("responsible_user_id")),
		IncludeOverdue: q.Get("include_overdue") == "true",
	}
}

func (h *Handler) createDeadline(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b deadlineBody
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		in, err := b.input()
		if err != nil {
			return 0, nil, err
		}
		d, err := h.Svc.CreateDeadline(ctx, p, in)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, deadlineJSON(d), nil
	})
}

func (h *Handler) bulkDeadlines(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b struct {
			Items []deadlineBody `json:"items"`
		}
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		ins := make([]DeadlineInput, len(b.Items))
		for i, item := range b.Items {
			if ins[i], err = item.input(); err != nil {
				return 0, nil, indexed(i, err)
			}
		}
		rows, err := h.Svc.BulkDeadlines(ctx, p, ins)
		if err != nil {
			return 0, nil, err
		}
		data := make([]map[string]any, 0, len(rows))
		for _, d := range rows {
			data = append(data, deadlineJSON(d))
		}
		return http.StatusCreated, map[string]any{"data": data}, nil
	})
}

func (h *Handler) getDeadline(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	d, err := h.Svc.GetDeadline(r.Context(), p, domain.ID(r.PathValue("deadline_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deadlineJSON(d))
}

func (h *Handler) patchDeadline(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.idempotent(w, r, p, body, func(ctx context.Context) (int, any, error) {
		var b deadlineBody
		if err := decodeStrict(body, &b); err != nil {
			return 0, nil, err
		}
		d, err := h.Svc.UpdateDeadline(ctx, p, domain.ID(r.PathValue("deadline_id")), b.patch())
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, deadlineJSON(d), nil
	})
}

func (h *Handler) availableJSON(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b availableBody
	if err := decodeStrict(body, &b); err != nil {
		writeErr(w, err)
		return
	}
	res, err := h.Svc.AvailableDates(r.Context(), p, b.request())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, availableJSON(res))
}

func (h *Handler) listFeeds(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := h.Svc.ListFeeds(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, pg, err := page(h.Svc, r, rows)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for _, f := range items {
		data = append(data, feedJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": pg})
}

func (h *Handler) createFeed(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	body, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Not replayed through Idempotency-Key: a stored replay would keep the secret URL.
	var b struct {
		Owner *string `json:"owner_user_id"`
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := decodeStrict(body, &b); err != nil {
			writeErr(w, err)
			return
		}
	}
	var owner domain.ID
	if b.Owner != nil {
		owner = domain.ID(*b.Owner)
	}
	f, url, err := h.Svc.CreateFeed(r.Context(), p, owner)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"feed": feedJSON(f), "url": url})
}

func (h *Handler) getFeed(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	f, err := h.Svc.GetFeed(r.Context(), p, domain.ID(r.PathValue("feed_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feedJSON(f))
}

func (h *Handler) revokeFeed(w http.ResponseWriter, r *http.Request) {
	p, err := h.writeCaller(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	f, err := h.Svc.RevokeFeed(r.Context(), p, domain.ID(r.PathValue("feed_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, feedJSON(f))
}

// download serves /ical/{secret}.ics without a session: the secret is the credential.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	secret, ok := strings.CutSuffix(r.PathValue("file"), ".ics")
	if !ok {
		writeErr(w, domain.NotFound("ical feed"))
		return
	}
	body, err := h.Svc.Download(r.Context(), secret)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(body)
}

func (h *Handler) widgetDeadlines(w http.ResponseWriter, r *http.Request) {
	h.widget(w, r, h.Svc.UpcomingDeadlinesWidget)
}

func (h *Handler) widgetToday(w http.ResponseWriter, r *http.Request) {
	h.widget(w, r, h.Svc.TodaysCalendarWidget)
}

func (h *Handler) widget(w http.ResponseWriter, r *http.Request, load func(context.Context, domain.Principal, string, string, domain.ID) (Widget, error)) {
	p, err := h.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	q := r.URL.Query()
	for _, k := range []string{"from_date", "to_date"} {
		if v := q.Get(k); v != "" {
			if _, err := parseDate(v); err != nil {
				writeErr(w, domain.Invalid(k, "date must be YYYY-MM-DD"))
				return
			}
		}
	}
	wd, err := load(r.Context(), p, q.Get("from_date"), q.Get("to_date"), domain.ID(q.Get("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wd)
}

func indexed(i int, err error) error {
	if api, ok := err.(*domain.APIError); ok {
		cp := *api
		cp.Field = "items[" + strconv.Itoa(i) + "]." + cp.Field
		return &cp
	}
	return err
}

// viewWindow is the [start, end) of a day, week (Monday first) or month view
// around date, in loc.
func viewWindow(view string, date time.Time, loc *time.Location) (time.Time, time.Time, error) {
	switch view {
	case "day":
		return dayStart(date, loc), dayStart(nextDay(date), loc), nil
	case "week":
		first := weekStart(date)
		last := time.Date(first.Year(), first.Month(), first.Day()+7, 0, 0, 0, 0, time.UTC)
		return dayStart(first, loc), dayStart(last, loc), nil
	case "month":
		first := time.Date(date.Year(), date.Month(), 1, 0, 0, 0, 0, time.UTC)
		next := time.Date(date.Year(), date.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		return dayStart(first, loc), dayStart(next, loc), nil
	}
	return time.Time{}, time.Time{}, domain.Invalid("view", "view must be day, week or month")
}

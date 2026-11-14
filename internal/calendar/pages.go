package calendar

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/frame"
)

var funcs = template.FuncMap{
	"kindLabel": kindLabel,
	"list":      func(v ...string) []string { return v },
	"on":        func(path, tab string) bool { return path == tab || strings.HasPrefix(path, tab+"/") },
}

func kindLabel(k string) string {
	switch k {
	case KindCourtDate:
		return "Court date"
	case KindClientMeeting:
		return "Client meeting"
	case KindDeadline:
		return "Deadline"
	case KindBlock:
		return "Block"
	}
	return "Other"
}

// view is the data every calendar page template gets.
type view struct {
	Title   string
	Path    string // the screen's path, for the area tabs
	CSRF    string
	Me      domain.ID
	Error   string
	Notice  string
	Overdue []deadlineRow
	// OverdueMore counts overdue deadlines past the panel's first page; the deadlines
	// page lists them all.
	OverdueMore int
	Remind      []Notice
	Data        any
}

type eventRow struct {
	ID, Title, Kind, When, Location, MatterID, Notes string
	AllDay                                           bool
	Attendees                                        []string
}

type deadlineRow struct {
	ID, Title, DueOn, Status, MatterID, Responsible, Offsets, DoneBy, DoneAt string
	Overdue                                                                  bool
}

type dayCell struct {
	Date, Label string
	InPeriod    bool
	Today       bool
	Events      []eventRow
	Deadlines   []deadlineRow
}

type calendarData struct {
	View, Date, UserID, MatterID, Kind string
	Heading, Prev, Next, Today         string
	DayLink, WeekLink, MonthLink       string
	FirmLink, MineLink                 string
	Weeks                              [][]dayCell
	Days                               []dayCell
	Kinds                              []string
}

func (h *Handler) pageRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /calendar/static/calendar.js", h.static("calendar.js", "text/javascript; charset=utf-8"))
	m.HandleFunc("GET /calendar", h.calendarPage)
	m.HandleFunc("POST /calendar/events", h.createEventPost)
	m.HandleFunc("GET /calendar/events/{id}", h.eventPage)
	m.HandleFunc("POST /calendar/events/{id}/delete", h.deleteEventPost)
	m.HandleFunc("GET /calendar/deadlines", h.deadlinesPage)
	m.HandleFunc("POST /calendar/deadlines", h.createDeadlinePost)
	m.HandleFunc("GET /calendar/deadlines/{id}", h.deadlinePage)
	m.HandleFunc("POST /calendar/deadlines/{id}/status", h.deadlineStatusPost)
	m.HandleFunc("GET /calendar/available-dates", h.availablePage)
	m.HandleFunc("POST /calendar/available-dates", h.availablePost)
	m.HandleFunc("POST /calendar/available-dates/download", h.availableDownload)
	m.HandleFunc("GET /calendar/feeds", h.feedsPage)
	m.HandleFunc("POST /calendar/feeds", h.createFeedPost)
	m.HandleFunc("POST /calendar/feeds/{id}/revoke", h.revokeFeedPost)
}

func (h *Handler) static(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(h.FS, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(b)
	}
}

func (h *Handler) csrf(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && c.Value != "" {
		return c.Value
	}
	b := make([]byte, sha256.Size)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")})
	return token
}

// formCaller authenticates a form post and checks the double-submit csrf field.
func (h *Handler) formCaller(r *http.Request) (domain.Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		return p, err
	}
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" || c.Value != r.FormValue("csrf") {
		return domain.Principal{}, domain.Forbidden("csrf")
	}
	return p, nil
}

// render draws the named calendar screen inside the app shell.
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, v view) {
	v.CSRF, v.Path = h.csrf(w, r), r.URL.Path
	var buf bytes.Buffer
	if err := h.Templates.ExecuteTemplate(&buf, name, v); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; frame-ancestors 'none'; form-action 'self'")
	frame.Write(w, r, frame.Page{Title: v.Title, Area: "calendar", Body: template.HTML(buf.String()), Status: status,
		Notice: v.Notice, Error: v.Error, Scripts: []string{"/calendar/static/calendar.js"}})
}

func (h *Handler) pageErr(w http.ResponseWriter, r *http.Request, err error) {
	var api *domain.APIError
	status := http.StatusInternalServerError
	msg := "Something went wrong."
	if errors.As(err, &api) {
		status, msg = api.HTTPStatus(), api.Message
		if api.Field != "" {
			msg = api.Field + ": " + msg
		}
	}
	h.render(w, r, status, "error.html", view{Title: "Calendar", Error: msg})
}

// chrome loads the reminders and overdue deadlines every calendar page shows.
func (h *Handler) chrome(r *http.Request, p domain.Principal, v *view) {
	ctx := r.Context()
	// The panel shows a page of overdue deadlines, oldest first, and counts the rest.
	n, _ := h.Svc.settingInt(ctx, "api.page_size") // zero on error: the whole list
	if rows, total, err := h.Svc.OverduePage(ctx, p, n); err == nil {
		v.OverdueMore = total - len(rows)
		names := h.rows(r)
		for _, d := range rows {
			v.Overdue = append(v.Overdue, names.deadline(d))
		}
	}
	if n, err := h.Svc.Notices(ctx, p); err == nil {
		v.Remind = n
	}
	v.Me = p.UserID
}

func (h *Handler) eventRow(r *http.Request, e Event) eventRow { return h.rows(r).event(e) }

// rowNames builds a page's event and deadline rows, reading the firm zone, today and each
// user's display name once per page rather than once per row (AC-017: thousands of
// overdue deadlines). Names are labels only; access is decided before a row is built.
type rowNames struct {
	h     *Handler
	ctx   context.Context
	loc   *time.Location
	today string
	users map[domain.ID]string
}

func (h *Handler) rows(r *http.Request) *rowNames {
	ctx := r.Context()
	loc := h.Svc.Zone(ctx)
	return &rowNames{h: h, ctx: ctx, loc: loc, today: localDate(h.Svc.now(), loc).Format(dateLayout), users: map[domain.ID]string{}}
}

// user is the user's name, or the id when there is none.
func (n *rowNames) user(id domain.ID) string {
	name, ok := n.users[id]
	if !ok {
		name = string(id)
		if u, err := n.h.Svc.d.Users.Get(n.ctx, id); err == nil {
			name = u.Name
		}
		n.users[id] = name
	}
	return name
}

func (n *rowNames) event(e Event) eventRow {
	loc := n.loc
	when := e.Start().In(loc).Format("Mon Jan 2, 3:04 PM") + " – " + e.End().In(loc).Format("3:04 PM")
	if e.AllDay {
		days := eventDaysAllDay(e, loc)
		first, last := days[0], days[len(days)-1]
		when = "All day " + first.Format("Mon Jan 2")
		if !last.Equal(first) {
			when += " – " + last.Format("Mon Jan 2")
		}
	}
	row := eventRow{ID: string(e.ID), Title: e.Title, Kind: e.Kind, When: when, Location: e.Location,
		MatterID: string(e.MatterID), Notes: e.Notes, AllDay: e.AllDay}
	for _, id := range e.Attendees {
		row.Attendees = append(row.Attendees, n.user(id))
	}
	return row
}

func (n *rowNames) deadline(d Deadline) deadlineRow {
	row := deadlineRow{ID: string(d.ID), Title: d.Title, DueOn: d.DueOn, Status: d.Status, MatterID: string(d.MatterID),
		Responsible: n.user(d.ResponsibleUserID), Offsets: joinInts(d.Offsets), Overdue: d.Status == StatusOpen && d.DueOn < n.today}
	if isSet(d.DoneAt) {
		row.DoneAt = domain.FromUnixMicro(d.DoneAt).In(n.loc).Format("Jan 2, 2006 3:04 PM")
		row.DoneBy = n.user(d.DoneBy)
	}
	return row
}

// eventDaysAllDay lists the dates an all-day event covers (its end is exclusive).
func eventDaysAllDay(e Event, loc *time.Location) []time.Time {
	var out []time.Time
	for d := localDate(e.Start(), loc); dayStart(d, loc).Before(e.End()); d = nextDay(d) {
		out = append(out, d)
	}
	if len(out) == 0 {
		out = append(out, localDate(e.Start(), loc))
	}
	return out
}

func (h *Handler) deadlineRow(r *http.Request, d Deadline) deadlineRow { return h.rows(r).deadline(d) }

func calLink(view, date, user, matter, kind string) string {
	q := url.Values{}
	q.Set("view", view)
	q.Set("date", date)
	for k, v := range map[string]string{"user_id": user, "matter_id": matter, "kind": kind} {
		if v != "" {
			q.Set(k, v)
		}
	}
	return "/calendar?" + q.Encode()
}

// calendarPage is the day, week and month calendar, per user or firm-wide,
// filtered by matter and kind. It reads through ListEvents and ListDeadlines, so it
// shows exactly what the API shows the same caller.
func (h *Handler) calendarPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	ctx := r.Context()
	q := r.URL.Query()
	loc := h.Svc.Zone(ctx)
	today := h.Svc.Today(ctx)
	mode := q.Get("view")
	if mode == "" {
		mode = "week"
	}
	date := today
	if v := q.Get("date"); v != "" && v != "today" {
		if date, err = parseDate(v); err != nil {
			h.pageErr(w, r, domain.Invalid("date", "date must be YYYY-MM-DD"))
			return
		}
	}
	start, end, err := viewWindow(mode, date, loc)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	user, matter, kind := domain.ID(q.Get("user_id")), domain.ID(q.Get("matter_id")), q.Get("kind")
	events, err := h.Svc.ListEvents(ctx, p, EventFilter{From: start, To: end, UserID: user, MatterID: matter, Kind: kind})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	firstDay, lastDay := localDate(start, loc), localDate(end.Add(-time.Nanosecond), loc)
	var deadlines []Deadline
	if kind == "" || kind == KindDeadline {
		deadlines, err = h.Svc.ListDeadlines(ctx, p, DeadlineFilter{From: firstDay.Format(dateLayout), To: lastDay.Format(dateLayout), MatterID: matter, ResponsibleUserID: user})
		if err != nil {
			h.pageErr(w, r, err)
			return
		}
	}
	gridFirst, gridLast := firstDay, lastDay
	if mode == "month" {
		gridFirst = weekStart(firstDay)
		ws := weekStart(lastDay)
		gridLast = time.Date(ws.Year(), ws.Month(), ws.Day()+6, 0, 0, 0, 0, time.UTC)
	}
	var cells []dayCell
	names := h.rows(r)
	for d := gridFirst; !d.After(gridLast); d = nextDay(d) {
		cell := dayCell{Date: d.Format(dateLayout), Label: d.Format("Mon Jan 2"), Today: d.Equal(today),
			InPeriod: !d.Before(firstDay) && !d.After(lastDay)}
		ds, de := dayStart(d, loc), dayStart(nextDay(d), loc)
		for _, e := range events {
			if overlaps(e.Start(), e.End(), ds, de) {
				cell.Events = append(cell.Events, names.event(e))
			}
		}
		for _, dl := range deadlines {
			if dl.DueOn == cell.Date {
				cell.Deadlines = append(cell.Deadlines, names.deadline(dl))
			}
		}
		cells = append(cells, cell)
	}
	y, mo, dd := date.Date()
	var prev, next time.Time
	switch mode {
	case "day":
		prev, next = time.Date(y, mo, dd-1, 0, 0, 0, 0, time.UTC), time.Date(y, mo, dd+1, 0, 0, 0, 0, time.UTC)
	case "week":
		prev, next = time.Date(y, mo, dd-7, 0, 0, 0, 0, time.UTC), time.Date(y, mo, dd+7, 0, 0, 0, 0, time.UTC)
	default:
		prev, next = monthStep(y, mo, dd, true), monthStep(y, mo, dd, false)
	}
	ds := date.Format(dateLayout)
	data := calendarData{
		View: mode, Date: ds, UserID: string(user), MatterID: string(matter), Kind: kind,
		Prev:    calLink(mode, prev.Format(dateLayout), string(user), string(matter), kind),
		Next:    calLink(mode, next.Format(dateLayout), string(user), string(matter), kind),
		Today:   calLink(mode, today.Format(dateLayout), string(user), string(matter), kind),
		DayLink: calLink("day", ds, string(user), string(matter), kind), WeekLink: calLink("week", ds, string(user), string(matter), kind),
		MonthLink: calLink("month", ds, string(user), string(matter), kind),
		FirmLink:  calLink(mode, ds, "", string(matter), kind), MineLink: calLink(mode, ds, string(p.UserID), string(matter), kind),
		Kinds: []string{KindCourtDate, KindClientMeeting, KindDeadline, KindBlock, KindOther},
	}
	switch mode {
	case "day":
		data.Heading = date.Format("Monday, January 2, 2006")
	case "week":
		data.Heading = "Week of " + firstDay.Format("January 2, 2006")
	default:
		data.Heading = date.Format("January 2006")
	}
	if mode == "month" {
		// The grid runs Monday to Sunday, so each Monday starts a row.
		for _, c := range cells {
			if d, _ := parseDate(c.Date); d.Weekday() == time.Monday || data.Weeks == nil {
				data.Weeks = append(data.Weeks, nil)
			}
			data.Weeks[len(data.Weeks)-1] = append(data.Weeks[len(data.Weeks)-1], c)
		}
	} else {
		data.Days = cells
	}
	v := view{Title: "Calendar", Data: data}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "calendar.html", v)
}

func (h *Handler) formTime(r *http.Request, field string, allDay bool) (time.Time, error) {
	v := strings.TrimSpace(r.FormValue(field))
	if v == "" {
		return time.Time{}, domain.Invalid(field, "required")
	}
	if allDay {
		d, err := time.Parse(dateLayout, v[:min(len(v), len(dateLayout))])
		if err != nil {
			return time.Time{}, domain.Invalid(field, "must be a date")
		}
		return d, nil
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", v, h.Svc.Zone(r.Context()))
	if err != nil {
		return time.Time{}, domain.Invalid(field, "must be a date and time")
	}
	return t, nil
}

func splitIDs(v string) []domain.ID {
	var out []domain.ID
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		out = append(out, domain.ID(strings.TrimSpace(part)))
	}
	return out
}

func (h *Handler) createEventPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	allDay := r.FormValue("all_day") == "on"
	start, err := h.formTime(r, "starts_at", allDay)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	end, err := h.formTime(r, "ends_at", allDay)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	att := splitIDs(r.FormValue("attendee_user_ids"))
	if r.FormValue("attend_me") == "on" {
		att = append([]domain.ID{p.UserID}, att...)
	}
	e, err := h.Svc.CreateEvent(r.Context(), p, EventInput{
		Title: r.FormValue("title"), Kind: r.FormValue("kind"), StartsAt: start, EndsAt: end, AllDay: allDay,
		MatterID: domain.ID(strings.TrimSpace(r.FormValue("matter_id"))), Location: r.FormValue("location"),
		Notes: r.FormValue("notes"), Attendees: att,
	})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/calendar/events/"+string(e.ID), http.StatusSeeOther)
}

func (h *Handler) eventPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	e, err := h.Svc.GetEvent(r.Context(), p, domain.ID(r.PathValue("id")))
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	v := view{Title: e.Title, Data: h.eventRow(r, e)}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "event.html", v)
}

func (h *Handler) deleteEventPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	if err := h.Svc.DeleteEvent(r.Context(), p, domain.ID(r.PathValue("id"))); err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/calendar", http.StatusSeeOther)
}

type deadlinesData struct {
	From, To, Status, MatterID, ResponsibleUserID string
	IncludeOverdue                                bool
	Rows                                          []deadlineRow
}

// deadlinesPage is where the upcoming deadlines widget opens. It reads the same
// query the widget's target carries, so the range and filters match.
func (h *Handler) deadlinesPage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	f := deadlineFilter(r)
	rows, err := h.Svc.ListDeadlines(r.Context(), p, f)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	data := deadlinesData{From: f.From, To: f.To, Status: f.Status, MatterID: string(f.MatterID),
		ResponsibleUserID: string(f.ResponsibleUserID), IncludeOverdue: f.IncludeOverdue}
	names := h.rows(r)
	for _, d := range rows {
		data.Rows = append(data.Rows, names.deadline(d))
	}
	v := view{Title: "Deadlines", Data: data}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "deadlines.html", v)
}

func (h *Handler) createDeadlinePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	var offsets []int
	if v := strings.TrimSpace(r.FormValue("reminder_offsets_days")); v != "" {
		if offsets, err = parseOffsets(v); err != nil {
			h.pageErr(w, r, err)
			return
		}
	}
	resp := domain.ID(strings.TrimSpace(r.FormValue("responsible_user_id")))
	if resp.IsZero() {
		resp = p.UserID
	}
	d, err := h.Svc.CreateDeadline(r.Context(), p, DeadlineInput{
		MatterID: domain.ID(strings.TrimSpace(r.FormValue("matter_id"))), Title: r.FormValue("title"),
		DueOn: r.FormValue("due_on"), ResponsibleUserID: resp, Offsets: offsets,
	})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, DeadlinesPath+"/"+string(d.ID), http.StatusSeeOther)
}

func (h *Handler) deadlinePage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	d, err := h.Svc.GetDeadline(r.Context(), p, domain.ID(r.PathValue("id")))
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	v := view{Title: d.Title, Data: h.deadlineRow(r, d)}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "deadline.html", v)
}

func (h *Handler) deadlineStatusPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	status := r.FormValue("status")
	d, err := h.Svc.UpdateDeadline(r.Context(), p, domain.ID(r.PathValue("id")), DeadlinePatch{Status: &status})
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, DeadlinesPath+"/"+string(d.ID), http.StatusSeeOther)
}

type availableData struct {
	Form   map[string]string
	Half   bool
	Result *AvailableResult
	// Matter picker (PRD 8.16: the matter is picked by search).
	MatterQ   string
	Searched  bool
	Matches   []MatterChoice
	SearchErr string
	Picked    *MatterChoice
	// Attorney picker; HasRoster is false when users cannot be listed.
	Attorneys []AttorneyChoice
	HasRoster bool
}

// pickers fills the matter and attorney pickers. Search errors show beside the
// search box; a picked matter the caller cannot see is dropped from the form.
func (h *Handler) pickers(r *http.Request, p domain.Principal, d *availableData) {
	ctx := r.Context()
	if d.MatterQ != "" {
		d.Searched = true
		hits, err := h.Svc.SearchMatters(ctx, p, d.MatterQ)
		var api *domain.APIError
		switch {
		case err == nil:
			d.Matches = hits
		case errors.As(err, &api):
			d.SearchErr = api.Message
		default:
			d.SearchErr = "search failed"
		}
	}
	if id := domain.ID(d.Form["matter_id"]); !id.IsZero() {
		if m, err := h.Svc.PickedMatter(ctx, p, id); err == nil {
			d.Picked = &m
		}
	}
	if list, ok, err := h.Svc.Attorneys(ctx, p); err == nil && ok {
		d.Attorneys, d.HasRoster = list, true
	}
}

func (h *Handler) availablePage(w http.ResponseWriter, r *http.Request) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	purpose, _ := h.Svc.setting(r.Context(), settingPurpose)
	title, _ := h.Svc.setting(r.Context(), settingTitle)
	q := r.URL.Query()
	form := map[string]string{"user_id": string(p.UserID), "start_date": h.Svc.Today(r.Context()).Format(dateLayout),
		"purpose": purpose, "title": title, "matter_id": strings.TrimSpace(q.Get("matter_id"))}
	if u := strings.TrimSpace(q.Get("user_id")); u != "" {
		form["user_id"] = u
	}
	data := availableData{Form: form, MatterQ: strings.TrimSpace(q.Get("matter_q"))}
	h.pickers(r, p, &data)
	if data.Picked == nil {
		form["matter_id"] = ""
	}
	v := view{Title: "Available dates", Data: data}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "available.html", v)
}

var availableFields = []string{"user_id", "start_date", "end_date", "matter_id", "court", "caption", "left_label", "right_label",
	"left_party_name", "right_party_name", "case_number", "firm_side", "purpose", "coordinate_with", "title", "service_date",
	"letter_recipient", "letter_address", "letter_re", "letter_salutation"}

func availableForm(r *http.Request) (AvailableRequest, map[string]string) {
	form := map[string]string{}
	for _, k := range availableFields {
		form[k] = strings.TrimSpace(r.FormValue(k))
	}
	return AvailableRequest{
		UserID: domain.ID(form["user_id"]), StartDate: form["start_date"], EndDate: form["end_date"],
		IncludeHalfDays: r.FormValue("include_half_days") == "on", MatterID: domain.ID(form["matter_id"]),
		Court: form["court"], Caption: form["caption"], LeftLabel: form["left_label"], RightLabel: form["right_label"],
		LeftName: form["left_party_name"], RightName: form["right_party_name"], CaseNumber: form["case_number"],
		FirmSide: form["firm_side"], Purpose: form["purpose"], CoordinateWith: form["coordinate_with"], Title: form["title"],
		ServiceDate: form["service_date"], LetterRecipient: form["letter_recipient"], LetterAddress: r.FormValue("letter_address"),
		LetterRe: form["letter_re"], LetterSalutation: form["letter_salutation"],
	}, form
}

// availablePost shows the result panel. It is a read: no event changes.
func (h *Handler) availablePost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	req, form := availableForm(r)
	v := view{Title: "Available dates"}
	data := availableData{Form: form, Half: req.IncludeHalfDays}
	res, err := h.Svc.AvailableDates(r.Context(), p, req)
	status := http.StatusOK
	if err != nil {
		var api *domain.APIError
		if !errors.As(err, &api) {
			h.pageErr(w, r, err)
			return
		}
		status, v.Error = api.HTTPStatus(), strings.TrimPrefix(api.Field+": "+api.Message, ": ")
	} else {
		data.Result = &res
	}
	h.pickers(r, p, &data)
	v.Data = data
	h.chrome(r, p, &v)
	h.render(w, r, status, "available.html", v)
}

func (h *Handler) availableDownload(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	req, _ := availableForm(r)
	res, err := h.Svc.AvailableDates(r.Context(), p, req)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	body, name := res.Pleading, "available-dates-pleading.txt"
	if r.FormValue("form") == "letter" {
		body, name = res.Letter, "available-dates-letter.txt"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write([]byte(body))
}

type feedRow struct {
	ID, Owner, Created string
	Firm, Revoked      bool
}

type feedsData struct {
	Rows    []feedRow
	NewURL  string
	IsAdmin bool
	Enabled bool
}

func (h *Handler) feedsPage(w http.ResponseWriter, r *http.Request) {
	h.renderFeeds(w, r, "")
}

func (h *Handler) renderFeeds(w http.ResponseWriter, r *http.Request, newURL string) {
	p, err := h.principal(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	rows, err := h.Svc.ListFeeds(r.Context(), p)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	on, _ := h.Svc.icalEnabled(r.Context())
	data := feedsData{NewURL: newURL, IsAdmin: p.HasRole(domain.RoleAdmin), Enabled: on}
	for _, f := range rows {
		row := feedRow{ID: string(f.ID), Firm: f.OwnerUserID.IsZero(), Revoked: f.Revoked,
			Created: f.CreatedAt.In(h.Svc.Zone(r.Context())).Format("Jan 2, 2006 3:04 PM")}
		if !row.Firm {
			row.Owner = string(f.OwnerUserID)
			if u, err := h.Svc.d.Users.Get(r.Context(), f.OwnerUserID); err == nil {
				row.Owner = u.Name
			}
		}
		data.Rows = append(data.Rows, row)
	}
	v := view{Title: "Calendar feeds", Data: data}
	if newURL != "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	h.chrome(r, p, &v)
	h.render(w, r, http.StatusOK, "feeds.html", v)
}

func (h *Handler) createFeedPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	owner := p.UserID
	if r.FormValue("scope") == "firm" {
		owner = ""
	}
	_, link, err := h.Svc.CreateFeed(r.Context(), p, owner)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	h.renderFeeds(w, r, link)
}

func (h *Handler) revokeFeedPost(w http.ResponseWriter, r *http.Request) {
	p, err := h.formCaller(r)
	if err != nil {
		h.pageErr(w, r, err)
		return
	}
	if _, err := h.Svc.RevokeFeed(r.Context(), p, domain.ID(r.PathValue("id"))); err != nil {
		h.pageErr(w, r, err)
		return
	}
	http.Redirect(w, r, "/calendar/feeds", http.StatusSeeOther)
}

package calendar

import (
	"context"
	"sort"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// WidgetTarget is where a widget title opens, with the widget's own filters.
type WidgetTarget struct {
	Path  string            `json:"path"`
	Query map[string]string `json:"query"`
}

// WidgetRow is one record in a widget; its target opens that record.
type WidgetRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle,omitempty"`
	TargetPath string `json:"target_path"`
}

// Widget is the contract Widget payload.
type Widget struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Target   WidgetTarget `json:"target"`
	PagePath string       `json:"page_path"`
	Rows     []WidgetRow  `json:"rows"`
}

// DeadlinesPath is the deadlines page the upcoming deadlines widget opens.
const DeadlinesPath = "/calendar/deadlines"

// UpcomingRange is the widget's default window: firm-local today through the
// furthest default deadline reminder offset (calendar.deadline_reminder_days).
func (s *Service) UpcomingRange(ctx context.Context) (string, string, error) {
	offsets, err := s.DefaultDeadlineOffsets(ctx)
	if err != nil {
		return "", "", err
	}
	var span int
	for _, o := range offsets {
		if o > span {
			span = o
		}
	}
	today := s.Today(ctx)
	return today.Format(dateLayout), addDays(today, span).Format(dateLayout), nil
}

// DeadlineQuery is the filter the widget and the deadlines page share. Building
// both from this one function is what keeps the click-through on the same range.
func DeadlineQuery(from, to string, matter domain.ID) (DeadlineFilter, map[string]string) {
	f := DeadlineFilter{From: from, To: to, MatterID: matter, Status: StatusOpen, IncludeOverdue: true}
	q := map[string]string{"from_date": from, "to_date": to, "status": StatusOpen, "include_overdue": "true"}
	if !matter.IsZero() {
		q["matter_id"] = string(matter)
	}
	return f, q
}

// UpcomingDeadlinesWidget lists the caller's visible open deadlines in the range,
// overdue ones first. Its title opens the deadlines page with the same range and
// each row opens that deadline. A walled matter's deadline is absent.
func (s *Service) UpcomingDeadlinesWidget(ctx context.Context, p domain.Principal, from, to string, matter domain.ID) (Widget, error) {
	if from == "" || to == "" {
		f, t, err := s.UpcomingRange(ctx)
		if err != nil {
			return Widget{}, err
		}
		if from == "" {
			from = f
		}
		if to == "" {
			to = t
		}
	}
	filter, query := DeadlineQuery(from, to, matter)
	rows, err := s.ListDeadlines(ctx, p, filter)
	if err != nil {
		return Widget{}, err
	}
	w := Widget{ID: "upcoming-deadlines", Title: "Upcoming deadlines", PagePath: DeadlinesPath,
		Target: WidgetTarget{Path: DeadlinesPath, Query: query}, Rows: []WidgetRow{}}
	// Today once for the widget: IsOverdue reads the firm zone setting on every call.
	today := s.Today(ctx).Format(dateLayout)
	for _, d := range rows {
		sub := "Due " + d.DueOn
		if d.Status == StatusOpen && d.DueOn < today {
			sub = "Overdue since " + d.DueOn
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(d.ID), Title: d.Title, Subtitle: sub, TargetPath: DeadlinesPath + "/" + string(d.ID)})
	}
	return w, nil
}

// TodaysCalendarWidget lists today's visible events; its title opens the calendar
// in day view on the same date.
func (s *Service) TodaysCalendarWidget(ctx context.Context, p domain.Principal, from, to string, matter domain.ID) (Widget, error) {
	loc := s.Zone(ctx)
	today := s.Today(ctx).Format(dateLayout)
	if from == "" {
		from = today
	}
	if to == "" {
		to = from
	}
	fd, err := parseDate(from)
	if err != nil {
		return Widget{}, domain.Invalid("from_date", "date must be YYYY-MM-DD")
	}
	td, err := parseDate(to)
	if err != nil || td.Before(fd) {
		return Widget{}, domain.Invalid("to_date", "date must be YYYY-MM-DD on or after from_date")
	}
	rows, err := s.ListEvents(ctx, p, EventFilter{From: dayStart(fd, loc), To: dayStart(nextDay(td), loc), MatterID: matter})
	if err != nil {
		return Widget{}, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].StartsAt < rows[j].StartsAt })
	q := map[string]string{"view": "day", "date": from}
	if !matter.IsZero() {
		q["matter_id"] = string(matter)
	}
	w := Widget{ID: "todays-calendar", Title: "Today's calendar", PagePath: "/calendar",
		Target: WidgetTarget{Path: "/calendar", Query: q}, Rows: []WidgetRow{}}
	for _, e := range rows {
		sub := "All day"
		if !e.AllDay {
			sub = e.Start().In(loc).Format("3:04 PM") + " – " + e.End().In(loc).Format("3:04 PM")
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(e.ID), Title: e.Title, Subtitle: sub, TargetPath: "/calendar/events/" + string(e.ID)})
	}
	return w, nil
}

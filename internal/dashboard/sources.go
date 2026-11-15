package dashboard

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/calendar"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// CoreServices adapts the domain services to the dashboard. Every adapter calls the
// owning service as the viewer, so the service's own role, wall and attorney-only
// checks decide what the widget, badge, search box or bell shows. Calendar is optional
// until the core composes it; so is DocTender, the review queue behind the sidebar's
// Documents > Doc Tender count (PRD 8.21).
type CoreServices struct {
	Practice  *practice.Service
	Trust     *trust.Service
	Billing   *billing.Service
	Calendar  *calendar.Service
	DocTender ReviewQueue
	Settings  *settings.Registry
	Now       func() time.Time
}

// ReviewQueue is the part of the Doc Tender service the sidebar reads: the open review
// items p can see, counted by the service as p (*doctender.Service satisfies it).
type ReviewQueue interface {
	OpenCount(ctx context.Context, p domain.Principal) (int, error)
}

func (c CoreServices) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c CoreServices) zone(ctx context.Context) *time.Location {
	if c.Settings != nil {
		if z, err := c.Settings.Get(ctx, "firm.time_zone"); err == nil && z != "" {
			if loc, err := time.LoadLocation(z); err == nil {
				return loc
			}
		}
	}
	return time.UTC
}

// practiceWidget and trustWidget convert a widget, keeping its first widget_rows rows as
// the other sources do; the title opens the full list.
func practiceWidget(w practice.Widget) Widget {
	out := Widget{ID: w.ID, Title: w.Title, Target: Target{Path: w.PagePath, Query: w.Query}, PagePath: w.PagePath}
	for _, r := range w.Rows {
		if len(out.Rows) >= fixed.WidgetRows {
			break
		}
		out.Rows = append(out.Rows, WidgetRow{ID: r.ID, Title: r.Title, Subtitle: r.Subtitle, TargetPath: r.TargetPath})
	}
	return out
}

func trustWidget(w trust.Widget) Widget {
	out := Widget{ID: w.ID, Title: w.Title, Target: Target{Path: w.Target.Path, Query: w.Target.Query}, PagePath: w.PagePath}
	for _, r := range w.Rows {
		if len(out.Rows) >= fixed.WidgetRows {
			break
		}
		out.Rows = append(out.Rows, WidgetRow{ID: r.ID, Title: r.Title, Subtitle: r.Subtitle, TargetPath: r.TargetPath})
	}
	return out
}

// calendarWidget converts a calendar widget, keeping the first widget_rows rows as every
// other source does; its title opens the full list.
func calendarWidget(w calendar.Widget) Widget {
	out := Widget{ID: w.ID, Title: w.Title, Target: Target{Path: w.Target.Path, Query: w.Target.Query}, PagePath: w.PagePath}
	for _, r := range w.Rows {
		if len(out.Rows) >= fixed.WidgetRows {
			break
		}
		out.Rows = append(out.Rows, WidgetRow{ID: r.ID, Title: r.Title, Subtitle: r.Subtitle, TargetPath: r.TargetPath})
	}
	return out
}

func (c CoreServices) matterLabel(ctx context.Context, p domain.Principal, id domain.ID) string {
	if c.Practice == nil || id.IsZero() {
		return ""
	}
	v, err := c.Practice.GetMatter(ctx, p, id)
	if err != nil {
		return ""
	}
	return v.Matter.Number + " " + v.Matter.Name
}

func hours(minutes int64) string { return fmt.Sprintf("%.1f h", float64(minutes)/60) }

func money(v any) string {
	switch n := v.(type) {
	case int64:
		return domain.Cents(n).String()
	case int:
		return domain.Cents(n).String()
	case domain.Cents:
		return n.String()
	case float64:
		return domain.Cents(int64(n)).String()
	}
	return fmt.Sprint(v)
}

// Sources is the widget source for each core widget whose service is composed.
func (c CoreServices) Sources() map[string]Source {
	src := map[string]Source{}
	if c.Practice != nil {
		src["my-open-matters"] = func(ctx context.Context, p domain.Principal, _ map[string]string) (Widget, error) {
			w, err := c.Practice.OpenMattersWidgetTop(ctx, p, fixed.WidgetRows)
			return practiceWidget(w), err
		}
		src["pending-conflict-checks"] = func(ctx context.Context, p domain.Principal, _ map[string]string) (Widget, error) {
			w, err := c.Practice.ConflictWidgetTop(ctx, p, fixed.WidgetRows)
			return practiceWidget(w), err
		}
		src["recent-activity"] = func(ctx context.Context, p domain.Principal, _ map[string]string) (Widget, error) {
			groups, err := c.Practice.ActivityFeed(ctx, p, "")
			if err != nil {
				return Widget{}, err
			}
			w := Widget{Title: "Recent activity", Target: Target{Path: "/practice/activity", Query: map[string]string{}}}
			for _, g := range groups {
				for _, r := range g.Rows {
					if len(w.Rows) >= fixed.ActivityRows {
						break
					}
					link := "/practice/activity"
					if !r.MatterID.IsZero() {
						link = "/practice/matters/" + string(r.MatterID)
					}
					w.Rows = append(w.Rows, WidgetRow{ID: string(r.ID), Title: r.Summary, Subtitle: r.ActorName + " · " + g.Label, TargetPath: link})
				}
			}
			return w, nil
		}
	}
	if c.Trust != nil {
		src["trust-alerts"] = func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
			w, err := c.Trust.TrustAlertsWidget(ctx, p, domain.ID(f["matter_id"]))
			return trustWidget(w), err
		}
		src["replenishment-requests"] = func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
			w, err := c.Trust.ReplenishmentWidget(ctx, p, domain.ID(f["matter_id"]))
			return trustWidget(w), err
		}
	}
	if c.Calendar != nil {
		src["upcoming-deadlines"] = func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
			w, err := c.Calendar.UpcomingDeadlinesWidgetTop(ctx, p, f["from_date"], f["to_date"], domain.ID(f["matter_id"]), fixed.WidgetRows)
			return calendarWidget(w), err
		}
		src["todays-calendar"] = func(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
			w, err := c.Calendar.TodaysCalendarWidget(ctx, p, f["from_date"], f["to_date"], domain.ID(f["matter_id"]))
			return calendarWidget(w), err
		}
	}
	if c.Billing != nil {
		src["my-time"] = c.myTime
		src["running-timer"] = c.runningTimer
		src["unbilled"] = c.unbilled
		src["ar-aging"] = c.arAging
	}
	return src
}

// myTime is the viewer's time today and this week (Monday start, firm time zone).
func (c CoreServices) myTime(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
	loc := c.zone(ctx)
	today := c.now().In(loc)
	sinceMonday := (int(today.Weekday()) + 6) % 7
	from := time.Date(today.Year(), today.Month(), today.Day()-sinceMonday, 0, 0, 0, 0, loc).Format("2006-01-02")
	to := today.Format("2006-01-02")
	if f["from_date"] != "" {
		from = f["from_date"]
	}
	if f["to_date"] != "" {
		to = f["to_date"]
	}
	rows, err := c.Billing.TimeEntries(ctx, p, billing.TimeFilter{UserID: p.UserID, FromDate: from, ToDate: to, MatterID: domain.ID(f["matter_id"])})
	if err != nil {
		return Widget{}, err
	}
	var day, week int64
	for _, t := range rows {
		week += t.Minutes()
		if t.WorkedOn == to {
			day += t.Minutes()
		}
	}
	q := map[string]string{"user_id": string(p.UserID), "from_date": from, "to_date": to}
	if f["matter_id"] != "" {
		q["matter_id"] = f["matter_id"]
	}
	w := Widget{Title: "My time today and this week", Target: Target{Path: "/billing/time", Query: q}}
	w.Rows = append(w.Rows,
		WidgetRow{ID: "today", Title: "Today: " + hours(day), TargetPath: Target{Path: "/billing/time", Query: map[string]string{"user_id": string(p.UserID), "from_date": to, "to_date": to}}.URL()},
		WidgetRow{ID: "week", Title: "This week: " + hours(week), Subtitle: fmt.Sprintf("%d entries since %s", len(rows), from), TargetPath: Target{Path: "/billing/time", Query: q}.URL()})
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].WorkedOn > rows[j].WorkedOn })
	for _, t := range rows {
		if len(w.Rows) >= fixed.WidgetRows {
			break
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(t.ID), Title: t.WorkedOn + " · " + hours(t.Minutes()), Subtitle: t.Description, TargetPath: "/billing/time/" + string(t.ID)})
	}
	return w, nil
}

func (c CoreServices) runningTimer(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
	rows, err := c.Timers(ctx, p)
	if err != nil {
		return Widget{}, err
	}
	q := map[string]string{}
	if f["matter_id"] != "" {
		q["matter_id"] = f["matter_id"]
	}
	w := Widget{Title: "Running timer", Target: Target{Path: "/billing/time", Query: q}}
	for _, t := range rows {
		if t.Status == "stopped" || (f["matter_id"] != "" && string(t.MatterID) != f["matter_id"]) {
			continue
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(t.ID), Title: t.Matter, Subtitle: fmt.Sprintf("%s, %d min", t.Status, t.Minutes), TargetPath: "/billing/time?matter_id=" + string(t.MatterID)})
	}
	return w, nil
}

func reportFilter(f map[string]string) billing.ReportFilter {
	return billing.ReportFilter{MatterID: domain.ID(f["matter_id"]), FromDate: f["from_date"], ToDate: f["to_date"]}
}

func filterQuery(f map[string]string) map[string]string {
	q := map[string]string{}
	for _, k := range []string{"matter_id", "from_date", "to_date"} {
		if f[k] != "" {
			q[k] = f[k]
		}
	}
	return q
}

// largestTotals is the first n of totals in a stable sort by amount, largest first,
// without sorting every matter.
func largestTotals(totals []billing.UnbilledTotal, n int) []billing.UnbilledTotal {
	var top []billing.UnbilledTotal
	for _, t := range totals {
		var at int // after every kept total at least as large: ties stay in order
		for at < len(top) && top[at].Cents >= t.Cents {
			at++
		}
		if at >= n {
			continue
		}
		top = append(top, billing.UnbilledTotal{})
		copy(top[at+1:], top[at:])
		top[at] = t
		if len(top) > n {
			top = top[:n]
		}
	}
	return top
}

func (c CoreServices) unbilled(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
	totals, err := c.Billing.UnbilledTotals(ctx, p, reportFilter(f))
	if err != nil {
		return Widget{}, err
	}
	var total int64
	for _, t := range totals {
		total += int64(t.Cents)
	}
	w := Widget{Title: "Unbilled time", Target: Target{Path: "/billing/reports/unbilled-time", Query: filterQuery(f)}}
	if len(totals) > 0 {
		w.Rows = append(w.Rows, WidgetRow{ID: "total", Title: "Total unbilled " + domain.Cents(total).String(), Subtitle: fmt.Sprintf("%d matters", len(totals)), TargetPath: Target{Path: "/billing/reports/unbilled-time", Query: filterQuery(f)}.URL()})
	}
	top := largestTotals(totals, fixed.WidgetRows)
	for _, t := range top {
		if len(w.Rows) >= fixed.WidgetRows {
			break
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(t.MatterID), Title: c.Billing.MatterNumber(ctx, t.MatterID) + " · " + t.Cents.String(), Subtitle: fmt.Sprintf("%d unbilled items", t.Items), TargetPath: "/practice/matters/" + string(t.MatterID)})
	}
	return w, nil
}

func (c CoreServices) arAging(ctx context.Context, p domain.Principal, f map[string]string) (Widget, error) {
	buckets, top, err := c.Billing.ARAgingSummary(ctx, p, reportFilter(f), fixed.ActivityRows)
	if err != nil {
		return Widget{}, err
	}
	names := make([]string, 0, len(buckets))
	for b := range buckets {
		names = append(names, b)
	}
	sort.Strings(names)
	w := Widget{Title: "AR aging", Target: Target{Path: "/billing/reports/ar-aging", Query: filterQuery(f)}}
	for _, b := range names {
		q := filterQuery(f)
		w.Rows = append(w.Rows, WidgetRow{ID: b, Title: b + ": " + money(int64(buckets[b])), TargetPath: Target{Path: "/billing/reports/ar-aging", Query: q}.URL()})
	}
	for _, r := range top {
		if len(w.Rows) >= fixed.ActivityRows {
			break
		}
		w.Rows = append(w.Rows, WidgetRow{ID: string(r.InvoiceID), Title: r.Number + " · " + money(int64(r.BalanceCents)),
			Subtitle: r.MatterNumber + ", " + fmt.Sprint(r.DaysPastDue) + " days past due", TargetPath: "/billing/invoices/" + string(r.InvoiceID)})
	}
	return w, nil
}

// Timers lists the viewer's timers through billing.
func (c CoreServices) Timers(ctx context.Context, p domain.Principal) ([]TimerRow, error) {
	if c.Billing == nil {
		return []TimerRow{}, nil
	}
	rows, err := c.Billing.Timers(ctx, p)
	if err != nil {
		return nil, err
	}
	out := []TimerRow{}
	for _, t := range rows {
		secs := t.AccumulatedSeconds
		if t.RunningSince != nil {
			secs += int64(c.now().Sub(*t.RunningSince).Seconds())
		}
		label := c.matterLabel(ctx, p, t.MatterID)
		if label == "" {
			label = "Timer"
		}
		out = append(out, TimerRow{ID: t.ID, MatterID: t.MatterID, Matter: label, Status: t.Status(), Minutes: secs / 60})
	}
	return out, nil
}

// Badges are the sidebar counts: active matters, draft invoices and open Doc Tender
// review items the viewer can read.
func (c CoreServices) Badges() map[string]func(ctx context.Context, p domain.Principal) (int, error) {
	b := map[string]func(ctx context.Context, p domain.Principal) (int, error){}
	if c.Practice != nil {
		b["matters"] = func(ctx context.Context, p domain.Principal) (int, error) {
			return c.Practice.CountMatters(ctx, p, practice.StatusOpen, "")
		}
	}
	if c.Billing != nil {
		b["invoices_draft"] = func(ctx context.Context, p domain.Principal) (int, error) {
			rows, err := c.Billing.Invoices(ctx, p, billing.InvoiceFilter{Status: "draft", Heads: true})
			return len(rows), err
		}
	}
	if c.DocTender != nil {
		b["doc_tender_open"] = c.DocTender.OpenCount
	}
	return b
}

// trustNotifier is the bell over the notifications table that trust writes.
type trustNotifier struct{ t *trust.Service }

// Notifier returns the bell, or nil when trust is not composed.
func (c CoreServices) Notifier() Notifier {
	if c.Trust == nil {
		return nil
	}
	return trustNotifier{c.Trust}
}

func fromTrust(n trust.Notification) Notification {
	return Notification{ID: n.ID, Kind: n.Kind, Title: n.Title, Link: n.Link, At: n.CreatedAt, ReadAt: n.ReadAt}
}

func (n trustNotifier) UnreadCount(ctx context.Context, p domain.Principal) (int, error) {
	return n.t.UnreadCount(ctx, p)
}

func (n trustNotifier) Recent(ctx context.Context, p domain.Principal, limit int) ([]Notification, error) {
	page, err := n.t.Notifications(ctx, p, nil, "", limit)
	if err != nil {
		return nil, err
	}
	out := []Notification{}
	for _, row := range page.Items {
		out = append(out, fromTrust(row))
	}
	return out, nil
}

func (n trustNotifier) MarkRead(ctx context.Context, p domain.Principal, id domain.ID) (Notification, error) {
	row, err := n.t.MarkRead(ctx, p, id)
	return fromTrust(row), err
}

func (n trustNotifier) MarkAllRead(ctx context.Context, p domain.Principal) (int, error) {
	return n.t.MarkAllRead(ctx, p)
}

// practiceSearch is the header search over practice's search service.
type practiceSearch struct{ s *practice.Service }

// Searcher returns the header search, or nil when practice is not composed.
func (c CoreServices) Searcher() Searcher {
	if c.Practice == nil {
		return nil
	}
	return practiceSearch{c.Practice}
}

func (s practiceSearch) Search(ctx context.Context, p domain.Principal, q, scope string) ([]SearchGroup, error) {
	res, err := s.s.Search(ctx, p, q, scope)
	if err != nil {
		return nil, err
	}
	out := []SearchGroup{}
	for _, g := range res.Groups {
		sg := SearchGroup{Scope: g.Scope, Hits: []SearchHit{}}
		for _, h := range g.Hits {
			sg.Hits = append(sg.Hits, SearchHit{Title: h.Title, Subtitle: h.Subtitle, RecordType: h.RecordType, TargetPath: h.TargetPath})
		}
		out = append(out, sg)
	}
	return out, nil
}

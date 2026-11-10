package calendar

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Event kinds from PRD section 5.
const (
	KindCourtDate     = "court_date"
	KindClientMeeting = "client_meeting"
	KindDeadline      = "deadline"
	KindBlock         = "block"
	KindOther         = "other"
)

// Deadline statuses.
const (
	StatusOpen   = "open"
	StatusDone   = "done"
	StatusWaived = "waived"
)

// Setting keys this package reads through the registry.
const (
	settingZone            = "firm.time_zone"
	settingDeadlineOffsets = "calendar.deadline_reminder_days"
	settingEventOffset     = "calendar.event_reminder_days"
	settingICal            = "calendar.ical_enabled"
	settingRangeMonths     = "calendar.available_dates_range_months"
	settingPurpose         = "calendar.available_dates_purpose"
	settingTitle           = "calendar.available_dates_title"
)

func validKind(k string) bool {
	switch k {
	case KindCourtDate, KindClientMeeting, KindDeadline, KindBlock, KindOther:
		return true
	}
	return false
}

func validStatus(s string) bool {
	return s == StatusOpen || s == StatusDone || s == StatusWaived
}

// Event is a calendar event with its attendees.
type Event struct {
	domain.CalendarEvent
	Attendees []domain.ID
}

// Start and End return the stored UTC instants.
func (e Event) Start() time.Time { return domain.FromUnixMicro(e.StartsAt) }
func (e Event) End() time.Time   { return domain.FromUnixMicro(e.EndsAt) }

// EventInput creates an event or, with every field set, replaces one.
type EventInput struct {
	Title         string
	Kind          string
	StartsAt      time.Time
	EndsAt        time.Time
	AllDay        bool
	MatterID      domain.ID
	Location      string
	TravelMinutes int
	Notes         string
	Attendees     []domain.ID
}

// EventPatch changes only the fields that are set.
type EventPatch struct {
	Title         *string
	Kind          *string
	StartsAt      *time.Time
	EndsAt        *time.Time
	AllDay        *bool
	MatterID      *domain.ID
	Location      *string
	TravelMinutes *int
	Notes         *string
	Attendees     *[]domain.ID
}

// EventFilter selects events. From and To bound the overlap window; zero values are open.
type EventFilter struct {
	From     time.Time
	To       time.Time
	MatterID domain.ID
	UserID   domain.ID
	Kind     string
}

// Deadline is a deadline with its reminder offsets parsed.
type Deadline struct {
	domain.Deadline
	Offsets []int
}

// DeadlineInput creates a deadline.
type DeadlineInput struct {
	MatterID          domain.ID
	Title             string
	DueOn             string
	ResponsibleUserID domain.ID
	Status            string
	Offsets           []int
}

// DeadlinePatch changes only the fields that are set.
type DeadlinePatch struct {
	MatterID          *domain.ID
	Title             *string
	DueOn             *string
	ResponsibleUserID *domain.ID
	Status            *string
	Offsets           *[]int
}

// DeadlineFilter selects deadlines. From and To are inclusive YYYY-MM-DD dates.
// IncludeOverdue adds open deadlines due before From that are also before the
// firm-local today (overdue), which is how the upcoming
// deadlines widget and the deadlines page show overdue items until done or waived.
type DeadlineFilter struct {
	From              string
	To                string
	MatterID          domain.ID
	ResponsibleUserID domain.ID
	Status            string
	IncludeOverdue    bool
}

// Feed is one revocable iCal feed. OwnerUserID zero is the firm feed.
// Only the SHA-256 of the secret is kept.
type Feed struct {
	domain.Meta
	OwnerUserID domain.ID
	SecretHash  string
	Revoked     bool
	RevokedAt   time.Time
	RevokedBy   domain.ID
}

// Notice is one reminder delivered to a user, in-app and, when mail is wired, by email.
type Notice struct {
	ID       domain.ID
	UserID   domain.ID
	Kind     string // deadline_reminder or event_reminder
	Title    string
	Body     string
	Path     string
	MatterID domain.ID
	RecordID domain.ID
	At       time.Time
	Emailed  bool
}

// Notice kinds.
const (
	NoticeDeadline = "deadline_reminder"
	NoticeEvent    = "event_reminder"
)

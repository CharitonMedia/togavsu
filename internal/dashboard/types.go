package dashboard

import (
	"context"
	"errors"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// GridColumns is the home screen width in grid columns (PRD 8.6). It is the shape of
// the grid, not a tunable.
const GridColumns = 12

// CodeLayoutLimit is the contract error code for a saved layout over the per-user cap.
const CodeLayoutLimit = "layout_limit"

// ErrSlotTaken is returned by a store when a concurrent insert took the slot first.
var ErrSlotTaken = errors.New("dashboard: layout slot taken")

// ErrNotFound is returned by a store for a missing row.
var ErrNotFound = errors.New("dashboard: not found")

// LayoutWidget is one placed widget: grid position, size and the widget's filters.
type LayoutWidget struct {
	WidgetID string            `json:"widget_id"`
	X        int               `json:"x"`
	Y        int               `json:"y"`
	W        int               `json:"w"`
	H        int               `json:"h"`
	Filters  map[string]string `json:"filters,omitempty"`
}

// Layout is a named saved layout (home_layouts). Slot keeps the per-user cap race free:
// each saved layout holds one slot number below the cap, unique per user.
type Layout struct {
	ID        domain.ID      `json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	CreatedBy domain.ID      `json:"created_by"`
	UpdatedAt time.Time      `json:"updated_at"`
	UserID    domain.ID      `json:"-"`
	Name      string         `json:"name"`
	Default   bool           `json:"default"`
	Slot      int            `json:"-"`
	Widgets   []LayoutWidget `json:"widgets"`
}

// RoleLayout is the admin-set default layout for one role.
type RoleLayout struct {
	Role      domain.Role    `json:"role"`
	Widgets   []LayoutWidget `json:"widgets"`
	UpdatedAt time.Time      `json:"-"`
	UpdatedBy domain.ID      `json:"-"`
}

// SubgroupState is one sidebar subgroup's collapsed flag.
type SubgroupState struct {
	ID        string `json:"id"`
	Collapsed bool   `json:"collapsed"`
}

// SidebarState is saved per user so it follows them to another computer.
type SidebarState struct {
	Collapsed bool            `json:"collapsed"`
	Subgroups []SubgroupState `json:"subgroups"`
}

// MeSettings is My settings (PRD 8.17). "firm" means use the firm's value.
type MeSettings struct {
	EventReminderDays    []int  `json:"event_reminder_offsets_days"`
	DeadlineReminderDays []int  `json:"deadline_reminder_offsets_days"`
	TimeZone             string `json:"time_zone"`
	Mode                 string `json:"mode"`
	TextSize             string `json:"text_size"`
	IdleTimeoutSeconds   int    `json:"idle_timeout_seconds"`
}

// Prefs is everything stored per user besides layouts.
type Prefs struct {
	Sidebar  SidebarState `json:"sidebar"`
	Settings *MeSettings  `json:"settings,omitempty"`
}

// NotificationPref is one cell row of the preference grid.
type NotificationPref struct {
	Kind          string `json:"kind"`
	InApp         bool   `json:"in_app"`
	Email         bool   `json:"email"`
	EmailRequired bool   `json:"email_required"`
}

// ProblemReport is a "Report a problem" submission.
type ProblemReport struct {
	ID        domain.ID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy domain.ID `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Page      string    `json:"page"`
	Message   string    `json:"message"`
	UserID    domain.ID `json:"user_id"`
}

// Store persists the ui records. Implementations: Memory and PG.
type Store interface {
	Working(ctx context.Context, user domain.ID) ([]LayoutWidget, bool, error)
	PutWorking(ctx context.Context, user domain.ID, widgets []LayoutWidget, at time.Time) error
	Layouts(ctx context.Context, user domain.ID) ([]Layout, error)
	// InsertLayout stores l in l.Slot. It returns ErrSlotTaken if another layout of the
	// same user holds that slot already.
	InsertLayout(ctx context.Context, l Layout) error
	UpdateLayout(ctx context.Context, l Layout) error
	DeleteLayout(ctx context.Context, user, id domain.ID) error
	// SetDefault marks id as the user's only default layout; the zero id clears it.
	SetDefault(ctx context.Context, user, id domain.ID, at time.Time) error
	RoleLayout(ctx context.Context, role domain.Role) (RoleLayout, bool, error)
	PutRoleLayout(ctx context.Context, l RoleLayout) error
	Prefs(ctx context.Context, user domain.ID) (Prefs, bool, error)
	PutPrefs(ctx context.Context, user domain.ID, p Prefs, at time.Time) error
	NotificationPrefs(ctx context.Context, user domain.ID) ([]NotificationPref, error)
	PutNotificationPrefs(ctx context.Context, user domain.ID, rows []NotificationPref, at time.Time) error
	InsertProblem(ctx context.Context, r ProblemReport) error
	Problems(ctx context.Context) ([]ProblemReport, error)
}

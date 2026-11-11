package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/settings"
)

// bounds are the ui's fixed shapes and input-safety caps (fixed.json). They are not
// firm settings; the firm's tunables are read through the settings registry.
type bounds struct {
	GridColumns         int `json:"grid_columns"`
	GridOrigin          int `json:"grid_origin"`
	MinWidgetSize       int `json:"min_widget_size"`
	MaxWidgetHeight     int `json:"max_widget_height"`
	MaxWidgetsPerLayout int `json:"max_widgets_per_layout"`
	DefaultWidgetW      int `json:"default_widget_w"`
	DefaultWidgetH      int `json:"default_widget_h"`
	LayoutNameMax       int `json:"layout_name_max"`
	SaveAttempts        int `json:"save_attempts"`
	MaxSubgroups        int `json:"max_subgroups"`
	SubgroupIDMax       int `json:"subgroup_id_max"`
	MaxReminderOffsets  int `json:"max_reminder_offsets"`
	MinReminderDay      int `json:"min_reminder_day"`
	MaxReminderDay      int `json:"max_reminder_day"`
	MinIdleSeconds      int `json:"min_idle_seconds"`
	ProblemMessageMax   int `json:"problem_message_max"`
	ProblemPageMax      int `json:"problem_page_max"`
	MailSubjectChars    int `json:"mail_subject_chars"`
	RecentNotifications int `json:"recent_notifications"`
	WidgetRows          int `json:"widget_rows"`
	ActivityRows        int `json:"activity_rows"`
	AuditRows           int `json:"audit_rows"`
	NoticeMax           int `json:"notice_max"`
	Initials            int `json:"initials"`
	UserPage            int `json:"user_page"`
	UserPagesMax        int `json:"user_pages_max"`
}

//go:embed fixed.json
var fixedJSON []byte

//go:embed catalog.json
var catalogJSON []byte

type catalogFile struct {
	Widgets     []WidgetDef         `json:"widgets"`
	RoleLayouts map[string][]string `json:"role_layouts"`
	Subgroups   map[string]string   `json:"subgroups"`
	Nav         []navSpec           `json:"nav"`
}

// navSpec is one core sidebar entry as catalog.json declares it.
type navSpec struct {
	NavEntry
	Resource string   `json:"resource"`
	Roles    []string `json:"roles"`
}

var (
	fixed   bounds
	catalog catalogFile
)

func init() {
	if err := json.Unmarshal(fixedJSON, &fixed); err != nil {
		panic(fmt.Sprintf("dashboard: fixed.json: %v", err))
	}
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic(fmt.Sprintf("dashboard: catalog.json: %v", err))
	}
	GridColumns = fixed.GridColumns
}

// registryDefault is a setting's registry default, for when a stored value cannot be
// read. The registry, not this package, owns the number.
func registryDefault(key string) string {
	for _, d := range settings.Definitions() {
		if d.Key == key {
			return d.Default
		}
	}
	return ""
}

func registryInt(key string) int {
	n, err := strconv.Atoi(registryDefault(key))
	if err != nil {
		panic("dashboard: setting " + key + " has no integer default")
	}
	return n
}

// capitalize upper-cases the first letter.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// truncate keeps the first n runes.
func truncate(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}

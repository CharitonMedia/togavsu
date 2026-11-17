package settings

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// How the admin page presents a setting without showing raw formats.
const (
	PlainRaw           = "raw"            // bool, enum, cents, plain string/int
	PlainLines         = "lines"          // JSON string list ↔ one item per line
	PlainDuration      = "duration"       // Go duration ↔ amount + unit
	PlainBytes         = "bytes"          // byte count ↔ amount + bytes/MB/GB
	PlainRetry         = "retry"          // retry schedule JSON ↔ plain delay lines
	PlainNotifications = "notifications"  // channel grid
	PlainColors        = "colors"         // named color rows
	PlainCaseExamples  = "case_examples"  // case number formats
	PlainModelPrices   = "model_prices"   // read-only table; JSON in Advanced
	PlainTaskOverrides = "task_overrides" // read-only table; JSON in Advanced
	PlainNaming        = "naming"         // pattern + sample file name
	PlainAdvancedOnly  = "advanced_only"  // shown only behind Advanced
)

// ColorFields are the named slots in appearance.colors.
var ColorFields = []struct{ Key, Label string }{
	{"staff_sidebar", "Sidebar"},
	{"staff_header", "Header"},
	{"staff_page", "Page"},
	{"staff_accent", "Accent"},
	{"client_header", "Client header"},
}

// NotificationKinds are the firm default grid rows, in display order.
var NotificationKinds = []struct{ Kind, Label string }{
	{"trust_flag", "Trust flags"},
	{"deadline_reminder", "Deadline reminders"},
	{"replenishment", "Replenishment requests"},
	{"matter_assignment", "Matter assignment"},
	{"update_failure", "Update failures"},
	{"module_alert", "Module alerts"},
}

// PlainOf chooses the admin presentation for a definition.
func PlainOf(def Definition) string {
	switch def.Key {
	case keyCaseExamples:
		return PlainCaseExamples
	case keyCasePatterns, "files.backup_scope":
		return PlainAdvancedOnly
	case "doctender.retry_schedule":
		return PlainRetry
	case "notifications.defaults":
		return PlainNotifications
	case "appearance.colors":
		return PlainColors
	case "ai.model_prices":
		return PlainModelPrices
	case "ai.task_overrides":
		return PlainTaskOverrides
	case "doctender.naming_pattern":
		return PlainNaming
	case "files.max_upload_bytes", "files.upload_chunk_bytes", "editor.run_memory_bytes":
		return PlainBytes
	}
	switch def.Kind {
	case KindDuration:
		return PlainDuration
	case KindJSON:
		if def.Special == "regexp_list" || def.Special == "model_prices" {
			return PlainAdvancedOnly
		}
		return PlainLines
	}
	return PlainRaw
}

// AdvancedOnly reports whether the setting's editor lives only behind Advanced.
func AdvancedOnly(def Definition) bool {
	return PlainOf(def) == PlainAdvancedOnly
}

// HasAdvancedJSON reports whether the plain view is read-only and the JSON
// editor sits behind Advanced (model prices and task overrides).
func HasAdvancedJSON(def Definition) bool {
	p := PlainOf(def)
	return p == PlainModelPrices || p == PlainTaskOverrides
}

// DisplayPlain is the Default/value column text for a stored value.
func DisplayPlain(def Definition, value string) string {
	if value == "" && def.Kind != KindBool {
		return "None"
	}
	switch PlainOf(def) {
	case PlainDuration:
		s, err := FormatDurationPlain(value)
		if err != nil {
			return value
		}
		return s
	case PlainBytes:
		s, err := FormatBytesPlain(value)
		if err != nil {
			return value
		}
		return s
	case PlainLines, PlainCaseExamples:
		s, err := FormatLinesPlain(value)
		if err != nil {
			return value
		}
		if s == "" {
			return "None"
		}
		return s
	case PlainRetry:
		s, err := FormatRetryPlain(value)
		if err != nil {
			return value
		}
		return s
	case PlainNotifications:
		return FormatNotificationsPlain(value)
	case PlainColors:
		return FormatColorsPlain(value)
	case PlainModelPrices:
		return FormatModelPricesPlain(value)
	case PlainTaskOverrides:
		return FormatTaskOverridesPlain(value)
	case PlainNaming:
		return value
	case PlainAdvancedOnly:
		return value
	case PlainRaw:
		if def.Kind == KindBool {
			if value == "true" {
				return "On"
			}
			return "Off"
		}
		return value
	}
	return value
}

// FormatDurationPlain shows a stored Go duration as "12 hours", "15 minutes",
// or "180 milliseconds" in the largest unit that holds the value exactly.
func FormatDurationPlain(stored string) (string, error) {
	d, err := time.ParseDuration(stored)
	if err != nil {
		return "", err
	}
	if d < 0 {
		return "", fmt.Errorf("%w: negative duration", ErrInvalid)
	}
	ns := int64(d)
	switch {
	case ns == 0:
		return "Right away", nil
	case ns%int64(24*time.Hour) == 0:
		n := ns / int64(24*time.Hour)
		return plural(n, "day"), nil
	case ns%int64(time.Hour) == 0:
		n := ns / int64(time.Hour)
		return plural(n, "hour"), nil
	case ns%int64(time.Minute) == 0:
		n := ns / int64(time.Minute)
		return plural(n, "minute"), nil
	case ns%int64(time.Second) == 0:
		n := ns / int64(time.Second)
		return plural(n, "second"), nil
	default:
		n := ns / int64(time.Millisecond)
		return plural(n, "millisecond"), nil
	}
}

// DurationParts splits a stored duration into a whole amount and unit for the
// plain editor (milliseconds, seconds, minutes, hours, days).
func DurationParts(stored string) (amount int64, unit string, err error) {
	d, err := time.ParseDuration(stored)
	if err != nil {
		return 0, "", err
	}
	ns := int64(d)
	switch {
	case ns == 0:
		return 0, "seconds", nil
	case ns%int64(24*time.Hour) == 0:
		return ns / int64(24*time.Hour), "days", nil
	case ns%int64(time.Hour) == 0:
		return ns / int64(time.Hour), "hours", nil
	case ns%int64(time.Minute) == 0:
		return ns / int64(time.Minute), "minutes", nil
	case ns%int64(time.Second) == 0:
		return ns / int64(time.Second), "seconds", nil
	default:
		return ns / int64(time.Millisecond), "milliseconds", nil
	}
}

// ParseDurationPlain builds the stored Go duration string from amount and unit.
// The result is time.Duration.String(), matching what the old text form stored
// after a round-trip through ParseDuration for the same length.
func ParseDurationPlain(amountStr, unit string) (string, error) {
	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return "", fmt.Errorf("%w: duration amount is required", ErrInvalid)
	}
	n, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("%w: duration amount must be a whole number", ErrInvalid)
	}
	var unitDur time.Duration
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "millisecond", "milliseconds", "ms":
		unitDur = time.Millisecond
	case "second", "seconds", "s":
		unitDur = time.Second
	case "minute", "minutes", "m":
		unitDur = time.Minute
	case "hour", "hours", "h":
		unitDur = time.Hour
	case "day", "days", "d":
		unitDur = 24 * time.Hour
	default:
		return "", fmt.Errorf("%w: unknown duration unit", ErrInvalid)
	}
	d, err := scaleDuration(n, unitDur)
	if err != nil {
		return "", err
	}
	return d.String(), nil
}

// scaleDuration multiplies n by unit without wrapping int64.
func scaleDuration(n int64, unit time.Duration) (time.Duration, error) {
	if unit <= 0 {
		return 0, fmt.Errorf("%w: unknown duration unit", ErrInvalid)
	}
	u := int64(unit)
	if n > 0 && n > math.MaxInt64/u {
		return 0, fmt.Errorf("%w: duration is too large", ErrInvalid)
	}
	return time.Duration(n) * unit, nil
}

// FormatBytesPlain shows a byte count as "2 GB", "8 MB", or "1 GB".
func FormatBytesPlain(stored string) (string, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(stored), 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("%w: byte size", ErrInvalid)
	}
	const gb = int64(1) << 30
	const mb = int64(1) << 20
	if n >= gb && n%gb == 0 {
		return fmt.Sprintf("%d GB", n/gb), nil
	}
	if n >= mb && n%mb == 0 {
		return fmt.Sprintf("%d MB", n/mb), nil
	}
	if n%mb == 0 {
		return fmt.Sprintf("%d MB", n/mb), nil
	}
	return fmt.Sprintf("%d bytes", n), nil
}

// BytesParts splits a stored byte count into amount and unit for the editor.
// Whole GB/MB values use those units; any other count uses bytes so a Save
// without edits stores the same value (never pre-fills a different scale).
func BytesParts(stored string) (amount int64, unit string, err error) {
	n, err := strconv.ParseInt(strings.TrimSpace(stored), 10, 64)
	if err != nil || n < 0 {
		return 0, "", fmt.Errorf("%w: byte size", ErrInvalid)
	}
	const gb = int64(1) << 30
	const mb = int64(1) << 20
	if n >= gb && n%gb == 0 {
		return n / gb, "GB", nil
	}
	if n%mb == 0 {
		return n / mb, "MB", nil
	}
	return n, "bytes", nil
}

// ParseBytesPlain builds the stored byte count from amount and bytes/MB/GB.
func ParseBytesPlain(amountStr, unit string) (string, error) {
	amountStr = strings.TrimSpace(amountStr)
	n, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("%w: byte size must be a whole number", ErrInvalid)
	}
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "GB":
		scaled, err := scaleBytes(n, 30)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(scaled, 10), nil
	case "MB":
		scaled, err := scaleBytes(n, 20)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(scaled, 10), nil
	case "BYTES", "B":
		return strconv.FormatInt(n, 10), nil
	default:
		return "", fmt.Errorf("%w: byte unit is bytes, MB, or GB", ErrInvalid)
	}
}

// scaleBytes left-shifts n by shift bits without wrapping int64.
func scaleBytes(n int64, shift uint) (int64, error) {
	if shift >= 63 {
		return 0, fmt.Errorf("%w: byte size is too large", ErrInvalid)
	}
	if n > 0 && n > math.MaxInt64>>shift {
		return 0, fmt.Errorf("%w: byte size is too large", ErrInvalid)
	}
	return n << shift, nil
}

// FormatLinesPlain turns a JSON string list into one item per line.
func FormatLinesPlain(stored string) (string, error) {
	list, err := parseStringList(stored)
	if err != nil {
		return "", err
	}
	return strings.Join(list, "\n"), nil
}

// ParseLinesPlain turns a textarea (one item per line) into the stored JSON list.
// Blank lines are dropped.
func ParseLinesPlain(text string) (string, error) {
	var items []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, line)
	}
	if items == nil {
		items = []string{}
	}
	return jsonText(items), nil
}

// FormatRetryPlain shows retry delays as "Right away", "15 minutes", …
func FormatRetryPlain(stored string) (string, error) {
	list, err := parseStringList(stored)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(list))
	for _, d := range list {
		s, err := FormatDurationPlain(d)
		if err != nil {
			return "", err
		}
		lines = append(lines, s)
	}
	return strings.Join(lines, "\n"), nil
}

// ParseRetryPlain turns plain delay lines into the stored JSON duration list.
func ParseRetryPlain(text string) (string, error) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		stored, err := parsePlainDelay(line)
		if err != nil {
			return "", err
		}
		out = append(out, stored)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("%w: retry schedule needs at least one delay", ErrInvalid)
	}
	return jsonText(out), nil
}

func parsePlainDelay(line string) (string, error) {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "right away" || lower == "immediately" || lower == "0" {
		return (0 * time.Second).String(), nil
	}
	parts := strings.Fields(lower)
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: delay %q should look like \"15 minutes\"", ErrInvalid, line)
	}
	return ParseDurationPlain(parts[0], parts[1])
}

type notifyChannels struct {
	Email       []string `json:"email"`
	EmailLocked []string `json:"email_locked"`
	InApp       []string `json:"in_app"`
}

// NotifyRow is one plain notifications grid row for the admin template.
type NotifyRow struct {
	Kind          string
	Label         string
	InApp         bool
	Email         bool
	EmailRequired bool
}

// NotifyRows builds the checkbox grid from a stored notifications.defaults value.
func NotifyRows(stored string) []NotifyRow {
	var f notifyChannels
	if err := json.Unmarshal([]byte(stored), &f); err != nil {
		_ = json.Unmarshal([]byte(domain.DefaultNotificationPrefs), &f)
	}
	out := make([]NotifyRow, 0, len(NotificationKinds))
	for _, k := range NotificationKinds {
		out = append(out, NotifyRow{
			Kind: k.Kind, Label: k.Label,
			InApp: contains(f.InApp, k.Kind), Email: contains(f.Email, k.Kind),
			EmailRequired: contains(f.EmailLocked, k.Kind),
		})
	}
	return out
}

// FormatNotificationsPlain is a short Default-column summary.
func FormatNotificationsPlain(stored string) string {
	rows := NotifyRows(stored)
	var parts []string
	for _, r := range rows {
		ch := []string{}
		if r.InApp {
			ch = append(ch, "in app")
		}
		if r.Email {
			ch = append(ch, "email")
		}
		if len(ch) == 0 {
			continue
		}
		parts = append(parts, r.Label+": "+strings.Join(ch, ", "))
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, "; ")
}

// ParseNotificationsForm builds the stored JSON from the admin checkbox grid.
func ParseNotificationsForm(form url.Values) (string, error) {
	var f notifyChannels
	f.EmailLocked = []string{"trust_flag"}
	for _, k := range NotificationKinds {
		if form.Get(k.Kind+".in_app") == "on" {
			f.InApp = append(f.InApp, k.Kind)
		}
		if k.Kind == "trust_flag" || form.Get(k.Kind+".email") == "on" {
			f.Email = append(f.Email, k.Kind)
		}
	}
	if !contains(f.Email, "trust_flag") {
		f.Email = append(f.Email, "trust_flag")
	}
	return jsonText(f), nil
}

// ColorRow is one named color for the plain custom-colors editor.
type ColorRow struct {
	Key    string
	Label  string
	Custom bool
	Value  string // #rrggbb when Custom
	Preset string // current preset color for comparison
}

// ColorRows builds the color editor from stored JSON and the active preset colors.
func ColorRows(stored string, preset map[string]string) []ColorRow {
	var c map[string]string
	_ = json.Unmarshal([]byte(stored), &c)
	if c == nil {
		c = map[string]string{}
	}
	out := make([]ColorRow, 0, len(ColorFields))
	for _, f := range ColorFields {
		v := c[f.Key]
		custom := hexColor.MatchString(v)
		row := ColorRow{Key: f.Key, Label: f.Label, Custom: custom, Preset: preset[f.Key]}
		if custom {
			row.Value = v
		} else if row.Preset != "" {
			row.Value = row.Preset
		} else {
			row.Value = "#000000"
		}
		out = append(out, row)
	}
	return out
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// FormatColorsPlain summarizes stored custom colors.
func FormatColorsPlain(stored string) string {
	var c map[string]string
	if err := json.Unmarshal([]byte(stored), &c); err != nil || len(c) == 0 {
		return "None"
	}
	var parts []string
	for _, f := range ColorFields {
		if v := c[f.Key]; hexColor.MatchString(v) {
			parts = append(parts, f.Label+": "+v)
		}
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, "; ")
}

// ParseColorsForm stores only colors the admin picked (mode=custom).
// Saving with nothing picked stores {}.
func ParseColorsForm(form url.Values) (string, error) {
	out := map[string]string{}
	for _, f := range ColorFields {
		if form.Get("color_"+f.Key+"_mode") != "custom" {
			continue
		}
		v := strings.TrimSpace(form.Get("color_" + f.Key))
		if !hexColor.MatchString(v) {
			return "", fmt.Errorf("%w: %s must be #rrggbb", ErrInvalid, f.Label)
		}
		out[f.Key] = strings.ToLower(v)
	}
	return jsonText(out), nil
}

// FormatModelPricesPlain is a short table summary or "None set".
func FormatModelPricesPlain(stored string) string {
	prices, err := domain.ParseModelPrices(stored)
	if err != nil || len(prices) == 0 {
		return "None set"
	}
	names := make([]string, 0, len(prices))
	for name := range prices {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString("; ")
		}
		p := prices[name]
		fmt.Fprintf(&b, "%s: %.0f in / %.0f out ¢ per million tokens", name, p.Input, p.Output)
	}
	return b.String()
}

// FormatTaskOverridesPlain summarizes per-task overrides or "None set".
func FormatTaskOverridesPlain(stored string) string {
	var m map[string]map[string]string
	if err := json.Unmarshal([]byte(stored), &m); err != nil || len(m) == 0 {
		return "None set"
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		ov := m[name]
		var bits []string
		for _, k := range []string{"provider", "model", "check_provider", "check_model"} {
			if v := ov[k]; v != "" {
				bits = append(bits, k+"="+v)
			}
		}
		parts = append(parts, name+": "+strings.Join(bits, ", "))
	}
	return strings.Join(parts, "; ")
}

// NamingSample builds a sample file name from a naming pattern with sample values.
func NamingSample(pattern string) string {
	repl := map[string]string{
		domain.TokenClientLast:   "Smith",
		domain.TokenClientFirst:  "Jordan",
		domain.TokenMatterNumber: "2026-0001",
		domain.TokenMatterName:   "Smith-v-Jones",
		domain.TokenWhat:         "petition",
		domain.TokenDocDate:      "2026-03-15",
		domain.TokenReceivedDate: "2026-03-16",
		domain.TokenCategory:     "pleadings",
		domain.TokenExt:          "pdf",
	}
	out := pattern
	for tok, v := range repl {
		out = strings.ReplaceAll(out, tok, v)
	}
	return out
}

// ParsePlainForm converts an admin plain form into the stored value string.
func ParsePlainForm(def Definition, form url.Values) (string, error) {
	switch PlainOf(def) {
	case PlainDuration:
		return ParseDurationPlain(form.Get("amount"), form.Get("unit"))
	case PlainBytes:
		return ParseBytesPlain(form.Get("amount"), form.Get("unit"))
	case PlainLines, PlainCaseExamples:
		return ParseLinesPlain(form.Get("value"))
	case PlainRetry:
		return ParseRetryPlain(form.Get("value"))
	case PlainNotifications:
		return ParseNotificationsForm(form)
	case PlainColors:
		return ParseColorsForm(form)
	case PlainNaming, PlainRaw, PlainAdvancedOnly, PlainModelPrices, PlainTaskOverrides:
		return form.Get("value"), nil
	default:
		return form.Get("value"), nil
	}
}

func plural(n int64, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

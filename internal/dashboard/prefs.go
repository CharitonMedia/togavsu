package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// --- My settings (PRD 8.17) ---

func parseDays(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32); err == nil {
			out = append(out, int(n))
		}
	}
	return out
}

// settingDuration reads a duration setting, falling back to its registry default.
func (s *Service) settingDuration(ctx context.Context, key string) time.Duration {
	d, err := time.ParseDuration(s.setting(ctx, key))
	if err != nil {
		d, _ = time.ParseDuration(registryDefault(key))
	}
	return d
}

// FirmIdleSeconds is security.idle_timeout, the longest idle time the firm allows.
func (s *Service) FirmIdleSeconds(ctx context.Context) int {
	return int(s.settingDuration(ctx, "security.idle_timeout").Seconds())
}

// MySettings returns p's settings with firm defaults filled in.
func (s *Service) MySettings(ctx context.Context, p domain.Principal) (MeSettings, error) {
	if p.UserID.IsZero() {
		return MeSettings{}, domain.Unauthenticated("sign in is required")
	}
	prefs, _, err := s.d.Store.Prefs(ctx, p.UserID)
	if err != nil {
		return MeSettings{}, err
	}
	out := MeSettings{
		EventReminderDays:    parseDays(s.setting(ctx, "calendar.event_reminder_days")),
		DeadlineReminderDays: parseDays(s.setting(ctx, "calendar.deadline_reminder_days")),
		Mode:                 "firm", TextSize: "firm", IdleTimeoutSeconds: s.FirmIdleSeconds(ctx),
	}
	if m := prefs.Settings; m != nil {
		if m.EventReminderDays != nil {
			out.EventReminderDays = m.EventReminderDays
		}
		if m.DeadlineReminderDays != nil {
			out.DeadlineReminderDays = m.DeadlineReminderDays
		}
		out.TimeZone = m.TimeZone
		if m.Mode != "" {
			out.Mode = m.Mode
		}
		if m.TextSize != "" {
			out.TextSize = m.TextSize
		}
		if m.IdleTimeoutSeconds >= fixed.MinIdleSeconds && m.IdleTimeoutSeconds < out.IdleTimeoutSeconds {
			out.IdleTimeoutSeconds = m.IdleTimeoutSeconds
		}
	}
	if out.EventReminderDays == nil {
		out.EventReminderDays = []int{}
	}
	if out.DeadlineReminderDays == nil {
		out.DeadlineReminderDays = []int{}
	}
	return out, nil
}

// MeSettingsPatch is PATCH /api/v1/me/settings: every field optional.
type MeSettingsPatch struct {
	EventReminderDays    *[]int  `json:"event_reminder_offsets_days"`
	DeadlineReminderDays *[]int  `json:"deadline_reminder_offsets_days"`
	TimeZone             *string `json:"time_zone"`
	Mode                 *string `json:"mode"`
	TextSize             *string `json:"text_size"`
	IdleTimeoutSeconds   *int    `json:"idle_timeout_seconds"`
}

func validDays(field string, days []int) error {
	if len(days) > fixed.MaxReminderOffsets {
		return domain.Invalid(field, fmt.Sprintf("at most %d reminder offsets", fixed.MaxReminderOffsets))
	}
	for _, d := range days {
		if d < fixed.MinReminderDay || d > fixed.MaxReminderDay {
			return domain.Invalid(field, fmt.Sprintf("reminder offsets are %d to %d days", fixed.MinReminderDay, fixed.MaxReminderDay))
		}
	}
	return nil
}

// UpdateMySettings applies a patch. Idle time can only be shorter than the firm's.
func (s *Service) UpdateMySettings(ctx context.Context, p domain.Principal, in MeSettingsPatch) (MeSettings, error) {
	if p.UserID.IsZero() {
		return MeSettings{}, domain.Unauthenticated("sign in is required")
	}
	if err := s.patchMySettings(ctx, p, in); err != nil {
		return MeSettings{}, err
	}
	return s.MySettings(ctx, p)
}

func (s *Service) patchMySettings(ctx context.Context, p domain.Principal, in MeSettingsPatch) error {
	s.lmu.Lock()
	defer s.lmu.Unlock()
	prefs, _, err := s.d.Store.Prefs(ctx, p.UserID)
	if err != nil {
		return err
	}
	m := MeSettings{}
	if prefs.Settings != nil {
		m = *prefs.Settings
	}
	if in.EventReminderDays != nil {
		if err := validDays("event_reminder_offsets_days", *in.EventReminderDays); err != nil {
			return err
		}
		m.EventReminderDays = append([]int{}, *in.EventReminderDays...)
	}
	if in.DeadlineReminderDays != nil {
		if err := validDays("deadline_reminder_offsets_days", *in.DeadlineReminderDays); err != nil {
			return err
		}
		m.DeadlineReminderDays = append([]int{}, *in.DeadlineReminderDays...)
	}
	if in.TimeZone != nil {
		z := strings.TrimSpace(*in.TimeZone)
		if z != "" {
			if _, err := time.LoadLocation(z); err != nil {
				return domain.Invalid("time_zone", "unknown time zone")
			}
		}
		m.TimeZone = z
	}
	if in.Mode != nil {
		switch *in.Mode {
		case "light", "dark", "device", "firm":
			m.Mode = *in.Mode
		default:
			return domain.Invalid("mode", "mode is light, dark, device or firm")
		}
	}
	if in.TextSize != nil {
		switch *in.TextSize {
		case "sm", "md", "lg", "firm":
			m.TextSize = *in.TextSize
		default:
			return domain.Invalid("text_size", "text size is sm, md, lg or firm")
		}
	}
	if in.IdleTimeoutSeconds != nil {
		n := *in.IdleTimeoutSeconds
		if n < fixed.MinIdleSeconds {
			return domain.Invalid("idle_timeout_seconds", fmt.Sprintf("at least %d seconds", fixed.MinIdleSeconds))
		}
		if firm := s.FirmIdleSeconds(ctx); n > firm {
			return domain.Invalid("idle_timeout_seconds", fmt.Sprintf("the firm allows at most %d seconds", firm))
		}
		m.IdleTimeoutSeconds = n
	}
	prefs.Settings = &m
	return s.d.Store.PutPrefs(ctx, p.UserID, prefs, s.d.Now().UTC())
}

// --- Notification preferences (PRD 8.17) ---

// NotificationKinds are the core kinds in the grid, in display order.
var NotificationKinds = []struct{ Kind, Label string }{
	{"trust_flag", "Trust flags"}, {"deadline_reminder", "Deadline reminders"}, {"replenishment", "Replenishment requests"},
	{"matter_assignment", "Matter assignment"}, {"update_failure", "Update failures"}, {"module_alert", "Module alerts"},
}

type firmChannels struct {
	Email       []string `json:"email"`
	EmailLocked []string `json:"email_locked"`
	InApp       []string `json:"in_app"`
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) firmChannels(ctx context.Context) firmChannels {
	var f firmChannels
	if err := json.Unmarshal([]byte(s.setting(ctx, "notifications.defaults")), &f); err != nil {
		_ = json.Unmarshal([]byte(domain.DefaultNotificationPrefs), &f)
	}
	return f
}

// NotificationPrefs is p's grid: the firm defaults with p's choices on top. A locked
// email (the trust below-zero email) stays on whatever p stored.
func (s *Service) NotificationPrefs(ctx context.Context, p domain.Principal) ([]NotificationPref, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	f := s.firmChannels(ctx)
	mine, err := s.d.Store.NotificationPrefs(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	byKind := map[string]NotificationPref{}
	for _, m := range mine {
		byKind[m.Kind] = m
	}
	var out []NotificationPref
	for _, k := range NotificationKinds {
		row := NotificationPref{Kind: k.Kind, InApp: has(f.InApp, k.Kind), Email: has(f.Email, k.Kind), EmailRequired: has(f.EmailLocked, k.Kind)}
		if m, ok := byKind[k.Kind]; ok {
			row.InApp, row.Email = m.InApp, m.Email
		}
		if row.EmailRequired {
			row.Email = true
		}
		out = append(out, row)
	}
	return out, nil
}

// PutNotificationPrefs saves p's grid. Turning off a required email is refused.
func (s *Service) PutNotificationPrefs(ctx context.Context, p domain.Principal, rows []NotificationPref) ([]NotificationPref, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	f := s.firmChannels(ctx)
	known := map[string]bool{}
	for _, k := range NotificationKinds {
		known[k.Kind] = true
	}
	seen := map[string]bool{}
	var store []NotificationPref
	for i, r := range rows {
		field := fmt.Sprintf("items[%d]", i)
		if !known[r.Kind] || seen[r.Kind] {
			return nil, domain.Invalid(field+".kind", "unknown or repeated notification kind")
		}
		seen[r.Kind] = true
		if has(f.EmailLocked, r.Kind) && !r.Email {
			return nil, domain.Invalid(field+".email", "the trust below-zero email can't be turned off")
		}
		store = append(store, NotificationPref{Kind: r.Kind, InApp: r.InApp, Email: r.Email})
	}
	sort.Slice(store, func(i, j int) bool { return store[i].Kind < store[j].Kind })
	if err := s.d.Store.PutNotificationPrefs(ctx, p.UserID, store, s.d.Now().UTC()); err != nil {
		return nil, err
	}
	return s.NotificationPrefs(ctx, p)
}

// Wants reports whether user wants kind on channel ("in_app" or "email"). Senders in
// other domains ask this before they deliver; a required email is always wanted.
func (s *Service) Wants(ctx context.Context, user domain.ID, kind, channel string) (bool, error) {
	rows, err := s.NotificationPrefs(ctx, domain.Principal{UserID: user, Roles: []domain.Role{domain.RoleStaff}})
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.Kind == kind {
			if channel == "email" {
				return r.Email || r.EmailRequired, nil
			}
			return r.InApp, nil
		}
	}
	return channel == "in_app", nil
}

// --- Appearance (PRD 8.17) ---

// Preset is one named color scheme.
type Preset struct {
	ID, Label                     string
	Sidebar, Header, Page, Accent string
	SidebarText, HeaderText       string
}

// Presets are the PRD 8.17 presets. The label is the value appearance.preset stores.
var Presets = []Preset{
	{ID: "navy_gold", Label: "Navy and gold", Sidebar: "#14213d", Header: "#1d2d50", Page: "#f7f7f4", Accent: "#c9a227", SidebarText: "#f4f1e8", HeaderText: "#ffffff"},
	{ID: "charcoal_copper", Label: "Charcoal and copper", Sidebar: "#2b2b2b", Header: "#383838", Page: "#f6f4f2", Accent: "#b87333", SidebarText: "#f2ece6", HeaderText: "#ffffff"},
	{ID: "forest_cream", Label: "Forest and cream", Sidebar: "#1f3d2b", Header: "#2c5039", Page: "#fbf8ef", Accent: "#a3c585", SidebarText: "#f8f4e3", HeaderText: "#ffffff"},
	{ID: "maximum_contrast", Label: "Maximum contrast", Sidebar: "#000000", Header: "#000000", Page: "#ffffff", Accent: "#ffd200", SidebarText: "#ffffff", HeaderText: "#ffffff"},
}

func presetByID(id string) (Preset, bool) {
	for _, p := range Presets {
		if p.ID == id || p.Label == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Fonts are the PRD 8.17 fonts. Serif marks the serif families.
var Fonts = []struct {
	Name  string
	Serif bool
}{
	{"Lora", true}, {"Merriweather", true}, {"Libre Baskerville", true}, {"Source Serif", true},
	{"Inter", false}, {"IBM Plex Sans", false}, {"Source Sans", false}, {"Atkinson Hyperlegible", false},
}

// Appearance is the contract Appearance object over the appearance.* settings.
type Appearance struct {
	Preset       string `json:"preset"`
	StaffSidebar string `json:"staff_sidebar"`
	StaffHeader  string `json:"staff_header"`
	StaffPage    string `json:"staff_page"`
	StaffAccent  string `json:"staff_accent"`
	ClientHeader string `json:"client_header"`
	HeadingFont  string `json:"heading_font"`
	BodyFont     string `json:"body_font"`
	TextSize     string `json:"text_size"`
	Mode         string `json:"mode"`
}

type colorsJSON struct {
	StaffSidebar string `json:"staff_sidebar,omitempty"`
	StaffHeader  string `json:"staff_header,omitempty"`
	StaffPage    string `json:"staff_page,omitempty"`
	StaffAccent  string `json:"staff_accent,omitempty"`
	ClientHeader string `json:"client_header,omitempty"`
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// GetAppearance reads the firm appearance. Every staff role may read it; it holds no
// firm records.
func (s *Service) GetAppearance(ctx context.Context, p domain.Principal) (Appearance, error) {
	if err := staff(p); err != nil {
		return Appearance{}, err
	}
	return s.appearance(ctx), nil
}

func (s *Service) appearance(ctx context.Context) Appearance {
	pr, ok := presetByID(s.setting(ctx, "appearance.preset"))
	custom := !ok
	if !ok {
		pr = Presets[0]
	}
	var c colorsJSON
	_ = json.Unmarshal([]byte(s.setting(ctx, "appearance.colors")), &c)
	a := Appearance{Preset: pr.ID, StaffSidebar: pr.Sidebar, StaffHeader: pr.Header, StaffPage: pr.Page, StaffAccent: pr.Accent,
		ClientHeader: pr.Header, HeadingFont: s.setting(ctx, "appearance.heading_font"), BodyFont: s.setting(ctx, "appearance.body_font"),
		TextSize: s.setting(ctx, "appearance.text_size"), Mode: s.setting(ctx, "appearance.mode")}
	if custom {
		pick := func(dst *string, v string) {
			if hexColor.MatchString(v) {
				*dst = v
			}
		}
		pick(&a.StaffSidebar, c.StaffSidebar)
		pick(&a.StaffHeader, c.StaffHeader)
		pick(&a.StaffPage, c.StaffPage)
		pick(&a.StaffAccent, c.StaffAccent)
	}
	if hexColor.MatchString(c.ClientHeader) {
		a.ClientHeader = c.ClientHeader
	}
	if a.HeadingFont == "" {
		a.HeadingFont = "Source Serif"
	}
	if a.BodyFont == "" {
		a.BodyFont = "Source Sans"
	}
	switch a.TextSize {
	case "sm", "md", "lg":
	default:
		a.TextSize = "md"
	}
	if a.Mode == "" {
		a.Mode = "device"
	}
	return a
}

func validFont(f string) bool {
	for _, x := range Fonts {
		if x.Name == f {
			return true
		}
	}
	return false
}

// SetAppearance writes the appearance settings through the registry: admin only, each
// changed key audited with its old and new values, visible on the next page load.
// preset "custom" (or empty) keeps the given colors instead of a preset's.
func (s *Service) SetAppearance(ctx context.Context, p domain.Principal, a Appearance) (Appearance, error) {
	if err := admin(p); err != nil {
		return Appearance{}, err
	}
	preset := ""
	if a.Preset != "" && a.Preset != "custom" {
		pr, ok := presetByID(a.Preset)
		if !ok {
			return Appearance{}, domain.Invalid("preset", "unknown preset")
		}
		preset = pr.Label
	}
	c := colorsJSON{StaffSidebar: a.StaffSidebar, StaffHeader: a.StaffHeader, StaffPage: a.StaffPage, StaffAccent: a.StaffAccent, ClientHeader: a.ClientHeader}
	for field, v := range map[string]string{"staff_sidebar": c.StaffSidebar, "staff_header": c.StaffHeader, "staff_page": c.StaffPage, "staff_accent": c.StaffAccent, "client_header": c.ClientHeader} {
		if v != "" && !hexColor.MatchString(v) {
			return Appearance{}, domain.Invalid(field, "colors are #rrggbb")
		}
	}
	for field, f := range map[string]string{"heading_font": a.HeadingFont, "body_font": a.BodyFont} {
		if f != "" && !validFont(f) {
			return Appearance{}, domain.Invalid(field, "unknown font")
		}
	}
	switch a.TextSize {
	case "", "sm", "md", "lg":
	default:
		return Appearance{}, domain.Invalid("text_size", "text size is sm, md or lg")
	}
	switch a.Mode {
	case "", "light", "dark", "device":
	default:
		return Appearance{}, domain.Invalid("mode", "mode is light, dark or device")
	}
	colors, _ := json.Marshal(c)
	want := map[string]string{
		"appearance.preset": preset, "appearance.colors": string(colors), "appearance.heading_font": a.HeadingFont,
		"appearance.body_font": a.BodyFont, "appearance.text_size": a.TextSize,
	}
	if a.Mode != "" {
		want["appearance.mode"] = a.Mode
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s.setting(ctx, k) == want[k] {
			continue
		}
		if _, err := s.d.Settings.Set(ctx, k, want[k], p); err != nil {
			return Appearance{}, domain.Invalid(k, err.Error())
		}
	}
	return s.appearance(ctx), nil
}

// Theme is what one page load renders: the firm appearance with the viewer's own mode
// and text size on top.
type Theme struct {
	Appearance
	EffectiveMode string
	EffectiveSize string
}

// ThemeFor is the viewer's theme. It reads the settings on every call, so an
// appearance change shows on the next page load with no restart.
func (s *Service) ThemeFor(ctx context.Context, p domain.Principal) Theme {
	a := s.appearance(ctx)
	t := Theme{Appearance: a, EffectiveMode: a.Mode, EffectiveSize: a.TextSize}
	if p.UserID.IsZero() {
		return t
	}
	if m, err := s.MySettings(ctx, p); err == nil {
		if m.Mode != "firm" {
			t.EffectiveMode = m.Mode
		}
		if m.TextSize != "firm" {
			t.EffectiveSize = m.TextSize
		}
	}
	return t
}

// --- Problem reports (PRD 8.9) ---

// ReportProblem saves a report and emails the recipients (mail.problem_recipients,
// default the admins). The report is kept even when the mail fails; the error is
// returned beside it so the form can say so.
func (s *Service) ReportProblem(ctx context.Context, p domain.Principal, page, message string) (ProblemReport, error, error) {
	if err := staff(p); err != nil {
		return ProblemReport{}, nil, err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return ProblemReport{}, nil, domain.Invalid("message", "describe the problem")
	}
	if utf8.RuneCountInString(message) > fixed.ProblemMessageMax {
		return ProblemReport{}, nil, domain.Invalid("message", fmt.Sprintf("at most %d characters", fixed.ProblemMessageMax))
	}
	page, _ = truncate(page, fixed.ProblemPageMax)
	id, err := s.d.IDs()
	if err != nil {
		return ProblemReport{}, nil, err
	}
	now := s.d.Now().UTC()
	r := ProblemReport{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: p.UserID, UserID: p.UserID, Page: page, Message: message}
	if err := s.d.Store.InsertProblem(ctx, r); err != nil {
		return ProblemReport{}, nil, err
	}
	return r, s.mailProblem(ctx, r), nil
}

// ProblemRecipients resolves mail.problem_recipients: "admins" is every active admin;
// other entries are addresses.
func (s *Service) ProblemRecipients(ctx context.Context) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s.setting(ctx, "mail.problem_recipients"), ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case part == "admins":
			if s.d.Staff == nil {
				continue
			}
			users, err := s.d.Staff(ctx)
			if err != nil {
				return nil, err
			}
			for _, u := range users {
				roles, _ := domain.ParseRoles(u.Roles)
				for _, r := range roles {
					if r == domain.RoleAdmin && u.Active && !seen[u.Email] {
						seen[u.Email] = true
						out = append(out, u.Email)
					}
				}
			}
		case strings.Contains(part, "@") && !seen[part]:
			seen[part] = true
			out = append(out, part)
		}
	}
	return out, nil
}

func (s *Service) mailProblem(ctx context.Context, r ProblemReport) error {
	if s.d.Mailer == nil {
		return fmt.Errorf("system mail is not wired")
	}
	to, err := s.ProblemRecipients(ctx)
	if err != nil {
		return err
	}
	who := string(r.UserID)
	if s.d.Users != nil {
		if u, err := s.d.Users.Get(ctx, r.UserID); err == nil {
			who = u.Name + " <" + u.Email + ">"
		}
	}
	body := "A problem was reported in Togavsu.\n\nFrom: " + who + "\nPage: " + r.Page + "\nAt: " + r.CreatedAt.Format(time.RFC3339) + "\n\n" + r.Message + "\n"
	var firstErr error
	for _, addr := range to {
		// ponytail: ceiling: a failed send is not retried; the report stays on the
		// System tab. Upgrade path: the shared system mail outbox. Trigger: the
		// notifications/mail integration lands an outbox outside trust.
		if err := s.d.Mailer.Send(ctx, addr, "Problem report: "+firstLine(r.Message), body); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if short, cut := truncate(s, fixed.MailSubjectChars); cut {
		return short + "…"
	}
	return s
}

// Problems lists problem reports for the admin's System tab.
func (s *Service) Problems(ctx context.Context, p domain.Principal) ([]ProblemReport, error) {
	if err := admin(p); err != nil {
		return nil, err
	}
	return s.d.Store.Problems(ctx)
}

// --- Notifications, search, timers: thin pass-throughs to the owning services ---

// UnreadCount is the bell count; zero when no notifier is wired.
func (s *Service) UnreadCount(ctx context.Context, p domain.Principal) (n int) {
	if s.d.Notifier != nil {
		if c, err := s.d.Notifier.UnreadCount(ctx, p); err == nil {
			n = c
		}
	}
	return n
}

// RecentNotifications is the bell panel.
func (s *Service) RecentNotifications(ctx context.Context, p domain.Principal) ([]Notification, error) {
	if s.d.Notifier == nil {
		return []Notification{}, nil
	}
	return s.d.Notifier.Recent(ctx, p, fixed.RecentNotifications)
}

// OpenNotification marks one notification read and returns its link.
func (s *Service) OpenNotification(ctx context.Context, p domain.Principal, id domain.ID) (string, error) {
	if s.d.Notifier == nil {
		return "", domain.NotFound("notification")
	}
	n, err := s.d.Notifier.MarkRead(ctx, p, id)
	if err != nil {
		return "", err
	}
	return safeLink(n.Link), nil
}

// MarkAllRead clears the bell count.
func (s *Service) MarkAllRead(ctx context.Context, p domain.Principal) (int, error) {
	if s.d.Notifier == nil {
		return 0, nil
	}
	return s.d.Notifier.MarkAllRead(ctx, p)
}

// safeLink keeps a redirect on this site.
func safeLink(l string) string {
	if !strings.HasPrefix(l, "/") || strings.HasPrefix(l, "//") || strings.HasPrefix(l, "/\\") {
		return "/"
	}
	return l
}

// Search scopes in picker order (PRD 8.10).
var Scopes = []struct{ ID, Label string }{{"all", "All"}, {"matters", "Matters"}, {"people", "People"}, {"invoices", "Invoices"}, {"files", "Files"}}

// SearchView is one header search answer.
type SearchView struct {
	Query     string
	Scope     string
	Groups    []SearchGroup
	TooShort  bool
	MinChars  int
	NoMatches string
}

// Search asks the practice search service as p.
func (s *Service) Search(ctx context.Context, p domain.Principal, q, scope string) (SearchView, error) {
	if err := staff(p); err != nil {
		return SearchView{}, err
	}
	if scope == "" {
		scope = "all"
	}
	known := false
	label := scope
	for _, sc := range Scopes {
		if sc.ID == scope {
			known, label = true, sc.Label
		}
	}
	if !known {
		return SearchView{}, domain.Invalid("scope", "unknown scope")
	}
	q = strings.TrimSpace(q)
	v := SearchView{Query: q, Scope: scope, MinChars: s.intSetting(ctx, "search.min_chars")}
	if utf8.RuneCountInString(q) < v.MinChars {
		v.TooShort = true
		return v, nil
	}
	if s.d.Search == nil {
		return SearchView{}, domain.NotFound("search")
	}
	groups, err := s.d.Search.Search(ctx, p, q, scope)
	if err != nil {
		return SearchView{}, err
	}
	any := false
	for _, g := range groups {
		if len(g.Hits) > 0 {
			any = true
		}
	}
	v.Groups = groups
	if !any {
		v.NoMatches = "No matches for " + q + " in " + label + "."
	}
	return v, nil
}

// SearchDelayMS is search.delay in milliseconds for the header box.
func (s *Service) SearchDelayMS(ctx context.Context) int {
	return int(s.settingDuration(ctx, "search.delay").Milliseconds())
}

// MyTimers is the header timers panel.
func (s *Service) MyTimers(ctx context.Context, p domain.Principal) ([]TimerRow, error) {
	if s.d.Timers == nil || !roleReads(p, domain.ResTime) {
		return []TimerRow{}, nil
	}
	return s.d.Timers.Timers(ctx, p)
}

// Seats is the System tab's seat count (PRD 3: the admin dashboard shows the active seat
// count so a host can bill from it). Staff is the seat count: active users holding a
// staff-app role (admin, attorney, staff, accountant). A client-portal user is not a
// seat; Clients counts active portal users separately so a host whose terms differ
// still sees the number. A user with a staff role and the client role is one seat.
type Seats struct {
	Staff   int
	Clients int
}

// SeatCount is the active seats the admin dashboard shows (PRD 3). Admin only.
func (s *Service) SeatCount(ctx context.Context, p domain.Principal) (Seats, error) {
	if err := admin(p); err != nil {
		return Seats{}, err
	}
	if s.d.Staff == nil {
		return Seats{}, nil
	}
	users, err := s.d.Staff(ctx)
	if err != nil {
		return Seats{}, err
	}
	var n Seats
	for _, u := range users {
		if !u.Active {
			continue
		}
		roles, _ := domain.ParseRoles(u.Roles)
		staff, client := false, false
		for _, r := range roles {
			switch r {
			case domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant:
				staff = true
			case domain.RoleClient:
				client = true
			}
		}
		switch {
		case staff:
			n.Staff++
		case client:
			n.Clients++
		}
	}
	return n, nil
}

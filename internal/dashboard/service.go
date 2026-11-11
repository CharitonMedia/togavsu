package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Users reads one user. The identity user repository satisfies it.
type Users interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
}

// Mailer sends one system mail. trust.SMTP satisfies it.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Notification is one row in the header's notification list.
type Notification struct {
	ID     domain.ID  `json:"id"`
	Kind   string     `json:"kind"`
	Title  string     `json:"title"`
	Link   string     `json:"link"`
	At     time.Time  `json:"created_at"`
	ReadAt *time.Time `json:"read_at"`
}

// Notifier is the notifications record set (PRD 8.9). Each call is the viewer's own
// notifications only; the implementation enforces that.
type Notifier interface {
	UnreadCount(ctx context.Context, p domain.Principal) (int, error)
	Recent(ctx context.Context, p domain.Principal, limit int) ([]Notification, error)
	MarkRead(ctx context.Context, p domain.Principal, id domain.ID) (Notification, error)
	MarkAllRead(ctx context.Context, p domain.Principal) (int, error)
}

// SearchHit and SearchGroup are one scope's search answer (PRD 8.10).
type SearchHit struct {
	Title      string `json:"title"`
	Subtitle   string `json:"subtitle"`
	RecordType string `json:"record_type"`
	TargetPath string `json:"target_path"`
}

// SearchGroup is the hits of one scope.
type SearchGroup struct {
	Scope string      `json:"scope"`
	Hits  []SearchHit `json:"hits"`
}

// Searcher is the practice search service: roles, walls and attorney-only fields are its
// job. The header box only presents its answer.
type Searcher interface {
	Search(ctx context.Context, p domain.Principal, q, scope string) ([]SearchGroup, error)
}

// TimerRow is one timer in the header's timers panel.
type TimerRow struct {
	ID       domain.ID `json:"id"`
	MatterID domain.ID `json:"matter_id"`
	Matter   string    `json:"matter"`
	Status   string    `json:"status"`
	Minutes  int64     `json:"minutes"`
}

// Timers is the billing timer service, called as the viewer.
type Timers interface {
	Timers(ctx context.Context, p domain.Principal) ([]TimerRow, error)
}

// Deps wires the service. Store and Settings are required.
type Deps struct {
	Store    Store
	Settings *settings.Registry
	Users    Users
	// Staff lists active staff users (seat count, problem report recipients).
	Staff    func(ctx context.Context) ([]domain.User, error)
	Catalog  *Catalog
	Notifier Notifier
	Search   Searcher
	Timers   Timers
	Mailer   Mailer
	// Badges are the sidebar counts by entry id, each counting only records p can read.
	Badges map[string]func(ctx context.Context, p domain.Principal) (int, error)
	Now    func() time.Time
	IDs    func() (domain.ID, error)
}

// Service is the dashboard application service.
type Service struct {
	d Deps
	// lmu serializes one process's layout writes; the slot constraint guards the rest.
	lmu  sync.Mutex
	navs *navRegistry
}

// New checks dependencies.
func New(d Deps) (*Service, error) {
	if d.Store == nil {
		return nil, errors.New("dashboard: store is required")
	}
	if d.Settings == nil {
		return nil, errors.New("dashboard: settings registry is required")
	}
	if d.Catalog == nil {
		d.Catalog = NewCatalog(nil)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDs == nil {
		d.IDs = domain.NewID
	}
	return &Service{d: d, navs: newNavRegistry()}, nil
}

// Catalog is the widget catalog the service loads from.
func (s *Service) Catalog() *Catalog { return s.d.Catalog }

func (s *Service) setting(ctx context.Context, key string) string {
	v, err := s.d.Settings.Get(ctx, key)
	if err != nil {
		return ""
	}
	return v
}

// intSetting reads an integer setting; an unreadable value falls back to the
// registry's default for that key.
func (s *Service) intSetting(ctx context.Context, key string) int {
	n, err := strconv.Atoi(s.setting(ctx, key))
	if err != nil {
		return registryInt(key)
	}
	return n
}

// staff is every role but client. The home screen and shell are staff screens.
func staff(p domain.Principal) error {
	if p.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	for _, r := range p.Roles {
		if r != domain.RoleClient {
			return nil
		}
	}
	return domain.Forbidden(domain.ReasonClientScope)
}

func admin(p domain.Principal) error {
	if err := staff(p); err != nil {
		return err
	}
	if !p.HasRole(domain.RoleAdmin) {
		return domain.Forbidden(domain.ReasonRole)
	}
	return nil
}

// PrimaryRole is the role whose default layout a user starts from: the first of admin,
// attorney, staff and accountant the user holds.
func PrimaryRole(p domain.Principal) domain.Role {
	for _, r := range allStaff {
		if p.HasRole(r) {
			return r
		}
	}
	return domain.RoleClient
}

// --- Layouts (PRD 8.6) ---

func (s *Service) validateWidgets(viewer domain.Principal, ws []LayoutWidget) error {
	if len(ws) > fixed.MaxWidgetsPerLayout {
		return domain.Invalid("widgets", fmt.Sprintf("a layout holds at most %d widgets", fixed.MaxWidgetsPerLayout))
	}
	for i, w := range ws {
		field := fmt.Sprintf("widgets[%d]", i)
		if !s.d.Catalog.Visible(viewer, w.WidgetID) {
			return domain.Invalid(field+".widget_id", "unknown widget or not available to this role: "+w.WidgetID)
		}
		if w.X < fixed.GridOrigin || w.W < fixed.MinWidgetSize || w.X+w.W > GridColumns {
			return domain.Invalid(field, "widget is outside the 12-column grid")
		}
		if w.Y < fixed.GridOrigin || w.H < fixed.MinWidgetSize || w.H > fixed.MaxWidgetHeight {
			return domain.Invalid(field, "widget height or row is out of range")
		}
	}
	return nil
}

// visible drops widgets p can no longer see (role change, module removed); the rest
// keep their places.
func (s *Service) visible(p domain.Principal, ws []LayoutWidget) []LayoutWidget {
	out := []LayoutWidget{}
	for _, w := range ws {
		if s.d.Catalog.Visible(p, w.WidgetID) {
			out = append(out, w)
		}
	}
	return out
}

// pack places widget ids left to right in rows using each widget's default size.
func (s *Service) pack(ids []string) []LayoutWidget {
	type cursor struct{ x, y, rowH int }
	var out []LayoutWidget
	var c cursor
	for _, id := range ids {
		d, ok := s.d.Catalog.Def(id)
		if !ok {
			continue
		}
		if c.x+d.W > GridColumns {
			c = cursor{y: c.y + c.rowH} // next row
		}
		out = append(out, LayoutWidget{WidgetID: id, X: c.x, Y: c.y, W: d.W, H: d.H})
		c.x += d.W
		c.rowH = max(c.rowH, d.H)
	}
	return out
}

// builtinRoleLayout is the layout a role starts with before the admin sets one.
func (s *Service) builtinRoleLayout(role domain.Role) []LayoutWidget {
	if ids, ok := catalog.RoleLayouts[string(role)]; ok {
		return s.pack(ids)
	}
	return []LayoutWidget{}
}

// RoleDefault returns the admin's layout for role, or the built-in one.
func (s *Service) RoleDefault(ctx context.Context, role domain.Role) (RoleLayout, error) {
	l, ok, err := s.d.Store.RoleLayout(ctx, role)
	if err != nil {
		return RoleLayout{}, err
	}
	if !ok {
		return RoleLayout{Role: role, Widgets: s.builtinRoleLayout(role)}, nil
	}
	return l, nil
}

// GetRoleLayout is the admin read of a role's default layout.
func (s *Service) GetRoleLayout(ctx context.Context, p domain.Principal, role string) (RoleLayout, error) {
	if err := admin(p); err != nil {
		return RoleLayout{}, err
	}
	r, err := domain.ParseRole(role)
	if err != nil {
		return RoleLayout{}, domain.Invalid("role", "unknown role")
	}
	return s.RoleDefault(ctx, r)
}

// SetRoleLayout is the admin write of a role's default layout.
func (s *Service) SetRoleLayout(ctx context.Context, p domain.Principal, role string, ws []LayoutWidget) (RoleLayout, error) {
	if err := admin(p); err != nil {
		return RoleLayout{}, err
	}
	r, err := domain.ParseRole(role)
	if err != nil {
		return RoleLayout{}, domain.Invalid("role", "unknown role")
	}
	if err := s.validateWidgets(domain.Principal{UserID: p.UserID, Roles: []domain.Role{r}}, ws); err != nil {
		return RoleLayout{}, err
	}
	l := RoleLayout{Role: r, Widgets: cloneWidgets(ws), UpdatedAt: s.d.Now().UTC(), UpdatedBy: p.UserID}
	if l.Widgets == nil {
		l.Widgets = []LayoutWidget{}
	}
	if err := s.d.Store.PutRoleLayout(ctx, l); err != nil {
		return RoleLayout{}, err
	}
	return l, nil
}

// Working is the layout the home screen shows: the user's own, else their role default.
// Widgets the user can no longer see drop out; the rest stay where they were.
func (s *Service) Working(ctx context.Context, p domain.Principal) ([]LayoutWidget, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	ws, ok, err := s.d.Store.Working(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		rl, err := s.RoleDefault(ctx, PrimaryRole(p))
		if err != nil {
			return nil, err
		}
		ws = rl.Widgets
	}
	return s.visible(p, ws), nil
}

// PutWorking saves the user's current layout (move, resize, add, remove).
func (s *Service) PutWorking(ctx context.Context, p domain.Principal, ws []LayoutWidget) ([]LayoutWidget, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	if err := s.validateWidgets(p, ws); err != nil {
		return nil, err
	}
	if ws == nil {
		ws = []LayoutWidget{}
	}
	if err := s.d.Store.PutWorking(ctx, p.UserID, ws, s.d.Now().UTC()); err != nil {
		return nil, err
	}
	return cloneWidgets(ws), nil
}

// Layouts lists the user's saved layouts, default first is not implied: slot order.
func (s *Service) Layouts(ctx context.Context, p domain.Principal) ([]Layout, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	ls, err := s.d.Store.Layouts(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	for i := range ls {
		ls[i].Widgets = s.visible(p, ls[i].Widgets)
	}
	return ls, nil
}

// Layout returns one of the user's saved layouts.
func (s *Service) Layout(ctx context.Context, p domain.Principal, id domain.ID) (Layout, error) {
	ls, err := s.Layouts(ctx, p)
	if err != nil {
		return Layout{}, err
	}
	for _, l := range ls {
		if l.ID == id {
			return l, nil
		}
	}
	return Layout{}, domain.NotFound("layout")
}

// raw returns a saved layout exactly as stored, for apply.
func (s *Service) raw(ctx context.Context, p domain.Principal, id domain.ID) (Layout, error) {
	ls, err := s.d.Store.Layouts(ctx, p.UserID)
	if err != nil {
		return Layout{}, err
	}
	for _, l := range ls {
		if l.ID == id {
			return l, nil
		}
	}
	return Layout{}, domain.NotFound("layout")
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", domain.Invalid("name", "a saved layout needs a name")
	}
	if utf8.RuneCountInString(name) > fixed.LayoutNameMax {
		return "", domain.Invalid("name", fmt.Sprintf("a layout name is at most %d characters", fixed.LayoutNameMax))
	}
	return name, nil
}

// LayoutLimit is the per-user cap, home.max_saved_layouts (default 10).
func (s *Service) LayoutLimit(ctx context.Context) int {
	return s.intSetting(ctx, "home.max_saved_layouts")
}

// SaveLayout saves widgets under a new name. Past the cap it is refused with
// layout_limit and nothing is written.
func (s *Service) SaveLayout(ctx context.Context, p domain.Principal, name string, ws []LayoutWidget) (Layout, error) {
	if err := staff(p); err != nil {
		return Layout{}, err
	}
	name, err := cleanName(name)
	if err != nil {
		return Layout{}, err
	}
	if err := s.validateWidgets(p, ws); err != nil {
		return Layout{}, err
	}
	if ws == nil {
		ws = []LayoutWidget{}
	}
	s.lmu.Lock()
	defer s.lmu.Unlock()
	limit := s.LayoutLimit(ctx)
	id, err := s.d.IDs()
	if err != nil {
		return Layout{}, err
	}
	now := s.d.Now().UTC()
	for attempt := 0; attempt < fixed.SaveAttempts; attempt++ {
		existing, err := s.d.Store.Layouts(ctx, p.UserID)
		if err != nil {
			return Layout{}, err
		}
		used := map[int]bool{}
		for _, l := range existing {
			if strings.EqualFold(l.Name, name) {
				return Layout{}, domain.Conflict("a saved layout already has that name")
			}
			used[l.Slot] = true
		}
		slot, free := freeSlot(used, limit)
		if !free || len(existing) >= limit {
			return Layout{}, &domain.APIError{Code: CodeLayoutLimit, Message: fmt.Sprintf("you can save up to %d layouts; delete or overwrite one first", limit)}
		}
		l := Layout{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: p.UserID, UserID: p.UserID, Name: name, Slot: slot, Widgets: cloneWidgets(ws)}
		err = s.d.Store.InsertLayout(ctx, l)
		if errors.Is(err, ErrSlotTaken) {
			continue
		}
		if err != nil {
			return Layout{}, err
		}
		return l, nil
	}
	return Layout{}, domain.Conflict("another save took the last free layout place; try again")
}

// freeSlot is the lowest slot below limit no saved layout holds.
func freeSlot(used map[int]bool, limit int) (int, bool) {
	for i := 0; i < limit; i++ {
		if !used[i] {
			return i, true
		}
	}
	return limit, false
}

// UpdateLayout renames and/or overwrites a saved layout.
func (s *Service) UpdateLayout(ctx context.Context, p domain.Principal, id domain.ID, name *string, ws *[]LayoutWidget) (Layout, error) {
	if err := staff(p); err != nil {
		return Layout{}, err
	}
	l, err := s.raw(ctx, p, id)
	if err != nil {
		return Layout{}, err
	}
	if name != nil {
		n, err := cleanName(*name)
		if err != nil {
			return Layout{}, err
		}
		others, err := s.d.Store.Layouts(ctx, p.UserID)
		if err != nil {
			return Layout{}, err
		}
		for _, o := range others {
			if o.ID != id && strings.EqualFold(o.Name, n) {
				return Layout{}, domain.Conflict("a saved layout already has that name")
			}
		}
		l.Name = n
	}
	if ws != nil {
		if err := s.validateWidgets(p, *ws); err != nil {
			return Layout{}, err
		}
		l.Widgets = cloneWidgets(*ws)
		if l.Widgets == nil {
			l.Widgets = []LayoutWidget{}
		}
	}
	l.UpdatedAt = s.d.Now().UTC()
	if err := s.d.Store.UpdateLayout(ctx, l); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Layout{}, domain.NotFound("layout")
		}
		return Layout{}, err
	}
	return l, nil
}

// DeleteLayout removes a saved layout.
func (s *Service) DeleteLayout(ctx context.Context, p domain.Principal, id domain.ID) error {
	if err := staff(p); err != nil {
		return err
	}
	if err := s.d.Store.DeleteLayout(ctx, p.UserID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.NotFound("layout")
		}
		return err
	}
	return nil
}

// SetDefaultLayout marks one saved layout as the user's default.
func (s *Service) SetDefaultLayout(ctx context.Context, p domain.Principal, id domain.ID) (Layout, error) {
	if err := staff(p); err != nil {
		return Layout{}, err
	}
	if err := s.d.Store.SetDefault(ctx, p.UserID, id, s.d.Now().UTC()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Layout{}, domain.NotFound("layout")
		}
		return Layout{}, err
	}
	return s.Layout(ctx, p, id)
}

// RoleOptionID is the reset option id that stands for the role default layout.
const RoleOptionID domain.ID = "00000000-0000-0000-0000-000000000000"

// ApplyLayout copies a saved layout, or the role default for RoleOptionID, into the
// working layout exactly as it was saved.
func (s *Service) ApplyLayout(ctx context.Context, p domain.Principal, id domain.ID) (Layout, error) {
	if err := staff(p); err != nil {
		return Layout{}, err
	}
	var l Layout
	if id == RoleOptionID {
		rl, err := s.RoleDefault(ctx, PrimaryRole(p))
		if err != nil {
			return Layout{}, err
		}
		l = s.roleOption(p, rl)
	} else {
		var err error
		if l, err = s.raw(ctx, p, id); err != nil {
			return Layout{}, err
		}
	}
	if err := s.d.Store.PutWorking(ctx, p.UserID, l.Widgets, s.d.Now().UTC()); err != nil {
		return Layout{}, err
	}
	l.Widgets = s.visible(p, l.Widgets)
	return l, nil
}

func (s *Service) roleOption(p domain.Principal, rl RoleLayout) Layout {
	return Layout{ID: RoleOptionID, Name: "Role default (" + capitalize(string(rl.Role)) + ")", CreatedBy: "", UserID: p.UserID, Widgets: cloneWidgets(rl.Widgets), CreatedAt: rl.UpdatedAt, UpdatedAt: rl.UpdatedAt}
}

// ResetOptions is the Reset list: the user's default saved layout first, then their
// other saved layouts, then the role default. With no saved layouts it is the role
// default alone, which Reset applies straight away.
func (s *Service) ResetOptions(ctx context.Context, p domain.Principal) ([]Layout, error) {
	ls, err := s.Layouts(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []Layout
	for _, l := range ls {
		if l.Default {
			out = append(out, l)
		}
	}
	for _, l := range ls {
		if !l.Default {
			out = append(out, l)
		}
	}
	rl, err := s.RoleDefault(ctx, PrimaryRole(p))
	if err != nil {
		return nil, err
	}
	opt := s.roleOption(p, rl)
	opt.Widgets = s.visible(p, opt.Widgets)
	return append(out, opt), nil
}

// LoadWidget runs one widget as p. Filters come from the layout; the click-through
// target carries them.
func (s *Service) LoadWidget(ctx context.Context, p domain.Principal, id string, filters map[string]string) (Widget, error) {
	if err := staff(p); err != nil {
		return Widget{}, err
	}
	return s.d.Catalog.Load(ctx, p, id, filters)
}

// Widgets lists the widget picker for p.
func (s *Service) Widgets(ctx context.Context, p domain.Principal) ([]WidgetDef, error) {
	if err := staff(p); err != nil {
		return nil, err
	}
	return s.d.Catalog.For(p), nil
}

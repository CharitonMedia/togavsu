// Package help implements PRD 8.11 and 8.18: the user guide (one Markdown source,
// served as HTML and as a PDF with its own contents), guide topics opened from every
// screen's ? icon, each role's getting-started tutorial, the firm's help sections and
// items, module guide pages under each module's own heading, and /llms.txt.
//
// The guide and the help center are staff only: a client user gets 404. /llms.txt is
// public and is built only from the product version, two llms.* settings and fixed
// text, never from firm records.
package help

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ErrNotFound is a missing row in a Store.
var ErrNotFound = errors.New("help: not found")

// Section is a firm help section (help_sections).
type Section struct {
	ID        domain.ID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy domain.ID `json:"created_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Title     string    `json:"title"`
	SortOrder int       `json:"sort_order"`
}

// Item is one help item: text, a video or an image (help_items).
type Item struct {
	ID        domain.ID  `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy domain.ID  `json:"created_by"`
	UpdatedAt time.Time  `json:"updated_at"`
	SectionID domain.ID  `json:"section_id"`
	Title     string     `json:"title"`
	Kind      string     `json:"kind"`
	Body      string     `json:"body"`
	FileID    *domain.ID `json:"file_id"`
	SortOrder int        `json:"sort_order"`
}

// Page is a module guide page.
type Page struct {
	ID        string    `json:"id"`
	ModuleID  string    `json:"module_id"`
	Title     string    `json:"title"`
	Markdown  string    `json:"markdown"`
	Keywords  []string  `json:"keywords"`
	Covers    []string  `json:"covers"`
	SortOrder int       `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// Store persists help records. Implementations: Memory and PG.
type Store interface {
	Sections(ctx context.Context) ([]Section, error)
	PutSection(ctx context.Context, s Section) error
	DeleteSection(ctx context.Context, id domain.ID) error
	Items(ctx context.Context, section domain.ID) ([]Item, error)
	PutItem(ctx context.Context, it Item) error
	DeleteItem(ctx context.Context, id domain.ID) error
	Pages(ctx context.Context, module string) ([]Page, error)
	// ReplacePages swaps every page of module in one step.
	ReplacePages(ctx context.Context, module string, pages []Page, at time.Time) error
	PutPage(ctx context.Context, p Page) error
	DeletePage(ctx context.Context, module, id string) error
	SetModuleEnabled(ctx context.Context, module string, enabled bool, at time.Time) error
	// Modules returns every module with registered pages and whether it is enabled.
	Modules(ctx context.Context) (map[string]bool, error)
}

// Settings reads one setting. *settings.Registry satisfies it.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
}

// Deps wires the service.
type Deps struct {
	Store    Store
	Settings Settings
	Guide    *Guide
	Version  string
	Now      func() time.Time
	IDs      func() (domain.ID, error)
}

// Service is the help application service.
type Service struct {
	d   Deps
	mu  sync.Mutex
	pdf []byte
}

// New checks dependencies; a nil Guide loads the embedded one.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.Settings == nil {
		return nil, errors.New("help: store and settings are required")
	}
	if d.Guide == nil {
		g, err := LoadGuide()
		if err != nil {
			return nil, err
		}
		d.Guide = g
	}
	if d.Version == "" {
		d.Version = "dev"
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.IDs == nil {
		d.IDs = domain.NewID
	}
	return &Service{d: d}, nil
}

// Guide is the parsed user guide.
func (s *Service) Guide() *Guide { return s.d.Guide }

// Version is the running core version the guide belongs to.
func (s *Service) Version() string { return s.d.Version }

// staffOnly is the help center's gate: any staff role reads; a client user, who must
// not learn the system's layout, gets not_found rather than forbidden.
func staffOnly(p domain.Principal) error {
	if p.UserID.IsZero() {
		return domain.Unauthenticated("sign in is required")
	}
	for _, r := range p.Roles {
		if r != domain.RoleClient {
			return nil
		}
	}
	return domain.NotFound("page")
}

func adminOnly(p domain.Principal) error {
	if err := staffOnly(p); err != nil {
		return err
	}
	if !p.HasRole(domain.RoleAdmin) {
		return domain.Forbidden(domain.ReasonRole)
	}
	return nil
}

// Allow reports whether p may read the guide and help center.
func (s *Service) Allow(p domain.Principal) error { return staffOnly(p) }

// GuidePDF returns the guide PDF, built once per process from the embedded source.
func (s *Service) GuidePDF(p domain.Principal) ([]byte, error) {
	if err := staffOnly(p); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pdf == nil {
		s.pdf = s.d.Guide.GuidePDF(s.d.Version)
	}
	return s.pdf, nil
}

// --- Sections and items ---

func cleanTitle(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", domain.Invalid("title", "a title is required")
	}
	if utf8.RuneCountInString(t) > 200 {
		return "", domain.Invalid("title", "at most 200 characters")
	}
	return t, nil
}

// Sections lists the firm's help sections in sort order.
func (s *Service) Sections(ctx context.Context, p domain.Principal) ([]Section, error) {
	if err := staffOnly(p); err != nil {
		return nil, err
	}
	out, err := s.d.Store.Sections(ctx)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, err
}

// Section returns one section.
func (s *Service) Section(ctx context.Context, p domain.Principal, id domain.ID) (Section, error) {
	all, err := s.Sections(ctx, p)
	if err != nil {
		return Section{}, err
	}
	for _, sec := range all {
		if sec.ID == id {
			return sec, nil
		}
	}
	return Section{}, domain.NotFound("help section")
}

// SectionInput is a section write; nil fields keep their value on update.
type SectionInput struct {
	Title     *string `json:"title"`
	SortOrder *int    `json:"sort_order"`
}

// CreateSection adds a section (admin).
func (s *Service) CreateSection(ctx context.Context, p domain.Principal, in SectionInput) (Section, error) {
	if err := adminOnly(p); err != nil {
		return Section{}, err
	}
	if in.Title == nil {
		return Section{}, domain.Invalid("title", "a title is required")
	}
	title, err := cleanTitle(*in.Title)
	if err != nil {
		return Section{}, err
	}
	id, err := s.d.IDs()
	if err != nil {
		return Section{}, err
	}
	now := s.d.Now().UTC()
	sec := Section{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: p.UserID, Title: title}
	if in.SortOrder != nil {
		sec.SortOrder = *in.SortOrder
	} else {
		existing, err := s.d.Store.Sections(ctx)
		if err != nil {
			return Section{}, err
		}
		sec.SortOrder = (len(existing) + 1) * 10
	}
	return sec, s.d.Store.PutSection(ctx, sec)
}

// UpdateSection renames or reorders a section (admin).
func (s *Service) UpdateSection(ctx context.Context, p domain.Principal, id domain.ID, in SectionInput) (Section, error) {
	if err := adminOnly(p); err != nil {
		return Section{}, err
	}
	sec, err := s.Section(ctx, p, id)
	if err != nil {
		return Section{}, err
	}
	if in.Title != nil {
		if sec.Title, err = cleanTitle(*in.Title); err != nil {
			return Section{}, err
		}
	}
	if in.SortOrder != nil {
		sec.SortOrder = *in.SortOrder
	}
	sec.UpdatedAt = s.d.Now().UTC()
	return sec, s.d.Store.PutSection(ctx, sec)
}

// DeleteSection removes a section and its items (admin).
func (s *Service) DeleteSection(ctx context.Context, p domain.Principal, id domain.ID) error {
	if err := adminOnly(p); err != nil {
		return err
	}
	if _, err := s.Section(ctx, p, id); err != nil {
		return err
	}
	return s.d.Store.DeleteSection(ctx, id)
}

// Items lists items, one section's or all, in sort order.
func (s *Service) Items(ctx context.Context, p domain.Principal, section domain.ID) ([]Item, error) {
	if err := staffOnly(p); err != nil {
		return nil, err
	}
	out, err := s.d.Store.Items(ctx, section)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SectionID != out[j].SectionID {
			return out[i].SectionID < out[j].SectionID
		}
		return out[i].SortOrder < out[j].SortOrder
	})
	return out, err
}

// Item returns one item.
func (s *Service) Item(ctx context.Context, p domain.Principal, id domain.ID) (Item, error) {
	all, err := s.Items(ctx, p, "")
	if err != nil {
		return Item{}, err
	}
	for _, it := range all {
		if it.ID == id {
			return it, nil
		}
	}
	return Item{}, domain.NotFound("help item")
}

// ItemInput is an item write; nil fields keep their value on update.
type ItemInput struct {
	SectionID *domain.ID  `json:"section_id"`
	Title     *string     `json:"title"`
	Kind      *string     `json:"kind"`
	Body      *string     `json:"body"`
	FileID    **domain.ID `json:"file_id"`
	SortOrder *int        `json:"sort_order"`
}

var videoURL = regexp.MustCompile(`^https://[^\s"'<>]+$`)

func (s *Service) checkItem(ctx context.Context, it Item) error {
	switch it.Kind {
	case "text":
		if strings.TrimSpace(it.Body) == "" {
			return domain.Invalid("body", "a text item needs a body")
		}
	case "video", "image":
		hasFile := it.FileID != nil && !it.FileID.IsZero()
		if !hasFile && !videoURL.MatchString(strings.TrimSpace(it.Body)) {
			return domain.Invalid("file_id", "a "+it.Kind+" item needs a file or an https link in the body")
		}
	default:
		return domain.Invalid("kind", "kind is text, video or image")
	}
	if utf8.RuneCountInString(it.Body) > 20000 {
		return domain.Invalid("body", "at most 20000 characters")
	}
	secs, err := s.d.Store.Sections(ctx)
	if err != nil {
		return err
	}
	for _, sec := range secs {
		if sec.ID == it.SectionID {
			return nil
		}
	}
	return domain.Invalid("section_id", "unknown help section")
}

// CreateItem adds an item to a section (admin).
func (s *Service) CreateItem(ctx context.Context, p domain.Principal, in ItemInput) (Item, error) {
	if err := adminOnly(p); err != nil {
		return Item{}, err
	}
	if in.SectionID == nil || in.Title == nil || in.Kind == nil {
		return Item{}, domain.Invalid("body", "section_id, title and kind are required")
	}
	title, err := cleanTitle(*in.Title)
	if err != nil {
		return Item{}, err
	}
	id, err := s.d.IDs()
	if err != nil {
		return Item{}, err
	}
	now := s.d.Now().UTC()
	it := Item{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: p.UserID, SectionID: *in.SectionID, Title: title, Kind: *in.Kind}
	if in.Body != nil {
		it.Body = *in.Body
	}
	if in.FileID != nil {
		it.FileID = *in.FileID
	}
	if in.SortOrder != nil {
		it.SortOrder = *in.SortOrder
	} else {
		existing, err := s.d.Store.Items(ctx, it.SectionID)
		if err != nil {
			return Item{}, err
		}
		it.SortOrder = (len(existing) + 1) * 10
	}
	if err := s.checkItem(ctx, it); err != nil {
		return Item{}, err
	}
	return it, s.d.Store.PutItem(ctx, it)
}

// UpdateItem edits an item (admin).
func (s *Service) UpdateItem(ctx context.Context, p domain.Principal, id domain.ID, in ItemInput) (Item, error) {
	if err := adminOnly(p); err != nil {
		return Item{}, err
	}
	it, err := s.Item(ctx, p, id)
	if err != nil {
		return Item{}, err
	}
	if in.SectionID != nil {
		it.SectionID = *in.SectionID
	}
	if in.Title != nil {
		if it.Title, err = cleanTitle(*in.Title); err != nil {
			return Item{}, err
		}
	}
	if in.Kind != nil {
		it.Kind = *in.Kind
	}
	if in.Body != nil {
		it.Body = *in.Body
	}
	if in.FileID != nil {
		it.FileID = *in.FileID
	}
	if in.SortOrder != nil {
		it.SortOrder = *in.SortOrder
	}
	if err := s.checkItem(ctx, it); err != nil {
		return Item{}, err
	}
	it.UpdatedAt = s.d.Now().UTC()
	return it, s.d.Store.PutItem(ctx, it)
}

// DeleteItem removes an item (admin).
func (s *Service) DeleteItem(ctx context.Context, p domain.Principal, id domain.ID) error {
	if err := adminOnly(p); err != nil {
		return err
	}
	if _, err := s.Item(ctx, p, id); err != nil {
		return err
	}
	return s.d.Store.DeleteItem(ctx, id)
}

// --- Module guide pages ---

var moduleSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
var pageSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)

// moduleWriter allows the module's own token (ModuleID equal to module), and admins
// acting for the module runtime on install, update and uninstall.
func moduleWriter(p domain.Principal, module string) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	if p.ModuleID != "" {
		if p.ModuleID != module {
			return domain.Forbidden("a module can write help pages only under its own heading")
		}
		return nil
	}
	return adminOnly(p)
}

func moduleReader(p domain.Principal, module string) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	if p.ModuleID != "" {
		if p.ModuleID != module {
			return domain.Forbidden("a module reads only its own help pages")
		}
		return nil
	}
	return staffOnly(p)
}

// PageInput is one module page write.
type PageInput struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Markdown string   `json:"markdown"`
	Keywords []string `json:"keywords"`
	Covers   []string `json:"covers"`
}

func cleanPage(module string, in PageInput, order int, at time.Time) (Page, error) {
	if !pageSlug.MatchString(in.ID) {
		return Page{}, domain.Invalid("id", "a page id is a lower-case slug")
	}
	title, err := cleanTitle(in.Title)
	if err != nil {
		return Page{}, err
	}
	if utf8.RuneCountInString(in.Markdown) > 100000 {
		return Page{}, domain.Invalid("markdown", "a page is at most 100000 characters")
	}
	pg := Page{ID: in.ID, ModuleID: module, Title: title, Markdown: in.Markdown, Keywords: in.Keywords, Covers: in.Covers, SortOrder: order, UpdatedAt: at}
	if pg.Keywords == nil {
		pg.Keywords = []string{}
	}
	if pg.Covers == nil {
		pg.Covers = []string{}
	}
	for _, c := range pg.Covers {
		if !strings.HasPrefix(c, module+".") && !strings.HasPrefix(c, "/modules/"+module) {
			return Page{}, domain.Invalid("covers", "a module page covers only the module's own widgets, tabs and screens")
		}
	}
	return pg, nil
}

// ModulePages lists one module's pages.
func (s *Service) ModulePages(ctx context.Context, p domain.Principal, module string) ([]Page, error) {
	if err := moduleReader(p, module); err != nil {
		return nil, err
	}
	return s.d.Store.Pages(ctx, module)
}

// RegisterPage adds or replaces one page under the module's heading.
func (s *Service) RegisterPage(ctx context.Context, p domain.Principal, module string, in PageInput) (Page, error) {
	if err := moduleWriter(p, module); err != nil {
		return Page{}, err
	}
	existing, err := s.d.Store.Pages(ctx, module)
	if err != nil {
		return Page{}, err
	}
	order := len(existing)
	for _, e := range existing {
		if e.ID == in.ID {
			order = e.SortOrder
		}
	}
	pg, err := cleanPage(module, in, order, s.d.Now().UTC())
	if err != nil {
		return Page{}, err
	}
	if err := s.d.Store.PutPage(ctx, pg); err != nil {
		return Page{}, err
	}
	if _, ok := s.moduleState(ctx, module); !ok {
		if err := s.d.Store.SetModuleEnabled(ctx, module, true, s.d.Now().UTC()); err != nil {
			return Page{}, err
		}
	}
	return pg, nil
}

// ReplacePages swaps a module's pages for a new version's, as on a module update.
func (s *Service) ReplacePages(ctx context.Context, p domain.Principal, module string, in []PageInput) ([]Page, error) {
	if err := moduleWriter(p, module); err != nil {
		return nil, err
	}
	now := s.d.Now().UTC()
	seen := map[string]bool{}
	var pages []Page
	for i, pi := range in {
		pg, err := cleanPage(module, pi, i, now)
		if err != nil {
			return nil, err
		}
		if seen[pg.ID] {
			return nil, domain.Invalid("pages", "duplicate page id "+pg.ID)
		}
		seen[pg.ID] = true
		pages = append(pages, pg)
	}
	if err := s.d.Store.ReplacePages(ctx, module, pages, now); err != nil {
		return nil, err
	}
	if _, ok := s.moduleState(ctx, module); !ok {
		if err := s.d.Store.SetModuleEnabled(ctx, module, true, now); err != nil {
			return nil, err
		}
	}
	if pages == nil {
		pages = []Page{}
	}
	return pages, nil
}

// RemovePage deletes one page.
func (s *Service) RemovePage(ctx context.Context, p domain.Principal, module, id string) error {
	if err := moduleWriter(p, module); err != nil {
		return err
	}
	if err := s.d.Store.DeletePage(ctx, module, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.NotFound("help page")
		}
		return err
	}
	return nil
}

// SetModuleEnabled hides (disable) or shows (enable) a module's pages. The module
// runtime calls it as the system when an admin disables or enables the module.
func (s *Service) SetModuleEnabled(ctx context.Context, module string, enabled bool) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	return s.d.Store.SetModuleEnabled(ctx, module, enabled, s.d.Now().UTC())
}

// RemoveModule deletes every page of an uninstalled module.
func (s *Service) RemoveModule(ctx context.Context, module string) error {
	if !moduleSlug.MatchString(module) {
		return domain.Invalid("module_id", "module id must be a slug")
	}
	return s.d.Store.ReplacePages(ctx, module, nil, s.d.Now().UTC())
}

func (s *Service) moduleState(ctx context.Context, module string) (bool, bool) {
	mods, err := s.d.Store.Modules(ctx)
	if err != nil {
		return false, false
	}
	on, ok := mods[module]
	return on, ok
}

// ModuleTopics returns the enabled modules' pages as topics under each module heading.
func (s *Service) ModuleTopics(ctx context.Context) ([]Chapter, error) {
	mods, err := s.d.Store.Modules(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for m, on := range mods {
		if on {
			names = append(names, m)
		}
	}
	sort.Strings(names)
	var out []Chapter
	for _, m := range names {
		pages, err := s.d.Store.Pages(ctx, m)
		if err != nil {
			return nil, err
		}
		if len(pages) == 0 {
			continue
		}
		ch := Chapter{Title: "Module: " + m}
		for _, pg := range pages {
			ch.Topics = append(ch.Topics, Topic{ID: m + "." + pg.ID, Title: pg.Title, Chapter: ch.Title, Markdown: pg.Markdown,
				Keywords: pg.Keywords, Paths: pg.Covers, Module: m})
		}
		out = append(out, ch)
	}
	return out, nil
}

// Topic finds a core or enabled module topic.
func (s *Service) Topic(ctx context.Context, p domain.Principal, id string) (Topic, error) {
	if err := staffOnly(p); err != nil {
		return Topic{}, err
	}
	if t, ok := s.d.Guide.Topic(id); ok {
		return t, nil
	}
	chs, err := s.ModuleTopics(ctx)
	if err != nil {
		return Topic{}, err
	}
	for _, ch := range chs {
		for _, t := range ch.Topics {
			if t.ID == id {
				return t, nil
			}
		}
	}
	return Topic{}, domain.NotFound("help topic")
}

// TopicHit is a contract HelpTopic.
type TopicHit struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Screen string `json:"screen"`
	Path   string `json:"path"`
	Where  string `json:"-"`
	Module string `json:"-"`
	Score  int    `json:"-"`
}

// SearchTopics finds topics by name and by the words on the screen (keywords and body),
// core and enabled module pages alike. An empty query lists every topic; screen limits
// the answer to the topic of one screen path.
func (s *Service) SearchTopics(ctx context.Context, p domain.Principal, q, screen string) ([]TopicHit, error) {
	if err := staffOnly(p); err != nil {
		return nil, err
	}
	topics := s.d.Guide.Topics()
	chs, err := s.ModuleTopics(ctx)
	if err != nil {
		return nil, err
	}
	for _, ch := range chs {
		topics = append(topics, ch.Topics...)
	}
	if screen != "" {
		if t, ok := s.d.Guide.ForPath(screen); ok {
			return []TopicHit{hit(t, 0)}, nil
		}
		for _, t := range topics {
			for _, c := range t.Paths {
				if pathMatches(c, screen) || c == screen {
					return []TopicHit{hit(t, 0)}, nil
				}
			}
		}
		return []TopicHit{}, nil
	}
	words := strings.Fields(strings.ToLower(q))
	var out []TopicHit
	for _, t := range topics {
		score := 0
		title := strings.ToLower(t.Title)
		kw := strings.ToLower(strings.Join(t.Keywords, " ") + " " + t.Where)
		body := strings.ToLower(PlainText(t.Markdown))
		for _, w := range words {
			switch {
			case strings.Contains(title, w):
				score += 10
			case strings.Contains(kw, w):
				score += 5
			case strings.Contains(body, w):
				score++
			default:
				score = -1000
			}
		}
		// The whole phrase, as it reads on the screen, outranks scattered words.
		if phrase := strings.ToLower(strings.TrimSpace(q)); score >= 0 && len(words) > 1 && (strings.Contains(title, phrase) || strings.Contains(kw, phrase)) {
			score += 20
		}
		if score >= 0 {
			out = append(out, hit(t, score))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if out == nil {
		out = []TopicHit{}
	}
	return out, nil
}

func hit(t Topic, score int) TopicHit {
	return TopicHit{ID: t.ID, Title: t.Title, Screen: t.Screen(), Path: t.Path(), Where: t.Where, Module: t.Module, Score: score}
}

// --- llms.txt (PRD 8.18) ---

// LLMsTxt is the public agent guide. It reads only llms.show_firm_name,
// llms.firm_paragraph and firm.name: never a client, matter or other firm record.
func (s *Service) LLMsTxt(ctx context.Context, baseURL string) string {
	get := func(k string) string {
		v, _ := s.d.Settings.Get(ctx, k)
		return v
	}
	var b strings.Builder
	b.WriteString("# Togavsu\n\n")
	fmt.Fprintf(&b, "> Togavsu practice management, version %s. A law firm's own install: contacts, matters, time and invoicing, trust, calendar, files, the document editor and Doc Tender.\n\n", s.d.Version)
	if show, _ := strconv.ParseBool(get("llms.show_firm_name")); show {
		if name := strings.TrimSpace(get("firm.name")); name != "" {
			fmt.Fprintf(&b, "This install belongs to %s.\n\n", oneLine(name))
		}
	}
	if para := strings.TrimSpace(get("llms.firm_paragraph")); para != "" {
		b.WriteString(oneLine(para) + "\n\n")
	}
	b.WriteString("## For agents\n\n")
	fmt.Fprintf(&b, "- OpenAPI 3.1 spec: %s/api/v1/openapi.json\n", baseURL)
	fmt.Fprintf(&b, "- MCP endpoint (same operations, generated from the spec): %s/mcp\n", baseURL)
	b.WriteString("- Every call needs a bearer token (Authorization: Bearer <token>). The firm's admin issues scoped, revocable tokens on Administrative > Agents and API.\n")
	fmt.Fprintf(&b, "- API docs: the OpenAPI spec above describes every endpoint, scope and error code; REST JSON lives under %s/api/v1.\n", baseURL)
	b.WriteString("- Writes accept an Idempotency-Key header. Lists use cursor pagination.\n\n")
	b.WriteString("This file holds no client, matter or firm records. Access to them is decided per call by the token's user, roles, scopes and matter walls.\n")
	return b.String()
}

func oneLine(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
	if utf8.RuneCountInString(s) > 1000 {
		s = string([]rune(s)[:1000])
	}
	return s
}

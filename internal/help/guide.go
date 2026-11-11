package help

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

// The guide has one source: the Markdown files embedded here. The core renders it to
// HTML and PDF from this source, so every image carries the guide of its own version.
//
// ponytail: ceiling: the PRD names /docs/guide as the source folder; Go embed cannot
// reach outside a package, so the files live in internal/help/guide. Guide pages carry
// no screenshots yet. Upgrade path: a docs/guide package with its own embed.go that this
// package imports, plus seed-data screenshots from the UI QC run. Trigger: the release
// task's guide build (PRD 8.11), or the integration lead allocating docs/guide.
//
//go:embed guide/*.md
var guideFS embed.FS

// Topic is one guide topic: a screen, an admin tab or a role's tutorial.
type Topic struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Chapter   string   `json:"chapter"`
	Templates []string `json:"-"`
	Paths     []string `json:"-"`
	Tab       string   `json:"-"`
	Tutorial  string   `json:"-"`
	Where     string   `json:"where"`
	Keywords  []string `json:"keywords"`
	Markdown  string   `json:"-"`
	// Module is the owning module for a module page; empty for core topics.
	Module string `json:"module,omitempty"`
}

// Path is where the topic opens in the help center.
func (t Topic) Path() string { return "/help/topics/" + t.ID }

// Screen is the first screen path the topic covers.
func (t Topic) Screen() string {
	if len(t.Paths) > 0 {
		return t.Paths[0]
	}
	return t.Path()
}

// Chapter is one "# " section of the guide.
type Chapter struct {
	Title  string
	Intro  string
	Topics []Topic
}

// Guide is the parsed user guide.
type Guide struct {
	Chapters []Chapter
	byID     map[string]Topic
	order    []string
}

var (
	topicHead = regexp.MustCompile(`^## (.+?)\s*\{#([a-z0-9_-]+)\}\s*$`)
	metaLine  = regexp.MustCompile(`^<!--\s*(.*?)\s*-->\s*$`)
)

// LoadGuide parses the embedded guide.
func LoadGuide() (*Guide, error) { return ParseGuide(guideFS, "guide") }

// ParseGuide parses every .md file in dir of fsys, in file name order.
func ParseGuide(fsys fs.FS, dir string) (*Guide, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	g := &Guide{byID: map[string]Topic{}}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, err
		}
		if err := g.parse(name, string(raw)); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (g *Guide) parse(file, src string) error {
	var ch *Chapter
	var cur *Topic
	var body []string
	flush := func() {
		if cur != nil {
			cur.Markdown = strings.TrimSpace(strings.Join(body, "\n"))
			ch.Topics = append(ch.Topics, *cur)
			cur = nil
		} else if ch != nil {
			ch.Intro = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = nil
	}
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "# "):
			flush()
			if ch != nil {
				g.Chapters = append(g.Chapters, *ch)
			}
			ch = &Chapter{Title: strings.TrimSpace(line[2:])}
		case strings.HasPrefix(line, "## "):
			if ch == nil {
				return fmt.Errorf("help: %s: topic before a chapter heading", file)
			}
			m := topicHead.FindStringSubmatch(line)
			if m == nil {
				return fmt.Errorf("help: %s:%d: a topic heading needs an {#id}", file, i+1)
			}
			flush()
			if _, dup := g.byID[m[2]]; dup {
				return fmt.Errorf("help: %s: duplicate topic id %s", file, m[2])
			}
			cur = &Topic{ID: m[2], Title: m[1], Chapter: ch.Title}
			if i+1 < len(lines) {
				if mm := metaLine.FindStringSubmatch(lines[i+1]); mm != nil {
					applyMeta(cur, mm[1])
					i++
				}
			}
			g.byID[cur.ID] = *cur
			g.order = append(g.order, cur.ID)
		default:
			body = append(body, line)
		}
	}
	flush()
	if ch != nil {
		g.Chapters = append(g.Chapters, *ch)
	}
	// byID holds the final copies with their Markdown.
	for _, c := range g.Chapters {
		for _, t := range c.Topics {
			g.byID[t.ID] = t
		}
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func applyMeta(t *Topic, meta string) {
	for _, part := range strings.Split(meta, "|") {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "template":
			t.Templates = append(t.Templates, splitList(v)...)
		case "path":
			t.Paths = append(t.Paths, splitList(v)...)
		case "tab":
			t.Tab = v
		case "tutorial":
			t.Tutorial = v
		case "where":
			t.Where = v
		case "keywords":
			t.Keywords = splitList(v)
		}
	}
}

// Topics lists every topic in guide order.
func (g *Guide) Topics() []Topic {
	out := make([]Topic, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.byID[id])
	}
	return out
}

// Topic returns one topic by id.
func (g *Guide) Topic(id string) (Topic, bool) {
	t, ok := g.byID[id]
	return t, ok
}

// ForTemplate returns the topic covering a page template ("billing/invoices.html").
func (g *Guide) ForTemplate(name string) (Topic, bool) {
	for _, id := range g.order {
		for _, tpl := range g.byID[id].Templates {
			if tpl == name {
				return g.byID[id], true
			}
		}
	}
	return Topic{}, false
}

// ForTab returns the topic of an admin tab.
func (g *Guide) ForTab(tab string) (Topic, bool) {
	for _, id := range g.order {
		if g.byID[id].Tab == tab {
			return g.byID[id], true
		}
	}
	return Topic{}, false
}

// Tutorials returns the getting-started tutorial of each role, in guide order.
func (g *Guide) Tutorials() []Topic {
	var out []Topic
	for _, id := range g.order {
		if g.byID[id].Tutorial != "" {
			out = append(out, g.byID[id])
		}
	}
	return out
}

// pathMatches reports whether a request path matches a pattern like
// /billing/invoices/{invoice_id}. A pattern with a query (/admin?tab=x) matches only its path part.
func pathMatches(pattern, path string) bool {
	pattern, _, _ = strings.Cut(pattern, "?")
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}") {
			if xs[i] == "" {
				return false
			}
			continue
		}
		if ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// ForPath returns the topic for the screen at path, the one the ? icon opens. A fixed
// path beats a pattern with a {parameter}.
func (g *Guide) ForPath(path string) (Topic, bool) {
	var best Topic
	bestScore := -1
	for _, id := range g.order {
		t := g.byID[id]
		if t.Tab != "" {
			continue
		}
		for _, p := range t.Paths {
			if !pathMatches(p, path) {
				continue
			}
			score := 100 - strings.Count(p, "{")
			if score > bestScore {
				best, bestScore = t, score
			}
		}
	}
	return best, bestScore >= 0
}

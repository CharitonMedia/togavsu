package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
)

// screenTemplate marks a page template: it opens a whole page (a domain's "head" layout
// or the shell). Layouts and partials define those and are not screens themselves.
var screenTemplate = regexp.MustCompile(`\{\{template "(head|shell-top)"`)

// TestEveryScreenAndAdminTabHasAGuideTopic is the AC-032 CI check. It runs in the Core
// tests job (scripts/ci/test-core.sh runs every Go package), so a new screen template
// under web/templates or a new admin tab without a guide topic fails the build.
func TestEveryScreenAndAdminTabHasAGuideTopic(t *testing.T) {
	g, err := help.LoadGuide()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("..", "..", "web", "templates")
	files, err := filepath.Glob(filepath.Join(root, "*", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	var screens []string
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(f)
		// shell/ holds the shell itself and its partials, not screens.
		if base == "error.html" || filepath.Base(filepath.Dir(f)) == "shell" || !screenTemplate.Match(raw) {
			continue
		}
		name := filepath.Base(filepath.Dir(f)) + "/" + base
		screens = append(screens, name)
		topic, ok := g.ForTemplate(name)
		if !ok {
			t.Errorf("screen %s has no guide topic: add one to internal/help/guide with template: %s", name, name)
			continue
		}
		checkTopic(t, topic)
	}
	if len(screens) < 40 {
		t.Fatalf("found only %d screens under %s; the scan is broken: %v", len(screens), root, screens)
	}
	for _, tab := range AdminTabIDs() {
		topic, ok := g.ForTab(tab)
		if !ok {
			t.Errorf("admin tab %s has no guide topic (tab: %s)", tab, tab)
			continue
		}
		checkTopic(t, topic)
		if !strings.Contains(topic.Where, "Administrative > ") {
			t.Errorf("admin tab topic %s does not give its menu path: %q", topic.ID, topic.Where)
		}
	}
	// The core's own screens without templates: sign-in and first-run setup.
	for _, path := range []string{"/login", "/setup"} {
		if _, ok := g.ForPath(path); !ok {
			t.Errorf("%s has no guide topic", path)
		}
	}
	roles := map[string]bool{}
	for _, tut := range g.Tutorials() {
		roles[tut.Tutorial] = true
		checkTopic(t, tut)
	}
	for _, r := range []domain.Role{domain.RoleAdmin, domain.RoleAttorney, domain.RoleStaff, domain.RoleAccountant} {
		if !roles[string(r)] {
			t.Errorf("no getting-started tutorial for %s", r)
		}
	}
	sort.Strings(screens)
	t.Logf("%d screens and %d admin tabs covered", len(screens), len(AdminTabIDs()))
}

func checkTopic(t *testing.T, topic help.Topic) {
	t.Helper()
	if strings.TrimSpace(topic.Where) == "" {
		t.Errorf("topic %s does not say where it lives", topic.ID)
	}
	if len(strings.Fields(help.PlainText(topic.Markdown))) < 12 {
		t.Errorf("topic %s does not say what the screen does", topic.ID)
	}
}

// The ? icon opens the topic for the screen the user is on.
func TestHelpIconTopicForEachScreenPath(t *testing.T) {
	g, err := help.LoadGuide()
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/": "home", "/billing/invoices": "billing-invoices", "/billing/invoices/0190": "billing-invoice",
		"/practice/matters/needs-case-number": "practice-needs-case-number", "/practice/matters/0190": "practice-matter",
		"/practice/matters/0190/files": "files-matter", "/calendar/deadlines": "calendar-deadlines", "/help": "help-center",
		"/trust/flags/1": "trust-flag", "/me/settings": "my-settings",
	} {
		got, ok := g.ForPath(path)
		if !ok || got.ID != want {
			t.Errorf("? on %s opens %q, want %q", path, got.ID, want)
		}
	}
}

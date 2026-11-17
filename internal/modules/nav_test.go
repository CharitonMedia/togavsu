package modules_test

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestNavEntriesCORE08Fields(t *testing.T) {
	order := 3
	m := modules.Manifest{
		ID: "msg",
		Tabs: []modules.Tab{
			{ID: "inbox", Title: "Inbox", Placement: "home", Path: "/inbox"},
			{ID: "other", Title: "Other", Placement: "home", Path: "/other"},
		},
		Sidebar: []modules.Link{
			{
				ID: "inbox", Title: "Messages", Path: "/inbox", Section: "communications",
				Subgroup: "mail", Icon: "mail", Order: &order,
				Badge: &modules.Badge{Path: "/badge", Tone: "warning"},
			},
		},
	}
	got := modules.NavEntries(m)
	if len(got) != 2 {
		t.Fatalf("entries: %+v", got)
	}
	first := got[0]
	if first.ID != "inbox" || first.Section != dashboard.SectionCommunications || first.Subgroup != "mail" ||
		first.Icon != "mail" || first.Order != 3 || first.Tone != "warning" ||
		first.Path != "/modules/msg/tabs/inbox" || first.Label != "Messages" {
		t.Fatalf("sidebar entry: %+v", first)
	}
	if got[1].ID != "other" || got[1].Section != dashboard.SectionActivity || got[1].Icon != "puzzle" {
		t.Fatalf("home fallback: %+v", got[1])
	}
}

func TestManifestRefusesUnknownSidebarIconAndSection(t *testing.T) {
	base := string(sampleManifest(t))
	withSidebar := func(extra string) string {
		return strings.Replace(base, "widgets:\n",
			"sidebar:\n  - id: clock\n    title: Clock\n    path: /tab\n"+extra+"widgets:\n", 1)
	}
	if _, err := modules.ParseManifest([]byte(withSidebar("    icon: not-an-icon\n"))); err == nil {
		t.Fatal("unknown icon accepted")
	} else if !strings.Contains(err.Error(), "icon") {
		t.Fatalf("icon error: %v", err)
	}
	if _, err := modules.ParseManifest([]byte(withSidebar("    section: nowhere\n"))); err == nil {
		t.Fatal("unknown section accepted")
	} else if !strings.Contains(err.Error(), "section") {
		t.Fatalf("section error: %v", err)
	}
	order := "    section: money\n    icon: mail\n    order: 2\n    badge:\n      path: /count\n      tone: normal\n"
	if _, err := modules.ParseManifest([]byte(withSidebar(order))); err != nil {
		t.Fatalf("valid sidebar: %v", err)
	}
	_ = domain.SidebarDefaultOrder // keep domain import for shared constant sanity
}

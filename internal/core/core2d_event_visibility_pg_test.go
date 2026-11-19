package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// Exercise real file writers and all readers of file event summaries on Postgres.
// Module delivery payloads intentionally contain record ids, never file summaries.
func TestCoreUpdate2DFileEventVisibility(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	matter, _ := x.matter("Synthetic event visibility", "", nil)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	x.installSample()
	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	const hiddenName, normalName = "attorney-secret-event.txt", "ordinary-event.txt"
	makeFile := func(name string, only bool) string {
		t.Helper()
		f := x.upload(x.atty, matter, name, "text/plain", []byte("Synthetic file bytes"), map[string]any{"attorney_only": only})
		id := str(t, f, "id")
		x.atty.must(http.StatusOK, "POST", "/api/v1/files/"+id+"/status", map[string]string{"status": "final"})
		if code, _ := x.download(x.atty, id, str(t, f, "current_version_id")); code != http.StatusOK {
			t.Fatalf("download: %d", code)
		}
		for _, action := range []string{"file.created", "file.version_created", "file.updated", "file.downloaded"} {
			if n := x.count(`SELECT COUNT(*) FROM core.events WHERE record_id=$1 AND action=$2 AND summary LIKE $3`, id, action, "%"+name+"%"); n != 1 {
				t.Fatalf("%s %s: want one real file event, got %d", name, action, n)
			}
		}
		return id
	}
	hidden, normal := makeFile(hiddenName, true), makeFile(normalName, false)
	fullActivity, err := x.app.Practice.Activity(ctx, x.principal(x.atty), practice.ActivityQuery{})
	if err != nil {
		t.Fatal(err)
	}
	assertVisibility := func(t *testing.T, body string, privileged bool) {
		t.Helper()
		if strings.Contains(body, hiddenName) != privileged || strings.Contains(body, hidden) != privileged {
			t.Fatalf("attorney-only event visibility (privileged=%v): %s", privileged, body)
		}
		if !strings.Contains(body, normalName) || !strings.Contains(body, normal) {
			t.Fatalf("ordinary file event missing: %s", body)
		}
	}
	read := func(t *testing.T, c *client, tool, path, viewer string, want int) string {
		t.Helper()
		var r resp
		if tool == "" {
			r = c.do("GET", path, nil, modules.ContextHeader, viewer)
			if r.code != want {
				t.Fatalf("GET %s: %d want %d: %s", path, r.code, want, r.body)
			}
			return r.body
		}
		args := map[string]any{"context_token": viewer}
		if tool == "listMatterTimeline" {
			args["path"] = map[string]string{"matter_id": matter}
		} else {
			args["query"] = map[string]string{"matter_id": matter, "limit": "200"}
		}
		r = mcpCall(c, tool, args)
		result, ok := r.json["result"].(map[string]any)
		if r.code != http.StatusOK || !ok || r.json["error"] != nil || result["isError"] != (want >= http.StatusBadRequest) {
			t.Fatalf("%s envelope: %d %s", tool, r.code, r.body)
		}
		value, ok := result["structuredContent"].(map[string]any)
		if !ok || (want == http.StatusForbidden && value["code"] != "scope_denied") {
			t.Fatalf("%s result: %s", tool, r.body)
		}
		return r.body
	}
	for _, who := range []struct {
		name string
		c    *client
		only bool
	}{{"staff", x.staff, false}, {"attorney", x.atty, true}, {"admin", x.admin, true}, {"service", bot, false}} {
		t.Run(who.name, func(t *testing.T) {
			var p domain.Principal
			if who.name == "service" {
				// The same service principal elevation used by listMatterTimeline.
				p = domain.Principal{ModuleID: "matter-clock", Source: domain.SourceService, Roles: []domain.Role{domain.RoleStaff}}
			} else {
				p = x.principal(who.c)
			}
			timeline, err := x.app.Practice.Timeline(ctx, p, domain.ID(matter))
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(timeline)
			if err != nil {
				t.Fatal(err)
			}
			assertVisibility(t, string(b), who.only)
			activity, err := x.app.Practice.Activity(ctx, p, practice.ActivityQuery{})
			if err != nil {
				t.Fatal(err)
			}
			hiddenCount := 4
			if who.only {
				hiddenCount = 0
			}
			if activity.All != fullActivity.All-hiddenCount || activity.Staff != fullActivity.Staff-hiddenCount {
				t.Fatalf("hidden file events leaked into tab counts: %+v", activity)
			}
			b, err = json.Marshal(activity)
			if err != nil {
				t.Fatal(err)
			}
			assertVisibility(t, string(b), who.only)
			for _, transport := range []string{"REST", "MCP"} {
				t.Run(transport, func(t *testing.T) {
					tool := ""
					if transport == "MCP" {
						tool = "listMatterTimeline"
					}
					assertVisibility(t, read(t, who.c, tool, "/api/v1/matters/"+matter+"/timeline", "", http.StatusOK), who.only)
					if transport == "MCP" {
						tool = "listEvents"
					}
					want := http.StatusOK
					if who.name == "service" {
						// The firm feed still requires a delegated viewer; do not widen it.
						want = http.StatusForbidden
					}
					body := read(t, who.c, tool, "/api/v1/events?matter_id="+matter+"&limit=200", "", want)
					if want == http.StatusOK {
						assertVisibility(t, body, who.only)
					} else {
						if strings.Contains(body, hiddenName) || strings.Contains(body, hidden) {
							t.Fatal("refused service feed leaked a file")
						}
						viewer := str(t, x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{}).json, "token")
						assertVisibility(t, read(t, bot, tool, "/api/v1/events?matter_id="+matter+"&limit=200", viewer, http.StatusOK), false)
					}
				})
			}
			if who.name != "service" {
				for _, path := range []string{"/practice/matters/" + matter, "/practice/activity", "/api/v1/widgets/recent-activity"} {
					body := who.c.must(http.StatusOK, "GET", path, nil).body
					if strings.Contains(body, hiddenName) != who.only || !strings.Contains(body, normalName) {
						t.Fatalf("%s file summary visibility (privileged=%v)", path, who.only)
					}
				}
				widget := mcpCall(who.c, toolName(t, "GET", "/api/v1/widgets/recent-activity"), map[string]any{})
				result, ok := widget.json["result"].(map[string]any)
				if !ok || result["isError"] != false || strings.Contains(widget.body, hiddenName) != who.only || !strings.Contains(widget.body, normalName) {
					t.Fatalf("MCP recent activity visibility (privileged=%v): %s", who.only, widget.body)
				}
			}
		})
	}
	// Paging must walk past hidden rows without leaking or losing visible events.
	var visible strings.Builder
	for cursor := ""; ; {
		page := x.staff.must(http.StatusOK, "GET", "/api/v1/events?matter_id="+matter+"&limit=1&cursor="+cursor, nil)
		if strings.Contains(page.body, hiddenName) || strings.Contains(page.body, hidden) {
			t.Fatal("paged feed leaked an attorney-only file event")
		}
		visible.WriteString(page.body)
		cursor, _ = page.json["page"].(map[string]any)["next_cursor"].(string)
		if cursor == "" {
			break
		}
	}
	assertVisibility(t, visible.String(), false)

	// Visibility follows the current file flag, even for older event summaries.
	x.atty.must(http.StatusOK, "PATCH", "/api/v1/files/"+hidden, map[string]any{"attorney_only": false})
	assertVisibility(t, read(t, bot, "listMatterTimeline", "", "", http.StatusOK), true)
	x.atty.must(http.StatusOK, "PATCH", "/api/v1/files/"+hidden, map[string]any{"attorney_only": true})
	assertVisibility(t, read(t, bot, "listMatterTimeline", "", "", http.StatusOK), false)
}

package compose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// fakeFiles stands in for the files service the core will pass (FileVersions): it
// answers a file's current version, and hides walled files from everyone but admins.
type fakeFiles struct {
	versions map[domain.ID]domain.ID
	walled   map[domain.ID]bool
}

func (f fakeFiles) CurrentVersion(_ context.Context, p domain.Principal, file domain.ID) (domain.ID, error) {
	v, ok := f.versions[file]
	if !ok || (f.walled[file] && !p.HasRole(domain.RoleAdmin)) {
		return "", domain.NotFound("file")
	}
	return v, nil
}

const (
	helpImage  = domain.ID("0190d000-0000-7000-8000-000000000001")
	helpVideo  = domain.ID("0190d000-0000-7000-8000-000000000002")
	helpWalled = domain.ID("0190d000-0000-7000-8000-000000000003")
	imageVer   = domain.ID("0190d000-0000-7000-8000-0000000000a1")
	videoVer   = domain.ID("0190d000-0000-7000-8000-0000000000a2")
	walledVer  = domain.ID("0190d000-0000-7000-8000-0000000000a3")
)

// contractGET reports whether path is a GET route in the published OpenAPI document.
func contractGET(t *testing.T, path string) bool {
	t.Helper()
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	for pattern, ops := range doc.Paths {
		if _, ok := ops["get"]; !ok {
			continue
		}
		re := "^" + regexp.MustCompile(`\\\{[^/]+\\\}`).ReplaceAllString(regexp.QuoteMeta(pattern), `[^/]+`) + "$"
		if regexp.MustCompile(re).MatchString(path) {
			return true
		}
	}
	return false
}

// TestUIHelpMediaItems is PRD 8.11's "an item is text, a video or an image": an image
// item and an uploaded video render from the files service's versioned download route
// (a real contract route), resolved as the viewer; a file the viewer may not read shows
// a note, not a broken element; image links are refused because the shell CSP allows
// only same-origin images.
func TestUIHelpMediaItems(t *testing.T) {
	ff := fakeFiles{versions: map[domain.ID]domain.ID{helpImage: imageVer, helpVideo: videoVer, helpWalled: walledVer}, walled: map[domain.ID]bool{helpWalled: true}}
	e := newEnvWith(t, Options{Files: ff})
	e.admin.form("/admin/help/sections", url.Values{"title": {"Screens"}})
	sec := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/help/sections", nil))[0]["id"].(string)
	add := func(v url.Values) string {
		v.Set("section_id", sec)
		return e.admin.form("/admin/help/items", v).loc
	}
	// Refused: an outside image link (CSP img-src 'self' data:), an image with no file,
	// and a file that does not exist.
	for name, v := range map[string]url.Values{
		"image link":   {"title": {"Logo"}, "kind": {"image"}, "body": {"https://cdn.example/logo.png"}},
		"image empty":  {"title": {"Logo"}, "kind": {"image"}},
		"unknown file": {"title": {"Logo"}, "kind": {"image"}, "file_id": {"0190d000-0000-7000-8000-0000000000ff"}},
	} {
		if loc := add(v); !strings.Contains(loc, "error=") {
			t.Fatalf("%s accepted: %s", name, loc)
		}
	}
	for _, v := range []url.Values{
		{"title": {"The matter screen"}, "kind": {"image"}, "file_id": {string(helpImage)}},
		{"title": {"Intake recording"}, "kind": {"video"}, "file_id": {string(helpVideo)}},
		{"title": {"Partner notes"}, "kind": {"image"}, "file_id": {string(helpWalled)}},
		{"title": {"Intake walkthrough"}, "kind": {"video"}, "body": {"https://videos.firm.example/intake"}},
	} {
		if loc := add(v); strings.Contains(loc, "error=") {
			t.Fatalf("%s refused: %s", v.Get("title"), loc)
		}
	}
	page := e.staff.must(http.StatusOK, "GET", "/help", nil)
	imgSrc := "/api/v1/files/" + string(helpImage) + "/versions/" + string(imageVer) + "/download"
	vidSrc := "/api/v1/files/" + string(helpVideo) + "/versions/" + string(videoVer) + "/download"
	for _, want := range []string{
		`<img src="` + imgSrc + `" alt="The matter screen">`,
		`<video controls src="` + vidSrc + `"></video>`,
		`<a href="https://videos.firm.example/intake" rel="noopener" target="_blank">Watch Intake walkthrough</a>`,
	} {
		if !strings.Contains(page.body, want) {
			i := strings.Index(page.body, "help-section")
			t.Fatalf("/help lacks %s:\n%.3000s", want, page.body[max(i, 0):])
		}
	}
	if !regexp.MustCompile(`<h3>Partner notes</h3>\s*<p class="hint">This image isn't available to you\.</p>`).MatchString(page.body) {
		t.Fatal("the walled image does not show its note")
	}
	if strings.Contains(page.body, string(walledVer)) || strings.Contains(page.body, "<img src=\"https") {
		t.Fatal("/help leaked a walled file or rendered an outside image")
	}
	// Every media element on the page points at a contract route the browser may load.
	srcs := regexp.MustCompile(`<(?:img|video)[^>]* src="([^"]+)"`).FindAllStringSubmatch(page.body, -1)
	if len(srcs) != 2 {
		t.Fatalf("media elements: %v", srcs)
	}
	for _, m := range srcs {
		if !contractGET(t, m[1]) {
			t.Fatalf("%s is not a contract GET route", m[1])
		}
	}
	if contractGET(t, "/api/v1/files/"+string(helpImage)+"/download") {
		t.Fatal("contract check matches the old, nonexistent route")
	}
	if !strings.Contains(page.csp, "img-src 'self' data:") || !strings.Contains(page.csp, "media-src 'self';") {
		t.Fatalf("CSP: %s", page.csp)
	}
	// The admin, who may read the walled file, sees it.
	if a := e.admin.must(http.StatusOK, "GET", "/help", nil); !strings.Contains(a.body, string(walledVer)) {
		t.Fatal("admin cannot see the walled image")
	}
}

// TestUIHelpMediaWithoutFiles: with no files service connected, file-backed help items
// are refused instead of stored as items that cannot be shown.
func TestUIHelpMediaWithoutFiles(t *testing.T) {
	e := newEnv(t)
	e.admin.form("/admin/help/sections", url.Values{"title": {"Screens"}})
	sec := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/help/sections", nil))[0]["id"].(string)
	if loc := e.admin.form("/admin/help/items", url.Values{"section_id": {sec}, "title": {"Clip"}, "kind": {"video"}, "file_id": {string(helpVideo)}}).loc; !strings.Contains(loc, "error=") {
		t.Fatalf("file-backed item stored without a files service: %s", loc)
	}
}

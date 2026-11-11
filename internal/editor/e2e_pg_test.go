package editor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	editorpages "github.com/CharitonMedia/togavsu/web/templates/editor"
)

// TestBrowserEditPageE2E drives the edit page in headless Chromium with the real browser
// editor built at the pin (web/editor/e2e/edit-page.mjs): the LawCraft wrapper starts on
// WebGL2, receives the document and the firm's details, takes typing, shows unsaved
// changes in the page header, and its Ctrl+S save becomes version 2; New Pleading, opened
// from Tell me, opens with the matter's court, county, case number and parties. It needs
// TOGAVSU_EDITOR_DIST, CHROME and PUPPETEER_CORE, and runs when TOGAVSU_BROWSER_E2E is set.
func TestBrowserEditPageE2E(t *testing.T) {
	if os.Getenv("TOGAVSU_BROWSER_E2E") == "" {
		t.Skip("TOGAVSU_BROWSER_E2E is not set")
	}
	distDir := os.Getenv("TOGAVSU_EDITOR_DIST")
	e := newEnv(t)
	who := map[string]domain.Principal{"staff": e.staff}
	h, err := NewHandler(e.svc, editorpages.FS, DistDir(distDir), func(r *http.Request) (domain.Principal, error) {
		if p, ok := who[r.Header.Get("X-Test-User")]; ok {
			return p, nil
		}
		return domain.Principal{}, domain.Unauthenticated("sign in is required")
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := h.Routes()
	// The page links the practice stylesheet; the core serves it, so a stub stands in.
	mux.HandleFunc("GET /practice/static/practice.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := e.create(e.staff, "Browser test.docx", e.matter, e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic browser document."}}), false)

	_, here, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(here), "..", "..", "web", "editor", "e2e", "edit-page.mjs")
	shots := os.Getenv("TOGAVSU_E2E_SHOTS")
	cmd := exec.Command("node", script, srv.URL, string(f.ID), "staff", shots)
	cmd.Stderr = os.Stderr
	raw, runErr := cmd.Output()
	var out struct {
		Error      string           `json:"error"`
		Saved      bool             `json:"saved"`
		State      string           `json:"state"`
		Messages   []map[string]any `json:"messages"`
		Console    []string         `json:"console"`
		Violations []string         `json:"violations"`
		Missing    []string         `json:"missing"`
		Pleading   map[string]any   `json:"pleading"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("e2e output %q: %v (%v)", raw, err, runErr)
	}
	if runErr != nil || out.Error != "" || !out.Saved {
		t.Fatalf("browser run failed: %s\nmessages %v\nconsole %v\nCSP %v", out.Error, out.Messages, out.Console, out.Violations)
	}
	if len(out.Violations) > 0 {
		t.Fatalf("Content Security Policy violations: %v", out.Violations)
	}
	types := map[string]bool{}
	for _, m := range out.Messages {
		if s, _ := m["type"].(string); s != "" {
			types[s] = true
		}
		if m["type"] == "togavsu:result" && m["error"] != nil {
			t.Fatalf("the firm's details were refused in the editor: %v", m["error"])
		}
	}
	for _, want := range []string{"wordcraft:ready", "wordcraft:dirty", "wordcraft:save", "togavsu:result", "togavsu:pleading"} {
		if !types[want] {
			t.Errorf("the page never saw %s: %v", want, out.Messages)
		}
	}
	vs := e.versions(f.ID)
	if len(vs) != 2 || vs[1].How != "editor" || vs[1].SavedBy != e.staff.UserID {
		t.Fatalf("versions after the browser save = %+v", vs)
	}
	_, _, b := e.current(f.ID)
	if err := CheckDOCX(b); err != nil {
		t.Fatal(err)
	}
	if txt := e.text(b, "docx"); !strings.Contains(txt, "Typed in the browser editor.") || !strings.Contains(txt, "Synthetic browser document.") {
		t.Fatalf("the saved version's text = %q", txt)
	}
	for _, c := range out.Console {
		if strings.Contains(c, "Content Security Policy") {
			t.Fatalf("console: %s", c)
		}
	}
	// The browser asks for /favicon.ico, which the core shell serves; nothing else may fail.
	for _, m := range out.Missing {
		if m != "404 /favicon.ico" {
			t.Fatalf("a request failed: %s (all: %v)", m, out.Missing)
		}
	}
	// New Pleading opened in the browser editor with the matter's caption (PRD 8.20).
	got, _ := json.Marshal(map[string]any{"courtType": out.Pleading["courtType"], "county": out.Pleading["county"], "caseNumber": out.Pleading["caseNumber"],
		"firstSide": out.Pleading["firstSide"], "secondSide": out.Pleading["secondSide"]})
	want := `{"caseNumber":"26AR-DR00123","county":"ADAIR","courtType":"CIRCUIT",` +
		`"firstSide":{"names":[{"client":true,"name":"Jane Roe"}],"role":"Petitioner"},` +
		`"secondSide":{"names":[{"client":false,"name":"John Roe"}],"role":"Respondent"}}`
	if string(got) != want {
		t.Fatalf("New Pleading in the browser opened with %s\nwant %s", got, want)
	}
	t.Logf("browser save: %s; console errors: %v; failed requests: %v", out.State, out.Console, out.Missing)
}

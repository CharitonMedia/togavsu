package editor

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func isRefused(err error) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == CodeCommandRefused
}

func fixtureKnown() map[string]CommandInfo {
	m := map[string]CommandInfo{}
	for _, id := range []string{"file.open", "file.save", "insert.textFromFile", "insert.picture", "text.insert", "mailings.recipients",
		"mailings.finish", "law.pleadings.new", "law.profiles.import", "view.zoom", "tools.autocorrect", "developer.macros", "font.add",
		"insert.link", "insert.onlinePicture", "references.addSource"} {
		m[id] = CommandInfo{ID: id}
	}
	return m
}

func TestRunsRefuseFileSystemAndNetworkCommands(t *testing.T) {
	known := fixtureKnown()
	refusedRuns := map[string][]Command{
		"read a local file":           {{ID: "insert.textFromFile", Parameters: map[string]any{"path": "/etc/hostname"}}},
		"open another file":           {{ID: "file.open", Parameters: map[string]any{"path": "/etc/passwd"}}},
		"save beside the document":    {{ID: "text.insert", Parameters: map[string]any{"text": "x"}}, {ID: "file.save", Parameters: map[string]any{"path": "/tmp/x.docx"}}},
		"recipients from a file":      {{ID: "mailings.recipients", Parameters: map[string]any{"path": "/home/data.csv"}}},
		"merge output to a path":      {{ID: "mailings.finish", Parameters: map[string]any{"path": "/tmp/out.docx"}}},
		"path deep in the parameters": {{ID: "text.insert", Parameters: map[string]any{"text": "x", "opts": []any{map[string]any{"Path": "/etc"}}}}},
		"a picture from disk":         {{ID: "insert.picture", Parameters: map[string]any{"data": "AAAA"}}},
		"import a profile":            {{ID: "law.profiles.import", Parameters: map[string]any{"path": "/x.json"}}},
		"app view":                    {{ID: "view.zoom"}},
		"app options":                 {{ID: "tools.autocorrect"}},
		"macros":                      {{ID: "developer.macros"}},
		"a font file":                 {{ID: "font.add"}},
		"not a LawCraft command":      {{ID: "shell.exec", Parameters: map[string]any{"cmd": "id"}}},
		// An address is refused on any command not reviewed to only store it.
		"an online picture":         {{ID: "insert.onlinePicture", Parameters: map[string]any{"url": "https://example.invalid/a.png"}}},
		"a source looked up online": {{ID: "references.addSource", Parameters: map[string]any{"source": map[string]any{"URI": "https://example.invalid/doi"}}}},
		"an address deep in a list": {{ID: "text.insert", Parameters: map[string]any{"text": "x", "runs": []any{map[string]any{"src": "file:///etc/passwd"}}}}},
		"an href":                   {{ID: "text.insert", Parameters: map[string]any{"href": "http://127.0.0.1/"}}},
	}
	for name, cmds := range refusedRuns {
		if err := checkCommands(known, cmds); !isRefused(err) {
			t.Errorf("%s: checkCommands = %v, want command_refused", name, err)
		}
	}
	ok := []Command{
		{ID: "text.insert", Parameters: map[string]any{"text": "x", "path": []any{1.0, 2.0}, "dir": "left"}},
		{ID: "mailings.recipients", Parameters: map[string]any{"csv": "ClientName\nJane Roe"}},
		{ID: "mailings.finish"},
		{ID: "law.pleadings.new", Parameters: map[string]any{"type": "motion"}},
		// A hyperlink's target is written into the document, never fetched.
		{ID: "insert.link", Parameters: map[string]any{"url": "https://example.invalid/", "text": "the court's site"}},
		{ID: "references.addSource", Parameters: map[string]any{"source": map[string]any{"title": "Synthetic", "url": 12.0}}},
	}
	if err := checkCommands(known, ok); err != nil {
		t.Fatalf("an allowed run was refused: %v", err)
	}
	if err := checkCommands(known, nil); err == nil || isRefused(err) {
		t.Fatalf("an empty run = %v, want a validation error", err)
	}
	for name, cmds := range refusedRuns {
		if err := checkCommands(known, cmds); err != nil && !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: the refusal does not say so: %v", name, err)
		}
	}
	if Status(refused("x")) != 422 {
		t.Fatalf("command_refused status = %d", Status(refused("x")))
	}
}

// reviewedFileParams are allowed commands whose parameters can name a file. Each is safe
// because pathParam refuses the run when such a parameter is given. A new one at a moved
// pin fails this test until it is reviewed.
var reviewedFileParams = map[string]bool{
	"mailings.recipients": true, // "path" refused; "csv" and "data" carry the bytes inline
	"mailings.finish":     true, // "path" refused; without it the merge stays in the run
	"equation.edit":       true, // "path" is an array position inside the equation, not a file
	"document.paragraph":  true, // "path" is an array block path inside the document, not a file
	"shape.connect":       true, // "path" is an array block path inside the document, not a file
	"equation.move":       true, // "dir" is a direction
	"shape.textDirection": true, // "dir" is a text direction
	"view.switchWindows":  true, // denied family; listed for completeness
	"picture.size":        true, // "original" is a bool (restore the picture's own size), never text
}

// TestPinnedCommandListIsFiltered reads the command list from wordcraft-cli at the pin.
func TestPinnedCommandListIsFiltered(t *testing.T) {
	cli := testCLI(t)
	s := &Service{cli: cli}
	all, err := cli.CommandList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range all {
		ids[c.ID] = true
	}
	for id := range deniedCommands {
		if !ids[id] {
			t.Errorf("denied command %s is not a command at the pinned commit: review the list", id)
		}
	}
	allowed, err := s.Commands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range allowed {
		got[c.ID] = true
		if c.ParametersSchema["type"] != "object" {
			t.Errorf("%s: parameters_schema %v", c.ID, c.ParametersSchema)
		}
	}
	for _, want := range []string{"law.pleadings.new", "law.profiles.apply", "mailings.recipients", "mailings.finish", "text.insert", "edit.replaceAll", "fields.lock", "law.toa.build"} {
		if !got[want] {
			t.Errorf("%s should be an allowed editor command", want)
		}
	}
	for id := range got {
		for _, f := range deniedFamilies {
			if strings.HasPrefix(id, f) {
				t.Errorf("%s is listed but its family is refused", id)
			}
		}
		if _, denied := deniedCommands[id]; denied {
			t.Errorf("%s is listed but refused", id)
		}
	}
	fileish := regexp.MustCompile(`"(path|dir|folder|file|template|output|import|original)"`)
	urlish := regexp.MustCompile(`(?i)"(url|uri|href|src)"`)
	byID := map[string]CommandInfo{}
	for _, c := range all {
		byID[c.ID] = c
	}
	for id := range got {
		p := byID[id].Params
		if fileish.MatchString(p) && !reviewedFileParams[id] {
			t.Errorf("allowed command %s takes a file-like parameter; review it: %s", id, p)
		}
		// A command that takes an address fails this test until it is reviewed: listed in
		// urlCommands (it only stores the address) or refused (deniedCommands). Either way a
		// run giving it one is refused until then.
		if urlish.MatchString(p) && urlCommands[id] == "" {
			t.Errorf("allowed command %s takes an address; review it into urlCommands or deniedCommands: %s", id, p)
			if err := checkCommands(byID, []Command{{ID: id, Parameters: map[string]any{"url": "https://example.invalid/"}}}); !isRefused(err) {
				t.Errorf("unreviewed command %s was not refused an address: %v", id, err)
			}
		}
	}
	for id := range urlCommands {
		if !got[id] {
			t.Errorf("urlCommands names %s, which is not an allowed command at the pin", id)
		} else if !urlish.MatchString(byID[id].Params) {
			t.Errorf("urlCommands names %s, which takes no address at the pin: review it", id)
		}
	}
	if len(got) < 300 || len(got) >= len(all) {
		t.Fatalf("allowed %d of %d commands", len(got), len(all))
	}
}

// testCLI is the wordcraft-cli built from the pinned commit (web/editor/build.sh). Tests
// that need it skip without it, unless TOGAVSU_REQUIRE_WORDCRAFT_CLI is set (the editor
// CI job), where a missing binary is a failure.
func testCLI(t *testing.T) *CLI {
	t.Helper()
	path := os.Getenv("TOGAVSU_WORDCRAFT_CLI")
	if path == "" {
		if os.Getenv("TOGAVSU_REQUIRE_WORDCRAFT_CLI") != "" {
			t.Fatal("TOGAVSU_WORDCRAFT_CLI is required: build it with web/editor/build.sh")
		}
		t.Skip("TOGAVSU_WORDCRAFT_CLI is not set (build wordcraft-cli with web/editor/build.sh)")
	}
	return &CLI{Path: path, TempDir: t.TempDir()}
}

// A court profile body that names a file is refused before law.profiles.validate, which
// reads a profile file when given {"path": …}, could see it.
func TestProfileBodyCannotNameAFile(t *testing.T) {
	// No wordcraft-cli: reaching it would be a different error than the refusal.
	s := &Service{cli: &CLI{}}
	for _, key := range []string{"path", "Path", "file", "url"} {
		body := map[string]any{"schema": 1.0, "id": "synthetic", "name": "Synthetic", key: "/etc/hostname"}
		_, err := s.validateProfile(context.Background(), body)
		var api *domain.APIError
		if !errors.As(err, &api) || api.Code != domain.CodeValidation || api.Field != "body."+key {
			t.Errorf("a profile body with %q = %v, want a validation error on body.%s", key, err, key)
		}
	}
	if _, err := s.validateProfile(context.Background(), map[string]any{"schema": 1.0, "id": "synthetic"}); err == nil || strings.Contains(err.Error(), "cannot name a file") {
		t.Fatalf("a body without a file was refused as naming one, or reached no runner: %v", err)
	}
}

package editor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CommandInfo is one entry of `wordcraft-cli commands --json`.
type CommandInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Location string `json:"location"`
	Mutates  bool   `json:"mutates"`
	Params   string `json:"params"`
}

// Command is one step of an editor run: a LawCraft command id and its parameters.
type Command struct {
	ID         string         `json:"id"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// EditorCommand is the API's listing of an allowed command.
type EditorCommand struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	ParametersSchema map[string]any `json:"parameters_schema"`
}

// CodeCommandRefused is the API error for a run that names a command reaching the file
// system or the network, or one that is not an allowed editor command.
const CodeCommandRefused = "command_refused"

func refused(format string, args ...any) *domain.APIError {
	return &domain.APIError{Code: CodeCommandRefused, Field: "commands", Message: fmt.Sprintf(format, args...)}
}

// deniedFamilies are whole command families that act outside the document: files and
// windows (file.*), the app's view and options (view.*, tools.*), macros and the control
// channel (developer.*), speech (readAloud.*), embedded objects read from disk (object.*)
// and link targets (link.*). Togavsu's own editor commands (togavsu.*, PleadingDefaults)
// are for the browser editor's window, not the document, and never reach LawCraft.
var deniedFamilies = []string{"file.", "view.", "tools.", "developer.", "readAloud.", "object.", "link.", "togavsu."}

// deniedCommands read or write a file or a folder by path, or change a store outside the
// document (court profiles, saved courts and firm details are Togavsu records, written
// through the court-profiles API and settings, never from a run).
var deniedCommands = map[string]string{
	"insert.picture":          "reads a picture file",
	"insert.textFromFile":     "reads a local file",
	"insert.object":           "reads a local file",
	"insert.quickParts":       "writes the building blocks store",
	"picture.change":          "reads a picture file",
	"font.add":                "reads a font file",
	"review.compare":          "reads another document",
	"review.combine":          "reads another document",
	"references.sources":      "imports a bibliography file",
	"styles.organizer":        "reads another document's styles",
	"styles.copyFrom":         "reads another document's styles",
	"mailings.printDocuments": "prints",
	"law.profiles.import":     "reads a profile file",
	"law.profiles.export":     "writes a profile file",
	"law.profiles.validate":   "can read a profile file",
	"law.profiles.create":     "writes the profile store; use POST /api/v1/court-profiles",
	"law.profiles.update":     "writes the profile store; use PATCH /api/v1/court-profiles/{id}",
	"law.profiles.approve":    "only a person verifies a court profile",
	"law.contacts.read":       "reads a local file",
	"law.courts.save":         "writes the saved courts file",
	"law.courts.delete":       "writes the saved courts file",
	"law.firm.set":            "writes the firm file; firm details come from firm settings",
}

// pathKeys are parameter names that name a file or folder when their value is text. A
// command that is otherwise allowed (mail merge recipients, Finish & Merge) is refused
// when one is given, at any depth. A block path inside the document (document.paragraph,
// equation.edit, shape.connect) is an array, and "dir" is a direction, so neither is one.
var pathKeys = map[string]bool{"path": true, "folder": true, "file": true, "import": true, "original": true, "template": true, "output": true}

// urlKeys are parameter names that carry a web or file address when their value is text.
var urlKeys = map[string]bool{"url": true, "uri": true, "href": true, "src": true}

// urlCommands are the allowed commands reviewed at the pin that take an address in a
// urlKeys parameter, and why that is safe: each only writes the address into the
// document, and none fetches it. Any other command given an address is refused, so a
// command that would read the network (an online picture, a source lookup) never runs
// with one. TestPinnedCommandListIsFiltered fails on an allowed command that takes an
// address and is not listed here.
var urlCommands = map[string]string{
	"insert.link": "stores the address as the hyperlink's target (engine insert/links.rs); nothing is fetched",
}

// allowedCommand says whether id may run headless, and why not.
func allowedCommand(id string) (bool, string) {
	for _, f := range deniedFamilies {
		if strings.HasPrefix(id, f) {
			return false, "acts outside the document (" + strings.TrimSuffix(f, ".") + " commands)"
		}
	}
	if why, ok := deniedCommands[id]; ok {
		return false, why
	}
	return true, ""
}

// pathParam finds a parameter naming a file or folder.
func pathParam(v any) (string, bool) { return textParam(v, pathKeys) }

// urlParam finds a parameter carrying an address.
func urlParam(v any) (string, bool) { return textParam(v, urlKeys) }

// textParam finds, at any depth, a parameter whose name is one of keys (in any case) and
// whose value is text.
func textParam(v any, keys map[string]bool) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		names := make([]string, 0, len(t))
		for k := range t {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if _, text := t[k].(string); text && keys[strings.ToLower(k)] {
				return k, true
			}
			if k2, ok := textParam(t[k], keys); ok {
				return k2, true
			}
		}
	case []any:
		for _, e := range t {
			if k, ok := textParam(e, keys); ok {
				return k, true
			}
		}
	}
	return "", false
}

// maxRunCommands bounds one run.
const maxRunCommands = 1 << 8

// checkCommands refuses the whole run when any command is not allowed. Nothing runs
// unless every step passes.
func checkCommands(known map[string]CommandInfo, cmds []Command) error {
	if len(cmds) == 0 {
		return domain.Invalid("commands", "name at least one command")
	}
	if len(cmds) > maxRunCommands {
		return domain.Invalid("commands", fmt.Sprintf("at most %d commands in one run", maxRunCommands))
	}
	for i, c := range cmds {
		if _, ok := known[c.ID]; !ok {
			return refused("command %d (%s) is not a LawCraft command at commit %s", i+1, c.ID, Commit)
		}
		if ok, why := allowedCommand(c.ID); !ok {
			return refused("command %d (%s) is refused: it %s", i+1, c.ID, why)
		}
		if k, ok := pathParam(map[string]any(c.Parameters)); ok {
			return refused("command %d (%s) is refused: parameter %q names a file or folder, and runs never read or write the file system", i+1, c.ID, k)
		}
		if k, ok := urlParam(map[string]any(c.Parameters)); ok && urlCommands[c.ID] == "" {
			return refused("command %d (%s) is refused: parameter %q is an address, and runs never reach the network", i+1, c.ID, k)
		}
	}
	return nil
}

// Commands lists the commands an editor run may use (GET /api/v1/editor/commands).
func (s *Service) Commands(ctx context.Context) ([]EditorCommand, error) {
	all, err := s.cli.CommandList(ctx)
	if err != nil {
		return nil, err
	}
	out := []EditorCommand{}
	for _, c := range all {
		if ok, _ := allowedCommand(c.ID); !ok {
			continue
		}
		schema := map[string]any{"type": "object", "additionalProperties": true}
		if c.Params != "" {
			schema["description"] = c.Params
		}
		out = append(out, EditorCommand{ID: c.ID, Title: c.Label, ParametersSchema: schema})
	}
	return out, nil
}

func (s *Service) knownCommands(ctx context.Context) (map[string]CommandInfo, error) {
	all, err := s.cli.CommandList(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]CommandInfo, len(all))
	for _, c := range all {
		m[c.ID] = c
	}
	return m, nil
}

package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The edit page sends each open command as its own job: the editor stops a job at its
// first failing command, so a court profile it refuses must not drop the matter's court
// or the New Pleading defaults that follow it.
func TestEditPageSendsEachOpenCommandAsItsOwnJob(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	ids := []string{"law.firm.set", "law.profiles.create", "law.profiles.approve", "law.courts.save", "togavsu.pleadings.defaults"}
	var cmds []Command
	for _, id := range ids {
		cmds = append(cmds, Command{ID: id, Parameters: map[string]any{"x": id}})
	}
	ctx, err := json.Marshal(map[string]any{"commands": cmds})
	if err != nil {
		t.Fatal(err)
	}
	ctxFile := filepath.Join(t.TempDir(), "context.json")
	if err := os.WriteFile(ctxFile, ctx, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "../../web/editor/e2e/open-commands.mjs", "../../web/templates/editor/edit.js", ctxFile).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var got struct {
		Posted []struct {
			Type     string    `json:"type"`
			Commands []Command `json:"commands"`
		} `json:"posted"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	var runs []string
	for _, m := range got.Posted {
		if m.Type != "togavsu:run" {
			continue
		}
		if len(m.Commands) != 1 {
			t.Fatalf("a job carries %d commands: %+v", len(m.Commands), m.Commands)
		}
		runs = append(runs, m.Commands[0].ID)
	}
	if strings.Join(runs, ",") != strings.Join(ids, ",") {
		t.Fatalf("jobs = %v, want one per command in order %v", runs, ids)
	}
	if !strings.Contains(got.Message, "law.profiles.create: refused") {
		t.Fatalf("a refused command is not shown: %q", got.Message)
	}
}

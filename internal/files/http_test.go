package files

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
)

// pendingOperations are files-family contract operations this package does not serve.
// Each belongs to another task or a later integration step; see the PR body.
var pendingOperations = map[string]string{
	"bulkFile":                  "bulk endpoints: api task",
	"reviewUnassignedFile":      "Doc Tender matter match review: doc-tender task",
	"issueFilePublicationGrant": "background publication grants: modules task",
	"acquireEditLock":           "editor task",
	"releaseEditLock":           "editor task",
	"renewEditLock":             "editor task",
	"getEditor":                 "editor task",
	"listEditorCommands":        "editor task",
	"runEditor":                 "editor task",
	"convertFile":               "editor task",
	"getDocTenderResult":        "doc-tender task",
}

func filesFamily(path string) bool {
	for _, p := range []string{"/api/v1/files", "/api/v1/file-uploads", "/api/v1/file-archives", "/api/v1/folders", "/api/v1/storage-backends", "/api/v1/storage-moves", "/api/v1/editor"} {
		if path == p || strings.HasPrefix(path, p+"/") || strings.HasPrefix(path, p+":") {
			return true
		}
	}
	return false
}

// TestRoutesServeTheFilesContract checks every files-family operation in the published
// MCP tool list is served by Routes, so /mcp (which dispatches through these
// handlers) reaches each one once the core mounts this mux.
func TestRoutesServeTheFilesContract(t *testing.T) {
	var doc struct {
		Tools []struct {
			Name   string `json:"name"`
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(api.MCPTools, &doc); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(&Service{}, os.DirFS("../../web/templates/files"), nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := h.Routes()
	var served, pending []string
	for _, tool := range doc.Tools {
		if !filesFamily(tool.Path) {
			continue
		}
		path := tool.Path
		for strings.Contains(path, "{") {
			a := strings.Index(path, "{")
			b := strings.Index(path, "}")
			path = path[:a] + "0190f0c2-7f3e-7c1a-8b2d-1f2e3d4c5b6a" + path[b+1:]
		}
		r, _ := http.NewRequest(tool.Method, path, nil)
		_, pattern := mux.Handler(r)
		if why, ok := pendingOperations[tool.Name]; ok {
			if pattern != "" {
				t.Errorf("%s is served but listed pending (%s)", tool.Name, why)
			}
			pending = append(pending, tool.Name)
			continue
		}
		if pattern == "" {
			t.Errorf("%s %s (%s) is not served", tool.Method, tool.Path, tool.Name)
			continue
		}
		served = append(served, tool.Name)
	}
	sort.Strings(served)
	if len(served) < 30 {
		t.Fatalf("only %d files operations served: %v", len(served), served)
	}
	if len(pending) != len(pendingOperations) {
		t.Fatalf("pending list names operations the contract does not have: %v", pending)
	}
	t.Logf("served %d operations, pending %d", len(served), len(pending))
}

func TestHumanBytes(t *testing.T) {
	for in, want := range map[int64]string{512: "512 B", 8 << 20: "8 MB", 2 << 30: "2 GB", 1536: "1.5 KB"} {
		if got := humanBytes(in); got != want {
			t.Fatalf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewableTypes(t *testing.T) {
	for in, want := range map[string]bool{
		"application/pdf": true, "image/jpeg": true, "video/mp4": true, "audio/mpeg": true, "text/plain; charset=utf-8": true,
		"text/html": false, "image/svg+xml": false, "application/octet-stream": false, "": false, "application/xhtml+xml": false,
	} {
		if got := previewable(in); got != want {
			t.Fatalf("previewable(%q) = %v", in, got)
		}
	}
}

package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
)

// emittedEvents is what each served files operation writes to core.events today,
// limited to the names the contract gives it.
var emittedEvents = map[string][]string{
	"publishFile":               {"file.created", "file.version_created"},
	"bulkFile":                  {"file.created", "file.version_created"},
	"issueFilePublicationGrant": {"file.background_authorized"},
	"updateFileMetadata":        {"file.updated"},
	"renameFile":                {"file.updated"},
	"moveFile":                  {"file.updated"},
	"markFileStatus":            {"file.updated"},
	"restoreFile":               {"file.updated"},
	"purgeFile":                 {"file.purged"},
	"addFileVersion":            {"file.version_created"},
	"restoreFileVersion":        {"file.version_created"},
	"downloadFileVersion":       {"file.downloaded"},
	"downloadFileRendition":     {"file.downloaded"},
	"createFileArchive":         {"file.downloaded"},
}

// offContractEvents is what a served operation writes that the contract does not name
// for it. Each one is part of the proposed shared change below.
var offContractEvents = map[string][]string{
	"trashFile": {"file.updated"},
}

// pendingEvents lists the contract events the files service does not write yet. The
// event catalogue and its webhook fan-out belong to the integration lead; the PR's
// "Proposed shared changes" asks whether to write these names here or narrow the
// contract. Until then they are disclosed, not silently missing.
var pendingEvents = map[string][]string{
	"trashFile":             {"file.trashed"},
	"createFileLink":        {"file.linked"},
	"quarantineFileVersion": {"file.quarantine_changed"},
	"createFileUpload":      {"file_upload.created"},
	"completeFileUpload":    {"file_upload.staged"},
	"createFolder":          {"folder.created"},
	"updateFolder":          {"folder.updated"},
	"deleteFolder":          {"folder.deleted"},
	"createStorageBackend":  {"storage_backend.created"},
	"updateStorageBackend":  {"storage_backend.updated"},
	"testStorageBackend":    {"storage_backend.tested"},
	"createStorageMove":     {"storage_move.started"},
	"confirmStorageMove":    {"storage_move.confirmed"},
}

// TestFilesEventsFollowTheContract maps every served files operation to the events the
// OpenAPI contract declares for it: each one is written or listed pending, nothing
// pending is also written, and every name claimed as written is in the service code.
func TestFilesEventsFollowTheContract(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	contract := map[string][]string{}
	for path, ops := range doc.Paths {
		if !filesFamily(path) {
			continue
		}
		for _, raw := range ops {
			var op struct {
				ID     string   `json:"operationId"`
				Events []string `json:"x-togavsu-events"`
			}
			if json.Unmarshal(raw, &op) != nil || op.ID == "" {
				continue
			}
			if _, later := pendingOperations[op.ID]; !later {
				contract[op.ID] = op.Events
			}
		}
	}
	if len(contract) < 30 {
		t.Fatalf("only %d served files operations in the contract", len(contract))
	}
	src := productionSource(t)
	for _, m := range []map[string][]string{emittedEvents, offContractEvents, pendingEvents} {
		for op := range m {
			if _, ok := contract[op]; !ok {
				t.Errorf("%s is not a served files operation of the contract", op)
			}
		}
	}
	for op, events := range contract {
		for _, ev := range events {
			written, pending := has(emittedEvents[op], ev), has(pendingEvents[op], ev)
			switch {
			case written && pending:
				t.Errorf("%s: %s is listed both written and pending", op, ev)
			case !written && !pending:
				t.Errorf("%s: the contract's %s is neither written nor listed pending", op, ev)
			}
		}
		for _, ev := range append(append([]string{}, emittedEvents[op]...), offContractEvents[op]...) {
			if !strings.Contains(src, `"`+ev+`"`) {
				t.Errorf("%s: %s is claimed as written but the service never names it", op, ev)
			}
		}
		for _, ev := range emittedEvents[op] {
			if !has(events, ev) {
				t.Errorf("%s: %s is not a contract event of this operation; list it off-contract", op, ev)
			}
		}
		for _, ev := range pendingEvents[op] {
			if !has(events, ev) {
				t.Errorf("%s: pending %s is not a contract event of this operation", op, ev)
			}
		}
	}
	var names []string
	for op, evs := range pendingEvents {
		for _, ev := range evs {
			names = append(names, op+" "+ev)
		}
	}
	sort.Strings(names)
	t.Logf("contract events pending: %d", len(names))
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func productionSource(t *testing.T) string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
	}
	return b.String()
}

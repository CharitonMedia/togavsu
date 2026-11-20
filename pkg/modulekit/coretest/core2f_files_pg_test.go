package coretest_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func stage2f(t *testing.T, cl *coretest.Client, matter, name string, headers ...string) string {
	t.Helper()
	data := []byte("Synthetic agreement body for public kit acceptance.\n")
	up := cl.Do("POST", "/api/v1/file-uploads", map[string]any{"file_name": name, "matter_id": matter, "size_bytes": len(data), "media_type": "text/plain"}, headers...)
	if up.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", up.Code, up.Body)
	}
	id := str(t, up.JSON, "id")
	chunkHeaders := append([]string{"Content-Type", "application/octet-stream", "Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data))}, headers...)
	r := cl.Raw("PUT", "/api/v1/file-uploads/"+id, data, chunkHeaders...)
	if r.Code != http.StatusOK {
		t.Fatalf("stage: %d %s", r.Code, r.Body)
	}
	if completed := cl.Do("POST", "/api/v1/file-uploads/"+id+"/complete", map[string]any{}, headers...); completed.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", completed.Code, completed.Body)
	}
	return id
}

func TestCoreUpdate2FFileTrashDelivery(t *testing.T) {
	c := coretest.New(t)
	var mu sync.Mutex
	events := map[string][]map[string]any{}
	for _, id := range []string{"files-listener-a", "files-listener-b", "files-no-scope"} {
		id := id
		c.Serve(id, func(m coretest.Module) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sender, b, err := coretest.ReadDelivery(m, r)
				if err != nil || sender != "core" {
					t.Errorf("delivery sender %q: %v", sender, err)
				}
				var event map[string]any
				if err := json.Unmarshal(b, &event); err != nil {
					t.Error(err)
				}
				mu.Lock()
				events[id] = append(events[id], event)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			})
		})
		scopes := []string{"files:read"}
		if id == "files-no-scope" {
			scopes = nil
		}
		install2d(t, c, id, scopes, "")
	}
	up := stage2f(t, c.Admin.Client, c.Open, "trash.txt")
	f := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up})
	id := str(t, f.JSON, "id")
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/trash", map[string]any{})
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/trash", map[string]any{})
	c.DeliverEvents()
	mu.Lock()
	defer mu.Unlock()
	for _, module := range []string{"files-listener-a", "files-listener-b", "files-no-scope"} {
		n := 0
		for _, event := range events[module] {
			if event["action"] == "file.trashed" {
				n++
				if event["record_id"] != id || event["matter_id"] != c.Open {
					t.Errorf("event: %v", event)
				}
			}
		}
		want := 1
		if module == "files-no-scope" {
			want = 0
		}
		if n != want {
			t.Errorf("%s trash deliveries %d want %d", module, n, want)
		}
	}
}

func TestCoreUpdate2FPublicationAuthorAndStatus(t *testing.T) {
	c := coretest.New(t)
	const module = "files-author"
	c.Handle(module, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	install2d(t, c, module, []string{"files:read", "files:write"}, "")
	bot := c.ModuleClient(module)
	up := stage2f(t, bot, c.Open, "draft.txt")
	body := map[string]any{"items": []any{map[string]any{"upload_id": up, "status": "draft", "on_behalf_of_user_id": c.Attorney.ID}}}
	r := bot.Must(http.StatusCreated, "POST", "/api/v1/files:bulk", body)
	if !strings.Contains(r.Body, `"status":"draft"`) || !strings.Contains(r.Body, `"created_by":"`+c.Attorney.ID+`"`) || !strings.Contains(r.Body, `"source":"`+module+`"`) {
		t.Fatalf("publication attribution: %s", r.Body)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(r.Body), &result); err != nil {
		t.Fatal(err)
	}
	id := ""
	if len(result.Data) > 0 {
		id = result.Data[0].ID
	}
	if len(result.Items) > 0 {
		id = result.Items[0].ID
	}
	if id == "" {
		t.Fatalf("missing file in %s", r.Body)
	}
	bot.Must(http.StatusOK, "POST", "/api/v1/files/"+id+"/status", map[string]any{"status": "final"})
	if c.Count("SELECT count(*) FROM core.file_versions WHERE file_id = $1 AND saved_by = $2", id, c.Attorney.ID) != 1 {
		t.Fatal("version author not retained")
	}
	if c.Count("SELECT count(*) FROM core.audit_log WHERE entity_id = $1 AND source = 'service' AND reason LIKE $2", id, "%module="+module+"%") == 0 {
		t.Fatal("module/source attribution missing")
	}
	// A revoked bearer cannot use the install's still-approved files:write scope.
	token := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "narrow files bearer", "module_id": module, "scopes": []string{"files:read"}, "approved_scopes": []string{"files:read", "files:write"}})
	denied := c.NewClient()
	denied.Bearer = str(t, token.JSON, "secret")
	if got := denied.Do("POST", "/api/v1/files/"+id+"/status", map[string]any{"status": "draft"}); got.Code != http.StatusForbidden {
		t.Fatalf("missing write scope: %d %s", got.Code, got.Body)
	}
	for _, author := range []string{c.Client.ID, c.Accountant.ID, "00000000-0000-0000-0000-000000000000"} {
		up := stage2f(t, bot, c.Open, "refused-author.txt")
		got := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "status": "draft", "on_behalf_of_user_id": author})
		if got.Code < 400 {
			t.Fatalf("invalid author %s: %d %s", author, got.Code, got.Body)
		}
	}
	// The same publication metadata and status path work through generated MCP tools.
	mcpUp := stage2f(t, bot, c.Open, "mcp-draft.txt")
	mcpPub := toolResult2d(t, bot, "bulkFile", map[string]any{"body": map[string]any{"items": []any{map[string]any{"upload_id": mcpUp, "status": "draft", "on_behalf_of_user_id": c.Attorney.ID}}}}, false)
	data, ok := mcpPub["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("MCP publication: %v", mcpPub)
	}
	mcpFile, ok := data[0].(map[string]any)
	if !ok || mcpFile["created_by"] != c.Attorney.ID || mcpFile["status"] != "draft" || mcpFile["source"] != module {
		t.Fatalf("MCP publication author/status: %v", data)
	}
	mcpID := str(t, mcpFile, "id")
	args := map[string]any{"path": map[string]any{"file_id": mcpID}, "body": map[string]any{"status": "final"}}
	if marked := toolResult2d(t, bot, "markFileStatus", args, false); marked["status"] != "final" {
		t.Fatalf("MCP status: %v", marked)
	}
	if refused := toolResult2d(t, denied, "markFileStatus", args, true); refused["code"] != "scope_denied" {
		t.Fatalf("MCP write scope: %v", refused)
	}
	// A bad author in the second bulk item rolls back the first file/version and bytes.
	first := stage2f(t, bot, c.Open, "rollback-first.txt")
	second := stage2f(t, bot, c.Open, "rollback-second.txt")
	beforeFiles := c.Count("SELECT count(*) FROM core.files")
	rejected := bot.Do("POST", "/api/v1/files:bulk", map[string]any{"items": []any{map[string]any{"upload_id": first, "status": "draft", "on_behalf_of_user_id": c.Attorney.ID}, map[string]any{"upload_id": second, "on_behalf_of_user_id": c.Client.ID}}})
	if rejected.Code < 400 || c.Count("SELECT count(*) FROM core.files") != beforeFiles {
		t.Fatalf("batch author validation was not atomic: %d %s", rejected.Code, rejected.Body)
	}
	for _, upload := range []string{first, second} {
		if state := bot.Do("GET", "/api/v1/file-uploads/"+upload, nil); state.Code != http.StatusOK || state.JSON["status"] != "staged" {
			t.Fatalf("rollback upload %s: %d %s", upload, state.Code, state.Body)
		}
	}
	// A viewer inside the wall cannot name an outside-wall publication author.
	contextToken := c.ContextFor(coretest.Attorney, module, c.Walled)
	wallUp := stage2f(t, bot, c.Walled, "refused-wall-author.txt", coretest.ContextHeader, contextToken)
	if got := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": wallUp, "on_behalf_of_user_id": c.Staff.ID}, coretest.ContextHeader, contextToken); got.Code < 400 {
		t.Fatalf("outside-wall author accepted: %s", got.Body)
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/users/"+c.Staff.ID+"/deactivate", nil)
	inactiveUp := stage2f(t, bot, c.Open, "inactive-author.txt")
	if got := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": inactiveUp, "on_behalf_of_user_id": c.Staff.ID}); got.Code < 400 {
		t.Fatalf("inactive author accepted: %s", got.Body)
	}
	up = stage2f(t, bot, c.Open, "refused-status.txt")
	if got := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "status": "invented"}); got.Code < 400 {
		t.Fatalf("invalid status accepted: %s", got.Body)
	}
	// The on-behalf author never supplies access to the service principal.
	if got := bot.Do("POST", "/api/v1/files", map[string]any{"upload_id": up, "matter_id": c.Walled, "on_behalf_of_user_id": c.Attorney.ID}); got.Code != http.StatusNotFound {
		t.Fatalf("wall bypass: %d %s", got.Code, got.Body)
	}
	// Walling the matter after publication also blocks the service's own status path.
	c.Wall(c.Open, c.Attorney)
	if got := bot.Do("POST", "/api/v1/files/"+id+"/status", map[string]any{"status": "draft"}); got.Code != http.StatusNotFound {
		t.Fatalf("status crossed wall: %d %s", got.Code, got.Body)
	}
	if got := c.Attorney.Must(http.StatusOK, "GET", "/api/v1/files/"+id, nil); got.JSON["status"] != "final" {
		t.Fatalf("denied status changed file: %s", got.Body)
	}

}

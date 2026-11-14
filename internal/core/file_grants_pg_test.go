package core

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// intakeManifest is a mail intake module: it writes files in the background and hears
// about file events.
func intakeManifest(id string) string {
	return `id: ` + id + `
name: Intake ` + id + `
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/` + id + `
  digest: sha256:5555555555555555555555555555555555555555555555555555555555555555
  port: 8080
permissions:
  - scope: files:read
  - scope: files:write
  - scope: events:read
events:
  - file.*
`
}

// TestFileGrantsBulkAndUnassignedReview is AC-002's background file publication on the
// running core with real module tokens: issueFilePublicationGrant, createFileUpload with
// no matter, bulkFile and reviewUnassignedFile over HTTP and /mcp; a staff session and a
// walled matter get no grant; another module neither reviews the file nor receives the
// grant, which is delivered only to its module on file.background_authorized.
func TestFileGrantsBulkAndUnassignedReview(t *testing.T) {
	x := newExt(t)
	d := x.docs
	ctx := context.Background()
	for _, name := range []string{"issueFilePublicationGrant", "bulkFile", "reviewUnassignedFile"} {
		found := false
		for _, tl := range x.app.tools {
			if tl.Name == name {
				found = x.app.mounted(tl)
			}
		}
		if !found {
			t.Fatalf("%s is not mounted", name)
		}
	}
	matter, _ := d.matter("Example Intake", "", nil)
	walled, _ := d.matter("Sealed Intake", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	mods := map[string]*client{}
	for _, id := range []string{"mail-intake", "other-intake"} {
		x.rt.Handle(id, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
		if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(intakeManifest(id)))); problem != "" {
			t.Fatalf("install %s: %s", id, problem)
		}
		x.approveOnTab(id)
		c := newClient(t, d.srv.URL)
		c.bearer = x.rt.Last(id).Secrets["togavsu_api_token"]
		mods[id] = c
	}
	mod, other := mods["mail-intake"], mods["other-intake"]

	if r := d.staff.do("POST", "/api/v1/files/publication-grants", map[string]any{"assignment": "unassigned", "source": "email"}); r.code != http.StatusForbidden {
		t.Fatalf("a staff session's grant: %d %s", r.code, r.body)
	}
	if r := mod.do("POST", "/api/v1/files/publication-grants", map[string]any{"assignment": "matter", "matter_id": walled, "source": "email"}); r.code != http.StatusNotFound {
		t.Fatalf("a walled matter grant: %d %s", r.code, r.body)
	}
	if r := mod.do("POST", "/api/v1/file-uploads", map[string]any{"file_name": "x.eml", "size_bytes": 1}); r.code != http.StatusForbidden {
		t.Fatalf("an ungranted unassigned upload: %d %s", r.code, r.body)
	}
	g := mod.must(http.StatusCreated, "POST", "/api/v1/files/publication-grants", map[string]any{"assignment": "unassigned", "source": "email", "queue": "inbox"}).json
	if g["module_id"] != "mail-intake" || g["matter_id"] != nil || g["issued_by"] != "core" || g["revoked"] != false {
		t.Fatalf("grant: %v", g)
	}
	mg := mod.must(http.StatusCreated, "POST", "/api/v1/files/publication-grants", map[string]any{"assignment": "matter", "matter_id": matter, "source": "email"}).json
	if mg["matter_id"] != matter {
		t.Fatalf("matter grant: %v", mg)
	}

	// Two staged uploads, one unassigned and one on the matter, published in one batch.
	stage := func(body map[string]any, data string) string {
		body["size_bytes"] = len(data)
		up := mod.must(http.StatusCreated, "POST", "/api/v1/file-uploads", body).json
		if up["owner"].(map[string]any)["owner_module_id"] != "mail-intake" {
			t.Fatalf("upload owner: %v", up)
		}
		id := str(t, up, "id")
		mod.raw("PUT", "/api/v1/file-uploads/"+id, []byte(data), "Content-Type", "application/octet-stream", "Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)))
		mod.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+id+"/complete", map[string]any{})
		return id
	}
	loose := stage(map[string]any{"file_name": "message.eml", "media_type": "message/rfc822"}, "From: someone@example.invalid")
	filed := stage(map[string]any{"file_name": "attachment.txt", "matter_id": matter, "media_type": "text/plain"}, "attachment text")
	if r := mod.do("POST", "/api/v1/files:bulk", map[string]any{"items": []any{map[string]any{"upload_id": loose}, map[string]any{"upload_id": loose}}}); r.code != http.StatusUnprocessableEntity || r.json["code"] != "batch_rejected" {
		t.Fatalf("a repeated upload id: %d %s", r.code, r.body)
	}
	if r := mod.do("POST", "/api/v1/files:bulk", map[string]any{"items": []any{map[string]any{"upload_id": loose, "matter_id": matter}}}); r.code != http.StatusForbidden {
		t.Fatalf("a module gave an unassigned upload a matter: %d %s", r.code, r.body)
	}
	pub := mod.must(http.StatusCreated, "POST", "/api/v1/files:bulk", map[string]any{"items": []any{map[string]any{"upload_id": loose}, map[string]any{"upload_id": filed}}})
	rows := items(t, pub)
	if len(rows) != 2 || rows[0]["matter_id"] != nil || rows[1]["matter_id"] != matter || rows[0]["source"] != "mail-intake" {
		t.Fatalf("bulk: %s", pub.body)
	}
	looseFile := str(t, rows[0], "id")
	if n := x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'file.created' AND record_id IN ($1, $2)`, looseFile, rows[1]["id"]); n != 2 {
		t.Fatalf("file.created events: %d", n)
	}
	// Staff see the unassigned file for the later review; the module reviews its own.
	d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+looseFile, nil)
	if r := mod.must(http.StatusOK, "GET", "/api/v1/files/unassigned/"+loose, nil); r.json["id"] != looseFile || r.json["matter_id"] != nil {
		t.Fatalf("review: %s", r.body)
	}
	if r := other.do("GET", "/api/v1/files/unassigned/"+loose, nil); r.code != http.StatusNotFound {
		t.Fatalf("another module's review: %d %s", r.code, r.body)
	}
	if r := mod.do("GET", "/api/v1/files/unassigned/"+filed, nil); r.code != http.StatusNotFound {
		t.Fatalf("review of an upload that has a matter: %d %s", r.code, r.body)
	}
	// The same operation on /mcp, with the same answer and the same refusal.
	call := mcpCall(mod, "reviewUnassignedFile", map[string]any{"path": map[string]any{"upload_id": loose}})
	res, _ := call.json["result"].(map[string]any)
	if sc, _ := res["structuredContent"].(map[string]any); res["isError"] != false || sc["id"] != looseFile {
		t.Fatalf("mcp review: %s", call.body)
	}
	call = mcpCall(other, "reviewUnassignedFile", map[string]any{"path": map[string]any{"upload_id": loose}})
	if res, _ := call.json["result"].(map[string]any); res["isError"] != true {
		t.Fatalf("mcp review by another module: %s", call.body)
	}

	// The grants go to mail-intake only, carrying background_file_authorization; the
	// other module, subscribed to file.*, hears about the files but not the grants.
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatal(err)
	}
	payloads := func(module, action string) []map[string]any {
		r, err := x.app.Pool.QueryContext(ctx, `SELECT payload FROM core.module_deliveries WHERE module_id = $1 AND action = $2`, module, action)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var out []map[string]any
		for r.Next() {
			var raw string
			var m map[string]any
			if err := r.Scan(&raw); err != nil || json.Unmarshal([]byte(raw), &m) != nil {
				t.Fatalf("payload %q: %v", raw, err)
			}
			out = append(out, m)
		}
		return out
	}
	got := payloads("mail-intake", "file.background_authorized")
	if len(got) != 2 {
		t.Fatalf("mail-intake grant deliveries: %v", got)
	}
	for _, p := range got {
		auth, _ := p["background_file_authorization"].(map[string]any)
		if auth["module_id"] != "mail-intake" || auth["issued_by"] != "core" {
			t.Fatalf("grant delivery: %v", p)
		}
	}
	if n := len(payloads("other-intake", "file.background_authorized")); n != 0 {
		t.Fatalf("other-intake received %d grants", n)
	}
	if created := payloads("other-intake", "file.created"); len(created) == 0 || strings.Contains(fmt.Sprint(created), "background_file_authorization") {
		t.Fatalf("other-intake file.created deliveries: %v", created)
	}
}

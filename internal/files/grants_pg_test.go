package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// stage starts, sends and completes one upload as p and returns its id.
func (e *env) stage(p domain.Principal, in UploadRequest, body string) domain.ID {
	e.t.Helper()
	ctx := context.Background()
	in.SizeBytes = int64(len(body))
	up, err := e.svc.CreateUpload(ctx, p, in)
	if err != nil {
		e.t.Fatalf("create upload %s: %v", in.FileName, err)
	}
	if len(body) > 0 {
		if _, err := e.svc.PutChunk(ctx, p, up.ID, 0, int64(len(body))-1, int64(len(body)), strings.NewReader(body)); err != nil {
			e.t.Fatal(err)
		}
	}
	if _, err := e.svc.CompleteUpload(ctx, p, up.ID); err != nil {
		e.t.Fatal(err)
	}
	return up.ID
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.svc.st.row(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) uploadStatus(id domain.ID) string {
	e.t.Helper()
	u, err := e.svc.st.getUpload(context.Background(), id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.Status
}

// modules makes the named modules installed and enabled for the files service.
// A module's own token has no user, so its rows are saved by the System user, which the
// running core creates at start (ensureSystemUser); this env adds it too.
func (e *env) modules(on map[string]bool) {
	e.svc.SetModuleCheck(func(_ context.Context, m string) (bool, error) { return on[m], nil })
	at := domain.UnixMicroUTC(e.clock.t)
	if err := e.svc.st.exec(context.Background(), `INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state)
VALUES (?,?,?,?,'System','system@example.invalid','[]',0,0,NULL,'','')`, domain.SystemUserID, at, at, domain.SystemUserID); err != nil {
		e.t.Fatal(err)
	}
}

func moduleP(id string) domain.Principal {
	return domain.Principal{Source: domain.SourceModule, ModuleID: id}
}

// TestPGBulkFilePublishesAllOrNothing is bulkFile: one transaction publishes every
// staged upload, with file.created and file.version_created after the commit; a replay
// returns the same files and emits nothing; a repeated id, a hidden matter or an
// unfinished upload rejects the batch, leaves every upload staged and stores nothing.
func TestPGBulkFilePublishesAllOrNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u1 := e.stage(e.staff, UploadRequest{FileName: "intake.txt", MatterID: e.open}, "intake notes")
	u2 := e.stage(e.staff, UploadRequest{FileName: "letter.txt", MatterID: e.open}, "engagement letter")
	rows, err := e.svc.PublishBatch(ctx, e.staff, []BulkItem{{UploadID: u1}, {UploadID: u2, Name: "engagement-letter.txt"}})
	if err != nil || len(rows) != 2 || rows[1].Name != "engagement-letter.txt" || rows[0].MatterID == nil || *rows[0].MatterID != e.open {
		t.Fatalf("bulk = %+v %v", rows, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE action = 'file.created'`); n != 2 {
		t.Fatalf("file.created events = %d, want 2", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE action = 'file.version_created'`); n != 2 {
		t.Fatalf("file.version_created events = %d, want 2", n)
	}
	if len(e.versions) != 2 || e.uploadStatus(u1) != "published" {
		t.Fatalf("after commit: %d version callbacks, upload %s", len(e.versions), e.uploadStatus(u1))
	}
	// Codex PR #55: the callbacks run after the bulk transaction, not on it.
	if len(e.versionErrs) != 0 {
		t.Fatalf("version callbacks ran on the finished transaction: %v", e.versionErrs)
	}
	if _, err := os.Stat(e.svc.stagingPath(u1)); !os.IsNotExist(err) {
		t.Fatalf("staged bytes kept after publication: %v", err)
	}

	// Replay: the same files, no new event, version or callback.
	again, err := e.svc.PublishBatch(ctx, e.staff, []BulkItem{{UploadID: u2}, {UploadID: u1}})
	if err != nil || again[0].ID != rows[1].ID || again[1].ID != rows[0].ID {
		t.Fatalf("replay = %+v %v", again, err)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE action IN ('file.created', 'file.version_created')`); n != 4 || len(e.versions) != 2 {
		t.Fatalf("replay emitted: %d events, %d callbacks", n, len(e.versions))
	}

	files := e.count(`SELECT COUNT(*) FROM files`)
	objects := e.count(`SELECT COUNT(*) FROM file_object_locations`)
	events := e.count(`SELECT COUNT(*) FROM events`)
	u3 := e.stage(e.outsider, UploadRequest{FileName: "a.txt", MatterID: e.open}, "first")
	u4 := e.stage(e.outsider, UploadRequest{FileName: "b.txt", MatterID: e.open}, "second")
	// Objects on the local disk: a rejected batch drops the bytes it already stored.
	stored := func() int {
		t.Helper()
		n := 0
		err := filepath.WalkDir(e.root+"/local", func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				n++
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	bytesStored := stored()
	unchanged := func(label string, ups ...domain.ID) {
		t.Helper()
		if e.count(`SELECT COUNT(*) FROM files`) != files || e.count(`SELECT COUNT(*) FROM file_object_locations`) != objects ||
			e.count(`SELECT COUNT(*) FROM events`) != events || len(e.versions) != 2 {
			t.Fatalf("%s wrote something", label)
		}
		if got := stored(); got != bytesStored {
			t.Fatalf("%s left %d stored objects behind", label, got-bytesStored)
		}
		for _, u := range ups {
			if e.uploadStatus(u) != "staged" {
				t.Fatalf("%s: upload %s is %s", label, u, e.uploadStatus(u))
			}
			if _, err := os.Stat(e.svc.stagingPath(u)); err != nil {
				t.Fatalf("%s: staged bytes gone: %v", label, err)
			}
		}
	}
	if _, err := e.svc.PublishBatch(ctx, e.outsider, []BulkItem{{UploadID: u3}, {UploadID: u3}}); !isCode(err, codeBatchRejected) {
		t.Fatalf("repeated upload_id = %v", err)
	}
	unchanged("a repeated upload_id", u3)
	// The second item moves its file onto a walled matter the outsider is not on: the
	// first item, already stored inside the transaction, is rolled back too.
	walled := e.walled
	if _, err := e.svc.PublishBatch(ctx, e.outsider, []BulkItem{{UploadID: u3}, {UploadID: u4, MatterID: &walled}}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("walled item = %v", err)
	}
	unchanged("a walled item", u3, u4)
	// Another user's upload is not found; an upload still receiving rejects the batch.
	if _, err := e.svc.PublishBatch(ctx, e.outsider, []BulkItem{{UploadID: u3}, {UploadID: u1}}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("another user's upload = %v", err)
	}
	open, err := e.svc.CreateUpload(ctx, e.outsider, UploadRequest{FileName: "c.txt", MatterID: e.open, SizeBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PublishBatch(ctx, e.outsider, []BulkItem{{UploadID: u3}, {UploadID: open.ID}}); !isCode(err, codeBatchRejected) {
		t.Fatalf("unfinished upload = %v", err)
	}
	unchanged("an unfinished upload", u3, u4)
	// Staff cannot make an attorney-only file; the batch writes nothing.
	if _, err := e.svc.PublishBatch(ctx, e.outsider, []BulkItem{{UploadID: u3}, {UploadID: u4, AttorneyOnly: true}}); err == nil {
		t.Fatal("staff published an attorney-only file")
	}
	unchanged("an attorney-only item", u3, u4)

	// Over HTTP: 201 with data, 422 batch_rejected, and an empty batch is rejected.
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.outsider, nil })
	post := func(body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+"/api/v1/files:bulk", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	if code, out := post(map[string]any{"items": []any{}}); code != http.StatusUnprocessableEntity || out["code"] != codeBatchRejected {
		t.Fatalf("empty batch = %d %v", code, out)
	}
	if code, out := post(map[string]any{"items": []any{map[string]any{"upload_id": u3}, map[string]any{"upload_id": u3}}}); code != http.StatusUnprocessableEntity || out["code"] != codeBatchRejected {
		t.Fatalf("repeated id over HTTP = %d %v", code, out)
	}
	if code, out := post(map[string]any{"items": []any{map[string]any{"upload_id": u3}, map[string]any{"upload_id": u4}}}); code != http.StatusCreated || len(out["data"].([]any)) != 2 {
		t.Fatalf("bulk over HTTP = %d %v", code, out)
	}
}

// TestPGPublicationGrantGovernsBackgroundUploads is issueFilePublicationGrant: only an
// installed, enabled module's own token gets a grant, for a matter it can reach (a
// walled or missing one is not found, and an admin viewer does not widen it) or
// unassigned. With an unassigned grant the module uploads and publishes with no matter
// and reviews only its own file; without one, after expiry, after a newer grant
// revoked it, or once the module is off, it is refused; a matter grant keeps the
// upload on its matter.
func TestPGPublicationGrantGovernsBackgroundUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mod, other := moduleP("mail-intake"), moduleP("zoom-intake")
	on := map[string]bool{"mail-intake": true, "zoom-intake": true}
	e.modules(on)
	open, walled := e.open, e.walled

	if _, err := e.svc.IssueGrant(ctx, e.admin, GrantRequest{Assignment: "unassigned", Source: "email"}, ""); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("a human session got a grant: %v", err)
	}
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "matter", MatterID: &walled, Source: "email"}, ""); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("walled matter grant = %v", err)
	}
	viaAdmin := mod
	viaAdmin.UserID, viaAdmin.Roles = e.admin.UserID, e.admin.Roles
	if _, err := e.svc.IssueGrant(ctx, viaAdmin, GrantRequest{Assignment: "matter", MatterID: &walled, Source: "email"}, ""); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("an admin viewer widened the grant: %v", err)
	}
	missing := id(t)
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "matter", MatterID: &missing, Source: "email"}, ""); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("missing matter grant = %v", err)
	}
	// Codex PR #55: an unassigned grant's contact is checked under the module, not
	// only for existence. A contact seen only through a walled matter answers like a
	// missing one, and an admin viewer does not widen it; a contact on a matter the
	// module reaches is accepted (checked at the end, with a module of its own).
	party := func(contact, matter domain.ID) {
		t.Helper()
		at := domain.UnixMicroUTC(e.clock.t)
		if err := e.svc.st.exec(ctx, `INSERT INTO contacts (id, created_at, updated_at, created_by, kind, given_name, middle_name, family_name, prefix, suffix, full_name,
preferred_name, also_known_as, date_of_birth, date_of_death, ssn_ein_ciphertext, entity_type, formation_state, formation_date, archived, source)
VALUES (?,?,?,?,'person','Walled','','Party','','','Walled Party','','','','','','','','',0,'test')`, contact, at, at, string(domain.SystemUserID)); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.st.exec(ctx, `INSERT INTO matter_parties (id, created_at, updated_at, created_by, matter_id, contact_id, role, is_primary, role_attributes, service_address, notes)
VALUES (?,?,?,?,?,?,'client',1,'{}','','')`, id(t), at, at, string(domain.SystemUserID), matter, contact); err != nil {
			t.Fatal(err)
		}
	}
	walledContact, openContact, noContact := id(t), id(t), id(t)
	party(walledContact, walled)
	party(openContact, open)
	_, missingErr := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "email", ContactID: &noContact}, "")
	if !isCode(missingErr, domain.CodeNotFound) {
		t.Fatalf("missing contact grant = %v", missingErr)
	}
	_, walledErr := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "email", ContactID: &walledContact}, "")
	if !isCode(walledErr, domain.CodeNotFound) || walledErr.Error() != missingErr.Error() {
		t.Fatalf("walled contact grant = %v, want the missing contact's %v", walledErr, missingErr)
	}
	if _, err := e.svc.IssueGrant(ctx, viaAdmin, GrantRequest{Assignment: "unassigned", Source: "email", ContactID: &walledContact}, ""); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("an admin viewer widened the contact: %v", err)
	}
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "fax"}, ""); !isCode(err, domain.CodeValidation) {
		t.Fatalf("unknown source = %v", err)
	}
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", MatterID: &open, Source: "email"}, ""); !isCode(err, domain.CodeValidation) {
		t.Fatalf("unassigned grant with a matter = %v", err)
	}
	on["mail-intake"] = false
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "email"}, ""); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("a disabled module got a grant: %v", err)
	}
	on["mail-intake"] = true

	// No grant: an upload with no matter is refused.
	if _, err := e.svc.CreateUpload(ctx, mod, UploadRequest{FileName: "x.eml", SizeBytes: 1}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("ungranted unassigned upload = %v", err)
	}
	queue := "inbox"
	g, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "email", ContactID: &e.clientContact, Queue: &queue}, "k1")
	if err != nil || g.ModuleID != "mail-intake" || g.MatterID != nil || g.IssuedBy != "core" || g.Revoked || g.Expired ||
		g.ContactID == nil || *g.ContactID != e.clientContact || len(g.UploadIDs) != 0 || g.TenantID == "" {
		t.Fatalf("grant = %+v %v", g, err)
	}
	// files.publication_grant_minutes starts at 60 (the test clock ticks on every read).
	if exp, err := time.Parse(time.RFC3339Nano, g.ExpiresAt); err != nil || exp.Sub(e.clock.now()) < 59*time.Minute || exp.Sub(e.clock.now()) > 60*time.Minute {
		t.Fatalf("expires %s at %s: %v", g.ExpiresAt, e.clock.now(), err)
	}
	// Idempotency: the same key and body replay; another body conflicts.
	if again, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "email", ContactID: &e.clientContact, Queue: &queue}, "k1"); err != nil || again.ExpiresAt != g.ExpiresAt {
		t.Fatalf("replay = %+v %v", again, err)
	}
	if _, err := e.svc.IssueGrant(ctx, mod, GrantRequest{Assignment: "unassigned", Source: "zoom"}, "k1"); !isCode(err, "idempotency_conflict") {
		t.Fatalf("key reuse = %v", err)
	}
	if n := e.count(`SELECT COUNT(*) FROM events WHERE action = 'file.background_authorized'`); n != 1 {
		t.Fatalf("file.background_authorized events = %d, want 1", n)
	}
	var body string
	if err := e.svc.st.row(ctx, `SELECT authorization_body FROM file_publication_grants WHERE module_id = 'mail-intake'`).Scan(&body); err != nil || !strings.Contains(body, `"assignment":"unassigned"`) {
		t.Fatalf("stored authorization %q %v", body, err)
	}

	// The grant's contact is the only one the upload may name.
	if _, err := e.svc.CreateUpload(ctx, mod, UploadRequest{FileName: "x.eml", SizeBytes: 1, ContactID: e.otherContact}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("upload naming another contact = %v", err)
	}
	up := e.stage(mod, UploadRequest{FileName: "message.eml", MediaType: "message/rfc822"}, "From: someone")
	// A module cannot give an unassigned upload a matter.
	if _, err := e.svc.PublishBatch(ctx, mod, []BulkItem{{UploadID: up, MatterID: &open}}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("matter on an unassigned upload = %v", err)
	}
	rows, err := e.svc.PublishBatch(ctx, mod, []BulkItem{{UploadID: up}})
	if err != nil || rows[0].MatterID != nil || rows[0].Source != "mail-intake" || rows[0].Origin != "received" || rows[0].ContactID == nil {
		t.Fatalf("unassigned publish = %+v %v", rows, err)
	}
	// Replay through the module's token returns the same unassigned file.
	if again, err := e.svc.PublishBatch(ctx, mod, []BulkItem{{UploadID: up}}); err != nil || again[0].ID != rows[0].ID {
		t.Fatalf("unassigned replay = %+v %v", again, err)
	}
	got, err := e.svc.ReviewUnassigned(ctx, mod, up)
	if err != nil || got.ID != rows[0].ID || got.MatterID != nil {
		t.Fatalf("review = %+v %v", got, err)
	}
	if _, err := e.svc.ReviewUnassigned(ctx, other, up); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("another module reviewed the upload: %v", err)
	}
	if _, err := e.svc.Get(ctx, other, rows[0].ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("another module read the unassigned file: %v", err)
	}
	if _, err := e.svc.ReviewUnassigned(ctx, e.staff, up); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("a human session reviewed: %v", err)
	}
	if view, err := e.svc.grantView(ctx, mustGrant(t, e, "mail-intake", "")); err != nil || len(view.UploadIDs) != 1 || view.UploadIDs[0] != up {
		t.Fatalf("grant uploads = %+v %v", view, err)
	}

	// Expiry: a staged upload under the grant is refused, and so is a new upload.
	late := e.stage(mod, UploadRequest{FileName: "late.eml"}, "From: later")
	e.clock.advance(61 * time.Minute)
	if _, err := e.svc.PublishBatch(ctx, mod, []BulkItem{{UploadID: late}}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("publish after expiry = %v", err)
	}
	if e.uploadStatus(late) != "staged" {
		t.Fatal("a refused publish changed the upload")
	}
	if _, err := e.svc.CreateUpload(ctx, mod, UploadRequest{FileName: "y.eml", SizeBytes: 1}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("upload after expiry = %v", err)
	}

	// A matter grant: a newer one revokes the older, and the upload stays on its matter.
	first, err := e.svc.IssueGrant(ctx, other, GrantRequest{Assignment: "matter", MatterID: &open, Source: "zoom"}, "")
	if err != nil || first.MatterID == nil || *first.MatterID != open || first.ContactID != nil {
		t.Fatalf("matter grant = %+v %v", first, err)
	}
	rec := e.stage(other, UploadRequest{FileName: "recording.mp4", MatterID: open}, "video bytes")
	if _, err := e.svc.IssueGrant(ctx, other, GrantRequest{Assignment: "matter", MatterID: &open, Source: "zoom"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: rec}}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("publish under a revoked grant = %v", err)
	}
	rec2 := e.stage(other, UploadRequest{FileName: "recording-2.mp4", MatterID: open}, "more video")
	closed := e.closed
	// The contract's wrong_matter: publishing a matter grant's upload onto another matter
	// is not_found, alone or in a batch, and publishes nothing.
	if _, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: rec2, MatterID: &closed}}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("publish onto another matter = %v", err)
	}
	side := e.stage(other, UploadRequest{FileName: "recording-side.mp4", MatterID: open}, "side video")
	if _, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: side, MatterID: &open}, {UploadID: rec2, MatterID: &closed}}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("batch with one item onto another matter = %v", err)
	}
	if rows, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: side}}); err != nil || len(rows) != 1 || *rows[0].MatterID != open {
		t.Fatalf("the refused batch published its other item: %+v %v", rows, err)
	}
	if rows, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: rec2}}); err != nil || *rows[0].MatterID != open || rows[0].Origin != "received" {
		t.Fatalf("matter-grant publish = %+v %v", rows, err)
	}
	// Disabling the module refuses its grant.
	rec3 := e.stage(other, UploadRequest{FileName: "recording-3.mp4", MatterID: open}, "even more")
	on["zoom-intake"] = false
	if _, err := e.svc.PublishBatch(ctx, other, []BulkItem{{UploadID: rec3}}); !isCode(err, domain.CodeForbidden) && !isCode(err, domain.CodeNotFound) {
		t.Fatalf("publish by a disabled module = %v", err)
	}
	on["drop-intake"] = true
	if g, err := e.svc.IssueGrant(ctx, moduleP("drop-intake"), GrantRequest{Assignment: "unassigned", Source: "drop_folder", ContactID: &openContact}, ""); err != nil || g.ContactID == nil || *g.ContactID != openContact {
		t.Fatalf("contact on a reachable matter = %+v %v", g, err)
	}
}

func mustGrant(t *testing.T, e *env, module string, matter domain.ID) grantRow {
	t.Helper()
	g, ok, err := e.svc.activeGrant(context.Background(), module, matter)
	if err != nil || !ok {
		t.Fatalf("active grant %s: %v %v", module, ok, err)
	}
	return g
}

// TestUnassignedRouteIsItsOwnMux: reviewUnassignedFile is served, and the file
// sub-resource routes still answer beside it.
func TestUnassignedRouteIsItsOwnMux(t *testing.T) {
	h, err := NewHandler(&Service{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/files/unassigned/0190f0c2-7f3e-7c1a-8b2d-1f2e3d4c5b6a", "/api/v1/files/unassigned/history"} {
		if _, pattern := h.UnassignedRoutes().Handler(httptest.NewRequest("GET", path, nil)); pattern == "" {
			t.Fatalf("%s is not served", path)
		}
	}
	if _, pattern := h.Routes().Handler(httptest.NewRequest("GET", "/api/v1/files/0190f0c2-7f3e-7c1a-8b2d-1f2e3d4c5b6a/history", nil)); pattern == "" {
		t.Fatal("file history is not served")
	}
}

package files

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func ids(rows []FileView) map[domain.ID]bool {
	out := map[domain.ID]bool{}
	for _, r := range rows {
		out[r.ID] = true
	}
	return out
}

// TestPGWalledFileHiddenOutsideTheWall is AC-039's wall clause: a file on a walled
// matter can't be listed, previewed, downloaded or searched from outside the wall, in
// the service and over the API, and its Markdown copy follows the same wall (AC-050).
func TestPGWalledFileHiddenOutsideTheWall(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	secret := e.create(e.attorney, NewFile{Name: "settlement-terms.pdf", MatterID: e.walled}, "walled bytes")
	md := "# Settlement\nThe quokka clause stays confidential.\n"
	if _, err := e.svc.AddRendition(ctx, e.attorney, secret.ID, *secret.CurrentVersionID, "markdown", strings.NewReader(md), int64(len(md))); err != nil {
		t.Fatal(err)
	}
	open := e.create(e.staff, NewFile{Name: "intake-notes.txt", MatterID: e.open}, "open bytes")

	// Inside the wall (attorney and the staff member named on it) everything works.
	for _, who := range []domain.Principal{e.attorney, e.staff} {
		if _, err := e.svc.Get(ctx, who, secret.ID); err != nil {
			t.Fatalf("inside wall get: %v", err)
		}
		found, err := e.svc.Search(ctx, who, "quokka", 10)
		if err != nil || !ids(found)[secret.ID] {
			t.Fatalf("inside wall search = %v %v", found, err)
		}
	}

	out := e.outsider
	if _, err := e.svc.Get(ctx, out, secret.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider get = %v", err)
	}
	if _, _, err := e.svc.List(ctx, out, ListQuery{MatterID: e.walled}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider matter list = %v", err)
	}
	all, _, err := e.svc.List(ctx, out, ListQuery{})
	if err != nil || ids(all)[secret.ID] || !ids(all)[open.ID] {
		t.Fatalf("outsider unfiltered list = %v %v", all, err)
	}
	for _, q := range []string{"quokka", "settlement"} {
		found, err := e.svc.Search(ctx, out, q, 10)
		if err != nil || len(found) != 0 {
			t.Fatalf("outsider search %q = %v %v", q, found, err)
		}
	}
	if _, err := e.svc.OpenVersion(ctx, out, secret.ID, *secret.CurrentVersionID, true); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider download = %v", err)
	}
	if _, err := e.svc.Versions(ctx, out, secret.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider versions = %v", err)
	}
	if _, err := e.svc.Renditions(ctx, out, secret.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider renditions = %v", err)
	}
	if _, _, err := e.svc.MarkdownCopy(ctx, out, secret.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider markdown = %v", err)
	}
	if _, err := e.svc.History(ctx, out, secret.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider history = %v", err)
	}
	if err := e.svc.Archive(ctx, out, []domain.ID{open.ID, secret.ID}, "", true, io.Discard); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider archive = %v", err)
	}
	if _, err := e.svc.Rename(ctx, out, secret.ID, "x"); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider rename = %v", err)
	}
	if _, err := e.svc.CreateUpload(ctx, out, UploadRequest{FileName: "x.txt", MatterID: e.walled, SizeBytes: 1}); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider upload into walled matter = %v", err)
	}
	if _, err := e.svc.Folders(ctx, out, e.walled); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider folders = %v", err)
	}

	// The same answers over HTTP: list, search, preview (inline), download, rendition.
	srv := e.server(func(r *http.Request) (domain.Principal, error) { return out, nil })
	for _, path := range []string{
		"/api/v1/files/" + string(secret.ID),
		"/api/v1/files/" + string(secret.ID) + "/versions/" + string(*secret.CurrentVersionID) + "/download?disposition=inline",
		"/api/v1/files/" + string(secret.ID) + "/versions/" + string(*secret.CurrentVersionID) + "/download?confirm=true",
		"/api/v1/files/" + string(secret.ID) + "/renditions",
		"/api/v1/files?matter_id=" + string(e.walled),
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || bytes.Contains(body, []byte("walled bytes")) {
			t.Fatalf("GET %s = %d %s", path, resp.StatusCode, body)
		}
	}
	for _, path := range []string{"/api/v1/files?q=quokka", "/api/v1/files"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Data []FileView `json:"data"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || ids(page.Data)[secret.ID] {
			t.Fatalf("GET %s = %d %v", path, resp.StatusCode, page.Data)
		}
	}
}

func TestPGRolesAttorneyOnlyAccountantAndClient(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ao := e.create(e.attorney, NewFile{Name: "memo.docx", MatterID: e.open, AttorneyOnly: true}, "privileged")
	receipt := e.create(e.staff, NewFile{Name: "receipt.pdf", MatterID: e.open, FinancialClass: "receipt"}, "receipt")
	plain := e.create(e.staff, NewFile{Name: "notes.txt", MatterID: e.open}, "notes")
	shared := e.create(e.staff, NewFile{Name: "engagement.pdf", MatterID: e.open, SharedWithClient: true}, "shared")
	walledShared := e.create(e.attorney, NewFile{Name: "w.pdf", MatterID: e.walled, SharedWithClient: true}, "w")
	ownContact := e.create(e.staff, NewFile{Name: "id-card.jpg", ContactID: e.clientContact, SharedWithClient: true}, "id")
	otherContact := e.create(e.staff, NewFile{Name: "other.jpg", ContactID: e.otherContact, SharedWithClient: true}, "other")

	if _, err := e.svc.Get(ctx, e.staff, ao.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("staff attorney-only = %v", err)
	}
	if _, err := e.svc.Get(ctx, e.attorney, ao.ID); err != nil {
		t.Fatalf("attorney attorney-only = %v", err)
	}
	if _, err := e.svc.Create(ctx, e.staff, NewFile{Name: "x.txt", MatterID: e.open, AttorneyOnly: true}, strings.NewReader("x"), 1); err == nil {
		t.Fatal("staff created an attorney-only file")
	}

	acct, _, err := e.svc.List(ctx, e.accountant, ListQuery{MatterID: e.open})
	if err != nil || len(acct) != 1 || acct[0].ID != receipt.ID {
		t.Fatalf("accountant list = %v %v", acct, err)
	}
	if _, err := e.svc.Get(ctx, e.accountant, plain.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("accountant general file = %v", err)
	}
	if _, err := e.svc.Rename(ctx, e.accountant, receipt.ID, "r.pdf"); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("accountant rename = %v", err)
	}

	cl, _, err := e.svc.List(ctx, e.client, ListQuery{})
	got := ids(cl)
	if err != nil || !got[shared.ID] || !got[ownContact.ID] || got[plain.ID] || got[otherContact.ID] || got[walledShared.ID] || got[ao.ID] {
		t.Fatalf("client list = %v %v", cl, err)
	}
	if _, err := e.svc.Trash(ctx, e.client, shared.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("client trash = %v", err)
	}

	// A client download is logged and marked client-visible for the highlight.
	d, err := e.svc.OpenVersion(ctx, e.client, shared.ID, *shared.CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, d.Body)
	d.Close()
	var actorKind, summary string
	var visible int
	err = e.pool.QueryRowContext(ctx, `SELECT actor_kind, summary, client_visible FROM events WHERE record_id = $1 AND action = 'file.downloaded'`, shared.ID).Scan(&actorKind, &summary, &visible)
	if err != nil || actorKind != "client" || visible != 1 || !strings.HasPrefix(summary, "Client downloaded") {
		t.Fatalf("client download event = %q %q %d %v", actorKind, summary, visible, err)
	}
	var audits int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE entity = 'file' AND entity_id = $1 AND field = 'download'`, shared.ID).Scan(&audits)
	if audits != 1 {
		t.Fatalf("download audit rows = %d", audits)
	}
}

// server serves the handler with a fixed principal.
func (e *env) server(who func(*http.Request) (domain.Principal, error)) *httptest.Server {
	e.t.Helper()
	h, err := NewHandler(e.svc, nil, who)
	if err != nil {
		e.t.Fatal(err)
	}
	h.Background = func(f func()) { f() }
	srv := httptest.NewServer(h.Routes())
	e.t.Cleanup(srv.Close)
	return srv
}

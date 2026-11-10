package files

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestPGFilesTabAndStoragePageRender(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ok := e.create(e.staff, NewFile{Name: "visible-letter.pdf", MatterID: e.open}, "x")
	md := "# letter\n"
	if _, err := e.svc.AddRendition(ctx, e.staff, ok.ID, *ok.CurrentVersionID, "markdown", strings.NewReader(md), int64(len(md))); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.MarkStatus(ctx, e.staff, ok.ID, "final"); err != nil {
		t.Fatal(err)
	}
	e.create(e.attorney, NewFile{Name: "walled-secret.pdf", MatterID: e.walled}, "y")
	page := func(who domain.Principal, path string) (int, string) {
		h, err := NewHandler(e.svc, os.DirFS("../../web/templates/files"), func(*http.Request) (domain.Principal, error) { return who, nil })
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(h.Routes())
		defer srv.Close()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") && !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("%s has no strict CSP", path)
		}
		return resp.StatusCode, string(b)
	}
	code, body := page(e.staff, "/practice/matters/"+string(e.open)+"/files")
	for _, want := range []string{"visible-letter.pdf", "visible-letter.md", `class="tag status-final"`, "Correspondence", `id="upload"`, "/files/static/files.js", "8 MB"} {
		if code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("files tab (%d) lacks %q:\n%s", code, want, body)
		}
	}
	if strings.Contains(body, "walled-secret") {
		t.Fatal("the files tab shows a walled file")
	}
	if code, _ := page(e.outsider, "/practice/matters/"+string(e.walled)+"/files"); code != http.StatusNotFound {
		t.Fatalf("outsider walled files tab = %d", code)
	}
	if code, body := page(e.admin, "/files/admin/storage"); code != http.StatusOK || !strings.Contains(body, "Local disk") || !strings.Contains(body, "Move all files") {
		t.Fatalf("storage page = %d", code)
	}
	if code, _ := page(e.staff, "/files/admin/storage"); code != http.StatusForbidden {
		t.Fatalf("staff storage page = %d", code)
	}
	if code, body := page(e.staff, "/files/static/files.js"); code != http.StatusOK || !strings.Contains(body, "Content-Range") {
		t.Fatalf("files.js = %d", code)
	}
	// An uploaded HTML file never previews inline: it downloads in a sandbox.
	html := e.create(e.staff, NewFile{Name: "page.html", MatterID: e.open, MediaType: "text/html"}, "<script>alert(1)</script>")
	pdf := e.create(e.staff, NewFile{Name: "brief.pdf", MatterID: e.open}, "%PDF-1.7")
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.staff, nil })
	for _, c := range []struct {
		f      FileView
		inline bool
	}{{html, false}, {pdf, true}} {
		f, inline := c.f, c.inline
		resp, err := http.Get(srv.URL + "/api/v1/files/" + string(f.ID) + "/versions/" + string(*f.CurrentVersionID) + "/download?disposition=inline")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		disp, csp := resp.Header.Get("Content-Disposition"), resp.Header.Get("Content-Security-Policy")
		if strings.HasPrefix(disp, "inline") != inline || (!inline && !strings.Contains(csp, "sandbox")) || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s: disposition %q csp %q", f.Name, disp, csp)
		}
	}
}

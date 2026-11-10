package files

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGMarkdownCopyNameWallsAndSearch is AC-050 on the file store: the Markdown copy
// has the original's base name with .md and follows a rename, follows the original's
// walls, and is what Files search matches. Reprocessing adds a Markdown version and
// keeps the old one.
func TestPGMarkdownCopyNameWallsAndSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.attorney, NewFile{Name: "smith-2026-0042-motion-to-withdraw-2026-10-15.docx", MatterID: e.walled}, "docx bytes")
	md1 := "# Motion to withdraw\nCounsel cites the zephyr doctrine.\n"
	r1, err := e.svc.AddRendition(ctx, e.attorney, f.ID, *f.CurrentVersionID, "markdown", strings.NewReader(md1), int64(len(md1)))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Name != "smith-2026-0042-motion-to-withdraw-2026-10-15.md" || r1.Number != 1 {
		t.Fatalf("markdown copy = %+v", r1)
	}
	if MarkdownName("notes.md") != "notes.md" {
		t.Fatal("a Markdown original is its own copy")
	}
	// Search matches the Markdown text, which is not in the name.
	hits, err := e.svc.Search(ctx, e.attorney, "Zephyr", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != f.ID {
		t.Fatalf("search = %v %v", hits, err)
	}
	// Outside the wall: no hit, no copy.
	if hits, _ := e.svc.Search(ctx, e.outsider, "zephyr", 10); len(hits) != 0 {
		t.Fatalf("outsider search = %v", hits)
	}
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.outsider, nil })
	resp, err := http.Get(srv.URL + "/api/v1/files/" + string(f.ID) + "/renditions/" + string(r1.ID))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || strings.Contains(string(body), "zephyr") {
		t.Fatalf("outsider rendition = %d %s", resp.StatusCode, body)
	}
	// Renaming the file carries the copy's name.
	if _, err := e.svc.Rename(ctx, e.attorney, f.ID, "smith-2026-0042-withdrawal"); err != nil {
		t.Fatal(err)
	}
	rs, err := e.svc.Renditions(ctx, e.attorney, f.ID)
	if err != nil || len(rs) != 1 || rs[0].Name != "smith-2026-0042-withdrawal.md" {
		t.Fatalf("renditions after rename = %+v %v", rs, err)
	}
	// Reprocessing adds Markdown version 2; version 1 stays and still downloads.
	md2 := "# Motion to withdraw\nCounsel cites the borealis doctrine.\n"
	r2, err := e.svc.AddRendition(ctx, e.attorney, f.ID, *f.CurrentVersionID, "markdown", strings.NewReader(md2), int64(len(md2)))
	if err != nil || r2.Number != 2 {
		t.Fatalf("second markdown = %+v %v", r2, err)
	}
	d, err := e.svc.OpenRendition(ctx, e.attorney, f.ID, r1.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := io.ReadAll(d.Body)
	d.Close()
	if string(old) != md1 || d.Name != "smith-2026-0042-withdrawal.md" {
		t.Fatalf("markdown v1 = %q %s", old, d.Name)
	}
	_, cur, err := e.svc.MarkdownCopy(ctx, e.attorney, f.ID)
	if err != nil || string(cur) != md2 {
		t.Fatalf("current markdown = %q %v", cur, err)
	}
	if hits, _ := e.svc.Search(ctx, e.attorney, "borealis", 10); len(hits) != 1 {
		t.Fatalf("search uses the newest copy: %v", hits)
	}
	if hits, _ := e.svc.Search(ctx, e.attorney, "zephyr", 10); len(hits) != 0 {
		t.Fatalf("search still matches the replaced copy: %v", hits)
	}
	// The copy lives on the same backend as the original.
	ver, _ := e.svc.st.getVersion(ctx, *f.CurrentVersionID)
	rr, _ := e.svc.st.getRendition(ctx, r2.ID)
	if ver.StorageBackendID != rr.StorageBackendID {
		t.Fatal("the Markdown copy is on another backend")
	}
	// Staff outside the wall cannot add a copy either.
	if _, err := e.svc.AddRendition(ctx, e.outsider, f.ID, *f.CurrentVersionID, "markdown", strings.NewReader("x"), 1); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider add rendition = %v", err)
	}
}

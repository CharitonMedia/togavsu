package files

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGArchiveEntries streams named version and rendition members after every check
// succeeds, and refuses bad paths or a hidden file before any zip byte (CORE-21).
func TestPGArchiveEntries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	letter := e.create(e.staff, NewFile{Name: "letter.pdf", MatterID: e.open}, "letter-bytes")
	memo := e.create(e.attorney, NewFile{Name: "memo.pdf", MatterID: e.open, AttorneyOnly: true}, "memo-bytes")
	md, err := e.svc.AddRendition(ctx, e.attorney, letter.ID, *letter.CurrentVersionID, "markdown", strings.NewReader("# letter"), int64(len("# letter")))
	if err != nil {
		t.Fatal(err)
	}
	versionID := *letter.CurrentVersionID
	renditionID := md.ID
	vid := versionID
	rid := renditionID

	var buf bytes.Buffer
	err = e.svc.Archive(ctx, e.attorney, ArchiveRequest{Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "docs/letter.pdf"},
		{FileID: letter.ID, RenditionID: &rid, Path: "docs/letter.md"},
	}}, &buf)
	if err != nil {
		t.Fatalf("entries archive: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("zip = %v members=%d", err, len(zr.File))
	}
	names := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		names[f.Name] = string(body)
	}
	if names["docs/letter.pdf"] != "letter-bytes" || names["docs/letter.md"] != "# letter" {
		t.Fatalf("zip bodies = %#v", names)
	}
	var downloads int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE record_id = $1 AND action = 'file.downloaded'`, letter.ID).Scan(&downloads)
	if downloads != 2 {
		t.Fatalf("letter downloads = %d, want 2", downloads)
	}

	hidden := &bytes.Buffer{}
	memoVid := *memo.CurrentVersionID
	err = e.svc.Archive(ctx, e.staff, ArchiveRequest{Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "ok.pdf"},
		{FileID: memo.ID, VersionID: &memoVid, Path: "secret.pdf"},
	}}, hidden)
	if !isCode(err, domain.CodeNotFound) || hidden.Len() != 0 {
		t.Fatalf("hidden entry = %v bytes=%d", err, hidden.Len())
	}

	badPath := &bytes.Buffer{}
	if err := e.svc.Archive(ctx, e.staff, ArchiveRequest{Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "../escape.pdf"},
	}}, badPath); !isCode(err, domain.CodeValidation) || badPath.Len() != 0 {
		t.Fatalf("bad path = %v bytes=%d", err, badPath.Len())
	}
	dup := &bytes.Buffer{}
	if err := e.svc.Archive(ctx, e.staff, ArchiveRequest{Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "Docs/a.pdf"},
		{FileID: letter.ID, RenditionID: &rid, Path: "docs/A.pdf"},
	}}, dup); !isCode(err, domain.CodeValidation) || dup.Len() != 0 {
		t.Fatalf("dup path = %v bytes=%d", err, dup.Len())
	}
	both := &bytes.Buffer{}
	if err := e.svc.Archive(ctx, e.staff, ArchiveRequest{
		FileIDs: []domain.ID{letter.ID},
		Entries: []ArchiveEntry{{FileID: letter.ID, VersionID: &vid, Path: "a.pdf"}},
	}, both); !isCode(err, domain.CodeValidation) || both.Len() != 0 {
		t.Fatalf("two selectors = %v bytes=%d", err, both.Len())
	}

	if _, err := e.svc.Quarantine(ctx, e.attorney, letter.ID, versionID, true); err != nil {
		t.Fatal(err)
	}
	qbuf := &bytes.Buffer{}
	if err := e.svc.Archive(ctx, e.attorney, ArchiveRequest{Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "q.pdf"},
	}}, qbuf); !isCode(err, "quarantine_confirmation_required") || qbuf.Len() != 0 {
		t.Fatalf("quarantine = %v bytes=%d", err, qbuf.Len())
	}
	confirmed := &bytes.Buffer{}
	if err := e.svc.Archive(ctx, e.attorney, ArchiveRequest{ConfirmQuarantine: true, Entries: []ArchiveEntry{
		{FileID: letter.ID, VersionID: &vid, Path: "q.pdf"},
	}}, confirmed); err != nil || confirmed.Len() == 0 {
		t.Fatalf("confirmed quarantine = %v bytes=%d", err, confirmed.Len())
	}
}

// TestPGArchiveAuthOnce checks credentials once at the start for every selector, then
// streams without calling Principal again (CORE-21).
func TestPGArchiveAuthOnce(t *testing.T) {
	e := newEnv(t)
	letter := e.create(e.staff, NewFile{Name: "once.pdf", MatterID: e.open}, "once-bytes")
	vid := *letter.CurrentVersionID
	var calls atomic.Int32
	srv := e.server(func(*http.Request) (domain.Principal, error) {
		calls.Add(1)
		return e.staff, nil
	})
	c := apiClient{t: t, url: srv.URL}

	bodies := []map[string]any{
		{"file_ids": []domain.ID{letter.ID}},
		{"entries": []map[string]any{{"file_id": letter.ID, "version_id": vid, "path": "once.pdf"}}},
	}
	folder, err := e.svc.FolderByName(context.Background(), e.attorney, e.open, "Correspondence")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.create(e.staff, NewFile{Name: "in-folder.pdf", MatterID: e.open, FolderID: folder.ID}, "folder-bytes")
	bodies = append(bodies, map[string]any{"folder_id": folder.ID})

	for _, body := range bodies {
		calls.Store(0)
		code, err := c.json("POST", "/api/v1/file-archives", body, nil)
		if err != nil || code != http.StatusOK {
			t.Fatalf("archive %v = %d %v", body, code, err)
		}
		if got := calls.Load(); got != 1 {
			t.Fatalf("principal calls for %v = %d, want 1", body, got)
		}
	}
}

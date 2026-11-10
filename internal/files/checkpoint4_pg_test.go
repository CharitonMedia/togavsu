package files

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// archiveBodies reads an ExportBytes archive back into its manifest and the body of
// each object entry, in archive order.
func archiveBodies(t *testing.T, archive []byte) ([]BackupObject, [][]byte) {
	t.Helper()
	list := readManifest(t, archive)
	tr := tar.NewReader(bytes.NewReader(archive))
	if _, err := tr.Next(); err != nil {
		t.Fatal(err)
	}
	var bodies [][]byte
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, b)
	}
	return list, bodies
}

// TestPGRestoreRejectsMissingOrRepeatedObjects is checkpoint 4 finding 2: an archive
// that ends cleanly before every manifest object appeared, or repeats one, fails the
// restore rather than reporting success.
func TestPGRestoreRejectsMissingOrRepeatedObjects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.create(e.staff, NewFile{Name: "a.txt", MatterID: e.open}, "first file")
	e.create(e.staff, NewFile{Name: "b.txt", MatterID: e.open}, "second file")
	var archive bytes.Buffer
	if _, err := e.svc.ExportBytes(ctx, e.admin, &archive, nil); err != nil {
		t.Fatal(err)
	}
	list, bodies := archiveBodies(t, archive.Bytes())
	if len(list) < 2 || len(bodies) != len(list) {
		t.Fatalf("archive has %d manifest objects and %d entries", len(list), len(bodies))
	}
	// Manifest of two, only the first object present.
	missing := buildArchiveWith(t, list, list[:1], bodies[:1])
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(missing)); err == nil || !strings.Contains(err.Error(), "ends before") {
		t.Fatalf("archive missing an object restored = %v", err)
	}
	// Manifest of two, the first object twice and the second never.
	repeated := buildArchiveWith(t, list, []BackupObject{list[0], list[0]}, [][]byte{bodies[0], bodies[0]})
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(repeated)); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("archive repeating an object restored = %v", err)
	}
	// A manifest naming one object twice is refused.
	doubled := buildArchiveWith(t, []BackupObject{list[0], list[0]}, list[:1], bodies[:1])
	if _, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(doubled)); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("manifest repeating an object restored = %v", err)
	}
	// The complete archive still restores.
	if rep, err := e.svc.RestoreBytes(ctx, e.admin, bytes.NewReader(archive.Bytes())); err != nil || rep.Objects != len(list) {
		t.Fatalf("complete restore = %+v %v", rep, err)
	}
}

// buildArchiveWith writes a manifest and a separate list of object entries, so a test
// can make the two disagree.
func buildArchiveWith(t *testing.T, manifest, entries []BackupObject, bodies [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEntry(tw, manifestName, int64(len(raw)), bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for i, o := range entries {
		if err := writeEntry(tw, "objects/"+string(o.ObjectID), int64(len(bodies[i])), bytes.NewReader(bodies[i])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestPGStreamsMustMatchTheirDeclaredSize is checkpoint 4 findings 4 and 5: a file
// stream longer than its declared size is refused rather than saved truncated, and a
// rendition, Markdown or not, must be exactly the size it declares.
func TestPGStreamsMustMatchTheirDeclaredSize(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Create(ctx, e.staff, NewFile{Name: "long.txt", MatterID: e.open}, strings.NewReader("hello world"), 5); !isCode(err, domain.CodeConflict) {
		t.Fatalf("longer stream than declared = %v", err)
	}
	if _, err := e.svc.Create(ctx, e.staff, NewFile{Name: "short.txt", MatterID: e.open}, strings.NewReader("hi"), 5); err == nil {
		t.Fatalf("shorter stream than declared = %v", err)
	}
	if got, _, err := e.svc.List(ctx, e.staff, ListQuery{MatterID: e.open}); err != nil || len(got) != 0 {
		t.Fatalf("refused creates left files: %v %v", got, err)
	}
	f := e.create(e.staff, NewFile{Name: "ok.txt", MatterID: e.open}, "body")
	v := *f.CurrentVersionID
	for _, c := range []struct {
		kind, body string
		size       int64
		code       bool // the refusal is a validation error, not a backend short write
	}{
		{"markdown", "# longer than declared", 3, true},
		{"markdown", "ab", 3, true},
		{"pdf_preview", "longer preview bytes", 4, true},
		{"pdf_preview", "ab", 4, false},
	} {
		_, err := e.svc.AddRendition(ctx, e.attorney, f.ID, v, c.kind, strings.NewReader(c.body), c.size)
		if err == nil || (c.code && !isCode(err, domain.CodeValidation)) {
			t.Fatalf("%s of %d bytes declared %d = %v", c.kind, len(c.body), c.size, err)
		}
	}
	// Search never indexed the oversized Markdown text.
	if got, err := e.svc.Search(ctx, e.attorney, "declared", 10); err != nil || len(got) != 0 {
		t.Fatalf("search found refused Markdown: %v %v", got, err)
	}
	md := "# exact"
	if _, err := e.svc.AddRendition(ctx, e.attorney, f.ID, v, "markdown", strings.NewReader(md), int64(len(md))); err != nil {
		t.Fatalf("exact Markdown = %v", err)
	}
}

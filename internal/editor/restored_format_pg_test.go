package editor

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// odtPackage is the smallest package that reads as OpenDocument text.
func odtPackage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(mediaTypes["odt"])); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Opus checkpoint P2: after a restore brings back a version written before the editor
// saved the file as DOCX, Convert and the edit page go by that version's format, not the
// file's extension. No LawCraft is needed: the stand-in runner writes back the input
// path it was given, which names the format it was handed.
func TestPGRestoredOlderFormatIsConvertedAndOpenedAsItself(t *testing.T) {
	e := newEnvWith(t, fakeCLI(t, `echo "$2" > "$3"`+"\n"))
	body := odtPackage(t)
	f, err := e.files.Create(e.ctx, e.staff, files.NewFile{Name: "Letter.odt", MatterID: e.matter, MediaType: mediaTypes["odt"]},
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	v1 := *f.CurrentVersionID
	docx := []byte("PK synthetic docx bytes")
	if _, err := e.files.SaveVersion(e.ctx, e.staff, f.ID, bytes.NewReader(docx), int64(len(docx)),
		files.SaveOptions{How: "editor", MediaType: docxMedia, Extension: "docx"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.files.RestoreVersion(e.ctx, e.staff, f.ID, v1); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.current(f.ID); got.Extension != "docx" && got.Extension != ".docx" {
		t.Fatalf("setup: the file's extension is %q, want docx after the editor save", got.Extension)
	}
	if _, err := e.svc.Convert(e.ctx, e.staff, f.ID, "odt"); code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "already in that format") {
		t.Fatalf("Convert to ODT of a restored ODT version: %v, want refused as the same format", err)
	}
	res, err := e.svc.Convert(e.ctx, e.staff, f.ID, "text")
	if err != nil {
		t.Fatalf("Convert to text: %v", err)
	}
	_, _, out := e.current(res.File.ID)
	if !strings.HasSuffix(strings.TrimSpace(string(out)), ".odt") {
		t.Fatalf("the runner was handed %q, want an .odt input", out)
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	name, got, err := e.svc.Document(e.ctx, e.staff, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Letter.odt" || !bytes.Equal(got, body) {
		t.Fatalf("Document gave %q (%d bytes), want Letter.odt with the restored ODT bytes", name, len(got))
	}
}

// Opus checkpoint P2: the declared media type does not decide the format. Markdown sent
// as text/plain and RTF sent as application/msword (as Windows browsers do) keep the
// format their extension and bytes say.
func TestPGDeclaredMediaTypeDoesNotOverrideTheFormat(t *testing.T) {
	e := newEnvWith(t, fakeCLI(t, `echo "$2" > "$3"`+"\n"))
	for _, c := range []struct {
		name, media, ext string
		body             []byte
	}{
		{"notes.md", "text/plain", ".md", []byte("# Notes\n\n- one\n")},
		{"Brief.rtf", "application/msword", ".rtf", []byte(`{\rtf1\ansi Synthetic brief.}`)},
	} {
		f, err := e.files.Create(e.ctx, e.staff, files.NewFile{Name: c.name, MatterID: e.matter, MediaType: c.media},
			bytes.NewReader(c.body), int64(len(c.body)))
		if err != nil {
			t.Fatal(err)
		}
		res, err := e.svc.Convert(e.ctx, e.staff, f.ID, "html")
		if err != nil {
			t.Fatalf("%s: Convert to HTML: %v", c.name, err)
		}
		if _, _, out := e.current(res.File.ID); !strings.HasSuffix(strings.TrimSpace(string(out)), c.ext) {
			t.Fatalf("%s: the runner was handed %q, want a %s input", c.name, out, c.ext)
		}
		if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
			t.Fatal(err)
		}
		if name, _, err := e.svc.Document(e.ctx, e.staff, f.ID); err != nil || name != c.name {
			t.Fatalf("Document gave %q, %v; want %s", name, err, c.name)
		}
	}
	md, err := e.files.Create(e.ctx, e.staff, files.NewFile{Name: "more.md", MatterID: e.matter, MediaType: "text/plain"},
		strings.NewReader("# More\n"), 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Convert(e.ctx, e.staff, md.ID, "text"); err != nil {
		t.Fatalf("Convert of Markdown sent as text/plain to text: %v, want it converted", err)
	}
}

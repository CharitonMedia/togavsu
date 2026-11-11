package doctender

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/files"
)

// Opus checkpoint P1: a photo whose read failed its check is grouped with its document,
// but its page stays unresolved in the PDF's Markdown and is never published as checked.
func TestFailedPhotoIsAnUnresolvedPageOfItsGroup(t *testing.T) {
	e := newEnv(t)
	photos := uploadPhotos(e)
	e.drain()
	// The second letter page's read failed its check, as runUnit records it: the unit
	// is failed and keeps its unchecked text.
	j := e.jobFor(photos[1].ID)
	if _, err := e.pool.ExecContext(e.ctx, `UPDATE doc_tender_units SET status = 'failed', last_error = 'check failed: synthetic' WHERE job_id = $1 AND unit = 'photo'`, string(j.ID)); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(10 * minuteUnit)
	e.drain()
	list, _, err := e.files.List(e.ctx, e.attorney, files.ListQuery{MatterID: e.open})
	if err != nil {
		t.Fatal(err)
	}
	var letter string
	for _, f := range list {
		if f.Extension != "pdf" {
			continue
		}
		if _, md := e.markdown(e.attorney, f.ID); strings.Contains(md, "LETTER") {
			letter = md
		}
	}
	if letter == "" {
		t.Fatal("no letter PDF was made")
	}
	if !strings.Contains(letter, "## Page 2\n\n[unresolved: page 2]") {
		t.Fatalf("page 2 is not marked unresolved:\n%s", letter)
	}
	if strings.Contains(letter, "PAGE 2 OF 3") {
		t.Fatalf("the failed page's unchecked text was published:\n%s", letter)
	}
	if !strings.Contains(letter, "PAGE 1 OF 3") || !strings.Contains(letter, "PAGE 3 OF 3") {
		t.Fatalf("the checked pages are missing:\n%s", letter)
	}
}

// Opus checkpoint P2: a password-protected DOCX (a compound file with an
// EncryptedPackage stream, not a zip) and a password-protected ODT (a zip whose manifest
// lists encryption data) are flagged as password-protected, not damaged.
func TestPasswordProtectedDOCXAndODTAreFlaggedAsSuch(t *testing.T) {
	e := newEnv(t)
	pkg := bytes.Repeat([]byte{0x5a}, 4096)
	docx := cfbNamed(t, "EncryptedPackage", pkg, false)
	if bytes.Contains(docx, []byte("EncryptedPackage")) {
		t.Fatal("setup: the stream name should be UTF-16, as Word writes it")
	}
	var odt bytes.Buffer
	zw := zip.NewWriter(&odt)
	for name, body := range map[string]string{
		"mimetype":              "application/vnd.oasis.opendocument.text",
		"META-INF/manifest.xml": `<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"><manifest:file-entry manifest:full-path="content.xml"><manifest:encryption-data manifest:checksum-type="SHA1"/></manifest:file-entry></manifest:manifest>`,
		"content.xml":           "\x00\x01synthetic encrypted bytes",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	fs := []files.FileView{
		e.upload(e.staff, e.open, "agreement.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx),
		e.upload(e.staff, e.open, "agreement.odt", "application/vnd.oasis.opendocument.text", odt.Bytes()),
	}
	e.drain()
	for _, f := range fs {
		ex := e.openExceptions(f.ID)
		if len(ex) != 1 || ex[0].Kind != "password_protected" {
			t.Fatalf("%s: items %+v, want one password_protected", f.Name, ex)
		}
	}
}

package editor

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

const (
	okTypes = `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	okRels = `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	okDoc     = `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Synthetic</w:t></w:r></w:p></w:body></w:document>`
	okDocRels = `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.invalid/" TargetMode="External"/></Relationships>`
	okStyles = `<?xml version="1.0" encoding="UTF-8"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`
)

type part struct{ name, body string }

func pkg(t *testing.T, parts ...part) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.Create(p.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func good() []part {
	return []part{
		{"[Content_Types].xml", okTypes}, {"_rels/.rels", okRels}, {"word/document.xml", okDoc},
		{"word/_rels/document.xml.rels", okDocRels}, {"word/styles.xml", okStyles},
	}
}

func replace(ps []part, name, body string) []part {
	out := append([]part(nil), ps...)
	for i := range out {
		if out[i].name == name {
			out[i].body = body
		}
	}
	return out
}

func without(ps []part, name string) []part {
	var out []part
	for _, p := range ps {
		if p.name != name {
			out = append(out, p)
		}
	}
	return out
}

func TestCheckDOCXAcceptsAWellFormedPackage(t *testing.T) {
	if err := CheckDOCX(pkg(t, good()...)); err != nil {
		t.Fatalf("a well-formed package was refused: %v", err)
	}
	if got := sniff(pkg(t, good()...)); got != "docx" {
		t.Fatalf("sniff = %q", got)
	}
}

// Each case is a fault that makes Word stop with "Word found unreadable content".
func TestCheckDOCXRefusesWhatWordWouldRepair(t *testing.T) {
	cases := map[string][]byte{
		"not a zip":            []byte("PK\x03\x04 but not really a zip archive"),
		"no content types":     pkg(t, without(good(), "[Content_Types].xml")...),
		"no package rels":      pkg(t, without(good(), "_rels/.rels")...),
		"dangling relation":    pkg(t, without(good(), "word/styles.xml")...),
		"malformed part":       pkg(t, replace(good(), "word/styles.xml", `<w:styles xmlns:w="x"><w:style></w:styles>`)...),
		"wrong main type":      pkg(t, replace(good(), "[Content_Types].xml", strings.Replace(okTypes, "document.main+xml", "template.main+xml", 1))...),
		"untyped part":         pkg(t, append(good(), part{"word/media/image1.png", "png"})...),
		"not a w:document":     pkg(t, replace(good(), "word/document.xml", `<w:body xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`)...),
		"no body":              pkg(t, replace(good(), "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`)...),
		"repeated rel id":      pkg(t, replace(good(), "word/_rels/document.xml.rels", strings.Replace(okDocRels, `Id="rId2"`, `Id="rId1"`, 1))...),
		"override of nothing":  pkg(t, replace(good(), "[Content_Types].xml", strings.Replace(okTypes, "</Types>", `<Override PartName="/word/missing.xml" ContentType="application/xml"/></Types>`, 1))...),
		"escaping part name":   pkg(t, append(good(), part{"../evil.xml", "<a/>"})...),
		"two main documents":   pkg(t, replace(good(), "_rels/.rels", strings.Replace(okRels, "</Relationships>", `<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`, 1))...),
		"duplicate part names": pkg(t, append(good(), part{"WORD/document.xml", okDoc})...),
	}
	for name, b := range cases {
		err := CheckDOCX(b)
		if !errors.Is(err, ErrNotWordCompatible) {
			t.Errorf("%s: CheckDOCX = %v, want ErrNotWordCompatible", name, err)
		}
	}
}

func TestSniffNamesDocumentFormats(t *testing.T) {
	odt := func() []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("mimetype")
		_, _ = w.Write([]byte("application/vnd.oasis.opendocument.text"))
		_ = zw.Close()
		return buf.Bytes()
	}()
	for want, b := range map[string][]byte{
		"pdf": []byte("%PDF-1.7\n"), "rtf": []byte(`{\rtf1\ansi Synthetic}`), "doc": []byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"),
		"odt": odt, "": []byte("# Markdown"),
	} {
		if got := sniff(b); got != want {
			t.Errorf("sniff = %q, want %q", got, want)
		}
	}
}

func TestEditableFormatsAreThePRDs(t *testing.T) {
	for _, ext := range []string{"docx", "DOC", ".odt", "rtf", "html", "htm", "md", "txt"} {
		if !Editable(ext) {
			t.Errorf("%s should open in the editor", ext)
		}
	}
	for _, ext := range []string{"pdf", "png", "xlsx", "eml", ""} {
		if Editable(ext) {
			t.Errorf("%s should not open in the editor", ext)
		}
	}
}

func TestPinnedInfoIsTheVersionedCommit(t *testing.T) {
	info := PinnedInfo()
	if info.Commit != "770a8fab12392485c0c2789fb254da7d9296df4d" || info.Source != "CharitonMedia/LawCraft" || !info.ReadOnly {
		t.Fatalf("editor info = %+v", info)
	}
}

// A version keeps its own format: a PDF saved onto a .docx file previews as a PDF.
func TestFormatOfGoesByExtensionUnlessTheBytesDisagree(t *testing.T) {
	odt := odtPackage(t)
	cases := []struct {
		fileExt, media string
		data           []byte
		want           string
	}{
		{"docx", "", []byte("%PDF-1.7\n%%EOF\n"), "pdf"},
		{".DOCX", "", []byte("{\\rtf1 synthetic}"), "rtf"},
		{"docx", "", []byte("\xD0\xCF\x11\xE0synthetic"), "doc"},
		{"md", "", []byte("# Notes\n\n- one\n"), "md"},
		{"markdown", "", []byte("plain"), "md"},
		{"rtf", "", []byte("{\\rtf1 synthetic}"), "rtf"},
		{"txt", "", []byte("hello"), "txt"},
		{"html", "", []byte("<p>hi</p>"), "html"},
		{"docx", "", odt, "odt"},
		{"odt", "", odt, "odt"},
		{"docx", "text/markdown", []byte("# Notes\n"), "md"},
		{"docx", "text/html; charset=utf-8", []byte("<p>hi</p>"), "html"},
		{"docx", "text/plain", []byte("hello"), "txt"},
		{"md", "text/plain", []byte("# Notes\n"), "md"},
		{"rtf", "application/msword", []byte("{\\rtf1 synthetic}"), "rtf"},
		{"docx", "application/pdf", []byte("PK synthetic"), "docx"},
	}
	for _, c := range cases {
		if got := formatOf(c.fileExt, c.media, c.data); got != c.want {
			t.Errorf("formatOf(%q, %q, %q) = %q, want %q", c.fileExt, c.media, c.data, got, c.want)
		}
	}
}

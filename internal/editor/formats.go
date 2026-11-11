package editor

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// editable is every extension the editor opens (PRD 8.20: DOCX, DOC, ODT, RTF, HTML,
// Markdown or text).
var editable = map[string]bool{"docx": true, "doc": true, "odt": true, "rtf": true, "html": true, "htm": true, "md": true, "markdown": true, "txt": true}

// Editable reports whether a file with this extension opens in the editor.
func Editable(ext string) bool { return editable[normExt(ext)] }

// conversionTargets maps the API's format names (PRD 8.20 Convert) to the extension
// wordcraft-cli writes.
var conversionTargets = map[string]string{"pdf": "pdf", "docx": "docx", "odt": "odt", "rtf": "rtf", "html": "html", "markdown": "md", "text": "txt"}

// mediaTypes are the types the file store records for what the editor writes.
var mediaTypes = map[string]string{
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"doc":  "application/msword",
	"odt":  "application/vnd.oasis.opendocument.text",
	"rtf":  "application/rtf",
	"html": "text/html",
	"htm":  "text/html",
	"md":   "text/markdown",
	"txt":  "text/plain",
	"pdf":  "application/pdf",
}

// formatOf is the format a version's bytes are in. It is the file's extension unless
// the bytes say otherwise, as when a restored version written before a DOCX save keeps
// its own format under the .docx name:
//   - bytes that are plainly another binary format (DOCX, ODT, RTF, DOC or PDF by their
//     signature) are that format;
//   - bytes without the signature of the binary format the extension names are the text
//     format the version's media type records (HTML, Markdown or text).
//
// A text extension is never overridden by the declared media type: a browser or client
// may send text/plain for Markdown or application/msword for RTF.
func formatOf(fileExt, mediaType string, data []byte) string {
	ext := canonExt(fileExt)
	got := sniff(data)
	switch got {
	case "docx", "odt", "rtf", "doc", "pdf":
		if got != ext {
			return got
		}
		return ext
	}
	switch ext {
	case "docx", "odt", "rtf", "doc":
		base, _, _ := strings.Cut(mediaType, ";")
		mt := strings.ToLower(strings.TrimSpace(base))
		for _, text := range []string{"html", "md", "txt"} {
			if mediaTypes[text] == mt {
				return text
			}
		}
	}
	return ext
}

// docxMedia is the DOCX media type.
var docxMedia = mediaTypes["docx"]

func normExt(ext string) string { return strings.ToLower(strings.TrimPrefix(ext, ".")) }

func extOf(name string) string { return normExt(path.Ext(name)) }

// sameFormat says two extensions name one format (htm and html, md and markdown).
func sameFormat(a, b string) bool { return canonExt(a) == canonExt(b) }

func canonExt(ext string) string {
	switch e := normExt(ext); e {
	case "htm":
		return "html"
	case "markdown":
		return "md"
	default:
		return e
	}
}

func baseName(name string) string { return strings.TrimSuffix(name, path.Ext(name)) }

// ErrNotWordCompatible is wrapped by CheckDOCX for every package fault Word would stop
// on with a repair prompt.
var ErrNotWordCompatible = errors.New("not a Word-compatible DOCX package")

const (
	nsWordMain      = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	ctMainDocument  = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	relOfficeDoc    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	relOfficeDocStr = "http://purl.oclc.org/ooxml/officeDocument/relationships/officeDocument"
	maxDOCXParts    = 1 << 14
	maxPartBytes    = 512 << 20
)

// ponytail: Word compatibility is checked against the packaging rules, not by Word.
// Ceiling: content Word rejects inside a well-formed part (an unknown element in a
// strict place) passes. Upgrade: open each release's sample saves in Word on a Windows
// runner during final acceptance. Trigger: a user reports a repair prompt on a saved file.
//
// CheckDOCX checks the Open Packaging Conventions rules whose breach makes Word offer to
// repair a document: a readable zip with unique, relative part names; [Content_Types].xml
// giving every part a type; _rels/.rels naming one main document of the WordprocessingML
// type; every relationship part well formed with each internal target present; every XML
// part well formed; and a w:document root with a w:body. It does not run Word: a
// document can pass and still render differently in Word.
func CheckDOCX(b []byte) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrNotWordCompatible, fmt.Sprintf(format, args...))
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return fail("not a zip package: %v", err)
	}
	if len(zr.File) > maxDOCXParts {
		return fail("%d parts", len(zr.File))
	}
	parts := map[string]*zip.File{}
	for _, f := range zr.File {
		name := f.Name
		if strings.HasSuffix(name, "/") {
			continue
		}
		if strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return fail("part name %q is not a relative package path", name)
		}
		key := strings.ToLower(name)
		if _, dup := parts[key]; dup {
			return fail("part %q appears twice", name)
		}
		parts[key] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := parts[strings.ToLower(name)]
		if !ok {
			return nil, fail("missing part %s", name)
		}
		if f.UncompressedSize64 > maxPartBytes {
			return nil, fail("part %s is too large", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fail("part %s: %v", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, maxPartBytes+1))
		if err != nil {
			return nil, fail("part %s does not decompress: %v", name, err)
		}
		return data, nil
	}

	ctBytes, err := read("[Content_Types].xml")
	if err != nil {
		return err
	}
	var ct struct {
		Defaults []struct {
			Extension   string `xml:"Extension,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Default"`
		Overrides []struct {
			PartName    string `xml:"PartName,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(ctBytes, &ct); err != nil {
		return fail("[Content_Types].xml: %v", err)
	}
	defaults := map[string]string{}
	for _, d := range ct.Defaults {
		defaults[strings.ToLower(d.Extension)] = d.ContentType
	}
	overrides := map[string]string{}
	for _, o := range ct.Overrides {
		name := strings.ToLower(strings.TrimPrefix(o.PartName, "/"))
		if _, ok := parts[name]; !ok {
			return fail("[Content_Types].xml overrides %s, which is not in the package", o.PartName)
		}
		overrides[name] = o.ContentType
	}
	typeOf := func(name string) string {
		if t, ok := overrides[strings.ToLower(name)]; ok {
			return t
		}
		return defaults[extOf(name)]
	}

	for key, f := range parts {
		if key == "[content_types].xml" {
			continue
		}
		if typeOf(f.Name) == "" {
			return fail("part %s has no content type", f.Name)
		}
		if ext := extOf(f.Name); ext == "xml" || ext == "rels" {
			data, err := read(f.Name)
			if err != nil {
				return err
			}
			if err := wellFormed(data); err != nil {
				return fail("part %s is not well-formed XML: %v", f.Name, err)
			}
		}
	}

	var main string
	for key, f := range parts {
		if !strings.HasSuffix(key, ".rels") {
			continue
		}
		dir := path.Dir(path.Dir(f.Name)) // a/_rels/b.xml.rels describes a/b.xml
		if path.Base(path.Dir(f.Name)) != "_rels" {
			return fail("relationship part %s is not in a _rels folder", f.Name)
		}
		data, err := read(f.Name)
		if err != nil {
			return err
		}
		var rels struct {
			Rels []struct {
				ID         string `xml:"Id,attr"`
				Type       string `xml:"Type,attr"`
				Target     string `xml:"Target,attr"`
				TargetMode string `xml:"TargetMode,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(data, &rels); err != nil {
			return fail("%s: %v", f.Name, err)
		}
		ids := map[string]bool{}
		for _, r := range rels.Rels {
			if r.ID == "" || ids[r.ID] {
				return fail("%s: relationship id %q is empty or repeated", f.Name, r.ID)
			}
			ids[r.ID] = true
			if strings.EqualFold(r.TargetMode, "External") {
				continue
			}
			target := r.Target
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				if dir == "." {
					dir = ""
				}
				target = path.Clean(path.Join(dir, target))
			}
			if _, ok := parts[strings.ToLower(target)]; !ok {
				return fail("%s: relationship %s points at %s, which is not in the package", f.Name, r.ID, r.Target)
			}
			if key == "_rels/.rels" && (r.Type == relOfficeDoc || r.Type == relOfficeDocStr) {
				if main != "" {
					return fail("_rels/.rels names more than one main document")
				}
				main = target
			}
		}
	}
	if main == "" {
		return fail("_rels/.rels names no main document")
	}
	if t := typeOf(main); t != ctMainDocument {
		return fail("main document %s has content type %q", main, t)
	}
	doc, err := read(main)
	if err != nil {
		return err
	}
	root, body, err := rootAndBody(doc)
	if err != nil {
		return fail("%s: %v", main, err)
	}
	if root.Space != nsWordMain || root.Local != "document" {
		return fail("%s root is {%s}%s, not w:document", main, root.Space, root.Local)
	}
	if !body {
		return fail("%s has no w:body", main)
	}
	return nil
}

func wellFormed(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	seenRoot := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			if !seenRoot {
				return errors.New("no root element")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if _, ok := tok.(xml.StartElement); ok {
			seenRoot = true
		}
	}
}

func rootAndBody(data []byte) (xml.Name, bool, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var root xml.Name
	// below counts open elements under the root.
	var below int64
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return root, false, nil
		}
		if err != nil {
			return root, false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if root.Local == "" {
				root = t.Name
				continue
			}
			if below == zero && t.Name.Space == nsWordMain && t.Name.Local == "body" {
				return root, true, nil
			}
			below++
		case xml.EndElement:
			below--
		}
	}
}

// sniff names the format of bytes the editor handed back: docx, odt, rtf, pdf, or "" when
// the bytes alone do not say (HTML, Markdown and text).
func sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(b, []byte(`{\rtf`)):
		return "rtf"
	case bytes.HasPrefix(b, []byte("\xD0\xCF\x11\xE0")):
		return "doc"
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return "zip"
		}
		for _, f := range zr.File {
			if f.Name == "mimetype" {
				rc, err := f.Open()
				if err != nil {
					return "zip"
				}
				m, _ := io.ReadAll(io.LimitReader(rc, 1<<7))
				_ = rc.Close()
				if string(m) == "application/vnd.oasis.opendocument.text" {
					return "odt"
				}
			}
			if strings.EqualFold(f.Name, "[Content_Types].xml") {
				return "docx"
			}
		}
		return "zip"
	}
	return ""
}

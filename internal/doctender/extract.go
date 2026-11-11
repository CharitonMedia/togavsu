package doctender

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// File kinds by extraction method (PRD 8.21 step 2).
const (
	typeText        = "text"
	typeMarkdown    = "markdown"
	typeHTML        = "html"
	typeWord        = "word"
	typeSheet       = "spreadsheet"
	typePDF         = "pdf"
	typePhoto       = "photo"
	typeAudio       = "audio"
	typeVideo       = "video"
	typeUnsupported = "unsupported"
)

var extKinds = map[string]string{
	"txt": typeText, "text": typeText, "md": typeMarkdown, "markdown": typeMarkdown, "html": typeHTML, "htm": typeHTML,
	"docx": typeWord, "doc": typeWord, "odt": typeWord, "rtf": typeWord,
	"xlsx": typeSheet, "csv": typeSheet,
	"pdf": typePDF,
	"jpg": typePhoto, "jpeg": typePhoto, "png": typePhoto, "gif": typePhoto, "heic": typePhoto, "tif": typePhoto, "tiff": typePhoto, "webp": typePhoto,
	"wav": typeAudio, "mp3": typeAudio, "m4a": typeAudio, "aac": typeAudio, "ogg": typeAudio, "flac": typeAudio, "opus": typeAudio,
	"mp4": typeVideo, "mov": typeVideo, "avi": typeVideo, "mkv": typeVideo, "webm": typeVideo, "m4v": typeVideo,
}

func fileKind(name, mediaType string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if k, ok := extKinds[ext]; ok {
		return k
	}
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		return typePhoto
	case strings.HasPrefix(mediaType, "audio/"):
		return typeAudio
	case strings.HasPrefix(mediaType, "video/"):
		return typeVideo
	case mediaType == "application/pdf":
		return typePDF
	case strings.HasPrefix(mediaType, "text/"):
		return typeText
	}
	return typeUnsupported
}

// work is one file being processed.
type work struct {
	job     *Job
	file    files.FileView
	version files.VersionView
	data    []byte
	kind    string
	pdf     *pdfDoc
	matter  MatterInfo
}

// precheck is a step-1 finding that stops the file before any model call.
type precheck struct {
	Kind   string
	Detail string
}

// plan opens the file and lists its units in order. A password-protected, damaged or
// unsupported file returns a precheck instead (PRD 8.21 step 1).
func (s *Service) plan(ctx context.Context, w *work) ([]unitSpec, *precheck, error) {
	switch w.kind {
	case typeText, typeMarkdown, typeHTML:
		if !utf8.Valid(w.data) {
			return nil, &precheck{"damaged", "the text is not valid UTF-8"}, nil
		}
		return []unitSpec{s.textUnit(w)}, nil, nil
	case typeSheet:
		return s.sheetUnits(w)
	case typeWord:
		return s.wordUnits(w)
	case typePDF:
		doc, err := parsePDF(w.data)
		if errors.Is(err, ErrEncrypted) {
			return nil, &precheck{"password_protected", "the PDF is password-protected"}, nil
		}
		if err != nil {
			return nil, &precheck{"damaged", err.Error()}, nil
		}
		w.pdf = doc
		var out []unitSpec
		for k := zero; k < doc.pageCount(); k++ {
			out = append(out, s.pageUnit(w, k))
		}
		return out, nil, nil
	case typePhoto:
		if err := checkPixels(w.data); errors.Is(err, errDecodeLimit) {
			return nil, &precheck{"damaged", err.Error()}, nil
		}
		return []unitSpec{s.photoUnit(w)}, nil, nil
	case typeAudio:
		return []unitSpec{s.transcriptUnit(w, w.data, w.file.Name)}, nil, nil
	case typeVideo:
		return s.videoUnits(ctx, w)
	}
	return nil, &precheck{"unsupported_format", "Doc Tender does not read " + strings.TrimPrefix(path.Ext(w.file.Name), ".") + " files"}, nil
}

func pageName(k int) string { return "page " + strconv.Itoa(k+one) }

// textUnit reads text, Markdown and HTML as they are. Its check is that the bytes read
// are the version's bytes (the SHA-256 was verified on open) and decode as UTF-8.
func (s *Service) textUnit(w *work) unitSpec {
	return unitSpec{Name: "text", Run: func(ctx context.Context, _ bool) (unitWork, error) {
		text := string(w.data)
		method := "read_as_is"
		if w.kind == typeHTML {
			text, method = htmlText(text), "html_text"
		}
		return unitWork{Text: text, Method: method, CheckMethod: "sha256_utf8", CheckResult: "verified " + w.version.SHA256, Passed: utf8.ValidString(text)}, nil
	}}
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reBlock  = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|table|section|article)[^>]*>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
)

func htmlText(s string) string {
	s = reScript.ReplaceAllString(s, "")
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	return tidyLines(html.UnescapeString(s))
}

// ---- spreadsheets: one Markdown table per sheet, checked by row and column counts ----

type sheet struct {
	Name string
	Rows [][]string
	// DeclaredRows and DeclaredCols are the file's own counts, read independently.
	DeclaredRows, DeclaredCols int
}

func (s *Service) sheetUnits(w *work) ([]unitSpec, *precheck, error) {
	var sheets []sheet
	if strings.EqualFold(path.Ext(w.file.Name), ".csv") {
		rows, err := csv.NewReader(bytes.NewReader(w.data)).ReadAll()
		if err != nil {
			return nil, &precheck{"damaged", "the CSV does not parse: " + err.Error()}, nil
		}
		cols := zero
		for _, r := range rows {
			cols = max(cols, len(r))
		}
		lines := zero
		for _, l := range strings.Split(strings.TrimRight(string(w.data), "\r\n"), "\n") {
			if strings.TrimSpace(l) != "" {
				lines++
			}
		}
		sheets = []sheet{{Name: "Sheet 1", Rows: rows, DeclaredRows: lines, DeclaredCols: cols}}
	} else {
		var err error
		sheets, err = readXLSX(w.data)
		if err != nil {
			return nil, &precheck{"damaged", "the workbook does not open: " + err.Error()}, nil
		}
	}
	var out []unitSpec
	for k := range sheets {
		sh := sheets[k]
		out = append(out, unitSpec{Name: "sheet " + strconv.Itoa(k+one), Run: func(ctx context.Context, _ bool) (unitWork, error) {
			table := markdownTable(sh.Rows)
			gotRows, gotCols := tableShape(table)
			ok := gotRows == sh.DeclaredRows && gotCols == sh.DeclaredCols
			return unitWork{
				Text: "## " + sh.Name + "\n\n" + table, Method: "sheet_table", CheckMethod: "row_column_counts",
				CheckResult: fmt.Sprintf("rows %d/%d columns %d/%d", gotRows, sh.DeclaredRows, gotCols, sh.DeclaredCols), Passed: ok,
			}, nil
		}})
	}
	if len(out) == 0 {
		return nil, &precheck{"damaged", "the workbook has no sheets"}, nil
	}
	return out, nil, nil
}

func cellText(c string) string {
	c = strings.ReplaceAll(c, "|", `\|`)
	return strings.Join(strings.Fields(c), " ")
}

// markdownTable renders rows; the first row is the header.
func markdownTable(rows [][]string) string {
	cols := zero
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if len(rows) == 0 || cols == zero {
		return ""
	}
	var b strings.Builder
	line := func(r []string) {
		b.WriteString("|")
		for c := zero; c < cols; c++ {
			v := ""
			if c < len(r) {
				v = cellText(r[c])
			}
			b.WriteString(" " + v + " |")
		}
		b.WriteString("\n")
	}
	line(rows[zero])
	b.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
	for _, r := range rows[one:] {
		line(r)
	}
	return b.String()
}

// tableShape counts a rendered table's data rows (with its header) and columns.
func tableShape(table string) (rows, cols int) {
	for k, l := range strings.Split(strings.TrimSpace(table), "\n") {
		if k == one || l == "" {
			continue
		}
		rows++
		n := strings.Count(strings.ReplaceAll(l, `\|`, ""), "|") - one
		cols = max(cols, n)
	}
	return rows, cols
}

func readXLSX(data []byte) ([]sheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	read := func(name string) ([]byte, error) {
		f, err := zr.Open(name)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readCapped(f)
	}
	var shared []string
	if raw, err := read("xl/sharedStrings.xml"); err == nil {
		var sst struct {
			SI []struct {
				T string `xml:"t"`
				R []struct {
					T string `xml:"t"`
				} `xml:"r"`
			} `xml:"si"`
		}
		if err := xml.Unmarshal(raw, &sst); err != nil {
			return nil, err
		}
		for _, si := range sst.SI {
			t := si.T
			for _, r := range si.R {
				t += r.T
			}
			shared = append(shared, t)
		}
	}
	wbRaw, err := read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wbRaw, &wb); err != nil {
		return nil, err
	}
	targets := map[string]string{}
	if raw, err := read("xl/_rels/workbook.xml.rels"); err == nil {
		var rels struct {
			R []struct {
				ID     string `xml:"Id,attr"`
				Target string `xml:"Target,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(raw, &rels); err != nil {
			return nil, err
		}
		for _, r := range rels.R {
			targets[r.ID] = path.Join("xl", strings.TrimPrefix(r.Target, "/xl/"))
		}
	}
	var out []sheet
	for k, sh := range wb.Sheets {
		name := targets[sh.RID]
		if name == "" {
			name = fmt.Sprintf("xl/worksheets/sheet%d.xml", k+one)
		}
		raw, err := read(name)
		if err != nil {
			return nil, err
		}
		var ws struct {
			Dim struct {
				Ref string `xml:"ref,attr"`
			} `xml:"dimension"`
			Rows []struct {
				Cells []struct {
					Ref    string `xml:"r,attr"`
					Type   string `xml:"t,attr"`
					V      string `xml:"v"`
					Inline string `xml:"is>t"`
				} `xml:"c"`
			} `xml:"sheetData>row"`
		}
		if err := xml.Unmarshal(raw, &ws); err != nil {
			return nil, err
		}
		cur := sheet{Name: sh.Name, DeclaredRows: len(ws.Rows)}
		for _, r := range ws.Rows {
			var row []string
			for _, c := range r.Cells {
				col := columnIndex(c.Ref)
				for len(row) < col {
					row = append(row, "")
				}
				v := c.V
				switch c.Type {
				case "s":
					if n, err := strconv.Atoi(c.V); err == nil && n < len(shared) {
						v = shared[n]
					}
				case "inlineStr":
					v = c.Inline
				}
				if col < len(row) {
					row[col] = v
				} else {
					row = append(row, v)
				}
				cur.DeclaredCols = max(cur.DeclaredCols, col+one)
			}
			cur.Rows = append(cur.Rows, row)
		}
		out = append(out, cur)
	}
	return out, nil
}

// columnIndex reads the column of a cell reference such as C7 (from 0).
func columnIndex(ref string) int {
	n := zero
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*int('Z'-'A'+1) + int(r-'A'+1)
	}
	return n - one
}

// ---- Word-family documents: wordcraft-cli, checked against the document's own counts ----

func (s *Service) wordUnits(w *work) ([]unitSpec, *precheck, error) {
	ext := strings.ToLower(path.Ext(w.file.Name))
	if ext == ".docx" || ext == ".odt" {
		if _, err := zip.NewReader(bytes.NewReader(w.data), int64(len(w.data))); err != nil {
			return nil, &precheck{"damaged", "the document does not open: " + err.Error()}, nil
		}
		if bytes.Contains(w.data, []byte("EncryptedPackage")) {
			return nil, &precheck{"password_protected", "the document is password-protected"}, nil
		}
	}
	if ext == ".doc" && bytes.HasPrefix(w.data, []byte("\xd0\xcf\x11\xe0")) && bytes.Contains(w.data, []byte("EncryptedPackage")) {
		return nil, &precheck{"password_protected", "the document is password-protected"}, nil
	}
	return []unitSpec{{Name: "document", Run: func(ctx context.Context, _ bool) (unitWork, error) {
		if s.d.Docs == nil {
			return unitWork{Method: "wordcraft-cli"}, errors.New("wordcraft-cli is not available in this core")
		}
		md, words, pages, err := s.d.Docs.ToMarkdown(ctx, w.file.Name, w.data)
		if err != nil {
			return unitWork{Method: "wordcraft-cli"}, err
		}
		// The check is independent of the converter: the counts come from the document
		// itself (its properties, or for RTF without them an in-package read of its text).
		// With no independent count the unit fails its check and goes to review; the
		// converter's agreement with its own Markdown is not a check.
		ownWords, ownPages, known := documentCounts(ext, w.data)
		gotWords := len(strings.Fields(stripMarkdown(md)))
		if !known {
			return unitWork{Text: md, Method: "wordcraft-cli", CheckMethod: "word_page_counts", Passed: false,
				CheckResult: fmt.Sprintf("words %d/%d pages %d: the document has no word count of its own to check against", gotWords, words, pages)}, nil
		}
		passed := gotWords == words && words == ownWords && (ownPages == zero || pages == ownPages)
		result := fmt.Sprintf("words %d/%d (document says %d) pages %d (document says %d)", gotWords, words, ownWords, pages, ownPages)
		return unitWork{Text: md, Method: "wordcraft-cli", CheckMethod: "word_page_counts", CheckResult: result, Passed: passed}, nil
	}}}, nil, nil
}

// documentCounts reads a document's own word and page counts: DOCX and ODT from their
// properties, DOC from its OLE SummaryInformation, RTF from its \info group or, when
// it has none, from an in-package read of its text.
func documentCounts(ext string, data []byte) (words, pages int, ok bool) {
	switch ext {
	case ".doc":
		return docCounts(data)
	case ".rtf":
		return rtfCounts(data)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return zero, zero, false
	}
	name := "docProps/app.xml"
	if ext == ".odt" {
		name = "meta.xml"
	}
	f, err := zr.Open(name)
	if err != nil {
		return zero, zero, false
	}
	defer f.Close()
	raw, err := readCapped(f)
	if err != nil {
		return zero, zero, false
	}
	if ext == ".odt" {
		m := regexp.MustCompile(`meta:word-count="(\d+)"`).FindSubmatch(raw)
		p := regexp.MustCompile(`meta:page-count="(\d+)"`).FindSubmatch(raw)
		if m == nil {
			return zero, zero, false
		}
		words, _ = strconv.Atoi(string(m[one]))
		if p != nil {
			pages, _ = strconv.Atoi(string(p[one]))
		}
		return words, pages, true
	}
	var app struct {
		Words *int `xml:"Words"`
		Pages int  `xml:"Pages"`
	}
	if err := xml.Unmarshal(raw, &app); err != nil || app.Words == nil {
		return zero, zero, false
	}
	return *app.Words, app.Pages, true
}

var reMarkdownMarks = regexp.MustCompile(`[#*_>\x60|]+`)

func stripMarkdown(md string) string { return reMarkdownMarks.ReplaceAllString(md, " ") }

// ---- PDF pages ----

const ocrPrompt = "Transcribe every word of text visible on this page exactly, line by line, in reading order. Output only the text, with no commentary. Output nothing if the page has no text."

// pageUnit reads one PDF page. A page with a usable text layer is read as text and
// checked against an OCR read of the same page; a scanned page goes to the OCR model
// and is checked against a second read by Tesseract.
func (s *Service) pageUnit(w *work, k int) unitSpec {
	return unitSpec{Name: pageName(k), Tasks: []string{TaskOCR}, Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		threshold, err := s.threshold(ctx)
		if err != nil {
			return unitWork{}, err
		}
		layer, lerr := w.pdf.pageText(k)
		img, mediaType, hasImage := w.pdf.pageImage(k)
		var pagePart Part
		if hasImage {
			pagePart = Part{MediaType: mediaType, Name: pageName(k), Data: img}
		} else {
			onePage, err := w.pdf.pagePDF(k)
			if err != nil {
				return unitWork{}, err
			}
			pagePart = Part{MediaType: "application/pdf", Name: fmt.Sprintf("page-%d.pdf", k+one), Data: onePage}
		}
		ocr := func(check bool) (string, string, error) {
			resp, err := s.d.Models.Call(ctx, Request{Task: TaskOCR, Check: check, System: ocrPrompt,
				Parts: []Part{{Text: fmt.Sprintf("Page %d of %d.", k+one, w.pdf.pageCount())}, pagePart}, FileID: w.file.ID, MatterID: w.job.MatterID})
			return tidyLines(resp.Text), resp.Provider + "/" + resp.Model, err
		}
		if lerr == nil && usableText(layer) {
			if checkMethod {
				// The retries are spent: the check model reads the page, compared with the layer.
				text, model, err := ocr(true)
				if err != nil {
					return unitWork{Method: "ocr_check_model"}, err
				}
				score := similarity(layer, text)
				return unitWork{Text: text, Method: "ocr_check_model", Model: model, CheckMethod: "text_layer", CheckResult: scoreText(score), Passed: score >= threshold}, nil
			}
			text, model, err := ocr(false)
			if err != nil {
				return unitWork{Method: "text_layer"}, err
			}
			score := similarity(layer, text)
			return unitWork{Text: layer, Method: "text_layer", CheckMethod: "ocr", CheckModel: model, CheckResult: scoreText(score), Passed: score >= threshold}, nil
		}
		// A scanned page: OCR model, checked by Tesseract on the same pixels.
		text, model, err := ocr(checkMethod)
		method := "ocr"
		if checkMethod {
			method = "ocr_check_model"
		}
		if err != nil {
			return unitWork{Method: method, Model: model}, err
		}
		second, secondName, err := s.secondRead(ctx, w, pagePart, hasImage, k)
		if err != nil {
			return unitWork{Method: method, Model: model}, err
		}
		score := similarity(text, second)
		if !usableText(text) && !usableText(second) {
			score = float64(one)
		}
		return unitWork{Text: text, Method: method, Model: model, CheckMethod: secondName, CheckResult: scoreText(score), Passed: score >= threshold}, nil
	}}
}

// secondRead is the independent read of a scanned page: Tesseract on the image, or the
// check model when the page image can't be given to Tesseract.
func (s *Service) secondRead(ctx context.Context, w *work, part Part, image bool, k int) (string, string, error) {
	if image && s.d.Second != nil {
		text, err := s.d.Second.Read(ctx, part.Data)
		return text, s.d.Second.Name(), err
	}
	resp, err := s.d.Models.Call(ctx, Request{Task: TaskOCR, Check: true, System: ocrPrompt,
		Parts: []Part{{Text: fmt.Sprintf("Page %d.", k+one)}, part}, FileID: w.file.ID, MatterID: w.job.MatterID})
	return tidyLines(resp.Text), "ocr_check_model:" + resp.Provider + "/" + resp.Model, err
}

func scoreText(f float64) string { return fmt.Sprintf("agreement %.3f", f) }

// ---- photos ----

const photoPrompt = `Describe this photo for a law firm's file. Answer with JSON: {"description": "what it shows", "text": "every word of text visible in it, line by line, exactly"}.`

type photoRead struct {
	Description string `json:"description"`
	Text        string `json:"text"`
}

func (s *Service) photoUnit(w *work) unitSpec {
	return unitSpec{Name: "photo", Tasks: []string{TaskPhoto}, Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		return s.readPhoto(ctx, w.file.ID, w.job.MatterID, w.file.Name, w.version.MediaType, w.data, checkMethod)
	}}
}

func (s *Service) readPhoto(ctx context.Context, fileID, matter domain.ID, name, mediaType string, data []byte, checkMethod bool) (unitWork, error) {
	threshold, err := s.threshold(ctx)
	if err != nil {
		return unitWork{}, err
	}
	if mediaType == "" || !strings.HasPrefix(mediaType, "image/") {
		mediaType = "image/" + strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	}
	resp, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: checkMethod, System: photoPrompt, JSON: true,
		Parts: []Part{{MediaType: mediaType, Name: name, Data: data}}, FileID: fileID, MatterID: matter})
	model := resp.Provider + "/" + resp.Model
	if err != nil {
		return unitWork{Method: "vision", Model: model}, err
	}
	var read photoRead
	if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &read); err != nil {
		return unitWork{Method: "vision", Model: model, CheckResult: "unreadable answer"}, nil
	}
	read.Text = tidyLines(read.Text)
	// The check: an independent read of the text in the photo.
	var second, checkName string
	if s.d.Second != nil {
		img := data
		if png, err := imageAsPNG(data); err == nil {
			img = png
		}
		second, err = s.d.Second.Read(ctx, img)
		checkName = s.d.Second.Name()
	} else {
		var r Response
		r, err = s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: !checkMethod, System: photoPrompt, JSON: true,
			Parts: []Part{{MediaType: mediaType, Name: name, Data: data}}, FileID: fileID, MatterID: matter})
		var other photoRead
		_ = json.Unmarshal([]byte(jsonObject(r.Text)), &other)
		second, checkName = tidyLines(other.Text), "photo_check_model:"+r.Provider+"/"+r.Model
	}
	if err != nil {
		return unitWork{Method: "vision", Model: model}, err
	}
	score := similarity(read.Text, second)
	if !usableText(read.Text) && !usableText(second) {
		score = float64(one)
	}
	return unitWork{Text: jsonText(read), Method: "vision", Model: model, CheckMethod: checkName, CheckResult: scoreText(score),
		Passed: score >= threshold && strings.TrimSpace(read.Description) != ""}, nil
}

// jsonObject trims a model answer to its outermost JSON object.
func jsonObject(s string) string {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < zero || end < start {
		return s
	}
	return s[start : end+one]
}

// ---- audio ----

type transcript struct {
	Duration float64   `json:"duration"`
	Segments []Segment `json:"segments"`
	Note     string    `json:"note,omitempty"`
}

var reExplained = regexp.MustCompile(`(?i)^\s*[\[(]\s*(silence|silent|music|pause|no speech|inaudible|noise|hold|break|recording paused)[^\])]*[\])]\s*$`)

// coverage is the transcript check: timestamps run the whole recording with no
// unexplained gap longer than maxGap. A segment that says why it is silent explains it.
func coverage(t transcript, maxGap time.Duration) (bool, string) {
	segs := append([]Segment(nil), t.Segments...)
	sort.Slice(segs, func(a, b int) bool { return segs[a].Start < segs[b].Start })
	limit := maxGap.Seconds()
	covered := float64(zero)
	for _, sg := range segs {
		if sg.Start-covered > limit {
			return false, fmt.Sprintf("unexplained gap %s to %s", hms(covered), hms(sg.Start))
		}
		if sg.End > covered {
			covered = sg.End
		}
	}
	if t.Duration-covered > limit {
		return false, fmt.Sprintf("unexplained gap %s to %s", hms(covered), hms(t.Duration))
	}
	return true, fmt.Sprintf("covered %s of %s", hms(covered), hms(t.Duration))
}

func hms(secs float64) string {
	d := time.Duration(secs * float64(secondUnit)).Round(secondUnit)
	h := d / hourUnit
	m := (d % hourUnit) / minuteUnit
	sec := (d % minuteUnit) / secondUnit
	return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
}

// mediaDuration reads a recording's length: natively for WAV, otherwise with ffprobe.
func (s *Service) mediaDuration(ctx context.Context, name string, data []byte) (time.Duration, error) {
	if d, err := wavDuration(data); err == nil {
		return d, nil
	}
	if strings.EqualFold(path.Ext(name), ".avi") {
		if _, total, err := aviFrames(data, hourUnit); err == nil {
			return total, nil
		}
	}
	if s.d.Media == nil {
		return zeroDuration, errors.New("no media tool to read this recording's length")
	}
	p, cleanup, err := tempCopy(name, data)
	if err != nil {
		return zeroDuration, err
	}
	defer cleanup()
	return s.d.Media.Duration(ctx, p)
}

func tempCopy(name string, data []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "doctender-*"+path.Ext(name))
	if err != nil {
		return "", nil, err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return "", nil, errors.Join(werr, cerr)
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

// transcriptUnit transcribes audio, checks coverage, and where a check model is set
// compares a second transcription in doctender.audio_check_window windows.
func (s *Service) transcriptUnit(w *work, audio []byte, name string) unitSpec {
	return unitSpec{Name: "transcript", Tasks: []string{TaskTranscription}, Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
		maxGap, err := s.duration(ctx, "doctender.audio_max_gap")
		if err != nil {
			return unitWork{}, err
		}
		window, err := s.duration(ctx, "doctender.audio_check_window")
		if err != nil {
			return unitWork{}, err
		}
		threshold, err := s.threshold(ctx)
		if err != nil {
			return unitWork{}, err
		}
		length, err := s.mediaDuration(ctx, w.file.Name, w.data)
		if err != nil {
			return unitWork{Method: "speech_to_text"}, err
		}
		if audio == nil {
			t := transcript{Duration: length.Seconds(), Note: "The recording has no audio track."}
			return unitWork{Text: jsonText(t), Method: "speech_to_text", CheckMethod: "no_audio_track", CheckResult: "no audio track", Passed: true}, nil
		}
		resp, err := s.d.Models.Call(ctx, Request{Task: TaskTranscription, Check: checkMethod, Parts: []Part{{MediaType: "audio/wav", Name: name, Data: audio}},
			FileID: w.file.ID, MatterID: w.job.MatterID})
		if checkMethod && errors.Is(err, ErrNoCheckModel) {
			// No second transcription model is set: the last attempt uses the model again,
			// still under the coverage check.
			checkMethod = false
			resp, err = s.d.Models.Call(ctx, Request{Task: TaskTranscription, Parts: []Part{{MediaType: "audio/wav", Name: name, Data: audio}},
				FileID: w.file.ID, MatterID: w.job.MatterID})
		}
		model := resp.Provider + "/" + resp.Model
		if err != nil {
			return unitWork{Method: "speech_to_text", Model: model}, err
		}
		t := transcript{Duration: length.Seconds(), Segments: resp.Segments}
		ok, result := coverage(t, maxGap)
		out := unitWork{Text: jsonText(t), Method: "speech_to_text", Model: model, CheckMethod: "coverage", CheckResult: result, Passed: ok}
		if !ok {
			return out, nil
		}
		second, err := s.d.Models.Call(ctx, Request{Task: TaskTranscription, Check: !checkMethod, Parts: []Part{{MediaType: "audio/wav", Name: name, Data: audio}},
			FileID: w.file.ID, MatterID: w.job.MatterID})
		if errors.Is(err, ErrNoCheckModel) {
			return out, nil
		}
		if err != nil {
			return unitWork{Method: "speech_to_text", Model: model}, err
		}
		worst, at := windowAgreement(resp.Segments, second.Segments, window, t.Duration)
		out.CheckMethod = "coverage+second_transcription:" + second.Provider + "/" + second.Model
		out.CheckResult = result + "; lowest window " + hms(at) + " " + scoreText(worst)
		out.Passed = worst >= threshold
		return out, nil
	}}
}

// windowAgreement compares two transcripts window by window and returns the lowest
// agreement and where it is.
func windowAgreement(a, b []Segment, window time.Duration, total float64) (float64, float64) {
	worst, at := float64(one), float64(zero)
	step := window.Seconds()
	for start := float64(zero); start < total || start == float64(zero); start += step {
		ta, tb := windowText(a, start, start+step), windowText(b, start, start+step)
		score := similarity(ta, tb)
		if !usableText(ta) && !usableText(tb) {
			score = float64(one)
		}
		if score < worst {
			worst, at = score, start
		}
		if step <= float64(zero) {
			break
		}
	}
	return worst, at
}

func windowText(segs []Segment, from, to float64) string {
	var parts []string
	for _, sg := range segs {
		mid := (sg.Start + sg.End) / float64(two)
		if mid >= from && mid < to {
			parts = append(parts, sg.Text)
		}
	}
	return strings.Join(parts, " ")
}

// ---- video: the transcript plus what is on screen, on one timeline ----

type screenFrame struct {
	At          float64 `json:"at"`
	Description string  `json:"description"`
	Text        string  `json:"text"`
}

func (s *Service) videoUnits(ctx context.Context, w *work) ([]unitSpec, *precheck, error) {
	interval, err := s.duration(ctx, "doctender.video_frame_interval")
	if err != nil {
		return nil, nil, err
	}
	window, err := s.duration(ctx, "doctender.audio_check_window")
	if err != nil {
		return nil, nil, err
	}
	var frames []Frame
	var audio []byte
	var total time.Duration
	if strings.EqualFold(path.Ext(w.file.Name), ".avi") {
		frames, audio, total, err = readAVI(w.data, interval)
	} else {
		err = errors.New("not a native format")
	}
	if err != nil {
		if s.d.Media == nil {
			return nil, &precheck{"unsupported_format", "this core has no video reader for " + w.file.Name}, nil
		}
		p, cleanup, terr := tempCopy(w.file.Name, w.data)
		if terr != nil {
			return nil, nil, terr
		}
		defer cleanup()
		if frames, err = s.d.Media.Frames(ctx, p, interval); err != nil {
			return nil, &precheck{"damaged", "the video does not open: " + err.Error()}, nil
		}
		if audio, err = s.d.Media.Audio(ctx, p); err != nil {
			return nil, &precheck{"damaged", "the video's audio does not open: " + err.Error()}, nil
		}
	}
	_ = total
	specs := []unitSpec{s.transcriptUnit(w, audio, strings.TrimSuffix(w.file.Name, path.Ext(w.file.Name))+".wav")}
	if w.file.Source == "zoom" {
		// Zoom recordings get the transcript only.
		return specs, nil, nil
	}
	// One unit per media window of frames.
	var groups [][]Frame
	for _, f := range frames {
		k := int(f.At / window)
		for len(groups) <= k {
			groups = append(groups, nil)
		}
		groups[k] = append(groups[k], f)
	}
	for k := range groups {
		fs := groups[k]
		if len(fs) == 0 {
			continue
		}
		specs = append(specs, unitSpec{Name: "frames " + hms(fs[zero].At.Seconds()), Tasks: []string{TaskPhoto}, Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
			threshold, err := s.threshold(ctx)
			if err != nil {
				return unitWork{}, err
			}
			var out []screenFrame
			worst := float64(one)
			var model, checkModel string
			for _, f := range fs {
				resp, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: checkMethod, System: photoPrompt + " This is one frame of a video.", JSON: true,
					Parts: []Part{{MediaType: "image/png", Name: "frame.png", Data: f.PNG}}, FileID: w.file.ID, MatterID: w.job.MatterID})
				if err != nil {
					return unitWork{Method: "vision_frames"}, err
				}
				model = resp.Provider + "/" + resp.Model
				var read photoRead
				if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &read); err != nil || strings.TrimSpace(read.Description) == "" {
					return unitWork{Method: "vision_frames", Model: model, CheckResult: "no description at " + hms(f.At.Seconds())}, nil
				}
				check, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: !checkMethod, System: photoPrompt + " This is one frame of a video.", JSON: true,
					Parts: []Part{{MediaType: "image/png", Name: "frame.png", Data: f.PNG}}, FileID: w.file.ID, MatterID: w.job.MatterID})
				if err != nil {
					return unitWork{Method: "vision_frames", Model: model}, err
				}
				checkModel = check.Provider + "/" + check.Model
				var other photoRead
				_ = json.Unmarshal([]byte(jsonObject(check.Text)), &other)
				score := similarity(read.Text, other.Text)
				if !usableText(read.Text) && !usableText(other.Text) {
					score = float64(one)
				}
				worst = min(worst, score)
				out = append(out, screenFrame{At: f.At.Seconds(), Description: strings.TrimSpace(read.Description), Text: tidyLines(read.Text)})
			}
			return unitWork{Text: jsonText(out), Method: "vision_frames", Model: model, CheckMethod: "on_screen_text_check_model", CheckModel: checkModel,
				CheckResult: scoreText(worst), Passed: worst >= threshold}, nil
		}})
	}
	return specs, nil, nil
}

// ---- agreement ----

// words normalizes text for comparison: lower case letters and digits only.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// similarity is 1 minus the word-level edit distance over the longer text's length.
func similarity(a, b string) float64 {
	wa, wb := words(a), words(b)
	n := max(len(wa), len(wb))
	if n == zero {
		return float64(one)
	}
	prev := make([]int, len(wb)+one)
	cur := make([]int, len(wb)+one)
	for j := range prev {
		prev[j] = j
	}
	for i := one; i <= len(wa); i++ {
		cur[zero] = i
		for j := one; j <= len(wb); j++ {
			cost := one
			if wa[i-one] == wb[j-one] {
				cost = zero
			}
			cur[j] = min(prev[j]+one, cur[j-one]+one, prev[j-one]+cost)
		}
		prev, cur = cur, prev
	}
	return float64(one) - float64(prev[len(wb)])/float64(n)
}

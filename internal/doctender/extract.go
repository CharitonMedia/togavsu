package doctender

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
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
	// tmp is a copy of the file on disk for ffmpeg, made once per run (mediaPath)
	// and removed when the run ends (cleanup).
	tmp  string
	done func()
}

// mediaPath is the file on disk for ffmpeg, copied the first time it is asked for.
func (w *work) mediaPath() (string, error) {
	if w.tmp == "" {
		p, done, err := tempCopy(w.file.Name, w.data)
		if err != nil {
			return "", err
		}
		w.tmp, w.done = p, done
	}
	return w.tmp, nil
}

func (w *work) cleanup() {
	if w.done != nil {
		w.done()
		w.tmp, w.done = "", nil
	}
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
		audio, pre, err := s.recordingAudio(ctx, w)
		if pre != nil || err != nil {
			return nil, pre, err
		}
		return []unitSpec{s.transcriptUnit(w, audio)}, nil, nil
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
		// Rows of different lengths are common in bank and ledger exports, so they parse.
		cr := csv.NewReader(bytes.NewReader(w.data))
		cr.FieldsPerRecord = -one
		rows, err := cr.ReadAll()
		if err != nil {
			return nil, &precheck{"damaged", "the CSV does not parse: " + err.Error()}, nil
		}
		lines, cols := csvExtent(w.data)
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
				R     string `xml:"r,attr"`
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
		cur := sheet{Name: sh.Name}
		if cur.DeclaredRows, cur.DeclaredCols, err = sheetExtent(raw, ws.Dim.Ref); err != nil {
			return nil, err
		}
		// Every padded row and cell counts toward the sheet's cell cap, checked before
		// it is built: a cell at ZZZZZZZ1 or a row at r="100000000" is refused at once.
		budget := sheetCellCap()
		grid := func(rows, cols int) int64 { return int64(rows) * int64(max(cols, one)) }
		widest := zero
		next := one // the sheet's own row number for a row with no r
		for _, r := range ws.Rows {
			idx := next
			if n, err := strconv.Atoi(r.R); err == nil && n >= one {
				idx = n
			}
			next = idx + one
			if len(r.Cells) == zero {
				// A row with only formatting adds nothing to the table.
				continue
			}
			if grid(idx, widest) > budget {
				return nil, fmt.Errorf("%w: sheet %q reaches row %d", errDecodeLimit, sh.Name, idx)
			}
			var row []string
			pos := zero
			for _, c := range r.Cells {
				// A cell may leave out its reference; it then follows the cell before it.
				col := pos
				if n := columnIndex(c.Ref); n >= zero {
					col = n
				}
				pos = col + one
				if grid(max(idx, len(cur.Rows)+one), max(widest, pos)) > budget {
					return nil, fmt.Errorf("%w: sheet %q reaches column %d", errDecodeLimit, sh.Name, pos)
				}
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
			}
			// Rows the sheet skips (1, 3, 7) stay as empty rows, so each row keeps its place.
			for len(cur.Rows) < idx-one {
				cur.Rows = append(cur.Rows, nil)
			}
			cur.Rows = append(cur.Rows, row)
			widest = max(widest, len(row))
		}
		if grid(len(cur.Rows), widest) > budget {
			return nil, fmt.Errorf("%w: sheet %q is %d rows by %d columns", errDecodeLimit, sh.Name, len(cur.Rows), widest)
		}
		out = append(out, cur)
	}
	return out, nil
}

// sheetExtent is the check's own count of a worksheet's rows and columns. It comes from
// the sheet's <dimension> ref (A1:C7 is 7 rows by 3 columns), which the writing program
// records apart from the cells. A sheet with no dimension has no count of its own, so the
// XML is read a second time token by token, apart from the parse that builds the table.
// Excel records an empty sheet as A1, which reads as no rows.
func sheetExtent(raw []byte, dim string) (rows, cols int, err error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	inData, rowIdx, pos, cells := false, zero, zero, zero
	attr := func(t xml.StartElement, name string) string {
		for _, a := range t.Attr {
			if a.Name.Local == name {
				return a.Value
			}
		}
		return ""
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return zero, zero, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "sheetData":
				inData = true
			case inData && t.Name.Local == "row":
				rowIdx++
				if n, err := strconv.Atoi(attr(t, "r")); err == nil && n >= one {
					rowIdx = n
				}
				pos = zero
			case inData && t.Name.Local == "c":
				col := pos
				if n := columnIndex(attr(t, "r")); n >= zero {
					col = n
				}
				pos = col + one
				cells++
				rows, cols = max(rows, rowIdx), max(cols, pos)
			}
		case xml.EndElement:
			if t.Name.Local == "sheetData" {
				inData = false
			}
		}
	}
	end := strings.ReplaceAll(dim, "$", "")
	if k := strings.LastIndexByte(end, ':'); k >= zero {
		end = end[k+one:]
	}
	r, c := refRow(end), columnIndex(end)+one
	if r < one || c < one || (cells == zero && !strings.Contains(dim, ":")) {
		return rows, cols, nil
	}
	return r, c, nil
}

// refRow reads the row of a cell reference such as C7 (from 1), or 0 when it has none.
func refRow(ref string) int {
	k := strings.IndexFunc(ref, unicode.IsDigit)
	if k < zero {
		return zero
	}
	n, err := strconv.Atoi(ref[k:])
	if err != nil {
		return zero
	}
	return n
}

// csvExtent is the check's own count of a CSV's records and widest record, read from the
// bytes apart from the CSV parser. It follows quotes, so a quoted comma or line break is
// not a new column or row, and skips empty lines as the parser does.
func csvExtent(data []byte) (rows, cols int) {
	inQuote, content, fields := false, false, one
	end := func() {
		if content {
			rows++
			cols = max(cols, fields)
		}
		content, fields = false, one
	}
	for _, b := range data {
		switch {
		case b == '"':
			inQuote, content = !inQuote, true
		case inQuote:
			content = true
		case b == ',':
			fields++
			content = true
		case b == '\n':
			end()
		case b == '\r':
		default:
			content = true
		}
	}
	end()
	return rows, cols
}

// columnIndex reads the column of a cell reference such as C7 (from 0).
// A reference past any real sheet saturates at math.MaxInt32 rather than overflowing,
// so the sheet's cell cap refuses it.
func columnIndex(ref string) int {
	n, letters := zero, int('Z'-'A'+1)
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		if n > math.MaxInt32/letters {
			return math.MaxInt32
		}
		n = n*letters + int(r-'A'+1)
	}
	return n - one
}

// ---- Word-family documents: wordcraft-cli, checked against the document's own counts ----

func (s *Service) wordUnits(w *work) ([]unitSpec, *precheck, error) {
	ext := strings.ToLower(path.Ext(w.file.Name))
	locked := &precheck{"password_protected", "the document is password-protected"}
	// A password-protected DOCX is not a zip: Word wraps it in a compound file with an
	// EncryptedPackage stream. It is checked first, so it is not flagged as damaged.
	if ext == ".docx" || ext == ".odt" || ext == ".doc" {
		if _, ok := cfbStream(w.data, "EncryptedPackage"); ok || (bytes.HasPrefix(w.data, cfbMagic) && bytes.Contains(w.data, []byte("EncryptedPackage"))) {
			return nil, locked, nil
		}
	}
	if ext == ".docx" || ext == ".odt" {
		zr, err := zip.NewReader(bytes.NewReader(w.data), int64(len(w.data)))
		if err != nil {
			return nil, &precheck{"damaged", "the document does not open: " + err.Error()}, nil
		}
		if odfEncrypted(zr, int64(len(w.data))) {
			return nil, locked, nil
		}
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
		if errors.Is(lerr, errDecodeLimit) {
			return unitWork{}, lerr
		}
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
		ocr := func(check, compared bool) (string, string, error) {
			resp, err := s.d.Models.Call(ctx, Request{Task: TaskOCR, Check: check, Compared: compared, System: ocrPrompt,
				Parts: []Part{{Text: fmt.Sprintf("Page %d of %d.", k+one, w.pdf.pageCount())}, pagePart}, FileID: w.file.ID, MatterID: w.job.MatterID})
			return tidyLines(resp.Text), resp.Provider + "/" + resp.Model, err
		}
		if lerr == nil && usableText(layer) {
			if checkMethod {
				// The retries are spent: the check model reads the page, compared with the layer.
				text, model, err := ocr(true, false)
				if err != nil {
					return unitWork{Method: "ocr_check_model"}, err
				}
				score := similarity(layer, text)
				return unitWork{Text: text, Method: "ocr_check_model", Model: model, CheckMethod: "text_layer", CheckResult: scoreText(score), Passed: score >= threshold}, nil
			}
			text, model, err := ocr(false, false)
			if err != nil {
				return unitWork{Method: "text_layer"}, err
			}
			score := similarity(layer, text)
			return unitWork{Text: layer, Method: "text_layer", CheckMethod: "ocr", CheckModel: model, CheckResult: scoreText(score), Passed: score >= threshold}, nil
		}
		// A scanned page: OCR model, checked by Tesseract on the same pixels, or by the
		// other model when the page can't be given to Tesseract.
		text, model, err := ocr(checkMethod, !(hasImage && s.d.Second != nil))
		method := "ocr"
		if checkMethod {
			method = "ocr_check_model"
		}
		if err != nil {
			return unitWork{Method: method, Model: model}, err
		}
		second, secondName, err := s.secondRead(ctx, w, pagePart, hasImage, k)
		if err == nil {
			err = selfCompared(model, strings.TrimPrefix(secondName, "ocr_check_model:"))
		}
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
	resp, err := s.d.Models.Call(ctx, Request{Task: TaskOCR, Check: true, Compared: true, System: ocrPrompt,
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
	resp, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: checkMethod, Compared: s.d.Second == nil, System: photoPrompt, JSON: true,
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
		r, err = s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: !checkMethod, Compared: true, System: photoPrompt, JSON: true,
			Parts: []Part{{MediaType: mediaType, Name: name, Data: data}}, FileID: fileID, MatterID: matter})
		var other photoRead
		_ = json.Unmarshal([]byte(jsonObject(r.Text)), &other)
		second, checkName = tidyLines(other.Text), "photo_check_model:"+r.Provider+"/"+r.Model
		if err == nil {
			err = selfCompared(model, r.Provider+"/"+r.Model)
		}
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
		if v, err := readAVI(data); err == nil {
			return v.total, nil
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

// audioChunks hands a recording to transcription in pieces that each fit one request
// (wire().TranscriptionRequestBytes, under the providers' upload limit), one piece at
// a time, so a long recording is never sent or held whole.
type audioChunks struct {
	none   bool          // the recording has no sound track
	name   string        // the file name sent with each piece
	media  string        // the pieces' media type
	length time.Duration // the recording's length, when known
	step   time.Duration // each piece's length; zero sends the recording as one piece
	read   func(ctx context.Context, from time.Duration) ([]byte, error)
}

func wholeAudio(name, media string, data []byte, length time.Duration) audioChunks {
	return audioChunks{name: name, media: media, length: length, read: func(context.Context, time.Duration) ([]byte, error) { return data, nil }}
}

// wavChunks cuts a WAV held in memory into pieces of at most window, each small enough
// for one request. A WAV that fits one request is sent whole.
func wavChunks(name string, wav []byte, window time.Duration) (audioChunks, error) {
	var f wavFormat
	var pcm []byte
	err := riffChunks(wav, "WAVE", func(id string, body []byte) error {
		switch id {
		case "fmt ":
			return binary.Read(bytes.NewReader(body), binary.LittleEndian, &f)
		case "data":
			pcm = body
		}
		return nil
	})
	if err != nil || f.ByteRate == uint32(zero) || f.BlockAlign == uint16(zero) {
		return audioChunks{}, errors.New("not a PCM WAV")
	}
	rate := float64(f.ByteRate)
	length := time.Duration(float64(len(pcm)) / rate * float64(secondUnit))
	limit := wire().TranscriptionRequestBytes
	if int64(len(wav)) <= limit {
		return wholeAudio(name, "audio/wav", wav, length), nil
	}
	// Each piece is a WAV of its own: its header counts against the request size too.
	room := limit - int64(len(wavBytes(f, nil)))
	step := min(window, time.Duration(float64(room)/rate*float64(secondUnit)))
	if step <= zeroDuration {
		return audioChunks{}, errors.New("the transcription request size is too small for this recording's sound")
	}
	align := int(f.BlockAlign)
	at := func(d time.Duration) int { return min(len(pcm), int(d.Seconds()*rate)/align*align) }
	return audioChunks{name: name, media: "audio/wav", length: length, step: step, read: func(_ context.Context, from time.Duration) ([]byte, error) {
		return wavBytes(f, pcm[at(from):at(from+step)]), nil
	}}, nil
}

// recordingAudio is how an audio file goes to transcription: a WAV cut in memory, any
// other recording that fits one request as it is, and a longer one in pieces that
// ffmpeg cuts from the file one at a time.
func (s *Service) recordingAudio(ctx context.Context, w *work) (audioChunks, *precheck, error) {
	window, err := s.duration(ctx, "doctender.audio_check_window")
	if err != nil {
		return audioChunks{}, nil, err
	}
	if audio, err := wavChunks(w.file.Name, w.data, window); err == nil {
		return audio, nil, nil
	}
	if int64(len(w.data)) <= wire().TranscriptionRequestBytes {
		return wholeAudio(w.file.Name, w.version.MediaType, w.data, zeroDuration), nil, nil
	}
	if s.d.Media == nil {
		return audioChunks{}, &precheck{"unsupported_format", "the recording is larger than one transcription request takes, and this core has no ffmpeg to cut it into pieces"}, nil
	}
	return s.ffmpegAudio(ctx, w, window, strings.TrimSuffix(w.file.Name, path.Ext(w.file.Name))+".wav")
}

// ffmpegAudio reads the file's length and sound track with ffmpeg and cuts the sound
// one piece per request, each when it is sent.
func (s *Service) ffmpegAudio(ctx context.Context, w *work, window time.Duration, name string) (audioChunks, *precheck, error) {
	p, err := w.mediaPath()
	if err != nil {
		return audioChunks{}, nil, err
	}
	length, err := s.d.Media.Duration(ctx, p)
	if pre, err := mediaTrouble(ctx, err, w.file.Name); pre != nil || err != nil {
		return audioChunks{}, pre, err
	}
	has, err := s.d.Media.HasAudio(ctx, p)
	if pre, err := mediaTrouble(ctx, err, w.file.Name); pre != nil || err != nil {
		return audioChunks{}, pre, err
	}
	if !has {
		return audioChunks{none: true, length: length}, nil, nil
	}
	// ffmpeg writes mono 16-bit PCM at the speech rate, plus a header of its own.
	rate := float64(wire().SpeechSampleRate * two)
	room := wire().TranscriptionRequestBytes - int64(len(wavBytes(wavFormat{}, nil)))
	step := min(window, time.Duration(float64(room)/rate*float64(secondUnit)))
	return audioChunks{name: name, media: "audio/wav", length: length, step: step, read: func(ctx context.Context, from time.Duration) ([]byte, error) {
		p, err := w.mediaPath()
		if err != nil {
			return nil, err
		}
		return s.d.Media.Audio(ctx, p, from, step)
	}}, nil, nil
}

// mediaTrouble sorts a media tool's error. A missing or unrunnable ffmpeg is not the
// file's fault: the file is held as unsupported, saying so, until ffmpeg is installed
// and the item retried. A stopped run is retried. Only ffmpeg's own refusal of the
// file marks it damaged.
func mediaTrouble(ctx context.Context, err error, name string) (*precheck, error) {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil, nil
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		return &precheck{"unsupported_format", name + " was not read: this core's ffmpeg is missing or cannot run (" + err.Error() + "). The file is not damaged; install ffmpeg, then use Retry."}, nil
	case ctx.Err() != nil:
		return nil, err
	case errors.As(err, &exit):
		return &precheck{"damaged", name + " does not open: " + err.Error()}, nil
	}
	return nil, err
}

// heard is one transcription pass over every piece: the segments on the recording's
// own clock, and the model that heard each piece.
type heard struct {
	segments []Segment
	models   []string
}

func (h heard) model() string {
	var out []string
	for _, m := range h.models {
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return strings.Join(out, "+")
}

// selfCompared refuses a comparison in which one model heard a piece twice.
func (h heard) selfCompared(other heard) error {
	for k := range min(len(h.models), len(other.models)) {
		if err := selfCompared(h.models[k], other.models[k]); err != nil {
			return err
		}
	}
	return nil
}

// transcribe runs one pass, a piece at a time, putting each piece's segments on the
// recording's clock.
func (s *Service) transcribe(ctx context.Context, w *work, audio audioChunks, check, compared bool) (heard, error) {
	var h heard
	for from := zeroDuration; ; from += audio.step {
		data, err := audio.read(ctx, from)
		if err != nil {
			return h, err
		}
		resp, err := s.d.Models.Call(ctx, Request{Task: TaskTranscription, Check: check, Compared: compared, Parts: []Part{{MediaType: audio.media, Name: audio.name, Data: data}},
			FileID: w.file.ID, MatterID: w.job.MatterID})
		if err != nil {
			return h, err
		}
		h.models = append(h.models, resp.Provider+"/"+resp.Model)
		for _, sg := range resp.Segments {
			sg.Start += from.Seconds()
			sg.End += from.Seconds()
			h.segments = append(h.segments, sg)
		}
		if audio.step <= zeroDuration || from+audio.step >= audio.length {
			return h, nil
		}
	}
}

// transcriptUnit transcribes audio, checks coverage, and where a check model is set
// compares a second transcription in doctender.audio_check_window windows.
func (s *Service) transcriptUnit(w *work, audio audioChunks) unitSpec {
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
		length := audio.length
		if length <= zeroDuration {
			if length, err = s.mediaDuration(ctx, w.file.Name, w.data); err != nil {
				return unitWork{Method: "speech_to_text"}, err
			}
		}
		if audio.none {
			t := transcript{Duration: length.Seconds(), Note: "The recording has no audio track."}
			return unitWork{Text: jsonText(t), Method: "speech_to_text", CheckMethod: "no_audio_track", CheckResult: "no audio track", Passed: true}, nil
		}
		first, err := s.transcribe(ctx, w, audio, checkMethod, true)
		if checkMethod && errors.Is(err, ErrNoCheckModel) {
			// No second transcription model is set: the last attempt uses the model again,
			// still under the coverage check.
			checkMethod = false
			first, err = s.transcribe(ctx, w, audio, false, false)
		}
		model := first.model()
		if err != nil {
			return unitWork{Method: "speech_to_text", Model: model}, err
		}
		t := transcript{Duration: length.Seconds(), Segments: first.segments}
		ok, result := coverage(t, maxGap)
		out := unitWork{Text: jsonText(t), Method: "speech_to_text", Model: model, CheckMethod: "coverage", CheckResult: result, Passed: ok}
		if !ok {
			return out, nil
		}
		second, err := s.transcribe(ctx, w, audio, !checkMethod, true)
		if errors.Is(err, ErrNoCheckModel) {
			return out, nil
		}
		if err == nil {
			err = first.selfCompared(second)
		}
		if err != nil {
			return unitWork{Method: "speech_to_text", Model: model}, err
		}
		worst, at := windowAgreement(first.segments, second.segments, window, t.Duration)
		out.CheckMethod = "coverage+second_transcription:" + second.model()
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

// videoUnits reads a video the way the earlier Doc Tender did, a stretch at a time:
// the sound in pieces for transcription, and one unit per doctender.audio_check_window
// of frames, each window's frames read when its unit runs and described one at a
// time, so an hour-long recording never holds more than one frame decoded.
func (s *Service) videoUnits(ctx context.Context, w *work) ([]unitSpec, *precheck, error) {
	interval, err := s.duration(ctx, "doctender.video_frame_interval")
	if err != nil {
		return nil, nil, err
	}
	window, err := s.duration(ctx, "doctender.audio_check_window")
	if err != nil {
		return nil, nil, err
	}
	wavName := strings.TrimSuffix(w.file.Name, path.Ext(w.file.Name)) + ".wav"
	var audio audioChunks
	var total time.Duration
	var frames func(ctx context.Context, from, to time.Duration, each func(Frame) error) error
	var avi *aviVideo
	if strings.EqualFold(path.Ext(w.file.Name), ".avi") {
		avi, _ = readAVI(w.data)
	}
	switch {
	case avi != nil:
		total = avi.total
		audio = audioChunks{none: true, length: total}
		if avi.audio != nil {
			if audio, err = wavChunks(wavName, avi.audio, window); err != nil {
				return nil, &precheck{"damaged", "the video's sound track does not open: " + err.Error()}, nil
			}
		}
		frames = func(_ context.Context, from, to time.Duration, each func(Frame) error) error {
			return avi.frames(from, to, interval, each)
		}
	case s.d.Media == nil:
		return nil, &precheck{"unsupported_format", "this core has no video reader for " + w.file.Name}, nil
	default:
		var pre *precheck
		if audio, pre, err = s.ffmpegAudio(ctx, w, window, wavName); pre != nil || err != nil {
			return nil, pre, err
		}
		total = audio.length
		frames = func(ctx context.Context, from, to time.Duration, each func(Frame) error) error {
			p, err := w.mediaPath()
			if err != nil {
				return err
			}
			return s.d.Media.Frames(ctx, p, from, to-from, interval, each)
		}
	}
	specs := []unitSpec{s.transcriptUnit(w, audio)}
	if w.file.Source == "zoom" {
		// Zoom recordings get the transcript only.
		return specs, nil, nil
	}
	for from := zeroDuration; from < total; from += window {
		to := min(from+window, total)
		specs = append(specs, unitSpec{Name: "frames " + hms(from.Seconds()), Tasks: []string{TaskPhoto}, Run: func(ctx context.Context, checkMethod bool) (unitWork, error) {
			threshold, err := s.threshold(ctx)
			if err != nil {
				return unitWork{}, err
			}
			var out []screenFrame
			worst := float64(one)
			var model, checkModel string
			var unread *unitWork
			err = frames(ctx, from, to, func(f Frame) error {
				name := "frame.png"
				if f.MediaType == "image/jpeg" {
					name = "frame.jpg"
				}
				part := Part{MediaType: f.MediaType, Name: name, Data: f.Data}
				resp, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: checkMethod, Compared: true, System: photoPrompt + " This is one frame of a video.", JSON: true,
					Parts: []Part{part}, FileID: w.file.ID, MatterID: w.job.MatterID})
				if err != nil {
					return err
				}
				model = resp.Provider + "/" + resp.Model
				var read photoRead
				if err := json.Unmarshal([]byte(jsonObject(resp.Text)), &read); err != nil || strings.TrimSpace(read.Description) == "" {
					unread = &unitWork{Method: "vision_frames", Model: model, CheckResult: "no description at " + hms(f.At.Seconds())}
					return errFramesDone
				}
				check, err := s.d.Models.Call(ctx, Request{Task: TaskPhoto, Check: !checkMethod, Compared: true, System: photoPrompt + " This is one frame of a video.", JSON: true,
					Parts: []Part{part}, FileID: w.file.ID, MatterID: w.job.MatterID})
				if err == nil {
					err = selfCompared(model, check.Provider+"/"+check.Model)
				}
				if err != nil {
					return err
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
				return nil
			})
			if unread != nil {
				return *unread, nil
			}
			if err != nil {
				return unitWork{Method: "vision_frames", Model: model}, err
			}
			return unitWork{Text: jsonText(out), Method: "vision_frames", Model: model, CheckMethod: "on_screen_text_check_model", CheckModel: checkModel,
				CheckResult: scoreText(worst), Passed: worst >= threshold}, nil
		}})
	}
	return specs, nil, nil
}

// errFramesDone stops a window's frames early once one has no description.
var errFramesDone = errors.New("doctender: frames stopped")

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

// odfEncrypted reports an OpenDocument package whose manifest lists encryption data,
// which is how a password-protected ODT stays a zip. The read is bounded by the size
// of the file itself.
func odfEncrypted(zr *zip.Reader, limit int64) bool {
	for _, f := range zr.File {
		if f.Name != "META-INF/manifest.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false
		}
		defer rc.Close()
		body, err := io.ReadAll(io.LimitReader(rc, limit))
		return err == nil && bytes.Contains(body, []byte("encryption-data"))
	}
	return false
}

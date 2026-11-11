package help

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// pdfLayout is the guide PDF's fixed geometry and font metrics (pdf_layout.json).
type pdfLayout struct {
	PageWidth      float64 `json:"page_width"`
	PageHeight     float64 `json:"page_height"`
	Margin         float64 `json:"margin"`
	LineHeight     float64 `json:"line_height"`
	BodySize       float64 `json:"body_size"`
	ChapterSize    float64 `json:"chapter_size"`
	TopicSize      float64 `json:"topic_size"`
	WhereSize      float64 `json:"where_size"`
	TitleSize      float64 `json:"title_size"`
	VersionSize    float64 `json:"version_size"`
	TitleTop       float64 `json:"title_top"`
	VersionTop     float64 `json:"version_top"`
	NoteTop        float64 `json:"note_top"`
	TopicGap       float64 `json:"topic_gap"`
	WhereGap       float64 `json:"where_gap"`
	ParaGap        float64 `json:"para_gap"`
	KeepWithNext   float64 `json:"keep_with_next"`
	TOCPerPage     int     `json:"toc_per_page"`
	TOCHeadGap     float64 `json:"toc_head_gap"`
	TOCHeadAfter   float64 `json:"toc_head_after"`
	TOCIndent      float64 `json:"toc_indent"`
	LinkBelow      float64 `json:"link_below"`
	LinkAbove      float64 `json:"link_above"`
	DestAbove      float64 `json:"dest_above"`
	FooterSize     float64 `json:"footer_size"`
	FooterY        float64 `json:"footer_y"`
	FooterOffset   float64 `json:"footer_offset"`
	BoldSlack      float64 `json:"bold_slack"`
	Em             float64 `json:"em"`
	FirstChar      rune    `json:"first_char"`
	LastChar       rune    `json:"last_char"`
	FallbackWidth  int     `json:"fallback_width"`
	Widths         []int   `json:"widths"`
	FirstObject    int     `json:"first_object"`
	ObjectsPerPage int     `json:"objects_per_page"`
	OutlinesObject int     `json:"outlines_object"`
}

//go:embed pdf_layout.json
var pdfLayoutJSON []byte

var layout = func() pdfLayout {
	var l pdfLayout
	if err := json.Unmarshal(pdfLayoutJSON, &l); err != nil || len(l.Widths) != int(l.LastChar-l.FirstChar)+1 {
		panic(fmt.Sprintf("help: pdf_layout.json is invalid: %v", err))
	}
	return l
}()

// GuidePDF renders the guide as a PDF with a title page, its own table of contents
// (page numbers that link to each topic) and PDF bookmarks, for printing or reading
// offline. It uses the standard Helvetica fonts, so nothing is embedded.
func (g *Guide) GuidePDF(version string) []byte {
	L := layout
	type line struct {
		text string
		size float64
		bold bool
		gap  float64
	}
	type entry struct {
		title string
		sub   bool
		page  int
		y     float64
	}
	// Lay out the content once to learn each topic's page.
	var pages [][]placed
	var entries []entry
	var cur []placed
	top := L.PageHeight - L.Margin
	y := top
	newPage := func() {
		pages = append(pages, cur)
		cur, y = nil, top
	}
	put := func(l line) {
		h := L.LineHeight * l.size / L.BodySize
		if y-h-l.gap < L.Margin {
			newPage()
		} else {
			y -= l.gap
		}
		y -= h
		cur = append(cur, placed{x: L.Margin, y: y, size: l.size, bold: l.bold, text: l.text})
	}
	width := L.PageWidth - L.Margin - L.Margin
	for _, ch := range g.Chapters {
		if len(cur) > 0 { // every chapter starts a page
			newPage()
		}
		entries = append(entries, entry{title: ch.Title, page: len(pages), y: y})
		put(line{text: ch.Title, size: L.ChapterSize, bold: true})
		for _, para := range paragraphs(ch.Intro) {
			for _, w := range wrapText(para, L.BodySize, width) {
				put(line{text: w, size: L.BodySize})
			}
			y -= L.ParaGap
		}
		for _, t := range ch.Topics {
			if y-L.KeepWithNext < L.Margin {
				newPage()
			}
			entries = append(entries, entry{title: t.Title, sub: true, page: len(pages), y: y})
			put(line{text: t.Title, size: L.TopicSize, bold: true, gap: L.TopicGap})
			if t.Where != "" {
				put(line{text: "Where: " + t.Where, size: L.WhereSize, gap: L.WhereGap})
			}
			for _, para := range paragraphs(t.Markdown) {
				opening := true // only a paragraph's first line takes the gap
				for _, w := range wrapText(para, L.BodySize, width) {
					var gap float64
					if opening {
						gap = L.ParaGap
					}
					put(line{text: w, size: L.BodySize, gap: gap})
					opening = false
				}
			}
		}
	}
	pages = append(pages, cur)

	// Contents pages follow the title page; content pages follow them. Page numbers
	// printed in the contents are 1-based.
	tocPages := (len(entries) + L.TOCPerPage - 1) / L.TOCPerPage
	first := 1 + tocPages
	var toc [][]placed
	var tocLinks [][]link
	for i := 0; i < len(entries); i += L.TOCPerPage {
		var pg []placed
		var ls []link
		ty := top
		if len(toc) == 0 {
			ty -= L.TOCHeadGap
			pg = append(pg, placed{x: L.Margin, y: ty, size: L.ChapterSize, bold: true, text: "Contents"})
			ty -= L.TOCHeadAfter
		}
		for _, e := range entries[i:min(i+L.TOCPerPage, len(entries))] {
			ty -= L.LineHeight
			x := L.Margin
			if e.sub {
				x += L.TOCIndent
			}
			num := fmt.Sprint(first + e.page + 1)
			pg = append(pg, placed{x: x, y: ty, size: L.BodySize, bold: !e.sub, text: e.title})
			pg = append(pg, placed{x: L.PageWidth - L.Margin - textWidth(num, L.BodySize), y: ty, size: L.BodySize, text: num})
			ls = append(ls, link{x1: L.Margin, y1: ty - L.LinkBelow, x2: L.PageWidth - L.Margin, y2: ty + L.LinkAbove, page: first + e.page, top: e.y})
		}
		toc = append(toc, pg)
		tocLinks = append(tocLinks, ls)
	}
	title := []placed{
		{x: L.Margin, y: L.PageHeight - L.TitleTop, size: L.TitleSize, bold: true, text: "Togavsu user guide"},
		{x: L.Margin, y: L.PageHeight - L.VersionTop, size: L.VersionSize, text: "Version " + version},
		{x: L.Margin, y: L.PageHeight - L.NoteTop, size: L.BodySize, text: "Staff only. This guide describes a system that holds client data."},
	}
	all := append([][]placed{title}, toc...)
	all = append(all, pages...)
	links := append([][]link{nil}, tocLinks...)
	for len(links) < len(all) {
		links = append(links, nil)
	}
	var marks []bookmark
	for _, e := range entries {
		marks = append(marks, bookmark{title: e.title, sub: e.sub, page: first + e.page, top: e.y})
	}
	return writePDF(all, links, marks)
}

type placed struct {
	x, y, size float64
	bold       bool
	text       string
}

type link struct {
	x1, y1, x2, y2 float64
	page           int
	top            float64
}

type bookmark struct {
	title string
	sub   bool
	page  int
	top   float64
}

func paragraphs(md string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, " "))
			cur = nil
		}
	}
	for _, l := range strings.Split(PlainText(md), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			flush()
			continue
		}
		if bullet, ok := strings.CutPrefix(t, "- "); ok {
			flush()
			t = "* " + bullet
		} else if mdNum.MatchString(t) {
			flush()
		}
		cur = append(cur, t)
	}
	flush()
	return out
}

func charWidth(r rune) int {
	if r < layout.FirstChar || r > layout.LastChar {
		return layout.FallbackWidth
	}
	return layout.Widths[r-layout.FirstChar]
}

func textWidth(s string, size float64) float64 {
	var w int
	for _, r := range ascii(s) {
		w += charWidth(r)
	}
	return float64(w) * size / layout.Em
}

func wrapText(s string, size, width float64) []string {
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		next := word
		if cur != "" {
			next = cur + " " + word
		}
		// Bold runs are wider than regular; leave some slack.
		if textWidth(next, size)*layout.BoldSlack > width && cur != "" {
			out = append(out, cur)
			cur = word
			continue
		}
		cur = next
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// ascii maps the punctuation the guide uses onto the font's ASCII range.
func ascii(s string) string {
	r := strings.NewReplacer("‘", "'", "’", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", "…", "...", "·", "-", " ", " ")
	s = r.Replace(s)
	var b strings.Builder
	for _, c := range s {
		if c >= layout.FirstChar && c <= layout.LastChar {
			b.WriteRune(c)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

func pdfString(s string) string {
	return "(" + strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`).Replace(ascii(s)) + ")"
}

func refs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d 0 R", id)
	}
	return strings.Join(parts, " ")
}

// siblings links each bookmark to the one before and after it in its list.
func siblings(list []int, prev, next map[int]int) {
	var last int
	seen := false
	for _, id := range list {
		if seen {
			prev[id], next[last] = last, id
		}
		last, seen = id, true
	}
}

func writePDF(pages [][]placed, links [][]link, marks []bookmark) []byte {
	L := layout
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}
	buf.WriteString("%PDF-1.4\n")
	// Object numbers: catalog, pages, regular font, bold font, outlines; then each page
	// and its contents, then link annotations, then bookmarks.
	pageObj := func(i int) int { return L.FirstObject + i*L.ObjectsPerPage }
	nextFree := pageObj(len(pages))
	annotObjs := make([][]int, len(pages))
	for i := range pages {
		for range links[i] {
			annotObjs[i] = append(annotObjs[i], nextFree)
			nextFree++
		}
	}
	markObj := func(i int) int { return nextFree + i }
	var kids []int
	for i := range pages {
		kids = append(kids, pageObj(i))
	}
	obj(fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /Outlines %d 0 R /PageMode /UseOutlines >>", L.OutlinesObject))
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", len(pages), refs(kids)))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	// Chapters are top-level bookmarks; topics nest under the chapter before them.
	var tops []int
	parentOf := map[int]int{}
	childrenOf := map[int][]int{}
	for i := range marks {
		if !marks[i].sub || len(tops) == 0 {
			marks[i].sub = false
			tops = append(tops, i)
			continue
		}
		p := tops[len(tops)-1]
		parentOf[i] = p
		childrenOf[p] = append(childrenOf[p], i)
	}
	prev, next := map[int]int{}, map[int]int{}
	siblings(tops, prev, next)
	for _, kids := range childrenOf {
		siblings(kids, prev, next)
	}
	if len(tops) > 0 {
		obj(fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", markObj(tops[0]), markObj(tops[len(tops)-1]), len(tops)))
	} else {
		obj("<< /Type /Outlines /Count 0 >>")
	}
	for i, pg := range pages {
		annots := ""
		if len(annotObjs[i]) > 0 {
			annots = " /Annots [" + refs(annotObjs[i]) + "]"
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R%s >>", L.PageWidth, L.PageHeight, pageObj(i)+1, annots))
		var c strings.Builder
		for _, t := range pg {
			font := "F1"
			if t.bold {
				font = "F2"
			}
			fmt.Fprintf(&c, "BT /%s %.1f Tf %.2f %.2f Td %s Tj ET\n", font, t.size, t.x, t.y, pdfString(t.text))
		}
		fmt.Fprintf(&c, "BT /F1 %.1f Tf %.2f %.2f Td %s Tj ET\n", L.FooterSize, L.PageWidth/2-L.FooterOffset, L.FooterY, pdfString(fmt.Sprint(i+1)))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()))
	}
	for i := range pages {
		for _, l := range links[i] {
			obj(fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [%.1f %.1f %.1f %.1f] /Border [0 0 0] /Dest [%d 0 R /XYZ 0 %.1f 0] >>",
				l.x1, l.y1, l.x2, l.y2, pageObj(l.page), l.top+L.DestAbove))
		}
	}
	for i, m := range marks {
		parent := L.OutlinesObject
		if m.sub {
			parent = markObj(parentOf[i])
		}
		extra := ""
		if p, ok := prev[i]; ok {
			extra += fmt.Sprintf(" /Prev %d 0 R", markObj(p))
		}
		if n, ok := next[i]; ok {
			extra += fmt.Sprintf(" /Next %d 0 R", markObj(n))
		}
		if kids := childrenOf[i]; len(kids) > 0 {
			extra += fmt.Sprintf(" /First %d 0 R /Last %d 0 R /Count -%d", markObj(kids[0]), markObj(kids[len(kids)-1]), len(kids))
		}
		obj(fmt.Sprintf("<< /Title %s /Parent %d 0 R /Dest [%d 0 R /XYZ 0 %.1f 0]%s >>", pdfString(m.title), parent, pageObj(m.page), m.top+L.DestAbove, extra))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

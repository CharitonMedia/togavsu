package help

import (
	"bytes"
	"fmt"
	"strings"
)

// GuidePDF renders the guide as a PDF with a title page, its own table of contents
// (page numbers that link to each topic) and PDF bookmarks, for printing or reading
// offline. It uses the standard Helvetica fonts, so nothing is embedded.
func (g *Guide) GuidePDF(version string) []byte {
	const (
		pageW, pageH = 612.0, 792.0
		margin       = 64.0
		lineH        = 14.0
		bodySize     = 10.5
	)
	type line struct {
		text string
		size float64
		bold bool
		gap  float64
	}
	type entry struct {
		title string
		level int
		page  int
		y     float64
	}
	// Lay out the content once to learn each topic's page.
	var pages [][]placed
	var entries []entry
	cur := []placed{}
	y := pageH - margin
	newPage := func() {
		pages = append(pages, cur)
		cur = []placed{}
		y = pageH - margin
	}
	put := func(l line) {
		h := lineH * l.size / bodySize
		if y-h-l.gap < margin {
			newPage()
		} else {
			y -= l.gap
		}
		y -= h
		cur = append(cur, placed{x: margin, y: y, size: l.size, bold: l.bold, text: l.text})
	}
	width := pageW - 2*margin
	for ci, ch := range g.Chapters {
		if ci > 0 {
			newPage()
		}
		entries = append(entries, entry{title: ch.Title, level: 0, page: len(pages), y: y})
		put(line{text: ch.Title, size: 18, bold: true})
		for _, para := range paragraphs(ch.Intro) {
			for _, w := range wrapText(para, bodySize, width) {
				put(line{text: w, size: bodySize})
			}
			y -= 4
		}
		for _, t := range ch.Topics {
			if y-80 < margin {
				newPage()
			}
			entries = append(entries, entry{title: t.Title, level: 1, page: len(pages), y: y})
			put(line{text: t.Title, size: 13, bold: true, gap: 10})
			if t.Where != "" {
				put(line{text: "Where: " + t.Where, size: 9.5, gap: 2})
			}
			for _, para := range paragraphs(t.Markdown) {
				for i, w := range wrapText(para, bodySize, width) {
					gap := 0.0
					if i == 0 {
						gap = 4
					}
					put(line{text: w, size: bodySize, gap: gap})
				}
			}
		}
	}
	pages = append(pages, cur)

	// Table of contents pages come after the title page.
	tocPerPage := 44
	tocPages := (len(entries) + tocPerPage - 1) / tocPerPage
	first := 1 + tocPages // index of the first content page
	var toc [][]placed
	var tocLinks [][]link
	for i := 0; i < len(entries); i += tocPerPage {
		var pg []placed
		var ls []link
		ty := pageH - margin
		if i == 0 {
			ty -= 22
			pg = append(pg, placed{x: margin, y: ty, size: 18, bold: true, text: "Contents"})
			ty -= 18
		}
		for _, e := range entries[i:min(i+tocPerPage, len(entries))] {
			ty -= lineH
			indent := 0.0
			if e.level == 1 {
				indent = 16
			}
			num := fmt.Sprint(first + e.page + 1)
			pg = append(pg, placed{x: margin + indent, y: ty, size: 10.5, bold: e.level == 0, text: e.title})
			pg = append(pg, placed{x: pageW - margin - textWidth(num, 10.5), y: ty, size: 10.5, text: num})
			ls = append(ls, link{x1: margin, y1: ty - 3, x2: pageW - margin, y2: ty + 11, page: first + e.page, top: e.y})
		}
		toc = append(toc, pg)
		tocLinks = append(tocLinks, ls)
	}
	title := []placed{
		{x: margin, y: pageH - 220, size: 28, bold: true, text: "Togavsu user guide"},
		{x: margin, y: pageH - 252, size: 13, text: "Version " + version},
		{x: margin, y: pageH - 274, size: 10.5, text: "Staff only. This guide describes a system that holds client data."},
	}
	all := append([][]placed{title}, toc...)
	all = append(all, pages...)
	links := make([][]link, len(all))
	for i, ls := range tocLinks {
		links[1+i] = ls
	}
	var marks []bookmark
	for _, e := range entries {
		marks = append(marks, bookmark{title: e.title, level: e.level, page: first + e.page, top: e.y})
	}
	return writePDF(all, links, marks, pageW, pageH)
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
	level int
	page  int
	top   float64
}

func paragraphs(md string) []string {
	var out []string
	var cur []string
	for _, l := range strings.Split(PlainText(md), "\n") {
		t := strings.TrimSpace(l)
		if t == "" {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, " "))
				cur = nil
			}
			continue
		}
		if strings.HasPrefix(t, "- ") || mdNum.MatchString(t) {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, " "))
				cur = nil
			}
			if strings.HasPrefix(t, "- ") {
				t = "* " + t[2:]
			}
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}
	return out
}

// helvetica holds the standard Helvetica advance widths for ASCII 32-126 (1/1000 em).
var helvetica = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
	667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
	556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

func textWidth(s string, size float64) float64 {
	w := 0
	for _, r := range ascii(s) {
		if r >= 32 && r <= 126 {
			w += helvetica[r-32]
		} else {
			w += 556
		}
	}
	return float64(w) * size / 1000
}

func wrapText(s string, size, width float64) []string {
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		next := word
		if cur != "" {
			next = cur + " " + word
		}
		// Bold runs are wider than regular; leave 6% slack.
		if textWidth(next, size)*1.06 > width && cur != "" {
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
		if c >= 32 && c <= 126 {
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

func writePDF(pages [][]placed, links [][]link, marks []bookmark, w, h float64) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string) int {
		offsets = append(offsets, buf.Len())
		n := len(offsets)
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", n, body)
		return n
	}
	buf.WriteString("%PDF-1.4\n")
	// Fixed object numbers: 1 catalog, 2 pages, 3 regular font, 4 bold font, 5 outlines.
	// Page objects come next, then their contents, annotations and bookmarks.
	n := len(pages)
	pageObj := func(i int) int { return 6 + i*2 }
	contentObj := func(i int) int { return 7 + i*2 }
	nextFree := 6 + n*2
	annotObjs := make([][]int, n)
	for i := range pages {
		for range links[i] {
			annotObjs[i] = append(annotObjs[i], nextFree)
			nextFree++
		}
	}
	markObj := func(i int) int { return nextFree + i }

	obj("<< /Type /Catalog /Pages 2 0 R /Outlines 5 0 R /PageMode /UseOutlines >>")
	kids := make([]string, n)
	for i := range pages {
		kids[i] = fmt.Sprintf("%d 0 R", pageObj(i))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, strings.Join(kids, " ")))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	// Top-level bookmarks are chapters; topics nest under them.
	var tops []int
	parentOf := make([]int, len(marks))
	childrenOf := map[int][]int{}
	last := -1
	for i, m := range marks {
		if m.level == 0 || last < 0 {
			tops = append(tops, i)
			last = i
			parentOf[i] = -1
		} else {
			parentOf[i] = last
			childrenOf[last] = append(childrenOf[last], i)
		}
	}
	if len(tops) > 0 {
		obj(fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", markObj(tops[0]), markObj(tops[len(tops)-1]), len(tops)))
	} else {
		obj("<< /Type /Outlines /Count 0 >>")
	}
	for i, pg := range pages {
		annots := ""
		if len(annotObjs[i]) > 0 {
			refs := make([]string, len(annotObjs[i]))
			for j, a := range annotObjs[i] {
				refs[j] = fmt.Sprintf("%d 0 R", a)
			}
			annots = " /Annots [" + strings.Join(refs, " ") + "]"
		}
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R%s >>", w, h, contentObj(i), annots))
		var c strings.Builder
		for _, t := range pg {
			font := "F1"
			if t.bold {
				font = "F2"
			}
			fmt.Fprintf(&c, "BT /%s %.1f Tf %.2f %.2f Td %s Tj ET\n", font, t.size, t.x, t.y, pdfString(t.text))
		}
		if i > 0 {
			fmt.Fprintf(&c, "BT /F1 9 Tf %.2f 36 Td %s Tj ET\n", w/2-8, pdfString(fmt.Sprint(i+1)))
		}
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", c.Len(), c.String()))
	}
	for i := range pages {
		for _, l := range links[i] {
			obj(fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [%.1f %.1f %.1f %.1f] /Border [0 0 0] /Dest [%d 0 R /XYZ 0 %.1f 0] >>",
				l.x1, l.y1, l.x2, l.y2, pageObj(l.page), l.top+14))
		}
	}
	for i, m := range marks {
		var sib []int
		if parentOf[i] < 0 {
			sib = tops
		} else {
			sib = childrenOf[parentOf[i]]
		}
		pos := 0
		for k, v := range sib {
			if v == i {
				pos = k
			}
		}
		parent := 5
		if parentOf[i] >= 0 {
			parent = markObj(parentOf[i])
		}
		extra := ""
		if pos > 0 {
			extra += fmt.Sprintf(" /Prev %d 0 R", markObj(sib[pos-1]))
		}
		if pos < len(sib)-1 {
			extra += fmt.Sprintf(" /Next %d 0 R", markObj(sib[pos+1]))
		}
		if kids := childrenOf[i]; len(kids) > 0 {
			extra += fmt.Sprintf(" /First %d 0 R /Last %d 0 R /Count %d", markObj(kids[0]), markObj(kids[len(kids)-1]), -len(kids))
		}
		obj(fmt.Sprintf("<< /Title %s /Parent %d 0 R /Dest [%d 0 R /XYZ 0 %.1f 0]%s >>", pdfString(m.title), parent, pageObj(m.page), m.top+14, extra))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}

package help

import (
	"html"
	"regexp"
	"strings"
)

// The guide and module help pages use a small Markdown subset: paragraphs, "- " and
// "1. " lists, ### headings, **bold**, `code` and [text](link). Everything is escaped
// first, so a module page cannot inject markup or script.

var (
	mdBold = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdCode = regexp.MustCompile("`([^`]+)`")
	mdLink = regexp.MustCompile(`\[(?P<text>[^\]]+)\]\((?P<href>[^)\s]+)\)`)
	mdNum  = regexp.MustCompile(`^\d+\.\s+`)
)

func safeHref(u string) string {
	switch {
	case strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"), strings.HasPrefix(u, "#"),
		strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "http://"):
		return u
	}
	return "#"
}

func inline(s string) string {
	s = html.EscapeString(s)
	s = mdCode.ReplaceAllString(s, "<code>$1</code>")
	s = mdBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = mdLink.ReplaceAllStringFunc(s, func(m string) string {
		p := mdLink.FindStringSubmatch(m)
		href := safeHref(html.UnescapeString(p[mdLink.SubexpIndex("href")]))
		return `<a href="` + html.EscapeString(href) + `">` + p[mdLink.SubexpIndex("text")] + `</a>`
	})
	return s
}

// RenderMarkdown renders the subset to HTML.
func RenderMarkdown(src string) string {
	var b strings.Builder
	var para []string
	list := ""
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	flush := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			flush()
			closeList()
		case strings.HasPrefix(t, "### "):
			flush()
			closeList()
			b.WriteString("<h4>" + inline(strings.TrimPrefix(t, "### ")) + "</h4>\n")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			flush()
			if list != "ul" {
				closeList()
				list = "ul"
				b.WriteString("<ul>\n")
			}
			item := strings.TrimPrefix(strings.TrimPrefix(t, "- "), "* ")
			b.WriteString("<li>" + inline(item) + "</li>\n")
		case mdNum.MatchString(t):
			flush()
			if list != "ol" {
				closeList()
				list = "ol"
				b.WriteString("<ol>\n")
			}
			b.WriteString("<li>" + inline(mdNum.ReplaceAllString(t, "")) + "</li>\n")
		default:
			if list != "" {
				closeList()
			}
			para = append(para, t)
		}
	}
	flush()
	closeList()
	return b.String()
}

// PlainText strips the subset's markup, for search and the PDF.
func PlainText(src string) string {
	s := mdLink.ReplaceAllString(src, "$1")
	s = mdBold.ReplaceAllString(s, "$1")
	s = mdCode.ReplaceAllString(s, "$1")
	return s
}

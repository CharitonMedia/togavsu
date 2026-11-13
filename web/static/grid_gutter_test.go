package static

import (
	"regexp"
	"strings"
	"testing"
)

// gridstack places each Home widget inside its cell using equal top, right, bottom and
// left margins, which is what gives rows the same gutter as columns. A height (or min- or
// max-height) on the widget itself overrides the bottom margin and the widget then runs
// into the row below.
func TestHomeWidgetsKeepRowGutter(t *testing.T) {
	css, err := FS.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), "")
	rule := regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	height := regexp.MustCompile(`(^|;)\s*(min-|max-)?height\s*:`)
	widget := regexp.MustCompile(`\.(widget|grid-stack-item-content)([^\w-]|$)`)
	for _, m := range rule.FindAllStringSubmatch(src, -1) {
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.TrimSpace(sel)
			if !widget.MatchString(sel) {
				continue
			}
			// Only the widget box itself matters, not something inside it.
			parts := strings.Fields(sel)
			if !widget.MatchString(parts[len(parts)-1]) {
				continue
			}
			if height.MatchString(m[2]) {
				t.Errorf("app.css: %q sets a height on the widget box, which removes the gutter between rows", sel)
			}
		}
	}
}

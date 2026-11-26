package static

import (
	"regexp"
	"strings"
	"testing"
)

// TestModulesPhoneKeepsFileInputsInsideViewport guards the desk-fix marketplace
// catalog phone QC (Width 390): input[type=file] has a large intrinsic width and
// grid items default to min-width:auto, which overflowed Install from a file (#pkg).
func TestModulesPhoneKeepsFileInputsInsideViewport(t *testing.T) {
	css, err := FS.ReadFile("app.css")
	if err != nil {
		t.Fatal(err)
	}
	src := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(css), "")
	media := regexp.MustCompile(`(?s)@media\s*\(\s*max-width\s*:\s*700px\s*\)\s*\{(.*)`)
	m := media.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("app.css: missing @media (max-width:700px) phone rules")
	}
	body := m[1]
	// Truncate at the matching close of this media block by brace depth.
	depth := 1
	end := 0
	for i, r := range body {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
		if depth == 0 {
			break
		}
	}
	if end == 0 {
		t.Fatal("app.css: unclosed phone media query")
	}
	phone := body[:end]
	needles := []string{
		".form-grid input[type=file]",
		"max-width:100%",
		"min-width:0",
		"#install-module",
	}
	for _, n := range needles {
		if !strings.Contains(phone, n) {
			t.Fatalf("app.css phone rules missing %q (Modules tab file input overflow)", n)
		}
	}
	if !strings.Contains(phone, "width:100%") {
		t.Fatal("app.css phone rules must set width:100% on form-grid file inputs")
	}
}

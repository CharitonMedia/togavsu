package doctender

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Core's auth wrapper reads a cookie session's form CSRF token only from the "csrf" field
// (internal/core/http.go), as the practice, billing and calendar pages post it. Every
// Doc Tender POST form must carry that field, or each review and admin write is refused.
func TestPostFormsCarryCoreCSRFField(t *testing.T) {
	paths, err := filepath.Glob("../../web/templates/doctender/*.html")
	if err != nil || len(paths) == 0 {
		t.Fatalf("templates: %v (%d found)", err, len(paths))
	}
	form := regexp.MustCompile(`(?is)<form\b[^>]*method="post"[^>]*>(.*?)</form>`)
	posts := 0
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range form.FindAllStringSubmatch(string(b), -1) {
			posts++
			if !strings.Contains(m[1], `name="csrf"`) {
				t.Errorf("%s: POST form without a csrf field:\n%s", filepath.Base(p), m[0])
			}
			if strings.Contains(m[1], `name="csrf_token"`) {
				t.Errorf("%s: POST form uses csrf_token, which core ignores", filepath.Base(p))
			}
		}
	}
	if posts == 0 {
		t.Fatal("no POST forms found; the check would pass vacuously")
	}
}

package files

import (
	"strings"
	"testing"
)

func TestArchiveZipPath(t *testing.T) {
	ok := []string{"a.txt", "dir/file.pdf", "a/b/c.md", strings.Repeat("x", 255)}
	for _, p := range ok {
		if got, err := archiveZipPath(p); err != nil || got != p {
			t.Fatalf("archiveZipPath(%q) = %q %v", p, got, err)
		}
	}
	bad := []string{
		"",
		"/abs",
		`dir\file`,
		"a/./b",
		"a/../b",
		"a//b",
		".",
		"..",
		"a/\x00b",
		"a/\tb",
		strings.Repeat("x", 256),
	}
	for _, p := range bad {
		if _, err := archiveZipPath(p); !isCode(err, "validation") {
			t.Fatalf("archiveZipPath(%q) = %v, want validation", p, err)
		}
	}
}

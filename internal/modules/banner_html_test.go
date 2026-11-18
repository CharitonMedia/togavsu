package modules

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBannerHTMLNoInlineStyle: CORE-34 height must survive staff CSP (style-src 'self').
func TestBannerHTMLNoInlineStyle(t *testing.T) {
	html := string(BannerHTML([]StaffBannerView{
		{ModuleID: "banner-mod", Path: "/banner", Height: 40, Src: "/m/banner-mod/banner"},
	}))
	if html == "" {
		t.Fatal("empty banner html")
	}
	if strings.Contains(html, "style=") {
		t.Fatalf("inline style blocked by staff CSP:\n%s", html)
	}
	if !strings.Contains(html, `height="40"`) {
		t.Fatalf("iframe must carry height attribute:\n%s", html)
	}
	if !strings.Contains(html, `data-height="40"`) {
		t.Fatalf("wrapper must expose data-height:\n%s", html)
	}
	if !strings.Contains(html, "staff-banner-h-40") {
		t.Fatalf("wrapper must use stepped height class:\n%s", html)
	}
	if !strings.Contains(html, "staff-banner-frame") {
		t.Fatalf("missing frame class:\n%s", html)
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	cssPath := filepath.Join(filepath.Dir(file), "..", "..", "web", "static", "app.css")
	css, err := os.ReadFile(cssPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".staff-banner-frame", ".staff-banner-h-40", ".staff-banners"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("app.css missing %s", want)
		}
	}
}

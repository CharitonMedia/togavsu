package frame

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteWithShellCallsShellNotBare(t *testing.T) {
	var called bool
	var got Page
	shell := func(w http.ResponseWriter, r *http.Request, pg Page) {
		called = true
		got = pg
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("from-shell"))
	}
	r := With(httptest.NewRequest(http.MethodGet, "/", nil), shell)
	rec := httptest.NewRecorder()
	pg := Page{Title: "Shell Screen", Area: "practice", Body: template.HTML("<p>body</p>")}
	Write(rec, r, pg)

	if !called {
		t.Fatal("Write with shell installed via With did not call the shell")
	}
	if got.Title != pg.Title || got.Area != pg.Area || string(got.Body) != string(pg.Body) {
		t.Fatalf("shell received %+v, want %+v", got, pg)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d (shell response, not Bare)", rec.Code, http.StatusTeapot)
	}
	body := rec.Body.String()
	if body != "from-shell" {
		t.Fatalf("body = %q, want from-shell (not bare document)", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") || strings.Contains(body, "area-practice") {
		t.Fatalf("body looks like Bare output: %q", body)
	}
}

func TestWriteWithoutShellWritesBareDocument(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	Write(rec, r, Page{
		Title: "Matters",
		Area:  "practice",
		Body:  template.HTML(`<p id="content">hello</p>`),
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>Matters · Togavsu</title>") {
		t.Fatalf("missing title in bare document: %q", body)
	}
	if !strings.Contains(body, `class="page area-practice"`) {
		t.Fatalf("missing area class in bare document: %q", body)
	}
	if !strings.Contains(body, `<p id="content">hello</p>`) {
		t.Fatalf("missing body in bare document: %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
}

func TestBareEscapesAndScriptsAndContentType(t *testing.T) {
	t.Run("escapes title notice and error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Bare(rec, Page{
			Title:  `<script>alert(1)</script>`,
			Notice: `<img src=x onerror=alert(1)>`,
			Error:  `"&<>`,
			Body:   template.HTML(`<p>ok</p>`),
		})
		body := rec.Body.String()
		for _, raw := range []string{
			`<script>alert(1)</script>`,
			`<img src=x onerror=alert(1)>`,
			`"&<>`,
		} {
			if strings.Contains(body, raw) {
				t.Fatalf("unescaped value %q still present in body: %q", raw, body)
			}
		}
		if !strings.Contains(body, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
			t.Fatalf("title not escaped: %q", body)
		}
		if !strings.Contains(body, `&lt;img src=x onerror=alert(1)&gt;`) {
			t.Fatalf("notice not escaped: %q", body)
		}
		if !strings.Contains(body, `&#34;&amp;&lt;&gt;`) {
			t.Fatalf("error not escaped: %q", body)
		}
	})

	t.Run("renders deferred same-origin scripts", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Bare(rec, Page{
			Title:   "Scripts",
			Scripts: []string{"/ui/static/a.js", "/ui/static/b.js"},
			Body:    template.HTML(`<p>x</p>`),
		})
		body := rec.Body.String()
		for _, src := range []string{"/ui/static/a.js", "/ui/static/b.js"} {
			tag := `<script src="` + src + `" defer></script>`
			if !strings.Contains(body, tag) {
				t.Fatalf("missing deferred script tag %q in %q", tag, body)
			}
		}
	})

	t.Run("keeps existing Content-Type", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rec.Header().Set("Content-Type", "application/xhtml+xml")
		Bare(rec, Page{Title: "Keep", Body: template.HTML(`<p>x</p>`)})
		if ct := rec.Header().Get("Content-Type"); ct != "application/xhtml+xml" {
			t.Fatalf("Content-Type = %q, want existing application/xhtml+xml", ct)
		}
	})

	t.Run("sets text/html when unset", func(t *testing.T) {
		rec := httptest.NewRecorder()
		Bare(rec, Page{Title: "Set", Body: template.HTML(`<p>x</p>`)})
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("Content-Type = %q, want text/html; charset=utf-8", ct)
		}
	})
}

func TestStatusZeroOrBelow100Writes200(t *testing.T) {
	for _, st := range []int{0, 99, -1} {
		rec := httptest.NewRecorder()
		pg := Page{Title: "OK", Status: st, Body: template.HTML(`<p>x</p>`)}
		Bare(rec, pg)
		if rec.Code != http.StatusOK {
			t.Fatalf("Status %d: WriteHeader got %d, want 200", st, rec.Code)
		}
		if got := pg.StatusCode(); got != http.StatusOK {
			t.Fatalf("Status %d: StatusCode() = %d, want 200", st, got)
		}
	}
}

func TestStatusSetIsWritten(t *testing.T) {
	rec := httptest.NewRecorder()
	pg := Page{Title: "Missing", Status: http.StatusNotFound, Body: template.HTML(`<p>gone</p>`)}
	Bare(rec, pg)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := pg.StatusCode(); got != http.StatusNotFound {
		t.Fatalf("StatusCode() = %d, want 404", got)
	}
}

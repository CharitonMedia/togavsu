// Package frame puts a staff screen inside the app shell: the sidebar, the top bar with
// search, timers, notifications, help and New, and the title bar. An area (practice,
// billing, trust, calendar, files, Doc Tender, the editor, settings) renders only its
// content; the core puts the shell on each request, so no area imports the dashboard
// and no area draws its own header.
package frame

import (
	"context"
	"html/template"
	"net/http"
)

// Page is one screen's content and what the shell needs to draw around it.
type Page struct {
	Title string
	// Area names the screen's styles in app.css: main gets class "area-<Area>".
	Area string
	Body template.HTML
	// Status is the HTTP status; zero means 200.
	Status int
	// Notice and Error are shown under the title. Empty keeps the shell's own (the
	// ?notice= and ?error= a form post redirected with).
	Notice, Error string
	// Scripts are same-origin scripts the content needs, loaded deferred.
	Scripts []string
}

// Shell writes pg inside the app shell for r. The handler may already have set headers
// such as a Content-Security-Policy the content needs; the shell keeps them.
type Shell func(w http.ResponseWriter, r *http.Request, pg Page)

type key struct{}

// With returns r carrying s, for every area handler that serves r.
func With(r *http.Request, s Shell) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), key{}, s))
}

// Write renders pg inside r's shell. A request with no shell (an area handler tested on
// its own) gets a plain document with the same content.
func Write(w http.ResponseWriter, r *http.Request, pg Page) {
	if s, ok := r.Context().Value(key{}).(Shell); ok && s != nil {
		s(w, r, pg)
		return
	}
	Bare(w, pg)
}

var bare = template.Must(template.New("bare").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Togavsu</title><link rel="stylesheet" href="/ui/static/app.css">{{range .Scripts}}
<script src="{{.}}" defer></script>{{end}}</head>
<body><main id="main" class="page{{if .Area}} area-{{.Area}}{{end}}"><div class="titlebar"><h1>{{.Title}}</h1></div>
{{if .Notice}}<p class="notice" role="status">{{.Notice}}</p>{{end}}{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
{{.Body}}</main></body></html>
`))

// Bare writes pg as a plain document with no sidebar or top bar: for a request the
// shell cannot draw (no shell installed, or a signed-in client on a staff route).
func Bare(w http.ResponseWriter, pg Page) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.WriteHeader(status(pg.Status))
	_ = bare.Execute(w, pg)
}

func status(s int) int {
	if s < http.StatusContinue {
		return http.StatusOK
	}
	return s
}

// StatusCode is pg's HTTP status, 200 when unset.
func (pg Page) StatusCode() int { return status(pg.Status) }

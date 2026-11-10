// Package web embeds the server-rendered templates and static files.
package web

import "embed"

// Practice holds templates/practice: the practice screens, CSS and JS.
//
//go:embed templates/practice
var Practice embed.FS

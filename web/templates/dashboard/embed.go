// Package dashboard embeds the dashboard templates (internal/dashboard parses them).
package dashboard

import "embed"

// FS holds this folder's *.html templates.
//
//go:embed *.html
var FS embed.FS

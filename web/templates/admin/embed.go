// Package admin embeds the admin templates (internal/dashboard parses them).
package admin

import "embed"

// FS holds this folder's *.html templates.
//
//go:embed *.html
var FS embed.FS

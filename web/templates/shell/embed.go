// Package shell embeds the shell templates (internal/dashboard parses them).
package shell

import "embed"

// FS holds this folder's *.html templates.
//
//go:embed *.html
var FS embed.FS

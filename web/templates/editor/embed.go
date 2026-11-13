// Package editorpages embeds the edit page, the New Pleading form and the page's script
// (internal/editor serves them).
package editorpages

import "embed"

// FS holds edit.html, pleading.html and edit.js.
//
//go:embed *.html *.js
var FS embed.FS

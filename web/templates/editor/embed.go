// Package editorpages embeds the edit page, the New Pleading form and the page's script
// and styles (internal/editor serves them).
package editorpages

import "embed"

// FS holds edit.html, pleading.html, edit.js and edit.css.
//
//go:embed *.html *.js *.css
var FS embed.FS

// Package static embeds the shell's CSS and JavaScript and the vendored htmx and
// gridstack.js builds (see vendor/README.md for versions and checksums).
package static

import "embed"

// FS is served under /ui/static/.
//
//go:embed *.css *.js vendor
var FS embed.FS

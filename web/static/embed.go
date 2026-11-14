// Package static embeds the shell's CSS and JavaScript and the vendored htmx and
// gridstack.js builds (see thirdparty/README.md for versions and checksums). The
// directory is not named vendor because Go module zips leave out vendor directories,
// which would break this embed for module repositories that import the core.
package static

import "embed"

// FS is served under /ui/static/.
//
//go:embed *.css *.js thirdparty
var FS embed.FS

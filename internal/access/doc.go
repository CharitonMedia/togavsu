// Package access is the service-layer check for roles, matter walls, and
// attorney-only redaction. Handlers and later packages call Gate before a
// list, search, widget, report, or direct read. A denied write does not run
// and does not append an audit row.
package access

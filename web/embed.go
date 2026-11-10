// Package web embeds the server-rendered templates and static files.
package web

import "embed"

// Practice holds templates/practice: the practice screens, CSS and JS.
//
//go:embed templates/practice
var Practice embed.FS

// Billing holds templates/billing: the billing screens and CSS.
//
//go:embed templates/billing
var Billing embed.FS

// Trust holds templates/trust: the trust screens and CSS.
//
//go:embed templates/trust
var Trust embed.FS

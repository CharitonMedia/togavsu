// Package qrcode renders a QR code as inline SVG on the server, so a two-factor
// setup page needs no external service and no image request.
package qrcode

import (
	"fmt"
	"html/template"
	"strings"

	"rsc.io/qr"
)

// SVG returns an SVG QR code for text at error-correction level M.
func SVG(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", fmt.Errorf("qrcode: %w", err)
	}
	// The QR spec puts a blank border of four modules on each side; scanners need it.
	n := code.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="232" height="232" shape-rendering="crispEdges" role="img" aria-label="QR code for your authenticator app">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < code.Size; y++ {
		for x := 0; x < code.Size; x++ {
			if code.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

// HTML is SVG for templates. It returns empty HTML when text is empty or too long to encode.
// The SVG holds only numbers built here, so it is safe to mark as HTML.
func HTML(text string) template.HTML {
	if text == "" {
		return ""
	}
	svg, err := SVG(text)
	if err != nil {
		return ""
	}
	return template.HTML(svg) // #nosec G203 -- built from integers above, no caller text.
}

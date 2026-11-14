package qrcode

import (
	"strings"
	"testing"
)

func TestSVGEncodesAnOTPAuthURI(t *testing.T) {
	uri := "otpauth://totp/Togavsu:patrick%40example.test?digits=6&issuer=Togavsu&period=30&secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	svg, err := SVG(uri)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("not an svg: %.60s", svg)
	}
	if strings.Contains(svg, "otpauth") || strings.Contains(svg, "JBSWY3DP") {
		t.Fatal("the svg must not carry the secret as text")
	}
	if strings.Count(svg, "h1v1h-1z") < 100 {
		t.Fatal("too few dark modules for a real code")
	}
	if HTML("") != "" {
		t.Fatal("empty text must render nothing")
	}
	if !strings.HasPrefix(string(HTML(uri)), "<svg ") {
		t.Fatal("HTML must render the same svg")
	}
}

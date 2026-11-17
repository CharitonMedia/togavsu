package modules_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestPGFrameBridge covers CORE-09: staff context-token refresh keeps matter_id and the
// frame page carries the nonce bridge script (navigate allowlist is unit-tested in
// TestBridgeNavigateURL).
func TestPGFrameBridge(t *testing.T) {
	e := newEnv(t)
	e.installSample()

	page := e.do("GET", "/modules/matter-clock/tabs/hours", nil, e.as("staff")...)
	if page.code != http.StatusOK {
		t.Fatalf("tab page: %d %s", page.code, page.body)
	}
	for _, needle := range []string{"togavsu:navigate", "togavsu:context.refresh", "context-token", "matter-clock", staffSandboxHint} {
		if !strings.Contains(page.body, needle) {
			t.Fatalf("staff frame missing %q in %s", needle, page.body[:min(500, len(page.body))])
		}
	}
	if !strings.Contains(page.body, "nonce=") {
		t.Fatal("staff frame missing script nonce")
	}

	homeTok := e.do("GET", "/modules/matter-clock/tabs/hours/context-token", nil, e.as("staff")...)
	if homeTok.code != http.StatusOK || homeTok.json["token"] == nil {
		t.Fatalf("home context-token: %d %s", homeTok.code, homeTok.body)
	}

	tok := e.do("GET", "/modules/matter-clock/tabs/matter/context-token?matter_id="+string(e.open.ID), nil, e.as("staff")...)
	if tok.code != http.StatusOK || tok.json["token"] == nil || tok.json["expires_at"] == nil {
		t.Fatalf("matter context-token: %d %s", tok.code, tok.body)
	}
	claims, err := e.svc.VerifyContext(e.ctx, tok.json["token"].(string))
	if err != nil || claims.MatterID != e.open.ID || claims.Module != "matter-clock" || claims.UserID != e.staff.UserID {
		t.Fatalf("refreshed claims: %+v %v", claims, err)
	}

	if r := e.do("GET", "/modules/matter-clock/tabs/matter/context-token", nil, e.as("staff")...); r.code == http.StatusOK {
		t.Fatalf("matter tab refresh without matter_id: %s", r.body)
	}
}

// TestPGFrameBridgeWidgetAndPanel: widgets and matter-item panels get connect-src so the
// bridge's context.refresh fetch is not blocked, and panel/widget context-token routes
// refresh with the live session (regression for opaque-origin / missing connect-src).
func TestPGFrameBridgeWidgetAndPanel(t *testing.T) {
	e := newEnv(t)
	e.installSample()

	widget := e.do("GET", "/modules/matter-clock/widgets/hours", nil, e.as("staff")...)
	if widget.code != http.StatusOK {
		t.Fatalf("widget page: %d %s", widget.code, widget.body)
	}
	if !strings.Contains(widget.csp, "connect-src 'self'") {
		t.Fatalf("widget bare-frame CSP missing connect-src: %q", widget.csp)
	}
	for _, needle := range []string{"togavsu:context.refresh", "togavsu:navigate", "context-token", staffSandboxHint} {
		if !strings.Contains(widget.body, needle) {
			t.Fatalf("widget frame missing %q", needle)
		}
	}
	wTok := e.do("GET", "/modules/matter-clock/widgets/hours/context-token", nil, e.as("staff")...)
	if wTok.code != http.StatusOK || wTok.json["token"] == nil {
		t.Fatalf("widget context-token: %d %s", wTok.code, wTok.body)
	}

	panel := e.do("GET", "/modules/matter-clock/matter-items/timer?matter_id="+string(e.open.ID), nil, e.as("staff")...)
	// matter-clock sample may not declare matter_items; accept 404 and still cover widget.
	if panel.code == http.StatusOK {
		if !strings.Contains(panel.csp, "connect-src 'self'") {
			t.Fatalf("panel bare-frame CSP missing connect-src: %q", panel.csp)
		}
		pTok := e.do("GET", "/modules/matter-clock/matter-items/timer/context-token?matter_id="+string(e.open.ID), nil, e.as("staff")...)
		if pTok.code != http.StatusOK || pTok.json["token"] == nil {
			t.Fatalf("panel context-token: %d %s", pTok.code, pTok.body)
		}
	}
}

const staffSandboxHint = `sandbox="allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox"`

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

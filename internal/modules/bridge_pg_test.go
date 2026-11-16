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

const staffSandboxHint = `sandbox="allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox"`

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

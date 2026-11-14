//go:build acceptance

package core

import "testing"

// TestReleaseAllOperationsMounted is AC-002's whole-contract check for the full
// acceptance run (scripts/release/full_acceptance.py runs it with -tags acceptance): every
// operation in api/openapi/v1/mcp-tools.json is served on /api/v1 and offered on /mcp.
// It fails, naming each one, while any operation is not mounted. The normal suite's
// TestContractRoutesFailClosed covers fail-closed behavior of the mounted ones.
func TestReleaseAllOperationsMounted(t *testing.T) {
	app, _ := start(t, testDSN(t))
	missing := 0
	for _, tl := range app.tools {
		if tl.Path == "/mcp" || app.mounted(tl) {
			continue
		}
		missing++
		t.Errorf("not mounted: %s %s (%s)", tl.Method, tl.Path, tl.Name)
	}
	t.Logf("%d of %d contract operations are not mounted", missing, len(app.tools))
}

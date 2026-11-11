package compose

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

func (e *env) principal(c *client) domain.Principal {
	e.t.Helper()
	caller, err := e.app.Identity.Authenticate(context.Background(), identity.Presented{SessionToken: e.session(c)})
	if err != nil {
		e.t.Fatal(err)
	}
	return caller.Principal
}

// auditPanel is the System tab's audit table as text.
func auditPanel(t *testing.T, c *client) string {
	t.Helper()
	page := c.must(http.StatusOK, "GET", "/admin?tab=system", nil).body
	panel := between(page, "<h2>Audit log</h2>", "</section>")
	if panel == "" {
		t.Fatalf("no audit panel on the System tab: %s", page)
	}
	return html.UnescapeString(panel)
}

// TestUISystemTabAuditScope: the System tab's audit rows keep the practice read scope
// of GET /api/v1/audit-log (PRD 4 walls, attorney-only redaction; AGENTS.md access
// rules). An admin not named on a wall sees none of that matter's rows; a principal
// without attorney-only access gets a conflict reason redacted; an admin on the wall
// and with attorney-only access still sees both.
func TestUISystemTabAuditScope(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	outsider := e.enroll("Olive Outsider", "olive@firm.example", domain.RoleAdmin)

	walled, _, _ := e.matter("Walled Estate of Quist", nil)
	e.admin.must(http.StatusOK, "PATCH", "/api/v1/matters/"+walled, map[string]any{"name": "Quist Sealed Rename"})
	e.wall(walled, e.admin.id, e.atty.id)

	open, _, client := e.matter("Open Matter of Pell", nil)
	pAdmin := e.principal(e.admin)
	if _, err := e.app.Practice.CreateConflict(ctx, pAdmin, practice.ConflictInput{Name: "Client Pell", Relationship: "prior client", Reason: "synthetic prior representation"}); err != nil {
		t.Fatal(err)
	}
	run, err := e.app.Practice.RunConflictCheck(ctx, pAdmin, domain.ID(open))
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Hits) == 0 {
		t.Fatalf("conflict check on %s (client %s) found no hit", open, client)
	}
	const secret = "Attorney-only conflict reason Zephyr"
	if _, err := e.app.Practice.DecideConflict(ctx, pAdmin, run.Hits[0].ID, practice.DecisionClear, secret); err != nil {
		t.Fatal(err)
	}

	// The admin on the wall, with attorney-only access, sees both rows in full.
	in := auditPanel(t, e.admin)
	for _, want := range []string{"Quist Sealed Rename", secret} {
		if !strings.Contains(in, want) {
			t.Fatalf("admin on the wall lost audit value %q:\n%s", want, in)
		}
	}

	// The admin outside the wall sees no row of the walled matter, through the page and
	// through the core API alike, and still sees the open matter's rows.
	out := auditPanel(t, outsider)
	for _, gone := range []string{"Quist", walled} {
		if strings.Contains(out, gone) {
			t.Fatalf("admin outside the wall sees %q on the System tab:\n%s", gone, out)
		}
	}
	if api := outsider.must(http.StatusOK, "GET", "/api/v1/audit-log", nil).body; strings.Contains(api, "Quist") {
		t.Fatal("core audit-log leaks the walled matter")
	}
	if !strings.Contains(out, secret) {
		t.Fatalf("admin outside the wall lost the open matter's conflict row:\n%s", out)
	}

	// The System hook as a principal without attorney-only access (staff assigned to the
	// open matter): the conflict reason is redacted, the walled matter is absent.
	ledger := audit.NewPostgres(e.app.Pool)
	rows, err := auditRows(ctx, e.principal(e.staff), ledger, e.app, 500)
	if err != nil {
		t.Fatal(err)
	}
	redacted := false
	for _, row := range rows {
		if strings.Contains(row.Old+row.New, "Quist") {
			t.Fatalf("staff off the wall sees the walled matter: %+v", row)
		}
		if strings.Contains(row.Old+row.New, "Zephyr") {
			t.Fatalf("attorney-only reason unredacted for staff: %+v", row)
		}
		if row.Field == "conflict_reason" {
			if !row.Redacted || row.New != practice.Redacted {
				t.Fatalf("conflict reason row not redacted: %+v", row)
			}
			redacted = true
		}
	}
	if !redacted {
		t.Fatalf("staff lost the open matter's conflict row entirely: %+v", rows)
	}

	// The limit counts only rows the viewer may read.
	few, err := auditRows(ctx, e.principal(outsider), ledger, e.app, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(few) != 3 {
		t.Fatalf("limit 3 returned %d visible rows", len(few))
	}
}

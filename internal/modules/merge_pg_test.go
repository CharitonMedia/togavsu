package modules_test

import (
	"strings"
	"testing"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/core"
)

// AC-002 mergeMatter (PRD 9.9): the core's merge moves a module's rows onto the survivor
// in the merge's own transaction and reports them per module; a refused relink rolls the
// whole merge back, so the merged matter is not archived and nothing is emitted.
func TestPGMergeMatterRelinksModuleRows(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	dep := e.rt.Last("matter-clock")
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	if err := matterclock.SetTarget(e.ctx, mdb, string(e.open.ID), 50, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	relink := core.ModuleRelinker(e.svc.Schemas(), e.app.Pool)
	other := e.matter("Other Estate", e.open.PracticeAreaID)
	if err := matterclock.SetTarget(e.ctx, mdb, string(other.ID), 7, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	events := func() int {
		return e.count(`SELECT COUNT(*) FROM core.events WHERE action = 'matter.merged'`)
	}

	// Both matters carry an extension row keyed by the matter: the module refuses, and
	// the merge leaves both matters and both rows as they were.
	if _, err := e.app.Practice.MergeMatter(e.ctx, e.attorney, other.ID, e.open.ID, relink); err == nil || !strings.Contains(err.Error(), "matter-clock") {
		t.Fatalf("merge onto an extension row: %v", err)
	}
	if got, _ := e.app.Practice.GetMatter(e.ctx, e.attorney, other.ID); got.Matter.Archived {
		t.Fatal("a refused merge archived the matter")
	}
	if events() != 0 || e.count(`SELECT COUNT(*) FROM core.matters WHERE archived = 1`) != 0 {
		t.Fatal("a refused merge left an event or an archived row")
	}

	// Staff can't merge, and the attorney can't merge into a walled matter they are not on.
	if _, err := e.app.Practice.MergeMatter(e.ctx, e.staff, e.open.ID, other.ID, relink); err == nil {
		t.Fatal("staff merged matters")
	}
	if _, err := e.app.Practice.MergeMatter(e.ctx, e.admin, e.open.ID, e.walled.ID, relink); err == nil || !strings.Contains(err.Error(), "not") {
		t.Fatalf("admin off the wall merged into the walled matter: %v", err)
	}

	// The walled matter has no extension row, so merging open into it moves open's row.
	res, err := e.app.Practice.MergeMatter(e.ctx, e.attorney, e.open.ID, e.walled.ID, relink)
	if err != nil {
		t.Fatal(err)
	}
	if res.ArchivedID != e.open.ID || res.SurvivorID != e.walled.ID || len(res.Relinked) != 1 ||
		res.Relinked[0].ModuleID != "matter-clock" || res.Relinked[0].Rows != 1 {
		t.Fatalf("merge result: %+v", res)
	}
	if e.count(`SELECT COUNT(*) FROM mod_matter_clock.matter_targets WHERE matter_id = $1`, string(e.walled.ID)) != 1 {
		t.Fatal("the module row did not move to the survivor")
	}
	if e.count(`SELECT COUNT(*) FROM core.matters WHERE id = $1 AND archived = 1`, string(e.open.ID)) != 1 {
		t.Fatal("the merged matter is not archived in core.matters")
	}
	if events() != 1 {
		t.Fatal("matter.merged was not emitted once")
	}
	got, err := e.app.Practice.GetMatter(e.ctx, e.attorney, e.open.ID)
	if err != nil || !got.Matter.Archived {
		t.Fatalf("the merged matter is gone or not archived: %v", err)
	}
}

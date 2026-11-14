package core

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// The module check knows the core's schema from internal/modules/core_tables.txt, with
// no database; it must be the tables the core's own migrations leave in schema core.
func TestModuleCheckKnowsTheCoreSchema(t *testing.T) {
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	got, err := modules.CreatedTables(ms)
	if err != nil {
		t.Fatal(err)
	}
	if want := modules.CoreSchemaTables(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("internal/modules/core_tables.txt is out of date; write the core's tables there, one a line:\n%s", strings.Join(got, "\n"))
	}
	for _, rel := range modules.Accepts().CoreRelations {
		if !strings.Contains("\n"+strings.Join(got, "\n")+"\n", "\n"+strings.TrimPrefix(rel, "core.")+"\n") {
			t.Fatalf("modules may link to %s, which the core's schema doesn't have", rel)
		}
	}
}

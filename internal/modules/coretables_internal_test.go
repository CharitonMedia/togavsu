package modules

import (
	"os"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/db"
)

func refusalCodes(rs Refusals) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Code)
	}
	return out
}

// A reference to a core record modules may link to is refused while the core's schema
// doesn't have the relation, so a migration held back for it stays held until both hold.
func TestReferenceNeedsTheRelationInTheCoreSchema(t *testing.T) {
	m := Manifest{ID: "links"}
	ms := []db.Migration{{Version: 1, Name: "0001_links",
		SQL: "CREATE TABLE links (id TEXT PRIMARY KEY, area_id TEXT REFERENCES core.practice_areas (id) ON DELETE RESTRICT);"}}
	if refs := MigrationRefusals(m, ms); len(refs) != 0 {
		t.Fatalf("with the relation in the schema: %v", refs)
	}
	delete(coreTables, "practice_areas")
	defer func() { coreTables["practice_areas"] = true }()
	refs := MigrationRefusals(m, ms)
	if len(refs) != 1 || refs[0].Code != RefuseCoreRelationMissing || refs[0].Value != "core.practice_areas" || refs[0].APICode != CodeMigration {
		t.Fatalf("without it: %v", refs)
	}
	if err := CheckMigrations(m, ms); err == nil {
		t.Fatal("the install check passed a reference to a relation the core doesn't have")
	}
}

// Assuming the core relations exist finds the problems a refused reference hides.
func TestMigrationRefusalsAssumingCore(t *testing.T) {
	m := Manifest{ID: "links"}
	ms := []db.Migration{{Version: 1, Name: "0001_links",
		SQL: "CREATE TABLE links (id TEXT PRIMARY KEY, refund_id TEXT REFERENCES core.payment_refunds (id) ON DELETE CASCADE);\n" +
			"CREATE TABLE fine (id TEXT PRIMARY KEY, refund_id TEXT REFERENCES core.payment_refunds (id) ON DELETE RESTRICT);"}}
	if got := refusalCodes(MigrationRefusals(m, ms)); len(got) != 2 || got[0] != RefuseCoreReference || got[1] != RefuseCoreReference {
		t.Fatalf("refusals: %v", got)
	}
	refs := MigrationRefusalsAssumingCore(m, ms)
	if len(refs) != 1 || refs[0].Code != RefuseStatement || refs[0].Statement != 1 {
		t.Fatalf("assuming the core: %v", refs)
	}
}

func TestCreatedTables(t *testing.T) {
	got, err := CreatedTables([]db.Migration{
		{Version: 1, Name: "0001_a", SQL: "CREATE TABLE a (id TEXT); CREATE TABLE IF NOT EXISTS core.b (id TEXT); CREATE UNLOGGED TABLE c (id TEXT); CREATE INDEX a_i ON a (id);"},
		{Version: 2, Name: "0002_b", SQL: "ALTER TABLE b RENAME TO bb; ALTER TABLE a RENAME COLUMN id TO key; DROP TABLE IF EXISTS c, missing; CREATE TABLE other.x (id TEXT);"},
	})
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "bb" {
		t.Fatalf("tables: %v %v", got, err)
	}
}

// Every refused manifest value is reported by ValidateAll; Validate returns the first.
func TestValidateAll(t *testing.T) {
	m := Manifest{ID: "Bad Id", Version: "1.0.0", Name: "n", Author: "a",
		Container: Container{Image: "i", Digest: "sha256:x", Port: 1},
		Tabs:      []Tab{{ID: "a", Title: "A", Placement: "client", Path: "/a"}, {ID: "b", Title: "B", Placement: "home", Path: "/b"}},
		Settings:  []SettingSpec{{Key: "k", Type: "number"}},
	}
	all := m.ValidateAll()
	var fields []string
	for _, err := range all {
		fields = append(fields, err.Error())
	}
	if len(all) != 5 || all[0].Error() != m.Validate().Error() {
		t.Fatalf("ValidateAll: %v", fields)
	}
	src, err := os.ReadFile("../../examples/matter-clock/package/" + ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := ParseManifest(src)
	if err != nil || len(sample.ValidateAll()) != 0 || (Manifest{}).Validate() == nil {
		t.Fatalf("Validate: %v", err)
	}
}

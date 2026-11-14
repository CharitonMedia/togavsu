package modules_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func samplePackage(t *testing.T, edit func(map[string][]byte)) []byte {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range []string{modules.ManifestFile, "migrations/0001_matter_targets.sql"} {
		b, err := os.ReadFile("../../examples/matter-clock/package/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = b
	}
	if edit != nil {
		edit(files)
	}
	raw, err := modules.Archive(files)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func replace(files map[string][]byte, name, old, new string) {
	if !strings.Contains(string(files[name]), old) {
		panic("fixture text not found: " + old)
	}
	files[name] = []byte(strings.Replace(string(files[name]), old, new, 1))
}

func TestCheckAllPassesWhatCheckPackagePasses(t *testing.T) {
	raw := samplePackage(t, nil)
	pkg, ms, refs := modules.CheckAll(raw)
	if len(refs) != 0 || pkg.Manifest.ID != "matter-clock" || len(ms) != 1 {
		t.Fatalf("sample: %v", refs)
	}
	if _, _, err := modules.CheckPackage(raw); err != nil {
		t.Fatal(err)
	}
	// Keys match as the decoder matches them: a key in another case is the same field.
	cased := samplePackage(t, func(f map[string][]byte) { replace(f, modules.ManifestFile, "\nprice:", "\nPrice:") })
	if _, _, refs := modules.CheckAll(cased); len(refs) != 0 {
		t.Fatalf("a key in another case: %v", refs)
	}
	if _, _, err := modules.CheckPackage(cased); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAllReportsEveryUnknownField(t *testing.T) {
	raw := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "  events: /events\n", "  events: /events\n  system_mail: true\n")
		replace(f, modules.ManifestFile, "    placement: home\n", "    placement: home\n    params: [matter_id]\n")
		replace(f, modules.ManifestFile, "tabs:\n", "notifications:\n  - kind: a\n    label: A\ntabs:\n")
		replace(f, modules.ManifestFile, "      h: 2\n    sizes:", "      h: 2\n      d: 1\n    sizes:")
	})
	_, _, refs := modules.CheckAll(raw)
	got := map[string]string{}
	for _, r := range refs {
		if r.Code != modules.RefuseUnknownField || r.Kind != modules.KindManifest || r.File != modules.ManifestFile {
			t.Fatalf("unexpected refusal %+v", r)
		}
		got[r.Path] = r.Value
	}
	want := map[string]string{"container.system_mail": "system_mail", "tabs[0].params": "params", "notifications": "notifications", "widgets[0].size.d": "d"}
	if len(got) != len(want) {
		t.Fatalf("refusals %v, want %v", got, want)
	}
	for p, v := range want {
		if got[p] != v {
			t.Fatalf("refusals %v, want %v", got, want)
		}
	}
	// The install check refuses the same package, and its error is the first refusal's.
	_, _, err := modules.CheckPackage(raw)
	if err == nil {
		t.Fatal("the install check passed an unknown field")
	}
	one := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "tabs:\n", "notifications:\n  - kind: a\ntabs:\n")
	})
	_, _, refs = modules.CheckAll(one)
	_, _, err = modules.CheckPackage(one)
	if len(refs) != 1 || err == nil || refs[0].Error() != err.Error() {
		t.Fatalf("one unknown field: %v vs %v", refs, err)
	}
}

func TestCheckAllReportsUnknownScopes(t *testing.T) {
	raw := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "permissions:\n", "permissions:\n  - scope: books:read\n    reason: r\n  - scope: views:other.view:read\n    reason: r\n  - scope: firm:read\n    reason: r\n")
	})
	_, _, refs := modules.CheckAll(raw)
	if len(refs) != 2 {
		t.Fatalf("refusals: %v", refs)
	}
	for i, want := range []string{"books:read", "firm:read"} {
		r := refs[i]
		if r.Code != modules.RefuseUnknownScope || r.Kind != modules.KindScope || r.Value != want || r.APICode != domain.CodeValidation {
			t.Fatalf("refusal %d: %+v", i, r)
		}
	}
	if refs[0].Path != "permissions[0]" || refs[1].Path != "permissions[2]" {
		t.Fatalf("paths: %s %s", refs[0].Path, refs[1].Path)
	}
	// The install check itself still reads scopes only at approval, as before.
	if _, _, err := modules.CheckPackage(raw); err != nil {
		t.Fatalf("install check: %v", err)
	}
	if identity.KnownScope("books:read") || !identity.KnownScope("matters:read") || !identity.KnownScope("views:a.b:read") {
		t.Fatal("KnownScope")
	}
}

func TestCheckAllReportsEveryMigrationRefusal(t *testing.T) {
	raw := samplePackage(t, func(f map[string][]byte) {
		replace(f, "migrations/0001_matter_targets.sql", "  updated_at BIGINT NOT NULL\n",
			"  updated_at BIGINT NOT NULL,\n  account_id TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT\n")
		f["migrations/0002_more.sql"] = []byte("CREATE TABLE refunds (id TEXT PRIMARY KEY, refund_id TEXT REFERENCES core.payment_refunds (id) ON DELETE RESTRICT);\n" +
			"CREATE TABLE contacts (id TEXT PRIMARY KEY);\n" +
			"ALTER TABLE refunds ADD CONSTRAINT fk FOREIGN KEY (id) REFERENCES core.trust_accounts (id) ON DELETE RESTRICT;\n")
	})
	_, _, refs := modules.CheckAll(raw)
	type want struct {
		code, file, value string
		statement         int
	}
	wants := []want{
		{modules.RefuseCoreReference, "migrations/0001_matter_targets.sql", "core.trust_accounts", 1},
		{modules.RefuseCoreReference, "migrations/0002_more.sql", "core.payment_refunds", 1},
		{modules.RefuseStatement, "migrations/0002_more.sql", "", 2},
		{modules.RefuseCoreReference, "migrations/0002_more.sql", "core.trust_accounts", 3},
	}
	if len(refs) != len(wants) {
		t.Fatalf("refusals: %v", refs)
	}
	for i, w := range wants {
		r := refs[i]
		if r.Code != w.code || r.File != w.file || r.Value != w.value || r.Statement != w.statement || r.APICode != modules.CodeMigration {
			t.Fatalf("refusal %d: %+v, want %+v", i, r, w)
		}
	}
	// The table whose CREATE was refused is not reported again by the cross-check, and
	// the install check's error is the first refusal's.
	_, _, err := modules.CheckPackage(raw)
	if err == nil || err.Error() != refs[0].Error() {
		t.Fatalf("install check: %v vs %v", err, refs[0])
	}
	var ref *modules.CoreReferenceError
	if errors.As(refs[0].Err, &ref) {
		t.Fatal("the core's error is the API error, as before")
	}
	pkg, _ := modules.ReadPackage(raw)
	ms, _ := pkg.Migrations()
	if first := modules.CheckMigrations(pkg.Manifest, ms); first == nil || first.Error() != modules.MigrationRefusals(pkg.Manifest, ms)[0].Error() {
		t.Fatalf("CheckMigrations: %v", first)
	}
}

func TestCheckAllStopsWhereNothingCanBeRead(t *testing.T) {
	if _, _, refs := modules.CheckAll([]byte("not a package")); len(refs) != 1 || refs[0].Code != modules.RefusePackage {
		t.Fatalf("garbage: %v", refs)
	}
	bad := samplePackage(t, func(f map[string][]byte) { f[modules.ManifestFile] = []byte("id: [x\n") })
	if _, _, refs := modules.CheckAll(bad); len(refs) != 1 || refs[0].Code != modules.RefuseYAML {
		t.Fatalf("bad yaml: %v", refs)
	}
	shape := samplePackage(t, func(f map[string][]byte) { replace(f, modules.ManifestFile, "tabs:\n", "tabs: oops\nx_tabs:\n") })
	_, _, refs := modules.CheckAll(shape)
	if len(refs) != 2 || refs[0].Code != modules.RefuseUnknownField || refs[1].Code != modules.RefuseManifest {
		t.Fatalf("wrong shape: %v", refs)
	}
	// A manifest value refused alongside an unknown field: both reported, with the
	// value's path.
	both := samplePackage(t, func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "    placement: matter\n", "    placement: client\n    nav: x\n")
	})
	_, _, refs = modules.CheckAll(both)
	if len(refs) != 2 || refs[0].Path != "tabs[1].nav" || refs[1].Code != modules.RefuseManifest || refs[1].Path != "tabs[1].placement" {
		t.Fatalf("value and field: %v", refs)
	}
	misnamed := samplePackage(t, func(f map[string][]byte) { f["migrations/bad.sql"] = []byte("SELECT 1;") })
	if _, _, refs := modules.CheckAll(misnamed); len(refs) != 1 || refs[0].Code != modules.RefuseMigrationFile {
		t.Fatalf("misnamed: %v", refs)
	}
	// The cross-check still reports a declared table no migration creates.
	noTable := samplePackage(t, func(f map[string][]byte) { delete(f, "migrations/0001_matter_targets.sql") })
	_, _, refs = modules.CheckAll(noTable)
	if len(refs) != 2 || refs[0].Code != modules.RefuseCrossCheck || refs[0].Path != "data.extends[0]" || refs[1].Path != "data.views[0]" {
		t.Fatalf("cross-check: %v", refs)
	}
}

func TestAccepts(t *testing.T) {
	a := modules.Accepts()
	if !strings.HasPrefix(a.ManifestSchema, "sha256:") || a.ManifestSchema != modules.Accepts().ManifestSchema {
		t.Fatal(a.ManifestSchema)
	}
	fields := strings.Join(a.ManifestFields, " ")
	for _, f := range []string{"id", "sidebar[*].path", "widgets[*].target.params", "data.extends[*].columns[*].attorney_only", "permissions[*].required"} {
		if !strings.Contains(" "+fields+" ", " "+f+" ") {
			t.Fatalf("%s missing from %s", f, fields)
		}
	}
	if strings.Contains(fields, "notifications") || len(a.Scopes) != len(identity.Scopes()) || len(a.ForbiddenScopes) != 3 {
		t.Fatalf("accepts: %+v", a)
	}
	if !strings.Contains(strings.Join(a.CoreRelations, " "), "core.matters") || !strings.Contains(strings.Join(a.CoreRecords, " "), "time_entry") {
		t.Fatalf("records: %+v", a)
	}
}

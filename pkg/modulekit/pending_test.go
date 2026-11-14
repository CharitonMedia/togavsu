package modulekit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func replaceIn(f map[string][]byte, name, old, new string) {
	if !strings.Contains(string(f[name]), old) {
		panic("fixture text not found: " + old)
	}
	f[name] = []byte(strings.Replace(string(f[name]), old, new, 1))
}

// waiting is the sample module with parts the core doesn't accept yet: an unknown
// top-level block, sidebar keys (one of them an item's first key), unknown scopes, and a
// migration at the end that links to a core table modules can't link to yet.
func waiting(t *testing.T) map[string][]byte {
	t.Helper()
	return edit(sample(t), func(f map[string][]byte) {
		m := modulekit.ManifestFile
		replaceIn(f, m, "tabs:\n", "notifications:\n  - kind: hours_low\n    label: Hours below target\nsidebar:\n  - section: time\n    id: clock\n    title: Matter Clock\n    path: /tab\n    icon: clock\ntabs:\n")
		replaceIn(f, m, "permissions:\n", "permissions:\n  - scope: books:read\n    reason: Read the books.\n")
		replaceIn(f, m, "  - scope: events:read\n", "  - scope: firm:read\n    reason: Read the firm name.\n  - scope: events:read\n")
		f["migrations/0002_trust_link.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN trust_account_id TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT;\n")
		f[modulekit.PendingFile] = []byte(`# Parts that wait on a core change.
pending:
  - field: notifications
    core: CORE-03
    reason: Tell the attorney when a matter falls behind.
  - field: sidebar[*].section
    core: CORE-08
    reason: File the entry under Time.
  - field: sidebar[].icon
    core: CORE-08
    reason: Show a clock in the sidebar.
  - scope: books:read
    core: CORE-02
    reason: Read the books without a viewer.
  - scope: firm:read
    core: CORE-18
    reason: Show the firm name on the tab.
  - scope: matters:read
    core: CORE-00
    reason: Already in the core, so never held back.
  - field: help.heading
    core: CORE-00
    reason: Accepted, so never held back.
  - migration: migrations/0002_trust_link.sql
    core: CORE-22
    reason: Link a target to its trust account.
`)
	})
}

func TestCheckAllReportsEveryRefusal(t *testing.T) {
	files := waiting(t)
	refs := modulekit.CheckAll(files)
	var codes []string
	for _, r := range refs {
		codes = append(codes, r.Code+" "+r.Where())
	}
	want := []string{
		"unknown_field togavsu-module.yaml notifications",
		"unknown_field togavsu-module.yaml sidebar[0].icon",
		"unknown_field togavsu-module.yaml sidebar[0].section",
		"unknown_scope togavsu-module.yaml permissions[0]",
		"unknown_scope togavsu-module.yaml permissions[3]",
		"core_reference migrations/0002_trust_link.sql statement 1",
	}
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refusals:\n%s\nwant:\n%s", strings.Join(codes, "\n"), strings.Join(want, "\n"))
	}
	_, _, err := modulekit.Check(files)
	var all modulekit.Refusals
	var one modulekit.Refusal
	if !errors.As(err, &all) || len(all) != len(want) || !errors.As(err, &one) || one.Code != modulekit.RefuseUnknownField {
		t.Fatalf("Check's error: %v", err)
	}
	if modulekit.Code(err) != "validation" || !strings.Contains(err.Error(), `unknown field "notifications"`) {
		t.Fatalf("Code and text: %q %v", modulekit.Code(err), err)
	}
	if refs := modulekit.CheckAll(sample(t)); refs != nil {
		t.Fatalf("sample: %v", refs)
	}
}

func TestScopesAreTheCoresList(t *testing.T) {
	scopes := modulekit.Scopes()
	has := map[string]bool{}
	for _, s := range scopes {
		has[s] = true
	}
	if !has["matters:read"] || !has["events:read"] || has["books:read"] {
		t.Fatalf("scopes: %v", scopes)
	}
	if !modulekit.KnownScope("views:other-module.view:read") || modulekit.KnownScope("firm:read") {
		t.Fatal("KnownScope")
	}
	a := modulekit.Accepts()
	if strings.Join(a.Scopes, " ") != strings.Join(scopes, " ") || !strings.HasPrefix(a.ManifestSchema, "sha256:") {
		t.Fatalf("Accepts: %+v", a)
	}
	// Check refuses an unknown scope at package time.
	unknown := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "permissions:\n", "permissions:\n  - scope: mail:send\n    reason: Send mail.\n")
	})
	refs := modulekit.CheckAll(unknown)
	if len(refs) != 1 || refs[0].Code != modulekit.RefuseUnknownScope || refs[0].Value != "mail:send" || refs[0].Path != "permissions[0]" {
		t.Fatalf("unknown scope: %v", refs)
	}
}

func TestInstallableHoldsBackExactlyTheRefusedPendingParts(t *testing.T) {
	files := waiting(t)
	out, held, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range held {
		got = append(got, h.Pending.Core+" "+h.File+" "+h.Part)
	}
	want := []string{
		"CORE-03 togavsu-module.yaml notifications",
		"CORE-08 togavsu-module.yaml sidebar[0].icon",
		"CORE-08 togavsu-module.yaml sidebar[0].section",
		"CORE-02 togavsu-module.yaml permissions[0]",
		"CORE-18 togavsu-module.yaml permissions[3]",
		"CORE-22 migrations/0002_trust_link.sql migrations/0002_trust_link.sql",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("held:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, ok := out[modulekit.PendingFile]; ok {
		t.Fatal("the pending list ships")
	}
	if _, ok := out["migrations/0002_trust_link.sql"]; ok {
		t.Fatal("the held migration ships")
	}
	m, err := modulekit.ParseManifest(out[modulekit.ManifestFile])
	if err != nil {
		t.Fatal(err)
	}
	// The accepted parts stay: the sidebar entry without its pending keys, matters:read,
	// the help heading; the manifest keeps its comments.
	if len(m.Sidebar) != 1 || m.Sidebar[0].ID != "clock" || m.Help.Heading != "Matter Clock" || strings.Join(m.Scopes(), " ") != "events:read matters:read time:read" {
		t.Fatalf("installable manifest: %+v", m)
	}
	if !strings.HasPrefix(string(out[modulekit.ManifestFile]), "# Matter Clock: the sample module") {
		t.Fatal("comments lost")
	}
	if !strings.Contains(held[1].String(), "waits on CORE-08: Show a clock in the sidebar.") || !strings.Contains(held[3].String(), "(scope books:read)") {
		t.Fatalf("held lines: %s / %s", held[1], held[3])
	}
	entries, err := modulekit.ParsePending(files[modulekit.PendingFile])
	if err != nil {
		t.Fatal(err)
	}
	var unused []string
	for _, e := range modulekit.Unused(entries, held) {
		unused = append(unused, e.Part())
	}
	if strings.Join(unused, ", ") != "scope matters:read, field help.heading" {
		t.Fatalf("unused: %v", unused)
	}
	// The result passes Check, Build signs it, and the core's whole check verifies it.
	if _, _, err := modulekit.Check(out); err != nil {
		t.Fatal(err)
	}
	pub, priv := key(t)
	signed, held2, err := modulekit.BuildInstallable(priv, files)
	if err != nil || len(held2) != len(held) {
		t.Fatalf("BuildInstallable: %v", err)
	}
	if v, ms, err := modulekit.VerifyFile(signed, []modulekit.TrustedKey{modulekit.Trust(pub)}); err != nil || v.ModuleID != "matter-clock" || len(ms) != 1 {
		t.Fatalf("verify: %v", err)
	}
	// Without a pending list, Installable is the package as it is.
	plain, none, err := modulekit.Installable(sample(t))
	if err != nil || len(none) != 0 || string(plain[modulekit.ManifestFile]) != string(sample(t)[modulekit.ManifestFile]) {
		t.Fatalf("no pending list: %v %v", none, err)
	}
}

func TestInstallableKeepsWhatTheCoreAccepts(t *testing.T) {
	// A pending part the core now accepts is kept, with no edit to the pending list.
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "  - scope: books:read\n    reason: Read the books.\n", "")
		replaceIn(f, modulekit.ManifestFile, "    icon: clock\n", "")
	})
	_, held, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range held {
		if h.Pending.Scope == "books:read" || h.Pending.Field == "sidebar[].icon" {
			t.Fatalf("held a part the package no longer has: %s", h)
		}
	}
	if len(held) != 4 {
		t.Fatalf("held: %v", held)
	}
}

func TestInstallableRefusesWhatNoEntryCovers(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "  events: /events\n", "  events: /events\n  system_mail: true\n")
		replaceIn(f, modulekit.ManifestFile, "permissions:\n", "permissions:\n  - scope: mail:send\n    reason: Send mail.\n")
	})
	_, _, err := modulekit.Installable(files)
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || len(refs) != 2 {
		t.Fatalf("uncovered: %v", err)
	}
	if refs[0].Path != "container.system_mail" || refs[1].Value != "mail:send" {
		t.Fatalf("uncovered: %v", refs)
	}
	// A held-back part whose removal breaks what is left is refused, not shipped broken:
	// the held migration creates the table a declared extension needs.
	broken := edit(waiting(t), func(f map[string][]byte) {
		f["migrations/0002_trust_link.sql"] = []byte("CREATE TABLE trust_links (matter_id TEXT PRIMARY KEY REFERENCES core.matters (id) ON DELETE RESTRICT, account_id TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT);\n")
		replaceIn(f, modulekit.ManifestFile, "  views:\n", "    - name: links\n      record: matter\n      table: trust_links\n      key: matter_id\n      columns:\n        - name: account_id\n  views:\n")
	})
	_, _, err = modulekit.Installable(broken)
	if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefuseCrossCheck || refs[0].Path != "data.extends[1]" {
		t.Fatalf("removal that breaks the rest: %v", err)
	}
}

func TestInstallableHoldsMigrationsAtTheEnd(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		f["migrations/0003_index.sql"] = []byte("CREATE INDEX matter_targets_note ON matter_targets (note);\n")
	})
	_, _, err := modulekit.Installable(files)
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefusePendingOrder || refs[0].File != "migrations/0002_trust_link.sql" {
		t.Fatalf("held migration before an installed one: %v", err)
	}
}

func TestParsePending(t *testing.T) {
	for name, src := range map[string]string{
		"two parts":     "pending:\n  - field: a\n    scope: b:read\n    core: C\n    reason: r\n",
		"no part":       "pending:\n  - core: C\n    reason: r\n",
		"no core":       "pending:\n  - field: a\n    reason: r\n",
		"no reason":     "pending:\n  - field: a\n    core: C\n",
		"bad path":      "pending:\n  - field: a..b\n    core: C\n    reason: r\n",
		"bad migration": "pending:\n  - migration: 0002_x.sql\n    core: C\n    reason: r\n",
		"duplicate":     "pending:\n  - scope: a:read\n    core: C\n    reason: r\n  - scope: a:read\n    core: D\n    reason: r\n",
		"unknown key":   "pending:\n  - field: a\n    core: C\n    reason: r\n    item: x\n",
		"not yaml":      "pending: [x\n",
	} {
		_, err := modulekit.ParsePending([]byte(src))
		var refs modulekit.Refusals
		if !errors.As(err, &refs) || refs[0].Code != modulekit.RefusePendingInvalid {
			t.Fatalf("%s: %v", name, err)
		}
		if _, _, err := modulekit.Installable(map[string][]byte{modulekit.PendingFile: []byte(src)}); err == nil {
			t.Fatalf("%s: Installable read a bad list", name)
		}
	}
}

func TestBuildChecks(t *testing.T) {
	_, priv := key(t)
	unknown := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "tabs:\n", "notifications:\n  - kind: a\ntabs:\n")
	})
	if signed, err := modulekit.Build(priv, unknown); err == nil || signed != nil || modulekit.Code(err) != "validation" {
		t.Fatalf("Build signed an unknown field: %v", err)
	}
	scope := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "permissions:\n", "permissions:\n  - scope: books:read\n    reason: r\n")
	})
	if _, err := modulekit.Build(priv, scope); err == nil {
		t.Fatal("Build signed an unknown scope")
	}
	_, err := modulekit.Build(priv, waiting(t))
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || refs[0].Code != modulekit.RefusePendingUnbuilt {
		t.Fatalf("Build with a pending list: %v", err)
	}
	if _, err := modulekit.Build(priv, sample(t)); err != nil {
		t.Fatal(err)
	}
}

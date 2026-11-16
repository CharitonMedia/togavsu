package modulekit_test

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

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
		f["migrations/0002_books_link.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN books_ledger_id TEXT REFERENCES core.books_ledgers (id) ON DELETE RESTRICT;\n")
		f[modulekit.PendingFile] = []byte(`# Parts that wait on a core change.
pending:
  - field: notifications
    core: CORE-03
    reason: Tell the attorney when a matter falls behind.
  - field: sidebar[*].section
    core: CORE-08
    reason: File the entry under Time.
  - scope: books:read
    core: CORE-02
    reason: Read the books without a viewer.
  - scope: matters:read
    core: CORE-00
    reason: Already in the core, so never held back.
  - field: help.heading
    core: CORE-00
    reason: Accepted, so never held back.
  - migration: migrations/0002_books_link.sql
    core: CORE-02
    reason: Link a target to a books ledger the core does not expose yet.
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
		"unknown_file pending.yaml",
		"unknown_field togavsu-module.yaml notifications",
		"manifest_invalid togavsu-module.yaml sidebar[0].section",
		"unknown_scope togavsu-module.yaml permissions[0]",
		"core_reference migrations/0002_books_link.sql statement 1",
	}
	if strings.Join(codes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("refusals:\n%s\nwant:\n%s", strings.Join(codes, "\n"), strings.Join(want, "\n"))
	}
	_, _, err := modulekit.Check(files)
	var all modulekit.Refusals
	var one modulekit.Refusal
	if !errors.As(err, &all) || len(all) != len(want) || !errors.As(err, &one) || one.Code != modulekit.RefuseUnknownFile {
		t.Fatalf("Check's error: %v", err)
	}
	if !strings.Contains(all[0].Message, "-installable") {
		t.Fatalf("the pending list's refusal says how to build: %s", all[0].Message)
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
	if !has["matters:read"] || !has["events:read"] || !has["firm:read"] || has["books:read"] {
		t.Fatalf("scopes: %v", scopes)
	}
	if !modulekit.KnownScope("views:other-module.view:read") || !modulekit.KnownScope("firm:read") || modulekit.KnownScope("books:read") {
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
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	out, held := ip.Files, ip.Held
	var got []string
	for _, h := range held {
		got = append(got, h.Pending.Core.String()+" "+h.File+" "+h.Part)
	}
	// In the order the parts are written: permissions come before the notifications and
	// sidebar blocks in the fixture, and an item's section before its icon.
	want := []string{
		"CORE-02 togavsu-module.yaml permissions[0]",
		"CORE-03 togavsu-module.yaml notifications",
		"CORE-08 togavsu-module.yaml sidebar[0].section",
		"CORE-02 migrations/0002_books_link.sql migrations/0002_books_link.sql",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("held:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, ok := out[modulekit.PendingFile]; ok {
		t.Fatal("the pending list ships")
	}
	if _, ok := out["migrations/0002_books_link.sql"]; ok {
		t.Fatal("the held migration ships")
	}
	m, err := modulekit.ParseManifest(out[modulekit.ManifestFile])
	if err != nil {
		t.Fatal(err)
	}
	// The accepted parts stay: the sidebar entry without its pending keys, matters:read,
	// the help heading; the manifest keeps its comments.
	if len(m.Sidebar) != 1 || m.Sidebar[0].ID != "clock" || m.Help.Heading != "Matter Clock" || strings.Join(m.Scopes(), " ") != "events:read firm:read matters:read time:read" {
		t.Fatalf("installable manifest: %+v", m)
	}
	if !strings.HasPrefix(string(out[modulekit.ManifestFile]), "# Matter Clock: the sample module") {
		t.Fatal("comments lost")
	}
	if !strings.Contains(held[2].String(), "waits on CORE-08:") || !strings.Contains(held[0].String(), "(scope books:read)") {
		t.Fatalf("held lines: %s / %s", held[2], held[0])
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
	// Installable returns the parsed list and the same unused entries.
	var fromIP []string
	for _, e := range ip.Unused {
		fromIP = append(fromIP, e.Part())
	}
	if len(ip.Pending) != len(entries) || strings.Join(fromIP, ", ") != strings.Join(unused, ", ") {
		t.Fatalf("Installable's pending %d and unused %v", len(ip.Pending), fromIP)
	}
	if refs := ip.UnusedRefusals(); len(refs) != 2 || refs[0].Code != modulekit.RefusePendingUnused || refs[0].Value != "scope matters:read" {
		t.Fatalf("UnusedRefusals: %v", refs)
	}
	// The result passes Check, Build signs it, and the core's whole check verifies it.
	if _, _, err := modulekit.Check(out); err != nil {
		t.Fatal(err)
	}
	pub, priv := key(t)
	signed, ip2, err := modulekit.BuildInstallable(priv, files)
	if err != nil || len(ip2.Held) != len(held) || len(ip2.Unused) != len(unused) {
		t.Fatalf("BuildInstallable: %v", err)
	}
	if v, ms, err := modulekit.VerifyFile(signed, []modulekit.TrustedKey{modulekit.Trust(pub)}); err != nil || v.ModuleID != "matter-clock" || len(ms) != 1 {
		t.Fatalf("verify: %v", err)
	}
	// Without a pending list, Installable is the package as it is.
	plain, err := modulekit.Installable(sample(t))
	if err != nil || len(plain.Held) != 0 || string(plain.Files[modulekit.ManifestFile]) != string(sample(t)[modulekit.ManifestFile]) {
		t.Fatalf("no pending list: %v %v", plain.Held, err)
	}
}

func TestInstallableKeepsWhatTheCoreAccepts(t *testing.T) {
	// A pending part the core now accepts is kept, with no edit to the pending list.
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "  - scope: books:read\n    reason: Read the books.\n", "")
		replaceIn(f, modulekit.ManifestFile, "    icon: clock\n", "")
	})
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	held := ip.Held
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
	_, err := modulekit.Installable(files)
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
		f["migrations/0002_books_link.sql"] = []byte("CREATE TABLE trust_links (matter_id TEXT PRIMARY KEY REFERENCES core.matters (id) ON DELETE RESTRICT, account_id TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT);\n")
		replaceIn(f, modulekit.ManifestFile, "  views:\n", "    - name: links\n      record: matter\n      table: trust_links\n      key: matter_id\n      columns:\n        - name: account_id\n  views:\n")
	})
	_, err = modulekit.Installable(broken)
	if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefuseCrossCheck || refs[0].Path != "data.extends[1]" {
		t.Fatalf("removal that breaks the rest: %v", err)
	}
}

func TestInstallableHoldsMigrationsAtTheEnd(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		f["migrations/0003_index.sql"] = []byte("CREATE INDEX matter_targets_note ON matter_targets (note);\n")
	})
	_, err := modulekit.Installable(files)
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefusePendingOrder || refs[0].File != "migrations/0002_books_link.sql" {
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
		if _, err := modulekit.Installable(map[string][]byte{modulekit.PendingFile: []byte(src)}); err == nil {
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

func TestCheckUpdateReadsWhatShipped(t *testing.T) {
	_, priv := key(t)
	signed, ip, err := modulekit.BuildInstallable(priv, waiting(t))
	if err != nil || len(ip.Held) == 0 {
		t.Fatalf("v1: %v", err)
	}
	sp, shipped, err := modulekit.ReadSignedFile(signed)
	if err != nil || sp.ListingID != "matter-clock" {
		t.Fatalf("ReadSignedFile: %v", err)
	}
	next := func(f func(map[string][]byte)) []byte {
		files := edit(waiting(t), func(m map[string][]byte) {
			replaceIn(m, modulekit.ManifestFile, `version: "1.0.0"`, `version: "1.1.0"`)
			f(m)
		})
		ip, err := modulekit.Installable(files)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := modulekit.Archive(ip.Files)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	// The held migration never shipped, so it may change, or ship now.
	if err := modulekit.CheckUpdate(shipped, next(func(m map[string][]byte) {
		m["migrations/0002_books_link.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN trust_note TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT;\n")
	})); err != nil {
		t.Fatalf("changed held migration: %v", err)
	}
	changed := next(func(m map[string][]byte) {
		m["migrations/0001_matter_targets.sql"] = append(m["migrations/0001_matter_targets.sql"], []byte("\n-- changed\n")...)
	})
	err = modulekit.CheckUpdate(shipped, changed)
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || refs[0].Code != modulekit.RefuseUpgrade || modulekit.Code(err) != modulekit.CodeMigration {
		t.Fatalf("changed shipped migration: %v", err)
	}
}

// A package directory's other files (a files.go that embeds it, a README) never ship,
// and the core refuses a package that holds one.
func TestThePackageFileSetIsExplicit(t *testing.T) {
	fsys := fstest.MapFS{}
	for name, body := range waiting(t) {
		fsys[name] = &fstest.MapFile{Data: body}
	}
	fsys["files.go"] = &fstest.MapFile{Data: []byte("package pkgfiles\n")}
	fsys["README.md"] = &fstest.MapFile{Data: []byte("# notes\n")}
	fsys["docs/notes.md"] = &fstest.MapFile{Data: []byte("notes\n")}
	files, err := modulekit.Files(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	if strings.Join(names, " ") != "migrations/0001_matter_targets.sql migrations/0002_books_link.sql pending.yaml togavsu-module.yaml" {
		t.Fatalf("package files: %v", names)
	}
	extra := edit(sample(t), func(f map[string][]byte) { f["files.go"] = []byte("package pkgfiles\n") })
	refs := modulekit.CheckAll(extra)
	if len(refs) != 1 || refs[0].Code != modulekit.RefuseUnknownFile || refs[0].File != "files.go" {
		t.Fatalf("extra file: %v", refs)
	}
	_, priv := key(t)
	if _, err := modulekit.Build(priv, extra); err == nil {
		t.Fatal("Build signed a package with a file the core doesn't read")
	}
	raw, err := modulekit.Archive(extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := modulekit.CheckPackage(raw); err == nil || modulekit.Code(err) != "validation" || !strings.Contains(err.Error(), "files.go") {
		t.Fatalf("the core's install check: %v", err)
	}
}

func TestUncoveredRefusalsSaySo(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "  events: /events\n", "  events: /events\n  system_mail: true\n")
	})
	_, err := modulekit.Installable(files)
	var refs modulekit.Refusals
	if !errors.As(err, &refs) || len(refs) != 1 {
		t.Fatalf("uncovered: %v", err)
	}
	if !strings.HasSuffix(refs[0].Message, "(not covered by pending.yaml)") || !strings.HasSuffix(err.Error(), "(not covered by pending.yaml)") {
		t.Fatalf("uncovered text: %q / %q", refs[0].Message, err.Error())
	}
	if modulekit.Code(err) != "validation" {
		t.Fatalf("code: %s", modulekit.Code(err))
	}
}

// Every refused manifest value is reported, not only the first.
func TestCheckAllReportsEveryRefusedValue(t *testing.T) {
	files := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "    placement: matter\n", "    placement: client\n")
		replaceIn(f, modulekit.ManifestFile, "    type: int\n", "    type: number\n")
		replaceIn(f, modulekit.ManifestFile, "  - time_entry.*\n", "  - Time Entry\n")
	})
	var got []string
	for _, r := range modulekit.CheckAll(files) {
		got = append(got, r.Code+" "+r.Path)
	}
	want := "manifest_invalid tabs[1].placement, manifest_invalid events[0], manifest_invalid settings[0].type"
	if strings.Join(got, ", ") != want {
		t.Fatalf("refusals: %v", got)
	}
}

// A field path can select list items by a key's value, and index paths name the
// manifest as written, however many items before them are held back.
func TestPendingPathsNameTheManifestAsWritten(t *testing.T) {
	clientTabs := func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "help:\n", "  - id: pay\n    title: Pay\n    placement: client\n    path: /pay\n"+
			"  # The client's signing tab.\n  - id: sign\n    title: Sign\n    placement: client\n    path: /sign\n"+
			"  - id: account\n    title: Account\n    placement: client\n    path: /account\nhelp:\n")
	}
	for name, pending := range map[string]string{
		"selector": "pending:\n  - field: tabs[placement=client]\n    core: CORE-10\n    reason: Client tabs.\n",
		"indexes": "pending:\n  - field: tabs[2]\n    core: CORE-10\n    reason: Pay.\n  - field: tabs[3]\n    core: CORE-10\n    reason: Sign.\n" +
			"  - field: tabs[4]\n    core: CORE-10\n    reason: Account.\n",
		"ids": "pending:\n  - field: tabs[id=pay]\n    core: CORE-10\n    reason: Pay.\n  - field: tabs[id=sign]\n    core: CORE-10\n    reason: Sign.\n" +
			"  - field: tabs[id=\"account\"]\n    core: CORE-10\n    reason: Account.\n",
	} {
		files := edit(sample(t), func(f map[string][]byte) {
			clientTabs(f)
			f[modulekit.PendingFile] = []byte(pending)
		})
		ip, err := modulekit.Installable(files)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var parts []string
		for _, h := range ip.Held {
			parts = append(parts, h.Part)
		}
		sort.Strings(parts)
		if strings.Join(parts, " ") != "tabs[2] tabs[3] tabs[4]" || len(ip.Unused) != 0 {
			t.Fatalf("%s: held %v, unused %v", name, parts, ip.Unused)
		}
		if string(ip.Files[modulekit.ManifestFile]) != string(sample(t)[modulekit.ManifestFile]) {
			t.Fatalf("%s: the manifest is not the sample again:\n%s", name, ip.Files[modulekit.ManifestFile])
		}
	}
	// A refusal that shows only once an earlier part is gone (the widget checks report
	// their first) still maps to the manifest as written.
	files := edit(sample(t), func(f map[string][]byte) {
		widget := func(id string) string {
			return "  - id: " + id + "\n    title: " + id + "\n    path: " + id + "\n    size:\n      w: 4\n      h: 2\n"
		}
		replaceIn(f, modulekit.ManifestFile, "tabs:\n", widget("one")+widget("two")+"tabs:\n")
		f[modulekit.PendingFile] = []byte("pending:\n  - field: widgets[1]\n    core: CORE-30\n    reason: One.\n  - field: widgets[2]\n    core: CORE-31\n    reason: Two.\n")
	})
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ip.Held) != 2 || ip.Held[0].Part != "widgets[1]" || ip.Held[1].Part != "widgets[2]" || ip.Held[1].Refusals[0].Path != "widgets[2].path" || len(ip.Unused) != 0 {
		t.Fatalf("held %v, unused %v", ip.Held, ip.Unused)
	}
}

func TestHeldPartsTakeTheirOwnComments(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "  - scope: books:read\n", "  # CORE-02: the books, with no one signed in.\n  - scope: books:read\n")
	})
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ip.Files[modulekit.ManifestFile]), "CORE-02") {
		t.Fatalf("the held scope's comment stayed:\n%s", ip.Files[modulekit.ManifestFile])
	}
}

func TestPendingCoreIsOneItemOrAList(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.PendingFile, "    core: CORE-22\n", "    core: [CORE-22, CORE-24]\n")
	})
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	last := ip.Held[len(ip.Held)-1]
	if strings.Join(last.Pending.Core, ",") != "CORE-22,CORE-24" || !strings.Contains(last.String(), "waits on CORE-22 and CORE-24: ") {
		t.Fatalf("core list: %v %s", last.Pending.Core, last)
	}
	if ip.Held[0].Pending.Core.String() != "CORE-02" {
		t.Fatalf("one item: %v", ip.Held[0].Pending.Core)
	}
	for _, bad := range []string{"core: []", "core: {a: b}"} {
		src := "pending:\n  - field: a\n    " + bad + "\n    reason: r\n"
		if _, err := modulekit.ParsePending([]byte(src)); err == nil {
			t.Fatalf("%s was read", bad)
		}
	}
}

// Each kind of entry holds back only the refusals it can cover, and a held migration is
// checked as if the core relations it links to existed.
func TestPendingEntriesCoverOnlyTheirKind(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(map[string][]byte)
		code string
		why  string
	}{
		"another statement in a held migration": {func(f map[string][]byte) {
			f["migrations/0002_books_link.sql"] = append(f["migrations/0002_books_link.sql"], []byte("CREATE TABLE public.leak (id TEXT);\n")...)
		}, modulekit.RefuseStatement, "a migration entry holds back only the file's references to core relations"},
		"a second problem in the held statement": {func(f map[string][]byte) {
			replaceIn(f, "migrations/0002_books_link.sql", "ON DELETE RESTRICT", "ON DELETE CASCADE")
		}, modulekit.RefuseStatement, "held back for CORE-22, but refused even once the core relations it references exist"},
		"a scope named by a field entry": {func(f map[string][]byte) {
			replaceIn(f, modulekit.PendingFile, "  - scope: books:read\n", "  - field: permissions[0]\n")
		}, modulekit.RefuseUnknownScope, "name the scope in a scope entry"},
	} {
		_, err := modulekit.Installable(edit(waiting(t), tc.edit))
		var refs modulekit.Refusals
		if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != tc.code || !strings.Contains(refs[0].Message, tc.why) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// A value the core requires can't be held back alone: removing it leaves the same
// refusal, so Installable stops with pending_edit after one more pass instead of
// looping.
func TestHoldingBackARequiredValueFailsClosed(t *testing.T) {
	defer modulekit.SetPassHook(func(pass int) {
		if pass > 3 {
			t.Fatalf("Installable is still looping at pass %d", pass)
		}
	})()
	for name, tc := range map[string]struct {
		edit func(map[string][]byte)
		part string
	}{
		"a tab's placement": {func(f map[string][]byte) {
			replaceIn(f, modulekit.ManifestFile, "    placement: matter\n", "    placement: client\n")
			f[modulekit.PendingFile] = []byte("pending:\n  - field: tabs[1].placement\n    core: CORE-10\n    reason: A client tab.\n")
		}, "tabs[1].placement"},
		"the version": {func(f map[string][]byte) {
			replaceIn(f, modulekit.ManifestFile, `version: "1.0.0"`, `version: "1.0.0 beta"`)
			f[modulekit.PendingFile] = []byte("pending:\n  - field: version\n    core: CORE-99\n    reason: A new version form.\n")
		}, "version"},
		"the container port": {func(f map[string][]byte) {
			replaceIn(f, modulekit.ManifestFile, "  port: 8080\n", "  port: 99999\n")
			f[modulekit.PendingFile] = []byte("pending:\n  - field: container.port\n    core: CORE-99\n    reason: A wider port range.\n")
		}, "container.port"},
	} {
		_, err := modulekit.Installable(edit(sample(t), tc.edit))
		var refs modulekit.Refusals
		if !errors.As(err, &refs) || len(refs) != 1 || refs[0].Code != modulekit.RefusePendingEdit || refs[0].Path != tc.part ||
			!strings.Contains(refs[0].Message, "hold back the whole item") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// Holding back the whole tab works.
	files := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "    placement: matter\n", "    placement: client\n")
		f[modulekit.PendingFile] = []byte("pending:\n  - field: tabs[1]\n    core: CORE-10\n    reason: A client tab.\n")
	})
	if ip, err := modulekit.Installable(files); err != nil || len(ip.Held) != 1 || ip.Held[0].Part != "tabs[1]" {
		t.Fatalf("whole tab: %v", err)
	}
}

// Installable finds held parts pass by pass (the tab checks report their first refusal),
// but lists them in the order they are written: client-portal's tabs came out as
// tabs[3], tabs[4], tabs[5], tabs[2], tabs[6].
func TestHeldPartsComeInManifestOrder(t *testing.T) {
	files := edit(waiting(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, "help:\n", "  - id: pay\n    title: Pay\n    placement: client\n    path: /pay\n"+
			"  - id: sign\n    title: Sign\n    placement: matter\n    path: /sign\n    nav: client\n"+
			"  - id: account\n    title: Account\n    placement: client\n    path: /account\nhelp:\n")
		replaceIn(f, modulekit.PendingFile, "pending:\n", "pending:\n  - field: tabs[2]\n    core: CORE-10\n    reason: Pay.\n"+
			"  - field: tabs[3]\n    core: CORE-10\n    reason: Sign.\n  - field: tabs[4]\n    core: CORE-10\n    reason: Account.\n")
	})
	passes := 0
	defer modulekit.SetPassHook(func(int) { passes++ })()
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if passes < 2 {
		t.Fatalf("the fixture should need several passes, took %d", passes)
	}
	var got []string
	for _, h := range ip.Held {
		got = append(got, h.Part)
	}
	want := "permissions[0] permissions[3] notifications sidebar[0].section sidebar[0].icon tabs[2] tabs[3] tabs[4] migrations/0002_books_link.sql"
	if strings.Join(got, " ") != want {
		t.Fatalf("held in order:\n%s\nwant:\n%s", strings.Join(got, " "), want)
	}
}

func TestCoreItemsHas(t *testing.T) {
	c := modulekit.CoreItems{"CORE-22", " CORE-24"}
	for item, want := range map[string]bool{"CORE-22": true, "core-24": true, " CORE-22 ": true, "CORE-2": false, "": false, "CORE-10": false} {
		if c.Has(item) != want {
			t.Fatalf("Has(%q) = %v", item, !want)
		}
	}
	if (modulekit.CoreItems{}).Has("CORE-22") {
		t.Fatal("an empty list has an item")
	}
	entries, err := modulekit.ParsePending([]byte("pending:\n  - field: a\n    core: [CORE-22, CORE-24]\n    reason: r\n  - field: b\n    core: CORE-03\n    reason: r\n"))
	if err != nil || !entries[0].Core.Has("CORE-24") || entries[1].Core.Has("CORE-24") || !entries[1].Core.Has("CORE-03") {
		t.Fatalf("parsed: %v %v", entries, err)
	}
}

func TestWritePendingReadsBack(t *testing.T) {
	entries := []modulekit.Pending{
		{Field: "tabs[placement=client]", Core: modulekit.CoreItems{"CORE-10"}, Reason: "The client tabs (Dashboard, Documents), framed in the portal."},
		{Field: `tabs[title="Pay now"]`, Core: modulekit.CoreItems{"CORE-10", "CORE-12"}, Reason: "Pay: the client's invoice # and amount."},
		{Scope: "books:read", Core: modulekit.CoreItems{"CORE-02"}, Reason: "true"},
		{Migration: "migrations/0004_trust_bank_map.sql", Core: modulekit.CoreItems{"CORE-22"}, Reason: "Map each trust account to its bank account: "},
		{Field: "sidebar[*].section", Core: modulekit.CoreItems{"CORE-08"}, Reason: "- [x] 12"},
	}
	src, err := modulekit.WritePending(entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := modulekit.ParsePending(src)
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	if len(got) != len(entries) {
		t.Fatalf("read back %d entries:\n%s", len(got), src)
	}
	for i := range entries {
		if got[i].Part() != entries[i].Part() || got[i].Reason != entries[i].Reason || strings.Join(got[i].Core, ",") != strings.Join(entries[i].Core, ",") {
			t.Fatalf("entry %d: %+v, want %+v\n%s", i, got[i], entries[i], src)
		}
	}
	// Plain where that reads back, so the file stays readable.
	if !strings.Contains(string(src), "  - field: tabs[placement=client]\n    core: CORE-10\n") || !strings.Contains(string(src), "core: [CORE-10, CORE-12]") {
		t.Fatalf("not plain:\n%s", src)
	}
	// What ParsePending refuses, WritePending refuses.
	for _, bad := range [][]modulekit.Pending{
		{{Field: "a", Scope: "b", Core: modulekit.CoreItems{"CORE-01"}, Reason: "r"}},
		{{Field: "a", Reason: "r"}},
		{{Field: "a", Core: modulekit.CoreItems{"CORE-01"}}},
		{{Field: "a", Core: modulekit.CoreItems{"CORE-01"}, Reason: "r"}, {Field: "a", Core: modulekit.CoreItems{"CORE-02"}, Reason: "s"}},
	} {
		if _, err := modulekit.WritePending(bad); err == nil {
			t.Fatalf("written: %+v", bad)
		} else if refs := (modulekit.Refusals{}); !errors.As(err, &refs) || refs[0].Code != modulekit.RefusePendingInvalid {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}

func TestAddPendingKeepsTheFile(t *testing.T) {
	src := []byte("# Parts that wait on a core change.\npending:\n    # The sidebar.\n    - field: sidebar[*].section\n      core: CORE-08\n      reason: File the entry under Time.")
	out, err := modulekit.AddPending(src, modulekit.Pending{Scope: "books:read", Core: modulekit.CoreItems{"CORE-02"}, Reason: "Read the books."})
	if err != nil {
		t.Fatal(err)
	}
	want := string(src) + "\n    - scope: books:read\n      core: CORE-02\n      reason: Read the books.\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	// A new list starts the file.
	fresh, err := modulekit.AddPending(nil, modulekit.Pending{Field: "notifications", Core: modulekit.CoreItems{"CORE-03"}, Reason: "Tell admins."})
	if err != nil || string(fresh) != "pending:\n  - field: notifications\n    core: CORE-03\n    reason: Tell admins.\n" {
		t.Fatalf("new list: %v\n%s", err, fresh)
	}
	// A duplicate is refused, and so is a list it can't append to in place.
	if _, err := modulekit.AddPending(out, modulekit.Pending{Scope: "books:read", Core: modulekit.CoreItems{"CORE-02"}, Reason: "Again."}); err == nil {
		t.Fatal("a duplicate was added")
	}
	if _, err := modulekit.AddPending([]byte("pending: []\n"), modulekit.Pending{Scope: "books:read", Core: modulekit.CoreItems{"CORE-02"}, Reason: "r"}); err == nil {
		t.Fatal("appended to a flow list")
	}
}

func TestWritePendingTakesAnEmptyList(t *testing.T) {
	for name, entries := range map[string][]modulekit.Pending{"nil": nil, "empty": {}} {
		src, err := modulekit.WritePending(entries)
		if err != nil || string(src) != "pending:\n" {
			t.Fatalf("%s: %q %v", name, src, err)
		}
		if got, err := modulekit.ParsePending(src); err != nil || len(got) != 0 {
			t.Fatalf("%s: read back %v %v", name, got, err)
		}
	}
}

func TestAddPendingKeepsCRLF(t *testing.T) {
	src := []byte("# Parts that wait on a core change.\r\npending:\r\n\r\n    # The sidebar.\r\n    - field: sidebar[*].section\r\n      core: CORE-08\r\n      reason: File the entry under Time.\r\n")
	out, err := modulekit.AddPending(src, modulekit.Pending{Scope: "books:read", Core: modulekit.CoreItems{"CORE-02"}, Reason: "Read the books."})
	if err != nil {
		t.Fatal(err)
	}
	want := string(src) + "    - scope: books:read\r\n      core: CORE-02\r\n      reason: Read the books.\r\n"
	if string(out) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
}

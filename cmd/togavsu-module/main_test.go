package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// copyPackage writes the sample module's package files under a new directory, with
// edit applied, and returns the directory.
func copyPackage(t *testing.T, edit func(map[string][]byte)) string {
	t.Helper()
	files, err := matterclock.Files()
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(files)
	}
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func cli(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUsage(t *testing.T) {
	if code, _, _ := cli(); code != exitUsage {
		t.Fatalf("no command: %d", code)
	}
	if code, _, _ := cli("sign"); code != exitUsage {
		t.Fatalf("unknown command: %d", code)
	}
	if code, out, _ := cli("help"); code != exitOK || !strings.Contains(out, "togavsu-module check") {
		t.Fatalf("help: %d %s", code, out)
	}
	if code, _, _ := cli("check"); code != exitUsage {
		t.Fatalf("check without a directory: %d", code)
	}
	if code, _, _ := cli("package", "-dir", "x"); code != exitUsage {
		t.Fatalf("package without key and out: %d", code)
	}
	if code, _, _ := cli("verify", "-file", "x"); code != exitUsage {
		t.Fatalf("verify without a key: %d", code)
	}
}

func TestCheck(t *testing.T) {
	good := copyPackage(t, nil)
	if code, out, errs := cli("check", good); code != exitOK || !strings.Contains(out, "ok: matter-clock 1.0.0, 1 migration(s)") {
		t.Fatalf("check: %d %s %s", code, out, errs)
	}
	bad := copyPackage(t, func(f map[string][]byte) {
		f["migrations/0002_bad.sql"] = []byte("CREATE TABLE contacts (id TEXT PRIMARY KEY);")
	})
	if code, _, errs := cli("check", bad); code != exitRefused || !strings.Contains(errs, "0002_bad") {
		t.Fatalf("refused migration: %d %s", code, errs)
	}
	if code, _, _ := cli("check", filepath.Join(t.TempDir(), "missing")); code != exitRefused {
		t.Fatalf("missing directory: %d", code)
	}
	bump := func(f map[string][]byte) {
		f[modulekit.ManifestFile] = bytes.Replace(f[modulekit.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "1.1.0"`), 1)
	}
	changed := copyPackage(t, func(f map[string][]byte) {
		bump(f)
		f["migrations/0001_matter_targets.sql"] = append(f["migrations/0001_matter_targets.sql"], []byte("\n-- changed\n")...)
	})
	if code, _, _ := cli("check", changed); code != exitOK {
		t.Fatalf("changed migration on its own: %d", code)
	}
	if code, _, errs := cli("check", "-previous", good, changed); code != exitRefused || !strings.Contains(errs, "migration") {
		t.Fatalf("changed migration as an update: %d %s", code, errs)
	}
	added := copyPackage(t, func(f map[string][]byte) {
		bump(f)
		f["migrations/0002_note_index.sql"] = []byte("CREATE INDEX matter_targets_note ON matter_targets (note);")
	})
	if code, out, errs := cli("check", "-previous", good, added); code != exitOK || !strings.Contains(out, "2 migration(s)") {
		t.Fatalf("added migration as an update: %d %s %s", code, out, errs)
	}
}

func TestPackageAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := write(t, "signing.key", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	dir := copyPackage(t, nil)
	out := filepath.Join(t.TempDir(), "matter-clock.signed.json")
	code, stdout, errs := cli("package", "-dir", dir, "-key", seed, "-out", out)
	if code != exitOK || !strings.Contains(stdout, modulekit.KeyID(pub)) || !strings.Contains(stdout, modulekit.PublicKeyPEM(pub)) {
		t.Fatalf("package: %d %s %s", code, stdout, errs)
	}
	signed, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// The command signs exactly what the sample module builds from its embedded files.
	want, err := matterclock.Build(priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(signed, want) {
		t.Fatal("the signed file differs from matterclock.Build")
	}

	pemFile := write(t, "signing.pub", modulekit.PublicKeyPEM(pub))
	if code, stdout, errs := cli("verify", "-file", out, "-pub", pemFile); code != exitOK || !strings.Contains(stdout, "ok: matter-clock 1.0.0 signed by "+modulekit.KeyID(pub)) {
		t.Fatalf("verify: %d %s %s", code, stdout, errs)
	}
	other, _, _ := ed25519.GenerateKey(nil)
	otherFile := write(t, "other.pub", modulekit.PublicKeyPEM(other))
	if code, _, errs := cli("verify", "-file", out, "-pub", otherFile); code != exitRefused || !strings.Contains(errs, "verification") {
		t.Fatalf("verify with another key: %d %s", code, errs)
	}
	if code, _, _ := cli("verify", "-file", out, "-pub", seed); code != exitRefused {
		t.Fatalf("verify with a seed as the key: %d", code)
	}
	tampered := bytes.Replace(signed, []byte(`"version":"1.0.0"`), []byte(`"version":"9.9.9"`), 1)
	tamperedFile := write(t, "tampered.json", string(tampered))
	if code, _, _ := cli("verify", "-file", tamperedFile, "-pub", pemFile); code != exitOK {
		// The listing version is not signed; the package bytes are. A changed listing
		// field alone still verifies, as the core's local install does.
		t.Fatalf("listing field changed: %d", code)
	}
	unsigned, _ := matterclock.Archive(nil)
	unsignedFile := write(t, "unsigned.tar.gz", string(unsigned))
	if code, _, _ := cli("verify", "-file", unsignedFile, "-pub", pemFile); code != exitRefused {
		t.Fatalf("unsigned archive: %d", code)
	}

	// A bad seed or a refused package is not signed, and nothing is written.
	badSeed := write(t, "bad.key", "not a seed")
	skipped := filepath.Join(t.TempDir(), "skipped.json")
	if code, _, _ := cli("package", "-dir", dir, "-key", badSeed, "-out", skipped); code != exitRefused {
		t.Fatalf("bad seed: %d", code)
	}
	refused := copyPackage(t, func(f map[string][]byte) {
		f["migrations/0002_bad.sql"] = []byte("CREATE TABLE contacts (id TEXT PRIMARY KEY);")
	})
	if code, _, _ := cli("package", "-dir", refused, "-key", seed, "-out", skipped); code != exitRefused {
		t.Fatalf("refused package signed: %d", code)
	}
	if _, err := os.Stat(skipped); !os.IsNotExist(err) {
		t.Fatalf("a refused package was written: %v", err)
	}
}

func replaceIn(f map[string][]byte, name, old, new string) {
	if !strings.Contains(string(f[name]), old) {
		panic("fixture text not found: " + old)
	}
	f[name] = []byte(strings.Replace(string(f[name]), old, new, 1))
}

// waiting adds parts the core doesn't accept yet, and a pending list naming them.
func waiting(f map[string][]byte) {
	replaceIn(f, modulekit.ManifestFile, "tabs:\n", "notifications:\n  - kind: hours_low\n    label: Hours below target\ntabs:\n")
	replaceIn(f, modulekit.ManifestFile, "permissions:\n", "permissions:\n  - scope: books:read\n    reason: Read the books.\n")
	f["migrations/0002_trust_link.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN trust_account_id TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT;\n")
	f[modulekit.PendingFile] = []byte("pending:\n" +
		"  - field: notifications\n    core: CORE-03\n    reason: Tell the attorney when a matter falls behind.\n" +
		"  - scope: books:read\n    core: CORE-02\n    reason: Read the books without a viewer.\n" +
		"  - scope: time:read\n    core: CORE-00\n    reason: Accepted already.\n" +
		"  - migration: migrations/0002_trust_link.sql\n    core: CORE-22\n    reason: Link a target to its trust account.\n")
}

func TestCheckReportsEveryRefusal(t *testing.T) {
	dir := copyPackage(t, waiting)
	code, _, errs := cli("check", dir)
	lines := strings.Split(strings.TrimSpace(errs), "\n")
	if code != exitRefused || len(lines) != 4 {
		t.Fatalf("check: %d\n%s", code, errs)
	}
	for i, want := range []string{
		"togavsu-module check: pending.yaml: unknown_file: ",
		"togavsu-module check: togavsu-module.yaml notifications: unknown_field: ",
		"togavsu-module check: togavsu-module.yaml permissions[0]: unknown_scope: ",
		"togavsu-module check: migrations/0002_trust_link.sql statement 1: core_reference: ",
	} {
		if !strings.HasPrefix(lines[i], want) {
			t.Fatalf("line %d: %s", i, lines[i])
		}
	}
	code, out, _ := cli("check", "-json", dir)
	var res checkResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != exitRefused || res.OK || len(res.Refusals) != 4 || res.Refusals[3].Value != "core.trust_accounts" {
		t.Fatalf("check -json: %d %s %v", code, out, err)
	}
	code, out, errs = cli("check", "-installable", "-json", dir)
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != exitOK || !res.OK || res.ID != "matter-clock" || res.Migrations != 1 {
		t.Fatalf("check -installable -json: %d %s %s", code, out, errs)
	}
}

func TestPackageInstallable(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := write(t, "signing.key", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	dir := copyPackage(t, waiting)
	out := filepath.Join(t.TempDir(), "matter-clock.signed.json")
	// The full package is refused, every refusal on its own line, and nothing is written.
	if code, _, errs := cli("package", "-dir", dir, "-key", seed, "-out", out); code != exitRefused || !strings.Contains(errs, "pending_unbuilt") {
		t.Fatalf("full package: %d %s", code, errs)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a refused package was written")
	}
	code, stdout, errs := cli("package", "-installable", "-dir", dir, "-key", seed, "-out", out)
	if code != exitOK || !strings.Contains(stdout, modulekit.KeyID(pub)) {
		t.Fatalf("package -installable: %d %s %s", code, stdout, errs)
	}
	lines := strings.Split(strings.TrimSpace(errs), "\n")
	want := []string{
		"held back togavsu-module.yaml notifications: waits on CORE-03: Tell the attorney when a matter falls behind.",
		"held back togavsu-module.yaml permissions[0] (scope books:read): waits on CORE-02: Read the books without a viewer.",
		"held back migrations/0002_trust_link.sql: waits on CORE-22: Link a target to its trust account.",
		"pending scope time:read (CORE-00) holds nothing back: the core accepts it or the package no longer has it; remove the entry",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stderr:\n%s", errs)
	}
	pemFile := write(t, "signing.pub", modulekit.PublicKeyPEM(pub))
	if code, stdout, errs := cli("verify", "-file", out, "-pub", pemFile); code != exitOK || !strings.Contains(stdout, "1 migration(s)") {
		t.Fatalf("verify: %d %s %s", code, stdout, errs)
	}
	// A refusal the pending list doesn't cover stops the build.
	uncovered := copyPackage(t, func(f map[string][]byte) {
		waiting(f)
		replaceIn(f, modulekit.ManifestFile, "  events: /events\n", "  events: /events\n  system_mail: true\n")
	})
	skipped := filepath.Join(t.TempDir(), "skipped.json")
	if code, _, errs := cli("package", "-installable", "-dir", uncovered, "-key", seed, "-out", skipped); code != exitRefused || !strings.Contains(errs, "container.system_mail: unknown_field") {
		t.Fatalf("uncovered: %d %s", code, errs)
	}
	if _, err := os.Stat(skipped); !os.IsNotExist(err) {
		t.Fatal("an uncovered package was written")
	}
}

func TestAccepts(t *testing.T) {
	code, out, _ := cli("accepts")
	var a modulekit.Accepted
	if err := json.Unmarshal([]byte(out), &a); err != nil || code != exitOK || len(a.Scopes) == 0 || a.ManifestSchema != modulekit.Accepts().ManifestSchema {
		t.Fatalf("accepts: %d %s %v", code, out, err)
	}
	if code, _, _ := cli("accepts", "extra"); code != exitUsage {
		t.Fatalf("accepts with an argument: %d", code)
	}
}

func checkJSON(t *testing.T, args ...string) (int, checkResult) {
	t.Helper()
	code, out, errs := cli(append([]string{"check", "-json"}, args...)...)
	var res checkResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("check -json %v: stdout %q is not the result (%v); stderr %s", args, out, err, errs)
	}
	return code, res
}

// Every failure under -json, Installable's included, is the documented result.
func TestCheckJSONReportsInstallableRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(map[string][]byte)
		code string
	}{
		"uncovered field": {func(f map[string][]byte) {
			waiting(f)
			replaceIn(f, modulekit.ManifestFile, "  events: /events\n", "  events: /events\n  system_mail: true\n")
		}, modulekit.RefuseUnknownField},
		"invalid pending list": {func(f map[string][]byte) {
			waiting(f)
			f[modulekit.PendingFile] = []byte("pending:\n  - field: notifications\n    reason: no core item\n")
		}, modulekit.RefusePendingInvalid},
		"held migration out of order": {func(f map[string][]byte) {
			waiting(f)
			f["migrations/0003_index.sql"] = []byte("CREATE INDEX matter_targets_note ON matter_targets (note);\n")
		}, modulekit.RefusePendingOrder},
	} {
		code, res := checkJSON(t, "-installable", copyPackage(t, tc.edit))
		if code != exitRefused || res.OK || len(res.Refusals) == 0 || res.Refusals[0].Code != tc.code {
			t.Fatalf("%s: %d %+v", name, code, res)
		}
	}
	code, res := checkJSON(t, "-installable", filepath.Join(t.TempDir(), "missing"))
	if code != exitRefused || res.OK || len(res.Refusals) != 1 || res.Refusals[0].Code != modulekit.RefuseUnreadable {
		t.Fatalf("missing directory: %d %+v", code, res)
	}
}

// The update baseline is what the previous release shipped, not its source run through
// Installable again.
func TestCheckUpdateAgainstTheShippedPackage(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := write(t, "signing.key", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	pemFile := write(t, "signing.pub", modulekit.PublicKeyPEM(pub))
	v1 := copyPackage(t, waiting)
	shipped := filepath.Join(t.TempDir(), "v1.signed.json")
	if code, _, errs := cli("package", "-installable", "-dir", v1, "-key", seed, "-out", shipped); code != exitOK {
		t.Fatalf("v1: %d %s", code, errs)
	}
	bump := func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, `version: "1.0.0"`, `version: "1.1.0"`)
	}
	// v1 held 0002 back, so v2 may still change it while it waits.
	v2 := copyPackage(t, func(f map[string][]byte) {
		waiting(f)
		bump(f)
		f["migrations/0002_trust_link.sql"] = []byte("ALTER TABLE matter_targets ADD COLUMN trust_account TEXT REFERENCES core.trust_accounts (id) ON DELETE RESTRICT;\n")
	})
	if code, out, errs := cli("check", "-installable", "-previous-file", shipped, "-previous-pub", pemFile, v2); code != exitOK || !strings.Contains(out, "ok: matter-clock 1.1.0, 1 migration(s)") {
		t.Fatalf("v2 against what v1 shipped: %d %s %s", code, out, errs)
	}
	// A source directory whose build held parts back is not a baseline.
	code, res := checkJSON(t, "-installable", "-previous", v1, v2)
	if code != exitRefused || len(res.Refusals) != 1 || res.Refusals[0].Code != modulekit.RefuseUpgradeBaseline {
		t.Fatalf("pending source as the baseline: %d %+v", code, res)
	}
	// A migration v1 did ship can't change.
	v3 := copyPackage(t, func(f map[string][]byte) {
		waiting(f)
		bump(f)
		f["migrations/0001_matter_targets.sql"] = append(f["migrations/0001_matter_targets.sql"], []byte("\n-- changed after it ran\n")...)
	})
	code, res = checkJSON(t, "-installable", "-previous-file", shipped, "-previous-pub", pemFile, v3)
	if code != exitRefused || len(res.Refusals) != 1 || res.Refusals[0].Code != modulekit.RefuseUpgrade || res.Refusals[0].APICode != modulekit.CodeMigration {
		t.Fatalf("changed shipped migration: %d %+v", code, res)
	}
	// The baseline must verify with the named key.
	other, _, _ := ed25519.GenerateKey(nil)
	otherFile := write(t, "other.pub", modulekit.PublicKeyPEM(other))
	if code, _, _ := cli("check", "-installable", "-previous-file", shipped, "-previous-pub", otherFile, v2); code != exitRefused {
		t.Fatalf("baseline signed by another key: %d", code)
	}
	if code, _, _ := cli("check", "-previous-file", shipped, v2); code != exitUsage {
		t.Fatalf("-previous-file without its key: %d", code)
	}
}

// -strict turns a pending entry that holds nothing back into a refusal, in the text and
// the -json result.
func TestStrictRefusesUnusedEntries(t *testing.T) {
	dir := copyPackage(t, waiting)
	code, _, errs := cli("check", "-installable", "-strict", dir)
	if code != exitRefused || !strings.Contains(errs, "pending.yaml: pending_unused: pending scope time:read (CORE-00) holds nothing back") {
		t.Fatalf("check -strict: %d %s", code, errs)
	}
	code, res := checkJSON(t, "-installable", "-strict", dir)
	if code != exitRefused || res.OK || len(res.Refusals) != 1 || res.Refusals[0].Code != modulekit.RefusePendingUnused ||
		len(res.Unused) != 1 || res.Unused[0].Scope != "time:read" || len(res.Held) != 3 {
		t.Fatalf("check -strict -json: %d %+v", code, res)
	}
	// Without -strict the entry is reported but the check passes, and -json lists it.
	code, res = checkJSON(t, "-installable", dir)
	if code != exitOK || !res.OK || len(res.Unused) != 1 || len(res.Held) != 3 {
		t.Fatalf("check -json: %d %+v", code, res)
	}
	used := copyPackage(t, func(f map[string][]byte) {
		waiting(f)
		replaceIn(f, modulekit.PendingFile, "  - scope: time:read\n    core: CORE-00\n    reason: Accepted already.\n", "")
	})
	if code, _, errs := cli("check", "-installable", "-strict", used); code != exitOK {
		t.Fatalf("check -strict with every entry used: %d %s", code, errs)
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := write(t, "signing.key", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	out := filepath.Join(t.TempDir(), "strict.json")
	if code, _, errs := cli("package", "-installable", "-strict", "-dir", dir, "-key", seed, "-out", out); code != exitRefused || !strings.Contains(errs, "pending_unused") {
		t.Fatalf("package -strict: %d %s", code, errs)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a strict refusal was written")
	}
	if code, _, _ := cli("check", "-strict", dir); code != exitUsage {
		t.Fatalf("-strict without -installable: %d", code)
	}
}

// package -unsigned writes the files that would ship, for inspection, and signs nothing.
func TestPackageUnsigned(t *testing.T) {
	dir := copyPackage(t, waiting)
	out := filepath.Join(t.TempDir(), "installable")
	code, stdout, errs := cli("package", "-installable", "-unsigned", "-dir", dir, "-out", out)
	if code != exitOK || !strings.Contains(stdout, "wrote 2 file(s)") {
		t.Fatalf("package -unsigned: %d %s %s", code, stdout, errs)
	}
	got, err := modulekit.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	files, err := modulekit.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ip.Files) || string(got[modulekit.ManifestFile]) != string(ip.Files[modulekit.ManifestFile]) {
		t.Fatalf("written files: %v", got)
	}
	if _, ok := got[modulekit.PendingFile]; ok {
		t.Fatal("the pending list was written")
	}
	if code, _, errs := cli("package", "-installable", "-unsigned", "-dir", dir, "-out", out); code != exitRefused || !strings.Contains(errs, "not empty") {
		t.Fatalf("into a full directory: %d %s", code, errs)
	}
	if code, _, _ := cli("package", "-unsigned", "-dir", dir, "-key", "k", "-out", out); code != exitUsage {
		t.Fatalf("-unsigned with -key: %d", code)
	}
	// Without -installable the package with its pending list is refused, not written.
	plain := filepath.Join(t.TempDir(), "plain")
	if code, _, errs := cli("package", "-unsigned", "-dir", dir, "-out", plain); code != exitRefused || !strings.Contains(errs, "unknown_file") {
		t.Fatalf("plain -unsigned: %d %s", code, errs)
	}
}

// A package directory's other files, such as the files.go that embeds it, never ship.
func TestPackageShipsOnlyThePackageFiles(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	seed := write(t, "signing.key", base64.StdEncoding.EncodeToString(priv.Seed())+"\n")
	dir := copyPackage(t, func(f map[string][]byte) {
		f["files.go"] = []byte("package pkgfiles\n\nimport \"embed\"\n\n//go:embed togavsu-module.yaml migrations\nvar FS embed.FS\n")
		f["README.md"] = []byte("# notes\n")
	})
	out := filepath.Join(t.TempDir(), "signed.json")
	if code, stdout, errs := cli("package", "-dir", dir, "-key", seed, "-out", out); code != exitOK {
		t.Fatalf("package: %d %s %s", code, stdout, errs)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := modulekit.VerifyFile(raw, []modulekit.TrustedKey{modulekit.Trust(pub)})
	if err != nil {
		t.Fatal(err)
	}
	_, shipped, err := modulekit.ReadSignedFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := modulekit.ReadPackage(shipped)
	if err != nil || v.ModuleID != "matter-clock" {
		t.Fatal(err)
	}
	for name := range pkg.Files {
		if name != modulekit.ManifestFile && !strings.HasPrefix(name, "migrations/") {
			t.Fatalf("%s shipped", name)
		}
	}
}

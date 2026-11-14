package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
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

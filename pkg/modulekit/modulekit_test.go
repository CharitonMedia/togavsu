package modulekit_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

const sampleDir = "../../examples/matter-clock/package"

func sample(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := modulekit.ReadDir(sampleDir)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func edit(files map[string][]byte, f func(map[string][]byte)) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files {
		out[k] = append([]byte(nil), v...)
	}
	f(out)
	return out
}

func TestParseManifest(t *testing.T) {
	files := sample(t)
	m, err := modulekit.ParseManifest(files[modulekit.ManifestFile])
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "matter-clock" || m.Version != "1.0.0" || m.Schema() != "mod_matter_clock" {
		t.Fatalf("manifest: %+v", m)
	}
	bad := bytes.Replace(files[modulekit.ManifestFile], []byte("id: matter-clock"), []byte("id: Matter Clock"), 1)
	if _, err := modulekit.ParseManifest(bad); modulekit.Code(err) != "validation" {
		t.Fatalf("bad id: %v (%q)", err, modulekit.Code(err))
	}
}

func TestCheckRunsTheInstallChecks(t *testing.T) {
	files := sample(t)
	pkg, ms, err := modulekit.Check(files)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.ID != "matter-clock" || len(ms) != 1 || ms[0].Checksum == "" {
		t.Fatalf("checked: %s %d", pkg.Manifest.ID, len(ms))
	}
	for name, sql := range map[string]string{
		"contacts table": "CREATE TABLE contacts (id TEXT PRIMARY KEY);",
		"outside schema": "CREATE TABLE core.module_rows (id TEXT PRIMARY KEY);",
		"trigger":        "CREATE FUNCTION f() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$; CREATE TRIGGER t BEFORE INSERT ON matter_targets FOR EACH ROW EXECUTE FUNCTION f();",
	} {
		bad := edit(files, func(f map[string][]byte) { f["migrations/0002_bad.sql"] = []byte(sql) })
		if _, _, err := modulekit.Check(bad); modulekit.Code(err) != modulekit.CodeMigration {
			t.Fatalf("%s: %v", name, err)
		}
	}
	misnamed := edit(files, func(f map[string][]byte) { f["migrations/bad.sql"] = []byte("SELECT 1;") })
	if _, _, err := modulekit.Check(misnamed); modulekit.Code(err) != modulekit.CodeMigration {
		t.Fatalf("misnamed migration: %v", err)
	}
	// A declared extension whose table no migration makes is refused by the cross-check.
	noTable := edit(files, func(f map[string][]byte) { delete(f, "migrations/0001_matter_targets.sql") })
	if _, _, err := modulekit.Check(noTable); err == nil {
		t.Fatal("an extension without its table passed")
	}
	if _, _, err := modulekit.CheckPackage([]byte("not a package")); err == nil {
		t.Fatal("garbage passed")
	}
}

func TestCheckUpgradeChecksums(t *testing.T) {
	files := sample(t)
	prev, err := modulekit.Archive(files)
	if err != nil {
		t.Fatal(err)
	}
	bump := func(f map[string][]byte) {
		f[modulekit.ManifestFile] = bytes.Replace(f[modulekit.ManifestFile], []byte(`version: "1.0.0"`), []byte(`version: "1.1.0"`), 1)
	}
	added, _ := modulekit.Archive(edit(files, func(f map[string][]byte) {
		bump(f)
		f["migrations/0002_note_index.sql"] = []byte("CREATE INDEX matter_targets_note ON matter_targets (note);")
	}))
	if err := modulekit.CheckUpgrade(prev, added); err != nil {
		t.Fatalf("a new migration: %v", err)
	}
	changed, _ := modulekit.Archive(edit(files, func(f map[string][]byte) {
		bump(f)
		f["migrations/0001_matter_targets.sql"] = append(f["migrations/0001_matter_targets.sql"], []byte("\n-- changed after it ran\n")...)
	}))
	if err := modulekit.CheckUpgrade(prev, changed); modulekit.Code(err) != modulekit.CodeMigration {
		t.Fatalf("a changed migration: %v", err)
	}
	_, ms, err := modulekit.CheckPackage(prev)
	if err != nil {
		t.Fatal(err)
	}
	applied := modulekit.Applied(ms)
	applied[7] = "a migration this package dropped"
	if err := modulekit.CheckChecksums(applied, ms); modulekit.Code(err) != modulekit.CodeMigration || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a dropped migration: %v", err)
	}
}

func TestFilesSkipLikeEmbed(t *testing.T) {
	files, err := modulekit.Files(fstest.MapFS{
		"togavsu-module.yaml":      {Data: []byte("id: x")},
		"migrations/0001_a.sql":    {Data: []byte("SELECT 1;")},
		".DS_Store":                {Data: []byte("junk")},
		"_notes/draft.sql":         {Data: []byte("junk")},
		"migrations/.0002_b.sql~":  {Data: []byte("junk")},
		"migrations/_0003_old.sql": {Data: []byte("junk")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["migrations/0001_a.sql"] == nil || files[modulekit.ManifestFile] == nil {
		t.Fatalf("files: %v", files)
	}
}

// The kit builds byte for byte what the sample module embeds and signs.
func TestBuildMatchesMatterClock(t *testing.T) {
	_, priv := key(t)
	fromDisk, err := modulekit.Build(priv, sample(t))
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := matterclock.Build(priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromDisk, embedded) {
		t.Fatal("the kit's package of examples/matter-clock/package differs from matterclock.Build")
	}
}

func TestSignAndVerifyFile(t *testing.T) {
	pub, priv := key(t)
	signed, err := modulekit.Build(priv, sample(t))
	if err != nil {
		t.Fatal(err)
	}
	var sp modulekit.SignedPackage
	if err := json.Unmarshal(signed, &sp); err != nil {
		t.Fatal(err)
	}
	pkg, _ := base64.StdEncoding.DecodeString(sp.PackageBase64)
	if sp.ListingID != "matter-clock" || sp.Version != "1.0.0" || sp.KeyID != modulekit.KeyID(pub) || sp.Digest != modulekit.Digest(pkg) {
		t.Fatalf("signed file: %+v", sp)
	}
	if _, err := modulekit.Verify(pkg, sp.Digest, sp.Signature, sp.KeyID, []modulekit.TrustedKey{modulekit.Trust(pub)}); err != nil {
		t.Fatal(err)
	}
	trusted := []modulekit.TrustedKey{modulekit.Trust(pub)}
	v, ms, err := modulekit.VerifyFile(signed, trusted)
	if err != nil || v.ModuleID != "matter-clock" || v.Signer.KeyID != modulekit.KeyID(pub) || len(ms) != 1 {
		t.Fatalf("verify: %+v %v", v, err)
	}

	other, _ := key(t)
	if _, _, err := modulekit.VerifyFile(signed, []modulekit.TrustedKey{modulekit.Trust(other)}); modulekit.Code(err) != modulekit.CodeUnsigned {
		t.Fatalf("untrusted key: %v", err)
	}
	if _, err := modulekit.Verify(pkg, "", sp.Signature, sp.KeyID, nil); !errors.Is(err, modulekit.ErrUntrustedKey) {
		t.Fatalf("no trusted keys: %v", err)
	}
	// Tampered bytes with the digest left as signed, and with the digest removed.
	body := append([]byte(nil), pkg...)
	body[len(body)/2] ^= 0xff
	sp.PackageBase64 = base64.StdEncoding.EncodeToString(body)
	tampered, _ := json.Marshal(sp)
	if _, _, err := modulekit.VerifyFile(tampered, trusted); modulekit.Code(err) != modulekit.CodeTampered {
		t.Fatalf("tampered: %v", err)
	}
	sp.Digest = ""
	tampered, _ = json.Marshal(sp)
	if _, _, err := modulekit.VerifyFile(tampered, trusted); modulekit.Code(err) != modulekit.CodeTampered {
		t.Fatalf("tampered without digest: %v", err)
	}
	if _, err := modulekit.Verify(body, "", sp.Signature, sp.KeyID, trusted); !errors.Is(err, modulekit.ErrSignatureInvalid) {
		t.Fatalf("bad signature: %v", err)
	}
	// A bare archive is not a signed package file.
	if _, _, err := modulekit.VerifyFile(pkg, trusted); err == nil {
		t.Fatal("an unsigned archive verified")
	}
	// A signed package whose migrations the core refuses fails verification too.
	refused, err := modulekit.Build(priv, edit(sample(t), func(f map[string][]byte) {
		f["migrations/0002_bad.sql"] = []byte("CREATE TABLE contacts (id TEXT PRIMARY KEY);")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := modulekit.VerifyFile(refused, trusted); modulekit.Code(err) != modulekit.CodeMigration {
		t.Fatalf("refused migration: %v", err)
	}
}

func TestKeys(t *testing.T) {
	pub, priv := key(t)
	back, err := modulekit.ParsePublicKeyPEM(modulekit.PublicKeyPEM(pub))
	if err != nil || !back.Equal(pub) {
		t.Fatalf("PEM round trip: %v", err)
	}
	if _, err := modulekit.ParsePublicKeyPEM("not a key"); !errors.Is(err, modulekit.ErrBadKey) {
		t.Fatalf("bad PEM: %v", err)
	}
	seed := base64.StdEncoding.EncodeToString(priv.Seed())
	got, err := modulekit.ParseSeed(seed + "\n")
	if err != nil || !got.Equal(priv) {
		t.Fatalf("seed: %v", err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := modulekit.ParseSeed(bad); !errors.Is(err, modulekit.ErrBadSeed) {
			t.Fatalf("seed %q: %v", bad, err)
		}
	}
	if !strings.HasPrefix(modulekit.KeyID(pub), "ed25519-") {
		t.Fatal(modulekit.KeyID(pub))
	}
}

func TestBuildPackageAndReadManifest(t *testing.T) {
	files := sample(t)
	tarball, err := modulekit.BuildPackage(files[modulekit.ManifestFile], map[string][]byte{"migrations/0001_matter_targets.sql": files["migrations/0001_matter_targets.sql"]})
	if err != nil {
		t.Fatal(err)
	}
	m, err := modulekit.ReadManifest(tarball)
	if err != nil || m.ID != "matter-clock" || len(m.Permissions) != 3 {
		t.Fatalf("listing manifest: %+v %v", m, err)
	}
	gz, _ := modulekit.Archive(files)
	if m2, err := modulekit.ReadManifest(gz); err != nil || m2.Version != "1.0.0" {
		t.Fatalf("gzip manifest: %+v %v", m2, err)
	}
	// The core installs only gzip packages.
	if _, err := modulekit.ReadPackage(tarball); err == nil {
		t.Fatal("a plain tar read as an installable package")
	}
}

func TestCheckSource(t *testing.T) {
	dir := t.TempDir()
	write := func(src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "m.go"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("package m\n\nfunc f() int { return 1 + 1 }\n")
	if err := modulekit.CheckSource(dir); err != nil {
		t.Fatalf("clean source refused: %v", err)
	}
	write("package m\n\nimport \"os\"\n\nfunc f() string { return os.Getenv(\"X\") }\n")
	if err := modulekit.CheckSource(dir); err == nil {
		t.Fatal("an environment read passed")
	}
}

// TestCoreModuleZipKeepsEmbeds guards the kit for module repositories: Go module zips
// leave out every directory named vendor and every nested module, so a go:embed that
// reaches into one compiles here but not for a repository that requires the core.
func TestCoreModuleZipKeepsEmbeds(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == root {
			return nil
		}
		if d.Name() == ".git" || d.Name() == "node_modules" {
			return filepath.SkipDir
		}
		if d.Name() == "vendor" {
			t.Errorf("%s: a directory named vendor is left out of the core's module zip", path)
		}
		if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
			t.Errorf("%s: a nested module is left out of the core's module zip", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

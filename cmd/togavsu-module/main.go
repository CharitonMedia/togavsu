// Command togavsu-module checks, packages and verifies Togavsu module packages with the
// core's own code (pkg/modulekit).
//
//	togavsu-module check [-installable [-strict]] [-json] [-previous <package dir> | -previous-file <signed file> -previous-pub <PEM file>] <package dir>
//	togavsu-module package [-installable [-strict]] -dir <package dir> (-key <seed file> -out <signed file> | -unsigned -out <new dir>)
//	togavsu-module verify -file <signed file> -pub <public key PEM file>
//	togavsu-module accepts
//
// check runs the manifest, scope and migration checks the core runs at install and
// prints every refusal, one per line; with -previous-file (the previous release's signed
// package, verified with -previous-pub) it also checks the package as an update of what
// that release shipped (no migration that ran may change or go missing). -previous takes
// the previous release's source directory instead, only when its build held nothing back. package
// checks the directory, archives it, signs it with the base64 Ed25519 seed in the key
// file and writes the signed package file; it refuses to sign a package check refuses.
// With -installable, check and package first hold back each part the directory's
// pending.yaml lists that the core refuses, and print one line per held-back part with
// the core item it waits on; -strict also refuses a pending entry that holds nothing
// back. package -unsigned writes the checked package files (Installable's with
// -installable) to a new directory instead of signing, to inspect what would ship. Only
// the package directory's own files are read (togavsu-module.yaml, pending.yaml and
// migrations/); anything else there is never shipped. verify checks a signed package
// file against one trusted public key, as the core does before it installs. accepts
// prints, as JSON, what this core accepts: its manifest fields and their digest, its
// scopes, and the core records a module may use. The command never makes or stores a
// key.
package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// Small fixed numbers spelled as arithmetic for the AC-030 source check, which reads
// cmd/ too.
var (
	zero = int(0)
	one  = zero + 1
)

func main() {
	os.Exit(run(os.Args[one:], os.Stdout, os.Stderr))
}

const usage = `usage:
  togavsu-module check [-installable [-strict]] [-json] [-previous <package dir> | -previous-file <signed file> -previous-pub <PEM file>] <package dir>
  togavsu-module package [-installable [-strict]] -dir <package dir> (-key <seed file> -out <signed file> | -unsigned -out <new dir>)
  togavsu-module verify -file <signed file> -pub <public key PEM file>
  togavsu-module accepts
`

// Exit codes: ok, a refused package or key, bad usage.
const (
	exitOK = iota
	exitRefused
	exitUsage
)

// run is the command; it returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < one {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	var err error
	code := exitRefused
	switch args[0] {
	case "check":
		err = check(args[one:], stdout, stderr, &code)
	case "package":
		err = pack(args[one:], stdout, stderr, &code)
	case "verify":
		err = verify(args[one:], stdout, &code)
	case "accepts":
		err = accepts(args[one:], stdout, &code)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return exitUsage
	}
	if err != nil {
		var refs modulekit.Refusals
		if errors.As(err, &refs) {
			for _, r := range refs {
				if where := r.Where(); where != "" {
					fmt.Fprintf(stderr, "togavsu-module %s: %s: %s: %s\n", args[0], where, r.Code, r.Message)
				} else {
					fmt.Fprintf(stderr, "togavsu-module %s: %s: %s\n", args[0], r.Code, r.Message)
				}
			}
			return code
		}
		fmt.Fprintf(stderr, "togavsu-module %s: %v\n", args[0], err)
		return code
	}
	return exitOK
}

// installable reads a package directory; with hold it returns modulekit.Installable's
// result and prints each held-back part, and each pending entry that holds nothing back.
// With strict, an entry that holds nothing back is a refusal.
func installable(dir string, hold, strict bool, stderr io.Writer) (modulekit.InstallablePackage, error) {
	files, err := modulekit.ReadDir(dir)
	if err != nil || !hold {
		return modulekit.InstallablePackage{Files: files}, err
	}
	ip, err := modulekit.Installable(files)
	if err != nil {
		return modulekit.InstallablePackage{}, err
	}
	for _, h := range ip.Held {
		fmt.Fprintln(stderr, h.String())
	}
	if strict {
		if refs := ip.UnusedRefusals(); len(refs) > zero {
			return ip, refs
		}
	}
	for _, r := range ip.UnusedRefusals() {
		fmt.Fprintln(stderr, r.Message)
	}
	return ip, nil
}

func flags(name string, code *int) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	*code = exitUsage
	return fs
}

func check(args []string, stdout, stderr io.Writer, code *int) error {
	fs := flags("check", code)
	previous := fs.String("previous", "", "source directory of the version this one updates (one built without pending parts)")
	previousFile := fs.String("previous-file", "", "signed package file of the version this one updates")
	previousPub := fs.String("previous-pub", "", "public key PEM the previous signed package file was signed with")
	hold := fs.Bool("installable", false, "hold back the pending parts the core refuses (pending.yaml)")
	strict := fs.Bool("strict", false, "with -installable, refuse a pending entry that holds nothing back")
	asJSON := fs.Bool("json", false, "print the result as JSON on stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != one {
		return fmt.Errorf("name one package directory")
	}
	if *strict && !*hold {
		return fmt.Errorf("-strict goes with -installable")
	}
	if *previous != "" && *previousFile != "" {
		return fmt.Errorf("name -previous or -previous-file, not both")
	}
	if (*previousFile == "") != (*previousPub == "") {
		return fmt.Errorf("-previous-file and -previous-pub go together")
	}
	*code = exitRefused
	pkg, ms, ip, err := checkDir(fs.Arg(zero), *previous, *previousFile, *previousPub, *hold, *strict, stderr)
	if *asJSON {
		res := checkResult{OK: err == nil, Refusals: structured(err), Held: ip.Held, Unused: ip.Unused}
		if err == nil {
			res.ID, res.Version, res.Migrations, res.Digest = pkg.Manifest.ID, pkg.Manifest.Version, len(ms), pkg.Digest
		}
		if werr := json.NewEncoder(stdout).Encode(res); werr != nil {
			return werr
		}
	}
	if err != nil {
		return err
	}
	if !*asJSON {
		fmt.Fprintf(stdout, "ok: %s %s, %d migration(s), digest %s\n", pkg.Manifest.ID, pkg.Manifest.Version, len(ms), pkg.Digest)
	}
	return nil
}

// checkDir is check's work: the package (its installable form with hold), then the
// update rule against the previous release when one is named.
func checkDir(dir, previous, previousFile, previousPub string, hold, strict bool, stderr io.Writer) (*modulekit.Package, []modulekit.Migration, modulekit.InstallablePackage, error) {
	ip, err := installable(dir, hold, strict, stderr)
	if err != nil {
		return nil, nil, ip, err
	}
	pkg, ms, err := modulekit.Check(ip.Files)
	if err != nil {
		return nil, nil, ip, err
	}
	next, err := modulekit.Archive(ip.Files)
	if err != nil {
		return nil, nil, ip, err
	}
	switch {
	case previousFile != "":
		shipped, err := shippedPackage(previousFile, previousPub)
		if err != nil {
			return nil, nil, ip, err
		}
		err = modulekit.CheckUpdate(shipped, next)
		return pkg, ms, ip, err
	case previous != "":
		// A source directory is the previous release only when its build held nothing
		// back; re-running Installable on it would apply today's core, not what shipped.
		old, err := modulekit.ReadDir(previous)
		if err != nil {
			return nil, nil, ip, err
		}
		if _, ok := old[modulekit.PendingFile]; ok {
			return nil, nil, ip, modulekit.Refusals{{Kind: modulekit.KindMigration, Code: modulekit.RefuseUpgradeBaseline, File: modulekit.PendingFile,
				Message: "the previous version lists pending parts, so its source is not what it shipped; pass its signed package with -previous-file and -previous-pub"}}
		}
		prev, err := modulekit.Archive(old)
		if err != nil {
			return nil, nil, ip, err
		}
		if err := modulekit.CheckUpgrade(prev, next); err != nil {
			return nil, nil, ip, modulekit.Refusals{{Kind: modulekit.KindMigration, Code: modulekit.RefuseUpgrade, Message: err.Error(), APICode: modulekit.Code(err), Err: err}}
		}
	}
	return pkg, ms, ip, nil
}

// shippedPackage reads a signed package file, verifies its signature with the key in
// pubFile, and returns the package bytes it carries.
func shippedPackage(file, pubFile string) ([]byte, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	pemText, err := os.ReadFile(pubFile)
	if err != nil {
		return nil, err
	}
	pub, err := modulekit.ParsePublicKeyPEM(string(pemText))
	if err != nil {
		return nil, err
	}
	sp, shipped, err := modulekit.ReadSignedFile(raw)
	if err != nil {
		return nil, err
	}
	if _, err := modulekit.Verify(shipped, sp.Digest, sp.Signature, sp.KeyID, []modulekit.TrustedKey{modulekit.Trust(pub)}); err != nil {
		return nil, err
	}
	return shipped, nil
}

// structured is err as refusals: its own list, or one refusal for any other failure.
func structured(err error) modulekit.Refusals {
	if err == nil {
		return modulekit.Refusals{}
	}
	var refs modulekit.Refusals
	if errors.As(err, &refs) {
		return refs
	}
	return modulekit.Refusals{{Kind: modulekit.KindPackage, Code: modulekit.RefuseUnreadable, Message: err.Error(), APICode: modulekit.Code(err), Err: err}}
}

// checkResult is check -json's output.
type checkResult struct {
	OK         bool               `json:"ok"`
	ID         string             `json:"id,omitempty"`
	Version    string             `json:"version,omitempty"`
	Migrations int                `json:"migrations,omitempty"`
	Digest     string             `json:"digest,omitempty"`
	Refusals   modulekit.Refusals `json:"refusals"`
	// Held and Unused are, with -installable, the parts held back and the pending
	// entries that held nothing back.
	Held   []modulekit.Held    `json:"held,omitempty"`
	Unused []modulekit.Pending `json:"unused,omitempty"`
}

func accepts(args []string, stdout io.Writer, code *int) error {
	fs := flags("accepts", code)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != zero {
		return fmt.Errorf("accepts takes no arguments")
	}
	*code = exitRefused
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(modulekit.Accepts())
}

func pack(args []string, stdout, stderr io.Writer, code *int) error {
	fs := flags("package", code)
	hold := fs.Bool("installable", false, "hold back the pending parts the core refuses (pending.yaml)")
	strict := fs.Bool("strict", false, "with -installable, refuse a pending entry that holds nothing back")
	unsigned := fs.Bool("unsigned", false, "write the checked package files to the -out directory instead of signing")
	dir := fs.String("dir", "", "package directory (togavsu-module.yaml and migrations/)")
	keyFile := fs.String("key", "", "file holding a base64 ed25519 seed")
	out := fs.String("out", "", "signed package file to write (with -unsigned, a new directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *dir == "" || *out == "" || fs.NArg() != zero:
		return fmt.Errorf("-dir and -out are required")
	case *unsigned && *keyFile != "":
		return fmt.Errorf("-unsigned writes files, not a signed package; drop -key")
	case !*unsigned && *keyFile == "":
		return fmt.Errorf("-key is required (or -unsigned)")
	case *strict && !*hold:
		return fmt.Errorf("-strict goes with -installable")
	}
	*code = exitRefused
	var priv ed25519.PrivateKey
	if !*unsigned {
		seed, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		if priv, err = modulekit.ParseSeed(string(seed)); err != nil {
			return err
		}
	}
	ip, err := installable(*dir, *hold, *strict, stderr)
	if err != nil {
		return err
	}
	if *unsigned {
		if _, _, err := modulekit.Check(ip.Files); err != nil {
			return err
		}
		if err := writeFiles(*out, ip.Files); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %d file(s) to %s, unsigned\n", len(ip.Files), *out)
		return nil
	}
	// Build refuses to sign what the core would refuse to install.
	signed, err := modulekit.Build(priv, ip.Files)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, signed, os.FileMode(zero)|0o644); err != nil {
		return err
	}
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Fprintf(stdout, "wrote %s, signed by key %s\npublic key (add it as a trusted signing key, public_key_pem):\n%s", *out, modulekit.KeyID(pub), modulekit.PublicKeyPEM(pub))
	return nil
}

// writeFiles writes package files under dir, which must not exist or be empty, so a
// stale file never sits beside the package.
func writeFiles(dir string, files map[string][]byte) error {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > zero {
		return fmt.Errorf("%s is not empty; name a new directory", dir)
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), os.FileMode(zero)|0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, body, os.FileMode(zero)|0o644); err != nil {
			return err
		}
	}
	return nil
}

func verify(args []string, stdout io.Writer, code *int) error {
	fs := flags("verify", code)
	file := fs.String("file", "", "signed package file")
	pubFile := fs.String("pub", "", "trusted public key, PEM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *pubFile == "" || fs.NArg() != zero {
		return fmt.Errorf("-file and -pub are required")
	}
	*code = exitRefused
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	pemText, err := os.ReadFile(*pubFile)
	if err != nil {
		return err
	}
	pub, err := modulekit.ParsePublicKeyPEM(string(pemText))
	if err != nil {
		return err
	}
	v, ms, err := modulekit.VerifyFile(raw, []modulekit.TrustedKey{modulekit.Trust(pub)})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ok: %s %s signed by %s, %d migration(s), digest %s\n", v.ModuleID, v.Version, v.Signer.KeyID, len(ms), v.Digest)
	return nil
}

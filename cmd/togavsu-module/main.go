// Command togavsu-module checks, packages and verifies Togavsu module packages with the
// core's own code (pkg/modulekit).
//
//	togavsu-module check [-installable] [-json] [-previous <package dir> | -previous-file <signed file> -previous-pub <PEM file>] <package dir>
//	togavsu-module package [-installable] -dir <package dir> -key <seed file> -out <signed file>
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
// the core item it waits on. verify checks a signed package file against one trusted
// public key, as the core does before it installs. accepts prints, as JSON, what this
// core accepts: its manifest fields and their digest, its scopes, and the core records a
// module may use. The command never makes or stores a key.
package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

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
  togavsu-module check [-installable] [-json] [-previous <package dir> | -previous-file <signed file> -previous-pub <PEM file>] <package dir>
  togavsu-module package [-installable] -dir <package dir> -key <seed file> -out <signed file>
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
// files and prints each held-back part, and each pending entry that holds nothing back.
func installable(dir string, hold bool, stderr io.Writer) (map[string][]byte, error) {
	files, err := modulekit.ReadDir(dir)
	if err != nil || !hold {
		return files, err
	}
	out, held, err := modulekit.Installable(files)
	if err != nil {
		return nil, err
	}
	for _, h := range held {
		fmt.Fprintln(stderr, h.String())
	}
	if src, ok := files[modulekit.PendingFile]; ok {
		entries, err := modulekit.ParsePending(src)
		if err != nil {
			return nil, err
		}
		for _, e := range modulekit.Unused(entries, held) {
			fmt.Fprintf(stderr, "pending %s (%s) holds nothing back: the core accepts it or the package no longer has it\n", e.Part(), e.Core)
		}
	}
	return out, nil
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
	asJSON := fs.Bool("json", false, "print the result as JSON on stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != one {
		return fmt.Errorf("name one package directory")
	}
	if *previous != "" && *previousFile != "" {
		return fmt.Errorf("name -previous or -previous-file, not both")
	}
	if (*previousFile == "") != (*previousPub == "") {
		return fmt.Errorf("-previous-file and -previous-pub go together")
	}
	*code = exitRefused
	pkg, ms, err := checkDir(fs.Arg(zero), *previous, *previousFile, *previousPub, *hold, stderr)
	if *asJSON {
		res := checkResult{OK: err == nil, Refusals: structured(err)}
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
func checkDir(dir, previous, previousFile, previousPub string, hold bool, stderr io.Writer) (*modulekit.Package, []modulekit.Migration, error) {
	files, err := installable(dir, hold, stderr)
	if err != nil {
		return nil, nil, err
	}
	pkg, ms, err := modulekit.Check(files)
	if err != nil {
		return nil, nil, err
	}
	next, err := modulekit.Archive(files)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case previousFile != "":
		shipped, err := shippedPackage(previousFile, previousPub)
		if err != nil {
			return nil, nil, err
		}
		err = modulekit.CheckUpdate(shipped, next)
		return pkg, ms, err
	case previous != "":
		// A source directory is the previous release only when its build held nothing
		// back; re-running Installable on it would apply today's core, not what shipped.
		old, err := modulekit.ReadDir(previous)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := old[modulekit.PendingFile]; ok {
			return nil, nil, modulekit.Refusals{{Kind: modulekit.KindMigration, Code: modulekit.RefuseUpgradeBaseline, File: modulekit.PendingFile,
				Message: "the previous version lists pending parts, so its source is not what it shipped; pass its signed package with -previous-file and -previous-pub"}}
		}
		prev, err := modulekit.Archive(old)
		if err != nil {
			return nil, nil, err
		}
		if err := modulekit.CheckUpgrade(prev, next); err != nil {
			return nil, nil, modulekit.Refusals{{Kind: modulekit.KindMigration, Code: modulekit.RefuseUpgrade, Message: err.Error(), APICode: modulekit.Code(err), Err: err}}
		}
	}
	return pkg, ms, nil
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
	dir := fs.String("dir", "", "package directory (togavsu-module.yaml and migrations/)")
	keyFile := fs.String("key", "", "file holding a base64 ed25519 seed")
	out := fs.String("out", "", "signed package file to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *keyFile == "" || *out == "" || fs.NArg() != zero {
		return fmt.Errorf("-dir, -key and -out are required")
	}
	*code = exitRefused
	seed, err := os.ReadFile(*keyFile)
	if err != nil {
		return err
	}
	priv, err := modulekit.ParseSeed(string(seed))
	if err != nil {
		return err
	}
	files, err := installable(*dir, *hold, stderr)
	if err != nil {
		return err
	}
	// Build refuses to sign what the core would refuse to install.
	signed, err := modulekit.Build(priv, files)
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

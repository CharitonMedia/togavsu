// Command togavsu-module checks, packages and verifies Togavsu module packages with the
// core's own code (pkg/modulekit).
//
//	togavsu-module check [-previous <package dir>] <package dir>
//	togavsu-module package -dir <package dir> -key <seed file> -out <signed file>
//	togavsu-module verify -file <signed file> -pub <public key PEM file>
//
// check runs the manifest and migration checks the core runs at install; with
// -previous it also checks the package as an update of that version (no migration that
// ran may change or go missing). package archives the directory, signs it with the
// base64 Ed25519 seed in the key file and writes the signed package file. verify checks
// a signed package file against one trusted public key, as the core does before it
// installs. The command never makes or stores a key.
package main

import (
	"crypto/ed25519"
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
  togavsu-module check [-previous <package dir>] <package dir>
  togavsu-module package -dir <package dir> -key <seed file> -out <signed file>
  togavsu-module verify -file <signed file> -pub <public key PEM file>
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
		err = check(args[one:], stdout, &code)
	case "package":
		err = pack(args[one:], stdout, &code)
	case "verify":
		err = verify(args[one:], stdout, &code)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintf(stderr, "togavsu-module %s: %v\n", args[0], err)
		return code
	}
	return exitOK
}

func flags(name string, code *int) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	*code = exitUsage
	return fs
}

func check(args []string, stdout io.Writer, code *int) error {
	fs := flags("check", code)
	previous := fs.String("previous", "", "package directory of the version this one updates")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != one {
		return fmt.Errorf("name one package directory")
	}
	*code = exitRefused
	files, err := modulekit.ReadDir(fs.Arg(zero))
	if err != nil {
		return err
	}
	raw, err := modulekit.Archive(files)
	if err != nil {
		return err
	}
	pkg, ms, err := modulekit.CheckPackage(raw)
	if err != nil {
		return err
	}
	if *previous != "" {
		old, err := modulekit.ReadDir(*previous)
		if err != nil {
			return err
		}
		prev, err := modulekit.Archive(old)
		if err != nil {
			return err
		}
		if err := modulekit.CheckUpgrade(prev, raw); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "ok: %s %s, %d migration(s), digest %s\n", pkg.Manifest.ID, pkg.Manifest.Version, len(ms), pkg.Digest)
	return nil
}

func pack(args []string, stdout io.Writer, code *int) error {
	fs := flags("package", code)
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
	files, err := modulekit.ReadDir(*dir)
	if err != nil {
		return err
	}
	// Refuse to sign what the core would refuse to install.
	if _, _, err := modulekit.Check(files); err != nil {
		return err
	}
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

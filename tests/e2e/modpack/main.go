// Command modpack signs the Matter Clock sample package with a given container image and
// digest, for the Compose-stack module run (scripts/ci/test-module-stack.sh). The sample
// manifest in examples/ carries a placeholder digest; this writes a package whose digest
// is the image the test pushed, signed with a throwaway key. Synthetic test tooling only.
//
//	go run ./tests/e2e/modpack -image localhost:5056/togavsu-matter-clock -digest sha256:... -out pkg.json -pub key.pem
package main

import (
	"bytes"
	"crypto/ed25519"
	"flag"
	"log"
	"os"
	"regexp"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

func main() {
	image := flag.String("image", "", "container image repository")
	digest := flag.String("digest", "", "image digest, sha256:<hex>")
	out := flag.String("out", "", "signed package file to write")
	pub := flag.String("pub", "", "file to write the signer's PEM public key to")
	flag.Parse()
	if !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(*digest) || *image == "" || *out == "" || *pub == "" {
		log.Fatal("modpack: -image, -digest sha256:<hex>, -out and -pub are required")
	}
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		log.Fatal(err)
	}
	pkg, err := matterclock.Build(priv, func(f map[string][]byte) {
		m := f[modules.ManifestFile]
		m = regexp.MustCompile(`(?m)^  image: .*$`).ReplaceAll(m, []byte("  image: "+*image))
		m = regexp.MustCompile(`(?m)^  digest: .*$`).ReplaceAll(m, []byte("  digest: "+*digest))
		if !bytes.Contains(m, []byte(*digest)) {
			log.Fatal("modpack: the manifest has no container digest line")
		}
		f[modules.ManifestFile] = m
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, pkg, 0o600); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*pub, []byte(marketplace.PublicKeyPEM(priv.Public().(ed25519.PublicKey))), 0o600); err != nil {
		log.Fatal(err)
	}
}

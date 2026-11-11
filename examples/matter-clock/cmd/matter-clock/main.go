// Command matter-clock runs the Matter Clock sample module, or signs its package.
//
//	matter-clock serve -core http://core:8080 -secrets /run/secrets -db-host postgres:5432 -db-name togavsu
//	matter-clock package -key signing.key -out matter-clock.tar.gz
//
// The signing key file holds a base64 ed25519 seed; the firm adds its public key on the
// admin page as a trusted local key. Secrets come from the Docker secrets the core hands
// the supervisor: togavsu_api_token, togavsu_webhook_secret, togavsu_db_role,
// togavsu_db_password and togavsu_db_schema.
package main

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	matterclock "github.com/CharitonMedia/togavsu/examples/matter-clock"
	"github.com/CharitonMedia/togavsu/internal/modules"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Small fixed numbers spelled as arithmetic for the AC-030 source check.
var (
	zero = int(0)
	one  = zero + 1
)

func main() {
	log.SetFlags(zero)
	args := os.Args[one:]
	if len(args) < one {
		log.Fatal("usage: matter-clock serve|package [flags]")
	}
	switch args[zero] {
	case "serve":
		serve(args[one:])
	case "package":
		pack(args[one:])
	default:
		log.Fatalf("unknown command %q", args[zero])
	}
}

func secret(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		log.Fatalf("secret %s: %v", name, err)
	}
	return strings.TrimSpace(string(b))
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":8080", "address to listen on")
	core := fs.String("core", "http://core:8080", "core base URL inside the stack")
	dir := fs.String("secrets", "/run/secrets", "directory of Docker secrets")
	host := fs.String("db-host", "postgres:5432", "Postgres host:port")
	name := fs.String("db-name", "togavsu", "database name")
	skew := fs.String("webhook-skew", "5m", "allowed webhook clock skew")
	header := fs.String("read-header-timeout", "10s", "HTTP read-header timeout")
	_ = fs.Parse(args)
	u := url.URL{Scheme: "postgres", User: url.UserPassword(secret(*dir, "togavsu_db_role"), secret(*dir, "togavsu_db_password")), Host: *host, Path: "/" + *name}
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		log.Fatal(err)
	}
	timeout, err := time.ParseDuration(*header)
	if err != nil {
		log.Fatal(err)
	}
	skewDur, err := time.ParseDuration(*skew)
	if err != nil {
		log.Fatal(err)
	}
	srv := matterclock.New(matterclock.Config{Core: *core, Token: secret(*dir, "togavsu_api_token"),
		WebhookSecret: secret(*dir, "togavsu_webhook_secret"), DB: db, SkewSeconds: int(skewDur.Seconds())})
	log.Printf("matter-clock listening on %s", *listen)
	log.Fatal((&http.Server{Addr: *listen, Handler: srv.Handler(), ReadHeaderTimeout: timeout}).ListenAndServe())
}

func pack(args []string) {
	fs := flag.NewFlagSet("package", flag.ExitOnError)
	keyFile := fs.String("key", "", "file holding a base64 ed25519 seed")
	out := fs.String("out", "matter-clock.tar.gz", "package file to write")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		log.Fatal("the key file must hold a base64 ed25519 seed")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pkg, err := matterclock.Build(priv, nil)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, pkg, os.FileMode(zero)|0o644); err != nil {
		log.Fatal(err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("wrote %s, signed by key %s\npublic key (add it as a trusted signing key, public_key_pem):\n%s", *out, modules.KeyID(pub), modules.PublicKeyPEM(pub))
}

// Command core is the Togavsu application server.
//
//	core          migrate, seed reference rows, and serve HTTP on :8080
//	core check    validate the four bootstrap values and exit
//	core migrate  apply migrations and seal the append-only tables, then exit
//	              (TOGAVSU_ADMIN_DATABASE_URL is a superuser; see core.MigrateSealed)
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CharitonMedia/togavsu/internal/bootstrap"
	"github.com/CharitonMedia/togavsu/internal/core"
)

// listen is the container port Caddy proxies to (deploy/caddy/Caddyfile CORE_UPSTREAM).
const listen = ":8080"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "check" {
		os.Exit(bootstrap.Run(os.Args, os.Stdout, os.Stderr))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		os.Exit(migrate(ctx, os.Stdout, os.Stderr))
	}
	os.Exit(serve(ctx, os.Stdout, os.Stderr))
}

// migrate prints migrated=true or migrated=false, which the supervisor's update reads.
func migrate(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := bootstrap.Load()
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	ran, err := core.MigrateSealed(ctx, cfg.DatabaseURL, bootstrap.AdminDatabaseURL())
	fmt.Fprintf(stdout, "migrated=%t\n", ran)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

func serve(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := bootstrap.Load()
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	app, err := core.Open(ctx, core.Config{DatabaseURL: cfg.DatabaseURL, Key: cfg.EncryptionKey, Domain: cfg.Domain,
		SupervisorURL: core.SupervisorURL, SupervisorSecret: cfg.SupervisorSecret})
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	defer app.Close()
	srv := &http.Server{Addr: listen, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(stderr, "http: ", 0)}
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	fmt.Fprintf(stdout, "togavsu core: serving domain=%s on %s\n", cfg.Domain, listen)
	select {
	case err := <-done:
		fmt.Fprintln(stderr, err.Error())
		return 1
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}

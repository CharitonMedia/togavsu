// Command core is the Togavsu application server.
//
//	core              refuse an unsealed database (core.Config.RequireSealed), migrate,
//	                  seed reference rows, and serve HTTP on :8080
//	core check        validate the four bootstrap values and exit
//	core migrate      apply the migrations as the core role and seal the append-only tables
//	                  (TOGAVSU_ADMIN_DATABASE_URL is a superuser and is required; see
//	                  core.MigrateSealed), verify the seal,
//	                  print migrated=true|false, and exit; Compose's migrate one-shot and the
//	                  supervisor's update migrator run it
//	core healthcheck  exit 0 when this container's /api/v1/health answers 200 (image HEALTHCHECK)
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

// healthURL is what the image HEALTHCHECK asks; the supervisor waits on that status.
const healthURL = "http://127.0.0.1:8080/api/v1/health"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "check":
			os.Exit(bootstrap.Run(os.Args, os.Stdout, os.Stderr))
		case "healthcheck":
			os.Exit(healthcheck(healthURL, os.Stderr))
		case "migrate":
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			os.Exit(migrate(ctx, os.Stdout, os.Stderr))
		default:
			fmt.Fprintf(os.Stderr, "core: unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(serve(ctx, os.Stdout, os.Stderr))
}

// migrate prints migrated=true when any migration was applied, including one applied
// before a later migration failed, so the supervisor knows whether a rollback applies.
func migrate(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := bootstrap.Load()
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 2
	}
	ran, err := core.MigrateSealed(ctx, cfg.DatabaseURL, bootstrap.AdminDatabaseURL())
	fmt.Fprintf(stdout, "migrated=%t\n", ran)
	if err != nil {
		fmt.Fprintln(stderr, "core: migrate:", err.Error())
		return 1
	}
	return 0
}

func healthcheck(url string, stderr io.Writer) int {
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "health", res.Status)
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
	// The serving core never holds the superuser URL; only core migrate does.
	if bootstrap.AdminDatabaseURL() != "" {
		fmt.Fprintln(stderr, "core: refusing to serve with "+bootstrap.AdminDatabaseURLName+" set; only core migrate may hold the superuser url")
		return 2
	}
	app, err := core.Open(ctx, core.Config{DatabaseURL: cfg.DatabaseURL, Key: cfg.EncryptionKey, Domain: cfg.Domain,
		SupervisorURL: core.SupervisorURL, SupervisorSecret: cfg.SupervisorSecret, RequireSealed: true})
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

// Command marketplace runs the Togavsu marketplace service (PRD 10.6). It is deployed
// separately from the core: marketplace -config /etc/togavsu-marketplace/config.json.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"

	"github.com/CharitonMedia/togavsu/marketplace"
)

func main() {
	path := flag.String("config", "/etc/togavsu-marketplace/config.json", "path to the JSON config file")
	flag.Parse()
	cfg, err := marketplace.LoadConfig(*path)
	if err != nil {
		log.Fatal(err)
	}
	token, err := cfg.OperatorToken()
	if err != nil {
		log.Fatal(err)
	}
	m, err := marketplace.Open(cfg.DataDir, cfg.AuthorSession(), nil)
	if err != nil {
		log.Fatal(err)
	}
	srv, err := marketplace.NewServer(m, cfg, token, nil)
	if err != nil {
		log.Fatal(err)
	}
	hs := &http.Server{Addr: cfg.Listen, Handler: srv.Handler(), ReadHeaderTimeout: cfg.ReadHeaderTimeout()}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = hs.Shutdown(context.WithoutCancel(ctx))
	}()
	log.Printf("marketplace listening on %s", cfg.Listen)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

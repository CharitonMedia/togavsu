package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch args[0] {
	case "serve":
		return serve()
	case "healthcheck":
		return healthcheck()
	case "backup":
		return backupOnce()
	case "version":
		fmt.Println("togavsu-supervisor 0.0.0-scaffold")
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve() error {
	cfg, err := supervisor.Load()
	if err != nil {
		return err
	}
	log := slog.New(supervisor.ScrubHandler{Next: slog.NewJSONHandler(os.Stdout, nil)})
	ops, err := wire(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           supervisor.NewMux(cfg.Token, ops, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := ops.Recover(context.Background()); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errc <- err
		}
	}()
	go schedule(ctx, cfg, ops)
	select {
	case <-ctx.Done():
		return ops.Drain(srv)
	case err := <-errc:
		return err
	}
}

func backupOnce() error {
	cfg, err := supervisor.Load()
	if err != nil {
		return err
	}
	ops, err := wire(cfg)
	if err != nil {
		return err
	}
	meta, err := ops.CreateBackup(context.Background(), "manual")
	if err != nil {
		return err
	}
	fmt.Println(meta.ID)
	return nil
}

func healthcheck() error {
	listen := os.Getenv("SUPERVISOR_LISTEN")
	if listen == "" {
		listen = ":8081"
	}
	host := listen
	if host[0] == ':' {
		host = "127.0.0.1" + host
	}
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + host + "/health")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health %s", res.Status)
	}
	return nil
}

func wire(cfg supervisor.Config) (*supervisor.Operations, error) {
	if err := os.MkdirAll(cfg.BackupDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	ledger := supervisor.OpenLedger(filepath.Join(cfg.DataDir, "ledger.jsonl"))
	journal := supervisor.OpenJournal(filepath.Join(cfg.DataDir, "restore-journal.jsonl"))
	updateJournal := supervisor.OpenUpdateJournal(filepath.Join(cfg.DataDir, "update-journal.jsonl"))
	outbox := supervisor.OpenOutbox(filepath.Join(cfg.DataDir, "mail-outbox.jsonl"))
	store := &supervisor.Store{
		Dir:            cfg.BackupDir,
		Dump:           supervisor.DumpCommand("pg_dump", cfg.DatabaseURL),
		Book:           &supervisor.DestinationBook{Path: filepath.Join(cfg.DataDir, "destinations.json"), Key: cfg.FieldKey},
		Outbox:         outbox,
		MaxImportBytes: cfg.RestoreMaxBytes,
	}
	if err := store.RecoverPlaintext(); err != nil {
		return nil, err
	}
	docker := &supervisor.DockerRuntime{
		Client:           supervisor.DockerSocketClient(cfg.DockerSocket),
		Network:          cfg.DockerNetwork,
		Env:              supervisor.CoreEnv(),
		Binds:            supervisor.CoreBinds(cfg.AppSecretsVolume),
		AppSecretsVolume: cfg.AppSecretsVolume,
	}
	sched, err := cfg.Schedule()
	if err != nil {
		return nil, err
	}
	updater := &supervisor.Updater{
		Window:   sched.Window,
		Paused:   cfg.UpdatesPaused,
		Registry: supervisor.IndexRegistry{URL: cfg.ReleaseIndexURL, HTTP: &http.Client{Timeout: 30 * time.Second}},
		Verifier: supervisor.CLIVerifier{Cosign: cfg.CosignBin, KeyPath: cfg.CosignKey},
		Runtime:  docker,
		Migrator: supervisor.DockerMigrator{Docker: docker},
		Backup: func(ctx context.Context, reason string) (supervisor.BackupMeta, error) {
			pass, err := cfg.ReloadPassphrase()
			if err != nil {
				return supervisor.BackupMeta{}, err
			}
			store.Passphrase = pass
			return store.Create(ctx, reason)
		},
		Rollback: func(context.Context, string) error {
			return supervisor.ErrPreserveWrites
		},
		Proxy: &supervisor.CaddyProxy{AdminURL: "http://localhost", AdminSocket: cfg.CaddyAdminSocket, UpstreamFile: cfg.UpstreamFile},
		Notify: func(ctx context.Context, subject, body string) {
			_ = outbox.Notify(ctx, subject, body)
		},
		Ledger:        ledger,
		HealthTimeout: time.Minute,
		Journal:       updateJournal,
		UpstreamFile:  cfg.UpstreamFile,
	}
	ops := &supervisor.Operations{
		Store:         store,
		Updater:       updater,
		Runtime:       docker,
		Gate:          &supervisor.Gate{},
		Ledger:        ledger,
		Journal:       journal,
		UpdateJournal: updateJournal,
		Catalog:       supervisor.PostgresCatalog{Bin: "psql", AdminURL: cfg.AdminDatabaseURL},
		Outbox:        outbox,
		Passphrase:    cfg.ReloadPassphrase,
		ApplyRestore: func(ctx context.Context, payload supervisor.RestorePayload) error {
			return supervisor.ApplyProductRestore(ctx, "psql", cfg.AdminDatabaseURL, cfg.DatabaseURL, payload)
		},
		Measure: supervisor.MeasureCommand("psql", cfg.AdminDatabaseURL),
	}
	return ops, nil
}

func schedule(ctx context.Context, cfg supervisor.Config, ops *supervisor.Operations) {
	sched, err := cfg.Schedule()
	if err != nil {
		slog.Error("scheduled tasks are off because the schedule settings are invalid", "err", err)
		return
	}
	runner := &supervisor.Scheduler{Ops: ops, Schedule: sched, Paused: cfg.UpdatesPaused}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if err := runner.Tick(ctx, now); err != nil {
				slog.Error("scheduled tasks were not run because the ledger could not be read", "err", err)
				continue
			}
			_, _ = supervisor.PruneLogFiles(cfg.LogDir, now, time.Duration(cfg.LogRetentionDays)*24*time.Hour)
			_ = ops.Prune(ctx, time.Duration(cfg.LocalRetentionDays)*24*time.Hour)
		}
	}
}

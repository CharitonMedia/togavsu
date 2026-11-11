package core

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// A trust mail send stuck on a mail server that accepts the connection and never answers
// must not hold Close (shutdown or an overnight update) for the whole send limit, and the
// outbox row it was sending keeps the error so the admin sees why it is still queued.
func TestFinanceCloseDuringSilentMailServer(t *testing.T) {
	f := newFin(t, func(c *Config) { c.MailEvery = 50 * time.Millisecond })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // never greets, never reads
			mu.Unlock()
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	port := ln.Addr().(*net.TCPAddr).Port
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_port", map[string]any{"value": port})
	// The default limit is 60 seconds; Close must not wait for it.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_timeout_seconds", map[string]any{"value": 600})

	matter, clientID := f.matter("Estate of Quiet", "quiet@client.example", nil)
	f.post(f.admin, matter, clientID, "deposit", 100_00)
	f.post(f.atty, matter, clientID, "withdrawal", 150_00) // below zero: queues flag mail

	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery loop never connected to the mail server")
	}
	closed := make(chan error, 1)
	start := time.Now()
	go func() { closed <- f.app.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close waited on a mail server that never answers")
	}
	t.Logf("Close returned after %v with a send in flight", time.Since(start))

	// Reopen without a delivery loop to read the outbox; cleanup closes this core.
	f.cfg.MailEvery = -1
	app, err := Open(context.Background(), f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.app = app
	if f.count(`SELECT count(*) FROM mail_outbox WHERE sent_at = 0`) == 0 {
		t.Fatal("the cut-off flag mail is no longer queued")
	}
	if f.count(`SELECT count(*) FROM mail_outbox WHERE sent_at = 0 AND attempts > 0 AND last_error <> ''`) == 0 {
		t.Fatal("the send cut off by Close left no error on its outbox row")
	}
}

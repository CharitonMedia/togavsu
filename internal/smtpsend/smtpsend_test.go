package smtpsend

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer answers a minimal SMTP dialogue and records the DATA body.
func fakeServer(t *testing.T, ln net.Listener, got chan<- string) {
	t.Helper()
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(s string) { conn.Write([]byte(s + "\r\n")) }
	say("220 fake ESMTP")
	var body strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				inData = false
				got <- body.String()
				say("250 queued")
				continue
			}
			body.WriteString(line)
			continue
		}
		switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
		case strings.HasPrefix(cmd, "EHLO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "DATA"):
			inData = true
			say("354 go ahead")
		case strings.HasPrefix(cmd, "QUIT"):
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func TestSendDeliversMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go fakeServer(t, ln, got)
	err = Send(context.Background(), ln.Addr().String(), nil, "from@example.test", []string{"to@example.test"}, []byte("Subject: hi\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case b := <-got:
		if !strings.Contains(b, "body") {
			t.Fatalf("server got %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server never received DATA")
	}
}

// A server that accepts and then never speaks must not hold Send past cancellation.
func TestSendReturnsWhenCanceledOnSilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			held <- c // accept, never write the greeting
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Send(ctx, ln.Addr().String(), nil, "a@example.test", []string{"b@example.test"}, []byte("x"))
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send still blocked 5s after cancel")
	}
	select {
	case c := <-held:
		c.Close()
	default:
	}
}

// The deadline alone ends a stalled send even when nobody cancels.
func TestSendHonorsDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(10 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = Send(ctx, ln.Addr().String(), nil, "a@example.test", []string{"b@example.test"}, []byte("x"))
	if err == nil {
		t.Fatal("want an error from a silent server")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Send took %v past a 300ms deadline", time.Since(start))
	}
}

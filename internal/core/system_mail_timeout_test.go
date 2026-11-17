package core

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestIsModuleUnreachableTimeoutIsRetryable(t *testing.T) {
	if isModuleUnreachable(context.DeadlineExceeded) {
		t.Fatal("deadline exceeded must not fall back to SMTP")
	}
	if isModuleUnreachable(&net.DNSError{IsTimeout: true, Err: "i/o timeout"}) {
		t.Fatal("net timeout must not fall back to SMTP")
	}
	to := timeoutErr{}
	if isModuleUnreachable(to) {
		t.Fatal("Timeout() net.Error must not fall back to SMTP")
	}
	if !isModuleUnreachable(errors.New("connection refused")) {
		t.Fatal("connection refused is unreachable")
	}
	if !isModuleUnreachable(&net.OpError{Op: "dial", Err: errors.New("connect: connection refused")}) {
		t.Fatal("dial refused is unreachable")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestSystemMailerClientHasTimeout(t *testing.T) {
	m := &systemMailer{}
	c := m.client()
	if c == nil || c.Timeout <= 0 {
		t.Fatalf("default client timeout = %v, want > 0", c.Timeout)
	}
	if c.Timeout > 10*time.Minute {
		t.Fatalf("default client timeout %v is unbounded for practical purposes", c.Timeout)
	}
}

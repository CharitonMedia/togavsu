// Package smtpsend is net/smtp.SendMail with a deadline. The standard SendMail ignores
// context and sets no I/O deadlines, so a server that accepts the connection and then
// stops answering holds the caller forever; that blocked graceful shutdown (PRD 6, core
// updates self-apply overnight).
package smtpsend

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"time"
)

// Timeout bounds one whole send when ctx carries no earlier deadline.
const Timeout = 60 * time.Second

// Send delivers raw from from to every address in to through addr ("host:port"),
// upgrading to STARTTLS when offered and authenticating with auth when non-nil, exactly
// as net/smtp.SendMail does. It returns once ctx ends or the send passes its deadline.
func Send(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, raw []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			return err
		}
	}
	// Cancellation before the deadline closes the connection so a blocked read returns.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	err = send(conn, host, auth, from, to, raw)
	if err != nil && ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	return err
}

func send(conn net.Conn, host string, auth smtp.Auth, from string, to []string, raw []byte) error {
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if auth != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server doesn't support AUTH")
		}
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

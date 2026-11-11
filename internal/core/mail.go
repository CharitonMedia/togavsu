package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/smtpsend"
)

// smtpMailer is billing's system mail (PRD 8.8): the admin's mail.smtp_* settings, read on
// every send so a change applies without a restart. net/smtp upgrades to STARTTLS when
// the server offers it.
type smtpMailer struct {
	Settings *settings.Registry
}

// errMailNotConfigured is returned while mail.smtp_host or mail.smtp_from is empty.
var errMailNotConfigured = errors.New("system mail is not configured (mail.smtp_host, mail.smtp_port and mail.smtp_from)")

func (m smtpMailer) Send(ctx context.Context, msg billing.Message) error {
	get := func(k string) string {
		v, _ := m.Settings.Get(ctx, k)
		return v
	}
	host, from, port := get("mail.smtp_host"), get("mail.smtp_from"), get("mail.smtp_port")
	if host == "" || from == "" {
		return errMailNotConfigured
	}
	if _, err := strconv.Atoi(port); err != nil {
		return errMailNotConfigured
	}
	if len(msg.To) == 0 {
		return errors.New("system mail has no recipient")
	}
	raw, err := buildMessage(from, msg)
	if err != nil {
		return err
	}
	var auth smtp.Auth
	if user := get("mail.smtp_username"); user != "" {
		auth = smtp.PlainAuth("", user, get("mail.smtp_password"), host)
	}
	limit, err := time.ParseDuration(get("mail.smtp_timeout_seconds") + "s")
	if err != nil {
		return errMailNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	return smtpsend.Send(ctx, net.JoinHostPort(host, port), auth, from, msg.To, raw)
}

// buildMessage renders msg as text/plain, or multipart/mixed with base64 attachments.
func buildMessage(from string, msg billing.Message) ([]byte, error) {
	for _, a := range append([]string{from}, msg.To...) {
		if _, err := mail.ParseAddress(a); err != nil {
			return nil, fmt.Errorf("system mail address %q: %w", a, err)
		}
	}
	var b bytes.Buffer
	head := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	head("From", from)
	head("To", strings.Join(msg.To, ", "))
	head("Subject", mime.QEncoding.Encode("utf-8", oneLine(msg.Subject)))
	head("MIME-Version", "1.0")
	text := strings.ReplaceAll(strings.ReplaceAll(msg.Text, "\r\n", "\n"), "\n", "\r\n")
	if len(msg.Attachments) == 0 {
		head("Content-Type", "text/plain; charset=utf-8")
		b.WriteString("\r\n" + text)
		return b.Bytes(), nil
	}
	w := multipart.NewWriter(&b)
	head("Content-Type", "multipart/mixed; boundary="+w.Boundary())
	b.WriteString("\r\n")
	part, err := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}})
	if err != nil {
		return nil, err
	}
	if _, err := part.Write([]byte(text)); err != nil {
		return nil, err
	}
	for _, a := range msg.Attachments {
		ctype := a.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {ctype},
			"Content-Transfer-Encoding": {"base64"},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": oneLine(a.Name)})},
		})
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(base64Lines(a.Data)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// base64Lines is data in base64 with CRLF after every 76 characters (RFC 2045 6.8).
func base64Lines(data []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(data)
	var b bytes.Buffer
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.Bytes()
}

func oneLine(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

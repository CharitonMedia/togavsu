package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"github.com/CharitonMedia/togavsu/internal/smtpsend"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// systemMailer routes every core system mail send (CORE-05): through the email module
// when mail.route_through_email_module is on and a declaring module is installed and
// enabled with mail:system approved, otherwise through SMTP.
type systemMailer struct {
	Settings    *settings.Registry
	Modules     *modules.Service
	HTTP        *http.Client
	Now         func() time.Time
	alertModule func(ctx context.Context, kind, recordID string) (string, error)
}

// systemMail is one message for the router and the module POST body.
type systemMail struct {
	ID          string
	Kind        string
	To          []string
	Subject     string
	Text        string
	Attachments []billing.Attachment
}

// Send implements billing.Mailer.
func (m *systemMailer) Send(ctx context.Context, msg billing.Message) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	return m.deliver(ctx, systemMail{
		ID: string(id), Kind: "billing", To: msg.To, Subject: msg.Subject,
		Text: msg.Text, Attachments: msg.Attachments,
	})
}

// AsSender is trust.Sender / dashboard.Mailer over the router.
func (m *systemMailer) AsSender() trust.Sender { return systemMailSender{m} }

// systemMailSender adapts systemMailer to trust.Sender and trust.OutboxSender.
type systemMailSender struct{ *systemMailer }

func (s systemMailSender) Send(ctx context.Context, to, subject, body string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	return s.deliver(ctx, systemMail{ID: string(id), Kind: "system", To: []string{to}, Subject: subject, Text: body})
}

func (s systemMailSender) SendOutbox(ctx context.Context, mail trust.Mail) error {
	return s.deliver(ctx, systemMail{
		ID: string(mail.ID), Kind: mail.Kind, To: []string{mail.To},
		Subject: mail.Subject, Text: mail.Body,
	})
}

func (m *systemMailer) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	// Bound every module POST so a stalled email module cannot block the outbox drain,
	// password reset or sendModuleMail (CORE-05). Reuse mail.smtp_timeout_seconds.
	sec := ""
	if m.Settings != nil {
		sec, _ = m.Settings.Get(context.Background(), "mail.smtp_timeout_seconds")
	}
	return &http.Client{Timeout: settings.ModuleMailHTTPTimeout(sec)}
}

func (m *systemMailer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *systemMailer) deliver(ctx context.Context, msg systemMail) error {
	if len(msg.To) == 0 {
		return errors.New("system mail has no recipient")
	}
	if msg.ID == "" {
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		msg.ID = string(id)
	}
	if msg.Kind == "" {
		msg.Kind = "system"
	}
	forceSMTP := m.aboutRoutingModule(ctx, msg)
	routeOn, _ := m.Settings.Get(ctx, "mail.route_through_email_module")
	if !forceSMTP && routeOn == "true" && m.Modules != nil {
		route, ok, err := m.Modules.SystemMailRoute(ctx)
		if err != nil {
			return err
		}
		if ok {
			err := m.postModule(ctx, route, msg)
			switch {
			case err == nil:
				return nil
			case errors.As(err, &trust.PermanentMailError{}):
				return err
			case errors.As(err, &moduleNotConfigured{}):
				return m.sendSMTP(ctx, msg)
			case isModuleUnreachable(err):
				return m.sendSMTP(ctx, msg)
			default:
				// 5xx / timeout: retry with the same Idempotency-Key.
				return err
			}
		}
	}
	return m.sendSMTP(ctx, msg)
}

func (m *systemMailer) aboutRoutingModule(ctx context.Context, msg systemMail) bool {
	if m.Modules == nil || (msg.Kind != "module_alert" && msg.Kind != "update_failure") {
		return false
	}
	route, ok, err := m.Modules.SystemMailRoute(ctx)
	if err != nil || !ok {
		return false
	}
	if m.alertModule != nil {
		mod, err := m.alertModule(ctx, msg.Kind, msg.ID)
		if err == nil && mod == route.ModuleID {
			return true
		}
	}
	if msg.Kind == "module_alert" {
		mod, err := m.Modules.OutboxAlertModule(ctx, msg.ID)
		if err == nil && mod == route.ModuleID {
			return true
		}
	}
	return strings.Contains(msg.Subject, route.ModuleID) || strings.Contains(msg.Text, route.ModuleID)
}

type moduleNotConfigured struct{ msg string }

func (e moduleNotConfigured) Error() string { return e.msg }

func isModuleUnreachable(err error) bool {
	if err == nil {
		return false
	}
	// Timeouts and deadline exceeded are retried with the same Idempotency-Key (CORE-05),
	// not treated as "module not running" SMTP fallback (which would double-deliver).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) {
		if ne.Timeout() {
			return false
		}
		return true
	}
	s := err.Error()
	if strings.Contains(s, "context deadline exceeded") || strings.Contains(s, "Client.Timeout") {
		return false
	}
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no such host") ||
		strings.Contains(s, "module not running") || strings.Contains(s, "connect:")
}

type moduleMailBody struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	To          []string           `json:"to"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text"`
	Attachments []moduleMailAttach `json:"attachments"`
}

type moduleMailAttach struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	DataBase64  string `json:"data_base64"`
}

func (m *systemMailer) postModule(ctx context.Context, route modules.SystemMailRoute, msg systemMail) error {
	if route.Secret == "" {
		return errors.New("modules: empty webhook secret")
	}
	atts := make([]moduleMailAttach, 0, len(msg.Attachments))
	for _, a := range msg.Attachments {
		ctype := a.ContentType
		if ctype == "" {
			ctype = "application/octet-stream"
		}
		atts = append(atts, moduleMailAttach{Name: a.Name, ContentType: ctype, DataBase64: base64.StdEncoding.EncodeToString(a.Data)})
	}
	raw, err := json.Marshal(moduleMailBody{
		ID: msg.ID, Kind: msg.Kind, To: msg.To, Subject: msg.Subject, Text: msg.Text, Attachments: atts,
	})
	if err != nil {
		return err
	}
	url := route.BaseURL + route.Path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", msg.ID)
	at := m.now()
	headers := modules.SignCoreWebhookHeaders(route.Secret, at, raw)
	if headers == nil {
		return errors.New("modules: empty webhook secret")
	}
	for key, values := range headers {
		req.Header[key] = values
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return trust.PermanentMailError{Msg: permanentMessage(body, "module rejected the message")}
	case resp.StatusCode == http.StatusServiceUnavailable:
		if problemCode(body) == "not_configured" {
			return moduleNotConfigured{msg: "email module is not configured for system mail"}
		}
		return fmt.Errorf("module answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	case resp.StatusCode >= http.StatusInternalServerError:
		return fmt.Errorf("module answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	default:
		return fmt.Errorf("module answered %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
}

func problemCode(body []byte) string {
	var p struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Code
}

func permanentMessage(body []byte, fallback string) string {
	var p struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &p) == nil && strings.TrimSpace(p.Message) != "" {
		return p.Message
	}
	if s := strings.TrimSpace(string(body)); s != "" {
		return s
	}
	return fallback
}

func (m *systemMailer) sendSMTP(ctx context.Context, msg systemMail) error {
	err := smtpMailer{Settings: m.Settings}.Send(ctx, billing.Message{
		To: msg.To, Subject: msg.Subject, Text: msg.Text, Attachments: msg.Attachments,
	})
	if errors.Is(err, errMailNotConfigured) {
		return fmt.Errorf("%w", err)
	}
	return err
}

// smtpMailer is the admin's mail.smtp_* settings (PRD 8.8). systemMailer uses it for
// fallback and for mail about the routing module itself.
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
	return smtpsend.Send(ctx, net.JoinHostPort(host, port), get("mail.smtp_starttls") != "false", auth, from, msg.To, raw)
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

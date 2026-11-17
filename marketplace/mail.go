package marketplace

import (
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Mail delivery status values persisted on MailRecord.
const (
	mailPending = "pending"
	mailSent    = "sent"
	mailFailed  = "failed"
)

// MailSender delivers outbound marketplace mail (price-change notices). Implementations
// must not log card numbers or Stripe secrets; bodies are plain text notices only.
type MailSender interface {
	Send(to, subject, body string) error
}

// RecordingMailer is the in-process sender used in tests: every Send is kept for
// inspection. Production deployments set smtp_addr.
type RecordingMailer struct {
	mu   sync.Mutex
	Sent []MailRecord
	now  func() time.Time
	// FailNext, when true, makes the next Send return an error (tests).
	FailNext bool
}

// NewRecordingMailer returns a mailer that records deliveries.
func NewRecordingMailer(now func() time.Time) *RecordingMailer {
	if now == nil {
		now = time.Now
	}
	return &RecordingMailer{now: func() time.Time { return now().UTC() }}
}

// Send records the message.
func (m *RecordingMailer) Send(to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailNext {
		m.FailNext = false
		return invalid("mail", "synthetic mail delivery failure")
	}
	m.Sent = append(m.Sent, MailRecord{To: to, Subject: subject, Body: body, SentAt: m.now(), Status: mailSent})
	return nil
}

// Delivered returns a copy of recorded messages.
func (m *RecordingMailer) Delivered() []MailRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MailRecord, len(m.Sent))
	copy(out, m.Sent)
	return out
}

// SMTPMailer sends mail through a local or operator SMTP relay (net/smtp).
type SMTPMailer struct {
	Addr string // host:port
	From string
	Auth smtp.Auth // optional; nil for open local relays in tests
}

// Send delivers one message.
func (m SMTPMailer) Send(to, subject, body string) error {
	from := strings.TrimSpace(m.From)
	if from == "" {
		from = Defaults().DefaultSMTPFrom
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return invalid("mail", "recipient is required")
	}
	msg := []byte("From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body + "\r\n")
	return smtp.SendMail(m.Addr, m.Auth, from, []string{to}, msg)
}

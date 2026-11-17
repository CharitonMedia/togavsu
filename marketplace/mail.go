package marketplace

import (
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// MailSender delivers outbound marketplace mail (price-change notices). Implementations
// must not log card numbers or Stripe secrets; bodies are plain text notices only.
type MailSender interface {
	Send(to, subject, body string) error
}

// RecordingMailer is the in-process sender used in tests and as the default when SMTP is
// unset: every Send is kept for inspection. Production deployments set smtp_addr.
type RecordingMailer struct {
	mu   sync.Mutex
	Sent []MailRecord
	now  func() time.Time
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
	m.Sent = append(m.Sent, MailRecord{To: to, Subject: subject, Body: body, SentAt: m.now()})
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
		from = "marketplace@localhost"
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

// StartFakeSMTP listens on 127.0.0.1 and accepts SMTP messages for tests. It never
// contacts an external relay.
type FakeSMTP struct {
	Addr     string
	ln       net.Listener
	mu       sync.Mutex
	Messages []string
}

// StartFakeSMTP starts a minimal SMTP acceptor on 127.0.0.1.
func StartFakeSMTP() (*FakeSMTP, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f := &FakeSMTP{Addr: ln.Addr().String(), ln: ln}
	go f.serve()
	return f, nil
}

// Close stops the fake SMTP server.
func (f *FakeSMTP) Close() error {
	if f.ln != nil {
		return f.ln.Close()
	}
	return nil
}

func (f *FakeSMTP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *FakeSMTP) handle(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	writeSMTP(c, "220 fake.smtp ESMTP")
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	dataMode := false
	for {
		n, err := c.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		for {
			var line string
			if dataMode {
				if i := strings.Index(string(buf), "\r\n.\r\n"); i >= 0 {
					msg := string(buf[:i])
					f.mu.Lock()
					f.Messages = append(f.Messages, msg)
					f.mu.Unlock()
					buf = buf[i+5:]
					dataMode = false
					writeSMTP(c, "250 OK")
					continue
				}
				break
			}
			i := strings.Index(string(buf), "\r\n")
			if i < 0 {
				break
			}
			line = string(buf[:i])
			buf = buf[i+2:]
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeSMTP(c, "250 OK")
			case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
				writeSMTP(c, "250 OK")
			case upper == "DATA":
				writeSMTP(c, "354 End data with <CR><LF>.<CR><LF>")
				dataMode = true
			case upper == "QUIT":
				writeSMTP(c, "221 Bye")
				return
			default:
				writeSMTP(c, "250 OK")
			}
		}
		if err != nil {
			return
		}
	}
}

func writeSMTP(c net.Conn, line string) {
	_, _ = fmt.Fprintf(c, "%s\r\n", line)
}

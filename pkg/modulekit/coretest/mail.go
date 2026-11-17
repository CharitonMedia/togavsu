package coretest

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// MailMsg is one message the SMTP stand-in accepted.
type MailMsg struct {
	From string
	To   []string
	Data string
}

// Mail is the CORE-29 SMTP stand-in: a loopback relay the composed core is pointed at
// so a module suite can observe DeliverMail without touching a real host outbox or MTA.
type Mail struct {
	mu     sync.Mutex
	msgs   []MailMsg
	reject bool
	ln     net.Listener
}

func startMail(t *testing.T) *Mail {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &Mail{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go m.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return m
}

func (m *Mail) port() string { return strconv.Itoa(m.ln.Addr().(*net.TCPAddr).Port) }

// Messages returns every message the stand-in has accepted.
func (m *Mail) Messages() []MailMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MailMsg(nil), m.msgs...)
}

// To returns messages delivered to addr.
func (m *Mail) To(addr string) []MailMsg {
	var out []MailMsg
	for _, msg := range m.Messages() {
		for _, r := range msg.To {
			if strings.EqualFold(r, addr) {
				out = append(out, msg)
			}
		}
	}
	return out
}

// SetReject makes the stand-in refuse every RCPT when v is true.
func (m *Mail) SetReject(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reject = v
}

func (m *Mail) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	rd := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 coretest.smtp ESMTP")
	var cur MailMsg
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-coretest.smtp")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from := strings.TrimSpace(line[10:])
			if i := strings.Index(from, ">"); i >= 0 {
				from = from[:i]
			}
			cur = MailMsg{From: strings.TrimPrefix(from, "<")}
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.mu.Lock()
			reject := m.reject
			m.mu.Unlock()
			if reject {
				say("550 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, strings.Trim(strings.TrimSpace(line[8:]), "<>\r\n"))
			say("250 OK")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				b.WriteString(l)
			}
			cur.Data = b.String()
			m.mu.Lock()
			m.msgs = append(m.msgs, cur)
			m.mu.Unlock()
			say("250 queued")
		case cmd == "RSET", cmd == "NOOP":
			say("250 OK")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

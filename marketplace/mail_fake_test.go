package marketplace

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// FakeSMTP listens on 127.0.0.1 and accepts SMTP messages for tests. It never
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
	cfg := Defaults()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c, cfg)
	}
}

func (f *FakeSMTP) handle(c net.Conn, cfg Config) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(cfg.SMTPDeadline()))
	writeSMTPLine(c, "220 fake.smtp ESMTP")
	buf := make([]byte, int(0), cfg.SMTPReadBufferBytes)
	tmp := make([]byte, cfg.SMTPTmpBufferBytes)
	dataMode := false
	for {
		n, err := c.Read(tmp)
		if n > int(0) {
			buf = append(buf, tmp[:n]...)
		}
		for {
			var line string
			if dataMode {
				if i := strings.Index(string(buf), "\r\n.\r\n"); i >= int(0) {
					msg := string(buf[:i])
					f.mu.Lock()
					f.Messages = append(f.Messages, msg)
					f.mu.Unlock()
					buf = buf[i+len("\r\n.\r\n"):]
					dataMode = false
					writeSMTPLine(c, "250 OK")
					continue
				}
				break
			}
			i := strings.Index(string(buf), "\r\n")
			if i < int(0) {
				break
			}
			line = string(buf[:i])
			buf = buf[i+len("\r\n"):]
			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeSMTPLine(c, "250 OK")
			case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
				writeSMTPLine(c, "250 OK")
			case upper == "DATA":
				writeSMTPLine(c, "354 End data with <CR><LF>.<CR><LF>")
				dataMode = true
			case upper == "QUIT":
				writeSMTPLine(c, "221 Bye")
				return
			default:
				writeSMTPLine(c, "250 OK")
			}
		}
		if err != nil {
			return
		}
	}
}

func writeSMTPLine(c net.Conn, line string) {
	_, _ = fmt.Fprintf(c, "%s\r\n", line)
}

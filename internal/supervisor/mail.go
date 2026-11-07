package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

// Notice is one admin message waiting for core system mail.
//
// ponytail: the supervisor appends an outbox file instead of speaking SMTP,
// ceiling: no message leaves the host, upgrade: the core mailer drains this
// file through the firm's system mail, trigger: SMTP settings or the email
// module can send.
type Notice struct {
	Time    time.Time `json:"time"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
}

type Outbox struct {
	path string
	now  func() time.Time
}

func OpenOutbox(path string) *Outbox {
	return &Outbox{path: path, now: time.Now}
}

func (o *Outbox) Notify(_ context.Context, subject, body string) error {
	if o == nil || o.path == "" {
		return nil
	}
	n := Notice{Time: o.now().UTC(), Subject: subject, Body: body}
	line, err := json.Marshal(n)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

func (o *Outbox) List() ([]Notice, error) {
	raw, err := os.ReadFile(o.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Notice
	for _, line := range bytesSplit(raw) {
		if len(line) == 0 {
			continue
		}
		var n Notice
		if err := json.Unmarshal(line, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func bytesSplit(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b == '\n' {
			lines = append(lines, raw[start:i])
			start = i + 1
		}
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}

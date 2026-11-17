package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

// Notice is one admin message waiting for core system mail. The core's DeliverMail
// drains this file through systemMailer (CORE-05).
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

func (o *Outbox) Path() string {
	if o == nil {
		return ""
	}
	return o.path
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
	raw, err := o.read()
	if err != nil {
		return nil, err
	}
	return parseNotices(raw)
}

// Take renames the outbox aside and returns its notices so the core can send them.
// A concurrent Notify that creates a new outbox after the rename is left for the next Take.
func (o *Outbox) Take() ([]Notice, error) {
	if o == nil || o.path == "" {
		return nil, nil
	}
	pending := o.path + ".draining"
	if err := os.Rename(o.path, pending); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	raw, err := os.ReadFile(pending)
	if err != nil {
		_ = os.Rename(pending, o.path)
		return nil, err
	}
	out, err := parseNotices(raw)
	if err != nil {
		_ = os.Rename(pending, o.path)
		return nil, err
	}
	if err := os.Remove(pending); err != nil && !os.IsNotExist(err) {
		return out, err
	}
	return out, nil
}

// PutAll appends notices back (failed sends stay queued for the next drain).
func (o *Outbox) PutAll(notices []Notice) error {
	if o == nil || o.path == "" || len(notices) == 0 {
		return nil
	}
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, n := range notices {
		line, err := json.Marshal(n)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func (o *Outbox) read() ([]byte, error) {
	raw, err := os.ReadFile(o.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	return raw, err
}

func parseNotices(raw []byte) ([]Notice, error) {
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

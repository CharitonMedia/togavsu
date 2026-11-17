package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Notice is one admin message waiting for core system mail. The core's DeliverMail
// drains this file through systemMailer (CORE-05). ID is stable across retries and is
// the Idempotency-Key the email module sees.
type Notice struct {
	ID      string    `json:"id,omitempty"`
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

func noticeID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (o *Outbox) Notify(_ context.Context, subject, body string) error {
	if o == nil || o.path == "" {
		return nil
	}
	id, err := noticeID()
	if err != nil {
		return err
	}
	n := Notice{ID: id, Time: o.now().UTC(), Subject: subject, Body: body}
	line, err := json.Marshal(n)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	// 0666 so the core user on the shared mail-outbox volume can Take/rename the file.
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
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

func (o *Outbox) drainingPath() string {
	return o.path + ".draining"
}

// Take renames the outbox aside and returns its notices so the core can send them.
// The .draining file stays until Ack so a crash between Take and send does not lose
// notices. A leftover .draining from a prior crash is returned first; concurrent
// Notify writes to path wait for the next Take.
func (o *Outbox) Take() ([]Notice, error) {
	if o == nil || o.path == "" {
		return nil, nil
	}
	pending := o.drainingPath()
	if _, err := os.Stat(pending); err == nil {
		return o.readDraining(pending)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.Rename(o.path, pending); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return o.readDraining(pending)
}

func (o *Outbox) readDraining(pending string) ([]Notice, error) {
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
	return out, nil
}

// Ack removes the .draining file after sends complete (and PutAll has restored any
// failures). Call only after PutAll of the remaining notices; a failed PutAll should
// leave .draining so the next Take recovers the batch.
func (o *Outbox) Ack() error {
	if o == nil || o.path == "" {
		return nil
	}
	if err := os.Remove(o.drainingPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// PutAll appends notices back (failed sends stay queued for the next drain).
func (o *Outbox) PutAll(notices []Notice) error {
	if o == nil || o.path == "" || len(notices) == 0 {
		return nil
	}
	f, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
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

// MigrateLegacyMailOutbox moves notices left in the pre-mail-volume path
// (SupervisorDataDir/mail-outbox.jsonl) into the dedicated mail outbox directory so an
// existing install does not orphan admin notices on upgrade.
func MigrateLegacyMailOutbox(dataDir, mailOutboxDir string) error {
	if dataDir == "" || mailOutboxDir == "" {
		return nil
	}
	legacy := filepath.Join(dataDir, MailOutboxFile)
	dest := filepath.Join(mailOutboxDir, MailOutboxFile)
	if filepath.Clean(legacy) == filepath.Clean(dest) {
		return nil
	}
	raw, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return os.Remove(legacy)
	}
	if err := os.MkdirAll(mailOutboxDir, 0o777); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(legacy)
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

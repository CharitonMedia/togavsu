package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

// errLedgerCorrupt means a newline-terminated ledger record is not valid.
// The tail was committed. Callers must not treat it as a torn write.
var errLedgerCorrupt = errors.New("ledger contains a corrupt committed record")

// Event is one append-only supervisor action. It is the control-plane
// history for updates and backups. It is not the product audit log: that
// log records every firm write with its user and source, and it belongs to
// the identity service.
type Event struct {
	Time   time.Time         `json:"time"`
	Kind   string            `json:"kind"`
	Actor  string            `json:"actor"`
	Fields map[string]string `json:"fields,omitempty"`
}

// Ledger appends JSON lines. It has no rewrite or delete method.
type Ledger struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func OpenLedger(path string) *Ledger {
	return &Ledger{path: path, now: time.Now}
}

func (l *Ledger) Append(kind, actor string, fields map[string]string) (Event, error) {
	ev := Event{Time: l.now().UTC(), Kind: kind, Actor: actor, Fields: fields}
	line, err := json.Marshal(ev)
	if err != nil {
		return Event{}, err
	}
	line = append(line, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := appendDurableLine(l.path, line, validateLedger); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func (l *Ledger) List(kind string) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := repairDurableTail(l.path, validateLedger); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	all, err := readLedger(raw)
	if err != nil {
		return nil, err
	}
	if kind == "" {
		return all, nil
	}
	var out []Event
	for _, ev := range all {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out, nil
}

func validateLedger(raw []byte) error {
	_, err := readLedger(raw)
	return err
}

func readLedger(raw []byte) ([]Event, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	body := raw
	if raw[len(raw)-1] != '\n' {
		i := bytes.LastIndexByte(raw, '\n')
		if i < 0 {
			return nil, nil
		}
		body = raw[:i+1]
	}
	parts := bytes.Split(body, []byte("\n"))
	var out []Event
	for _, part := range parts[:len(parts)-1] {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(part, &ev); err != nil {
			return nil, errLedgerCorrupt
		}
		out = append(out, ev)
	}
	return out, nil
}

// Latest scans the ledger from the start.
//
// ponytail: each schedule tick rereads the whole file, ceiling: linear scan,
// upgrade: keep a rebuilt tail offset beside the ledger, trigger: a tick's
// scan exceeds 50ms or the file passes 50k events.
func (l *Ledger) Latest(kind string) (Event, bool, error) {
	all, err := l.List(kind)
	if err != nil || len(all) == 0 {
		return Event{}, false, err
	}
	return all[len(all)-1], true, nil
}

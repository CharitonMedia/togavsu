package supervisor

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

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
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return Event{}, err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return Event{}, err
	}
	if err := f.Sync(); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func (l *Ledger) List(kind string) ([]Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, err
		}
		if kind == "" || ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
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

package supervisor

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	phaseAccepted       = "accepted"
	phaseRunning        = "running"
	phaseStopping       = "stopping"
	phaseWritersStopped = "writers_stopped"
	phaseApplying       = "applying"
	phaseSwapIntended   = "swap_intended"
	phaseRestarting     = "restarting"
	phaseCompleted      = "completed"
	phaseFailed         = "failed"
)

// journalRecord is the durable restore operation. It records intent and
// observed runtime facts. It must not contain passphrases, tokens, or
// database URLs.
type journalRecord struct {
	Record     RestoreRecord     `json:"record"`
	Phase      string            `json:"phase"`
	Containers []containerIntent `json:"containers,omitempty"`
	Database   *databaseIntent   `json:"database,omitempty"`
	Recovery   string            `json:"recovery,omitempty"`
	Cleanup    string            `json:"cleanup,omitempty"`
}

type containerIntent struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Prior        string `json:"prior"`
	StopIntended bool   `json:"stop_intended"`
	Observed     string `json:"observed,omitempty"`
}

type databaseIntent struct {
	Configured   string `json:"configured"`
	Fresh        string `json:"fresh"`
	Replaced     string `json:"replaced"`
	SwapIntended bool   `json:"swap_intended"`
}

func (r journalRecord) terminal() bool {
	return r.Record.Status == "completed" || r.Record.Status == "failed"
}

// Journal is the append-only restore lifecycle. Each record is fsynced before
// a destructive step so a killed process can reconcile from the last complete
// line.
//
// ponytail: recovery rereads the whole file, ceiling: linear scan, upgrade:
// keep the latest record per id beside the log, trigger: a restart scan
// exceeds 50ms or the file passes 50k records.
type Journal struct {
	path string
	mu   sync.Mutex
	now  func() time.Time
}

func OpenJournal(path string) *Journal {
	return &Journal{path: path, now: time.Now}
}

func (j *Journal) Put(rec journalRecord) error {
	if j == nil || j.path == "" {
		return errors.New("restore journal is not configured")
	}
	if rec.Record.Started.IsZero() && j.now != nil {
		rec.Record.Started = j.now().UTC()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if serr != nil {
		return serr
	}
	if cerr != nil {
		return cerr
	}
	dir, err := os.Open(filepath.Dir(j.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (j *Journal) Latest(id string) (journalRecord, bool, error) {
	all, err := j.read()
	if err != nil {
		return journalRecord{}, false, err
	}
	rec, ok := all[id]
	return rec, ok, nil
}

func (j *Journal) Incomplete() ([]journalRecord, error) {
	all, err := j.read()
	if err != nil {
		return nil, err
	}
	var out []journalRecord
	for _, rec := range all {
		if !rec.terminal() {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (j *Journal) read() (map[string]journalRecord, error) {
	if j == nil || j.path == "" {
		return map[string]journalRecord{}, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.Open(j.path)
	if os.IsNotExist(err) {
		return map[string]journalRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	for sc.Scan() {
		lines = append(lines, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := map[string]journalRecord{}
	for i, line := range lines {
		if len(line) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			if i == len(lines)-1 {
				continue
			}
			return nil, err
		}
		if rec.Record.ID == "" {
			continue
		}
		out[rec.Record.ID] = rec
	}
	return out, nil
}

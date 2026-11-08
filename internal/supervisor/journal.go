package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// errJournalCorrupt means a newline-terminated record is not valid. That
// record was committed. Callers must not treat it as a torn tail or append
// another record after it.
var errJournalCorrupt = errors.New("restore journal contains a corrupt committed record")

const (
	phaseAccepted       = "accepted"
	phaseRunning        = "running"
	phaseStopping       = "stopping"
	phaseWritersStopped = "writers_stopped"
	phaseApplying       = "applying"
	phaseSwapIntended   = "swap_intended"
	phaseRolledBack     = "rolled_back"
	phaseApplied        = "applied"
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
	Configured     string `json:"configured"`
	Fresh          string `json:"fresh"`
	Replaced       string `json:"replaced"`
	SwapIntended   bool   `json:"swap_intended"`
	SwapCommitted  bool   `json:"swap_committed,omitempty"`
	SwapRolledBack bool   `json:"swap_rolled_back,omitempty"`
	// Allocated is set before CREATE DATABASE. FreshOID is the verified oid.
	// An allocation without FreshOID is not deleted.
	Allocated     bool   `json:"allocated,omitempty"`
	OpID          string `json:"op_id,omitempty"`
	ClusterID     string `json:"cluster_id,omitempty"`
	FreshOID      string `json:"fresh_oid,omitempty"`
	ConfiguredOID string `json:"configured_oid,omitempty"`
}

func (r journalRecord) terminal() bool {
	return r.Record.Status == "completed" || r.Record.Status == "failed"
}

// needsMaintenance is a completed data restore whose previous database may
// still exist. It is not an unsafe recovery: writers and the new data are
// already confirmed, and startup must not stay down while only that drop
// remains.
func (r journalRecord) needsMaintenance() bool {
	if r.Record.Status != "completed" {
		return false
	}
	return r.Record.Cleanup == "pending" || r.Cleanup == "pending" || r.Record.Error == "cleanup_pending"
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
	// failRemaining, when set by tests, fails that many later Put calls
	// before they write. Zero means every Put is attempted. failPhase
	// limits those failures to one phase; empty matches every phase.
	failRemaining int
	failPhase     string
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
	if j.failRemaining > 0 && (j.failPhase == "" || rec.Phase == j.failPhase) {
		j.failRemaining--
		return errors.New("journal persist failed")
	}
	return appendDurableLine(j.path, line, func(raw []byte) error {
		_, err := parseCommitted(raw)
		return err
	})
}

// appendDurableLine repairs an unterminated tail, then appends one
// newline-terminated record and fsyncs the file and its directory. A
// newline-terminated corrupt record is left in place and returned.
func appendDurableLine(path string, line []byte, committed func([]byte) error) error {
	if len(line) == 0 || line[len(line)-1] != '\n' {
		return errors.New("journal line is not terminated")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := repairDurableTail(path, committed); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
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
	return syncDir(filepath.Dir(path))
}

// repairDurableTail discards only an unterminated suffix. Bytes through the
// last newline are a committed prefix and are not rewritten. A corrupt
// newline-terminated record is returned so a later append cannot hide it.
func repairDurableTail(path string, committed func([]byte) error) error {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := committed(raw); err != nil {
		return err
	}
	if raw[len(raw)-1] == '\n' {
		return nil
	}
	keep := 0
	if i := bytes.LastIndexByte(raw, '\n'); i >= 0 {
		keep = i + 1
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	terr := f.Truncate(int64(keep))
	serr := f.Sync()
	cerr := f.Close()
	if terr != nil {
		return terr
	}
	if serr != nil {
		return serr
	}
	if cerr != nil {
		return cerr
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
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

// Maintenance returns completed restores that still owe cleanup of a
// replaced database. Those records stay out of Incomplete so they do not
// block admission or startup.
func (j *Journal) Maintenance() ([]journalRecord, error) {
	all, err := j.read()
	if err != nil {
		return nil, err
	}
	var out []journalRecord
	for _, rec := range all {
		if rec.needsMaintenance() {
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
	raw, err := os.ReadFile(j.path)
	if os.IsNotExist(err) {
		return map[string]journalRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseCommitted(raw)
}

// parseCommitted returns newline-terminated records. Bytes after the last
// newline are an uncommitted tail: valid JSON without a newline is not a
// committed record and is ignored here. A newline-terminated line that is
// not valid JSON fails closed.
func parseCommitted(raw []byte) (map[string]journalRecord, error) {
	out := map[string]journalRecord{}
	if len(raw) == 0 {
		return out, nil
	}
	body := raw
	if raw[len(raw)-1] != '\n' {
		i := bytes.LastIndexByte(raw, '\n')
		if i < 0 {
			return out, nil
		}
		body = raw[:i+1]
	}
	parts := bytes.Split(body, []byte("\n"))
	for _, part := range parts[:len(parts)-1] {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(part, &rec); err != nil || rec.Record.ID == "" {
			return nil, errJournalCorrupt
		}
		out[rec.Record.ID] = rec
	}
	return out, nil
}

package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// updateIntent is the durable update and migration lifecycle. It is written
// before Docker creates or starts a container and is reread after a restart.
type updateIntent struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	Phase         string    `json:"phase"`
	MigrationName string    `json:"migration_name,omitempty"`
	MigrationID   string    `json:"migration_id,omitempty"`
	MigrationRan  string    `json:"migration_ran,omitempty"`
	CurrentID     string    `json:"current_id,omitempty"`
	CurrentName   string    `json:"current_name,omitempty"`
	NextName      string    `json:"next_name,omitempty"`
	NextID        string    `json:"next_id,omitempty"`
	SwitchTo      string    `json:"switch_to,omitempty"`
	Cleanup       string    `json:"cleanup,omitempty"`
	BackupID      string    `json:"backup_id,omitempty"`
	Started       time.Time `json:"started"`
	Finished      time.Time `json:"finished,omitempty"`
}

func (r updateIntent) terminal() bool {
	if r.Cleanup != "" {
		return false
	}
	return r.Status == "completed" || r.Status == "failed"
}

// UpdateJournal is the append-only update lifecycle beside the restore journal.
type UpdateJournal struct {
	path string
	now  func() time.Time
}

func OpenUpdateJournal(path string) *UpdateJournal {
	return &UpdateJournal{path: path, now: time.Now}
}

func (j *UpdateJournal) Put(rec updateIntent) error {
	if j == nil || j.path == "" {
		return errors.New("update journal is not configured")
	}
	if rec.Started.IsZero() && j.now != nil {
		rec.Started = j.now().UTC()
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	line = append(line, '\n')
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
	df, err := os.Open(filepath.Dir(j.path))
	if err != nil {
		return err
	}
	defer df.Close()
	return df.Sync()
}

func (j *UpdateJournal) Latest(id string) (updateIntent, bool, error) {
	all, err := j.read()
	if err != nil {
		return updateIntent{}, false, err
	}
	rec, ok := all[id]
	return rec, ok, nil
}

func (j *UpdateJournal) Incomplete() ([]updateIntent, error) {
	all, err := j.read()
	if err != nil {
		return nil, err
	}
	var out []updateIntent
	for _, rec := range all {
		if !rec.terminal() {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (j *UpdateJournal) read() (map[string]updateIntent, error) {
	if j == nil || j.path == "" {
		return map[string]updateIntent{}, nil
	}
	raw, err := os.ReadFile(j.path)
	if os.IsNotExist(err) {
		return map[string]updateIntent{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]updateIntent{}
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
		var rec updateIntent
		if err := json.Unmarshal(part, &rec); err != nil || rec.ID == "" {
			return nil, errJournalCorrupt
		}
		out[rec.ID] = rec
	}
	return out, nil
}

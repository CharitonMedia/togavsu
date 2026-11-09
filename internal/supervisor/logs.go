package supervisor

import (
	"os"
	"path/filepath"
	"time"
)

// PruneLogFiles removes supervisor log files older than retention.
// The default retention is 400 days. Docker stdout retention is a separate
// host setting and is not shortened here.
func PruneLogFiles(dir string, now time.Time, retention time.Duration) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-retention)
	n := 0
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			return n, err
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, ent.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

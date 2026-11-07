package db

import "fmt"

// MigrationRange is a block of schema_migrations versions reserved for one task.
// Core rows use owner "core". The task name is who may add a version, not the owner column.
type MigrationRange struct {
	Task string
	Min  int
	Max  int
}

// MigrationRanges is the shared allocation. Versions outside these blocks are rejected.
func MigrationRanges() []MigrationRange {
	return []MigrationRange{
		{Task: "contracts", Min: 1, Max: 99},
		{Task: "identity", Min: 100, Max: 199},
		{Task: "practice", Min: 200, Max: 299},
		{Task: "billing", Min: 300, Max: 399},
		{Task: "trust", Min: 400, Max: 499},
		{Task: "calendar", Min: 500, Max: 599},
		{Task: "files", Min: 600, Max: 699},
		{Task: "editor", Min: 700, Max: 799},
		{Task: "doc-tender", Min: 800, Max: 899},
		{Task: "ui", Min: 900, Max: 999},
		{Task: "modules", Min: 1000, Max: 1099},
		{Task: "marketplace", Min: 1100, Max: 1199},
		{Task: "release", Min: 1200, Max: 1299},
	}
}

// ValidateMigrationVersions checks that ranges do not overlap and each migration sits in one range.
func ValidateMigrationVersions(ms []Migration) error {
	ranges := MigrationRanges()
	for i, a := range ranges {
		if a.Min < 1 || a.Min > a.Max || a.Task == "" {
			return fmt.Errorf("%w: bad range %s", ErrVersion, a.Task)
		}
		for _, b := range ranges[i+1:] {
			if a.Max >= b.Min && b.Max >= a.Min {
				return fmt.Errorf("%w: %s overlaps %s", ErrVersion, a.Task, b.Task)
			}
		}
	}
	for _, m := range ms {
		hits := 0
		for _, r := range ranges {
			if m.Version >= r.Min && m.Version <= r.Max {
				hits++
			}
		}
		if hits != 1 {
			return fmt.Errorf("%w: version %d is outside the allocated ranges", ErrVersion, m.Version)
		}
	}
	return nil
}

// TaskForVersion returns the task that owns a version number.
func TaskForVersion(version int) (string, bool) {
	for _, r := range MigrationRanges() {
		if version >= r.Min && version <= r.Max {
			return r.Task, true
		}
	}
	return "", false
}

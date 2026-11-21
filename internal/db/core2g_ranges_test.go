package db

import "testing"

func TestCoreUpdate2GMigrationAllocation(t *testing.T) {
	if task, ok := TaskForVersion(1551); !ok || task != "core-update-2d" {
		t.Fatalf("1551 owner=%s", task)
	}
	if task, ok := TaskForVersion(1600); !ok || task != "core-update-2g" {
		t.Fatalf("1600 must belong to core-update-2g, got %q", task)
	}
}

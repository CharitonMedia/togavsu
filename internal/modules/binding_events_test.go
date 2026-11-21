package modules

import "testing"

func TestBindingEventUUID(t *testing.T) {
	// The binding contract accepts UUIDs generally, not just core's UUIDv7 IDs.
	for _, id := range []string{"d210ad4a-3c92-4ed1-9684-8fb164155760", "D210AD4A-3C92-4ED1-9684-8FB164155760", "019b6968-4600-7001-8000-000000000001", "00000000-0000-0000-0000-000000000000", "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		if !validEventUUID(id) {
			t.Errorf("valid UUID refused: %s", id)
		}
	}
	for _, id := range []string{"", "not-a-uuid", "d210ad4a3c924ed196848fb164155760", "d210ad4a-3c92-4ed1-9684-8fb16415576z", "d210ad4a-3c92-4ed1-9684-8fb164155760 "} {
		if validEventUUID(id) {
			t.Errorf("invalid UUID accepted: %s", id)
		}
	}
}

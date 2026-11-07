package supervisor

import "testing"

func TestPlatformAcceptanceStaysPending(t *testing.T) {
	got := PlatformAcceptance()
	if len(got) != 3 {
		t.Fatalf("statuses %d", len(got))
	}
	for _, item := range got {
		if item.Status != "pending" || item.BlockedBy == "" {
			t.Fatalf("%+v", item)
		}
	}
	if got[0].ID != "AC-001" || got[1].ID != "AC-016" || got[2].ID != "AC-018" {
		t.Fatalf("%+v", got)
	}
}

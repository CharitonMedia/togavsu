package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestTrustDepositFeedAttribution covers the P2 fix: a staff trust deposit lands in
// core.events (Activities feed) attributed to that staff user with a readable summary,
// not "System — trust.entry_posted".
func TestTrustDepositFeedAttribution(t *testing.T) {
	f := newFin(t)
	matter, clientID := f.matter("Feed Trust Estate", "feed.trust@example.com", nil)
	staffID := str(t, f.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")

	entryID := f.post(f.staff, matter, clientID, "deposit", 250_00)

	feed := f.staff.must(http.StatusOK, "GET", "/api/v1/events?matter_id="+matter+"&limit=50", nil)
	rows, _ := feed.json["data"].([]any)
	var found map[string]any
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["action"] == "trust.entry_posted" && str(t, row, "record_id") == entryID {
			found = row
			break
		}
	}
	if found == nil {
		t.Fatalf("no trust.entry_posted for %s in feed: %s", entryID, feed.body)
	}
	if found["actor_kind"] != "staff" {
		t.Fatalf("actor_kind: got %v want staff", found["actor_kind"])
	}
	if str(t, found, "actor_id") != staffID {
		t.Fatalf("actor_id: got %v want %s", found["actor_id"], staffID)
	}
	summary := str(t, found, "summary")
	if summary == "trust.entry_posted" || strings.Contains(summary, "trust.entry_posted") {
		t.Fatalf("summary still raw event name: %q", summary)
	}
	if !strings.Contains(summary, "Trust deposit") || !strings.Contains(summary, "$250.00") ||
		!strings.Contains(summary, "Feed Trust Estate") {
		t.Fatalf("summary not readable deposit line: %q", summary)
	}
}

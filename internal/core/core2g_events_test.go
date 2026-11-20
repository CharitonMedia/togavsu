package core

import "testing"

func TestCoreUpdate2GReservedActions(t *testing.T) {
	for _, action := range []string{"contact.phone_changed", "matter.party_updated", "practice_area.updated", "county.updated", "referral_source.updated", "contact.email_updated", "contact.address_updated", "trust.entry_reversed", "payment.future_event", "core.future_event", "calendar.future_event"} {
		if err := (eventWrite{Action: action, RecordType: "synthetic", RecordID: "synthetic", Summary: "Synthetic event"}).validate(""); err == nil {
			t.Errorf("accepted core namespace: %s", action)
		}
	}
	if err := (eventWrite{Action: "synthetic.completed", RecordType: "synthetic", RecordID: "synthetic", Summary: "Synthetic event"}).validate(""); err != nil {
		t.Fatal(err)
	}
}

func TestCoreUpdate2GReservedAppends(t *testing.T) {
	x := newExt(t)
	for _, action := range []string{"contact.phone_changed", "matter.party_updated", "practice_area.updated", "county.updated", "referral_source.updated", "contact.email_updated", "contact.address_updated", "trust.entry_reversed", "payment.future_event"} {
		body := map[string]any{"action": action, "record_type": "synthetic", "record_id": "synthetic", "summary": "Synthetic event"}
		for _, path := range []string{"/api/v1/events", "/api/v1/events:bulk"} {
			var in any = body
			if path == "/api/v1/events:bulk" {
				in = map[string]any{"items": []any{body}}
			}
			r := x.staff.do("POST", path, in)
			wantCode := "validation"
			if path == "/api/v1/events:bulk" {
				wantCode = "batch_rejected"
			}
			if r.code != map[string]int{"validation": 400, "batch_rejected": 422}[wantCode] || r.json["code"] != wantCode {
				t.Errorf("reserved %s on %s: %d %s", action, path, r.code, r.body)
			}
		}
	}
}

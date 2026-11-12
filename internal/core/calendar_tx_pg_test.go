package core

import (
	"net/http"
	"testing"
	"time"
)

// TestCalendarWritesAreOneTransaction covers the two halves of a calendar write that used
// to commit separately on Postgres: each item of a bulk save, and the activity row that
// follows a single save. A database failure partway (forced here by a trigger) leaves no
// calendar row, audit row or timeline row behind, and a retry with the same
// Idempotency-Key writes each item exactly once.
func TestCalendarWritesAreOneTransaction(t *testing.T) {
	d := newDocs(t)
	open, _ := d.matter("Rivera Dissolution", "", nil)
	super := d.superuser()
	exec := func(q string) {
		t.Helper()
		if _, err := super.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// One function per table: plpgsql doesn't short-circuit, so a field the other table
	// lacks would fail every insert.
	exec(`CREATE FUNCTION core.calendar_tx_test_save() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.title = 'Breaks the save' THEN
		RAISE EXCEPTION 'synthetic calendar write failure';
	END IF;
	RETURN NEW;
END $$`)
	exec(`CREATE FUNCTION core.calendar_tx_test_timeline() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.summary = 'Breaks the timeline' THEN
		RAISE EXCEPTION 'synthetic activity write failure';
	END IF;
	RETURN NEW;
END $$`)
	exec(`CREATE TRIGGER calendar_tx_test BEFORE INSERT ON core.calendar_events FOR EACH ROW EXECUTE FUNCTION core.calendar_tx_test_save()`)
	exec(`CREATE TRIGGER calendar_tx_test BEFORE INSERT ON core.events FOR EACH ROW EXECUTE FUNCTION core.calendar_tx_test_timeline()`)

	day := time.Now().UTC().AddDate(0, 0, 7)
	at := func(hour int) string {
		return time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	event := func(title string, hour int) map[string]any {
		return map[string]any{"title": title, "kind": "client_meeting", "starts_at": at(hour), "ends_at": at(hour + 1),
			"matter_id": open, "attendee_user_ids": []string{d.attyID}}
	}
	type counts struct{ events, deadlines, audit, timeline int }
	snap := func() counts {
		return counts{
			events:    d.count(`SELECT COUNT(*) FROM calendar_events`),
			deadlines: d.count(`SELECT COUNT(*) FROM deadlines`),
			audit:     d.count(`SELECT COUNT(*) FROM audit_log WHERE entity IN ('calendar_event', 'deadline')`),
			timeline:  d.count(`SELECT COUNT(*) FROM events WHERE action LIKE 'calendar_event.%' OR action LIKE 'deadline.%'`),
		}
	}
	unchanged := func(what string, before counts) {
		t.Helper()
		if now := snap(); now != before {
			t.Fatalf("%s left rows behind: %+v, was %+v", what, now, before)
		}
	}

	// Bulk events: the second item fails in the database after the first was written.
	before := snap()
	batch := map[string]any{"items": []any{event("First consult", 9), event("Breaks the save", 11)}}
	if r := d.staff.do("POST", "/api/v1/calendar-events:bulk", batch, "Idempotency-Key", "bulk-1"); r.code < http.StatusInternalServerError {
		t.Fatalf("bulk with a failing item: %d %s", r.code, r.body)
	}
	unchanged("a bulk save whose second item failed", before)

	// A single save whose timeline row fails keeps neither the event nor its audit rows.
	if r := d.staff.do("POST", "/api/v1/calendar-events", event("Breaks the timeline", 13)); r.code < http.StatusInternalServerError {
		t.Fatalf("event with a failing activity row: %d %s", r.code, r.body)
	}
	unchanged("a save whose activity row failed", before)

	// Bulk deadlines: the second item's timeline row fails.
	due := day.Format("2006-01-02")
	deadlines := map[string]any{"items": []any{
		map[string]any{"matter_id": open, "title": "Answer due", "due_on": due, "responsible_user_id": d.attyID},
		map[string]any{"matter_id": open, "title": "Breaks the timeline", "due_on": due, "responsible_user_id": d.attyID},
	}}
	if r := d.staff.do("POST", "/api/v1/deadlines:bulk", deadlines, "Idempotency-Key", "bulk-2"); r.code < http.StatusInternalServerError {
		t.Fatalf("bulk deadlines with a failing activity row: %d %s", r.code, r.body)
	}
	unchanged("a bulk deadline save whose second activity row failed", before)

	// With the database healthy again, the same retries write each item once.
	exec(`DROP TRIGGER calendar_tx_test ON core.calendar_events`)
	exec(`DROP TRIGGER calendar_tx_test ON core.events`)
	exec(`DROP FUNCTION core.calendar_tx_test_save()`)
	exec(`DROP FUNCTION core.calendar_tx_test_timeline()`)
	for _, retry := range []struct {
		path, key string
		body      map[string]any
	}{{"/api/v1/calendar-events:bulk", "bulk-1", batch}, {"/api/v1/deadlines:bulk", "bulk-2", deadlines}} {
		if r := d.staff.do("POST", retry.path, retry.body, "Idempotency-Key", retry.key); r.code/100 != 2 {
			t.Fatalf("retry %s: %d %s", retry.path, r.code, r.body)
		}
	}
	now := snap()
	if now.events != before.events+2 || now.deadlines != before.deadlines+2 || now.timeline != before.timeline+4 || now.audit <= before.audit {
		t.Fatalf("retries wrote %+v, was %+v; want two events, two deadlines and four timeline rows", now, before)
	}
}

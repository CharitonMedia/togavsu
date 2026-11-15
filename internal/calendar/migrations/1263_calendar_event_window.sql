-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). Event lists read only the
-- events that can overlap the requested window (Postgres.EventsBetween) and only those
-- events' attendees, instead of every event and attendee row.
CREATE INDEX calendar_events_starts_idx ON calendar_events (starts_at, id);
CREATE INDEX calendar_event_attendees_event_idx ON calendar_event_attendees (event_id);

-- +goose Down
DROP INDEX calendar_event_attendees_event_idx;
DROP INDEX calendar_events_starts_idx;

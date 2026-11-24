-- +goose Up
-- Nullable whole-object storage preserves old events during rolling upgrades.
ALTER TABLE core.calendar_events ADD COLUMN video TEXT;

-- +goose Down
ALTER TABLE core.calendar_events DROP COLUMN video;

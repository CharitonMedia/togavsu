-- +goose Up
ALTER TABLE core.events ADD COLUMN module_id TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Expand-only: attribution is retained on downgrade.

-- +goose Up
-- One-time CORE-36 backfill marker (core-update-2a 1400-1449). RebuildCoreRows inserts
-- a row after the first successful rewrite; later starts skip the full rewrite.
CREATE TABLE IF NOT EXISTS billing_core_rows_backfill (
	done_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS billing_core_rows_backfill;

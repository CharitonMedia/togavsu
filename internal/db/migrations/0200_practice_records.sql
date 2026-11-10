-- +goose Up
-- practice migration range 200-299
-- practice_records holds every practice row as one JSON document per (kind, id).
-- The core loads it at start and writes each practice mutation through in one transaction.
-- seq keeps append order for timeline, audit, close and conflict-run rows. The core assigns it.
-- ponytail: ceiling: one core process owns these rows; section 5 relational tables are not yet the practice source of truth. See PONYTAIL-DEBT.md.
CREATE TABLE practice_records (
  kind TEXT NOT NULL,
  id TEXT NOT NULL,
  seq BIGINT NOT NULL,
  body TEXT NOT NULL,
  PRIMARY KEY (kind, id)
);
CREATE INDEX practice_records_seq ON practice_records (seq);

-- +goose Down
DROP TABLE practice_records;

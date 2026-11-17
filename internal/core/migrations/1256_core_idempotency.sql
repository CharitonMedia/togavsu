-- +goose Up
-- release migration range 1240-1259 (release-api-coverage).
-- Idempotency-Key results for the core's own write routes (events, AI provider keys and
-- checks, email changes). One row per caller, operation and key; the response is stored
-- whole and replayed for api.idempotency_retention_hours.
CREATE TABLE core_idempotency (
  actor TEXT NOT NULL,
  operation TEXT NOT NULL,
  idem_key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  status INTEGER NOT NULL,
  response TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (actor, operation, idem_key)
);

-- The Activities feed (listEvents) reads the stream newest first.
CREATE INDEX events_created_idx ON events (created_at, id);

-- +goose Down
DROP INDEX events_created_idx;
DROP TABLE core_idempotency;

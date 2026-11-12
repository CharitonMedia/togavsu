-- +goose Up
-- calendar migration range 500-599
-- Events, attendees and deadlines use the section 5 tables from migration 0001. This adds
-- what the calendar keeps beside them: revocable iCal feeds (only the secret's SHA-256 is
-- stored), the reminder sends already claimed, in-app reminder notices, and stored
-- Idempotency-Key responses.
CREATE TABLE ical_feeds (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  owner_user_id TEXT REFERENCES users (id) ON DELETE RESTRICT,
  secret_hash TEXT NOT NULL UNIQUE,
  revoked INTEGER NOT NULL CHECK (revoked IN (0, 1)),
  revoked_at BIGINT NOT NULL,
  revoked_by TEXT
);

CREATE TABLE calendar_reminders_sent (
  reminder_key TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL
);

CREATE TABLE calendar_notices (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  path TEXT NOT NULL,
  matter_id TEXT,
  record_id TEXT,
  emailed INTEGER NOT NULL CHECK (emailed IN (0, 1))
);
CREATE INDEX calendar_notices_user ON calendar_notices (user_id, created_at);

CREATE TABLE calendar_idempotency (
  idem_key TEXT PRIMARY KEY,
  request_hash TEXT NOT NULL,
  status INTEGER NOT NULL,
  body TEXT NOT NULL
);

-- +goose Down
DROP TABLE calendar_idempotency;
DROP TABLE calendar_notices;
DROP TABLE calendar_reminders_sent;
DROP TABLE ical_feeds;

-- +goose Up
-- trust migration range 400-499.
--
-- Matters and contacts live in practice_records (migration 200), not in the section 5
-- matters and contacts tables, so the trust tables cannot reference those rows by
-- foreign key. The trust service checks that the matter exists and that the client is a
-- client party on it, inside the posting transaction.
-- ponytail: ceiling: matter and client ids on trust rows are checked by the service, not by
-- a foreign key. Upgrade path: restore the foreign keys in a later trust migration once
-- practice writes the relational matters and contacts tables. Trigger: practice makes
-- the section 5 tables its source of truth.
ALTER TABLE trust_ledger DROP CONSTRAINT trust_ledger_matter_id_fkey;
ALTER TABLE trust_ledger DROP CONSTRAINT trust_ledger_client_id_fkey;
ALTER TABLE trust_balance_cache DROP CONSTRAINT trust_balance_cache_matter_id_fkey;
ALTER TABLE trust_balance_cache DROP CONSTRAINT trust_balance_cache_client_id_fkey;
ALTER TABLE trust_flags DROP CONSTRAINT trust_flags_matter_id_fkey;
ALTER TABLE trust_flags DROP CONSTRAINT trust_flags_client_id_fkey;
ALTER TABLE trust_replenishment_requests DROP CONSTRAINT trust_replenishment_requests_matter_id_fkey;
ALTER TABLE trust_replenishment_requests DROP CONSTRAINT trust_replenishment_requests_client_id_fkey;
ALTER TABLE trust_replenishment_requests DROP CONSTRAINT trust_replenishment_requests_invoice_id_fkey;

-- row_guard is the one ledger column the owner keeps UPDATE on (InstallPrivileges).
-- Postgres needs UPDATE on at least one column of trust_ledger to run the FOR KEY SHARE
-- check behind trust_flags.trust_entry_id, trust_ledger.reverses_id and
-- payments.trust_entry_id. The CHECK pins it to 0, so no UPDATE can change a row.
ALTER TABLE trust_ledger ADD COLUMN row_guard INTEGER NOT NULL DEFAULT 0 CHECK (row_guard = 0);
CREATE UNIQUE INDEX trust_ledger_one_reversal ON trust_ledger (reverses_id);
CREATE INDEX trust_ledger_account_idx ON trust_ledger (trust_account_id);

CREATE UNIQUE INDEX trust_balance_cache_owner ON trust_balance_cache (trust_account_id, client_id, matter_id);

-- A flag is about a matter balance, a client balance, or an account balance.
ALTER TABLE trust_flags ADD COLUMN scope TEXT NOT NULL DEFAULT 'matter' CHECK (scope IN ('matter', 'client', 'account'));
ALTER TABLE trust_flags ADD COLUMN trust_account_id TEXT REFERENCES trust_accounts (id) ON DELETE RESTRICT;
ALTER TABLE trust_flags ADD COLUMN balance_cents BIGINT NOT NULL DEFAULT 0;

-- Evergreen minimum per matter. No row means the trust.evergreen_minimum_cents default.
CREATE TABLE trust_evergreen (
  matter_id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  minimum_cents BIGINT NOT NULL CHECK (minimum_cents >= 0)
);

-- A flat fee moved from trust to operating. Collected while its ledger row is not reversed.
CREATE TABLE trust_flat_fee_applications (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL,
  trust_entry_id TEXT NOT NULL REFERENCES trust_ledger (id) ON DELETE RESTRICT,
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0)
);
CREATE INDEX trust_flat_fee_matter_idx ON trust_flat_fee_applications (matter_id);

-- Section 8.9 notifications (user, kind, title, link, read at). read_at 0 is unread.
CREATE TABLE notifications (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  link TEXT NOT NULL,
  matter_id TEXT,
  read_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX notifications_user_idx ON notifications (user_id, read_at);

-- System mail queued in the same transaction as the write that caused it.
CREATE TABLE mail_outbox (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  kind TEXT NOT NULL,
  record_id TEXT NOT NULL,
  to_address TEXT NOT NULL,
  subject TEXT NOT NULL,
  body TEXT NOT NULL,
  sent_at BIGINT NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX mail_outbox_unsent_idx ON mail_outbox (sent_at);

-- Idempotency-Key replays for trust writes. The response is stored whole.
CREATE TABLE trust_idempotency (
  actor_id TEXT NOT NULL,
  operation TEXT NOT NULL,
  idem_key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  response TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  PRIMARY KEY (actor_id, operation, idem_key)
);

-- +goose Down
DROP TABLE trust_idempotency;
DROP TABLE mail_outbox;
DROP TABLE notifications;
DROP TABLE trust_flat_fee_applications;
DROP TABLE trust_evergreen;

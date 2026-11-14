-- +goose Up
-- release migration range 1200-1299 (AC-017 performance). Billing reads by matter, user, date and invoice through plain indexed columns
-- beside each document instead of reading every row of a kind. Put writes them with the
-- body; Postgres.IndexRows fills rows stored before this migration (indexed = 0) at start.
-- Plain TEXT columns and B-tree indexes only, so the SQLite dialect can carry them.
ALTER TABLE billing_records ADD COLUMN matter_id TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_records ADD COLUMN user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_records ADD COLUMN on_date TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_records ADD COLUMN invoice_id TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_records ADD COLUMN indexed INTEGER NOT NULL DEFAULT 0 CHECK (indexed IN (0, 1));
CREATE INDEX billing_records_matter_idx ON billing_records (kind, matter_id, id);
CREATE INDEX billing_records_user_idx ON billing_records (kind, user_id, on_date, id);
CREATE INDEX billing_records_invoice_idx ON billing_records (kind, invoice_id, id);
CREATE INDEX billing_records_date_idx ON billing_records (kind, on_date DESC, id);
CREATE INDEX billing_records_unindexed_idx ON billing_records (kind, indexed, id);

-- +goose Down
DROP INDEX billing_records_unindexed_idx;
DROP INDEX billing_records_date_idx;
DROP INDEX billing_records_invoice_idx;
DROP INDEX billing_records_user_idx;
DROP INDEX billing_records_matter_idx;
ALTER TABLE billing_records DROP COLUMN indexed;
ALTER TABLE billing_records DROP COLUMN invoice_id;
ALTER TABLE billing_records DROP COLUMN on_date;
ALTER TABLE billing_records DROP COLUMN user_id;
ALTER TABLE billing_records DROP COLUMN matter_id;

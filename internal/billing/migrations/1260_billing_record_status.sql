-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). An invoice's or payment's
-- status and an invoice's due date beside the document, so the sidebar's draft count,
-- invoice lists by status and the overdue list read only matching rows instead of every
-- invoice header. Put writes them with the body; resetting indexed makes
-- Postgres.IndexRows fill them for invoices and payments stored before this migration.
ALTER TABLE billing_records ADD COLUMN status TEXT NOT NULL DEFAULT '';
ALTER TABLE billing_records ADD COLUMN due_on TEXT NOT NULL DEFAULT '';
CREATE INDEX billing_records_status_idx ON billing_records (kind, status, due_on, id);
UPDATE billing_records SET indexed = 0 WHERE kind IN ('invoice', 'payment');

-- +goose Down
DROP INDEX billing_records_status_idx;
ALTER TABLE billing_records DROP COLUMN due_on;
ALTER TABLE billing_records DROP COLUMN status;

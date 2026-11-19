-- +goose Up
-- CORE-37: opening invoice import columns on dual-written core.invoices / core.payments
-- and a firm-unique external_ref index for CSV re-runs.

ALTER TABLE invoices ADD COLUMN IF NOT EXISTS imported INTEGER NOT NULL DEFAULT 0 CHECK (imported IN (0, 1));
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS import_source TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS external_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS document_of_record_file_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS invoices_external_ref_uidx
  ON invoices (external_ref) WHERE external_ref <> '';

ALTER TABLE payments ADD COLUMN IF NOT EXISTS imported INTEGER NOT NULL DEFAULT 0 CHECK (imported IN (0, 1));

-- +goose Down
SELECT 1;

-- +goose Up
-- Pin opening invoices to immutable original file bytes. The Go backfill uses each
-- import's creation time, so an upload made after import cannot replace its PDF.
ALTER TABLE invoices ADD COLUMN document_of_record_version_id TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;

-- +goose Up
-- release migration range 1200-1299 (AC-017 performance). head keeps an invoice's JSON
-- without its lines, so invoice lists and AR aging read a few hundred bytes per invoice
-- instead of every line. Put writes it with the body; Postgres.IndexRows fills invoices
-- stored before this migration. Empty for every other kind.
ALTER TABLE billing_records ADD COLUMN head TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE billing_records DROP COLUMN head;

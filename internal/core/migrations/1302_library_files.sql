-- +goose Up
-- module-core range 1300-1399 (CORE-17)
-- Firm library files: no matter or contact, skipped by Doc Tender.
ALTER TABLE files ADD COLUMN library BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE file_uploads ADD COLUMN library BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
SELECT 1;

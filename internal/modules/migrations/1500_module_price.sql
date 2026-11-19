-- +goose Up
-- CORE-35: structured price columns on the module record. The core shows and stores
-- the manifest price; it never charges, checks or enforces it.

ALTER TABLE modules ADD COLUMN IF NOT EXISTS price_amount_cents BIGINT NOT NULL DEFAULT 0;
ALTER TABLE modules ADD COLUMN IF NOT EXISTS price_currency TEXT NOT NULL DEFAULT '';
ALTER TABLE modules ADD COLUMN IF NOT EXISTS price_interval TEXT NOT NULL DEFAULT '';
ALTER TABLE modules ADD COLUMN IF NOT EXISTS price_note TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;

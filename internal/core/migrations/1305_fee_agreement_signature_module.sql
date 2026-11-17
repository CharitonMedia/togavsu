-- +goose Up
-- module-core range 1300-1399 (CORE-25); 1300-1304 used
-- Module that moved a fee agreement to sent may service-call it to signed.
ALTER TABLE fee_agreements ADD COLUMN signature_module_id TEXT NOT NULL DEFAULT '';

-- +goose Down
SELECT 1;

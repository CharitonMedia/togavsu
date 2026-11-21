-- +goose Up
-- CORE-23/31 receipt continuation can record a payment on a newly created
-- contact before a matter exists. Existing matter-bound payments retain their FK.
ALTER TABLE payments ALTER COLUMN matter_id DROP NOT NULL;

-- +goose Down
-- Expand-only: retain nullable matter_id on downgrade, including every contact-only
-- payment. Restoring NOT NULL would fail with those immutable money records.

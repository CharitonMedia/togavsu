-- +goose Up
-- CORE-23/31 receipt continuation can record a payment on a newly created
-- contact before a matter exists. Existing matter-bound payments retain their FK.
ALTER TABLE payments ALTER COLUMN matter_id DROP NOT NULL;

-- +goose Down
-- Refuse rollback while contact-only payments exist; never discard money records.
ALTER TABLE payments ALTER COLUMN matter_id SET NOT NULL;

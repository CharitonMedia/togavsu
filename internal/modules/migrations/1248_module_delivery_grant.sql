-- +goose Up
-- release range 1200-1299 (release-api-coverage 1248-1251)
-- task_authorization is the opaque grant a delivery carries in X-Togavsu-Task-Authorization
-- (payment.processor_authorized's processor grant), sealed with the module secret key.
-- Empty for an ordinary event delivery. Expand only: an older core ignores the column.
ALTER TABLE module_deliveries ADD COLUMN task_authorization TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE module_deliveries DROP COLUMN task_authorization;

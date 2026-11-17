-- +goose Up
-- module-core range 1300-1399 (CORE-28)
-- Device grants: a staff user connects a device to one module; the module exchanges
-- the secret for a short-lived context token. Grants lapse after 90 days unused or
-- when the user is deactivated.
CREATE TABLE module_devices (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id),
  module_id TEXT NOT NULL REFERENCES modules (id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '',
  secret_hash TEXT NOT NULL,
  last_used_at BIGINT NOT NULL,
  revoked_at BIGINT,
  CONSTRAINT module_devices_secret_hash_unique UNIQUE (secret_hash)
);
CREATE INDEX module_devices_user_idx ON module_devices (user_id) WHERE revoked_at IS NULL;
CREATE INDEX module_devices_module_idx ON module_devices (module_id) WHERE revoked_at IS NULL;

-- +goose Down
SELECT 1;

-- +goose Up
-- module-core range 1300-1399 (CORE-06)
-- Sealed secrets owned by one module; the module's bearer reads and writes them.
CREATE TABLE module_secrets (
  module_id TEXT NOT NULL REFERENCES modules (id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  value TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  PRIMARY KEY (module_id, name),
  CONSTRAINT module_secrets_name_shape CHECK (name ~ '^[a-z0-9/_-]{1,128}$')
);

-- +goose Down
DROP TABLE module_secrets;

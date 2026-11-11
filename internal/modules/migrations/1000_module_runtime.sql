-- +goose Up
-- modules migration range 1000-1099
-- Module runtime state (PRD 9). Module records themselves live in each module's own
-- schema (mod_<id>); these core tables only track installs, approvals, signing keys,
-- packages, settings and event deliveries. Timestamps are unix microseconds UTC.

CREATE TABLE modules (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  version TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('awaiting_approval', 'installed', 'disabled', 'uninstalling', 'removed', 'cancelled')),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  source TEXT NOT NULL CHECK (source IN ('local', 'marketplace')),
  package_id TEXT NOT NULL,
  pending_package_id TEXT NOT NULL,
  token_id TEXT NOT NULL,
  token_secret TEXT NOT NULL,
  webhook_secret TEXT NOT NULL,
  db_role TEXT NOT NULL,
  db_password TEXT NOT NULL,
  schema_state TEXT NOT NULL CHECK (schema_state IN ('none', 'active', 'locked', 'dropped')),
  last_error TEXT NOT NULL
);

CREATE TABLE module_packages (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  module_id TEXT NOT NULL,
  version TEXT NOT NULL,
  digest TEXT NOT NULL,
  key_id TEXT NOT NULL,
  manifest TEXT NOT NULL,
  archive TEXT NOT NULL
);

CREATE TABLE module_permissions (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  module_id TEXT NOT NULL,
  scope TEXT NOT NULL,
  required INTEGER NOT NULL CHECK (required IN (0, 1)),
  decision TEXT NOT NULL CHECK (decision IN ('pending', 'approved', 'declined')),
  decided_by TEXT NOT NULL,
  decided_at BIGINT NOT NULL
);

CREATE TABLE module_settings (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  module_id TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL
);

CREATE TABLE module_deliveries (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  module_id TEXT NOT NULL,
  event_id TEXT NOT NULL,
  action TEXT NOT NULL,
  payload TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
  attempts INTEGER NOT NULL,
  next_attempt_at BIGINT NOT NULL,
  last_error TEXT NOT NULL,
  delivered_at BIGINT NOT NULL
);

CREATE TABLE module_event_cursor (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  last_created_at BIGINT NOT NULL,
  last_event_id TEXT NOT NULL
);

CREATE UNIQUE INDEX module_permissions_scope_uidx ON module_permissions (module_id, scope);
CREATE UNIQUE INDEX module_settings_key_uidx ON module_settings (module_id, key);
CREATE UNIQUE INDEX module_deliveries_event_uidx ON module_deliveries (module_id, event_id);
CREATE INDEX module_deliveries_due_idx ON module_deliveries (status, next_attempt_at);
CREATE INDEX module_packages_module_idx ON module_packages (module_id);

-- +goose Down
SELECT 1;

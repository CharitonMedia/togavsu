-- +goose Up
-- marketplace migration range 1100-1199 (internal/marketplace). Portable SQL.

-- Trusted local signing keys the firm admin adds (PRD 8.7, 9.3). A firm that patches a
-- module signs its build with one of these.
CREATE TABLE trusted_signing_keys (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  key_id TEXT NOT NULL UNIQUE,
  public_key_pem TEXT NOT NULL
);

-- The verified author keys the marketplace last published (PRD 9.3: keys published
-- through the marketplace are accepted signers). Kept so a local file install still
-- verifies while the marketplace is unreachable.
CREATE TABLE marketplace_signing_keys (
  key_id TEXT PRIMARY KEY,
  author TEXT NOT NULL,
  public_key_pem TEXT NOT NULL,
  fetched_at BIGINT NOT NULL
);

-- Flags and pulls on installed modules (PRD 10.5). source_alert_id is the
-- marketplace's alert id, so one alert is delivered once however often the core checks.
CREATE TABLE module_alerts (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  source_alert_id TEXT NOT NULL UNIQUE,
  module_id TEXT NOT NULL,
  version TEXT NOT NULL,
  installed_version TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('flag', 'pull')),
  note TEXT NOT NULL,
  warning TEXT NOT NULL,
  raised_at BIGINT NOT NULL
);
CREATE INDEX module_alerts_module_idx ON module_alerts (module_id, created_at);

-- One row: when the core last checked the marketplace and how that went.
CREATE TABLE marketplace_checks (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  attempted_at BIGINT NOT NULL,
  succeeded_at BIGINT NOT NULL,
  last_error TEXT NOT NULL
);

-- +goose Down
DROP TABLE marketplace_checks;
DROP TABLE module_alerts;
DROP TABLE marketplace_signing_keys;
DROP TABLE trusted_signing_keys;

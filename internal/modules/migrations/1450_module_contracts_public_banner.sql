-- +goose Up
-- core-update-2b migration range 1450-1499
-- CORE-31 contract links, CORE-32 public page registry, CORE-34 banner order setting.

CREATE TABLE module_contract_links (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contract TEXT NOT NULL,
  consumer_id TEXT NOT NULL,
  provider_id TEXT NOT NULL
);

CREATE UNIQUE INDEX module_contract_links_uidx
  ON module_contract_links (contract, consumer_id, provider_id);
CREATE INDEX module_contract_links_provider_idx
  ON module_contract_links (contract, provider_id);
CREATE INDEX module_contract_links_consumer_idx
  ON module_contract_links (consumer_id);

-- Declared public pages for installed modules (CORE-32). Refreshed on install/update.
CREATE TABLE module_public_pages (
  module_id TEXT NOT NULL,
  path TEXT NOT NULL,
  PRIMARY KEY (module_id, path)
);

-- Banner order is modules.staff_banner_order in the settings registry (CORE-34);
-- the catalog default is [] until an admin reorders.

-- +goose Down
SELECT 1;

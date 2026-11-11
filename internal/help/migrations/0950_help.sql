-- +goose Up
-- ui migration range 950-999 (internal/help).

-- The firm's help sections and items under the guides (PRD 8.11).
CREATE TABLE help_sections (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  title TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE help_items (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  section_id TEXT NOT NULL REFERENCES help_sections (id) ON DELETE CASCADE,
  title TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('text', 'video', 'image')),
  body TEXT NOT NULL DEFAULT '',
  file_id TEXT,
  sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX help_items_section_idx ON help_items (section_id, sort_order);

-- Module guide pages, each under its module's own heading. enabled follows the
-- module: disable hides its pages, uninstall deletes them.
CREATE TABLE help_module_pages (
  module_id TEXT NOT NULL,
  page_id TEXT NOT NULL,
  updated_at BIGINT NOT NULL,
  title TEXT NOT NULL,
  markdown TEXT NOT NULL,
  keywords TEXT NOT NULL,
  covers TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (module_id, page_id)
);

CREATE TABLE help_modules (
  module_id TEXT PRIMARY KEY,
  updated_at BIGINT NOT NULL,
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1))
);

-- +goose Down
DROP TABLE help_modules;
DROP TABLE help_module_pages;
DROP TABLE help_items;
DROP TABLE help_sections;

-- +goose Up
-- files migration range 600-699
-- Expands the section 5 file tables from migration 0001. Nothing here edits a file
-- version or rendition row: those stay append-only. What can change about a stored
-- object (where its bytes live now, whether a scanner quarantined it) is kept beside
-- the row, so the version itself never changes.

-- Accountants read only receipts and invoice PDFs (PRD 4). purged_* records an admin's
-- empty-from-trash; the record stays for the audit log. files_matter_idx and
-- file_versions_number_uidx come from migration 0001.
ALTER TABLE files ADD COLUMN financial_class TEXT NOT NULL DEFAULT 'general'
  CHECK (financial_class IN ('general', 'receipt', 'invoice_pdf'));
ALTER TABLE files ADD COLUMN purged_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE files ADD COLUMN purged_by TEXT REFERENCES users (id) ON DELETE RESTRICT;
CREATE INDEX files_contact_idx ON files (contact_id);
CREATE INDEX file_versions_sha_idx ON file_versions (sha256);
CREATE UNIQUE INDEX file_renditions_number_idx ON file_renditions (file_version_id, kind, number);
CREATE INDEX file_links_from_idx ON file_links (from_file_id);
CREATE INDEX file_links_to_idx ON file_links (to_file_id);

-- Matter folders are records only; files.folder holds a folder id or ''.
CREATE TABLE folders (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL,
  UNIQUE (matter_id, name)
);

-- The current home of a version's or rendition's bytes, written with every object and
-- replaced when a storage move repoints it. sealed records whether those bytes were
-- encrypted when written, so turning a backend's encryption off or on changes only new
-- bytes. A row that is absent means the backend and key on the version or rendition row.
CREATE TABLE file_object_locations (
  object_id TEXT PRIMARY KEY,
  object_kind TEXT NOT NULL CHECK (object_kind IN ('version', 'rendition')),
  storage_backend_id TEXT NOT NULL REFERENCES storage_backends (id) ON DELETE RESTRICT,
  storage_key TEXT NOT NULL,
  sealed INTEGER NOT NULL CHECK (sealed IN (0, 1)),
  updated_at BIGINT NOT NULL
);

-- Append-only quarantine marks from a scanning module. The newest mark wins.
CREATE TABLE file_quarantine_marks (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_version_id TEXT NOT NULL REFERENCES file_versions (id) ON DELETE RESTRICT,
  quarantined INTEGER NOT NULL CHECK (quarantined IN (0, 1)),
  source TEXT NOT NULL
);
CREATE INDEX file_quarantine_marks_version_idx ON file_quarantine_marks (file_version_id, created_at);

-- Resumable uploads. Received bytes sit in the core's staging directory under the
-- upload id until publish moves them to the default backend.
CREATE TABLE file_uploads (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  owner_user_id TEXT NOT NULL,
  owner_module_id TEXT NOT NULL,
  file_name TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  contact_id TEXT REFERENCES contacts (id) ON DELETE RESTRICT,
  media_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  declared_sha256 TEXT NOT NULL,
  chunk_bytes BIGINT NOT NULL,
  received_bytes BIGINT NOT NULL,
  sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('receiving', 'staged', 'published', 'expired')),
  expires_at BIGINT NOT NULL,
  file_id TEXT,
  version_id TEXT
);

-- One row per object a move copies, so a stopped move resumes where it stopped and
-- old copies are deleted only after every row checks out.
ALTER TABLE storage_moves ADD COLUMN progress_total INTEGER NOT NULL DEFAULT 0;
CREATE TABLE storage_move_items (
  move_id TEXT NOT NULL REFERENCES storage_moves (id) ON DELETE RESTRICT,
  object_id TEXT NOT NULL,
  object_kind TEXT NOT NULL CHECK (object_kind IN ('version', 'rendition')),
  from_backend_id TEXT NOT NULL,
  from_key TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'checked', 'old_deleted')),
  PRIMARY KEY (move_id, object_id)
);

-- A read whose bytes failed their SHA-256 or authentication.
CREATE TABLE storage_alerts (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  storage_backend_id TEXT NOT NULL,
  object_id TEXT NOT NULL,
  file_id TEXT NOT NULL,
  detail TEXT NOT NULL,
  emailed INTEGER NOT NULL CHECK (emailed IN (0, 1))
);

-- What Files search matches: the newest Markdown copy of each file, lowercased.
CREATE TABLE file_search_text (
  file_id TEXT PRIMARY KEY REFERENCES files (id) ON DELETE RESTRICT,
  rendition_id TEXT NOT NULL REFERENCES file_renditions (id) ON DELETE RESTRICT,
  body TEXT NOT NULL
);

-- +goose Down
DROP TABLE file_search_text;
DROP TABLE storage_alerts;
DROP TABLE storage_move_items;
ALTER TABLE storage_moves DROP COLUMN progress_total;
DROP TABLE file_uploads;
DROP TABLE file_quarantine_marks;
DROP TABLE file_object_locations;
DROP TABLE folders;
DROP INDEX file_links_to_idx;
DROP INDEX file_links_from_idx;
DROP INDEX file_renditions_number_idx;
DROP INDEX file_versions_sha_idx;
DROP INDEX files_contact_idx;
ALTER TABLE files DROP COLUMN purged_by;
ALTER TABLE files DROP COLUMN purged_at;
ALTER TABLE files DROP COLUMN financial_class;

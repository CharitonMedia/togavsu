-- +goose Up
-- release migration range 1200-1299 (AC-002, issueFilePublicationGrant). A background file
-- publication grant lets one installed module upload and publish files with no viewer
-- session: onto one matter it can already reach, or unassigned (no matter, for a later
-- review). authorization is the BackgroundFileAuthorization delivered to that module on
-- file.background_authorized, as issued. file_uploads.grant_id names the grant an upload
-- was created under, so publishing it is checked against the same grant.
CREATE TABLE file_publication_grants (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  module_id TEXT NOT NULL,
  assignment TEXT NOT NULL CHECK (assignment IN ('matter', 'unassigned')),
  matter_id TEXT,
  contact_id TEXT,
  queue TEXT NOT NULL,
  source TEXT NOT NULL CHECK (source IN ('email', 'zoom', 'importer', 'drop_folder')),
  expires_at BIGINT NOT NULL,
  revoked_at BIGINT NOT NULL,
  idempotency_key TEXT NOT NULL,
  request_hash TEXT NOT NULL,
  authorization_body TEXT NOT NULL,
  CHECK ((assignment = 'matter') = (matter_id IS NOT NULL))
);
CREATE INDEX file_publication_grants_module_idx ON file_publication_grants (module_id, assignment, matter_id);
CREATE UNIQUE INDEX file_publication_grants_idem_idx ON file_publication_grants (module_id, idempotency_key) WHERE idempotency_key <> '';
ALTER TABLE file_uploads ADD COLUMN grant_id TEXT REFERENCES file_publication_grants (id) ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE file_uploads DROP COLUMN grant_id;
DROP INDEX file_publication_grants_idem_idx;
DROP INDEX file_publication_grants_module_idx;
DROP TABLE file_publication_grants;

-- +goose Up
-- doc-tender migration range 800-899
-- Expands the section 5 Doc Tender tables from migration 0001 (PRD 8.21). Job and unit
-- rows are working state; results, renditions and resolved exceptions are kept.

-- A job processes one file version (kind file) or decides one photo grouping (kind
-- group). reason says why it was queued: arrival, sweep, reopen, retry, reprocess.
ALTER TABLE doc_tender_jobs ADD COLUMN file_id TEXT REFERENCES files (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_jobs ADD COLUMN matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_jobs ADD COLUMN kind TEXT NOT NULL DEFAULT 'file' CHECK (kind IN ('file', 'group'));
ALTER TABLE doc_tender_jobs ADD COLUMN reason TEXT NOT NULL DEFAULT 'arrival';
ALTER TABLE doc_tender_jobs ADD COLUMN held_since BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_jobs ADD COLUMN finished_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_jobs ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0;
-- A photo waiting for grouping names the group job that decides it.
ALTER TABLE doc_tender_jobs ADD COLUMN group_job_id TEXT NOT NULL DEFAULT '';
-- One unfinished job per version: a second arrival event for the same version joins it.
CREATE UNIQUE INDEX doc_tender_jobs_active_uidx ON doc_tender_jobs (file_version_id, kind)
  WHERE status NOT IN ('published', 'exception');
CREATE INDEX doc_tender_jobs_status_idx ON doc_tender_jobs (status, next_attempt_at);
CREATE INDEX doc_tender_jobs_file_idx ON doc_tender_jobs (file_id);

-- A unit is one page, photo, sheet, media window or step. Each is saved as it finishes,
-- so a stopped job resumes at the first unit without a passing check.
ALTER TABLE doc_tender_units ADD COLUMN ordinal INTEGER NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_units ADD COLUMN status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'passed', 'failed'));
ALTER TABLE doc_tender_units ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_units ADD COLUMN next_attempt_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_units ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE doc_tender_units ADD COLUMN check_method TEXT NOT NULL DEFAULT '';
ALTER TABLE doc_tender_units ADD COLUMN check_model TEXT NOT NULL DEFAULT '';
ALTER TABLE doc_tender_units ADD COLUMN started_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_units ADD COLUMN finished_at BIGINT NOT NULL DEFAULT 0;
ALTER TABLE doc_tender_units ADD COLUMN cost_cents BIGINT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX doc_tender_units_job_unit_uidx ON doc_tender_units (job_id, unit);

-- Results are inserted once per processed version; a reprocess adds a row.
ALTER TABLE doc_tender_results ADD COLUMN file_id TEXT REFERENCES files (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_results ADD COLUMN job_id TEXT REFERENCES doc_tender_jobs (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_results ADD COLUMN confidences TEXT NOT NULL DEFAULT '{}';
ALTER TABLE doc_tender_results ADD COLUMN markdown_rendition_id TEXT REFERENCES file_renditions (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_results ADD COLUMN suggested_matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT;
-- A category the check models disagreed on is not published: category stays null.
ALTER TABLE doc_tender_results ALTER COLUMN category DROP NOT NULL;
CREATE INDEX doc_tender_results_file_idx ON doc_tender_results (file_id, created_at);
-- name_set records that this result published the file's name; Doc Tender names a file once.
ALTER TABLE doc_tender_results ADD COLUMN name_set BOOLEAN NOT NULL DEFAULT false;

-- best_answer is what Doc Tender would have published. emailed_at 0 is not yet mailed.
ALTER TABLE doc_tender_exceptions ADD COLUMN job_id TEXT REFERENCES doc_tender_jobs (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_exceptions ADD COLUMN file_version_id TEXT REFERENCES file_versions (id) ON DELETE RESTRICT;
ALTER TABLE doc_tender_exceptions ADD COLUMN best_answer TEXT NOT NULL DEFAULT '{}';
ALTER TABLE doc_tender_exceptions ADD COLUMN emailed_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX doc_tender_exceptions_status_idx ON doc_tender_exceptions (status, kind);
CREATE INDEX doc_tender_exceptions_file_idx ON doc_tender_exceptions (file_id);

-- A photo grouping is decided once, by the models (how model_agreement) or by a person
-- (how person, which locks it). Each photo belongs to at most one group.
CREATE TABLE doc_tender_groups (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  how TEXT NOT NULL CHECK (how IN ('model_agreement', 'person', 'single')),
  title TEXT NOT NULL,
  output_file_id TEXT REFERENCES files (id) ON DELETE RESTRICT,
  job_id TEXT REFERENCES doc_tender_jobs (id) ON DELETE RESTRICT
);
CREATE TABLE doc_tender_group_members (
  group_id TEXT NOT NULL REFERENCES doc_tender_groups (id) ON DELETE RESTRICT,
  file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  position INTEGER NOT NULL,
  PRIMARY KEY (group_id, position)
);
CREATE UNIQUE INDEX doc_tender_group_members_file_uidx ON doc_tender_group_members (file_id);

-- Provider health: while down_since is set, no attempts are spent.
CREATE TABLE doc_tender_providers (
  provider TEXT PRIMARY KEY,
  down_since BIGINT NOT NULL,
  checked_at BIGINT NOT NULL,
  last_error TEXT NOT NULL
);

-- Last send of bundled mail (exceptions every 15 minutes, the daily summary).
CREATE TABLE doc_tender_mail (
  kind TEXT PRIMARY KEY,
  sent_at BIGINT NOT NULL,
  sent_on TEXT NOT NULL
);

-- +goose Down
UPDATE doc_tender_results SET category = 'reference' WHERE category IS NULL;
ALTER TABLE doc_tender_results ALTER COLUMN category SET NOT NULL;
DROP TABLE doc_tender_mail;
DROP TABLE doc_tender_providers;
DROP TABLE doc_tender_group_members;
DROP TABLE doc_tender_groups;
DROP INDEX doc_tender_exceptions_file_idx;
DROP INDEX doc_tender_exceptions_status_idx;
DROP INDEX doc_tender_results_file_idx;
DROP INDEX doc_tender_units_job_unit_uidx;
DROP INDEX doc_tender_jobs_file_idx;
DROP INDEX doc_tender_jobs_status_idx;
DROP INDEX doc_tender_jobs_active_uidx;
ALTER TABLE doc_tender_exceptions DROP COLUMN emailed_at;
ALTER TABLE doc_tender_exceptions DROP COLUMN best_answer;
ALTER TABLE doc_tender_exceptions DROP COLUMN file_version_id;
ALTER TABLE doc_tender_exceptions DROP COLUMN job_id;
ALTER TABLE doc_tender_results DROP COLUMN name_set;
ALTER TABLE doc_tender_results DROP COLUMN suggested_matter_id;
ALTER TABLE doc_tender_results DROP COLUMN markdown_rendition_id;
ALTER TABLE doc_tender_results DROP COLUMN confidences;
ALTER TABLE doc_tender_results DROP COLUMN job_id;
ALTER TABLE doc_tender_results DROP COLUMN file_id;
ALTER TABLE doc_tender_units DROP COLUMN cost_cents;
ALTER TABLE doc_tender_units DROP COLUMN finished_at;
ALTER TABLE doc_tender_units DROP COLUMN started_at;
ALTER TABLE doc_tender_units DROP COLUMN check_model;
ALTER TABLE doc_tender_units DROP COLUMN check_method;
ALTER TABLE doc_tender_units DROP COLUMN last_error;
ALTER TABLE doc_tender_units DROP COLUMN next_attempt_at;
ALTER TABLE doc_tender_units DROP COLUMN attempts;
ALTER TABLE doc_tender_units DROP COLUMN status;
ALTER TABLE doc_tender_units DROP COLUMN ordinal;
ALTER TABLE doc_tender_jobs DROP COLUMN group_job_id;
ALTER TABLE doc_tender_jobs DROP COLUMN cost_cents;
ALTER TABLE doc_tender_jobs DROP COLUMN finished_at;
ALTER TABLE doc_tender_jobs DROP COLUMN held_since;
ALTER TABLE doc_tender_jobs DROP COLUMN reason;
ALTER TABLE doc_tender_jobs DROP COLUMN kind;
ALTER TABLE doc_tender_jobs DROP COLUMN matter_id;
ALTER TABLE doc_tender_jobs DROP COLUMN file_id;

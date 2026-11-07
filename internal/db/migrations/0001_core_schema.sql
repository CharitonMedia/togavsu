-- +goose Up
-- core schema version 1
-- contracts migration range 1-99
-- section 5 records. Money is integer cents. Timestamps are unix microseconds UTC.
-- JSON is stored whole in TEXT columns.
-- invoice_id on time entries and expenses has no foreign key because invoice lines already point at those rows.
-- current_version_id has no foreign key because a file and its versions refer to each other.
-- Append-only tables: trust_ledger, audit_log, events, file_versions, file_renditions, ai_call_log.
-- Their repository interfaces have no update or delete method. This schema does not enforce that.
-- Raw SQL and the table owner can still update or delete rows. There is no trigger.
-- PRD section 6 forbids triggers that carry business logic, so immutability stays with the
-- identity, practice, trust, files, and modules implementations. See docs/architecture/contracts.md.

CREATE TABLE practice_areas (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  matter_types TEXT NOT NULL,
  stages TEXT NOT NULL,
  active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE TABLE counties (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  state TEXT NOT NULL
);

CREATE TABLE contacts (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('person', 'organization')),
  given_name TEXT NOT NULL,
  middle_name TEXT NOT NULL,
  family_name TEXT NOT NULL,
  prefix TEXT NOT NULL,
  suffix TEXT NOT NULL,
  full_name TEXT NOT NULL,
  preferred_name TEXT NOT NULL,
  also_known_as TEXT NOT NULL,
  date_of_birth TEXT NOT NULL,
  date_of_death TEXT NOT NULL,
  ssn_ein_ciphertext TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  formation_state TEXT NOT NULL,
  formation_date TEXT NOT NULL,
  archived INTEGER NOT NULL CHECK (archived IN (0, 1)),
  source TEXT NOT NULL
);

CREATE TABLE contact_addresses (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  label TEXT NOT NULL,
  is_primary INTEGER NOT NULL CHECK (is_primary IN (0, 1)),
  line1 TEXT NOT NULL,
  line2 TEXT NOT NULL,
  city TEXT NOT NULL,
  region TEXT NOT NULL,
  postal_code TEXT NOT NULL,
  country TEXT NOT NULL,
  effective_from TEXT NOT NULL,
  effective_to TEXT NOT NULL
);

CREATE TABLE contact_emails (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  label TEXT NOT NULL,
  is_primary INTEGER NOT NULL CHECK (is_primary IN (0, 1)),
  email TEXT NOT NULL,
  effective_from TEXT NOT NULL,
  effective_to TEXT NOT NULL
);

CREATE TABLE contact_phones (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  label TEXT NOT NULL,
  is_primary INTEGER NOT NULL CHECK (is_primary IN (0, 1)),
  phone_e164 TEXT NOT NULL,
  effective_from TEXT NOT NULL,
  effective_to TEXT NOT NULL
);

CREATE TABLE contact_tags (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  tag TEXT NOT NULL
);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  email TEXT NOT NULL,
  roles TEXT NOT NULL,
  active INTEGER NOT NULL CHECK (active IN (0, 1)),
  default_hourly_rate_cents BIGINT NOT NULL,
  contact_id TEXT REFERENCES contacts (id) ON DELETE RESTRICT,
  bar_number TEXT NOT NULL,
  bar_state TEXT NOT NULL
);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  token_hash TEXT NOT NULL,
  expires_at BIGINT NOT NULL,
  idle_expires_at BIGINT NOT NULL,
  revoked_at BIGINT
);

CREATE TABLE password_credentials (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  argon2_hash TEXT NOT NULL
);

CREATE TABLE totp_secrets (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  ciphertext TEXT NOT NULL,
  confirmed INTEGER NOT NULL CHECK (confirmed IN (0, 1))
);

CREATE TABLE recovery_codes (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  code_hash TEXT NOT NULL,
  used_at BIGINT
);

CREATE TABLE api_tokens (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT REFERENCES users (id) ON DELETE RESTRICT,
  module_id TEXT NOT NULL,
  name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  scopes TEXT NOT NULL,
  revoked_at BIGINT
);

CREATE TABLE referral_sources (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('person', 'organization', 'advertising', 'web', 'other')),
  contact_id TEXT REFERENCES contacts (id) ON DELETE RESTRICT,
  active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE TABLE matters (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  number TEXT NOT NULL,
  name TEXT NOT NULL,
  practice_area_id TEXT NOT NULL REFERENCES practice_areas (id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('prospect', 'open', 'on_hold', 'closed')),
  stage TEXT NOT NULL,
  case_number TEXT NOT NULL,
  court TEXT NOT NULL,
  county_id TEXT REFERENCES counties (id) ON DELETE RESTRICT,
  client_position TEXT NOT NULL,
  opened_on TEXT NOT NULL,
  closed_on TEXT NOT NULL,
  close_reason TEXT NOT NULL,
  statute_of_limitations TEXT NOT NULL,
  billing_type TEXT NOT NULL CHECK (billing_type IN ('flat', 'hourly', 'mixed', 'contingency', 'pro_bono')),
  flat_fee_cents BIGINT NOT NULL,
  matter_rate_cents BIGINT NOT NULL,
  responsible_attorney_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  assigned_staff_id TEXT REFERENCES users (id) ON DELETE RESTRICT,
  referral_source_id TEXT REFERENCES referral_sources (id) ON DELETE RESTRICT
);

CREATE TABLE matter_parties (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  role TEXT NOT NULL,
  is_primary INTEGER NOT NULL CHECK (is_primary IN (0, 1)),
  role_attributes TEXT NOT NULL,
  service_address TEXT NOT NULL,
  notes TEXT NOT NULL
);

CREATE TABLE matter_walls (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE settings (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  key TEXT NOT NULL,
  value TEXT NOT NULL
);

CREATE TABLE doc_categories (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL,
  tier TEXT NOT NULL CHECK (tier IN ('high', 'standard')),
  folder TEXT NOT NULL,
  active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE TABLE storage_backends (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('local_disk', 'network_path', 's3', 'dropbox')),
  name TEXT NOT NULL,
  settings_ciphertext TEXT NOT NULL,
  encryption INTEGER NOT NULL CHECK (encryption IN (0, 1)),
  is_default INTEGER NOT NULL CHECK (is_default IN (0, 1)),
  health TEXT NOT NULL,
  backed_up INTEGER NOT NULL CHECK (backed_up IN (0, 1)),
  drop_folder INTEGER NOT NULL CHECK (drop_folder IN (0, 1))
);

CREATE TABLE files (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  extension TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  contact_id TEXT REFERENCES contacts (id) ON DELETE RESTRICT,
  folder TEXT NOT NULL,
  category TEXT REFERENCES doc_categories (name) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('', 'draft', 'final', 'executed', 'filed', 'received')),
  origin TEXT NOT NULL CHECK (origin IN ('firm', 'received')),
  attorney_only INTEGER NOT NULL CHECK (attorney_only IN (0, 1)),
  shared_with_client INTEGER NOT NULL CHECK (shared_with_client IN (0, 1)),
  source TEXT NOT NULL,
  arrived_name TEXT NOT NULL,
  locked_fields TEXT NOT NULL,
  current_version_id TEXT NOT NULL,
  trashed_at BIGINT NOT NULL,
  trashed_by TEXT
);

CREATE TABLE file_versions (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  number INTEGER NOT NULL,
  storage_backend_id TEXT NOT NULL REFERENCES storage_backends (id) ON DELETE RESTRICT,
  storage_key TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  sha256 TEXT NOT NULL,
  media_type TEXT NOT NULL,
  saved_by TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  saved_at BIGINT NOT NULL,
  how TEXT NOT NULL CHECK (how IN ('upload', 'editor', 'editor_run', 'conversion', 'grouping', 'module')),
  note TEXT NOT NULL,
  quarantined INTEGER NOT NULL CHECK (quarantined IN (0, 1))
);

CREATE TABLE file_renditions (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_version_id TEXT NOT NULL REFERENCES file_versions (id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK (kind IN ('markdown', 'pdf_preview', 'grouped_pdf')),
  number INTEGER NOT NULL,
  storage_backend_id TEXT NOT NULL REFERENCES storage_backends (id) ON DELETE RESTRICT,
  storage_key TEXT NOT NULL,
  size_bytes BIGINT NOT NULL,
  sha256 TEXT NOT NULL,
  made_by TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  made_at BIGINT NOT NULL
);

CREATE TABLE file_links (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  from_file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  to_file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK (kind IN ('photos_of', 'conversion_of', 'email_attachment'))
);

CREATE TABLE edit_locks (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  since_at BIGINT NOT NULL,
  renewed_at BIGINT NOT NULL
);

CREATE TABLE storage_moves (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  from_backend_id TEXT NOT NULL REFERENCES storage_backends (id) ON DELETE RESTRICT,
  to_backend_id TEXT NOT NULL REFERENCES storage_backends (id) ON DELETE RESTRICT,
  scope TEXT NOT NULL CHECK (scope IN ('all', 'matter')),
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'checked', 'confirmed', 'failed')),
  progress INTEGER NOT NULL,
  confirmed_by TEXT REFERENCES users (id) ON DELETE RESTRICT,
  confirmed_at BIGINT NOT NULL
);

CREATE TABLE fee_agreements (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  fee_type TEXT NOT NULL,
  amount_cents BIGINT NOT NULL,
  services TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft', 'sent', 'signed', 'void')),
  signed_on TEXT NOT NULL,
  void_reason TEXT NOT NULL,
  file_id TEXT REFERENCES files (id) ON DELETE RESTRICT
);

CREATE TABLE time_entries (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  worked_on TEXT NOT NULL,
  increments INTEGER NOT NULL,
  description TEXT NOT NULL,
  activity_type TEXT NOT NULL,
  billable INTEGER NOT NULL CHECK (billable IN (0, 1)),
  rate_cents BIGINT NOT NULL,
  invoice_id TEXT,
  source TEXT NOT NULL
);

CREATE TABLE timers (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  started_at BIGINT NOT NULL,
  stopped_at BIGINT,
  time_entry_id TEXT
);

CREATE TABLE expenses (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  incurred_on TEXT NOT NULL,
  description TEXT NOT NULL,
  amount_cents BIGINT NOT NULL,
  expense_type TEXT NOT NULL,
  billable INTEGER NOT NULL CHECK (billable IN (0, 1)),
  markup_percent INTEGER NOT NULL,
  file_id TEXT REFERENCES files (id) ON DELETE RESTRICT,
  invoice_id TEXT
);

CREATE TABLE invoices (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  number TEXT NOT NULL,
  issued_on TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('draft', 'sent', 'viewed', 'paid', 'void')),
  subtotal_cents BIGINT NOT NULL,
  adjustment_cents BIGINT NOT NULL,
  adjustment_reason TEXT NOT NULL,
  interest_cents BIGINT NOT NULL,
  total_cents BIGINT NOT NULL,
  amount_paid_cents BIGINT NOT NULL,
  balance_due_cents BIGINT NOT NULL,
  write_off_cents BIGINT NOT NULL,
  write_off_reason TEXT NOT NULL,
  void_reason TEXT NOT NULL,
  dispute_status TEXT NOT NULL,
  dispute_notes TEXT NOT NULL,
  sent_at BIGINT NOT NULL,
  paid_at BIGINT NOT NULL,
  client_message TEXT NOT NULL,
  internal_notes TEXT NOT NULL
);

CREATE TABLE invoice_lines (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  invoice_id TEXT NOT NULL REFERENCES invoices (id) ON DELETE RESTRICT,
  kind TEXT NOT NULL CHECK (kind IN ('time', 'expense', 'flat_fee', 'adjustment')),
  description TEXT NOT NULL,
  amount_cents BIGINT NOT NULL,
  time_entry_id TEXT,
  expense_id TEXT
);

CREATE TABLE trust_accounts (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  bank TEXT NOT NULL,
  name TEXT NOT NULL,
  iolta INTEGER NOT NULL CHECK (iolta IN (0, 1)),
  last_four TEXT NOT NULL,
  active INTEGER NOT NULL CHECK (active IN (0, 1))
);

CREATE TABLE trust_ledger (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  trust_account_id TEXT NOT NULL REFERENCES trust_accounts (id) ON DELETE RESTRICT,
  client_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  entry_type TEXT NOT NULL CHECK (entry_type IN ('deposit', 'withdrawal', 'transfer_to_operating', 'refund', 'adjustment')),
  amount_cents BIGINT NOT NULL,
  method TEXT NOT NULL,
  description TEXT NOT NULL,
  reverses_id TEXT REFERENCES trust_ledger (id) ON DELETE RESTRICT
);

CREATE TABLE trust_balance_cache (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  trust_account_id TEXT NOT NULL REFERENCES trust_accounts (id) ON DELETE RESTRICT,
  client_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  balance_cents BIGINT NOT NULL
);

CREATE TABLE trust_flags (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  trust_entry_id TEXT NOT NULL REFERENCES trust_ledger (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  client_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  reason TEXT NOT NULL,
  resolved_at BIGINT NOT NULL,
  resolved_by TEXT,
  resolution_note TEXT NOT NULL
);

CREATE TABLE trust_replenishment_requests (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  client_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  amount_cents BIGINT NOT NULL,
  threshold_cents BIGINT NOT NULL,
  message TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('active', 'satisfied', 'cancelled')),
  invoice_id TEXT REFERENCES invoices (id) ON DELETE RESTRICT
);

CREATE TABLE trust_reconciliations (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  trust_account_id TEXT NOT NULL REFERENCES trust_accounts (id) ON DELETE RESTRICT,
  statement_on TEXT NOT NULL,
  statement_balance_cents BIGINT NOT NULL,
  ledger_total_cents BIGINT NOT NULL,
  client_balance_cents BIGINT NOT NULL,
  difference_cents BIGINT NOT NULL
);

CREATE TABLE payments (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  contact_id TEXT NOT NULL REFERENCES contacts (id) ON DELETE RESTRICT,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  amount_cents BIGINT NOT NULL,
  destination TEXT NOT NULL CHECK (destination IN ('operating', 'trust')),
  method TEXT NOT NULL CHECK (method IN ('cash', 'check', 'card', 'ach', 'internal_transfer', 'other')),
  reference_number TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'failed', 'reversed')),
  invoice_id TEXT REFERENCES invoices (id) ON DELETE RESTRICT,
  trust_entry_id TEXT REFERENCES trust_ledger (id) ON DELETE RESTRICT,
  source TEXT NOT NULL
);

CREATE TABLE notes (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  contact_id TEXT REFERENCES contacts (id) ON DELETE RESTRICT,
  author_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  pinned INTEGER NOT NULL CHECK (pinned IN (0, 1)),
  status TEXT NOT NULL CHECK (status IN ('active', 'archived'))
);

CREATE TABLE note_tags (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  note_id TEXT NOT NULL REFERENCES notes (id) ON DELETE RESTRICT,
  tag TEXT NOT NULL
);

CREATE TABLE conflict_records (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  aliases TEXT NOT NULL,
  date_of_birth TEXT NOT NULL,
  relationship TEXT NOT NULL CHECK (relationship IN ('prior_client', 'opposing_party', 'other')),
  scope TEXT NOT NULL CHECK (scope IN ('client', 'matter')),
  reason TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  starts_on TEXT NOT NULL,
  ends_on TEXT NOT NULL,
  source TEXT NOT NULL
);

CREATE TABLE conflict_reviews (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  conflict_record_id TEXT NOT NULL REFERENCES conflict_records (id) ON DELETE RESTRICT,
  confidence INTEGER NOT NULL,
  decision TEXT NOT NULL CHECK (decision IN ('clear', 'escalate', 'no_go')),
  reason TEXT NOT NULL,
  decided_by TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  decided_at BIGINT NOT NULL
);

CREATE TABLE calendar_events (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  title TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('court_date', 'client_meeting', 'deadline', 'block', 'other')),
  starts_at BIGINT NOT NULL,
  ends_at BIGINT NOT NULL,
  all_day INTEGER NOT NULL CHECK (all_day IN (0, 1)),
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  location TEXT NOT NULL,
  travel_minutes INTEGER NOT NULL,
  notes TEXT NOT NULL
);

CREATE TABLE calendar_event_attendees (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  event_id TEXT NOT NULL REFERENCES calendar_events (id) ON DELETE RESTRICT,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT
);

CREATE TABLE deadlines (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  matter_id TEXT NOT NULL REFERENCES matters (id) ON DELETE RESTRICT,
  title TEXT NOT NULL,
  due_on TEXT NOT NULL,
  responsible_user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  status TEXT NOT NULL CHECK (status IN ('open', 'done', 'waived')),
  reminder_offsets TEXT NOT NULL,
  done_by TEXT,
  done_at BIGINT NOT NULL
);

CREATE TABLE audit_log (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  role TEXT NOT NULL,
  source TEXT NOT NULL,
  entity TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  field TEXT NOT NULL,
  old_value TEXT NOT NULL,
  new_value TEXT NOT NULL,
  reason TEXT NOT NULL
);

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  actor_kind TEXT NOT NULL CHECK (actor_kind IN ('staff', 'client', 'agent', 'module', 'system')),
  actor_id TEXT NOT NULL,
  action TEXT NOT NULL,
  record_type TEXT NOT NULL,
  record_id TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  summary TEXT NOT NULL,
  client_visible INTEGER NOT NULL CHECK (client_visible IN (0, 1))
);

CREATE TABLE court_profiles (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  name TEXT NOT NULL,
  profile_json TEXT NOT NULL,
  verified INTEGER NOT NULL CHECK (verified IN (0, 1)),
  verified_by TEXT,
  verified_at BIGINT NOT NULL
);

CREATE TABLE doc_tender_jobs (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_version_id TEXT NOT NULL REFERENCES file_versions (id) ON DELETE RESTRICT,
  stage TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL,
  next_attempt_at BIGINT NOT NULL,
  last_error TEXT NOT NULL,
  lease_owner TEXT NOT NULL,
  lease_until BIGINT NOT NULL
);

CREATE TABLE doc_tender_units (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  job_id TEXT NOT NULL REFERENCES doc_tender_jobs (id) ON DELETE RESTRICT,
  unit TEXT NOT NULL,
  method TEXT NOT NULL,
  model TEXT NOT NULL,
  text TEXT NOT NULL,
  check_result TEXT NOT NULL
);

CREATE TABLE doc_tender_results (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_version_id TEXT NOT NULL REFERENCES file_versions (id) ON DELETE RESTRICT,
  name_parts TEXT NOT NULL,
  summary TEXT NOT NULL,
  category TEXT NOT NULL REFERENCES doc_categories (name) ON DELETE RESTRICT,
  category_how TEXT NOT NULL,
  critical_fields TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT
);

CREATE TABLE doc_tender_exceptions (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  file_id TEXT NOT NULL REFERENCES files (id) ON DELETE RESTRICT,
  kind TEXT NOT NULL,
  opened_at BIGINT NOT NULL,
  detail TEXT NOT NULL,
  status TEXT NOT NULL,
  resolved_by TEXT,
  resolved_at BIGINT NOT NULL,
  resolution TEXT NOT NULL
);

CREATE TABLE ai_call_log (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  task TEXT NOT NULL,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  prompt_tokens INTEGER NOT NULL,
  completion_tokens INTEGER NOT NULL,
  cost_cents BIGINT NOT NULL,
  fell_back INTEGER NOT NULL CHECK (fell_back IN (0, 1)),
  fallback_provider TEXT NOT NULL,
  fallback_model TEXT NOT NULL,
  user_id TEXT REFERENCES users (id) ON DELETE RESTRICT,
  module_id TEXT NOT NULL,
  matter_id TEXT REFERENCES matters (id) ON DELETE RESTRICT,
  file_id TEXT REFERENCES files (id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX users_email_uidx ON users (email);
CREATE UNIQUE INDEX matters_number_uidx ON matters (number);
CREATE UNIQUE INDEX settings_key_uidx ON settings (key);
CREATE UNIQUE INDEX doc_categories_name_uidx ON doc_categories (name);
CREATE UNIQUE INDEX counties_name_state_uidx ON counties (name, state);
CREATE UNIQUE INDEX matter_walls_pair_uidx ON matter_walls (matter_id, user_id);
CREATE UNIQUE INDEX password_credentials_user_uidx ON password_credentials (user_id);
CREATE UNIQUE INDEX totp_secrets_user_uidx ON totp_secrets (user_id);
CREATE UNIQUE INDEX file_versions_number_uidx ON file_versions (file_id, number);
CREATE UNIQUE INDEX edit_locks_file_uidx ON edit_locks (file_id);
CREATE UNIQUE INDEX fee_agreements_one_active ON fee_agreements (matter_id) WHERE status <> 'void';
CREATE UNIQUE INDEX timers_one_running ON timers (user_id, matter_id) WHERE stopped_at IS NULL;
CREATE INDEX matters_status_idx ON matters (status);
CREATE INDEX files_matter_idx ON files (matter_id);
CREATE INDEX trust_ledger_matter_idx ON trust_ledger (matter_id);
CREATE INDEX trust_ledger_client_idx ON trust_ledger (client_id);
CREATE INDEX audit_log_entity_idx ON audit_log (entity, entity_id);
CREATE INDEX events_matter_idx ON events (matter_id);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- +goose Down
-- The initial schema is not reversed in place.
SELECT 1;

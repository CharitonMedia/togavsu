-- Matter Clock keeps one monthly target per matter, linked to the core matter by id.
CREATE TABLE matter_targets (
  matter_id TEXT PRIMARY KEY REFERENCES core.matters (id) ON DELETE RESTRICT,
  monthly_target_tenths INTEGER NOT NULL CHECK (monthly_target_tenths >= 0),
  note TEXT NOT NULL DEFAULT '',
  updated_at BIGINT NOT NULL
);

-- Events the core delivered, so the module (and its tests) can show what it received.
CREATE TABLE received_events (
  event_id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  record_id TEXT NOT NULL,
  received_at BIGINT NOT NULL
);

CREATE VIEW monthly_targets AS
  SELECT t.matter_id, t.monthly_target_tenths FROM matter_targets t;

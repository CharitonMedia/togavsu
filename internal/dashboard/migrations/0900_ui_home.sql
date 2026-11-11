-- +goose Up
-- ui migration range 900-949 (internal/dashboard). Help takes 950-999.
-- Portable SQL: JSON columns are stored and read whole and parsed in Go.

-- Saved home layouts (PRD 8.6). slot is below home.max_saved_layouts and unique per
-- user, so two concurrent saves cannot both take the last free place.
CREATE TABLE home_layouts (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  slot INTEGER NOT NULL CHECK (slot >= 0),
  widgets TEXT NOT NULL,
  UNIQUE (user_id, slot),
  UNIQUE (user_id, name)
);
CREATE UNIQUE INDEX home_layouts_one_default ON home_layouts (user_id) WHERE is_default = 1;

-- The layout a user works in. It is not a saved layout and does not count.
CREATE TABLE home_working_layouts (
  user_id TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  updated_at BIGINT NOT NULL,
  widgets TEXT NOT NULL
);

-- The admin's default layout for each role.
CREATE TABLE role_layouts (
  role TEXT PRIMARY KEY CHECK (role IN ('admin', 'attorney', 'staff', 'accountant', 'client')),
  updated_at BIGINT NOT NULL,
  updated_by TEXT NOT NULL,
  widgets TEXT NOT NULL
);

-- Sidebar state and My settings, saved with the user so they follow them.
CREATE TABLE user_preferences (
  user_id TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  updated_at BIGINT NOT NULL,
  prefs TEXT NOT NULL
);

-- PRD 8.17 notification preference grid; a missing row is the default.
CREATE TABLE notification_preferences (
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  updated_at BIGINT NOT NULL,
  in_app INTEGER NOT NULL CHECK (in_app IN (0, 1)),
  email INTEGER NOT NULL CHECK (email IN (0, 1)),
  PRIMARY KEY (user_id, kind)
);

-- "Report a problem" submissions (PRD 8.9). Kept; emailed to the recipients.
CREATE TABLE problem_reports (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
  page TEXT NOT NULL,
  message TEXT NOT NULL
);
CREATE INDEX problem_reports_created_idx ON problem_reports (created_at);

-- +goose Down
DROP TABLE problem_reports;
DROP TABLE notification_preferences;
DROP TABLE user_preferences;
DROP TABLE role_layouts;
DROP TABLE home_working_layouts;
DROP TABLE home_layouts;

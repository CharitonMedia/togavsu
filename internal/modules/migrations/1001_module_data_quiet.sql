-- +goose Up
-- modules migration range 1000-1099
-- data_quiet_since is when the module's schema last stopped changing: its login was
-- locked (disable, keep-data uninstall) or a migration last ran on it while it was
-- locked. 0 while the module can write. Delete data accepts a backup the admin names
-- only if it was taken after this moment (PRD 9.9, AC-038). Unix microseconds UTC.
ALTER TABLE modules ADD COLUMN data_quiet_since BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE modules DROP COLUMN data_quiet_since;

-- +goose Up
-- module-core range 1300-1399 (CORE-14)
-- Client portal temporary passwords force a change on first use.
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0 CHECK (must_change_password IN (0, 1));

-- +goose Down
SELECT 1;

-- +goose Up
-- release migration range 1200-1299 (AC-002, archiveMatter). archived is the matter's
-- archived flag, distinct from closed: the matter stays readable and drops out of default
-- lists. The practice projection writes it with every matter row. Expand only: the old
-- core leaves it at 0.
ALTER TABLE matters ADD COLUMN archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1));

-- +goose Down
ALTER TABLE matters DROP COLUMN archived;

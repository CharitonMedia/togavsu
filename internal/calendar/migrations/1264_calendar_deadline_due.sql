-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). Deadline lists narrow by
-- status, matter and due date in SQL (Postgres.DeadlinesFor) instead of reading every
-- deadline.
CREATE INDEX deadlines_due_idx ON deadlines (due_on, id);
CREATE INDEX deadlines_status_due_idx ON deadlines (status, due_on, id);
CREATE INDEX deadlines_matter_idx ON deadlines (matter_id, due_on, id);

-- +goose Down
DROP INDEX deadlines_matter_idx;
DROP INDEX deadlines_status_due_idx;
DROP INDEX deadlines_due_idx;

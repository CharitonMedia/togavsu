-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). billing_unbilled holds the
-- billable time entries and expenses that no invoice bills yet, with the few fields the
-- unbilled report shows, so the report reads a narrow table instead of scanning every
-- time entry body (1,000,000 at the AC-017 seed). total_cents is the row's billed amount
-- (rate times minutes per hour, or cost plus markup, each rounded once half away from
-- zero as billing's timeAmount and expenseAmount round), so per-matter totals are one
-- aggregate. Postgres.Put keeps it in step with
-- each document in the same transaction, IndexRows fills it for documents stored without
-- Put, and the foreign key drops a row with its document.
CREATE TABLE billing_unbilled (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    matter_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    on_date TEXT NOT NULL,
    description TEXT NOT NULL,
    minutes BIGINT NOT NULL,
    rate_cents BIGINT NOT NULL,
    amount_cents BIGINT NOT NULL,
    markup_percent BIGINT NOT NULL,
    total_cents BIGINT NOT NULL,
    PRIMARY KEY (kind, id),
    FOREIGN KEY (kind, id) REFERENCES billing_records (kind, id) ON DELETE CASCADE
);
CREATE INDEX billing_unbilled_date_idx ON billing_unbilled (kind, on_date, id);
CREATE INDEX billing_unbilled_user_idx ON billing_unbilled (kind, user_id, on_date, id);
CREATE INDEX billing_unbilled_matter_idx ON billing_unbilled (kind, matter_id, id);
INSERT INTO billing_unbilled (kind, id, matter_id, user_id, on_date, description, minutes, rate_cents, amount_cents, markup_percent, total_cents)
SELECT kind, id, matter_id, user_id, on_date, description, minutes, rate_cents, amount_cents, markup_percent,
    CASE WHEN kind = 'time_entry' THEN (rate_cents * minutes) / 60 + (rate_cents * minutes) % 60 * 2 / 60
        ELSE (amount_cents * (markup_percent + 100)) / 100 + (amount_cents * (markup_percent + 100)) % 100 * 2 / 100 END
FROM (SELECT kind, id, matter_id, user_id, on_date, COALESCE(b->>'Description', '') AS description,
        CASE WHEN kind = 'time_entry' THEN COALESCE((b->>'Increments')::BIGINT, 0) * COALESCE((b->>'increment_minutes')::BIGINT, 0) ELSE 0 END AS minutes,
        CASE WHEN kind = 'time_entry' THEN COALESCE((b->>'RateCents')::BIGINT, 0) ELSE 0 END AS rate_cents,
        CASE WHEN kind = 'expense' THEN COALESCE((b->>'AmountCents')::BIGINT, 0) ELSE 0 END AS amount_cents,
        CASE WHEN kind = 'expense' THEN COALESCE((b->>'MarkupPercent')::BIGINT, 0) ELSE 0 END AS markup_percent
    FROM (SELECT kind, id, matter_id, user_id, on_date, body::jsonb AS b FROM billing_records
          WHERE kind IN ('time_entry', 'expense') AND indexed = 1 AND invoice_id = '') d
    WHERE COALESCE((b->>'Billable')::BOOLEAN, FALSE)) r;

-- +goose Down
DROP TABLE billing_unbilled;

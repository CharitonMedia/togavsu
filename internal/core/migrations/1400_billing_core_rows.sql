-- +goose Up
-- core-update-2a range 1400-1449 (CORE-36): mark the billing JSON→rows move.
-- Dual-write lives in internal/billing/core_rows.go (Postgres.Put). Startup calls
-- RebuildCoreRows once behind billing_core_rows_backfill (migration 1401) after
-- IndexRows so every existing invoice, payment and expense lands in core.invoices /
-- core.payments / core.expenses with cent reconciliation.
SELECT 1;

-- +goose Down
SELECT 1;

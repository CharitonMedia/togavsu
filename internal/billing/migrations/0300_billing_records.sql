-- +goose Up
-- billing migration range 300-399
-- billing_records holds every billing row as one JSON document per (kind, id): time entries,
-- timers, expenses, invoices with their lines, payments, fee items, activity and expense
-- types, billing events and idempotency results. UUIDv7 ids keep insertion order.
-- ponytail: ceiling: the section 5 relational tables (time_entries, invoices, payments) are
-- not yet the billing source of truth because practice keeps matters in practice_records, so
-- their foreign keys to matters cannot be met. See internal/billing/doc.go.
CREATE TABLE billing_records (
  kind TEXT NOT NULL,
  id TEXT NOT NULL,
  body TEXT NOT NULL,
  PRIMARY KEY (kind, id)
);

-- +goose Down
DROP TABLE billing_records;

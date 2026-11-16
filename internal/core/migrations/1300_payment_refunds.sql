-- +goose Up
-- module-core range 1300-1399 (CORE-22 / CORE-24)
-- payment_refunds is a linkable core record (CORE-22). The refund API (CORE-24) writes it.
CREATE TABLE payment_refunds (
  id TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  updated_at BIGINT NOT NULL,
  created_by TEXT NOT NULL,
  payment_id TEXT NOT NULL REFERENCES payments (id) ON DELETE RESTRICT,
  amount_cents BIGINT NOT NULL,
  reason TEXT NOT NULL,
  reference_number TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'failed')),
  trust_entry_id TEXT REFERENCES trust_ledger (id) ON DELETE RESTRICT,
  source TEXT NOT NULL DEFAULT ''
);

ALTER TABLE payments ADD COLUMN refunded_cents BIGINT NOT NULL DEFAULT 0;
ALTER TABLE payments ADD COLUMN refund_state TEXT NOT NULL DEFAULT 'none'
  CHECK (refund_state IN ('none', 'partial', 'full'));

-- +goose Down
SELECT 1;

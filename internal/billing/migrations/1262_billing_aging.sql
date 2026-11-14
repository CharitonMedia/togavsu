-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). billing_aging holds what AR
-- aging reads of each invoice and payment: number, status, dates, total or amount, the
-- void or reversal time and an invoice's write-offs (JSON, '' when none), so the report
-- reads one narrow row per invoice instead of decoding every invoice header and payment.
-- Postgres.Put keeps it in step in the same transaction, IndexRows fills it for documents
-- stored without Put, and the foreign key drops a row with its document.
CREATE TABLE billing_aging (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    matter_id TEXT NOT NULL,
    invoice_id TEXT NOT NULL,
    number TEXT NOT NULL,
    status TEXT NOT NULL,
    on_date TEXT NOT NULL,
    due_on TEXT NOT NULL,
    amount_cents BIGINT NOT NULL,
    closed_at TEXT NOT NULL,
    write_offs TEXT NOT NULL,
    PRIMARY KEY (kind, id),
    FOREIGN KEY (kind, id) REFERENCES billing_records (kind, id) ON DELETE CASCADE
);
CREATE INDEX billing_aging_date_idx ON billing_aging (kind, on_date, id);
INSERT INTO billing_aging (kind, id, matter_id, invoice_id, number, status, on_date, due_on, amount_cents, closed_at, write_offs)
SELECT kind, id, COALESCE(b->>'MatterID', ''), CASE WHEN kind = 'payment' THEN COALESCE(b->>'InvoiceID', '') ELSE '' END,
    COALESCE(b->>'Number', ''), COALESCE(b->>'Status', ''),
    CASE WHEN kind = 'payment' THEN COALESCE(b->>'received_on', '') ELSE COALESCE(b->>'IssuedOn', '') END,
    COALESCE(b->>'due_on', ''),
    CASE WHEN kind = 'payment' THEN COALESCE((b->>'AmountCents')::BIGINT, 0) ELSE COALESCE((b->>'TotalCents')::BIGINT, 0) END,
    CASE WHEN jsonb_typeof(b->CASE WHEN kind = 'payment' THEN 'reversed_at' ELSE 'voided_at' END) = 'string'
        THEN (b->CASE WHEN kind = 'payment' THEN 'reversed_at' ELSE 'voided_at' END)::TEXT ELSE '' END,
    CASE WHEN kind = 'invoice' AND jsonb_typeof(b->'write_offs') = 'array' AND jsonb_array_length(b->'write_offs') > 0
        THEN (b->'write_offs')::TEXT ELSE '' END
FROM (SELECT kind, id, (CASE WHEN kind = 'invoice' AND head <> '' THEN head ELSE body END)::jsonb AS b FROM billing_records
      WHERE kind IN ('invoice', 'payment')) r;

-- +goose Down
DROP TABLE billing_aging;

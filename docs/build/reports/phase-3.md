# Phase 3 report: billing and trust on the composed core

Status: **all four phase cases pass; full product acceptance pending.** Billing and trust run inside the core at `b59f0aaeaa0aefafa4acec593f1322d35c33b496` on Postgres as the non-superuser core role, with their API, MCP tools, screens and system mail. Local runs of all six required CI jobs pass there. GitHub Product CI on that SHA is pending (the controller publishes the branch). AC-005, AC-007, AC-008, AC-009 and AC-026 pass globally; AC-003, AC-004, AC-006, AC-018 and AC-021 pass their phase cases but stay pending globally, for the reasons below. 44 of 52 criteria remain pending. Machine-readable evidence: [integrate-finance.json](integrate-finance.json).

## Phase cases at b59f0aaeaa0aefafa4acec593f1322d35c33b496 (each exit 0)

| Case | Criteria | Command |
| --- | --- | --- |
| invoice-workflows | AC-008, AC-009, AC-026 | `go test -count=1 -v -run '^TestFinanceInvoiceWorkflows$' ./internal/core/` |
| trust-invariants | AC-005, AC-006, AC-007 | `go test -count=1 -v -run '^TestFinanceTrustInvariants$' ./internal/core/` |
| finance-permissions | AC-003, AC-004, AC-018 | `go test -count=1 -v -run '^TestFinancePermissions$' ./internal/core/` |
| trust-notices | AC-021 | `go test -count=1 -v -run '^TestFinanceTrustNotices$' ./internal/core/` |

Also at that SHA: `TestFinanceCrossDomainTransfers` (exit 0). With INSERT on `billing_records` revoked, Apply trust fails after its trust post, and the trust row and its audit rows roll back with it. Trust-funded payments, their reversals, bulk caps and a restart are covered too.

The tests drive the running core over HTTP with real Postgres (a fresh non-superuser role set up like `togavsu_app`) and a loopback SMTP server. Synthetic data only.

## Criteria

Passed globally:

- AC-005: a withdrawal to -250.00 posts and raises a flag. The core's own mail loop sends the flag to the admin and the responsible attorney, and not to staff or the accountant. A refused send stays queued and goes out on a later tick.
- AC-007: the seeded ledger reconciles with zero differences. A statement 12.34 off shows 12.34 on the API, the screen and the report.
- AC-008: an invoice drafts from unbilled time, is approved by an attorney and is sent by system mail with the PDF attached. A send through a refusing relay is undone. The invoice takes part and full payments. After the payments are reversed it voids, and its time is unbilled and redrafts.
- AC-009: a real unpaid invoice and a real trust balance both warn, and the matter closes after acknowledgement.
- AC-026: the matter shows Awaiting trust funds, then Ready to apply. Apply posts one transfer and the matter shows Collected. A second apply is refused, as are a short apply and an accountant's apply.

Pending globally, with what passes now:

- AC-003: no billing or trust surface shows a walled matter to staff: lists, reports, widgets, notifications, search, screens, direct reads, MCP, posting and audit reads. Calendar, files, editor, Doc Tender and module surfaces are not composed into the core yet.
- AC-004: the accountant reads all billing and trust records and screens. 16 billing and trust writes are refused with nothing written. Calendar, files and module writes are not in the core yet.
- AC-006: no HTTP route or MCP tool edits a ledger row, and the core role cannot UPDATE, DELETE or TRUNCATE `trust_ledger`; Compose first run checks this too. Reversals work once and restore the balance. Still pending under contracts ceiling 4: the owning role could grant UPDATE back to itself.
- AC-018: the audit half passes for billing and trust (actor, source, refused writes add no rows). The backup half (download, S3, network path, restore of a running core) is not demonstrated end to end.
- AC-021: the count rises for the responsible attorney on the API and the trust screens, and Mark all read clears it for that user only. The PRD's bell in the shell header on every screen belongs to the ui assignment.

## Visual QC

21 headless Chrome steps at the tested SHA, with no console errors and no 4xx or 5xx responses. Screenshots and the record are in [screens/integrate-finance](screens/integrate-finance/qc.json).

## Simplification and debt

Net Go lines: +1491 (+388 without tests). Debt entries went from 37 to 52, which is +16 recorded and 1 removed:

- 14 are the billing and trust packages' existing ponytails, which had not been entered in the ledger.
- 2 are new: the 30-second mail loop and a routing note.
- The double screen authentication entry was removed: each request now authenticates once.

Registering `api.bulk_batch_max` resolved two package ponytails.

## Not done here

- Files wiring (invoice PDFs as file records, expense receipts) waits for integrate-documents.
- Billing events are not merged into the practice timeline.
- Custom fields on time and expenses, `invoices:bulk` and payment processors are not built.
- The tracking-issue comment could not be posted from this lane (read-only token).

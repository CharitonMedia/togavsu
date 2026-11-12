# QuickBooks module specification

Module id `quickbooks`. Status: specified, not built. This file is the whole build contract for the module lane; every decision is made here.

## 1. Purpose and scope

The firm's accountant keeps the books in QuickBooks Online (QBO). In the core the accountant role is read-only (PRD 4). This module is how the firm's money records reach QBO.

- **Direction:** one-way push from Togavsu to QBO. Togavsu is the system of record. The module never writes to any Togavsu money record, and it holds only two core write scopes: `notifications:write`, which it uses to report problems, and `module_secrets:write`, which keeps its own QuickBooks client secret and tokens in the core's sealed secret store.
- **What it pushes:** billed contacts as Customers, sent invoices as Invoices, write-offs as CreditMemos, completed operating payments as Payments, payment reversals as voids, every trust ledger entry as a JournalEntry between a mapped trust bank account and a mapped client trust liability account, trust-to-operating transfers as the trust side of that move, and (when the admin picks it) expenses as Purchases.
- **Trust is never income.** Trust deposits credit the client trust liability account. Income reaches QBO only through invoices, and through one mapped income account for a trust transfer that pays no invoice.
- **When:** on core events, within seconds, plus a nightly reconcile that finds anything an event missed and anything changed in QBO since the push.
- **Who sees it:** admin and accountant get a home widget and a QuickBooks tab with sync status, exceptions and a read-only books view. Both can retry a failed item or sync now. Only the admin connects QBO, maps accounts and resets the sync. Attorneys and staff see the matter tab.
- **Failures are loud:** a failed item shows on the widget and tab, raises a core notification for admins and accountants, and emails each of them whose notification preference keeps email on, which is the default.

## 2. Source of requirements

Core PRD (togavsu-core-prd.md):

- Section 4: roles. The accountant is read-only over invoices, payments, trust ledgers, reconciliations, billing reports and exports; no notes, files or sensitive contact fields. Walls apply to everyone, admin included.
- Section 5: records this module covers: contacts (billing name, email, address, phone only), matters (number and primary client), invoices with line items (time, expense, flat fee, adjustment), adjustment, interest, write-off, void; payments (destination operating or trust, method, status pending, completed, failed, reversed, applied invoice, related trust entry); expenses (billable, markup, invoice once billed); trust accounts; the append-only trust ledger (deposit, withdrawal, transfer to operating, refund, adjustment, reversal).
- Section 8.3: payments apply to invoices; trust-to-operating transfers apply to invoices and post a matching ledger entry.
- Section 8.4: trust entries, the below-zero rule (entries post and flag; they still sync), monthly reconciliation.
- Section 8.15: flat fee apply moves the fee from trust to operating against an invoice; Apply trust and Record outside payment on sent invoices.
- Section 9 in full: manifest (9.2), signed install and scoped token (9.3), iframe UI with context tokens (9.4), module data in schema `mod_quickbooks` with numbered migrations, foreign keys ON DELETE RESTRICT, no copies of core records, include= and published views (9.9). QuickBooks sync is named in 9.7.

Nolan PMS Unified record shapes the sync must cover (read from src/lib/post-payment.ts, invoice-mutations.ts, trust-mutations.ts, trust-application.ts, lawpay.ts and src/app/api/expenses, invoices, trust):

| Unified record | Togavsu record | QBO result |
| --- | --- | --- |
| payments with destination operating, methods cash, check, lawpay_card, lawpay_ach, internal_transfer, other; pro bono payments at zero | payments (methods card and ach replace the lawpay names) | Payment; a zero payment is skipped |
| payments with destination trust | payments plus a trust ledger deposit | JournalEntry from the ledger row only; the payment row is skipped so trust is never counted twice |
| invoices with time and expense lines, adjustment_amount, interest_charged, void with reason | invoices with lines, adjustment, interest, void | Invoice; void becomes a QBO void |
| invoice import route | bulkInvoice (invoice.created) | Invoice, under the same rules |
| trust_ledger deposit, withdrawal, return, adjustment, reversal_of_id | trust_ledger deposit, withdrawal, refund, transfer to operating, adjustment, reversal | JournalEntry; a reversal posts a mirror JournalEntry |
| trust-application (trust to invoice) | apply-trust and flat-fee apply: operating payment with method internal_transfer and trust_entry_id, plus a transfer_to_operating ledger row | trust side JournalEntry plus a Payment deposited to the operating bank |
| LawPay refunds | payment reversal (reversePayment) | void of the QBO Payment |
| expenses with type, billable, markup, receipt | expenses | invoice lines when billed; a Purchase only in purchases mode |
| Unified void restores applied funds to trust | Togavsu refuses to void an invoice with payments until they are reversed | payment voids reach QBO before the invoice void |

## 3. Manifest

`modules/quickbooks/package/togavsu-module.yaml`, in the field layout of internal/modules/manifest.go:

- id `quickbooks`, name `QuickBooks Sync`, version `"1.0.0"`, author `Togavsu build`, homepage `https://github.com/CharitonMedia/togavsu/tree/main/modules/quickbooks`, license `Same as Togavsu`, price `Free`.
- container: image `ghcr.io/charitonmedia/togavsu-quickbooks`, digest placeholder `sha256:` plus 64 zeros (set to the built image digest before signing), port 8080, events `/events`.

### Permissions (all required)

| Scope | Reason shown to the admin |
| --- | --- |
| `invoices:read` | Read sent invoices and their lines to post them to QuickBooks. |
| `payments:read` | Read completed payments and reversals to post them. |
| `expenses:read` | Read expenses for purchases mode and for invoice line checks. |
| `trust:read` | Read trust accounts and every ledger entry to post them to the trust and liability accounts. |
| `contacts:read` | Receive contact updates so QuickBooks customer names and addresses stay current. |
| `matters:read` | Show a matter's sync state on the matter screen. |
| `events:read` | Subscribe to the events below. |
| `books:read` | Read the firm's money records without a viewer, for the background sync and the nightly reconcile (section 8). |
| `notifications:write` | Tell admins and accountants, by notification and email, when an item fails to sync. |
| `module_secrets:write` | Keep the QuickBooks client secret and sign-in tokens sealed by the core, never in the module's own tables. |

### Widgets

| id | title | path | size | sizes | target | roles |
| --- | --- | --- | --- | --- | --- | --- |
| `sync-status` | QuickBooks sync | `/widget` | 4x2 | 4x2, 6x3 | tab `books`, params `[status]` | admin, accountant |

The widget shows connection state (Connected to "Sample Law Co", Sandbox, Not connected, Reconnect needed), items synced today, items waiting, and exceptions. Each count links to the books tab with `status` set to `synced`, `pending` or `exceptions`.

### Tabs

| id | title | placement | path | who |
| --- | --- | --- | --- | --- |
| `books` | QuickBooks | home | `/books` | admin and accountant; others see a one-line note |
| `setup` | QuickBooks setup | home | `/setup` | admin acts; accountant sees it read-only |
| `matter` | QuickBooks | matter | `/matter` | admin, attorney, staff |

### Sidebar entries

| id | title | path | section | icon | order |
| --- | --- | --- | --- | --- | --- |
| `quickbooks` | QuickBooks | `/books` | `money` | `ledger` | 410 |

### Notifications

| kind | label | default channels |
| --- | --- | --- |
| `sync_problem` | QuickBooks sync problem | in_app, email |

The core names the kind `quickbooks.sync_problem` in each user's notification preferences (section 8, item 7).

### Help guide

Heading `QuickBooks`. Pages (id, title, keywords, covers):

- `overview`, QuickBooks sync, [quickbooks, accountant, books, sync], [quickbooks.sync-status, /modules/quickbooks/tabs/books]
- `connect`, Connecting QuickBooks, [connect, oauth, sandbox, intuit], [/modules/quickbooks/tabs/setup]
- `mapping`, Account and item mapping, [accounts, items, mapping, chart of accounts], [/modules/quickbooks/tabs/setup]
- `exceptions`, Exceptions and retry, [failed, retry, exception, drift], [/modules/quickbooks/tabs/books]
- `trust`, How trust money posts, [trust, iolta, liability, transfer], [/modules/quickbooks/tabs/books, /modules/quickbooks/tabs/matter]

### Event subscriptions

`invoice.*`, `payment.*`, `expense.*`, `trust.entry_posted`, `contact.updated`.

### Data

- extends: `invoice_sync` (record `invoice`, table `invoice_sync`, key `invoice_id`, columns `state`, `synced_at`, `last_error`); `payment_sync` (record `payment`, table `payment_sync`, key `payment_id`, same columns).
- views: `invoice_qbo` (record `invoice`, key `invoice_id`, columns `state`, `synced_at`), so another module (the client portal) can show whether an invoice is in the books under `views:quickbooks.invoice_qbo:read`.

### Settings schema

Rendered by the core as the module's settings form (admin only). A mapping value is a QBO account or item Id (all digits) or its exact full name; the module resolves it and the setup tab shows the result.

| key | type | default | required | secret | meaning |
| --- | --- | --- | --- | --- | --- |
| `qbo_client_id` | string | | yes | no | Client ID of the firm's Intuit developer app |
| `qbo_environment` | enum: sandbox, production | sandbox | yes | no | Which QBO API the connection uses |
| `sync_start_date` | string (YYYY-MM-DD) | | no | no | Records dated before this never sync; empty means the day QBO was first connected |
| `account_operating_bank` | string | | yes | no | Bank account for trust transfers into operating, and for payments in operating_bank mode |
| `account_trust_bank` | string | | yes | no | Default trust bank account; the setup tab can map each Togavsu trust account to its own |
| `account_trust_liability` | string | Client Trust Liability | yes | no | Other Current Liability account holding client trust balances |
| `account_earned_from_trust` | string | Legal Fee Income | yes | no | Income account for a trust transfer to operating that pays no invoice |
| `payment_deposit` | enum: undeposited_funds, operating_bank | undeposited_funds | yes | no | Where client payments land in QBO |
| `expense_push` | enum: invoice_lines_only, purchases | invoice_lines_only | yes | no | Whether expenses also post as Purchases |
| `account_client_costs` | string | Client Costs Advanced | no | no | Expense or Other Current Asset account for purchases mode |
| `account_expense_paid_from` | string | | no | no | Bank or Credit Card account for purchases mode |
| `item_legal_services` | string | Legal Services | yes | no | Item for time lines |
| `item_flat_fee` | string | Flat Fee | yes | no | Item for flat fee lines |
| `item_reimbursed_costs` | string | Reimbursed Costs | yes | no | Item for expense lines |
| `item_adjustment` | string | Invoice Adjustment | yes | no | Item for adjustment lines and the invoice adjustment |
| `item_interest` | string | Late Interest | yes | no | Item for interest |
| `item_write_off` | string | Write-Off | yes | no | Item for write-off credit memos |
| `reconcile_hour_utc` | int | 8 | yes | no | Hour (UTC) the nightly reconcile starts; 8 is 2 or 3 a.m. Central |
| `retry_schedule` | string | 1m,5m,30m,2h,6h | yes | no | Waits between retries of a retryable fault, comma separated; the row fails after the last |
| `alert_interval_minutes` | int | 15 | yes | no | How often new exceptions are gathered into one sync_problem alert |

The Intuit app's client secret is not a setting. The admin enters it in the Client secret field on the setup tab, which stores it through the core secret store as `qbo/client_secret` (section 8, item 8) and afterwards shows only whether it is set. Connect stays disabled until `qbo_client_id` and the client secret are both set.

## 4. Data

Schema `mod_quickbooks`. Ids are TEXT (UUIDv7 for module rows, core ids for links). Times are BIGINT microseconds UTC, as the core stores them. Every link to a core record is a foreign key with ON DELETE RESTRICT. No table holds a copy of a core record: the sync tables hold only QBO ids, states, hashes and errors, keyed to the core record. Table names avoid the reserved list in internal/modules/sqlcheck.go.

**0001_connection.sql**

- `connection`: `id SMALLINT PRIMARY KEY CHECK (id = 1)`, `environment TEXT` (sandbox, production), `realm_id TEXT`, `company_name TEXT`, `access_expires_at BIGINT`, `refresh_expires_at BIGINT`, `state TEXT` (disconnected, connected, reconnect_needed), `connected_by TEXT REFERENCES core.users (id) ON DELETE RESTRICT`, `connected_at BIGINT`, `first_connected_on TEXT`, `last_refresh_at BIGINT`, `last_error TEXT`. No column holds a token. The access and refresh tokens live only in the core secret store as `qbo/access_token` and `qbo/refresh_token` (section 8, item 8), so the module's database login and a backup of its schema never hold a usable token. A token secret missing from the store reads as no connection: the state becomes reconnect_needed and the admins are alerted.
- `oauth_states`: `state TEXT PRIMARY KEY`, `user_id TEXT NOT NULL REFERENCES core.users (id) ON DELETE RESTRICT`, `redirect_uri TEXT`, `expires_at BIGINT`, `used_at BIGINT`.
- `received_events`: `event_id TEXT PRIMARY KEY`, `type TEXT`, `record_id TEXT`, `received_at BIGINT` (private, as in matter-clock).
- `trust_bank_map`: `trust_account_id TEXT PRIMARY KEY REFERENCES core.trust_accounts (id) ON DELETE RESTRICT`, `qbo_account TEXT NOT NULL`, `set_by TEXT REFERENCES core.users (id) ON DELETE RESTRICT`, `set_at BIGINT`.

**0002_sync.sql**

Every sync table carries the same state columns: `realm_id`, `state` (pending, synced, skipped, failed, drift, acknowledged, hidden), `skip_reason`, `waiting_on`, `qbo_id`, `qbo_sync_token`, `pushed_hash` (SHA-256 of the canonical QBO payload last accepted), `request_id` (QBO requestid of the call in flight), `attempts INT`, `next_attempt_at BIGINT`, `last_error_code`, `last_error`, `synced_at`, `updated_at`.

- `customer_sync`: `id TEXT PRIMARY KEY`, `contact_id TEXT NOT NULL REFERENCES core.contacts (id) ON DELETE RESTRICT`, `qbo_display_name TEXT`, `adopted BOOLEAN`, plus the state columns. `contact_id` is indexed but not unique, so a core contact merge (which moves module links in its own transaction) never fails; the module treats two rows on one contact as a drift exception.
- `invoice_sync`: `invoice_id TEXT PRIMARY KEY REFERENCES core.invoices (id) ON DELETE RESTRICT`, `customer_sync_id TEXT REFERENCES customer_sync (id)`, `voided BOOLEAN`, `credit_memo_qbo_id`, `credit_memo_sync_token`, `write_off_cents BIGINT`, `write_off_link_qbo_id` (the zero Payment that applies the credit memo), plus the state columns.
- `payment_sync`: `payment_id TEXT PRIMARY KEY REFERENCES core.payments (id) ON DELETE RESTRICT`, `trust_entry_id TEXT REFERENCES core.trust_ledger (id) ON DELETE RESTRICT` (the linked ledger row when the payment came from trust), `voided BOOLEAN`, plus the state columns.
- `trust_entry_sync`: `trust_entry_id TEXT PRIMARY KEY REFERENCES core.trust_ledger (id) ON DELETE RESTRICT`, `linked_payment_id TEXT REFERENCES core.payments (id) ON DELETE RESTRICT`, `shape TEXT` (two_line, four_line, mirror), plus the state columns.
- `expense_sync`: `expense_id TEXT PRIMARY KEY REFERENCES core.expenses (id) ON DELETE RESTRICT`, plus the state columns.
- `sync_log` (append-only; the module only inserts): `id TEXT PRIMARY KEY`, `at BIGINT`, `realm_id TEXT`, `contact_id`, `invoice_id`, `payment_id`, `trust_entry_id`, `expense_id` (each nullable, each a foreign key to its core table ON DELETE RESTRICT), `CHECK (num_nonnulls(contact_id, invoice_id, payment_id, trust_entry_id, expense_id) = 1)`, `operation` (create, update, void, delete, link, adopt, skip, acknowledge, retry_requested), `outcome` (ok, retry, failed, drift), `http_status INT`, `qbo_fault_code TEXT`, `message TEXT`, `request_id TEXT`, `actor_user_id TEXT REFERENCES core.users (id) ON DELETE RESTRICT` (set when a person pressed Retry, Acknowledge or Sync now).
- `runs`: `id TEXT PRIMARY KEY`, `kind` (nightly, manual), `started_at`, `finished_at`, `examined INT`, `queued INT`, `failed INT`, `drift INT`, `started_by TEXT REFERENCES core.users (id) ON DELETE RESTRICT`.
- `alerts_sent`: `id TEXT PRIMARY KEY`, `at BIGINT`, `kind` (failures, connection, reconnect_soon), `item_count INT`.

**0003_views.sql**

- `CREATE VIEW invoice_qbo AS SELECT invoice_id, state, synced_at FROM invoice_sync;`

Indexes: `(state, next_attempt_at)` on each sync table; `(at)` and each foreign key column on `sync_log`.

## 5. Mapping

Dates: an invoice uses `issued_on`, an expense `incurred_on`; a payment and a trust entry use `created_at` converted to the firm's time zone from `GET /api/v1/books/profile`. Every QBO entity the module creates carries `PrivateNote` (Customer: `Notes`) of the form `Togavsu invoice 0190f3c2-...` so the accountant can trace it. Amounts are integer cents in Togavsu and two-decimal values in QBO; the module converts exactly and never rounds a sum. All calls pin `minorversion=75`. No sales tax: items are non-taxable and no tax detail is sent. The module never calls an Intuit send or email operation; invoices carry `EmailStatus NotSet` and `AllowOnlinePayment false`, so QBO never contacts a client.

| Togavsu record | When it pushes | QBO entity | Fields |
| --- | --- | --- | --- |
| Billed contact (the contact on a pushed invoice, payment or trust entry) | before the first record that needs it; again on `contact.updated` | Customer | DisplayName (organization name or full name; `:` and control characters removed), GivenName, FamilyName, CompanyName, PrimaryEmailAddr, PrimaryPhone, BillAddr (Line1, Line2, City, CountrySubDivisionCode, PostalCode, Country), Notes marker |
| Invoice, status sent, viewed or paid, dated on or after the start date | `invoice.sent`, `invoice.created`, `invoice.updated`, nightly | Invoice | CustomerRef (the matter's primary client), DocNumber = invoice number, TxnDate = issued_on, one line per invoice line with SalesItemLineDetail ItemRef by kind (time: item_legal_services, flat_fee: item_flat_fee, expense: item_reimbursed_costs, adjustment: item_adjustment), Amount, Description; an invoice-level adjustment as one item_adjustment line; interest as one item_interest line; PrivateNote with marker and matter number. Lines plus adjustment plus interest must equal total_cents, or the row fails with `totals_mismatch` |
| Invoice write-off (`write_off_cents` above zero) | `invoice.updated`, nightly | CreditMemo plus a zero Payment | CreditMemo: CustomerRef, TxnDate, one item_write_off line for write_off_cents with the reason as Description; a zero Payment linking the Invoice and the CreditMemo for that amount. A larger later write-off updates both |
| Invoice void | `invoice.voided` | Invoice void | delete the write-off link and CreditMemo if any, then `operation=void` on the Invoice. Runs only after every synced payment on it is voided. A never-synced invoice is skipped |
| Payment, destination operating, status completed, amount above zero | `payment.completed`, nightly | Payment | CustomerRef (the payment's contact), TotalAmt, TxnDate, PaymentRefNum = reference_number (first 21 characters), PaymentMethodRef by method name (cash: Cash, check: Check, card: Credit Card, ach: ACH; omitted when the company lacks it), Line LinkedTxn to the synced Invoice; DepositToAccountRef = Undeposited Funds or account_operating_bank per `payment_deposit`, and always account_operating_bank for method internal_transfer |
| Payment, destination trust | never | none | skipped with reason `posted from trust ledger entry`; its ledger row posts instead |
| Payment, reversed | `payment.reversed`, nightly | Payment void | `operation=update&include=void` on the synced Payment; a never-synced payment is skipped |
| Trust deposit | `trust.entry_posted`, nightly | JournalEntry | Debit trust bank, Credit trust liability with Entity = client Customer |
| Trust withdrawal or refund | same | JournalEntry | Debit trust liability (Entity client), Credit trust bank |
| Trust adjustment | same | JournalEntry | positive: Debit trust bank, Credit liability; negative: the reverse |
| Trust transfer to operating, paying an invoice (a payment exists with this `trust_entry_id`) | same | JournalEntry (two lines) | Debit trust liability (Entity client), Credit trust bank. The operating side is the linked Payment, deposited to account_operating_bank and applied to the invoice |
| Trust transfer to operating, paying no invoice | same | JournalEntry (four lines) | Debit trust liability (Entity client), Credit trust bank, Debit account_operating_bank, Credit account_earned_from_trust |
| Trust reversal | same | JournalEntry (mirror) | the reversed entry's JournalEntry with every debit and credit swapped |
| Expense, purchases mode only | `expense.created`, `expense.updated`, nightly | Purchase | AccountRef = account_expense_paid_from, PaymentType Cash or CreditCard by that account's type, one AccountBasedExpenseLineDetail line to account_client_costs with CustomerRef (the matter's client) and BillableStatus Billable or NotBillable, TxnDate = incurred_on |
| Expense, invoice_lines_only mode | never on its own | none | reaches QBO as an item_reimbursed_costs line of the invoice that bills it |

A transfer to operating is linked or not by listing the matter's payments for the entry's date through the books reader and looking for one whose `trust_entry_id` is the entry; apply-trust and flat-fee apply write both rows in one core transaction, so the answer is final when the event arrives. A zero-amount trust entry is skipped.

Trust bank account: the `trust_bank_map` row for the entry's trust account, else `account_trust_bank`. Walled matters: the books reader honors walls like any other principal (section 8, item 2), so a walled matter's invoices, payments, expenses and trust entries, the matter itself and its client linkage never reach the module and are never pushed to QBO. A contact whose every matter is walled gets no Customer. When the wall is lifted, the records sync on the next reconcile (section 9).

Account checks before any push, from the resolved QBO account's AccountType: trust bank and operating bank are Bank; trust liability is Other Current Liability; earned from trust is Income; client costs is Expense, Other Expense, Cost of Goods Sold or Other Current Asset; paid-from is Bank or Credit Card; items are Service or NonInventory. A trust bank resolving to anything but Bank, or the liability resolving to anything but Other Current Liability, is a mapping error and no trust entry posts.

Customer matching on first push: if a QBO Customer has the exact DisplayName and its Notes carry no other Togavsu contact id, the module adopts it (logged `adopt`). If the name is taken by a Vendor or Employee (QBO fault 6240), the module creates `Jane Sample (0190f3c2)`.

## 6. Adapter

Package `modules/quickbooks/qbo`. The sync code depends only on these interfaces; the HTTP adapter talks to Intuit and the fake keeps everything in memory.

```go
type Auth interface {
	AuthCodeURL(state, redirectURI string) string
	Exchange(ctx context.Context, code, redirectURI string) (Tokens, error)
	Refresh(ctx context.Context, refreshToken string) (Tokens, error)
	Revoke(ctx context.Context, token string) error
}

type Books interface {
	Company(ctx context.Context) (Company, error)
	Accounts(ctx context.Context) ([]Account, error)
	Items(ctx context.Context) ([]Item, error)
	PaymentMethods(ctx context.Context) ([]PaymentMethod, error)
	CustomerByName(ctx context.Context, displayName string) (*Customer, error)
	Create(ctx context.Context, e Entity, requestID string) (Ref, error)
	Update(ctx context.Context, e Entity, requestID string) (Ref, error)
	Void(ctx context.Context, kind Kind, ref Ref, requestID string) (Ref, error)
	Delete(ctx context.Context, kind Kind, ref Ref, requestID string) error
	Read(ctx context.Context, kind Kind, id string) (Entity, Ref, error)
	Changes(ctx context.Context, kinds []Kind, since time.Time) ([]Change, error)
	CreateAccount(ctx context.Context, a Account, requestID string) (Ref, error)
	CreateItem(ctx context.Context, i Item, requestID string) (Ref, error)
}
```

- `Tokens` is `{AccessToken, RefreshToken, AccessExpiresAt, RefreshExpiresAt, RealmID}`. `Ref` is `{ID, SyncToken}`. `Kind` is Customer, Invoice, Payment, CreditMemo, JournalEntry or Purchase. `Entity` is a typed payload per kind. `Change` is `{Kind, ID, SyncToken, Deleted, UpdatedAt}`. A `Books` value is bound to one realm and environment by `qbo.Open(conn, tokenSource)`.
- Errors are `*Fault{HTTPStatus, Code, Message, Retryable}`. Retryable: 429, 500, 502, 503, 504, network errors and timeouts. Not retryable: validation and business faults (for example 6240 duplicate name, 6000 business rule), 5010 stale SyncToken (handled by one read and one retry), 610 object not found (becomes drift). A 401 makes the caller refresh once and repeat.
- **HTTP adapter** (`qbo/http.go`): base URL `https://quickbooks.api.intuit.com` or `https://sandbox-quickbooks.api.intuit.com` by environment, path `/v3/company/{realmId}/{entity}?minorversion=75`; authorize at `https://appcenter.intuit.com/connect/oauth2` with scope `com.intuit.quickbooks.accounting`; token and refresh at `https://oauth.platform.intuit.com/oauth2/v1/tokens/bearer` with HTTP Basic client id and secret; revoke at `https://developer.api.intuit.com/v2/oauth2/tokens/revoke`. Every create, update, void and delete sends `requestid` (a UUIDv5 of the record id, operation and payload hash, under 50 characters), and QBO answers a repeated requestid with the first response, so a retried call is processed once. Changes uses `GET /cdc?entities=...&changedSince=` (QBO looks back at most 30 days). A client-side limiter holds each realm to 400 requests a minute and 4 at once (QBO allows 500 a minute and 10 at once). Base URLs are adapter config so its tests point at an httptest server.
- **Fake** (`qbo/fake`): `fake.New()` returns one value implementing Auth and Books. It keeps companies, accounts, items, payment methods and entities in maps; enforces DisplayName uniqueness across names (6240), SyncToken concurrency (5010), balanced JournalEntries on known accounts (6000), void semantics (amounts zero, record kept), requestid replay (same requestid returns the first response), and records a change log for Changes. It issues access tokens that expire after one hour of the test clock and rotates the refresh token on every refresh. Hooks: `FailNext(kind, *Fault)`, `Throttle(n)`, `Timeout(kind)` (applies the write and then returns an error, to prove requestid replay), `RevokeRefresh()` (the next refresh returns invalid_grant), `EditAsAccountant(kind, id)` and `DeleteAsAccountant(kind, id)` for drift, `Calls()` for assertions, `Seed(...)` for a synthetic chart of accounts, and `Balances()` per account. No test calls Intuit.

## 7. Core API calls it uses

Background calls are service calls: the module token with no context token, run as the module's service principal (section 8, item 2). Because the module holds `books:read`, that principal is the books reader for the money reads. The books reader sits outside every matter wall. Viewer calls carry the frame's context token, so the answer is exactly what that viewer may see.

| Method and path | Scope | Used for | Caller |
| --- | --- | --- | --- |
| GET /api/v1/modules/quickbooks/settings | own module (no scope) | client id, environment, mapping, modes | background |
| PUT, GET, DELETE /api/v1/modules/quickbooks/secrets/{name} | module_secrets:write | `qbo/client_secret`, `qbo/access_token`, `qbo/refresh_token` | background (the setup tab's form post stores the client secret with the module's own token) |
| POST /api/v1/modules/quickbooks/context-tokens/verify | own module | who is looking at a frame | viewer |
| GET /api/v1/books/profile | books:read | firm time zone and name | background |
| GET /api/v1/books/matters/{matter_id} | books:read | matter number, primary client | background |
| GET /api/v1/books/contacts/{contact_id} | books:read | customer name, email, phone, billing address | background |
| GET /api/v1/invoices, GET /api/v1/invoices/{invoice_id} | invoices:read + books:read | invoices to push and reconcile | background |
| GET /api/v1/payments, GET /api/v1/payments/{payment_id} | payments:read + books:read | payments and their links to trust entries | background |
| GET /api/v1/expenses, GET /api/v1/expenses/{expense_id} | expenses:read + books:read | purchases mode | background |
| GET /api/v1/trust-accounts, GET /api/v1/trust-accounts/{trust_account_id} | trust:read + books:read | account names for the setup map | background and viewer |
| GET /api/v1/trust-ledger, GET /api/v1/trust-ledger/{trust_entry_id} | trust:read + books:read | ledger entries to push and reconcile | background |
| GET /api/v1/trust/balances | trust:read + books:read | nightly totals check | background |
| POST /api/v1/notifications | notifications:write | failure and connection alerts: kind `sync_problem`, `recipient_roles` admin and accountant (admin alone for connection alerts) | background |
| GET /api/v1/invoices?include=quickbooks.invoice_sync | invoices:read | books view | viewer |
| GET /api/v1/payments?include=quickbooks.payment_sync | payments:read | books view | viewer |
| GET /api/v1/trust-ledger, GET /api/v1/expenses | trust:read, expenses:read | books view | viewer |
| GET /api/v1/matters/{matter_id} | matters:read | matter tab header | viewer |

The module calls no other core endpoint and no core write except POST /api/v1/notifications and its own secrets.

## 8. Core changes

The module-core lane builds these from docs/modules/CORE-CHANGES.md. Each item names its merged definition there, which is the one built.

The core today runs a module token with no context token as a principal with no user and no roles (internal/domain/access.go Decide returns unauthenticated), so every read above is refused without a viewer, and the background sync and nightly reconcile cannot run on the core as it stands. These changes are prerequisites:

1. **Scope `books:read` (CORE-02).** Add to api/contract/scopes.py and internal/identity/scopes.go: "Read the firm's invoices, payments, expenses, trust accounts and trust ledger, and the billing details of billed contacts, firm-wide except walled matters and without a viewer, for a module's background money work." Grantable only to a module token; an ordinary API token request for it is refused.
2. **Service calls and the books reader (CORE-01, CORE-02).** A new `module_delegation` value `delegated_viewer_or_service_principal` on listInvoice, getInvoice, listPayment, getPayment, listExpense, getExpense, listTrustAccount, getTrustAccount, listTrustLedger (GET /api/v1/trust-ledger), getTrustEntry (GET /api/v1/trust-ledger/{trust_entry_id}) and getTrustBalances (GET /api/v1/trust/balances). A module token holding `books:read` and the operation's own read scope, calling with no context token, makes a service call that runs as the books reader: the module's service principal with the accountant role's field set, firm-wide, with matter walls applied exactly as for a user outside every wall. A walled matter's invoices, payments, expenses and trust entries are left out of every list and not_found on every get, and getTrustBalances leaves out their amounts, so no text, count or total from a walled matter reaches the module. When the wall is lifted, the records come back on the next read with no other change. Every service call is audited under the module id with source `service`. The books reader can call no write operation (scope_denied).
3. **Endpoint GET /api/v1/books/profile (CORE-02)** (books:read): `{firm_name, time_zone, currency}`, currency always `USD`.
4. **Endpoint GET /api/v1/books/matters/{matter_id} (CORE-02)** (books:read): `{id, number, name, status, client_contact_id, responsible_attorney_user_id}`; a walled matter is not_found; `client_contact_id` is the primary client party. This module ignores `responsible_attorney_user_id`.
5. **Endpoint GET /api/v1/books/contacts/{contact_id} (CORE-02)** (books:read): `{id, kind, display_name, given_name, family_name, organization_name, email, phone, billing_address {line1, line2, city, region, postal_code, country}, archived}` from the contact's primary current email, phone and address. Only a contact that is the client of a matter without a wall, or the contact on a payment or trust entry the books reader can read, is readable; any other is not_found, including a contact whose every matter is walled. No date of birth, identifier, tags or custom fields.
6. **Event delivery to a books reader (CORE-02).** `invoice.*`, `payment.*`, `expense.*` and `trust.*` events on a walled matter are dropped for a module holding `books:read`, as for any principal outside the wall. Lifting a wall sends no catch-up events; the next reconcile finds the records (section 9, Reconcile). The derived read scope rule is unchanged.
7. **Module notification kinds and POST /api/v1/notifications (CORE-03).** The route is in the contract with no Go route today. The manifest's `notifications` list declares kind `sync_problem`, which the core names `quickbooks.sync_problem`, with default channels in_app and email, in a QuickBooks group of the notification preferences grid. NotificationWrite takes `recipient_roles` (a non-empty subset of admin and accountant) in place of `user_id`, accepted only on a service call from a module holding `notifications:write`. The core creates one notification per active user holding a named role and emails each recipient whose preference for the kind keeps email on. `link` must be a `/modules/{module_id}/` page of the calling module.
8. **Module secret store (CORE-06).** `PUT`, `GET` and `DELETE /api/v1/modules/{module_id}/secrets/{name}` with new scope `module_secrets:write`, callable only by the module's own token. Values are sealed with the core key like secret settings, and the audit log records only the name. This module stores `qbo/client_secret`, `qbo/access_token` and `qbo/refresh_token`.
9. **Linkable trust accounts (CORE-22).** Add `trust_account` (`trust_accounts`) to `coreRecords` in `internal/modules/manifest.go`, so `trust_bank_map`'s foreign key to `core.trust_accounts` passes the install check.
10. **Test support: no harness change (CORE-29).** The acceptance suite is `internal/modules/module_quickbooks_pg_test.go` in package `modules_test` and uses the existing harness as it is (section 10). CORE-29 extends `compose` so the money, trust, notification and secret routes this module calls are served in the test core.
11. **Sidebar placement (CORE-08).** Manifest `sidebar` entries gain `section`, `icon` and `order`. This module's entry sits in section `money` with icon `ledger` at order 410, in place of the fixed Activity section and puzzle icon.
12. **Frame sandbox (CORE-09).** The staff frame sandbox gains `allow-popups` and `allow-popups-to-escape-sandbox`, so the setup tab's Connect button opens Intuit's consent page in its own top-level window. Frames never get `allow-same-origin`.

## 9. Behaviors and acceptance criteria

### Connection

- The setup tab shows the redirect URI to register in the Intuit app, `https://{host the tab was loaded from}/m/quickbooks/oauth/callback`, then a Connect button. Connect opens a new top-level window (Intuit will not load in a frame) to `/m/quickbooks/oauth/start?nonce=...`; the nonce is a one-use `oauth_states` row bound to the admin who pressed it, valid 10 minutes. The callback checks state, exchanges the code with the client secret read from `qbo/client_secret`, stores the tokens through the core secret store, stores `realm_id` and the company name, and records `first_connected_on`.
- Access tokens refresh when under 5 minutes remain; every refresh stores the new refresh token in `qbo/refresh_token`. An `invalid_grant` sets reconnect_needed, pauses all pushes (items stay pending) and alerts admins at once. Fourteen days before `refresh_expires_at` the admins get a reconnect reminder.
- Disconnect (admin) revokes the refresh token at Intuit and deletes both token secrets; sync rows stay.
- Connecting a realm other than the one the sync rows belong to is refused until the admin runs Reset sync, which needs the typed word RESET, clears the five sync tables and keeps `sync_log`.
- Sandbox: with `qbo_environment` sandbox every module page shows a Sandbox company banner, and the widget says Sandbox.

### Push

- An event is verified (HMAC of the raw body, timestamp within 5 minutes), recorded once in `received_events`, and turned into an upsert of the record's sync row to pending with `next_attempt_at` now. A new event on a failed row resets its attempts.
- One worker pushes due rows every 5 seconds in dependency order: customers, invoices, write-offs, payments, trust entries, expenses, then voids. A row waiting on another (a payment on an unsynced invoice, an invoice void with an unvoided payment) stays pending with `waiting_on` set and does not count an attempt. A payment on an invoice dated before the start date pushes unapplied.
- The module builds the QBO payload, hashes it, and pushes only when the hash differs from `pushed_hash`. It creates when there is no `qbo_id`; otherwise it sends a full update with the stored SyncToken. On 5010 it reads the current SyncToken; a SyncToken it did not write is drift.
- Retry schedule for retryable faults: the `retry_schedule` setting, by default 1 minute, 5 minutes, 30 minutes, 2 hours, 6 hours. After the schedule's last retry fails (the fifth failure by default), or at once for a non-retryable fault, the row is failed. A changed schedule applies from the next retry, with no restart. The nightly reconcile tries each failed row once more.
- Before a create the module stores `request_id`; a retry reuses it, so a timeout after QBO accepted the write never makes a second entity.
- Every push and retry reads its record through the books reader first. A record that read returns as not_found is on a walled matter (core money records are never deleted), and its row moves to state `hidden`: the module makes no QBO call for it and clears `waiting_on`, `attempts`, `last_error_code` and `last_error`. A row synced before its matter was walled stays as it is in QBO, and the module makes no further call for it while the wall stands. A customer row whose contact the books reader returns as not_found is hidden the same way.

### Reconcile

- Nightly at `reconcile_hour_utc`, and on Sync now: list every invoice (sent, viewed, paid, void), payment, trust entry and (purchases mode) expense dated on or after the start date through the books reader, mark pending any record with no sync row or whose payload hash differs, then push. The reconcile is a full read of every record, not a cursor of changes since the last run, and sync state is kept per record, so a record the books reader could not see before is found as soon as it is visible. A `hidden` row whose record the books reader returns again goes back to pending; the push then compares hashes as usual, so a record synced before the wall and unchanged since returns to synced with no QBO call, and one that changed under the wall is updated. Records on a matter whose wall is lifted therefore sync on the next nightly run or Sync now. A row whose record the full read no longer returns moves to `hidden`.
- Drift: read QBO changes since the last run (by Changes when the last run is under 30 days old, else by reading each synced entity). A synced entity whose SyncToken moved without the module, or that was deleted, becomes drift. Customers are not drift-checked; the module rewrites only the customer fields it owns. The full read runs before the drift check, and hidden rows are not drift-checked.
- Totals check: for each trust account, the sum of synced trust JournalEntry nets must equal the core ledger total for the same entries; a difference is an exception naming the account and amount.
- An imported invoice whose amount paid exceeds its payment records raises exception `paid_without_payments`; it still pushes, and the exception tells the accountant QBO will show a balance.

### Alerts

- Failed and drift rows are exceptions; a hidden row never is. Every `alert_interval_minutes` (15 by default), if new exceptions exist, the module sends one `quickbooks.sync_problem` notification to admins and accountants (`recipient_roles`), linked to `/modules/quickbooks/tabs/books?status=exceptions`; each recipient whose preference keeps email on, which is the default, also gets it by email. Connection loss alerts at once. Each send is recorded in `alerts_sent`.

### Screens

- Books tab: status line, exceptions list (record, what failed, QBO fault text, attempts, Retry, and for drift Re-push or Acknowledge), and a read-only books view by month: invoices, payments, trust entries and expenses with amount, QBO state and QBO id, fetched with the viewer's own context token and joined to the module's rows, with totals per month for Togavsu and for what reached QBO.
- Retry, Re-push, Acknowledge and Sync now are module form posts that carry the context token; the module verifies it, requires admin or accountant, changes only its own rows and writes a `sync_log` row with `actor_user_id`. Acknowledge keeps a drift row quiet until Togavsu changes the record again.
- Setup tab (admin acts): connect and disconnect, every mapping with the QBO account or item it resolved to and whether its type is right, a list of QBO accounts and items with their Ids to copy into settings, the trust bank map per Togavsu trust account, Create missing accounts and items (creates the defaults named in section 3 that do not exist, never a bank account), and Reset sync.
- Matter tab: the matter's invoices, payments and trust entries with QBO state.
- Walled matters: the widget and every tab show nothing about a walled matter to any viewer, not even a count. Every count and list leaves out hidden rows, and before it shows or counts a row the module confirms the row's record through the books reader, listing the period or matter shown as the reconcile does; a record the books reader does not return moves its row to hidden and is left out. The books view lists only records that both the viewer's context token and the books reader return.

### Acceptance criteria

All run against the fake QBO and synthetic records (firm Sample Law Co, client Jane Sample, matter 2026-0042).

- **MOD-QBO-01** The signed package installs through the module runtime; the admin sees the ten scopes with their reasons; declining `books:read` cancels the install; schema `mod_quickbooks` exists with migrations 0001 to 0003 recorded.
- **MOD-QBO-02** The install check accepts every migration; every core link is a foreign key ON DELETE RESTRICT, and hard-deleting a synced core contact is refused naming the module.
- **MOD-QBO-03** Widget `sync-status` has target tab `books`; its counts link to `status=synced`, `pending` and `exceptions`.
- **MOD-QBO-04** OAuth: start, callback and exchange against the fake store the tokens only through `putModuleSecret` as `qbo/access_token` and `qbo/refresh_token`; no `mod_quickbooks` column, log line or HTTP response holds a token or the client secret; Connect opens its own top-level window from the frame; a reused or expired state is refused; a non-admin context token cannot start a connection.
- **MOD-QBO-05** An expired access token refreshes before the next push and the rotated refresh token is stored; `invalid_grant` sets reconnect_needed, pauses pushes and alerts admins at once.
- **MOD-QBO-06** A sent invoice with time, expense, flat fee and adjustment lines, an invoice adjustment and interest posts one QBO Invoice whose lines, items and total match to the cent, with DocNumber equal to the invoice number and EmailStatus NotSet.
- **MOD-QBO-07** A draft invoice never reaches QBO; an invoice dated before the start date is skipped with a reason.
- **MOD-QBO-08** Trust deposit never posted as income: a deposit of 150000 cents posts a JournalEntry debiting the trust bank and crediting the trust liability with Entity Jane Sample; no line of any trust JournalEntry touches an Income account except the four-line shape's earned line, and the fake's income balances are unchanged by the deposit.
- **MOD-QBO-09** A payment with destination trust is skipped and only its ledger deposit posts, so the trust bank rises once.
- **MOD-QBO-10** Apply trust of 50000 cents to a sent invoice posts a two-line trust JournalEntry and a Payment of 50000 deposited to the operating bank and applied to the invoice; the QBO invoice balance drops by 50000.
- **MOD-QBO-11** A flat fee apply posts the same pair against its flat-fee invoice.
- **MOD-QBO-12** A transfer to operating with no linked payment posts the four-line JournalEntry with the earned-from-trust income account.
- **MOD-QBO-13** Withdrawal, refund, positive and negative adjustment and a reversal each post the JournalEntry shape in section 5; a reversal mirrors the original exactly.
- **MOD-QBO-14** A below-zero withdrawal posts normally; the module neither blocks nor skips it.
- **MOD-QBO-15** A completed operating payment posts with the deposit account `payment_deposit` names and links to the synced invoice; a payment whose invoice is not yet synced waits and posts after it.
- **MOD-QBO-16** A reversed payment voids its QBO Payment; a reversed payment never synced is skipped.
- **MOD-QBO-17** Voided invoice: after its payments are reversed and voided in QBO, the QBO Invoice is voided; a write-off credit memo and its link on that invoice are deleted first; a draft voided before sync makes no QBO call.
- **MOD-QBO-18** A write-off of 12500 cents posts a CreditMemo and a zero Payment applying it; raising the write-off to 20000 updates both; the QBO invoice balance equals Togavsu's balance due.
- **MOD-QBO-19** Duplicate event: the same event delivered twice is recorded once and makes one QBO create; two different events for one record coalesce into one push.
- **MOD-QBO-20** Retry after a failed push: a 503 on the first push retries on schedule and succeeds; five 503s make the row failed and raise one `quickbooks.sync_problem` notification to each admin and accountant, emailed to each whose preference keeps email on (the default); the accountant's Retry posts it and logs the accountant as actor.
- **MOD-QBO-21** Timeout after QBO applied the write: the retry with the same requestid returns the first entity and QBO holds one invoice.
- **MOD-QBO-22** A mapping error (trust liability mapped to an Income account) fails every trust entry at once with code `mapping_invalid` and posts nothing; those rows retry by themselves once the setting is fixed.
- **MOD-QBO-23** Accountant cannot mutate core records: the module's approved scopes hold no write scopes but `notifications:write` and `module_secrets:write`; with the module token, and with an accountant context token, POST /api/v1/payments, POST /api/v1/trust-ledger, PATCH /api/v1/invoices/{id} and POST /api/v1/invoices/{id}/void are refused and the core rows are unchanged; Retry, Re-push and Acknowledge change only `mod_quickbooks` rows.
- **MOD-QBO-24** A staff or attorney context token cannot Retry, Acknowledge, Sync now or Reset; only an admin can Connect, Disconnect, Create missing and Reset.
- **MOD-QBO-25** The nightly reconcile finds a record whose event was never delivered and pushes it; a second run with no changes makes no QBO write.
- **MOD-QBO-26** Drift: an invoice edited in QBO by the fake's accountant hook becomes drift with an exception; Re-push restores Togavsu's version; Acknowledge silences it until the Togavsu invoice changes.
- **MOD-QBO-27** Customer: the first push creates the customer; an existing QBO customer with the same name and no other marker is adopted; a name taken by a vendor gets the id suffix; `contact.updated` updates the name and address.
- **MOD-QBO-28** Walled matter: a second matter, 2026-0043, is walled to one attorney, and its client is on no other matter. A sent invoice, a completed operating payment and a trust deposit on it never reach the module: the books reader's lists leave them out, its gets and GET /api/v1/books/matters/{id} return not_found, GET /api/v1/books/contacts/{id} for its client returns not_found, getTrustBalances leaves out the deposit, no event for them reaches `/events`, `mod_quickbooks` holds no row for them, and after a nightly reconcile the fake QBO has recorded no call and holds no entity for them. The widget, books tab, matter tab and alerts show no row or count for them to an admin, the accountant or the attorney on the wall. An invoice on 2026-0042 synced before an admin walls that matter moves to hidden at the next reconcile, leaves every count, and gets no QBO call when it changes under the wall. Lifting both walls and pressing Sync now pushes the 2026-0043 customer, invoice, payment and deposit with whole descriptions, and updates the 2026-0042 invoice in QBO.
- **MOD-QBO-29** Purchases mode posts each expense once as a Purchase to the client costs account; invoice_lines_only mode posts none.
- **MOD-QBO-30** Sandbox: with `qbo_environment` sandbox the adapter uses the sandbox base URL and every page shows the banner.
- **MOD-QBO-31** A realm switch is refused until Reset sync with RESET; Reset clears the sync tables and keeps the log.
- **MOD-QBO-32** GET /api/v1/invoices/{id}?include=quickbooks.invoice_sync returns the sync state for an accountant, and not_found for a walled matter the viewer is off.
- **MOD-QBO-33** Totals check: a JournalEntry altered in the fake to a different amount raises a totals exception naming the trust account and the difference.
- **MOD-QBO-34** Tunable settings: `retry_schedule` and `alert_interval_minutes` show on the admin settings page with the defaults above; changing `retry_schedule` changes the next retry's time with no restart, and changing `alert_interval_minutes` changes when the next alert can go out.

## 10. Tests

Unit tests live under modules/quickbooks and the acceptance suite lives in internal/modules. Both run in the existing "Core tests" CI job: scripts/ci/test-core.sh runs `go vet ./...` and `go test` over `go list ./...`, which includes both, against real Postgres via TOGAVSU_TEST_DATABASE_URL. No CI file changes.

- **Unit (no database):** `mapping_test.go` for every row of section 5, cent conversion, hash stability, DisplayName cleaning and account type checks; `qbo/http_test.go` drives the HTTP adapter against an httptest server with synthetic QBO JSON (requestid sent, minorversion 75, 401 then refresh, 429 backoff, fault parsing, limiter); `qbo/fake/fake_test.go` proves the fake enforces what section 6 says.
- **Acceptance (`internal/modules/module_quickbooks_pg_test.go`, package `modules_test`):** uses the existing harness in `internal/modules/harness_pg_test.go` (`newEnv`, `approveAll`, `contextFor`, `moduleDB`) with no change to it (section 8, item 10). Before install each test sets `e.rt.handlers["quickbooks"]` to a handler that, on its first request after each Deploy, reads `e.rt.last("quickbooks")` for the deployment's secrets and builds `quickbooks.New(Config{...})` with the module's own database login from `e.moduleDB`, the core test server URL and `qbo/fake`. Each test builds the package with `quickbooks.Build` and a generated Ed25519 key, adds the public key as a trusted local key, installs through POST /api/v1/modules and approves the scopes. Tests create synthetic money through the core API (invoices, payments, trust posts, apply trust, flat fee apply, void, write-off, reversals), let core events reach `/events`, drive the worker with an injected clock, and assert on the fake's state and the module's tables. One test per criterion, named `TestMODQBO01Install` and so on. They skip only when TOGAVSU_TEST_DATABASE_URL is unset outside CI, as the harness does.
- No test reaches the network beyond httptest servers; the HTTP adapter's base URLs are always overridden in tests.

## 11. Code layout

```
modules/quickbooks/
  README.md                 what it does, build, sign, install
  Dockerfile                as examples/matter-clock/Dockerfile, building ./modules/quickbooks/cmd/quickbooks
  limits.json               tunables: limiter, worker tick, state lifetime
  package.go                embeds package/; Files, Archive, Build, SignFile (matter-clock's signing path)
  server.go                 routes: /widget /books /setup /matter /events /oauth/start /oauth/callback
                            /retry /acknowledge /sync-now /reset /disconnect /create-missing /trust-bank-map /healthz
  core.go                   core API client (module token, optional context token)
  worker.go                 queue, dependency order, retry schedule, alerts
  reconcile.go              nightly and manual runs, drift, totals check
  mapping.go                pure payload builders and hashing
  connect.go                OAuth start, callback, refresh, disconnect; client secret and tokens through the core secret store
  store.go                  mod_quickbooks reads and writes
  templates/                widget and tab HTML
  qbo/adapter.go            interfaces, types, faults
  qbo/http.go               Intuit adapter
  qbo/fake/fake.go          in-memory QBO
  cmd/quickbooks/main.go    `quickbooks serve` and `quickbooks package -key signing.key -out quickbooks.signed.json`
  package/togavsu-module.yaml
  package/migrations/0001_connection.sql
  package/migrations/0002_sync.sql
  package/migrations/0003_views.sql
  *_test.go                 unit tests only

internal/modules/module_quickbooks_pg_test.go   section 10 acceptance suite
```

- Go standard library plus existing dependencies (pgx). No new dependency and no change to go.mod.
- Small fixed numbers follow the AC-030 convention matter-clock uses; tunables live in limits.json. Code is gofmt clean.
- Signing: `quickbooks package` signs with an Ed25519 seed through internal/marketplace (Sign, Digest, KeyID, SignedPackage), exactly as matter-clock; the admin installs the signed file locally or from the marketplace.
- The lane edits only modules/quickbooks/, docs/modules/quickbooks/ and the one suite file internal/modules/module_quickbooks_pg_test.go. It does not edit .github/, go.mod, protected control files or any other file in a core package. Section 8 items go to the module-core lane through docs/modules/CORE-CHANGES.md.

## 12. Out of scope

- Any write from QBO back into Togavsu, and any QBO webhook endpoint (drift is found by the nightly change read). A later version that takes Intuit webhooks would receive them through the core's inbound webhook route (CORE-07).
- Opening balances and history before the start date; the accountant books those in QBO.
- Bank feeds, grouping Undeposited Funds into bank deposits, and bank reconciliation inside QBO.
- Firm overhead expenses, payroll, vendors and bills; the core has no such records.
- Sales tax, multicurrency, classes, locations and QBO sub-customers per matter.
- Emailing invoices or taking payments through QBO.
- QuickBooks Desktop.
- Time entries on their own (they reach QBO as invoice lines).

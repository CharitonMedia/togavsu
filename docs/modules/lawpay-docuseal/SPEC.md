# Module spec: lawpay-docuseal

Module id `lawpay-docuseal`. Schema `mod_lawpay_docuseal`. Core PRD section 9.8, item 8. Every decision is made here, so the build runs from this spec with no pauses.

**Where this module builds (Nov 14, 2026).** This module builds in its own repository, CharitonMedia/togavsu-lawpay-docuseal (modules PRD section 2), not in the core repository. Read each path below under `modules/lawpay-docuseal/` as the same path at that repository's root, `cmd/lawpay-docuseal/` as its `cmd/`, and `internal/modules/module_lawpay_docuseal_pg_test.go` as that repository's Postgres acceptance suite. The suite runs on `pkg/modulekit/coretest` from the public module kit (modules PRD section 5), whose counterparts of the harness names used below (`newEnv`, `e.rt.handlers`, `e.rt.last`, `approveAll`, `contextFor`, `moduleDB`) are listed in docs/modules/MODULE-KIT.md. Packaging and signing go through `pkg/modulekit`, never `internal/marketplace`. "Lane" below means this module's build in its repository. The repository's go.mod requires the core module for the kit and only dependencies the core's go.mod pins. The spec itself stays here, at docs/modules/lawpay-docuseal/SPEC.md.

## 1. Purpose and scope

The module takes LawPay card and ACH payments into the firm's operating and trust accounts, issues full and partial refunds, and reconciles LawPay's records against the core's. It sends fee agreements for e-signature through DocuSeal and files the signed copy on the matter.

Money never moves outside the core. A LawPay charge becomes a core payment through the core billing payment API. A charge that landed in the LawPay trust account becomes a core trust deposit through the same API with destination trust, and the core posts the ledger row. A refund becomes a core payment refund (section 7, C6). The module never reads or writes a core table; it keeps its own LawPay and DocuSeal records, linked to core ids by foreign key. Money is integer cents everywhere, and LawPay amounts are integer cents too, so no value is converted to dollars and back.

The client portal module shows Pay, Make a deposit and Sign from the three views in section 4.4 and opens this module's `pay` and `sign` client tabs. The contract between the two is client-portal SPEC section 7; this spec implements it exactly.

## 2. What Unified does today

Unified's working approach carries over, written clean. Paths are relative to `nolan-pms-unified/`.

| # | Unified behavior | Source | In this module |
| --- | --- | --- | --- |
| U1 | LawPay gateway wrapper: Basic auth with the secret key, base `https://api.8am.com/v1`, charge to an account id, full or partial refund on a charge, read a transaction; errors carry LawPay's message, code and HTTP status. | `src/lib/lawpay.ts` | LawPay adapter (section 5), same four calls and error shape. |
| U2 | Separate operating and trust LawPay accounts and a public key for hosted card fields; account ids in firm settings, keys in environment variables. | `src/db/schema/firm-settings.ts`, `src/lib/lawpay.ts` | Admin-entered module settings; keys are secret settings; the two accounts stay separate. |
| U3 | Client trust deposit: an HMAC-signed pay session (30 minutes) carries the server-decided amount and links; hosted fields tokenize the card; the server charges the trust account and, in one transaction, writes the ledger deposit and the payment and marks the named replenishment request satisfied. Amount $1.00 to $50,000.00, prefilled from the client's active replenishment request. | `src/app/api/portal/pay/init/route.ts`, `src/app/api/portal/pay/route.ts`, `src/lib/portal/pay-session.ts` | A single-use pay intent row replaces the signed envelope (server-decided amount, 30 minutes), created when the `pay` client tab loads. The portal passes the active request's `replenishment_request_id`, and the amount is then that request's amount. The charge runs through core processor initialization and `createPayment`; the core posts the ledger row and satisfies the request (C5). Same limits, as settings. |
| U4 | The portal's Pay button on an invoice opens a trust deposit with the balance prefilled; funds land in trust and are applied to the invoice by a deliberate manual act. Direct invoice payment to operating is refused, and "the firm bills against trust, so no LawPay invoice pay-link is built." | `src/components/portal/InvoicesSection.tsx`, `src/app/api/portal/pay/init/route.ts`, `planning/PRD-invoicing-2026-06-04.md` non-goal 4 | `invoice_payability` view and the `pay` client tab. Default destination trust, with the invoice recorded on the module's charge row and not on the core payment, so the deposit never reduces the invoice on its own; until staff apply it, the invoice reads `in_progress` so the client isn't asked to pay twice. Setting `invoice_pay_destination` lets a firm choose operating, where the core payment names the invoice. |
| U5 | Card charged but the database write failed: the client sees the LawPay transaction id and is told to contact the firm; the failure goes to a payment error log with a PAY- code. | `src/app/api/portal/pay/route.ts` | Charge state `charged_unrecorded`, automatic retries with the same idempotency key, then an exception that notifies the attorney and admins. PAY- codes kept. |
| U6 | Staff take card payments through hosted fields on the phone intake page, into operating. | `src/components/intake/StaffCardFields.tsx`, `src/app/api/staff-intake/route.ts` | Matter action Take payment: card or ACH, operating or trust, optional invoice. |
| U7 | LawPay webhook: no staff auth (provider callback), always 200, raw payload logged before parsing, transaction id and status pulled from several candidate fields, pending payments advanced to completed or failed, latest payload attached, unknown transactions logged, GET health check. | `src/app/api/lawpay/webhook/route.ts`, `src/db/schema/lawpay-webhook-log.ts` | Same receiver behind the core's inbound hook route (C1), plus a URL token and API verification before any write. |
| U8 | Refund reconciliation plan: detect refunds before the completed/failed branch (a refund payload carrying `charge_id` must never complete a pending charge); verify every refund with a live transaction read; fail closed; idempotent; partial refunds left alone only because Unified's status set had no partial state; trust refunds flagged for manual ledger work. | `PLAN-lawpay-refund-reconciliation.md` | Kept in full. The core refund record (C6) gives partial refunds a home, so they post. A refund made in the LawPay dashboard is verified, then held as an exception for an attorney to post, because a core refund needs an attorney's authority. |
| U9 | Conflict no-go: full refund of the consult fee on the matter's latest completed LawPay card payment; check and cash payments reported for manual handling. | `src/lib/no-go.ts` | On `conflict_check.decided` with decision no_go, the module proposes that refund and notifies the responsible attorney; one click issues it. The client notice and intake purge belong to the intake module. |
| U10 | Trust refund by check on matter close, as a ledger row with the check number. | `src/app/api/matters/[id]/trust-refund/route.ts` | Stays in the core (trust entry type refund). The Refund panel links to it for check refunds. |
| U11 | Fee agreement draft: fee type, amount above zero and at most $999,999.99, services description required and at most 4,000 characters, case number, new case yes/no, client identity prefilled from the contact; closed matters read-only; one active agreement per matter. | `src/app/api/matters/[id]/design-sheet/fee-agreement/route.ts`, `src/db/schema/fee-agreements.ts` | Core `fee_agreements` record; case number and new case in the module's extension table. Client identity is read from core contact records at send time and never copied. |
| U12 | Billing classification from the fee type on draft create and update, NULL-only: never overwrites a billing type or flat fee someone set; flat fee fills only on a flat matter. | `src/lib/fee-agreement-billing.ts` | Same rule through `PATCH /api/v1/matters/{id}`. |
| U13 | Send: draft to sending by compare-and-set; DocuSeal submission with a Client submitter (prefilled read-only identity fields, DocuSeal email off) and a Firm submitter (fee fields, DocuSeal email on, so DocuSeal sends the countersign request and the completed copy to both); ids saved with three retries; if saving fails, the submission is archived and the row returns to draft; the firm emails the signing link; an email failure leaves the agreement sent with a Resend warning. | `.../fee-agreement/[agreementId]/send/route.ts`, `src/lib/docuseal-client.ts`, `src/lib/fee-agreement-email.ts` | Same steps and order, plus one: the unsigned agreement is filed as a core file on the matter, shared with the client, so the portal's file-keyed `signature_requests` view can show it. The invite goes through core mail (C10) and links to the portal's `sign` tab. |
| U14 | DocuSeal webhook: `X-Webhook-Token` compared in constant time; client `form.completed` moves sent to client_signed (during voiding it stamps client_signed_during_void_at); `submission.completed` takes a compare-and-set lock and stamps countersigned, legal execution date and executed; unknown or terminal submissions go to an orphan log with a duplicate count. The executed PDF download was deferred for the MyCase cutover. | `src/app/api/docuseal/webhook/route.ts` | Same, plus a confirming submission read, permanent dedupe, and the executed PDF stored as a core file record on the matter. |
| U15 | Status labels; staff home counts agreements awaiting countersignature; the portal dashboard shows "Awaiting your signature". | `src/components/SendFeeAgreementCard.tsx`, `src/app/home/queries.ts`, `src/app/portal/dashboard/page.tsx` | Same labels; widget; `signature_requests` view keyed to the sent file. |
| U16 | Void and resend: states voiding and voided, void reason, prior status, email contexts for resend and void, DocuSeal archive. | `src/db/schema/fee-agreements.ts`, `src/lib/fee-agreement-email.ts` | Void and Resend on the fee agreement panel. |
| U17 | Method labels: card "Credit card", ACH "Bank transfer (ACH)". | `src/lib/invoice-pdf.ts` | Core methods `card` and `ach`, same labels. |
| U18 | Payment attempts rate-limited (3 per IP per 10 minutes on intake); a rate-limit key for the LawPay webhook. | `src/app/api/intake/payment/route.ts`, `src/db/schema/auth.ts` | 3 attempts per client per 10 minutes on pay pages (settings); the core caps inbound hook traffic (C1). |

## 3. Manifest

`package/togavsu-module.yaml`, in the core's block-YAML subset. Fields marked with a C comment are the core changes in section 7.

```yaml
id: lawpay-docuseal
name: LawPay and DocuSeal
version: "1.0.0"
author: Togavsu build
homepage: https://github.com/CharitonMedia/togavsu/tree/main/modules/lawpay-docuseal
license: Same as Togavsu
price: Free
container:
  image: ghcr.io/charitonmedia/togavsu-lawpay-docuseal
  digest: sha256:0000000000000000000000000000000000000000000000000000000000000000
  port: 8080
  events: /events
permissions:
  - scope: matters:read
    reason: Read the matter, client party, responsible attorney, billing type and fee agreement.
  - scope: matters:write
    reason: Save fee agreement drafts and status, and fill an empty billing type from the fee type.
  - scope: contacts:read
    reason: Read the client's name, email, phone and address to prefill the DocuSeal form.
  - scope: invoices:read
    reason: Show the invoice balance on a pay page and keep pay links current.
  - scope: payments:read
    reason: Show LawPay payments and match them to LawPay records.
  - scope: payments:write
    reason: Record LawPay charges as core payments and post refunds.
  - scope: trust:read
    reason: Show trust balances and warn before a refund takes a client below zero.
  - scope: trust:write
    reason: Record charges that landed in the LawPay trust account as trust deposits.
  - scope: trust:withdraw
    reason: Post refunds of trust deposits. The core still requires an attorney or admin.
  - scope: files:read
    reason: Link the signed fee agreement on the matter.
  - scope: files:write
    reason: Store the signed fee agreement PDF as a file record on the matter.
  - scope: events:read
    reason: Receive invoice, payment, fee agreement, conflict and replenishment events and grants.
  - scope: events:write
    reason: Add fee agreement milestones to the case timeline.
  - scope: notifications:write
    reason: Tell attorneys and admins about payment and signing exceptions.
  - scope: conflicts:read
    reason: Propose the consult fee refund when a conflict check ends in no go.
  - scope: mail:send
    reason: Email the client the signing link and void notices through system mail.
  # C11
  - scope: books:read
    reason: Read invoice balances, payments and trust balances for the sweep and for events, when no one is signed in.
  # C15
  - scope: client_access:read
    reason: Check that the client can sign in to the portal before sending a fee agreement.
payment_processor:            # C2
  destinations: [operating, trust]
inbound_webhooks:             # C1
  - id: lawpay
    path: /hooks/lawpay
  - id: docuseal
    path: /hooks/docuseal
notifications:                # C9
  - kind: exception
    label: LawPay and signing exceptions
    default_channels: [in_app, email]
  - kind: refund_proposed
    label: Proposed refunds
    default_channels: [in_app, email]
widgets:
  - id: awaiting-signature
    title: Fee agreements awaiting signature
    path: /widget/awaiting-signature
    size:
      w: 4
      h: 2
    target:
      tab: agreements
      params: [state]
  - id: exceptions
    title: Payment and signing exceptions
    path: /widget/exceptions
    size:
      w: 4
      h: 2
    target:
      tab: reconciliation
      params: [kind]
    roles: [admin, attorney]
tabs:
  - id: agreements
    title: Fee agreements
    placement: home
    path: /tab/agreements
  - id: reconciliation
    title: LawPay reconciliation
    placement: home
    path: /tab/reconciliation
  - id: matter
    title: Payments and signing
    placement: matter
    path: /matter
  # C12: client tabs, kept out of the portal nav
  - id: pay
    title: Pay
    placement: client
    path: /client/pay
    params: [purpose, invoice_id, matter_id, replenishment_request_id, return_to]
    nav: false
  - id: sign
    title: Sign
    placement: client
    path: /client/sign
    params: [signing_request_id, return_to]
    nav: false
matter_items:
  - id: fee-agreement
    kind: panel
    title: Fee agreement
    path: /matter/fee-agreement
  - id: send-fee-agreement
    kind: action
    title: Send fee agreement
    path: /matter/send
  - id: take-payment
    kind: action
    title: Take payment
    path: /matter/charge
  - id: refund
    kind: action
    title: Refund
    path: /matter/refund
sidebar:
  - id: lawpay
    title: LawPay
    path: /tab/reconciliation
    # C14
    section: money
    icon: payment
    order: 400
help:
  heading: LawPay and DocuSeal
  pages:
    - id: overview
      title: LawPay and DocuSeal
      keywords: [lawpay, docuseal, card, ach, settings, webhook]
      covers: [lawpay-docuseal.awaiting-signature, lawpay-docuseal.exceptions]
      body: "Takes card and ACH payments, issues refunds and sends fee agreements for signature. Every payment and refund is a core record. The settings page shows the two webhook URLs to copy into LawPay and DocuSeal."
    - id: take-payment
      title: Taking a payment or a refund
      keywords: [charge, trust deposit, refund, partial]
      covers: [/modules/lawpay-docuseal/matter-items/take-payment, /modules/lawpay-docuseal/matter-items/refund]
      body: "Pick operating or trust, enter the amount and the client's card or bank details in the LawPay fields. An attorney or admin can refund all or part of a LawPay payment. A trust refund that takes the client below zero still posts and raises a trust flag."
    - id: fee-agreement
      title: Sending a fee agreement
      keywords: [fee agreement, docuseal, sign, void, resend]
      covers: [/modules/lawpay-docuseal/matter-items/fee-agreement, /modules/lawpay-docuseal/tabs/agreements]
      body: "Save a draft, then Send. The client signs first and the firm countersigns in DocuSeal. The signed PDF is filed on the matter."
    - id: reconciliation
      title: LawPay reconciliation
      keywords: [reconcile, exception, unknown transaction]
      covers: [/modules/lawpay-docuseal/tabs/reconciliation]
      body: "Lists LawPay transactions and anything that needs a person, such as a charge recorded late, a refund made in the LawPay dashboard, an unknown transaction or a signing error."
events:
  - invoice.sent
  - invoice.updated
  - invoice.voided
  - payment.completed
  - payment.reversed
  - payment.processor_authorized
  - payment.refund_authorized
  - fee_agreement.updated
  - conflict_check.decided
  - trust.replenishment_requested
  - file.background_authorized
data:
  extends:
    - name: agreement_details
      record: fee_agreement
      table: agreement_details
      key: fee_agreement_id
      columns:
        - name: case_number
        - name: new_case
  views:
    - name: invoice_payability
      record: invoice
      key: invoice_id
      columns:
        - name: invoice_id
        - name: payable
        - name: unpayable_reason
        - name: in_progress_cents
    - name: matter_payability
      record: matter
      key: matter_id
      columns:
        - name: matter_id
        - name: trust_deposits_enabled
        - name: minimum_deposit_cents
    - name: signature_requests
      record: file
      key: file_id
      columns:
        - name: file_id
        - name: signing_request_id
        - name: matter_id
        - name: signer_contact_id
        - name: status
        - name: sent_at
        - name: expires_at
settings:
  - key: lawpay_secret_key
    label: LawPay secret key
    type: string
    required: true
    secret: true
  - key: lawpay_public_key
    label: LawPay public key for hosted fields
    type: string
    required: true
  - key: lawpay_operating_account_id
    label: LawPay operating account id
    type: string
    required: true
  - key: lawpay_trust_account_id
    label: LawPay trust account id
    type: string
    required: true
  - key: core_trust_account_id
    label: Togavsu trust account id for LawPay trust deposits
    type: string
    required: true
  - key: lawpay_api_base
    label: LawPay API address
    type: string
    default: "https://api.8am.com/v1"
  - key: lawpay_webhook_token
    label: LawPay webhook URL token
    type: string
    required: true
    secret: true
  - key: min_charge_cents
    label: Smallest charge in cents
    type: int
    default: "100"
  - key: max_charge_cents
    label: Largest charge in cents
    type: int
    default: "5000000"
  - key: invoice_pay_destination
    label: Where a client's invoice payment lands
    type: enum
    options: [trust, operating]
    default: trust
  - key: pay_intent_minutes
    label: Minutes a pay page stays valid
    type: int
    default: "30"
  - key: pay_attempts_per_window
    label: Card attempts allowed per client per window
    type: int
    default: "3"
  - key: pay_attempt_window_minutes
    label: Attempt window in minutes
    type: int
    default: "10"
  - key: reconcile_interval_minutes
    label: Minutes between reconciliation sweeps
    type: int
    default: "60"
  - key: retry_limit
    label: Automatic retries before an exception
    type: int
    default: "3"
  - key: pending_check_days
    label: Days to keep checking a pending ACH charge
    type: int
    default: "14"
  - key: docuseal_base_url
    label: DocuSeal address
    type: string
    required: true
  - key: docuseal_api_key
    label: DocuSeal API key
    type: string
    required: true
    secret: true
  - key: docuseal_webhook_token
    label: DocuSeal X-Webhook-Token value
    type: string
    required: true
    secret: true
  - key: docuseal_template_id
    label: DocuSeal fee agreement template id
    type: int
    required: true
  - key: firm_signer_name
    label: Firm signer name on the agreement
    type: string
    required: true
  - key: firm_signer_email
    label: Firm signer email for DocuSeal
    type: string
    required: true
  - key: signing_expire_days
    label: Days before an unsigned fee agreement expires (0 never)
    type: int
    default: "0"
```

The settings page shows the webhook URLs to paste into the providers: `https://{firm domain}/hooks/lawpay-docuseal/lawpay?token={lawpay_webhook_token}` and `https://{firm domain}/hooks/lawpay-docuseal/docuseal`. It has a Test connection button per service: a LawPay transaction read of a made-up id (not_found counts as connected) and a DocuSeal template read.

## 4. Data

Tables live in `mod_lawpay_docuseal`. Ids are TEXT UUIDv7, as in the core. Every foreign key to a core record is `ON DELETE RESTRICT`. No table holds a copy of a core record: no names, emails, phones or addresses are stored; contact details are read from the core when needed.

### 4.1 `0001_lawpay.sql`

- `received_events` (event_id PK, action, record_id, received_at): moduleEvent dedupe, as in matter-clock.
- `processor_grants` (initialization_id PK, kind (payment or refund), payment_id TEXT, refund_id TEXT NULL, grant_credential TEXT, expires_at, received_at): grants delivered on `payment.processor_authorized` and `payment.refund_authorized`. `payment_id` has no foreign key because the core payment row doesn't exist until `createPayment`.
- `lawpay_charges`: id PK; matter_id FK core.matters; contact_id FK core.contacts; invoice_id FK core.invoices NULL; payment_id FK core.payments NULL (set once the core payment exists); reserved_payment_id TEXT; initialization_id TEXT UNIQUE NULL; replenishment_request_id TEXT NULL; purpose (trust_deposit, invoice_pay, staff_charge); channel (portal, staff); destination (operating, trust); method (card, ach) NULL until charged; amount_cents BIGINT CHECK (amount_cents > 0); lawpay_charge_id TEXT UNIQUE NULL; lawpay_status TEXT; state (intent, authorized, charging, declined, charged_unrecorded, recorded_pending, recorded_completed, failed, expired); failure_code TEXT (Unified's PAY- codes); failure_message TEXT; attempts INT; initiated_by FK core.users; expires_at, created_at, updated_at.
- `lawpay_refunds`: id PK; charge_id FK lawpay_charges; payment_id FK core.payments; core_refund_id FK core.payment_refunds NULL (C7); lawpay_refund_id TEXT UNIQUE NULL; amount_cents BIGINT CHECK (amount_cents > 0); reason TEXT; origin (firm, lawpay_dashboard, no_go); state (proposed, core_pending, processor_sent, processor_done, posted, failed, external_detected, dismissed); requested_by FK core.users NULL; attempts INT; created_at, updated_at.
- `lawpay_webhook_receipts`: id PK; received_at; dedupe_key TEXT UNIQUE (SHA-256 of the raw body); transaction_id TEXT; event_type TEXT; payload JSONB (the body as received, or `{"raw": first 4000 bytes}` when malformed, as Unified); duplicate_count INT; verdict (applied, duplicate, malformed, unverified, unknown_transaction, ignored); charge_id FK lawpay_charges NULL; refund_id FK lawpay_refunds NULL.
- `exceptions`: id PK; kind (charged_unrecorded, external_refund, unknown_transaction, account_mismatch, amount_mismatch, refund_unposted, manual_refund_needed, signed_pdf_failed, docuseal_orphan, signing_error); matter_id FK core.matters NULL; charge_id, refund_id FK NULL; signing_id TEXT NULL; detail TEXT; status (open, resolved); resolved_by FK core.users NULL; resolved_at; resolution_note; notified_at; created_at. One open row per (kind, charge_id, refund_id, signing_id).

### 4.2 `0002_docuseal.sql`

- `agreement_details` (fee_agreement_id PK FK core.fee_agreements, case_number TEXT with length at most 100, new_case BOOLEAN NULL, updated_at): the declared extension of the core fee agreement.
- `signings`: id PK; fee_agreement_id FK core.fee_agreements; matter_id FK core.matters; client_contact_id FK core.contacts; state (sending, sent, client_signed, processing_post_execution, executed, declined, expired, voiding, voided, error); prior_state TEXT NULL; docuseal_submission_id TEXT UNIQUE NULL; client_submitter_id, firm_submitter_id TEXT; client_signing_url TEXT; audit_url TEXT; sent_at, client_signed_at, client_signed_during_void_at, countersigned_at, executed_at, declined_at, expires_at TIMESTAMPTZ (expires_at NULL when `signing_expire_days` is 0); legal_execution_date DATE; sent_file_id FK core.files NULL (the unsigned copy shared with the client at send); signed_file_id FK core.files NULL; void_reason TEXT; last_error TEXT; invite_error TEXT; pdf_attempts INT; last_action_by TEXT (user id or `webhook`); created_at, updated_at. Partial unique index: one row per fee_agreement_id where state is not executed, declined, expired, voided or error, as Unified.
- `docuseal_webhook_receipts`: id PK; dedupe_key TEXT UNIQUE (submission id, event type, submitter id and event timestamp); submission_id; event_type; payload JSONB; signing_id FK signings NULL; duplicate_count INT; verdict (applied, duplicate, orphan, terminal, ignored); received_at.

### 4.3 `0003_portal.sql`

- `module_state` (id SMALLINT PK CHECK (id = 1), lawpay_ready BOOLEAN, trust_ready BOOLEAN, docuseal_ready BOOLEAN, min_charge_cents BIGINT, checked_at): the module rewrites this one row each time it loads its own settings, at start and then every minute. `lawpay_ready` means every LawPay setting is filled; `trust_ready` adds that `core_trust_account_id` is an active trust account and no account_mismatch exception is open; `docuseal_ready` means every DocuSeal setting is filled. It lets the published views answer `not_configured` without a settings read.
- `payable_invoices` (invoice_id PK FK core.invoices, matter_id FK core.matters, destination (trust, operating), status (open, funded, paid, void), funded_cents BIGINT NOT NULL DEFAULT 0, opened_at, closed_at, reopened_by FK core.users NULL). A row opens on `invoice.sent` with the destination `invoice_pay_destination` holds at that moment. It turns paid when the core invoice balance reaches zero (read by the books reader on `invoice.updated` and `payment.completed`) and void on `invoice.voided`. When a trust-destination charge naming the invoice reaches recorded_completed, the row turns funded with that charge's amount in `funded_cents`, because the money sits in trust until staff apply it. A funded row returns to open when staff press Reopen online payment on the matter tab, and turns paid when the balance reaches zero.
- `matter_deposits` (matter_id PK FK core.matters, deposits_enabled BOOLEAN NOT NULL DEFAULT true, updated_by FK core.users NULL, updated_at). A matter gets a row, enabled, on its first `trust.replenishment_requested`, its first `invoice.sent` or its first LawPay charge. Staff turn client deposits on or off for the matter on the matter tab, which creates the row when it is missing.
- The three published views below are `CREATE VIEW` statements in this migration.

### 4.4 Published views

These implement client-portal SPEC section 7 exactly. Another module reads them through `GET /api/v1/modules/lawpay-docuseal/views/{view}?record_id=...` or `include=lawpay-docuseal.{view}`, inside the viewer's access: the core applies the viewer's access to the keyed record, so a client sees only rows for their own readable invoices, matters and shared files.

| View | Scope | Key | Columns and rules |
| --- | --- | --- | --- |
| `invoice_payability` | `views:lawpay-docuseal.invoice_payability:read` | invoice_id (core invoice) | One row per `payable_invoices` row whose status isn't void. `invoice_id` TEXT. `in_progress_cents` BIGINT: the sum of this invoice's charges in authorized, charging, charged_unrecorded or recorded_pending, plus `funded_cents` while the row is funded. `unpayable_reason` TEXT, first match wins: `not_configured` when `lawpay_ready` is false, or the destination is trust and `trust_ready` is false; `zero_balance` when the row is paid; `in_progress` when the row is funded or `in_progress_cents` is above zero; otherwise `''`. `payable` BOOLEAN is true exactly when `unpayable_reason` is `''`. |
| `matter_payability` | `views:lawpay-docuseal.matter_payability:read` | matter_id (core matter) | One row per `matter_deposits` row. `matter_id` TEXT. `trust_deposits_enabled` BOOLEAN: `deposits_enabled` and `lawpay_ready` and `trust_ready`. `minimum_deposit_cents` BIGINT: `module_state.min_charge_cents`. |
| `signature_requests` | `views:lawpay-docuseal.signature_requests:read` | file_id (core file) | One row per signing that has a `sent_file_id` and isn't in state sending or error, keyed to `sent_file_id`; the client is the one signer a contact holds, so this is one row per signer per document. `file_id`, `signing_request_id` (the signing id), `matter_id`, `signer_contact_id` (the client contact), all TEXT. `status` TEXT: sent is `awaiting_client`; client_signed is `client_signed`; processing_post_execution and executed are `completed`; declined is `declined`; expired is `expired`; voiding and voided are `void`. `sent_at` BIGINT and `expires_at` BIGINT or NULL, unix seconds. |

The views carry no amount the core owns: the pay tab reads the balance from the core when it loads. The DocuSeal signing URL never appears in a view.

### 4.5 Client tabs

Both are declared with `placement: client` and `nav: false` (C12), so the core frames them at `/portal/lawpay-docuseal/{tab}` with the client's context token and only the declared params pass through. An id the client can't read answers 404.

- **`pay`** (`/client/pay`), params `purpose`, `invoice_id`, `matter_id`, `replenishment_request_id`, `return_to`.
  - `purpose=invoice_pay` needs `invoice_id`. The invoice must be readable by the client and its `invoice_payability` row payable; otherwise the page says "This invoice can't be paid online right now." The amount is the invoice's balance due, read from the core as the client, never a client figure. The destination is the row's destination.
  - `purpose=trust_deposit` needs `matter_id` with `trust_deposits_enabled`. With `replenishment_request_id`, the module reads that request as the client (`getTrustReplenishment`); it must be active, on the same matter and naming the client's contact, and the amount is the request's amount. Without it, the client enters an amount from `max(min_charge_cents, minimum_deposit_cents)` to `max_charge_cents`.
  - The page creates the pay intent (server-decided amount, `pay_intent_minutes`), shows LawPay's hosted fields, and after the charge shows the result with a Back link.
- **`sign`** (`/client/sign`), params `signing_request_id`, `return_to`. The signing's `client_contact_id` must be the viewer's linked contact, else 404. In state sent the page shows Sign, a link that opens the DocuSeal signing URL in a new window (`target="_blank"`, allowed by the client sandbox); in any other state it shows the status label.
- `return_to` is honored only when it starts with `/portal/client-portal/`; anything else returns to `/portal`. Back is a link with `target="_top"`, which the client sandbox allows on a click.

## 5. Adapters

```go
type LawPay interface {
    Charge(ctx context.Context, r ChargeRequest) (Transaction, error)              // POST /charges
    Refund(ctx context.Context, chargeID string, cents int64) (Transaction, error) // POST /charges/{id}/refund
    Transaction(ctx context.Context, id string) (Transaction, error)               // GET /transactions/{id}
}
type ChargeRequest struct{ AccountID, TokenID, Reference string; AmountCents int64 }
type Transaction struct {
    ID, Type, Status, AccountID, ChargeID, MethodType string // Type CHARGE or REFUND; MethodType card or bank
    AmountCents int64
}
type LawPayError struct{ Message, Code string; HTTPStatus int }

type DocuSeal interface {
    CreateSubmission(ctx context.Context, r SubmissionRequest) (Submission, error)
    Submission(ctx context.Context, id string) (Submission, error)
    CurrentPDF(ctx context.Context, id string) ([]byte, error)   // the agreement as sent, before signing
    CompletedPDF(ctx context.Context, id string) ([]byte, error)
    Archive(ctx context.Context, id string) error
}
```

- `lawpayHTTP`: Basic auth with `lawpay_secret_key` and an empty password, base `lawpay_api_base`, amount sent as the decimal string of integer cents, response amounts parsed as integers. A non-2xx response becomes `LawPayError` from `messages[0]`. The reference is the module charge id, so a retried request is traceable in LawPay.
- `docusealHTTP`: header `X-Auth-Token`, base `docuseal_base_url`. Accepts both response shapes Unified handles (an array of submitters, or an object with `submitters`). Client submitter `send_email: false`; Firm submitter keeps DocuSeal email on. `CurrentPDF` reads `GET /api/submissions/{id}/documents` and downloads the first document. `CompletedPDF` reads `combined_document_url`, else the first document URL. `Archive` is `DELETE /api/submissions/{id}`. With `signing_expire_days` above 0, `CreateSubmission` sends `expire_at` that many days out, and DocuSeal's `submission.expired` webhook ends the signing.
- Status mapping lives in one table-driven function. CHARGE AUTHORIZED or COMPLETED with method card: success, recorded completed. Method bank with AUTHORIZED: recorded pending. COMPLETED or SETTLED on a pending charge: completed. DECLINED, FAILED or VOIDED: a decline before recording, or failed on a pending charge. REFUND AUTHORIZED or COMPLETED: refund done.
- `fake` package: `fake.LawPay` and `fake.DocuSeal` hold transactions and submissions in memory, take scripted outcomes (decline, network error, 404, delayed settlement, a refund made in the dashboard, a client who declines to sign, an expired submission), and generate webhook bodies. Every test uses them. No test, CI job or default config reaches LawPay or DocuSeal; live adapters are built only from settings at runtime. Card data never touches the module: the hosted fields script returns a token, and fakes use placeholder tokens such as `tok_test_card` and `tok_test_bank`.
- Clock and id generator are interfaces, so tests drive time.

## 6. Core API calls

Interactive calls carry `X-Togavsu-Context-Token`: a staff viewer on staff pages, the client on the `pay` and `sign` tabs. Background writes carry a core-issued task authorization in `X-Togavsu-Task-Authorization` (processor, refund and file publication grants). Background reads (the sweep and event handling) are service calls as the books reader (C11, CORE-02), and the signature callback is a service call bound to this module (C8).

| Method and path | Scope | Used for |
| --- | --- | --- |
| GET /api/v1/modules/lawpay-docuseal/settings | own token | settings, secrets included |
| POST /api/v1/modules/lawpay-docuseal/context-tokens/verify | own token | check a frame's viewer |
| GET /api/v1/matters/{matter_id} | matters:read | client party, responsible attorney, billing type, case number, status |
| PATCH /api/v1/matters/{matter_id} | matters:write | NULL-only billing type and flat fee |
| GET, POST /api/v1/fee-agreements; GET, PATCH /api/v1/fee-agreements/{id} | matters:read, matters:write | agreement record and status (draft, sent, signed, void) |
| GET /api/v1/contacts/{id}, /api/v1/contact-emails, /api/v1/contact-phones, /api/v1/contact-addresses | contacts:read | DocuSeal prefill at send time |
| GET /api/v1/invoices, /api/v1/invoices/{id} | invoices:read; plus books:read with no viewer | balance on the pay tab as the client; balance after `invoice.updated` and `payment.completed` as the books reader |
| POST /api/v1/payments/processor-initializations | payments:write, plus trust:write for trust | authorize a charge (C2, C3) |
| POST /api/v1/payments | payments:write, plus trust:write for trust | record a charge with its grant; record a verified unknown transaction by an attorney |
| POST /api/v1/payments/{id}/status | payments:write, plus trust:write for trust | pending ACH to completed or failed |
| GET /api/v1/payments, /api/v1/payments/{id} | payments:read; plus books:read with no viewer | screens as the viewer; matching and the sweep as the books reader |
| POST /api/v1/payments/{id}/refunds; POST /api/v1/payments/{id}/refunds/{refund_id}/status; GET /api/v1/payments/{id}/refunds | payments:write, plus trust:withdraw for trust | refunds (C6) |
| GET /api/v1/trust/balances, /api/v1/trust-accounts/{id} | trust:read; plus books:read with no viewer | below-zero warning as the viewer; the settings check behind `trust_ready` as the books reader |
| GET /api/v1/trust-replenishment-requests/{id} | trust:read | the `pay` tab's `replenishment_request_id`, read as the client (C5, CORE-12) |
| GET /api/v1/client-access?contact_id= | client_access:read | Send checks that the client has active portal access (C15) |
| POST /api/v1/file-uploads, PUT /api/v1/file-uploads/{id}, POST /api/v1/file-uploads/{id}/complete, POST /api/v1/files | files:write | store the unsigned copy at Send, as the staff viewer, with `shared_with_client` true |
| POST /api/v1/files/publication-grants, then the same upload calls | files:write | store the signed PDF from the webhook, with a publication grant |
| POST /api/v1/files/{id}/status | files:write | mark the signed PDF executed (C8) |
| GET /api/v1/files/{id} | files:read | link the signed copy |
| POST /api/v1/events | events:write | timeline milestones |
| POST /api/v1/notifications | notifications:write | exception and proposal notices with `audience` and `matter_id` (C9) |
| POST /api/v1/mail/messages | mail:send | signing invite, resend, void notice, with `matter_id` and `to_contact_ids` (C10) |
| GET /api/v1/conflict-checks/{id} | conflicts:read | no-go decision detail |

## 7. Core changes

The module-core lane builds these from docs/modules/CORE-CHANGES.md. Each item names its merged definition there; that definition is the one built, and this list says what this module needs from it.

1. **C1 Inbound provider webhooks (CORE-07).** Manifest field `inbound_webhooks: [{id, path}]` (id a slug, path module-local). Public route `POST /hooks/{module_id}/{hook_id}`, `security: []` in OpenAPI, served by the core; caddy already sends every path to the core, so the Caddyfile doesn't change. The core forwards the raw body, query string and headers to the module's declared path, minus `Cookie`, `Authorization`, `X-Togavsu-Context-Token` and `X-CSRF-Token`, and adds `X-Togavsu-Module` and `X-Togavsu-Inbound-Hook`. POST only. A module not installed or disabled, or a hook it doesn't declare, answers 404 and forwards nothing. New registry settings `modules.inbound_webhook_max_bytes` (start 1048576; over is 413) and `modules.inbound_webhook_rate_per_minute` (start 120 per hook; over is 429 with Retry-After). The core returns the module's status and body and writes delivery counts, not bodies, to the audit log.
2. **C2 Processor registration and initialization (CORE-23).** Manifest field `payment_processor: {destinations: [operating, trust]}` registers a processor. Implement `initiateProcessorPayment`, delivery of `processor_authorization` on `payment.processor_authorized`, and acceptance of that grant in `X-Togavsu-Task-Authorization` on `createPayment` and `updatePaymentStatus`, as `docs/api/conventions.md` describes; `internal/billing/doc.go` lists them as not implemented.
3. **C3 Client-initiated processor payments (CORE-23).** Add `initiateProcessorPayment` to the client context's allowed operations, limited to the client's own shared matter, with `contact_id` the client user's linked contact, and destination trust, or destination operating only with the `invoice_id` of that client's own sent invoice with a balance due. Anything else stays not_found or forbidden.
4. **C4 Pending grant lifetime (CORE-23).** A processor grant whose payment was created pending stays valid for `updatePaymentStatus` on that payment until it leaves pending or `payments.processor_pending_grant_days` passes (new registry setting, start 30).
5. **C5 Replenishment link (CORE-23, CORE-12).** `ProcessorInitializationWrite` and `PaymentWrite` take optional `replenishment_request_id` (an active request on the same matter and client). Completing that trust payment marks the request satisfied and emits `trust.replenishment_satisfied`. The client context gains read-only `listTrustReplenishment` and `getTrustReplenishment` for the client's own readable matters, with the field allowlist of CORE-12; the `pay` tab uses `getTrustReplenishment`.
6. **C6 Payment refunds (CORE-24).** New core record `payment_refunds`: payment, amount_cents, reason, reference_number, status (pending, completed, failed), trust_entry_id, source, created_by. `Payment` gains read-only `refunded_cents` and `refund_state` (none, partial, full); its status stays completed.
   - `POST /api/v1/payments/{payment_id}/refunds` (`createPaymentRefund`): body `amount_cents`, `reason`, optional `reference_number`, `status` pending or completed. Roles admin and attorney; scope payments:write, plus trust:withdraw when the payment's destination is trust; delegated viewer; Idempotency-Key. Refused with `validation_failed`, error code `refund_exceeds_payment`, when the amount is zero or more than the payment amount minus all pending and completed refunds; refused when the payment isn't completed.
   - `POST /api/v1/payments/{payment_id}/refunds/{refund_id}/status` (`updatePaymentRefundStatus`): pending to completed or failed; the same status replays. Delegated viewer, or noninteractive with the `refund_authorization` grant the core delivers on `payment.refund_authorized` to the module named in the payment's `source` when a refund is created pending.
   - `GET /api/v1/payments/{payment_id}/refunds` (`listPaymentRefunds`): payments:read.
   - Completion, in one transaction: for a trust payment, one trust ledger row of entry type refund on the payment's trust account, client and matter, under the 8.4 below-zero rule (it posts, flags and emails); for an operating payment applied to an invoice, a lower amount paid on that invoice, and status back from paid to sent when a balance reappears.
   - Events `payment.refund_created`, `payment.refund_authorized`, `payment.refunded` and `payment.refund_failed` (read scope payments:read), plus `trust.entry_posted` and `trust.flag_raised` as they occur.
7. **C7 Linkable records (CORE-22).** Add `fee_agreement` (core.fee_agreements) and `payment_refund` (core.payment_refunds) to `coreRecords` in `internal/modules/manifest.go`, so the module's foreign keys and the `agreement_details` extension pass the install check; add `fee-agreements` to `pluralRecords` for include=.
8. **C8 Signature callback (CORE-25, CORE-01).** `fee_agreements` gains `signature_module_id`, set when a module token moves an agreement to sent. That module may then, by service call (no viewer), call `updateFeeAgreement` to move sent to signed with `signed_on` and `signed_file_id`, and `markFileStatus` to set that one file executed. No other field or transition opens to it.
9. **C9 Module notification kinds and audiences (CORE-03).** Manifest `notifications: [{kind, label, default_channels}]`; kinds join the preference grid as `lawpay-docuseal.exception` and `lawpay-docuseal.refund_proposed`. `createNotification` with `audience` (`admins`, `responsible_attorney` or `admins_and_responsible_attorney`) and `matter_id` in place of `user_id`, sent as a viewer call or a service call; the core resolves recipients, sends in-app and email per each user's preferences, and applies the matter wall.
10. **C10 Module mail (CORE-04).** `POST /api/v1/mail/messages`, scope `mail:send`, as a viewer call by the staff user who pressed Send, Resend or Void. Body `matter_id`, `to_contact_ids` (the client contact), `subject`, `text` and, for the invite and resend, `link_path` `/portal/lawpay-docuseal/sign?signing_request_id={id}`. The core sends to the contact's current primary email through the system mail router (CORE-05), so it reaches the email module when system mail is routed there; it returns a per-recipient status the module branches on (`sent`, or `failed` or `skipped` with the error) and writes `mail.sent` on the matter timeline. The module never sees the address.
11. **C11 Books reads (CORE-02, CORE-01).** Scope `books:read` and the service-call form of the invoice, payment and trust reads, so the sweep and event handling read balances and payments with no one signed in, with walled matters left out, as for any principal outside the wall.
12. **C12 Client tabs (CORE-10).** `tabs[].placement: client`, `tabs[].params` and `tabs[].nav: false` for `pay` and `sign`; the core frames them at `/portal/lawpay-docuseal/{tab}` with the client's context token.
13. **C13 Frame sandbox (CORE-09).** The client sandbox list, so Sign opens DocuSeal in a new window and Back navigates the top window on a click.
14. **C14 Sidebar placement (CORE-08).** `section: money`, `icon: payment`, `order: 400`.
15. **C15 Client access read (CORE-14).** `client_access:read` and `GET /api/v1/client-access?contact_id=`, used only to check that the signer has active portal access before Send.

## 8. Behaviors and acceptance criteria

Each criterion runs against Postgres and the fake adapters with synthetic records.

**Settings**

- MOD-PAY-01: With a required setting empty, pay pages, Take payment, Refund and Send show "LawPay (or DocuSeal) isn't set up" with a link to the settings page and make no adapter call.
- MOD-PAY-02: An admin reading module settings sees every secret redacted; the module's own read gets the values; no secret appears in a page, log line, receipt or exception.
- MOD-PAY-03: A `core_trust_account_id` that `GET /api/v1/trust-accounts/{id}` doesn't return as active blocks trust charges and opens account_mismatch with a notification; operating charges keep working.

**Charges**

- MOD-PAY-04: Staff Take payment of 25000 cents to operating: one core initialization, one adapter charge on the operating account id, one core payment (method card, destination operating, reference the LawPay charge id, source lawpay-docuseal, status completed), one charge row in recorded_completed.
- MOD-PAY-05: The same to trust: the adapter charges the trust account id; the core payment has destination trust and `trust_account_id` equal to `core_trust_account_id`; exactly one core trust deposit row exists.
- MOD-PAY-06: An ACH charge the fake reports AUTHORIZED records a pending core payment and no trust row. A later verified COMPLETED webhook completes it and the core posts one trust row; a second COMPLETED webhook posts nothing.
- MOD-PAY-07: An amount below `min_charge_cents` or above `max_charge_cents` is refused before any core or adapter call.
- MOD-PAY-08: A declined card leaves no core payment, a declined charge row with a PAY-CHRG code, and shows "Card declined" to the payer.
- MOD-PAY-09: Submitting one pay intent twice charges once and returns the first result. An intent older than `pay_intent_minutes` is expired and charges nothing.
- MOD-PAY-10: A client's fourth card attempt inside `pay_attempt_window_minutes` is refused with 429 and no adapter call.
- MOD-PAY-11: The `pay` tab with `purpose=trust_deposit`, the matter and the `replenishment_request_id` of an active request for 50000 cents charges exactly 50000 with no amount field shown; after the charge the core request is satisfied (C5). Without `replenishment_request_id` the client enters the amount, and one below `minimum_deposit_cents` is refused before any call.
- MOD-PAY-12: A client invoice payment with `invoice_pay_destination` trust charges the trust account; the core payment is a trust deposit with no `invoice_id`, the invoice balance is unchanged, and the module's charge row names the invoice. With the setting at operating, the core payment names the invoice and its balance drops by the amount.
- MOD-PAY-13: On the `pay` tab, a client can't pay matter B's deposit or another client's invoice, and a `replenishment_request_id` naming another client or matter is refused; each answers 404 with no core or adapter call.
- MOD-PAY-14: When `createPayment` fails after a successful adapter charge, the payer sees the LawPay transaction id and the contact-the-firm message; the row is charged_unrecorded; the sweep retries with the same idempotency key; after `retry_limit` failures one charged_unrecorded exception opens and one notification goes to the admins and the responsible attorney.

**LawPay webhook**

- MOD-PAY-15: A missing or wrong `token` gets 401 and writes nothing, not even a receipt.
- MOD-PAY-16: Malformed JSON with a good token is stored with verdict malformed and answers 200.
- MOD-PAY-17: A duplicate delivery (same raw body) increments `duplicate_count`, answers 200 and changes no charge, payment or refund.
- MOD-PAY-18: A webhook naming a transaction the fake doesn't know (verification 404) changes nothing, gets verdict unverified and answers 200.
- MOD-PAY-19: The trap from U8: a body with type REFUND, status COMPLETED and a top-level `charge_id` of a pending charge leaves the charge and its core payment pending.
- MOD-PAY-20: A verified charge on an account id that matches neither setting, or disagrees with the row's destination, posts nothing and opens account_mismatch.
- MOD-PAY-21: A verified charge with no charge row opens unknown_transaction. An attorney picks the matter and contact on the reconciliation tab and records it; the core payment takes the verified amount and method and the destination from the account id; the exception resolves.
- MOD-PAY-22: The module's `GET /hooks/lawpay` answers `{"ok":true}` for health checks inside the stack; the public route takes POST only (C1).

**Refunds**

- MOD-PAY-23: Partial refund: an attorney refunds 4000 of a 10000 operating payment. Order: core refund pending, adapter refund, core refund completed. Result: `refunded_cents` 4000, `refund_state` partial, refund row posted, invoice amount paid lower by 4000.
- MOD-PAY-24: A second refund of 6000 succeeds and makes `refund_state` full. A further 1 cent is refused by the module and by the core with `refund_exceeds_payment`, with no adapter call.
- MOD-PAY-25: A refund of 15000 on a 10000 payment is refused before any core write or adapter call.
- MOD-PAY-26: A trust refund of 30000 on a 30000 trust payment when the client's matter balance is 10000: the panel says "This leaves the client at -$200.00 in trust. It will post and raise a trust flag." and still allows submit; the core posts the refund row, raises one trust flag and emails the admins and responsible attorney; the module never blocks it.
- MOD-PAY-27: Staff and accountant users don't see Refund, and a direct call is forbidden by the core.
- MOD-PAY-28: An adapter refund failure marks the core refund and the module row failed and shows the LawPay message; nothing posts.
- MOD-PAY-29: When the adapter refund succeeds and the core completion fails, the row is processor_done; the sweep completes it with the `refund_authorization` grant; the later webhook matches the stored LawPay refund id and does nothing.
- MOD-PAY-30: A verified refund made in the LawPay dashboard opens external_refund with its amount and notifies. Post refund on the reconciliation tab, by an attorney, creates a completed core refund with the LawPay refund id as reference; a partial dashboard refund posts as partial.
- MOD-PAY-31: `conflict_check.decided` with decision no_go on a matter whose latest completed LawPay payment is a 17500 card charge creates one proposed refund of 17500 and one refund_proposed notification; a replayed event adds nothing; Issue refund runs the MOD-PAY-23 sequence. When the latest payment wasn't through LawPay, the module opens manual_refund_needed instead.

**Reconciliation**

- MOD-PAY-32: The sweep reads every pending ACH charge younger than `pending_check_days` and advances it; an older one opens an exception.
- MOD-PAY-33: The reconciliation tab lists LawPay transactions with matter, amount, destination, state and core payment link, filtered by state and kind; every open exception has Resolve with a required note.

**Fee agreements**

- MOD-PAY-34: Saving a draft creates the core fee agreement (draft) and the agreement_details row. An amount at or below 0 or above 99999999 cents, an empty services description, or one over 4000 characters is refused. On a closed matter the panel is read-only.
- MOD-PAY-35: A flat draft of 150000 cents on a matter with no billing type sets billing type flat and flat fee 150000; an hourly matter stays hourly; a flat matter with a flat fee keeps it. Fee type maps to the billing type of the same name.
- MOD-PAY-36: Send with the client's email, phone, street, city, state (two letters) or ZIP (##### or #####-####) missing or malformed answers 422 listing the fields and calls nothing. Send for a client with no active portal access (`GET /api/v1/client-access` 404, or `active` false) answers 422 "Give the client portal access first; they sign in the portal." and calls nothing.
- MOD-PAY-37: Send: signing row sending; one DocuSeal submission with Client (clientName, clientAddress, clientCityStateZip, clientPhone, clientEmail, all read-only, email off) and Firm (signer name and email from settings; representationDescription, caseNumber, newCase, feeType label such as "Flat Fee" or "Advance Fee" for hourly, feeAmount); ids stored; the agreement as sent (`CurrentPDF`) filed as one core file on the matter with `shared_with_client` true, its id in `sent_file_id`; core agreement sent; one invite through core mail to the client contact with a link to `/portal/lawpay-docuseal/sign?signing_request_id={id}`; client-visible timeline event "Fee agreement sent for signature".
- MOD-PAY-38: Two Send clicks at once make one submission; the second gets 409.
- MOD-PAY-39: When storing the ids or filing the unsigned copy fails, the submission is archived and the agreement returns to draft. When the archive fails too, the signing is error with the orphan submission id in `last_error`, and an exception opens.
- MOD-PAY-40: A failed invite leaves the agreement sent with "Use Resend signing link"; Resend sends the same link with no new submission.
- MOD-PAY-41: Void from sent or client_signed: voiding, DocuSeal archive, core status void with the reason, voided, void notice to the client. A client `form.completed` during voiding stamps `client_signed_during_void_at` and the panel shows it.

**DocuSeal webhook**

- MOD-PAY-42: A missing or wrong `X-Webhook-Token` gets 401 and writes nothing.
- MOD-PAY-43: Client `form.completed` moves sent to client_signed; firm `form.completed` stamps `countersigned_at`; a duplicate increments `duplicate_count` and changes nothing.
- MOD-PAY-44: `submission.completed`, once the adapter confirms the submission completed: one change to processing_post_execution; the PDF stored as a core file on the matter, shared with the client, status executed; the core agreement signed with `signed_on` set to the completion date in the firm's time zone and `signed_file_id`; state executed; client-visible event "Fee agreement executed". Two concurrent deliveries produce one file.
- MOD-PAY-45: `submission.completed` while still sent (the client event was lost) proceeds once the adapter shows both submitters completed.
- MOD-PAY-46: A PDF download failure keeps the signing executed, retries on the sweep, and after `retry_limit` opens signed_pdf_failed with a notification.
- MOD-PAY-47: Unknown submission ids get verdict orphan; events for executed, voided or error signings get verdict terminal; both answer 200. An internal error answers 500 so DocuSeal retries.

**Views and widgets**

- MOD-PAY-48: `invoice.sent` opens one `payable_invoices` row and `invoice_payability` reads `payable` true. A pending ACH charge naming it reads `in_progress` with its amount in `in_progress_cents`. With destination trust, a completed charge for the full balance reads `in_progress` (funded) until staff press Reopen online payment, or until the core balance hits zero, which reads `zero_balance`. With a LawPay setting empty it reads `not_configured`. `invoice.voided` removes the row. A client reading the view for another client's invoice gets no rows.
- MOD-PAY-49: `signature_requests`, keyed to the sent file, reads `awaiting_client` while sent, then `client_signed`, then `completed`; voiding and voided read `void`. With the file unshared, a client gets no row. The `sign` tab with the client's context shows Sign opening the DocuSeal signing URL in a new window; another client's context gets 404.
- MOD-PAY-50: Reading a view without its approved `views:lawpay-docuseal.{view}:read` scope is forbidden.
- MOD-PAY-51: The awaiting-signature widget counts sent and client_signed agreements on matters the viewer can see; a walled matter is absent; the click-through opens the agreements tab filtered by state.
- MOD-PAY-52: `matter_payability` reads `trust_deposits_enabled` true on a matter after its first replenishment request, with `minimum_deposit_cents` equal to `min_charge_cents`; false after staff turn client deposits off on the matter tab, and false while `trust_ready` is false. A matter with no row gives the portal no Make a deposit.
- MOD-PAY-53: A client `form.declined` moves sent to declined, the view reads `declined`, one signing_error exception opens and the responsible attorney is notified. With `signing_expire_days` 30, the submission carries an `expire_at` 30 days out, `expires_at` shows in the view, and a verified `submission.expired` moves sent to expired and the view reads `expired`. With 0, no `expire_at` is sent and `expires_at` is NULL.

## 9. Tests

Every test runs in the existing "Core tests" CI job (`go test ./...` against Postgres from `TOGAVSU_TEST_DATABASE_URL`). Postgres tests skip locally without it and fail in CI without it, as `testDSN` in `internal/modules/harness_pg_test.go` does.

- `internal/modules/module_lawpay_docuseal_pg_test.go` (package `modules_test`, so it uses the existing harness in `internal/modules/harness_pg_test.go`: `newEnv`, `approveAll`, `contextFor`, `moduleDB`): before install the suite sets `e.rt.handlers["lawpay-docuseal"]` to a handler that, on its first request after each Deploy, reads `e.rt.last("lawpay-docuseal")` for the deployment's secrets and builds this module's handler with its own schema login from `e.moduleDB`, with `fake.LawPay`, `fake.DocuSeal` and a test clock injected through `Config`. It then builds and signs this package with `lawpaydocuseal.Build(priv, nil)` from a generated key, adds that key as trusted, installs through `POST /api/v1/modules` and approves every permission. coretest serves the core routes this module calls (CORE-29). Core events reach the module's `/events`; provider webhooks reach it through `/hooks/lawpay-docuseal/...` on the core (CORE-07).
- In the same file, one test per criterion, `TestPGModPay01` through `TestPGModPay53`, asserting core rows through the core API and module rows through the module's own database login.
- Also in the same file: the package passes the install check (migrations, no duplicate record tables, RESTRICT keys, declared views and client tabs exist); uninstall with keep-data leaves the schema; hard-deleting a matter with module rows is refused and names the module.
- `modules/lawpay-docuseal/webhook_test.go`, `status_test.go`, `money_test.go`: unit tests for candidate-field extraction, refund-first detection, status mapping, dedupe keys and cents parsing (a fractional or negative amount is refused).
- Fakes generate every payload. No test opens a connection outside the test servers.

## 10. Code layout

```
modules/lawpay-docuseal/
  README.md                    what it does, settings, webhook URLs, build and sign steps
  Dockerfile                   as examples/matter-clock/Dockerfile; ENTRYPOINT ["/lawpay-docuseal", "serve"]
  module.go                    Config, New, Handler, Build, Archive, SignFile (the module kit's signing path, pkg/modulekit)
  charges.go refunds.go reconcile.go lawpayhook.go
  agreements.go docusealhook.go views.go pages.go sweep.go
  core/client.go               typed client for the section 6 calls
  adapters/lawpay.go adapters/docuseal.go adapters/status.go
  fake/lawpay.go fake/docuseal.go
  web/                         HTMX templates and the hosted fields loader
  cmd/lawpay-docuseal/main.go  "serve" and "package -key signing.key -out lawpay-docuseal.signed.json", as matter-clock
  package/togavsu-module.yaml
  package/migrations/0001_lawpay.sql 0002_docuseal.sql 0003_portal.sql
  *_test.go                    unit tests only

internal/modules/module_lawpay_docuseal_pg_test.go   section 9 acceptance suite
```

Secrets come from the Docker secrets the core hands the supervisor (`togavsu_api_token`, `togavsu_webhook_secret`, `togavsu_db_role`, `togavsu_db_password`, `togavsu_db_schema`), read as matter-clock reads them. Tunables come from module settings, never from integer literals, so `settings.CheckModule` passes. The sweep runs inside the module process every `reconcile_interval_minutes`. The build writes only the CharitonMedia/togavsu-lawpay-docuseal repository and nothing in the core repository; a change to this spec goes to the integration lead. C1 to C15 go to the module-core lane through docs/modules/CORE-CHANGES.md and land before this module merges.

## 11. Out of scope

- Pre-matter consult fee charges on the public intake path and the no-go client notice; they belong to the intake module.
- Chargeback disputes inside LawPay; a verified chargeback lands as an external_refund exception for an attorney to post.
- Check refunds and check printing (core trust refund).
- Saved cards, recurring or scheduled charges, surcharges and payment plans.
- Building or editing the DocuSeal template; the firm's template uses the field names in MOD-PAY-37.
- Signing any document other than the fee agreement.
- QuickBooks or other ledger sync.

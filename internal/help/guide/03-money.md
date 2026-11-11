# Time, billing and trust

## Billing / Generate {#billing-worklist}
<!-- template: billing/worklist.html | path: /billing | where: Money > Billing / Generate | keywords: flat fee, apply flat fee, generate draft invoice -->
Two tables: flat-fee matters with their status (Needs fee amount, Awaiting trust funds, Ready to apply, Collected) and hourly or mixed matters with unbilled time. Apply flat fee moves the fee from trust to operating (attorneys and admins). Generate draft invoice drafts from the unbilled time on a row or on every selected row.

## Invoices {#billing-invoices}
<!-- template: billing/invoices.html | path: /billing/invoices | where: Money > Invoices | keywords: drafts, sent, paid, void, overdue -->
Lists invoices by status with the draft count in the sidebar. Open one to approve, send, record payments or void it.

## Invoice {#billing-invoice}
<!-- template: billing/invoice.html | path: /billing/invoices/{invoice_id} | where: Money > Invoices > an invoice | keywords: approve, send, pdf, collections, apply trust, write off -->
Shows the lines, totals, payments and balance. Attorneys approve and send; the PDF goes by system mail or downloads. The Collections panel applies trust, records an outside payment or requests replenishment for the balance.

## Time Entries {#billing-time}
<!-- template: billing/time.html | path: /billing/time | where: Money > Time Entries | keywords: log time, timers, bulk entry, increments -->
Log time against a matter in the firm's increment (6 minutes by default), run timers, and review drafts from stopped timers.

## Time entry {#billing-time-entry}
<!-- template: billing/time_entry.html | path: /billing/time/{time_entry_id} | where: Money > Time Entries > an entry | keywords: edit time, activity type, billable -->
Edit one entry's date, increments, description, activity type and billable flag until it is invoiced.

## Expenses {#billing-expenses}
<!-- template: billing/expenses.html | path: /billing/expenses | where: Money > Time Entries > Expenses | keywords: receipts, markup, expense types -->
Record expenses with a type, amount, billable flag, markup and receipt file.

## Expense {#billing-expense}
<!-- template: billing/expense.html | path: /billing/expenses/{expense_id} | where: Money > Expenses > an expense | keywords: edit expense -->
Edit one expense's date, description, amount, type, billable flag, markup and receipt until it is invoiced; after that it is part of the invoice and changes through the invoice.

## Payments {#billing-payments}
<!-- template: billing/payments.html | path: /billing/payments | where: Money > Payments | keywords: record payment, reverse, operating, trust -->
Lists payments with their destination, method and status, and records new ones. A reversal undoes a payment without deleting it.

## Billing reports {#billing-report}
<!-- template: billing/report.html | path: /billing/reports/{report} | where: Money > Billing / Generate > Reports | keywords: unbilled time, time by user, ar aging, payments received, write-offs, csv -->
Unbilled time, time by user, AR aging, payments received and write-offs, each with CSV export.

## Trust overview {#trust-ledger}
<!-- template: trust/ledger.html | path: /trust, /trust/ledger | where: Money > Trust > Trust Ledger | keywords: deposit, withdrawal, transfer, refund, adjustment, reversal -->
The append-only trust ledger. Post deposits (staff and up), withdrawals, transfers to operating and refunds (attorneys and admins). Corrections are reversal entries; no entry is ever edited or deleted.

## Trust Balances {#trust-balances}
<!-- template: trust/balances.html | path: /trust/balances | where: Money > Trust > Trust Balances | keywords: client balance, matter balance, account -->
Balances per client per matter, per client and per account, computed from the ledger.

## Trust flags {#trust-flags}
<!-- template: trust/flags.html | path: /trust/flags | where: Home > Trust alerts | keywords: below zero, resolve -->
Flags raised when an entry takes a balance below zero. The entry still posts; the flag emails the admins and the responsible attorney and stays here until an attorney or admin resolves it with a note.

## Trust flag {#trust-flag}
<!-- template: trust/flag.html | path: /trust/flags/{trust_flag_id} | where: Trust alerts > a flag | keywords: resolve flag, note -->
One flag with the entry that raised it. Resolve it with a note.

## Matter trust {#trust-matter}
<!-- template: trust/matter.html | path: /trust/matters/{matter_id} | where: Matter screen > Trust | keywords: evergreen, flat fee, matter ledger -->
The matter's own trust ledger with its running balance per client, the evergreen minimum that triggers a replenishment request, and the flat-fee status the Billing / Generate worklist uses.

## Trust Replenishment {#trust-replenishment}
<!-- template: trust/replenishment.html | path: /trust/replenishment | where: Money > Trust > Trust Replenishment | keywords: evergreen, request, satisfy, cancel -->
Requests raised when a matter drops below its evergreen minimum, or made from an invoice. Each request is active until satisfied or cancelled.

## Trust reconciliation {#trust-reconciliation}
<!-- template: trust/reconciliation.html | path: /trust/reconciliation | where: Money > Trust > Reconciliation | keywords: statement balance, monthly, difference -->
Enter the month's statement balance and date. The core shows the ledger total, the sum of client balances and any difference, and keeps the result.

## Trust notifications {#trust-notifications}
<!-- template: trust/notifications.html | path: /trust/notifications | where: Header > Bell > All notifications | keywords: unread, mark read -->
Your own notifications from trust, such as flags and replenishment requests, with their read state. Click one to open its item; Mark all read clears the count in the header bell.

# Acceptance matrix

Task `qa`. PRD sections 11 and 12. This map does not accept the product.

Every criterion is `pending`. A pass needs product evidence for each positive case and each failure case. The harness refuses mocks, skips, oracle passes, scaffold output, and the two structural Doc Tender stand-ins. `app_complete` stays false until all 52 criteria have that evidence and a clean Docker Desktop install is recorded.

Requirement sentences are copied from `docs/build/acceptance.json` at build time. Domains come from `docs/build/tasks.json`.

## Summary

| ID | Full phase | Recheck | Implementing domains | Cases | Status |
| --- | --- | --- | --- | --- | --- |
| AC-001 | 1 | 7 | contracts, platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 3 | pending |
| AC-002 | 7 | none | api, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core | 5 | pending |
| AC-003 | 6 | 7 | identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core | 8 | pending |
| AC-004 | 7 | none | identity, integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions | 9 | pending |
| AC-005 | 3 | 7 | integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions | 4 | pending |
| AC-006 | 3 | 7 | integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions | 7 | pending |
| AC-007 | 3 | 7 | integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions | 4 | pending |
| AC-008 | 3 | 7 | integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions | 9 | pending |
| AC-009 | 3 | 7 | practice, integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions | 5 | pending |
| AC-010 | 5 | 7 | integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 5 | pending |
| AC-011 | 6 | 7 | integrate-practice, calendar, integrate-finance, integrate-documents, ui, integrate-extensions | 5 | pending |
| AC-012 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-core, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules | 4 | pending |
| AC-013 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules | 3 | pending |
| AC-014 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions | 4 | pending |
| AC-015 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, marketplace, integrate-extensions | 5 | pending |
| AC-016 | 7 | none | platform, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 3 | pending |
| AC-017 | 7 | none | integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 3 | pending |
| AC-018 | 7 | none | platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 6 | pending |
| AC-019 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 6 | pending |
| AC-020 | 5 | 7 | practice, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 6 | pending |
| AC-021 | 5 | 7 | integrate-practice, trust, integrate-finance, integrate-documents, ui, integrate-extensions | 4 | pending |
| AC-022 | 7 | none | integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 4 | pending |
| AC-023 | 3 | 7 | practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 4 | pending |
| AC-024 | 5 | 7 | identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 6 | pending |
| AC-025 | 5 | 7 | practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 5 | pending |
| AC-026 | 3 | 7 | integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions | 3 | pending |
| AC-027 | 4 | 7 | integrate-practice, calendar, integrate-finance, integrate-documents, integrate-extensions | 5 | pending |
| AC-028 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, ui, modules, integrate-extensions | 6 | pending |
| AC-029 | 5 | 7 | contracts, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 367 | pending |
| AC-030 | 7 | none | contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 7 | pending |
| AC-031 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions | 4 | pending |
| AC-032 | 7 | none | integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions | 6 | pending |
| AC-033 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions | 5 | pending |
| AC-034 | 7 | none | integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 3 | pending |
| AC-035 | 1 | 7 | contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions | 5 | pending |
| AC-036 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules | 6 | pending |
| AC-037 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules | 6 | pending |
| AC-038 | 6 | 7 | integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules | 4 | pending |
| AC-039 | 4 | 7 | integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions | 5 | pending |
| AC-040 | 7 | none | integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions | 4 | pending |
| AC-041 | 4 | 7 | integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions | 7 | pending |
| AC-042 | 4 | 7 | integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions | 4 | pending |
| AC-043 | 6 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 7 | pending |
| AC-044 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 3 | pending |
| AC-045 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 4 | pending |
| AC-046 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 4 | pending |
| AC-047 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 4 | pending |
| AC-048 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 4 | pending |
| AC-049 | 4 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 5 | pending |
| AC-050 | 7 | none | integrate-practice, files, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 5 | pending |
| AC-051 | 5 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 4 | pending |
| AC-052 | 5 | 7 | integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions | 3 | pending |

## Criteria

## AC-001

A fresh install reaches the first-run setup (firm name, first admin, two-factor) in one Compose up with no manual edits.

Phases in scope: 1, 7.
Full behavior phase: 1.
Recheck phases: 7.
Responsible domains: contracts, platform, qa, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: contracts, platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: contracts, platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Phase 1 is one Compose up to the first-run form. Phase 7 runs that same check on a clean Docker Desktop install. Linux Compose CI does not meet the phase 7 recheck.

### Positive

#### AC-001-P1 First-run setup in one Compose up

Phase 1. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Empty synthetic volume. No hand-edited env file and no client data.

Action: docker compose up, then open the first-run URL.

Expect: The form asks for firm name, the first admin, and TOTP enrollment.; No manual edit was required before the form appeared.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-001-F1 Setup that needs a hand edit

Phase 1. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: A stack whose documented start step edits a config file or env file by hand.

Action: Follow only the Compose up path.

Expect: The first-run form is not reached, so the criterion fails.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-001-F2 First admin without two-factor

Phase 1. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic firm name and admin account.

Action: Submit the first admin with TOTP skipped.

Expect: The account is not saved as the first admin.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-002

Every core feature is reachable through /api/v1 and through /mcp, and the OpenAPI spec validates.

Phases in scope: 1, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: api, qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core, release.
Implementing domains: api, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core.
Pending dependencies: api, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core, contracts, platform, identity, practice, billing, trust, calendar, files, editor, doc-tender, ui, modules, marketplace.
Legacy Doc Tender failures: none.
Status: pending.

Phase 1 checks that the published OpenAPI 3.1 document validates. The whole sentence waits until every core feature is on /api/v1 and /mcp.

### Positive

#### AC-002-P1 OpenAPI document validates

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: The api task's OpenAPI 3.1 document, synthetic server not required.

Action: Run a real OpenAPI 3.1 validator on the document.

Expect: The validator exits 0.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-002-P2 Every core feature on both surfaces

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic firm with one row for each core feature.

Action: For each core operation, call /api/v1 and the matching /mcp tool.

Expect: Both surfaces succeed for every core feature.; The operation ids match the validated OpenAPI document.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-002-F1 Invalid OpenAPI fails the validator

Phase 1. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: A copy of the spec with a broken schema.

Action: Run the same validator.

Expect: The validator exits non-zero.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-002-F2 Feature missing on one surface

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic firm.

Action: Compare the core feature list to /api/v1 and /mcp.

Expect: A feature present on only one surface fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-002-F3 UI handler with no API

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic firm.

Action: Exercise a core screen whose action has no /api/v1 operation.

Expect: The missing operation fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-003

A staff user can't see a walled matter anywhere: list, search, widget, report or API.

Phases in scope: 1, 2, 3, 5, 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core, release.
Implementing domains: identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core.
Pending dependencies: identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, module-core, billing, trust, ui, files, modules.
Legacy Doc Tender failures: none.
Status: pending.

A wall names the users who keep the matter. Everyone else loses it, including an admin who was not named. List, search, widget, report, and API are the AC-003 surfaces. Module rows are the same rule.

### Positive

#### AC-003-P1 Walled matter absent from the API

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic matter walled to one attorney. Staff user is not named.

Action: Staff calls the matter list and the matter read on /api/v1.

Expect: The matter id is absent from both responses.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-P2 Walled matter absent from the matter list

Phase 2. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Same wall and staff user.

Action: Open the matter list in the UI and through the practice service.

Expect: The matter id is absent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-P3 Walled matter absent from billing and trust reports

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Walled matter with a synthetic invoice and trust balance.

Action: Staff opens billing and trust reports.

Expect: The matter id is absent from both reports.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-P4 Walled matter absent from search and widgets

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Same wall. Staff user.

Action: Search the matter number and load home-screen widgets.

Expect: Search and widgets omit the matter.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-P5 Walled matter absent from module include rows

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Sample module row pointing at the walled matter. Staff user outside the wall.

Action: GET the matter with include= for the module.

Expect: The response has no module row for that matter.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-003-F1 Any of the five surfaces leaks the matter

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Staff user outside the wall.

Action: Check list, search, widget, report, and API.

Expect: The matter id on any one of those five surfaces fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-F2 Unnamed admin still loses the matter

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Wall names one attorney. Admin user is not on it.

Action: Admin checks list, search, widget, report, and API.

Expect: The matter id is absent on all five.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-003-F3 Module row leaks a walled matter

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Staff user outside the wall.

Action: Read the module extension through include= and through the module token.

Expect: Any returned row for that matter fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-004

An accountant can read billing and trust and can't write anything.

Phases in scope: 1, 2, 3, 4, 5, 6, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, identity, integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: identity, integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: identity, integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions, files, ui, modules, calendar, editor, doc-tender.
Legacy Doc Tender failures: none.
Status: pending.

Accountant reads are invoices, payments, trust ledgers, reconciliations, billing reports, exports, receipts, and invoice PDFs. Every other read and every write is denied. The full sentence waits until MCP routes exist.

### Positive

#### AC-004-P1 Accountant reads billing and trust

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic invoices, payments, trust ledger, reconciliation, and billing report.

Action: Accountant GET each of those resources and the exports.

Expect: Each read returns the synthetic rows.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-P2 Accountant reads a receipt and an invoice PDF

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: One receipt file and one invoice PDF on a synthetic matter.

Action: Accountant downloads both.

Expect: Both downloads return the bytes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-004-F1 Access policy denies an accountant write

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Accountant principal.

Action: Call the access policy with a write operation.

Expect: The policy returns deny and writes nothing.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F2 Accountant cannot read notes

Phase 2. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic note.

Action: Accountant GET the note.

Expect: The note body is not returned.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F3 Accountant writes to billing or trust

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic invoice and trust ledger.

Action: POST, PUT, PATCH, and DELETE on billing and trust through the service.

Expect: Each call is denied.; Row counts and balances are unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F4 Accountant reads a file that is not a receipt or invoice PDF

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic correspondence file.

Action: Accountant lists and downloads it.

Expect: The file is absent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F5 Accountant UI post is denied

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Accountant session.

Action: Submit a billing or practice form that writes.

Expect: The response is denied and the store is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F6 Accountant module token cannot write

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Token scoped as the accountant.

Action: Module write call.

Expect: The write is denied.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-004-F7 Accountant MCP write is denied

Phase 7. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Accountant bearer token.

Action: Call every registered write tool on /mcp.

Expect: Each write is denied and the store is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-005

A trust withdrawal that takes a client below zero posts, raises a flag, and sends email to the admins and the responsible attorney.

Phases in scope: 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

A below-zero withdrawal posts. It raises a flag and emails every admin and the responsible attorney. It does not block the post.

### Positive

#### AC-005-P1 Below-zero withdrawal posts, flags, and emails

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic client balance 100 cents. Withdrawal 250 cents. Two admins and one responsible attorney.

Action: Post the withdrawal through the service, the UI, and /api/v1.

Expect: The ledger has the withdrawal.; A trust flag is open.; Email goes to both admins and the responsible attorney.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-005-F1 Blocked below-zero withdrawal

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Same balances.

Action: Post a withdrawal the service rejects as negative.

Expect: A rejection fails the criterion. The entry has to post.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-005-F2 Email misses an admin or the attorney

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Two admins and one responsible attorney.

Action: Inspect the recipients after the post.

Expect: A missing admin or a missing responsible attorney fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-005-F3 No flag after a negative balance

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Withdrawal that crosses zero.

Action: Read trust flags.

Expect: No open flag fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-006

Trust ledger entries can't be edited or deleted through any path; reversals work.

Phases in scope: 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

No path may edit or delete a ledger row. A reversal is a new row. The original row and its history stay byte for byte. API, MCP, and UI are not the only paths: the trust service, the trust repository, and direct SQL as the application role must refuse UPDATE and DELETE. A repository interface that omits those methods is not acceptance. PONYTAIL-DEBT.md contracts entry 4 stays pending until the trust task shows that SQL rejection. No database trigger carries the rule.

### Positive

#### AC-006-P1 Reversal appends and leaves the original

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: One posted synthetic deposit.

Action: Reverse it.

Expect: A new reversal row exists.; The original id, amount, client, matter, and description are unchanged.; The balance includes both rows.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-006-P2 Reversal is a new row below the transports

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A posted withdrawal. The trust service and the trust repository, not HTTP, MCP, or the UI.

Action: Reverse the withdrawal through the service.

Expect: The reversal is a new row.; The original row and every earlier history row are byte-for-byte unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-006-F1 API edit or delete

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Posted ledger row.

Action: PATCH and DELETE the row on /api/v1.

Expect: Both are denied.; The row is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-006-F2 MCP edit or delete

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Posted ledger row.

Action: Call the MCP update and delete tools.

Expect: Both are denied.; The row is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-006-F3 UI edit or delete

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Posted ledger row.

Action: Submit the ledger form for edit and for delete.

Expect: Both are denied.; The row is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-006-F4 Service and repository UPDATE and DELETE

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A posted trust row and its history. Calls go to the trust service and the trust repository, not through HTTP, MCP, or the UI.

Action: Update the amount, then delete the row, on the service and again on the repository.

Expect: Each update and each delete is refused.; The original row and its history are unchanged.; A repository interface that simply has no update or delete method is not this proof.; No database trigger carries the rule.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-006-F5 Application-role SQL UPDATE and DELETE

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: The same posted row. A SQL session as the application role, not the table owner.

Action: Run UPDATE and DELETE against trust_ledger.

Expect: Both statements are refused or change no row.; The original row and its history are unchanged.; An authorized reversal, if one is posted in the same session, is a new row.; The database-immutability debt stays pending until this rejection is shown.; No database trigger carries the rule.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-007

Reconciliation shows a zero difference on seeded data and the right difference when the statement balance is off.

Phases in scope: 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, trust, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Seeded books tie out to the cent. An off statement shows that same cent difference, including the sign.

### Positive

#### AC-007-P1 Seeded reconciliation ties

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic ledger whose client balances sum to the account total. Statement uses that total.

Action: Run reconciliation.

Expect: The stored difference is 0 cents.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-007-P2 Off statement shows the difference

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Same books. Statement is 125 cents higher.

Action: Run reconciliation.

Expect: The stored difference is 125 cents.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-007-F1 Nonzero books shown as zero

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Statement off by 125 cents.

Action: Read the stored difference.

Expect: A stored 0 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-007-F2 Wrong cents or wrong sign

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Statement 125 cents lower than the ledger.

Action: Read the stored difference.

Expect: Any value other than -125 cents fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-008

Invoices draft from unbilled time, approve, send by system mail, accept payment, and void back to unbilled.

Phases in scope: 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Draft from unbilled time and unbilled expenses, attorney approves, system mail sends, payment applies, and void returns both to unbilled. Each eligible expense is included once and leaves the unbilled set when the draft consumes it. A time-only draft does not satisfy the expense cases.

### Positive

#### AC-008-P1 Draft, approve, send, pay, void

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic unbilled time on an open matter. System mail catches the message.

Action: Draft the invoice, approve it as an attorney, send it, record a payment, then void it.

Expect: The draft lines match the unbilled time.; System mail has the invoice.; The payment reduces the balance.; After void the time is unbilled again and the invoice is void.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-P2 Draft includes each eligible expense once

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Open matter with two unbilled expenses and one expense already on another invoice.

Action: Generate one draft for that matter.

Expect: Each unbilled expense appears on exactly one line.; The already invoiced expense is not on the draft.; The two drafted expenses are no longer unbilled.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-P3 Void returns the expenses to unbilled

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Invoice whose draft consumed those two expenses.

Action: Void the invoice and read unbilled expenses.

Expect: Both expenses are unbilled again.; Neither stays billed on the void invoice.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-008-F1 Void leaves time billed

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Approved synthetic invoice.

Action: Void it and read the time rows.

Expect: Time that stays billed fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-F2 Send skips system mail

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Approved invoice.

Action: Send by a path that does not use system mail.

Expect: A send with no system-mail record fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-F3 Staff approves an invoice

Phase 3. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Draft invoice. Staff user.

Action: Staff calls approve.

Expect: Approve is denied.; The invoice stays draft.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-F4 Expense drafted twice

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: One unbilled expense.

Action: Draft it, then draft again without voiding.

Expect: The expense on a second draft, or on two lines of one draft, fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-F5 Void leaves the expense billed

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Invoice that consumed one expense.

Action: Void it and read unbilled expenses.

Expect: The expense still billed, or missing from unbilled, fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-008-F6 Already invoiced expense is drafted again

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: An expense that is already on an invoice.

Action: Generate a new draft for that matter.

Expect: That expense on the new draft fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-009

A matter with an unpaid invoice or a leftover trust balance closes after a warning.

Phases in scope: 2, 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, practice, integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: practice, integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: practice, integrate-practice, billing, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Unpaid invoices and leftover trust warn. Neither one blocks the close.

### Positive

#### AC-009-P1 Close records a warning acknowledgement

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic open matter.

Action: Close it with the warning acknowledgement set.

Expect: The matter is closed.; The warning acknowledgement is stored.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-009-P2 Unpaid invoice warns and still closes

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Open matter with an unpaid synthetic invoice.

Action: Close the matter.

Expect: The close screen shows the unpaid-invoice warning.; After confirm, the matter is closed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-009-P3 Leftover trust warns and still closes

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Open matter with a nonzero synthetic trust balance and no invoice.

Action: Close the matter.

Expect: The close screen shows the trust warning.; After confirm, the matter is closed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-009-F1 Close blocked by an unpaid invoice or trust balance

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Matter with an unpaid invoice, and a second matter with leftover trust.

Action: Confirm the close on each.

Expect: A close that the service rejects fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-009-F2 Close with no warning

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Unpaid invoice.

Action: Close and read the close record.

Expect: A close with no stored warning fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-010

Home-screen layouts save per user; a user saves a named layout and marks it default, and after a reset that layout is offered first and restores exactly; the role default the admin set stays available; an 11th saved layout is refused.

Phases in scope: 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Ten saved layouts is the cap. Reset offers the user's default first and restores it exactly. The role default remains available.

### Positive

#### AC-010-P1 Named default survives reset

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic user with a custom grid.

Action: Save it under a name, mark it default, reset, and apply the offered layout.

Expect: That named layout is offered first.; Widget ids and coordinates match the saved layout.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-010-P2 Role default stays available

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin-set role default and a different user default.

Action: Open reset.

Expect: The role default is still in the list and restores exactly.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-010-F1 Eleventh saved layout

Phase 5. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: User who already has 10 saved layouts.

Action: Save another.

Expect: The save is refused.; The count stays 10.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-010-F2 Another user's layout

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Two synthetic users.

Action: User A lists layouts.

Expect: User B's layout id is absent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-010-F3 Restore that moves a widget

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Saved layout with known coordinates.

Action: Apply it after reset.

Expect: A changed coordinate or a dropped widget that the user can still see fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-011

Clicking the upcoming deadlines widget opens the deadlines page with the same date range, and clicking one deadline opens it; a staff user's click never reaches a walled matter; the sample module's widget opens the tab its manifest names; a manifest with a widget that has no click-through target is refused at install.

Phases in scope: 4, 5, 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, calendar, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, calendar, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, calendar, integrate-finance, integrate-documents, ui, integrate-extensions, modules.
Legacy Doc Tender failures: none.
Status: pending.

The deadlines widget keeps its date range. A walled matter is not reachable from the click. A module widget needs a click-through target or install refuses it.

### Positive

#### AC-011-P1 Deadline record opens

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic deadline inside a date range.

Action: Open that deadline through the calendar API.

Expect: The deadline id matches.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-011-P2 Widget keeps the date range and opens one deadline

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Upcoming-deadlines widget filtered to a known range. One deadline in range.

Action: Click the widget, then click the deadline.

Expect: The deadlines page uses the same date range.; The deadline page is that deadline.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-011-P3 Sample widget opens its manifest tab

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed sample module whose widget names one of its tabs.

Action: Click the widget.

Expect: The named tab opens.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-011-F1 Staff click on a walled deadline

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Deadline on a matter the staff user cannot see.

Action: Click it from the widget.

Expect: The deadline page does not open.; The matter id is not shown.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-011-F2 Widget with no click-through target

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Module manifest whose widget omits the target.

Action: Install the package.

Expect: Install is refused before the module runs.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-012

The sample module installs from a local file, asks for its permissions, shows its widget and tab, receives events, and uninstalls, all without a core restart.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-core, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-core, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-core, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Legacy Doc Tender failures: none.
Status: pending.

Matter Clock installs from a local file, shows its permission prompt, widget, and tab, receives an event, and uninstalls. The core process does not restart.

### Positive

#### AC-012-P1 Install, event, uninstall, no restart

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Signed sample module package on local disk. Record the core process start time.

Action: Install, approve permissions, emit a subscribed event, then uninstall.

Expect: The permission list is shown before the module runs.; The widget and the matter tab render.; The module receives the event.; After uninstall the widget and tab are gone.; The core process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-012-F1 Install restarts the core

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Sample package.

Action: Install and compare the core process start time.

Expect: A new process start fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-012-F2 Install with no permission prompt

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Package that requests scopes.

Action: Install.

Expect: An install that skips the prompt fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-012-F3 Event delivered after uninstall

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Module just uninstalled.

Action: Emit an event it had subscribed to.

Expect: The uninstalled module does not receive it.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-013

An unsigned or tampered module is refused.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Legacy Doc Tender failures: none.
Status: pending.

Unsigned and tampered packages are refused before a container starts. A trusted local signature is the control.

### Positive

#### AC-013-P1 Trusted local signature installs

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Sample package signed by a key the admin marked trusted.

Action: Install from a local file.

Expect: Install proceeds to the permission prompt.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-013-F1 Unsigned package

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Sample package with the signature removed.

Action: Install it.

Expect: Install is refused.; No module container starts.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-013-F2 Tampered package

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Signed package with one byte changed after signing.

Action: Install it.

Expect: Install is refused.; No module container starts.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-014

A module switched in for calendar takes over calendar screens and API; switching back restores the core's.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, calendar.
Legacy Doc Tender failures: none.
Status: pending.

Switching calendar to a module moves screens and API to that module and leaves core calendar data read-only. Switching back restores the core.

### Positive

#### AC-014-P1 Module takes calendar screens and API

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic module that replaces calendar. Core calendar rows already exist.

Action: Switch calendar to the module and open the screen and the API.

Expect: The module serves the screen and the API.; A core calendar write is rejected while the module is selected.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-014-P2 Switching back restores the core

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Calendar currently served by the module.

Action: Switch back to the core.

Expect: Core screens and the core API serve calendar again.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-014-F1 Core write while the module is selected

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Module selected for calendar.

Action: POST a core calendar event.

Expect: The core write is denied.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-014-F2 Module receives calendar calls after switch back

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Calendar switched back to the core.

Action: Create an event through the API.

Expect: The core handles the call.; The module does not receive it.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-015

A flagged module shows the warning on its listing and alerts installed firms by dashboard and email; a pulled module can't be installed from the marketplace and can still be installed from a local file.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, marketplace, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, marketplace, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, marketplace, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

A flag warns. It does not remove the listing. A pull blocks marketplace install and still allows a local file.

### Positive

#### AC-015-P1 Flagged listing shows the warning

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic marketplace listing flagged with a short note.

Action: Open the listing.

Expect: The page shows We've been alerted that this item has problems. and the note.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-015-P2 Installed firms are alerted

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Two synthetic firms with the module installed. The listing is flagged.

Action: Run the alert check.

Expect: Each firm has a dashboard alert and an email.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-015-P3 Pulled module still installs from a local file

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Pulled module package on disk, signed with a trusted local key.

Action: Install from the local file.

Expect: Install reaches the permission prompt.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-015-F1 Marketplace install of a pulled module

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Pulled listing.

Action: Install from the marketplace.

Expect: Install is refused.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-015-F2 Flag hides the listing

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Flagged listing that was not pulled.

Action: Search the marketplace.

Expect: A missing listing fails the criterion. A flag is a warning, not a pull.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-016

An overnight update swaps the core with no failed requests during a load test; a bad image (failed signature or health check) is refused and the old version keeps running.

Phases in scope: 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: platform, qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: platform, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: platform, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The load test runs during a signed swap. A bad signature or a failed health check keeps the old version. A migration that ran before a failed health check restores the backup.

### Positive

#### AC-016-P1 Signed swap under load drops no requests

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic stack on the old signed image. Load against page and API routes.

Action: Publish a newer signed image and let the updater swap.

Expect: Failed request count during the swap is 0.; The new version serves health after the swap.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-016-F1 Bad signature

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Running signed version. Candidate image with a bad signature.

Action: Run the updater.

Expect: The candidate is refused.; The old version still serves health.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-016-F2 Failed health check

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Candidate whose signature is valid and whose health check fails after migration.

Action: Run the updater.

Expect: The candidate is refused.; The old version serves health.; The pre-update backup is restored.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-017

p95 page and API response under 300 ms on seed data of 50 users, 20,000 matters and 1,000,000 time entries.

Phases in scope: 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, integrate-extensions, contracts, platform.
Legacy Doc Tender failures: none.
Status: pending.

The seed is 50 users, 20,000 matters, and 1,000,000 time entries. p95 for pages and for the API must be under 300 ms. A smaller seed cannot pass. integrate-practice, integrate-finance, integrate-documents, and integrate-extensions own this criterion with release. contracts supplies the seed and platform supplies the stack.

### Positive

#### AC-017-P1 p95 under 300 ms on the full seed

Phase 7. Kind: performance. Status: pending. Expected exit code: 0.

Arrange: Synthetic seed of at least 50 users, 20,000 matters, and 1,000,000 time entries.

Action: Measure p95 page and p95 API over the seeded routes.

Expect: users >= 50, matters >= 20000, time entries >= 1000000.; p95 page < 300 ms and p95 API < 300 ms.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-017-F1 p95 at 300 ms or more

Phase 7. Kind: performance. Status: pending. Expected exit code: 0.

Arrange: Full seed.

Action: Read the measured p95.

Expect: A page or API p95 of 300 ms or more fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-017-F2 Smaller seed

Phase 7. Kind: performance. Status: pending. Expected exit code: 0.

Arrange: Seed below any one of the three counts. p95 under 300 ms.

Action: Submit the run as AC-017 evidence.

Expect: The run is rejected because the seed is short.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-018

Audit log records every write with actor and source. A backup downloads to the admin's computer, copies to an S3-compatible destination and a network path, and restores to a working system with matching record counts.

Phases in scope: 1, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: platform, qa, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: platform, identity, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Phase 1 is the audit row on every write. Phase 7 is download, the S3-compatible copy, the network-path copy, and a restore with matching counts.

### Positive

#### AC-018-P1 Every write has actor and source

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic writes from the UI, from an API token, and from a module id.

Action: Read the audit log for those writes.

Expect: Each write has an actor and a source of UI, API token, or module id.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-018-P2 Backup download and both copies

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin session. S3-compatible destination and a network path, both synthetic.

Action: Create a backup, download it, and copy it to both destinations.

Expect: The admin machine has the archive.; Both destinations have a copy.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-018-P3 Restore matches record counts

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Backup of a known synthetic database.

Action: Restore it to a clean stack.

Expect: The stack serves health.; Record counts match the source backup.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-018-F1 Write with no audit row

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: One synthetic write.

Action: Read the audit log.

Expect: A missing row, actor, or source fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-018-F2 Restore count mismatch

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Backup with known counts.

Action: Restore and compare counts.

Expect: Any mismatched count fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-018-F3 Destination copy missing

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: S3-compatible destination and a network path configured.

Action: Run the backup.

Expect: A missing copy on either destination fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-019

The sidebar shows only the entries a user's role and installed modules allow, with live counts; a collapsed subgroup or sidebar stays collapsed for that user on another computer.

Phases in scope: 5, 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, modules.
Legacy Doc Tender failures: none.
Status: pending.

Sidebar entries follow the role and the installed modules. Counts are live. Collapse state follows that user to another computer.

### Positive

#### AC-019-P1 Role entries and live counts

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic staff user and an admin. Known matter and draft-invoice counts.

Action: Load the sidebar for each.

Expect: Staff does not see admin-only entries.; The badges match the live queries.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-019-P2 Collapse follows the user

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: User collapses a subgroup and the sidebar.

Action: Sign in as that user from a second session.

Expect: The subgroup and the sidebar are still collapsed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-019-P3 Installed module entry

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Sample module installed with a sidebar entry.

Action: Load the sidebar.

Expect: The module entry is present and its count matches the module.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-019-F1 Staff sees an admin entry

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Staff user.

Action: Load the sidebar and call the nav API.

Expect: An admin-only entry fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-019-F2 Count includes a walled matter

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Staff user outside a wall. One active matter behind the wall.

Action: Read the Matters count.

Expect: A count that includes that matter fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-019-F3 Module entry without the module

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Sample module not installed.

Action: Load the sidebar.

Expect: The sample entry is absent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-020

Search finds a matter by number, a person by phone and an invoice by number in each scope and in All, and returns nothing from a walled matter to a user outside the wall.

Phases in scope: 2, 3, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, practice, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: practice, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: practice, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, billing.
Legacy Doc Tender failures: none.
Status: pending.

Matter number, E.164 phone, and invoice number hit their own scope and All. A walled matter and its invoice stay out. Attorney-only values are not searched for staff.

### Positive

#### AC-020-P1 Matter number in the matters scope

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic matter 2026-0001.

Action: Search that number in Matters.

Expect: The matter is the hit.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-020-P2 Phone in the people scope

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic person with phone +15555550100.

Action: Search that E.164 number in People.

Expect: The person is the hit.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-020-P3 Invoice number in the invoices scope

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic invoice with a known number.

Action: Search that number in Invoices.

Expect: The invoice is the hit and it names its matter.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-020-P4 All scope returns each hit

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The same matter, person, and invoice.

Action: Search each value in All.

Expect: Each search returns its hit under the right scope heading.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-020-F1 Walled matter, person, and invoice

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Staff user outside the wall. The matter, a party phone, and an invoice sit on that matter.

Action: Search each value in its scope and in All.

Expect: Every search returns nothing for those records.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-020-F2 Staff search returns an attorney-only value

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Contact with an encrypted SSN. Staff user.

Action: Search a fragment of that SSN.

Expect: No hit is produced from the attorney-only value.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-021

A new trust flag raises the responsible attorney's notification count; Mark all read clears it.

Phases in scope: 3, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, trust, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, trust, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, trust, integrate-finance, integrate-documents, ui, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The new trust flag raises the responsible attorney's unread count. Mark all read clears that count. Another attorney's count stays put.

### Positive

#### AC-021-P1 Flag names the responsible attorney

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic trust flag on a matter with a responsible attorney.

Action: Read the flag.

Expect: The flag is tied to that attorney.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-021-P2 Count goes up, then Mark all read clears it

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Responsible attorney with a known unread count.

Action: Raise a new trust flag, read the count, then Mark all read.

Expect: The count increases by one.; After Mark all read the flag's notification is read and the count no longer includes it.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-021-F1 Another attorney's count moves

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Two attorneys. The flag belongs to one matter.

Action: Raise the flag.

Expect: The other attorney's unread count is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-021-F2 Mark all read leaves the flag unread

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Attorney with the new flag notification.

Action: Mark all read and reload.

Expect: An unread flag notification fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-022

/help shows the user guide in HTML and PDF for the running version and the firm's sections; an admin adds a help item from the dashboard; a client user gets a 404 on /help/guide.

Phases in scope: 5, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

HTML and the firm's help items are phase 5. The PDF has to be the guide for the running version, which is the phase 7 image. A client user gets 404 on /help/guide.

### Positive

#### AC-022-P1 HTML guide and a firm help item

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Staff session. Admin adds a synthetic help item from the dashboard.

Action: Open /help and the new item.

Expect: The HTML guide opens.; The firm's sections include the new item.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-022-P2 PDF guide for the running version

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Running image version recorded.

Action: Download the PDF guide.

Expect: The PDF opens and its version matches the running core.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-022-F1 Client user on /help/guide

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Client session.

Action: GET /help/guide and /help/guide.pdf.

Expect: Both return 404.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-022-F2 Guide from another version

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Running version known.

Action: Fetch the guide.

Expect: A different version fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-023

Reopening a closed matter requires a reason, restores its earlier status and shows in the case timeline; a zero-fee matter carries its marker and stays out of AR aging.

Phases in scope: 2, 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, billing.
Legacy Doc Tender failures: none.
Status: pending.

Reopen needs a reason, returns the pre-close status, and shows on the timeline. A zero-fee matter keeps that marker and stays out of AR aging.

### Positive

#### AC-023-P1 Reopen restores the earlier status

Phase 2. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Matter that was open, then closed. Synthetic reason.

Action: Reopen it as an attorney.

Expect: A blank reason is not used.; Status is the pre-close status.; The timeline shows the reopen and the reason.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-023-P2 Zero-fee matter stays out of AR aging

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic zero-fee matter with the marker, and one ordinary unpaid invoice on another matter.

Action: Open AR aging.

Expect: The zero-fee matter is absent.; The marker is still on the matter.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-023-F1 Reopen with a blank reason

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Closed matter.

Action: Reopen with an empty reason.

Expect: The reopen is refused.; Status stays closed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-023-F2 Zero-fee matter in AR aging

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Zero-fee matter.

Action: Read AR aging.

Expect: A row for that matter fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-024

The edit history card shows every change to a matter with old and new values; attorney-only values show redacted to staff.

Phases in scope: 1, 2, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: identity, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, ui.
Legacy Doc Tender failures: none.
Status: pending.

The history card shows every matter change with old and new values. Staff see attorney-only values redacted. The service redacts them too.

### Positive

#### AC-024-P1 History has old and new values

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic matter. Change a plain field twice.

Action: Read the history API.

Expect: Each change has the old value and the new value.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-024-P2 Edit history card shows those rows

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Same matter.

Action: Open the edit history card as an attorney.

Expect: The card shows every change with old and new values.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-024-F1 Staff service read of an attorney-only value

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Matter change that touches a conflict reason.

Action: Staff calls the history API.

Expect: The old and new values are redacted.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-024-F2 Staff card shows the secret

Phase 5. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Same history.

Action: Open the card as staff.

Expect: A visible old or new secret fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-024-F3 A change missing from the card

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Known set of matter changes.

Action: Compare the card to the audit rows.

Expect: A missing change fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-024-F4 SQL UPDATE and DELETE of audit and timeline rows

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A stored audit_log row and an events row. Calls go to the service and the repository, then to SQL as the application role, not the table owner.

Action: Update and delete each row through the service, the repository, and SQL.

Expect: Each update and each delete is refused or changes no row.; The original row and its history are unchanged.; A repository interface that omits update and delete is not this proof.; The database-immutability debt stays pending until this rejection is shown.; No database trigger carries the rule.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-025

A custom field limited to one practice area shows only on that area's matters, saves through the UI and the API, and appears in CSV export.

Phases in scope: 2, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: practice, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, ui.
Legacy Doc Tender failures: none.
Status: pending.

A custom field limited to one practice area shows only there, saves from the UI and the API, and appears in CSV.

### Positive

#### AC-025-P1 API save and practice-area limit

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Field limited to the synthetic probate area. One probate matter and one criminal matter.

Action: Save a value through the API on the probate matter.

Expect: The probate matter returns the value.; The criminal matter's payload omits the field.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-025-P2 UI save

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Same field and probate matter.

Action: Save a new value on the matter screen.

Expect: The API returns the UI value.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-025-P3 CSV export includes the field

Phase 5. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Probate matter with the value set.

Action: Export matters CSV.

Expect: The column is present and the cell matches the saved value.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-025-F1 Field appears on the wrong practice area

Phase 2. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Criminal matter.

Action: Read the matter.

Expect: The limited field in the payload fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-025-F2 CSV omits the field

Phase 5. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Saved value on the right practice area.

Action: Export CSV.

Expect: A missing column or a blank cell fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-026

A flat-fee matter with the fee in trust shows Ready to apply; applying it posts the trust transfer and marks it Collected.

Phases in scope: 3, 7.
Full behavior phase: 3.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, billing, trust, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Flat fee sitting in trust shows Ready to apply. Apply posts the trust transfer and marks Collected. An hourly matter does not show Ready to apply.

### Positive

#### AC-026-P1 Ready to apply, then Collected

Phase 3. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Flat-fee matter whose full fee is in trust for that client.

Action: Open the billing worklist and apply the fee as an attorney.

Expect: The row shows Ready to apply before the post.; Apply writes the trust transfer to operating.; The row shows Collected.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-026-F1 Collected without the trust transfer

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Ready row.

Action: Apply and read the trust ledger.

Expect: Collected with no transfer row fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-026-F2 Hourly matter shows Ready to apply

Phase 3. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Hourly matter with unbilled time and trust funds.

Action: Open the flat-fee worklist.

Expect: That matter on the flat-fee list fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-027

Available dates lists only days with no court date, block or all-day event between the dates given, in pleading and letter form, and never changes the calendar.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, calendar, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, calendar, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, calendar, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

List days with no court date, block, or all-day event, in pleading form and letter form. The calendar does not change. Half-day mode is the section 8.16 rule on the same tool.

### Positive

#### AC-027-P1 Busy days drop out

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A date range with one court date, one block, one all-day event, and one one-hour client meeting.

Action: Request available dates with half days off.

Expect: The court date, the block, and the all-day event are absent.; The day of the one-hour meeting is listed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-027-P2 Pleading form and letter form

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The same free days. Synthetic matter caption.

Action: Render both forms.

Expect: The pleading has the caption, title, dates, and certificate of service.; The letter has recipient, Re line, and salutation.; Both list the same free days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-027-P3 Half-day mode

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Separate days: a morning court date, a morning-only block, an afternoon-only block, and a full-day block.

Action: Request available dates with half days on.

Expect: The morning court date and the morning-only block are listed as the afternoon only.; The afternoon-only block is listed as the morning only.; The full-day block is absent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-027-F1 Listing changes the calendar

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Known calendar row count.

Action: Request available dates in both forms.

Expect: Any inserted, updated, or deleted event fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-027-F2 A blocked day is listed

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A block on a date inside the range.

Action: Request available dates.

Expect: That date in the free list fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-028

An Appearance change shows on the next page load with no restart; a module's AI call runs on the key in AI settings and the module never sees the key; /llms.txt loads without sign-in and holds no firm records.

Phases in scope: 1, 5, 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, ui, modules, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, ui, modules, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, ui, modules, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

/llms.txt is phase 1 and needs no sign-in. Appearance is the next page load with no restart. The module never sees the AI key.

### Positive

#### AC-028-P1 llms.txt without sign-in holds no firm records

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Seeded synthetic matter and contact. No session cookie.

Action: GET /llms.txt.

Expect: Status 200.; The body has no matter number, contact name, phone, or other seeded record value.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-028-P2 Appearance change on the next load

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the core process start time. Change one appearance setting.

Action: Load the next page.

Expect: The new appearance is visible.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-028-P3 Module AI call uses the settings key

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: AI settings hold a synthetic test key. Sample module asks for a call.

Action: Run the call and inspect the module process environment and logs.

Expect: The call log names the provider from AI settings.; The key string is absent from the module environment and logs.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-028-F1 llms.txt contains a seeded record

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Seeded matter number 2026-0001.

Action: GET /llms.txt with no session.

Expect: That matter number, or any other seeded firm record, fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-028-F2 Module can see the key

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Synthetic test key.

Action: Read the module environment, logs, and iframe token.

Expect: The key on any of those fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-028-F3 Appearance change needs a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Changed appearance setting.

Action: Load the next page without a restart.

Expect: The old appearance still showing fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-029

Every default named in this PRD is a setting on its admin page tab with that default; changing one there takes effect on the next request with no restart and logs the old and new values; Reset to default restores it.

Phases in scope: 1, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: contracts, qa, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: contracts, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: contracts, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The harness inventory in tests/harness/defaults.py is the list of named defaults, including every Doc Tender category and the conditional labeling, categorizing, and check-model defaults. Phase 1 checks the registry. Phase 5 changes, audits, and resets each resettable default on its own. One working setting does not cover the rest. Fixed rules are not switches.

### Positive

#### AC-029-P1 Registry holds every named default

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Fresh database.

Action: Compare the settings registry to tests/harness/defaults.py entries whose setting flag is true, including each category row and each conditional AI default.

Expect: Each key is present on its tab with the inventoried default.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-matter_number_format Change matter_number_format applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting matter_number_format on the Firm profile tab starts at its inventoried default.

Action: Change matter_number_format on its admin tab and issue the next request.

Expect: The new value of matter_number_format is in effect.; The audit row for matter_number_format has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-matter_number_format Reset matter_number_format to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: matter_number_format on the Firm profile tab has been changed away from its inventoried default.

Action: Press Reset to default on matter_number_format and issue the next request.

Expect: matter_number_format is back to its inventoried default: YYYY-NNNN.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-totp_by_role Change totp_by_role applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting totp_by_role on the Security tab starts at its inventoried default.

Action: Change totp_by_role on its admin tab and issue the next request.

Expect: The new value of totp_by_role is in effect.; The audit row for totp_by_role has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-totp_by_role Reset totp_by_role to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: totp_by_role on the Security tab has been changed away from its inventoried default.

Action: Press Reset to default on totp_by_role and issue the next request.

Expect: totp_by_role is back to its inventoried default: required for admin, attorney, staff, and accountant; optional for client; admin always required.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-idle_timeout Change idle_timeout applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting idle_timeout on the Security tab starts at its inventoried default.

Action: Change idle_timeout on its admin tab and issue the next request.

Expect: The new value of idle_timeout is in effect.; The audit row for idle_timeout has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-idle_timeout Reset idle_timeout to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: idle_timeout on the Security tab has been changed away from its inventoried default.

Action: Press Reset to default on idle_timeout and issue the next request.

Expect: idle_timeout is back to its inventoried default: 12 hours.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-failed_sign_ins_before_lockout Change failed_sign_ins_before_lockout applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting failed_sign_ins_before_lockout on the Security tab starts at its inventoried default.

Action: Change failed_sign_ins_before_lockout on its admin tab and issue the next request.

Expect: The new value of failed_sign_ins_before_lockout is in effect.; The audit row for failed_sign_ins_before_lockout has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-failed_sign_ins_before_lockout Reset failed_sign_ins_before_lockout to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: failed_sign_ins_before_lockout on the Security tab has been changed away from its inventoried default.

Action: Press Reset to default on failed_sign_ins_before_lockout and issue the next request.

Expect: failed_sign_ins_before_lockout is back to its inventoried default: 5.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-lockout_length Change lockout_length applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting lockout_length on the Security tab starts at its inventoried default.

Action: Change lockout_length on its admin tab and issue the next request.

Expect: The new value of lockout_length is in effect.; The audit row for lockout_length has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-lockout_length Reset lockout_length to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: lockout_length on the Security tab has been changed away from its inventoried default.

Action: Press Reset to default on lockout_length and issue the next request.

Expect: lockout_length is back to its inventoried default: 15 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-api_rate_limit Change api_rate_limit applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting api_rate_limit on the Security tab starts at its inventoried default.

Action: Change api_rate_limit on its admin tab and issue the next request.

Expect: The new value of api_rate_limit is in effect.; The audit row for api_rate_limit has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-api_rate_limit Reset api_rate_limit to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: api_rate_limit on the Security tab has been changed away from its inventoried default.

Action: Press Reset to default on api_rate_limit and issue the next request.

Expect: api_rate_limit is back to its inventoried default: 600 requests a minute per token.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-case_number_pattern Change case_number_pattern applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting case_number_pattern on the Practice tab starts at its inventoried default.

Action: Change case_number_pattern on its admin tab and issue the next request.

Expect: The new value of case_number_pattern is in effect.; The audit row for case_number_pattern has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-case_number_pattern Reset case_number_pattern to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: case_number_pattern on the Practice tab has been changed away from its inventoried default.

Action: Press Reset to default on case_number_pattern and issue the next request.

Expect: case_number_pattern is back to its inventoried default: Missouri docket numbers.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-conflict_score_floor Change conflict_score_floor applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting conflict_score_floor on the Practice tab starts at its inventoried default.

Action: Change conflict_score_floor on its admin tab and issue the next request.

Expect: The new value of conflict_score_floor is in effect.; The audit row for conflict_score_floor has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-conflict_score_floor Reset conflict_score_floor to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: conflict_score_floor on the Practice tab has been changed away from its inventoried default.

Action: Press Reset to default on conflict_score_floor and issue the next request.

Expect: conflict_score_floor is back to its inventoried default: 60 out of 100.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-time_increment Change time_increment applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting time_increment on the Time and billing tab starts at its inventoried default.

Action: Change time_increment on its admin tab and issue the next request.

Expect: The new value of time_increment is in effect.; The audit row for time_increment has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-time_increment Reset time_increment to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: time_increment on the Time and billing tab has been changed away from its inventoried default.

Action: Press Reset to default on time_increment and issue the next request.

Expect: time_increment is back to its inventoried default: 6 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-expense_markup Change expense_markup applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting expense_markup on the Time and billing tab starts at its inventoried default.

Action: Change expense_markup on its admin tab and issue the next request.

Expect: The new value of expense_markup is in effect.; The audit row for expense_markup has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-expense_markup Reset expense_markup to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: expense_markup on the Time and billing tab has been changed away from its inventoried default.

Action: Press Reset to default on expense_markup and issue the next request.

Expect: expense_markup is back to its inventoried default: 0 percent.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-payment_terms Change payment_terms applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting payment_terms on the Time and billing tab starts at its inventoried default.

Action: Change payment_terms on its admin tab and issue the next request.

Expect: The new value of payment_terms is in effect.; The audit row for payment_terms has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-payment_terms Reset payment_terms to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: payment_terms on the Time and billing tab has been changed away from its inventoried default.

Action: Press Reset to default on payment_terms and issue the next request.

Expect: payment_terms is back to its inventoried default: due in 30 days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-grace_period Change grace_period applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting grace_period on the Time and billing tab starts at its inventoried default.

Action: Change grace_period on its admin tab and issue the next request.

Expect: The new value of grace_period is in effect.; The audit row for grace_period has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-grace_period Reset grace_period to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: grace_period on the Time and billing tab has been changed away from its inventoried default.

Action: Press Reset to default on grace_period and issue the next request.

Expect: grace_period is back to its inventoried default: 15 days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-late_interest Change late_interest applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting late_interest on the Time and billing tab starts at its inventoried default.

Action: Change late_interest on its admin tab and issue the next request.

Expect: The new value of late_interest is in effect.; The audit row for late_interest has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-late_interest Reset late_interest to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: late_interest on the Time and billing tab has been changed away from its inventoried default.

Action: Press Reset to default on late_interest and issue the next request.

Expect: late_interest is back to its inventoried default: off.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-evergreen_minimum Change evergreen_minimum applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting evergreen_minimum on the Trust tab starts at its inventoried default.

Action: Change evergreen_minimum on its admin tab and issue the next request.

Expect: The new value of evergreen_minimum is in effect.; The audit row for evergreen_minimum has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-evergreen_minimum Reset evergreen_minimum to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: evergreen_minimum on the Trust tab has been changed away from its inventoried default.

Action: Press Reset to default on evergreen_minimum and issue the next request.

Expect: evergreen_minimum is back to its inventoried default: none.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-reconciliation_reminder_day Change reconciliation_reminder_day applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting reconciliation_reminder_day on the Trust tab starts at its inventoried default.

Action: Change reconciliation_reminder_day on its admin tab and issue the next request.

Expect: The new value of reconciliation_reminder_day is in effect.; The audit row for reconciliation_reminder_day has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-reconciliation_reminder_day Reset reconciliation_reminder_day to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: reconciliation_reminder_day on the Trust tab has been changed away from its inventoried default.

Action: Press Reset to default on reconciliation_reminder_day and issue the next request.

Expect: reconciliation_reminder_day is back to its inventoried default: the 5th.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-deadline_reminders Change deadline_reminders applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting deadline_reminders on the Calendar tab starts at its inventoried default.

Action: Change deadline_reminders on its admin tab and issue the next request.

Expect: The new value of deadline_reminders is in effect.; The audit row for deadline_reminders has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-deadline_reminders Reset deadline_reminders to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: deadline_reminders on the Calendar tab has been changed away from its inventoried default.

Action: Press Reset to default on deadline_reminders and issue the next request.

Expect: deadline_reminders is back to its inventoried default: 7 days and 1 day before.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-event_reminder Change event_reminder applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting event_reminder on the Calendar tab starts at its inventoried default.

Action: Change event_reminder on its admin tab and issue the next request.

Expect: The new value of event_reminder is in effect.; The audit row for event_reminder has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-event_reminder Reset event_reminder to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: event_reminder on the Calendar tab has been changed away from its inventoried default.

Action: Press Reset to default on event_reminder and issue the next request.

Expect: event_reminder is back to its inventoried default: 1 day before.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-available_dates_range Change available_dates_range applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting available_dates_range on the Calendar tab starts at its inventoried default.

Action: Change available_dates_range on its admin tab and issue the next request.

Expect: The new value of available_dates_range is in effect.; The audit row for available_dates_range has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-available_dates_range Reset available_dates_range to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_range on the Calendar tab has been changed away from its inventoried default.

Action: Press Reset to default on available_dates_range and issue the next request.

Expect: available_dates_range is back to its inventoried default: three months.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-available_dates_purpose Change available_dates_purpose applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting available_dates_purpose on the Calendar tab starts at its inventoried default.

Action: Change available_dates_purpose on its admin tab and issue the next request.

Expect: The new value of available_dates_purpose is in effect.; The audit row for available_dates_purpose has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-available_dates_purpose Reset available_dates_purpose to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_purpose on the Calendar tab has been changed away from its inventoried default.

Action: Press Reset to default on available_dates_purpose and issue the next request.

Expect: available_dates_purpose is back to its inventoried default: one day trial.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-available_dates_title Change available_dates_title applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting available_dates_title on the Calendar tab starts at its inventoried default.

Action: Change available_dates_title on its admin tab and issue the next request.

Expect: The new value of available_dates_title is in effect.; The audit row for available_dates_title has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-available_dates_title Reset available_dates_title to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_title on the Calendar tab has been changed away from its inventoried default.

Action: Press Reset to default on available_dates_title and issue the next request.

Expect: available_dates_title is back to its inventoried default: AVAILABLE DATES FOR HEARING.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-default_storage_backend Change default_storage_backend applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting default_storage_backend on the Files and storage tab starts at its inventoried default.

Action: Change default_storage_backend on its admin tab and issue the next request.

Expect: The new value of default_storage_backend is in effect.; The audit row for default_storage_backend has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-default_storage_backend Reset default_storage_backend to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_storage_backend on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on default_storage_backend and issue the next request.

Expect: default_storage_backend is back to its inventoried default: local disk.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-encryption_local_and_network Change encryption_local_and_network applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting encryption_local_and_network on the Files and storage tab starts at its inventoried default.

Action: Change encryption_local_and_network on its admin tab and issue the next request.

Expect: The new value of encryption_local_and_network is in effect.; The audit row for encryption_local_and_network has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-encryption_local_and_network Reset encryption_local_and_network to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: encryption_local_and_network on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on encryption_local_and_network and issue the next request.

Expect: encryption_local_and_network is back to its inventoried default: on.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-backup_backend_scope Change backup_backend_scope applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting backup_backend_scope on the Files and storage tab starts at its inventoried default.

Action: Change backup_backend_scope on its admin tab and issue the next request.

Expect: The new value of backup_backend_scope is in effect.; The audit row for backup_backend_scope has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-backup_backend_scope Reset backup_backend_scope to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: backup_backend_scope on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on backup_backend_scope and issue the next request.

Expect: backup_backend_scope is back to its inventoried default: all.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-largest_upload Change largest_upload applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting largest_upload on the Files and storage tab starts at its inventoried default.

Action: Change largest_upload on its admin tab and issue the next request.

Expect: The new value of largest_upload is in effect.; The audit row for largest_upload has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-largest_upload Reset largest_upload to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: largest_upload on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on largest_upload and issue the next request.

Expect: largest_upload is back to its inventoried default: 2 GB.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-upload_chunk Change upload_chunk applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting upload_chunk on the Files and storage tab starts at its inventoried default.

Action: Change upload_chunk on its admin tab and issue the next request.

Expect: The new value of upload_chunk is in effect.; The audit row for upload_chunk has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-upload_chunk Reset upload_chunk to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: upload_chunk on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on upload_chunk and issue the next request.

Expect: upload_chunk is back to its inventoried default: 8 MB.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-default_folders Change default_folders applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting default_folders on the Files and storage tab starts at its inventoried default.

Action: Change default_folders on its admin tab and issue the next request.

Expect: The new value of default_folders is in effect.; The audit row for default_folders has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-default_folders Reset default_folders to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_folders on the Files and storage tab has been changed away from its inventoried default.

Action: Press Reset to default on default_folders and issue the next request.

Expect: default_folders is back to its inventoried default: Correspondence, Pleadings, Discovery, Evidence, Client documents, Billing, Notes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-edit_lock_timeout Change edit_lock_timeout applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting edit_lock_timeout on the Document editor tab starts at its inventoried default.

Action: Change edit_lock_timeout on its admin tab and issue the next request.

Expect: The new value of edit_lock_timeout is in effect.; The audit row for edit_lock_timeout has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-edit_lock_timeout Reset edit_lock_timeout to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: edit_lock_timeout on the Document editor tab has been changed away from its inventoried default.

Action: Press Reset to default on edit_lock_timeout and issue the next request.

Expect: edit_lock_timeout is back to its inventoried default: 5 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-editor_runs_at_once Change editor_runs_at_once applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting editor_runs_at_once on the Document editor tab starts at its inventoried default.

Action: Change editor_runs_at_once on its admin tab and issue the next request.

Expect: The new value of editor_runs_at_once is in effect.; The audit row for editor_runs_at_once has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-editor_runs_at_once Reset editor_runs_at_once to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_runs_at_once on the Document editor tab has been changed away from its inventoried default.

Action: Press Reset to default on editor_runs_at_once and issue the next request.

Expect: editor_runs_at_once is back to its inventoried default: 2.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-editor_run_limits Change editor_run_limits applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting editor_run_limits on the Document editor tab starts at its inventoried default.

Action: Change editor_run_limits on its admin tab and issue the next request.

Expect: The new value of editor_run_limits is in effect.; The audit row for editor_run_limits has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-editor_run_limits Reset editor_run_limits to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_run_limits on the Document editor tab has been changed away from its inventoried default.

Action: Press Reset to default on editor_run_limits and issue the next request.

Expect: editor_run_limits is back to its inventoried default: 60 seconds and 1 GB.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-pdf_previews Change pdf_previews applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting pdf_previews on the Document editor tab starts at its inventoried default.

Action: Change pdf_previews on its admin tab and issue the next request.

Expect: The new value of pdf_previews is in effect.; The audit row for pdf_previews has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-pdf_previews Reset pdf_previews to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: pdf_previews on the Document editor tab has been changed away from its inventoried default.

Action: Press Reset to default on pdf_previews and issue the next request.

Expect: pdf_previews is back to its inventoried default: on.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_tender_enabled Change doc_tender_enabled applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_tender_enabled on the Doc Tender tab starts at its inventoried default.

Action: Change doc_tender_enabled on its admin tab and issue the next request.

Expect: The new value of doc_tender_enabled is in effect.; The audit row for doc_tender_enabled has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_tender_enabled Reset doc_tender_enabled to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_tender_enabled on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_tender_enabled and issue the next request.

Expect: doc_tender_enabled is back to its inventoried default: on.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-naming_pattern Change naming_pattern applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting naming_pattern on the Doc Tender tab starts at its inventoried default.

Action: Change naming_pattern on its admin tab and issue the next request.

Expect: The new value of naming_pattern is in effect.; The audit row for naming_pattern has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-naming_pattern Reset naming_pattern to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: naming_pattern on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on naming_pattern and issue the next request.

Expect: naming_pattern is back to its inventoried default: <clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-review_confidence Change review_confidence applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting review_confidence on the Doc Tender tab starts at its inventoried default.

Action: Change review_confidence on its admin tab and issue the next request.

Expect: The new value of review_confidence is in effect.; The audit row for review_confidence has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-review_confidence Reset review_confidence to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_confidence on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on review_confidence and issue the next request.

Expect: review_confidence is back to its inventoried default: 0.90.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-photo_group_quiet_time Change photo_group_quiet_time applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting photo_group_quiet_time on the Doc Tender tab starts at its inventoried default.

Action: Change photo_group_quiet_time on its admin tab and issue the next request.

Expect: The new value of photo_group_quiet_time is in effect.; The audit row for photo_group_quiet_time has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-photo_group_quiet_time Reset photo_group_quiet_time to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_group_quiet_time on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on photo_group_quiet_time and issue the next request.

Expect: photo_group_quiet_time is back to its inventoried default: 10 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-retry_schedule Change retry_schedule applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting retry_schedule on the Doc Tender tab starts at its inventoried default.

Action: Change retry_schedule on its admin tab and issue the next request.

Expect: The new value of retry_schedule is in effect.; The audit row for retry_schedule has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-retry_schedule Reset retry_schedule to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: retry_schedule on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on retry_schedule and issue the next request.

Expect: retry_schedule is back to its inventoried default: 6 attempts: 2 at once, then 15 minutes, 2 hours, 12 hours, and 24 hours.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-review_queue_recipients Change review_queue_recipients applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting review_queue_recipients on the Doc Tender tab starts at its inventoried default.

Action: Change review_queue_recipients on its admin tab and issue the next request.

Expect: The new value of review_queue_recipients is in effect.; The audit row for review_queue_recipients has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-review_queue_recipients Reset review_queue_recipients to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_queue_recipients on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on review_queue_recipients and issue the next request.

Expect: review_queue_recipients is back to its inventoried default: the admins.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-daily_summary_email Change daily_summary_email applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting daily_summary_email on the Doc Tender tab starts at its inventoried default.

Action: Change daily_summary_email on its admin tab and issue the next request.

Expect: The new value of daily_summary_email is in effect.; The audit row for daily_summary_email has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-daily_summary_email Reset daily_summary_email to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: daily_summary_email on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on daily_summary_email and issue the next request.

Expect: daily_summary_email is back to its inventoried default: on, 7:00 a.m. firm time.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-exception_email_bundle Change exception_email_bundle applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting exception_email_bundle on the Doc Tender tab starts at its inventoried default.

Action: Change exception_email_bundle on its admin tab and issue the next request.

Expect: The new value of exception_email_bundle is in effect.; The audit row for exception_email_bundle has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-exception_email_bundle Reset exception_email_bundle to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: exception_email_bundle on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on exception_email_bundle and issue the next request.

Expect: exception_email_bundle is back to its inventoried default: at most one email every 15 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-saved_layouts_cap Change saved_layouts_cap applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting saved_layouts_cap on the Home screen tab starts at its inventoried default.

Action: Change saved_layouts_cap on its admin tab and issue the next request.

Expect: The new value of saved_layouts_cap is in effect.; The audit row for saved_layouts_cap has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-saved_layouts_cap Reset saved_layouts_cap to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: saved_layouts_cap on the Home screen tab has been changed away from its inventoried default.

Action: Press Reset to default on saved_layouts_cap and issue the next request.

Expect: saved_layouts_cap is back to its inventoried default: 10.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-search_start Change search_start applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting search_start on the Lists and search tab starts at its inventoried default.

Action: Change search_start on its admin tab and issue the next request.

Expect: The new value of search_start is in effect.; The audit row for search_start has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-search_start Reset search_start to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_start on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on search_start and issue the next request.

Expect: search_start is back to its inventoried default: 2 characters.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-search_delay Change search_delay applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting search_delay on the Lists and search tab starts at its inventoried default.

Action: Change search_delay on its admin tab and issue the next request.

Expect: The new value of search_delay is in effect.; The audit row for search_delay has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-search_delay Reset search_delay to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_delay on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on search_delay and issue the next request.

Expect: search_delay is back to its inventoried default: 180 ms.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-search_hits Change search_hits applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting search_hits on the Lists and search tab starts at its inventoried default.

Action: Change search_hits on its admin tab and issue the next request.

Expect: The new value of search_hits is in effect.; The audit row for search_hits has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-search_hits Reset search_hits to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_hits on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on search_hits and issue the next request.

Expect: search_hits is back to its inventoried default: 5 in All, 8 in one scope.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-activity_window Change activity_window applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting activity_window on the Lists and search tab starts at its inventoried default.

Action: Change activity_window on its admin tab and issue the next request.

Expect: The new value of activity_window is in effect.; The audit row for activity_window has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-activity_window Reset activity_window to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_window on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on activity_window and issue the next request.

Expect: activity_window is back to its inventoried default: 30 days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-activity_page_size Change activity_page_size applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting activity_page_size on the Lists and search tab starts at its inventoried default.

Action: Change activity_page_size on its admin tab and issue the next request.

Expect: The new value of activity_page_size is in effect.; The audit row for activity_page_size has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-activity_page_size Reset activity_page_size to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_page_size on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on activity_page_size and issue the next request.

Expect: activity_page_size is back to its inventoried default: 100.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-notes_page_size Change notes_page_size applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting notes_page_size on the Lists and search tab starts at its inventoried default.

Action: Change notes_page_size on its admin tab and issue the next request.

Expect: The new value of notes_page_size is in effect.; The audit row for notes_page_size has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-notes_page_size Reset notes_page_size to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notes_page_size on the Lists and search tab has been changed away from its inventoried default.

Action: Press Reset to default on notes_page_size and issue the next request.

Expect: notes_page_size is back to its inventoried default: 25.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-problem_report_recipients Change problem_report_recipients applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting problem_report_recipients on the Notifications and mail tab starts at its inventoried default.

Action: Change problem_report_recipients on its admin tab and issue the next request.

Expect: The new value of problem_report_recipients is in effect.; The audit row for problem_report_recipients has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-problem_report_recipients Reset problem_report_recipients to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: problem_report_recipients on the Notifications and mail tab has been changed away from its inventoried default.

Action: Press Reset to default on problem_report_recipients and issue the next request.

Expect: problem_report_recipients is back to its inventoried default: the admins.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-notification_channel_defaults Change notification_channel_defaults applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting notification_channel_defaults on the Notifications and mail tab starts at its inventoried default.

Action: Change notification_channel_defaults on its admin tab and issue the next request.

Expect: The new value of notification_channel_defaults is in effect.; The audit row for notification_channel_defaults has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-notification_channel_defaults Reset notification_channel_defaults to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notification_channel_defaults on the Notifications and mail tab has been changed away from its inventoried default.

Action: Press Reset to default on notification_channel_defaults and issue the next request.

Expect: notification_channel_defaults is back to its inventoried default: every kind in-app; email also for trust flags, deadline reminders, update failures, and module alerts.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-appearance_mode Change appearance_mode applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting appearance_mode on the Appearance tab starts at its inventoried default.

Action: Change appearance_mode on its admin tab and issue the next request.

Expect: The new value of appearance_mode is in effect.; The audit row for appearance_mode has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-appearance_mode Reset appearance_mode to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: appearance_mode on the Appearance tab has been changed away from its inventoried default.

Action: Press Reset to default on appearance_mode and issue the next request.

Expect: appearance_mode is back to its inventoried default: follow the device.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-staff_portal_label Change staff_portal_label applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting staff_portal_label on the Appearance tab starts at its inventoried default.

Action: Change staff_portal_label on its admin tab and issue the next request.

Expect: The new value of staff_portal_label is in effect.; The audit row for staff_portal_label has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-staff_portal_label Reset staff_portal_label to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: staff_portal_label on the Appearance tab has been changed away from its inventoried default.

Action: Press Reset to default on staff_portal_label and issue the next request.

Expect: staff_portal_label is back to its inventoried default: Staff portal.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-marketplace_address Change marketplace_address applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting marketplace_address on the Modules and marketplace tab starts at its inventoried default.

Action: Change marketplace_address on its admin tab and issue the next request.

Expect: The new value of marketplace_address is in effect.; The audit row for marketplace_address has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-marketplace_address Reset marketplace_address to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_address on the Modules and marketplace tab has been changed away from its inventoried default.

Action: Press Reset to default on marketplace_address and issue the next request.

Expect: marketplace_address is back to its inventoried default: the Togavsu marketplace.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-marketplace_alert_frequency Change marketplace_alert_frequency applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting marketplace_alert_frequency on the Modules and marketplace tab starts at its inventoried default.

Action: Change marketplace_alert_frequency on its admin tab and issue the next request.

Expect: The new value of marketplace_alert_frequency is in effect.; The audit row for marketplace_alert_frequency has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-marketplace_alert_frequency Reset marketplace_alert_frequency to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_alert_frequency on the Modules and marketplace tab has been changed away from its inventoried default.

Action: Press Reset to default on marketplace_alert_frequency and issue the next request.

Expect: marketplace_alert_frequency is back to its inventoried default: daily.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-update_window Change update_window applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting update_window on the Updates tab starts at its inventoried default.

Action: Change update_window on its admin tab and issue the next request.

Expect: The new value of update_window is in effect.; The audit row for update_window has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-update_window Reset update_window to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: update_window on the Updates tab has been changed away from its inventoried default.

Action: Press Reset to default on update_window and issue the next request.

Expect: update_window is back to its inventoried default: 2:00 to 4:00 a.m. firm time.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-nightly_backup_time Change nightly_backup_time applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting nightly_backup_time on the Backups and logs tab starts at its inventoried default.

Action: Change nightly_backup_time on its admin tab and issue the next request.

Expect: The new value of nightly_backup_time is in effect.; The audit row for nightly_backup_time has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-nightly_backup_time Reset nightly_backup_time to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: nightly_backup_time on the Backups and logs tab has been changed away from its inventoried default.

Action: Press Reset to default on nightly_backup_time and issue the next request.

Expect: nightly_backup_time is back to its inventoried default: 1:00 a.m. firm time.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-local_backup_retention Change local_backup_retention applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting local_backup_retention on the Backups and logs tab starts at its inventoried default.

Action: Change local_backup_retention on its admin tab and issue the next request.

Expect: The new value of local_backup_retention is in effect.; The audit row for local_backup_retention has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-local_backup_retention Reset local_backup_retention to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: local_backup_retention on the Backups and logs tab has been changed away from its inventoried default.

Action: Press Reset to default on local_backup_retention and issue the next request.

Expect: local_backup_retention is back to its inventoried default: 30 days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-test_restore_day Change test_restore_day applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting test_restore_day on the Backups and logs tab starts at its inventoried default.

Action: Change test_restore_day on its admin tab and issue the next request.

Expect: The new value of test_restore_day is in effect.; The audit row for test_restore_day has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-test_restore_day Reset test_restore_day to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: test_restore_day on the Backups and logs tab has been changed away from its inventoried default.

Action: Press Reset to default on test_restore_day and issue the next request.

Expect: test_restore_day is back to its inventoried default: the 1st.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-log_retention Change log_retention applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting log_retention on the Backups and logs tab starts at its inventoried default.

Action: Change log_retention on its admin tab and issue the next request.

Expect: The new value of log_retention is in effect.; The audit row for log_retention has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-log_retention Reset log_retention to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: log_retention on the Backups and logs tab has been changed away from its inventoried default.

Action: Press Reset to default on log_retention and issue the next request.

Expect: log_retention is back to its inventoried default: 400 days.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-webhook_retries Change webhook_retries applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting webhook_retries on the Agents and API tab starts at its inventoried default.

Action: Change webhook_retries on its admin tab and issue the next request.

Expect: The new value of webhook_retries is in effect.; The audit row for webhook_retries has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-webhook_retries Reset webhook_retries to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: webhook_retries on the Agents and API tab has been changed away from its inventoried default.

Action: Press Reset to default on webhook_retries and issue the next request.

Expect: webhook_retries is back to its inventoried default: 8 over 24 hours.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-ai_timeout Change ai_timeout applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting ai_timeout on the AI and inference tab starts at its inventoried default.

Action: Change ai_timeout on its admin tab and issue the next request.

Expect: The new value of ai_timeout is in effect.; The audit row for ai_timeout has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-ai_timeout Reset ai_timeout to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ai_timeout on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on ai_timeout and issue the next request.

Expect: ai_timeout is back to its inventoried default: 30 seconds.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-labeling_model Change labeling_model applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting labeling_model on the AI and inference tab starts at its inventoried default.

Action: Change labeling_model on its admin tab and issue the next request.

Expect: The new value of labeling_model is in effect.; The audit row for labeling_model has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-labeling_model Reset labeling_model to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on labeling_model and issue the next request.

Expect: labeling_model is back to its inventoried default: gpt-6.1-sol.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-labeling_model_with_openai_key Change labeling_model_with_openai_key applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting labeling_model_with_openai_key on the AI and inference tab starts at its inventoried default.

Action: Change labeling_model_with_openai_key on its admin tab and issue the next request.

Expect: The new value of labeling_model_with_openai_key is in effect.; The audit row for labeling_model_with_openai_key has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-labeling_model_with_openai_key Reset labeling_model_with_openai_key to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_with_openai_key on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on labeling_model_with_openai_key and issue the next request.

Expect: labeling_model_with_openai_key is back to its inventoried default: gpt-6.1-sol when an OpenAI key is set.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-categorizing_model_with_openai_key Change categorizing_model_with_openai_key applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting categorizing_model_with_openai_key on the AI and inference tab starts at its inventoried default.

Action: Change categorizing_model_with_openai_key on its admin tab and issue the next request.

Expect: The new value of categorizing_model_with_openai_key is in effect.; The audit row for categorizing_model_with_openai_key has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-categorizing_model_with_openai_key Reset categorizing_model_with_openai_key to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_with_openai_key on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on categorizing_model_with_openai_key and issue the next request.

Expect: categorizing_model_with_openai_key is back to its inventoried default: gpt-6.1-sol when an OpenAI key is set.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-labeling_model_without_openai_key Change labeling_model_without_openai_key applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting labeling_model_without_openai_key on the AI and inference tab starts at its inventoried default.

Action: Change labeling_model_without_openai_key on its admin tab and issue the next request.

Expect: The new value of labeling_model_without_openai_key is in effect.; The audit row for labeling_model_without_openai_key has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-labeling_model_without_openai_key Reset labeling_model_without_openai_key to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_without_openai_key on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on labeling_model_without_openai_key and issue the next request.

Expect: labeling_model_without_openai_key is back to its inventoried default: primary provider's strong model.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-categorizing_model_without_openai_key Change categorizing_model_without_openai_key applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting categorizing_model_without_openai_key on the AI and inference tab starts at its inventoried default.

Action: Change categorizing_model_without_openai_key on its admin tab and issue the next request.

Expect: The new value of categorizing_model_without_openai_key is in effect.; The audit row for categorizing_model_without_openai_key has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-categorizing_model_without_openai_key Reset categorizing_model_without_openai_key to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_without_openai_key on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on categorizing_model_without_openai_key and issue the next request.

Expect: categorizing_model_without_openai_key is back to its inventoried default: primary provider's strong model.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-labeling_check_model_with_secondary Change labeling_check_model_with_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting labeling_check_model_with_secondary on the AI and inference tab starts at its inventoried default.

Action: Change labeling_check_model_with_secondary on its admin tab and issue the next request.

Expect: The new value of labeling_check_model_with_secondary is in effect.; The audit row for labeling_check_model_with_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-labeling_check_model_with_secondary Reset labeling_check_model_with_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_with_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on labeling_check_model_with_secondary and issue the next request.

Expect: labeling_check_model_with_secondary is back to its inventoried default: secondary provider's model for labeling.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-categorizing_check_model_with_secondary Change categorizing_check_model_with_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting categorizing_check_model_with_secondary on the AI and inference tab starts at its inventoried default.

Action: Change categorizing_check_model_with_secondary on its admin tab and issue the next request.

Expect: The new value of categorizing_check_model_with_secondary is in effect.; The audit row for categorizing_check_model_with_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-categorizing_check_model_with_secondary Reset categorizing_check_model_with_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_with_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on categorizing_check_model_with_secondary and issue the next request.

Expect: categorizing_check_model_with_secondary is back to its inventoried default: secondary provider's model for categorizing.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-labeling_check_model_without_secondary Change labeling_check_model_without_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting labeling_check_model_without_secondary on the AI and inference tab starts at its inventoried default.

Action: Change labeling_check_model_without_secondary on its admin tab and issue the next request.

Expect: The new value of labeling_check_model_without_secondary is in effect.; The audit row for labeling_check_model_without_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-labeling_check_model_without_secondary Reset labeling_check_model_without_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_without_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on labeling_check_model_without_secondary and issue the next request.

Expect: labeling_check_model_without_secondary is back to its inventoried default: primary provider's other tier.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-categorizing_check_model_without_secondary Change categorizing_check_model_without_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting categorizing_check_model_without_secondary on the AI and inference tab starts at its inventoried default.

Action: Change categorizing_check_model_without_secondary on its admin tab and issue the next request.

Expect: The new value of categorizing_check_model_without_secondary is in effect.; The audit row for categorizing_check_model_without_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-categorizing_check_model_without_secondary Reset categorizing_check_model_without_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_without_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on categorizing_check_model_without_secondary and issue the next request.

Expect: categorizing_check_model_without_secondary is back to its inventoried default: primary provider's other tier.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-ocr_check_model_with_secondary Change ocr_check_model_with_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting ocr_check_model_with_secondary on the AI and inference tab starts at its inventoried default.

Action: Change ocr_check_model_with_secondary on its admin tab and issue the next request.

Expect: The new value of ocr_check_model_with_secondary is in effect.; The audit row for ocr_check_model_with_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-ocr_check_model_with_secondary Reset ocr_check_model_with_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_with_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on ocr_check_model_with_secondary and issue the next request.

Expect: ocr_check_model_with_secondary is back to its inventoried default: secondary provider's model for OCR.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-ocr_check_model_without_secondary Change ocr_check_model_without_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting ocr_check_model_without_secondary on the AI and inference tab starts at its inventoried default.

Action: Change ocr_check_model_without_secondary on its admin tab and issue the next request.

Expect: The new value of ocr_check_model_without_secondary is in effect.; The audit row for ocr_check_model_without_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-ocr_check_model_without_secondary Reset ocr_check_model_without_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_without_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on ocr_check_model_without_secondary and issue the next request.

Expect: ocr_check_model_without_secondary is back to its inventoried default: primary provider's other tier for OCR.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-photo_description_grouping_check_model_with_secondary Change photo_description_grouping_check_model_with_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting photo_description_grouping_check_model_with_secondary on the AI and inference tab starts at its inventoried default.

Action: Change photo_description_grouping_check_model_with_secondary on its admin tab and issue the next request.

Expect: The new value of photo_description_grouping_check_model_with_secondary is in effect.; The audit row for photo_description_grouping_check_model_with_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-photo_description_grouping_check_model_with_secondary Reset photo_description_grouping_check_model_with_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_with_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on photo_description_grouping_check_model_with_secondary and issue the next request.

Expect: photo_description_grouping_check_model_with_secondary is back to its inventoried default: secondary provider's model for photo description and grouping.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-photo_description_grouping_check_model_without_secondary Change photo_description_grouping_check_model_without_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting photo_description_grouping_check_model_without_secondary on the AI and inference tab starts at its inventoried default.

Action: Change photo_description_grouping_check_model_without_secondary on its admin tab and issue the next request.

Expect: The new value of photo_description_grouping_check_model_without_secondary is in effect.; The audit row for photo_description_grouping_check_model_without_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-photo_description_grouping_check_model_without_secondary Reset photo_description_grouping_check_model_without_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_without_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on photo_description_grouping_check_model_without_secondary and issue the next request.

Expect: photo_description_grouping_check_model_without_secondary is back to its inventoried default: primary provider's other tier for photo description and grouping.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-transcription_check_model_with_secondary Change transcription_check_model_with_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting transcription_check_model_with_secondary on the AI and inference tab starts at its inventoried default.

Action: Change transcription_check_model_with_secondary on its admin tab and issue the next request.

Expect: The new value of transcription_check_model_with_secondary is in effect.; The audit row for transcription_check_model_with_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-transcription_check_model_with_secondary Reset transcription_check_model_with_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_with_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on transcription_check_model_with_secondary and issue the next request.

Expect: transcription_check_model_with_secondary is back to its inventoried default: secondary provider's model for transcription.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-transcription_check_model_without_secondary Change transcription_check_model_without_secondary applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting transcription_check_model_without_secondary on the AI and inference tab starts at its inventoried default.

Action: Change transcription_check_model_without_secondary on its admin tab and issue the next request.

Expect: The new value of transcription_check_model_without_secondary is in effect.; The audit row for transcription_check_model_without_secondary has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-transcription_check_model_without_secondary Reset transcription_check_model_without_secondary to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_without_secondary on the AI and inference tab has been changed away from its inventoried default.

Action: Press Reset to default on transcription_check_model_without_secondary and issue the next request.

Expect: transcription_check_model_without_secondary is back to its inventoried default: primary provider's other tier for transcription.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-high_tier_categories Change high_tier_categories applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting high_tier_categories on the Doc Tender tab starts at its inventoried default.

Action: Change high_tier_categories on its admin tab and issue the next request.

Expect: The new value of high_tier_categories is in effect.; The audit row for high_tier_categories has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-high_tier_categories Reset high_tier_categories to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: high_tier_categories on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on high_tier_categories and issue the next request.

Expect: high_tier_categories is back to its inventoried default: court_filing, court_notice, discovery, agency_notice, police_criminal, estate_planning, contracts_agreements.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_court_filing Change doc_category_court_filing applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_court_filing on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_court_filing on its admin tab and issue the next request.

Expect: The new value of doc_category_court_filing is in effect.; The audit row for doc_category_court_filing has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_court_filing Reset doc_category_court_filing to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_filing on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_court_filing and issue the next request.

Expect: doc_category_court_filing is back to its inventoried default: court_filing: Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing; folder Pleadings; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_court_notice Change doc_category_court_notice applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_court_notice on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_court_notice on its admin tab and issue the next request.

Expect: The new value of doc_category_court_notice is in effect.; The audit row for doc_category_court_notice has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_court_notice Reset doc_category_court_notice to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_notice on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_court_notice and issue the next request.

Expect: doc_category_court_notice is back to its inventoried default: court_notice: Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link; folder Pleadings; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_discovery Change doc_category_discovery applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_discovery on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_discovery on its admin tab and issue the next request.

Expect: The new value of doc_category_discovery is in effect.; The audit row for doc_category_discovery has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_discovery Reset doc_category_discovery to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_discovery on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_discovery and issue the next request.

Expect: doc_category_discovery is back to its inventoried default: discovery: Interrogatories, requests for production or admission, responses, depositions; folder Discovery; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_correspondence Change doc_category_correspondence applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_correspondence on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_correspondence on its admin tab and issue the next request.

Expect: The new value of doc_category_correspondence is in effect.; The audit row for doc_category_correspondence has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_correspondence Reset doc_category_correspondence to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_correspondence on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_correspondence and issue the next request.

Expect: doc_category_correspondence is back to its inventoried default: correspondence: Letter or email to or from the client, counsel or a third party, including plea offers; folder Correspondence; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_agency_notice Change doc_category_agency_notice applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_agency_notice on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_agency_notice on its admin tab and issue the next request.

Expect: The new value of doc_category_agency_notice is in effect.; The audit row for doc_category_agency_notice has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_agency_notice Reset doc_category_agency_notice to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_agency_notice on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_agency_notice and issue the next request.

Expect: doc_category_agency_notice is back to its inventoried default: agency_notice: Letter from a government agency, such as child support, IRS, DOR, Children's Division or MSPD; folder Correspondence; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_estate_planning Change doc_category_estate_planning applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_estate_planning on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_estate_planning on its admin tab and issue the next request.

Expect: The new value of doc_category_estate_planning is in effect.; The audit row for doc_category_estate_planning has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_estate_planning Reset doc_category_estate_planning to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_estate_planning on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_estate_planning and issue the next request.

Expect: doc_category_estate_planning is back to its inventoried default: estate_planning: The will, trust, power of attorney, directive or beneficiary deed itself; folder Client documents; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_property_title Change doc_category_property_title applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_property_title on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_property_title on its admin tab and issue the next request.

Expect: The new value of doc_category_property_title is in effect.; The audit row for doc_category_property_title has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_property_title Reset doc_category_property_title to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_property_title on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_property_title and issue the next request.

Expect: doc_category_property_title is back to its inventoried default: property_title: Deed, deed of trust, mortgage release, secured note, survey, title; folder Client documents; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_financial_record Change doc_category_financial_record applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_financial_record on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_financial_record on its admin tab and issue the next request.

Expect: The new value of doc_category_financial_record is in effect.; The audit row for doc_category_financial_record has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_financial_record Reset doc_category_financial_record to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_financial_record on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_financial_record and issue the next request.

Expect: doc_category_financial_record is back to its inventoried default: financial_record: Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form; folder Client documents; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_personal_record Change doc_category_personal_record applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_personal_record on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_personal_record on its admin tab and issue the next request.

Expect: The new value of doc_category_personal_record is in effect.; The audit row for doc_category_personal_record has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_personal_record Reset doc_category_personal_record to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_personal_record on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_personal_record and issue the next request.

Expect: doc_category_personal_record is back to its inventoried default: personal_record: ID, birth, death or dissolution certificate, driving record, completion certificate; folder Client documents; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_medical_insurance Change doc_category_medical_insurance applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_medical_insurance on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_medical_insurance on its admin tab and issue the next request.

Expect: The new value of doc_category_medical_insurance is in effect.; The audit row for doc_category_medical_insurance has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_medical_insurance Reset doc_category_medical_insurance to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_medical_insurance on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_medical_insurance and issue the next request.

Expect: doc_category_medical_insurance is back to its inventoried default: medical_insurance: Medical record or evaluation, health or vision insurance document; folder Client documents; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_contracts_agreements Change doc_category_contracts_agreements applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_contracts_agreements on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_contracts_agreements on its admin tab and issue the next request.

Expect: The new value of doc_category_contracts_agreements is in effect.; The audit row for doc_category_contracts_agreements has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_contracts_agreements Reset doc_category_contracts_agreements to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_contracts_agreements on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_contracts_agreements and issue the next request.

Expect: doc_category_contracts_agreements is back to its inventoried default: contracts_agreements: Contract between parties, prenup, articles, bylaws or operating agreement; folder Client documents; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_police_criminal Change doc_category_police_criminal applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_police_criminal on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_police_criminal on its admin tab and issue the next request.

Expect: The new value of doc_category_police_criminal is in effect.; The audit row for doc_category_police_criminal has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_police_criminal Reset doc_category_police_criminal to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_police_criminal on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_police_criminal and issue the next request.

Expect: doc_category_police_criminal is back to its inventoried default: police_criminal: Police report, probable cause statement, citation, bodycam report, restitution; folder Evidence; tier high.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_evidence_media Change doc_category_evidence_media applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_evidence_media on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_evidence_media on its admin tab and issue the next request.

Expect: The new value of doc_category_evidence_media is in effect.; The audit row for doc_category_evidence_media has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_evidence_media Reset doc_category_evidence_media to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_evidence_media on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_evidence_media and issue the next request.

Expect: doc_category_evidence_media is back to its inventoried default: evidence_media: Photo, video, screenshot, text messages or call log, including phone message exports; folder Evidence; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_call_meeting Change doc_category_call_meeting applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_call_meeting on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_call_meeting on its admin tab and issue the next request.

Expect: The new value of doc_category_call_meeting is in effect.; The audit row for doc_category_call_meeting has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_call_meeting Reset doc_category_call_meeting to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_call_meeting on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_call_meeting and issue the next request.

Expect: doc_category_call_meeting is back to its inventoried default: call_meeting: Recording, transcript or notes of a phone call, voicemail or meeting; folder Notes; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_engagement_intake Change doc_category_engagement_intake applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_engagement_intake on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_engagement_intake on its admin tab and issue the next request.

Expect: The new value of doc_category_engagement_intake is in effect.; The audit row for doc_category_engagement_intake has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_engagement_intake Reset doc_category_engagement_intake to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_engagement_intake on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_engagement_intake and issue the next request.

Expect: doc_category_engagement_intake is back to its inventoried default: engagement_intake: This firm's fee agreement, invoice, intake or consultation notes, MSPD forms; folder Billing; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_firm_work_product Change doc_category_firm_work_product applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_firm_work_product on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_firm_work_product on its admin tab and issue the next request.

Expect: The new value of doc_category_firm_work_product is in effect.; The audit row for doc_category_firm_work_product has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_firm_work_product Reset doc_category_firm_work_product to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_firm_work_product on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_firm_work_product and issue the next request.

Expect: doc_category_firm_work_product is back to its inventoried default: firm_work_product: This firm's memo, research, hearing or trial prep, case notes; folder Notes; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P2-doc_category_reference Change doc_category_reference applies on the next request

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Record the process start time. Setting doc_category_reference on the Doc Tender tab starts at its inventoried default.

Action: Change doc_category_reference on its admin tab and issue the next request.

Expect: The new value of doc_category_reference is in effect.; The audit row for doc_category_reference has the old value and the new value.; The process start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-P3-doc_category_reference Reset doc_category_reference to its default

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_reference on the Doc Tender tab has been changed away from its inventoried default.

Action: Press Reset to default on doc_category_reference and issue the next request.

Expect: doc_category_reference is back to its inventoried default: reference: Article, statute, template, logo or blank form; not about a client matter; folder Notes; tier standard.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-029-F1 A named default is missing

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Registry dump.

Action: Diff it against the inventory, including categories and conditional labeling, categorizing, and check-model defaults.

Expect: Any missing key or wrong default fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-matter_number_format Change to matter_number_format requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: matter_number_format on the Firm profile tab was changed.

Action: Issue the next request without a restart.

Expect: matter_number_format still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-matter_number_format Reset leaves matter_number_format changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: matter_number_format was changed away from its inventoried default.

Action: Reset matter_number_format and read it.

Expect: A value of matter_number_format other than YYYY-NNNN fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-totp_by_role Change to totp_by_role requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: totp_by_role on the Security tab was changed.

Action: Issue the next request without a restart.

Expect: totp_by_role still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-totp_by_role Reset leaves totp_by_role changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: totp_by_role was changed away from its inventoried default.

Action: Reset totp_by_role and read it.

Expect: A value of totp_by_role other than required for admin, attorney, staff, and accountant; optional for client; admin always required fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-idle_timeout Change to idle_timeout requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: idle_timeout on the Security tab was changed.

Action: Issue the next request without a restart.

Expect: idle_timeout still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-idle_timeout Reset leaves idle_timeout changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: idle_timeout was changed away from its inventoried default.

Action: Reset idle_timeout and read it.

Expect: A value of idle_timeout other than 12 hours fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-failed_sign_ins_before_lockout Change to failed_sign_ins_before_lockout requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: failed_sign_ins_before_lockout on the Security tab was changed.

Action: Issue the next request without a restart.

Expect: failed_sign_ins_before_lockout still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-failed_sign_ins_before_lockout Reset leaves failed_sign_ins_before_lockout changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: failed_sign_ins_before_lockout was changed away from its inventoried default.

Action: Reset failed_sign_ins_before_lockout and read it.

Expect: A value of failed_sign_ins_before_lockout other than 5 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-lockout_length Change to lockout_length requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: lockout_length on the Security tab was changed.

Action: Issue the next request without a restart.

Expect: lockout_length still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-lockout_length Reset leaves lockout_length changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: lockout_length was changed away from its inventoried default.

Action: Reset lockout_length and read it.

Expect: A value of lockout_length other than 15 minutes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-api_rate_limit Change to api_rate_limit requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: api_rate_limit on the Security tab was changed.

Action: Issue the next request without a restart.

Expect: api_rate_limit still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-api_rate_limit Reset leaves api_rate_limit changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: api_rate_limit was changed away from its inventoried default.

Action: Reset api_rate_limit and read it.

Expect: A value of api_rate_limit other than 600 requests a minute per token fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-case_number_pattern Change to case_number_pattern requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: case_number_pattern on the Practice tab was changed.

Action: Issue the next request without a restart.

Expect: case_number_pattern still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-case_number_pattern Reset leaves case_number_pattern changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: case_number_pattern was changed away from its inventoried default.

Action: Reset case_number_pattern and read it.

Expect: A value of case_number_pattern other than Missouri docket numbers fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-conflict_score_floor Change to conflict_score_floor requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: conflict_score_floor on the Practice tab was changed.

Action: Issue the next request without a restart.

Expect: conflict_score_floor still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-conflict_score_floor Reset leaves conflict_score_floor changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: conflict_score_floor was changed away from its inventoried default.

Action: Reset conflict_score_floor and read it.

Expect: A value of conflict_score_floor other than 60 out of 100 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-time_increment Change to time_increment requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: time_increment on the Time and billing tab was changed.

Action: Issue the next request without a restart.

Expect: time_increment still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-time_increment Reset leaves time_increment changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: time_increment was changed away from its inventoried default.

Action: Reset time_increment and read it.

Expect: A value of time_increment other than 6 minutes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-expense_markup Change to expense_markup requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: expense_markup on the Time and billing tab was changed.

Action: Issue the next request without a restart.

Expect: expense_markup still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-expense_markup Reset leaves expense_markup changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: expense_markup was changed away from its inventoried default.

Action: Reset expense_markup and read it.

Expect: A value of expense_markup other than 0 percent fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-payment_terms Change to payment_terms requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: payment_terms on the Time and billing tab was changed.

Action: Issue the next request without a restart.

Expect: payment_terms still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-payment_terms Reset leaves payment_terms changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: payment_terms was changed away from its inventoried default.

Action: Reset payment_terms and read it.

Expect: A value of payment_terms other than due in 30 days fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-grace_period Change to grace_period requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: grace_period on the Time and billing tab was changed.

Action: Issue the next request without a restart.

Expect: grace_period still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-grace_period Reset leaves grace_period changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: grace_period was changed away from its inventoried default.

Action: Reset grace_period and read it.

Expect: A value of grace_period other than 15 days fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-late_interest Change to late_interest requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: late_interest on the Time and billing tab was changed.

Action: Issue the next request without a restart.

Expect: late_interest still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-late_interest Reset leaves late_interest changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: late_interest was changed away from its inventoried default.

Action: Reset late_interest and read it.

Expect: A value of late_interest other than off fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-evergreen_minimum Change to evergreen_minimum requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: evergreen_minimum on the Trust tab was changed.

Action: Issue the next request without a restart.

Expect: evergreen_minimum still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-evergreen_minimum Reset leaves evergreen_minimum changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: evergreen_minimum was changed away from its inventoried default.

Action: Reset evergreen_minimum and read it.

Expect: A value of evergreen_minimum other than none fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-reconciliation_reminder_day Change to reconciliation_reminder_day requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: reconciliation_reminder_day on the Trust tab was changed.

Action: Issue the next request without a restart.

Expect: reconciliation_reminder_day still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-reconciliation_reminder_day Reset leaves reconciliation_reminder_day changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: reconciliation_reminder_day was changed away from its inventoried default.

Action: Reset reconciliation_reminder_day and read it.

Expect: A value of reconciliation_reminder_day other than the 5th fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-deadline_reminders Change to deadline_reminders requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: deadline_reminders on the Calendar tab was changed.

Action: Issue the next request without a restart.

Expect: deadline_reminders still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-deadline_reminders Reset leaves deadline_reminders changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: deadline_reminders was changed away from its inventoried default.

Action: Reset deadline_reminders and read it.

Expect: A value of deadline_reminders other than 7 days and 1 day before fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-event_reminder Change to event_reminder requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: event_reminder on the Calendar tab was changed.

Action: Issue the next request without a restart.

Expect: event_reminder still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-event_reminder Reset leaves event_reminder changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: event_reminder was changed away from its inventoried default.

Action: Reset event_reminder and read it.

Expect: A value of event_reminder other than 1 day before fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-available_dates_range Change to available_dates_range requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_range on the Calendar tab was changed.

Action: Issue the next request without a restart.

Expect: available_dates_range still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-available_dates_range Reset leaves available_dates_range changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_range was changed away from its inventoried default.

Action: Reset available_dates_range and read it.

Expect: A value of available_dates_range other than three months fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-available_dates_purpose Change to available_dates_purpose requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_purpose on the Calendar tab was changed.

Action: Issue the next request without a restart.

Expect: available_dates_purpose still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-available_dates_purpose Reset leaves available_dates_purpose changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_purpose was changed away from its inventoried default.

Action: Reset available_dates_purpose and read it.

Expect: A value of available_dates_purpose other than one day trial fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-available_dates_title Change to available_dates_title requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_title on the Calendar tab was changed.

Action: Issue the next request without a restart.

Expect: available_dates_title still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-available_dates_title Reset leaves available_dates_title changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: available_dates_title was changed away from its inventoried default.

Action: Reset available_dates_title and read it.

Expect: A value of available_dates_title other than AVAILABLE DATES FOR HEARING fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-default_storage_backend Change to default_storage_backend requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_storage_backend on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: default_storage_backend still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-default_storage_backend Reset leaves default_storage_backend changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_storage_backend was changed away from its inventoried default.

Action: Reset default_storage_backend and read it.

Expect: A value of default_storage_backend other than local disk fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-encryption_local_and_network Change to encryption_local_and_network requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: encryption_local_and_network on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: encryption_local_and_network still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-encryption_local_and_network Reset leaves encryption_local_and_network changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: encryption_local_and_network was changed away from its inventoried default.

Action: Reset encryption_local_and_network and read it.

Expect: A value of encryption_local_and_network other than on fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-backup_backend_scope Change to backup_backend_scope requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: backup_backend_scope on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: backup_backend_scope still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-backup_backend_scope Reset leaves backup_backend_scope changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: backup_backend_scope was changed away from its inventoried default.

Action: Reset backup_backend_scope and read it.

Expect: A value of backup_backend_scope other than all fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-largest_upload Change to largest_upload requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: largest_upload on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: largest_upload still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-largest_upload Reset leaves largest_upload changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: largest_upload was changed away from its inventoried default.

Action: Reset largest_upload and read it.

Expect: A value of largest_upload other than 2 GB fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-upload_chunk Change to upload_chunk requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: upload_chunk on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: upload_chunk still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-upload_chunk Reset leaves upload_chunk changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: upload_chunk was changed away from its inventoried default.

Action: Reset upload_chunk and read it.

Expect: A value of upload_chunk other than 8 MB fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-default_folders Change to default_folders requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_folders on the Files and storage tab was changed.

Action: Issue the next request without a restart.

Expect: default_folders still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-default_folders Reset leaves default_folders changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: default_folders was changed away from its inventoried default.

Action: Reset default_folders and read it.

Expect: A value of default_folders other than Correspondence, Pleadings, Discovery, Evidence, Client documents, Billing, Notes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-edit_lock_timeout Change to edit_lock_timeout requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: edit_lock_timeout on the Document editor tab was changed.

Action: Issue the next request without a restart.

Expect: edit_lock_timeout still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-edit_lock_timeout Reset leaves edit_lock_timeout changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: edit_lock_timeout was changed away from its inventoried default.

Action: Reset edit_lock_timeout and read it.

Expect: A value of edit_lock_timeout other than 5 minutes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-editor_runs_at_once Change to editor_runs_at_once requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_runs_at_once on the Document editor tab was changed.

Action: Issue the next request without a restart.

Expect: editor_runs_at_once still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-editor_runs_at_once Reset leaves editor_runs_at_once changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_runs_at_once was changed away from its inventoried default.

Action: Reset editor_runs_at_once and read it.

Expect: A value of editor_runs_at_once other than 2 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-editor_run_limits Change to editor_run_limits requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_run_limits on the Document editor tab was changed.

Action: Issue the next request without a restart.

Expect: editor_run_limits still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-editor_run_limits Reset leaves editor_run_limits changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: editor_run_limits was changed away from its inventoried default.

Action: Reset editor_run_limits and read it.

Expect: A value of editor_run_limits other than 60 seconds and 1 GB fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-pdf_previews Change to pdf_previews requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: pdf_previews on the Document editor tab was changed.

Action: Issue the next request without a restart.

Expect: pdf_previews still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-pdf_previews Reset leaves pdf_previews changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: pdf_previews was changed away from its inventoried default.

Action: Reset pdf_previews and read it.

Expect: A value of pdf_previews other than on fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_tender_enabled Change to doc_tender_enabled requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_tender_enabled on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_tender_enabled still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_tender_enabled Reset leaves doc_tender_enabled changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_tender_enabled was changed away from its inventoried default.

Action: Reset doc_tender_enabled and read it.

Expect: A value of doc_tender_enabled other than on fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-naming_pattern Change to naming_pattern requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: naming_pattern on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: naming_pattern still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-naming_pattern Reset leaves naming_pattern changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: naming_pattern was changed away from its inventoried default.

Action: Reset naming_pattern and read it.

Expect: A value of naming_pattern other than <clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext> fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-review_confidence Change to review_confidence requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_confidence on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: review_confidence still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-review_confidence Reset leaves review_confidence changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_confidence was changed away from its inventoried default.

Action: Reset review_confidence and read it.

Expect: A value of review_confidence other than 0.90 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-photo_group_quiet_time Change to photo_group_quiet_time requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_group_quiet_time on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: photo_group_quiet_time still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-photo_group_quiet_time Reset leaves photo_group_quiet_time changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_group_quiet_time was changed away from its inventoried default.

Action: Reset photo_group_quiet_time and read it.

Expect: A value of photo_group_quiet_time other than 10 minutes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-retry_schedule Change to retry_schedule requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: retry_schedule on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: retry_schedule still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-retry_schedule Reset leaves retry_schedule changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: retry_schedule was changed away from its inventoried default.

Action: Reset retry_schedule and read it.

Expect: A value of retry_schedule other than 6 attempts: 2 at once, then 15 minutes, 2 hours, 12 hours, and 24 hours fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-review_queue_recipients Change to review_queue_recipients requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_queue_recipients on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: review_queue_recipients still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-review_queue_recipients Reset leaves review_queue_recipients changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: review_queue_recipients was changed away from its inventoried default.

Action: Reset review_queue_recipients and read it.

Expect: A value of review_queue_recipients other than the admins fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-daily_summary_email Change to daily_summary_email requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: daily_summary_email on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: daily_summary_email still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-daily_summary_email Reset leaves daily_summary_email changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: daily_summary_email was changed away from its inventoried default.

Action: Reset daily_summary_email and read it.

Expect: A value of daily_summary_email other than on, 7:00 a.m. firm time fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-exception_email_bundle Change to exception_email_bundle requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: exception_email_bundle on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: exception_email_bundle still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-exception_email_bundle Reset leaves exception_email_bundle changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: exception_email_bundle was changed away from its inventoried default.

Action: Reset exception_email_bundle and read it.

Expect: A value of exception_email_bundle other than at most one email every 15 minutes fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-saved_layouts_cap Change to saved_layouts_cap requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: saved_layouts_cap on the Home screen tab was changed.

Action: Issue the next request without a restart.

Expect: saved_layouts_cap still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-saved_layouts_cap Reset leaves saved_layouts_cap changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: saved_layouts_cap was changed away from its inventoried default.

Action: Reset saved_layouts_cap and read it.

Expect: A value of saved_layouts_cap other than 10 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-search_start Change to search_start requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_start on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: search_start still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-search_start Reset leaves search_start changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_start was changed away from its inventoried default.

Action: Reset search_start and read it.

Expect: A value of search_start other than 2 characters fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-search_delay Change to search_delay requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_delay on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: search_delay still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-search_delay Reset leaves search_delay changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_delay was changed away from its inventoried default.

Action: Reset search_delay and read it.

Expect: A value of search_delay other than 180 ms fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-search_hits Change to search_hits requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_hits on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: search_hits still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-search_hits Reset leaves search_hits changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: search_hits was changed away from its inventoried default.

Action: Reset search_hits and read it.

Expect: A value of search_hits other than 5 in All, 8 in one scope fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-activity_window Change to activity_window requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_window on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: activity_window still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-activity_window Reset leaves activity_window changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_window was changed away from its inventoried default.

Action: Reset activity_window and read it.

Expect: A value of activity_window other than 30 days fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-activity_page_size Change to activity_page_size requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_page_size on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: activity_page_size still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-activity_page_size Reset leaves activity_page_size changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: activity_page_size was changed away from its inventoried default.

Action: Reset activity_page_size and read it.

Expect: A value of activity_page_size other than 100 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-notes_page_size Change to notes_page_size requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notes_page_size on the Lists and search tab was changed.

Action: Issue the next request without a restart.

Expect: notes_page_size still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-notes_page_size Reset leaves notes_page_size changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notes_page_size was changed away from its inventoried default.

Action: Reset notes_page_size and read it.

Expect: A value of notes_page_size other than 25 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-problem_report_recipients Change to problem_report_recipients requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: problem_report_recipients on the Notifications and mail tab was changed.

Action: Issue the next request without a restart.

Expect: problem_report_recipients still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-problem_report_recipients Reset leaves problem_report_recipients changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: problem_report_recipients was changed away from its inventoried default.

Action: Reset problem_report_recipients and read it.

Expect: A value of problem_report_recipients other than the admins fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-notification_channel_defaults Change to notification_channel_defaults requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notification_channel_defaults on the Notifications and mail tab was changed.

Action: Issue the next request without a restart.

Expect: notification_channel_defaults still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-notification_channel_defaults Reset leaves notification_channel_defaults changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: notification_channel_defaults was changed away from its inventoried default.

Action: Reset notification_channel_defaults and read it.

Expect: A value of notification_channel_defaults other than every kind in-app; email also for trust flags, deadline reminders, update failures, and module alerts fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-appearance_mode Change to appearance_mode requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: appearance_mode on the Appearance tab was changed.

Action: Issue the next request without a restart.

Expect: appearance_mode still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-appearance_mode Reset leaves appearance_mode changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: appearance_mode was changed away from its inventoried default.

Action: Reset appearance_mode and read it.

Expect: A value of appearance_mode other than follow the device fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-staff_portal_label Change to staff_portal_label requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: staff_portal_label on the Appearance tab was changed.

Action: Issue the next request without a restart.

Expect: staff_portal_label still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-staff_portal_label Reset leaves staff_portal_label changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: staff_portal_label was changed away from its inventoried default.

Action: Reset staff_portal_label and read it.

Expect: A value of staff_portal_label other than Staff portal fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-marketplace_address Change to marketplace_address requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_address on the Modules and marketplace tab was changed.

Action: Issue the next request without a restart.

Expect: marketplace_address still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-marketplace_address Reset leaves marketplace_address changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_address was changed away from its inventoried default.

Action: Reset marketplace_address and read it.

Expect: A value of marketplace_address other than the Togavsu marketplace fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-marketplace_alert_frequency Change to marketplace_alert_frequency requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_alert_frequency on the Modules and marketplace tab was changed.

Action: Issue the next request without a restart.

Expect: marketplace_alert_frequency still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-marketplace_alert_frequency Reset leaves marketplace_alert_frequency changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: marketplace_alert_frequency was changed away from its inventoried default.

Action: Reset marketplace_alert_frequency and read it.

Expect: A value of marketplace_alert_frequency other than daily fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-update_window Change to update_window requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: update_window on the Updates tab was changed.

Action: Issue the next request without a restart.

Expect: update_window still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-update_window Reset leaves update_window changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: update_window was changed away from its inventoried default.

Action: Reset update_window and read it.

Expect: A value of update_window other than 2:00 to 4:00 a.m. firm time fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-nightly_backup_time Change to nightly_backup_time requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: nightly_backup_time on the Backups and logs tab was changed.

Action: Issue the next request without a restart.

Expect: nightly_backup_time still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-nightly_backup_time Reset leaves nightly_backup_time changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: nightly_backup_time was changed away from its inventoried default.

Action: Reset nightly_backup_time and read it.

Expect: A value of nightly_backup_time other than 1:00 a.m. firm time fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-local_backup_retention Change to local_backup_retention requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: local_backup_retention on the Backups and logs tab was changed.

Action: Issue the next request without a restart.

Expect: local_backup_retention still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-local_backup_retention Reset leaves local_backup_retention changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: local_backup_retention was changed away from its inventoried default.

Action: Reset local_backup_retention and read it.

Expect: A value of local_backup_retention other than 30 days fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-test_restore_day Change to test_restore_day requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: test_restore_day on the Backups and logs tab was changed.

Action: Issue the next request without a restart.

Expect: test_restore_day still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-test_restore_day Reset leaves test_restore_day changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: test_restore_day was changed away from its inventoried default.

Action: Reset test_restore_day and read it.

Expect: A value of test_restore_day other than the 1st fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-log_retention Change to log_retention requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: log_retention on the Backups and logs tab was changed.

Action: Issue the next request without a restart.

Expect: log_retention still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-log_retention Reset leaves log_retention changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: log_retention was changed away from its inventoried default.

Action: Reset log_retention and read it.

Expect: A value of log_retention other than 400 days fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-webhook_retries Change to webhook_retries requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: webhook_retries on the Agents and API tab was changed.

Action: Issue the next request without a restart.

Expect: webhook_retries still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-webhook_retries Reset leaves webhook_retries changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: webhook_retries was changed away from its inventoried default.

Action: Reset webhook_retries and read it.

Expect: A value of webhook_retries other than 8 over 24 hours fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-ai_timeout Change to ai_timeout requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ai_timeout on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: ai_timeout still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-ai_timeout Reset leaves ai_timeout changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ai_timeout was changed away from its inventoried default.

Action: Reset ai_timeout and read it.

Expect: A value of ai_timeout other than 30 seconds fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-labeling_model Change to labeling_model requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: labeling_model still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-labeling_model Reset leaves labeling_model changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model was changed away from its inventoried default.

Action: Reset labeling_model and read it.

Expect: A value of labeling_model other than gpt-6.1-sol fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-labeling_model_with_openai_key Change to labeling_model_with_openai_key requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_with_openai_key on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: labeling_model_with_openai_key still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-labeling_model_with_openai_key Reset leaves labeling_model_with_openai_key changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_with_openai_key was changed away from its inventoried default.

Action: Reset labeling_model_with_openai_key and read it.

Expect: A value of labeling_model_with_openai_key other than gpt-6.1-sol when an OpenAI key is set fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-categorizing_model_with_openai_key Change to categorizing_model_with_openai_key requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_with_openai_key on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: categorizing_model_with_openai_key still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-categorizing_model_with_openai_key Reset leaves categorizing_model_with_openai_key changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_with_openai_key was changed away from its inventoried default.

Action: Reset categorizing_model_with_openai_key and read it.

Expect: A value of categorizing_model_with_openai_key other than gpt-6.1-sol when an OpenAI key is set fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-labeling_model_without_openai_key Change to labeling_model_without_openai_key requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_without_openai_key on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: labeling_model_without_openai_key still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-labeling_model_without_openai_key Reset leaves labeling_model_without_openai_key changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_model_without_openai_key was changed away from its inventoried default.

Action: Reset labeling_model_without_openai_key and read it.

Expect: A value of labeling_model_without_openai_key other than primary provider's strong model fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-categorizing_model_without_openai_key Change to categorizing_model_without_openai_key requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_without_openai_key on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: categorizing_model_without_openai_key still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-categorizing_model_without_openai_key Reset leaves categorizing_model_without_openai_key changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_model_without_openai_key was changed away from its inventoried default.

Action: Reset categorizing_model_without_openai_key and read it.

Expect: A value of categorizing_model_without_openai_key other than primary provider's strong model fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-labeling_check_model_with_secondary Change to labeling_check_model_with_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_with_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: labeling_check_model_with_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-labeling_check_model_with_secondary Reset leaves labeling_check_model_with_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_with_secondary was changed away from its inventoried default.

Action: Reset labeling_check_model_with_secondary and read it.

Expect: A value of labeling_check_model_with_secondary other than secondary provider's model for labeling fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-categorizing_check_model_with_secondary Change to categorizing_check_model_with_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_with_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: categorizing_check_model_with_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-categorizing_check_model_with_secondary Reset leaves categorizing_check_model_with_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_with_secondary was changed away from its inventoried default.

Action: Reset categorizing_check_model_with_secondary and read it.

Expect: A value of categorizing_check_model_with_secondary other than secondary provider's model for categorizing fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-labeling_check_model_without_secondary Change to labeling_check_model_without_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_without_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: labeling_check_model_without_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-labeling_check_model_without_secondary Reset leaves labeling_check_model_without_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: labeling_check_model_without_secondary was changed away from its inventoried default.

Action: Reset labeling_check_model_without_secondary and read it.

Expect: A value of labeling_check_model_without_secondary other than primary provider's other tier fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-categorizing_check_model_without_secondary Change to categorizing_check_model_without_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_without_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: categorizing_check_model_without_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-categorizing_check_model_without_secondary Reset leaves categorizing_check_model_without_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: categorizing_check_model_without_secondary was changed away from its inventoried default.

Action: Reset categorizing_check_model_without_secondary and read it.

Expect: A value of categorizing_check_model_without_secondary other than primary provider's other tier fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-ocr_check_model_with_secondary Change to ocr_check_model_with_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_with_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: ocr_check_model_with_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-ocr_check_model_with_secondary Reset leaves ocr_check_model_with_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_with_secondary was changed away from its inventoried default.

Action: Reset ocr_check_model_with_secondary and read it.

Expect: A value of ocr_check_model_with_secondary other than secondary provider's model for OCR fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-ocr_check_model_without_secondary Change to ocr_check_model_without_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_without_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: ocr_check_model_without_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-ocr_check_model_without_secondary Reset leaves ocr_check_model_without_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: ocr_check_model_without_secondary was changed away from its inventoried default.

Action: Reset ocr_check_model_without_secondary and read it.

Expect: A value of ocr_check_model_without_secondary other than primary provider's other tier for OCR fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-photo_description_grouping_check_model_with_secondary Change to photo_description_grouping_check_model_with_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_with_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: photo_description_grouping_check_model_with_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-photo_description_grouping_check_model_with_secondary Reset leaves photo_description_grouping_check_model_with_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_with_secondary was changed away from its inventoried default.

Action: Reset photo_description_grouping_check_model_with_secondary and read it.

Expect: A value of photo_description_grouping_check_model_with_secondary other than secondary provider's model for photo description and grouping fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-photo_description_grouping_check_model_without_secondary Change to photo_description_grouping_check_model_without_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_without_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: photo_description_grouping_check_model_without_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-photo_description_grouping_check_model_without_secondary Reset leaves photo_description_grouping_check_model_without_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: photo_description_grouping_check_model_without_secondary was changed away from its inventoried default.

Action: Reset photo_description_grouping_check_model_without_secondary and read it.

Expect: A value of photo_description_grouping_check_model_without_secondary other than primary provider's other tier for photo description and grouping fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-transcription_check_model_with_secondary Change to transcription_check_model_with_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_with_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: transcription_check_model_with_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-transcription_check_model_with_secondary Reset leaves transcription_check_model_with_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_with_secondary was changed away from its inventoried default.

Action: Reset transcription_check_model_with_secondary and read it.

Expect: A value of transcription_check_model_with_secondary other than secondary provider's model for transcription fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-transcription_check_model_without_secondary Change to transcription_check_model_without_secondary requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_without_secondary on the AI and inference tab was changed.

Action: Issue the next request without a restart.

Expect: transcription_check_model_without_secondary still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-transcription_check_model_without_secondary Reset leaves transcription_check_model_without_secondary changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: transcription_check_model_without_secondary was changed away from its inventoried default.

Action: Reset transcription_check_model_without_secondary and read it.

Expect: A value of transcription_check_model_without_secondary other than primary provider's other tier for transcription fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-high_tier_categories Change to high_tier_categories requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: high_tier_categories on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: high_tier_categories still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-high_tier_categories Reset leaves high_tier_categories changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: high_tier_categories was changed away from its inventoried default.

Action: Reset high_tier_categories and read it.

Expect: A value of high_tier_categories other than court_filing, court_notice, discovery, agency_notice, police_criminal, estate_planning, contracts_agreements fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_court_filing Change to doc_category_court_filing requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_filing on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_court_filing still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_court_filing Reset leaves doc_category_court_filing changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_filing was changed away from its inventoried default.

Action: Reset doc_category_court_filing and read it.

Expect: A value of doc_category_court_filing other than court_filing: Pleading, motion, petition, order, judgment, exhibit, or a party's notice of hearing; folder Pleadings; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_court_notice Change to doc_category_court_notice requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_notice on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_court_notice still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_court_notice Reset leaves doc_category_court_notice changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_court_notice was changed away from its inventoried default.

Action: Reset doc_category_court_notice and read it.

Expect: A value of doc_category_court_notice other than court_notice: Clerk or e-filing notice: filing confirmation, Case.net page, hearing notice or link; folder Pleadings; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_discovery Change to doc_category_discovery requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_discovery on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_discovery still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_discovery Reset leaves doc_category_discovery changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_discovery was changed away from its inventoried default.

Action: Reset doc_category_discovery and read it.

Expect: A value of doc_category_discovery other than discovery: Interrogatories, requests for production or admission, responses, depositions; folder Discovery; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_correspondence Change to doc_category_correspondence requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_correspondence on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_correspondence still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_correspondence Reset leaves doc_category_correspondence changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_correspondence was changed away from its inventoried default.

Action: Reset doc_category_correspondence and read it.

Expect: A value of doc_category_correspondence other than correspondence: Letter or email to or from the client, counsel or a third party, including plea offers; folder Correspondence; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_agency_notice Change to doc_category_agency_notice requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_agency_notice on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_agency_notice still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_agency_notice Reset leaves doc_category_agency_notice changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_agency_notice was changed away from its inventoried default.

Action: Reset doc_category_agency_notice and read it.

Expect: A value of doc_category_agency_notice other than agency_notice: Letter from a government agency, such as child support, IRS, DOR, Children's Division or MSPD; folder Correspondence; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_estate_planning Change to doc_category_estate_planning requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_estate_planning on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_estate_planning still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_estate_planning Reset leaves doc_category_estate_planning changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_estate_planning was changed away from its inventoried default.

Action: Reset doc_category_estate_planning and read it.

Expect: A value of doc_category_estate_planning other than estate_planning: The will, trust, power of attorney, directive or beneficiary deed itself; folder Client documents; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_property_title Change to doc_category_property_title requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_property_title on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_property_title still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_property_title Reset leaves doc_category_property_title changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_property_title was changed away from its inventoried default.

Action: Reset doc_category_property_title and read it.

Expect: A value of doc_category_property_title other than property_title: Deed, deed of trust, mortgage release, secured note, survey, title; folder Client documents; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_financial_record Change to doc_category_financial_record requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_financial_record on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_financial_record still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_financial_record Reset leaves doc_category_financial_record changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_financial_record was changed away from its inventoried default.

Action: Reset doc_category_financial_record and read it.

Expect: A value of doc_category_financial_record other than financial_record: Bank or tax record, pay stub, statement, invoice, income and expense or asset and debt form; folder Client documents; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_personal_record Change to doc_category_personal_record requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_personal_record on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_personal_record still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_personal_record Reset leaves doc_category_personal_record changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_personal_record was changed away from its inventoried default.

Action: Reset doc_category_personal_record and read it.

Expect: A value of doc_category_personal_record other than personal_record: ID, birth, death or dissolution certificate, driving record, completion certificate; folder Client documents; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_medical_insurance Change to doc_category_medical_insurance requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_medical_insurance on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_medical_insurance still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_medical_insurance Reset leaves doc_category_medical_insurance changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_medical_insurance was changed away from its inventoried default.

Action: Reset doc_category_medical_insurance and read it.

Expect: A value of doc_category_medical_insurance other than medical_insurance: Medical record or evaluation, health or vision insurance document; folder Client documents; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_contracts_agreements Change to doc_category_contracts_agreements requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_contracts_agreements on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_contracts_agreements still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_contracts_agreements Reset leaves doc_category_contracts_agreements changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_contracts_agreements was changed away from its inventoried default.

Action: Reset doc_category_contracts_agreements and read it.

Expect: A value of doc_category_contracts_agreements other than contracts_agreements: Contract between parties, prenup, articles, bylaws or operating agreement; folder Client documents; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_police_criminal Change to doc_category_police_criminal requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_police_criminal on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_police_criminal still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_police_criminal Reset leaves doc_category_police_criminal changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_police_criminal was changed away from its inventoried default.

Action: Reset doc_category_police_criminal and read it.

Expect: A value of doc_category_police_criminal other than police_criminal: Police report, probable cause statement, citation, bodycam report, restitution; folder Evidence; tier high fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_evidence_media Change to doc_category_evidence_media requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_evidence_media on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_evidence_media still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_evidence_media Reset leaves doc_category_evidence_media changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_evidence_media was changed away from its inventoried default.

Action: Reset doc_category_evidence_media and read it.

Expect: A value of doc_category_evidence_media other than evidence_media: Photo, video, screenshot, text messages or call log, including phone message exports; folder Evidence; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_call_meeting Change to doc_category_call_meeting requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_call_meeting on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_call_meeting still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_call_meeting Reset leaves doc_category_call_meeting changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_call_meeting was changed away from its inventoried default.

Action: Reset doc_category_call_meeting and read it.

Expect: A value of doc_category_call_meeting other than call_meeting: Recording, transcript or notes of a phone call, voicemail or meeting; folder Notes; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_engagement_intake Change to doc_category_engagement_intake requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_engagement_intake on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_engagement_intake still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_engagement_intake Reset leaves doc_category_engagement_intake changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_engagement_intake was changed away from its inventoried default.

Action: Reset doc_category_engagement_intake and read it.

Expect: A value of doc_category_engagement_intake other than engagement_intake: This firm's fee agreement, invoice, intake or consultation notes, MSPD forms; folder Billing; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_firm_work_product Change to doc_category_firm_work_product requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_firm_work_product on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_firm_work_product still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_firm_work_product Reset leaves doc_category_firm_work_product changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_firm_work_product was changed away from its inventoried default.

Action: Reset doc_category_firm_work_product and read it.

Expect: A value of doc_category_firm_work_product other than firm_work_product: This firm's memo, research, hearing or trial prep, case notes; folder Notes; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F2-doc_category_reference Change to doc_category_reference requires a restart

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_reference on the Doc Tender tab was changed.

Action: Issue the next request without a restart.

Expect: doc_category_reference still at its previous value fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F3-doc_category_reference Reset leaves doc_category_reference changed

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: doc_category_reference was changed away from its inventoried default.

Action: Reset doc_category_reference and read it.

Expect: A value of doc_category_reference other than reference: Article, statute, template, logo or blank form; not about a client matter; folder Notes; tier standard fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-029-F4 A fixed rule is exposed as a switch

Phase 5. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Admin settings page.

Action: Look for a switch that turns off append-only trust, forever audit retention, the below-zero flag, or service-layer permission checks.

Expect: Any such switch fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-030

A CI check fails the build when core code reads an environment variable outside the bootstrap package or uses a tunable value that isn't in the settings registry; the release build passes it.

Phases in scope: 1, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: contracts, qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, platform.
Legacy Doc Tender failures: none.
Status: pending.

The product CI fails a core env read outside bootstrap and a tunable that is not in the registry. The release build passes. settings.CheckModule is a source heuristic, not full AC-030 acceptance: it flags an integer literal used as configuration even when the number is not a current registry default, and it does not treat arithmetic, shifts, a leading minus, empty len or cap, loop counters, or return 0, err as configuration. fixedSite exclusions are debt. A settings.Get lookup of a registry key is not this proof. A green harness fixture is not that CI log.

### Positive

#### AC-030-P1 Checker flags the bad fixture and allows bootstrap

Phase 1. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: Temp tree: os.Getenv in internal/billing, os.Getenv in internal/bootstrap, settings.Get of an unknown key, settings.Get of a registered key.

Action: Run the product CI check on that tree.

Expect: The billing env read fails the build.; The bootstrap env read does not.; The unknown tunable fails the build.; The registered tunable does not.

Evidence: Product CI log. The harness scanner in tests/harness/oracles.py is the rule, and its own unit test is not this evidence.

#### AC-030-P2 Release build passes the check

Phase 7. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: The release tag's source tree.

Action: Run the same CI check.

Expect: The check exits 0.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-030-P3 Hardcoded timeout, limit, or threshold fails the check

Phase 1. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: Isolated file internal/practice/limits.go, outside every fixedSite function: const sessionTimeoutSeconds = 45, const uploadLimit = 3, and return failures >= 4. None of those numbers is excused by matching a registry default. The file does not call settings.Get.

Action: Run settings.ScanSource on that file, the check product CI runs via go test ./internal/settings/.

Expect: The check exits 1.; The report names 45, 3, and 4 as tunable numbers.; A registry-key lookup does not satisfy this case.; This scan is not full AC-030 acceptance of the release build.

Evidence: Product CI or ScanSource log for the isolated fixture. The harness scanner is not this evidence.

#### AC-030-P4 Registered setting control passes the check

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Isolated file that reads one registered setting through settings.Registry and contains no configuration integer literal.

Action: Run settings.ScanSource on that file alone.

Expect: The check exits 0.; The file is not reported as a hardcoded tunable.; A passing registry-key lookup on a different file does not cover the hardcoded fixture.

Evidence: ScanSource log for the control file. The harness scanner is not this evidence.

### Failure

#### AC-030-F1 Core env read outside bootstrap

Phase 1. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: A core Go file outside internal/bootstrap that calls os.Getenv.

Action: Run the CI check.

Expect: The build fails.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-030-F2 Release build fails the check

Phase 7. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: A release candidate that reads an env var outside bootstrap.

Action: Run the CI check.

Expect: The CI check exits non-zero. A green release with that read fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-030-F3 Hardcoded tunable is not reported

Phase 1. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: The same isolated limits.go file, with sessionTimeoutSeconds = 45, uploadLimit = 3, and failures >= 4.

Action: Run settings.ScanSource on that file.

Expect: An exit of 0 fails the criterion.; A report that omits 45, 3, or 4 fails the criterion.; Passing because those numbers are absent from the settings registry fails the criterion.

Evidence: ScanSource log for the isolated fixture. The harness scanner is not this evidence.

## AC-031

With the primary AI provider unreachable, a module's AI call falls back to the secondary provider and the call log shows both.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

When the primary provider does not answer, the call uses the secondary. The call log shows both attempts.

### Positive

#### AC-031-P1 Fallback logs both providers

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Primary provider unreachable. Secondary answers. Module AI call.

Action: Run the call and read the AI call log.

Expect: The call succeeds on the secondary.; The log has the primary attempt and the secondary attempt.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-031-F1 Log shows only the secondary

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Primary unreachable.

Action: Read the call log.

Expect: A log with no primary attempt fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-031-F2 No secondary attempt

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Primary unreachable. Secondary configured.

Action: Run the call.

Expect: An error that never calls the secondary fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-031-F3 SQL UPDATE and DELETE of a call-log row

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A stored ai_call_log row. Calls go to the service and the repository, then to SQL as the application role, not the table owner.

Action: Update and delete the row through the service, the repository, and SQL.

Expect: Each update and each delete is refused or changes no row.; The original row is unchanged.; A repository interface that omits update and delete is not this proof.; The database-immutability debt stays pending until this rejection is shown.; No database trigger carries the rule.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-032

Every screen and every admin tab has a guide topic, opened from its ? icon, that says what it does and where it lives; a CI check fails the build when a screen or tab has none; each role's getting-started tutorial opens from /help.

Phases in scope: 5, 6, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, ui, integrate-extensions, modules.
Legacy Doc Tender failures: none.
Status: pending.

Every screen and every admin tab has a ? topic that says what it does and where it lives. The CI check fails a gap. Each of admin, attorney, staff, and accountant has a getting-started tutorial from /help.

### Positive

#### AC-032-P1 Screens and admin tabs open a topic

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Route table and admin tabs for the phase 5 UI.

Action: Open each ? icon.

Expect: Each topic says what the screen does and the menu path where it lives.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-032-P2 Module screen has a topic

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed sample module widget and tab.

Action: Open each ? icon.

Expect: Each opens the module topic.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-032-P3 Role tutorials from /help

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Staff session for each of admin, attorney, staff, and accountant.

Action: Open /help and each getting-started tutorial.

Expect: All four tutorials open.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-032-F1 CI passes a screen with no topic

Phase 5. Kind: integration. Status: pending. Expected exit code: 1.

Arrange: Fixture route with no guide id.

Action: Run the guide CI check.

Expect: The build has to fail.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-032-F2 A role tutorial is missing

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: /help for the four staff roles.

Action: Open each tutorial.

Expect: A missing role fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-032-F3 Topic omits where the screen lives

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: A screen whose topic has no menu path.

Action: Open the ? icon.

Expect: A topic that does not say where the screen lives fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-033

The sample module's guide appears in the help center under its own heading and in search after install, opens from its widget's ? icon, refreshes on update and disappears after uninstall, all without a restart.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The sample guide shows under its own heading, in search, and from the widget ? icon. Update refreshes it. Uninstall removes it. No restart.

### Positive

#### AC-033-P1 Guide appears on install

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Sample module not installed. Record the core start time.

Action: Install it, search help, and open the widget ? icon.

Expect: The help center has the module heading.; Search finds the guide.; The widget ? icon opens it.; The core start time is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-033-P2 Update refreshes the guide

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed sample guide with known body text.

Action: Update the module to a package with new guide text.

Expect: Search returns the new text.; The core does not restart.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-033-P3 Uninstall removes the guide

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed sample guide.

Action: Uninstall.

Expect: The heading and the search hit are gone.; The core does not restart.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-033-F1 Guide still searchable after uninstall

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Just uninstalled.

Action: Search help for the module heading.

Expect: A hit fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-033-F2 Stale guide after update

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Updated package with new text.

Action: Search help.

Expect: The old body fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-034

Every phase report gives the explicit simplification review result and the debt ledger count; the released repo holds PONYTAIL-DEBT.md, and every `ponytail:` marker in it names an upgrade trigger.

Phases in scope: 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Legacy Doc Tender failures: none.
Status: pending.

Every phase report includes the simplification net line count and the debt ledger count. The released repo has PONYTAIL-DEBT.md, and every ponytail marker in it names an upgrade trigger. A marker check on the current file does not pass this criterion by itself.

### Positive

#### AC-034-P1 Seven phase reports and the ledger

Phase 7. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Released tree with docs/build/reports/phase-1.md through phase-7.md and PONYTAIL-DEBT.md.

Action: Read each report and every ponytail marker.

Expect: Each report states the simplification net line count and the debt ledger count.; Every ponytail marker includes an upgrade trigger.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-034-F1 Report missing the ledger count

Phase 7. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A phase report with a line count and no debt ledger count.

Action: Check the seven reports.

Expect: That report fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-034-F2 Marker with no upgrade trigger

Phase 7. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: PONYTAIL-DEBT.md with a ponytail line that has no trigger.

Action: Scan the markers.

Expect: That marker fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-035

Every record in section 5 exists after a fresh install, and the seed data fills each one.

Phases in scope: 1, 7.
Full behavior phase: 1.
Recheck phases: 7.
Responsible domains: contracts, qa, integrate-practice, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: contracts, integrate-practice, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The record list is tests/harness/records.py. Records added after section 5 do not fill this criterion. Every seed row is synthetic.

### Positive

#### AC-035-P1 Every section 5 record exists

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Fresh install, no seed.

Action: List the schema.

Expect: Each name in SECTION5_RECORDS exists as a record.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-035-P2 Seed fills each record

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Fresh install plus the synthetic seed.

Action: Count rows for each section 5 record.

Expect: Each count is at least 1.; Each seeded contact passes the synthetic-data policy.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-035-F1 Missing section 5 record

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Fresh schema.

Action: Diff it against SECTION5_RECORDS.

Expect: Any missing name fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-035-F2 Seed leaves a record empty

Phase 1. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Seed run.

Action: Count rows.

Expect: A zero count fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-035-F3 Seed row fails the synthetic policy

Phase 1. Kind: security. Status: pending. Expected exit code: 0.

Arrange: A seeded contact.

Action: Run contact_errors on it.

Expect: Any policy error fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-036

The sample module's migration creates matter\_targets in its own schema, linked to matters; the matter screen shows its target through include=; its login can't read core contacts; a staff user outside a wall gets none of its rows for a walled matter; a second module reads its published view only after the admin approves the scope.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Legacy Doc Tender failures: none.
Status: pending.

matter_targets lives in the sample module schema and points at matters. include= shows it. The module login cannot read core contacts. A second module reads the published view only after the admin approves the scope. Walls still apply.

### Positive

#### AC-036-P1 Migration creates matter_targets

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Sample module package.

Action: Install and inspect the module schema.

Expect: matter_targets exists in that schema.; Its foreign key points at matters.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-036-P2 Matter screen include= shows the target

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic matter with a target row. User who can see the matter.

Action: Open the matter with include= for the sample target.

Expect: The target value is on the matter screen.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-036-P3 Second module reads the view after approval

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Second module whose install requests the published view scope.

Action: Admin approves the scope, then the module reads the view.

Expect: The read succeeds only after that approval.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-036-F1 Module login reads core contacts

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Sample module database login.

Action: SELECT from core contacts with that login.

Expect: The statement is denied.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-036-F2 Staff outside the wall gets module rows

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Target row on a walled matter. Staff user not named.

Action: Read include= and the module's own user-facing list.

Expect: No target row is returned.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-036-F3 Second module reads the view before approval

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Scope not approved.

Action: Second module reads the published view.

Expect: The read is denied.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-037

A module package whose migration creates its own contacts table, writes outside its schema or adds a trigger is refused at install; a changed migration that already ran is refused on update; a failing migration rolls back and the old version keeps running.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Legacy Doc Tender failures: none.
Status: pending.

A migration that creates a contacts table, writes outside its schema, or adds a trigger is refused. A changed migration that already ran is refused. A failing migration rolls back and the old version keeps running.

### Positive

#### AC-037-P1 Legal migration installs

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Package whose only migration creates matter_targets in its own schema.

Action: Install it.

Expect: Install completes and the table exists.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-037-F1 Migration creates a contacts table

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Package migration with CREATE TABLE contacts.

Action: Install.

Expect: Install is refused.; No module container is on the new version.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-037-F2 Migration writes outside its schema

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Migration with a statement aimed at the core schema.

Action: Install.

Expect: Install is refused.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-037-F3 Migration adds a trigger

Phase 6. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Migration with CREATE TRIGGER.

Action: Install.

Expect: Install is refused.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-037-F4 Changed migration that already ran

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Installed module. The applied migration file's checksum changes.

Action: Update.

Expect: The update is refused.; The old version still runs.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-037-F5 Failing migration rolls back

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Update whose new migration fails.

Action: Run the update.

Expect: The migration transaction rolls back.; The old version still serves.; The core process is still the old module version.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-038

Uninstall with Delete data drops the schema only after a backup and the typed confirmation; a core record that module rows point to can't be hard-deleted.

Phases in scope: 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules, release.
Implementing domains: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Pending dependencies: integrate-practice, integrate-finance, integrate-documents, modules, integrate-extensions, module-documents, module-email, module-client-portal, module-lawpay-docuseal, module-quickbooks, integrate-modules.
Legacy Doc Tender failures: none.
Status: pending.

Delete data drops the schema only after a backup and the typed confirmation. A core row that module rows point at cannot be hard-deleted.

### Positive

#### AC-038-P1 Delete data after backup and typed confirmation

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed sample module with a schema. Admin types the confirmation.

Action: Uninstall with Delete data.

Expect: A backup that includes the schema finishes first.; Then the schema and its login are dropped.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-038-F1 Delete data with no backup

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Installed module. Backup step suppressed.

Action: Uninstall with Delete data.

Expect: The schema is still present.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-038-F2 Wrong typed confirmation

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Backup available. Confirmation text does not match.

Action: Uninstall with Delete data.

Expect: The schema is still present.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-038-F3 Hard delete of a referenced core record

Phase 6. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: matter_targets row pointing at a matter.

Action: Hard-delete that matter.

Expect: The delete is refused.; The error names the module that holds the row.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-039

A 1 GB upload over a connection that drops resumes and its SHA-256 matches; a file on a walled matter can't be listed, previewed, downloaded or searched from outside the wall; every save adds a version and no version changes.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

A 1 GB upload resumes after a dropped connection and the SHA-256 matches. A walled file is hidden from list, preview, download, and search. Every save adds a version and no version's bytes change.

### Positive

#### AC-039-P1 Resumed 1 GB upload matches SHA-256

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic 1 GB payload with a known SHA-256. Connection drops mid-upload.

Action: Resume the upload and read the stored version.

Expect: The stored SHA-256 equals the source.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-039-P2 Save adds a version and the old bytes stay

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: File with version 1 bytes recorded.

Action: Save new bytes.

Expect: Version 2 exists.; Version 1's SHA-256 is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-039-F1 Walled file is visible outside the wall

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: File on a walled matter. Staff user not named.

Action: List, preview, download, and search.

Expect: The file appearing on any of those four fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-039-F2 A version's bytes change

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Stored version hash.

Action: Save the file, then re-read the old version.

Expect: A changed hash fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-039-F3 SQL UPDATE and DELETE of a saved version

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A stored file_versions row and its file_renditions row. Calls go to the service and the repository, then to SQL as the application role, not the table owner.

Action: Update and delete each row through the service, the repository, and SQL.

Expect: Each update and each delete is refused or changes no row.; The original bytes and hash are unchanged.; A new save, if one is posted, is a new row.; A repository interface that omits update and delete is not this proof.; The database-immutability debt stays pending until this rejection is shown.; No database trigger carries the rule.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-040

Moving all files from local disk to S3-compatible storage and then to Dropbox keeps every file readable during the move, checks every SHA-256 and deletes old copies only after the admin confirms; a changed byte on a backend fails the read and emails the admins; a restored backup brings back files from every backend with matching SHA-256s.

Phases in scope: 4, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, files, integrate-finance, integrate-documents, integrate-extensions, platform.
Legacy Doc Tender failures: none.
Status: pending.

Move local disk to S3-compatible storage, then to Dropbox. Files stay readable, every SHA-256 is checked, and old copies go only after the admin confirms. A changed byte fails the read and emails admins. Restore brings back every backend. AC-040-P1 needs disposable synthetic endpoints. In-process doubles are not acceptance, including when the adapter calls into the double.

### Positive

#### AC-040-P1 Move across backends and delete only after confirm

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0. In-process doubles are refused.

Arrange: Synthetic files on local disk. A disposable S3-compatible endpoint and a disposable Dropbox endpoint, started for this run, holding only synthetic objects.

Action: Move all files to the S3-compatible endpoint, read them during the move, then move them to the Dropbox endpoint.

Expect: Every file is readable during both moves.; Every version SHA-256 matches on the new backend.; Old copies remain until the admin confirms, then they are deleted.; The evidence names both live endpoints.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused. An in-process double is refused.

#### AC-040-P2 Restore returns files from every backend

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Backup taken while files sit on more than one backend.

Action: Restore it.

Expect: Each file's SHA-256 matches the backup, for every backend that held a copy.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-040-F1 Changed byte on a backend

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: One stored object with a flipped byte.

Action: Read the file.

Expect: The read fails.; Admins get an email.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-040-F2 Old copies deleted before confirm

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Move checked out, admin has not confirmed.

Action: Inspect the old backend.

Expect: A deleted old copy fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-041

A DOCX opens in the editor, saves as a new version and opens in Word with no repair prompt; a second user sees the edit lock, which frees 5 minutes after the editor closes without saving; New Pleading fills its caption from the matter; the Document editor tab shows commit 770a8fab12392485c0c2789fb254da7d9296df4d.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

LawCraft commit 770a8fab12392485c0c2789fb254da7d9296df4d. A DOCX round-trips through Word with no repair prompt. The lock is visible to the second user and frees 5 minutes after the editor closes without a save. New Pleading fills the caption from the matter.

### Positive

#### AC-041-P1 DOCX saves a version and opens in Word

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic DOCX on a matter.

Action: Open it in the editor, save, and open the new version in Word.

Expect: The save is a new version.; Word opens it with no repair prompt.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-041-P2 Lock shows, then frees at 5 minutes

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: User A holds the edit lock and closes without saving. User B opens the file.

Action: Read the lock at 4 minutes 59 seconds and at 5 minutes.

Expect: User B sees who holds the lock and since when.; The lock is still held at 4 minutes 59 seconds.; The lock is free at 5 minutes.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-041-P3 New Pleading fills the caption

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Synthetic matter with court, county, case number, and parties.

Action: Run New Pleading.

Expect: The caption matches those matter fields.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-041-P4 Document editor tab shows the pinned commit

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin session.

Action: Open the Document editor tab.

Expect: The tab shows 770a8fab12392485c0c2789fb254da7d9296df4d.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-041-F1 Lock still held after 5 minutes

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Editor closed without a save and without a renewal.

Action: Read the lock at 5 minutes.

Expect: A lock that is still held fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-041-F2 Wrong commit on the tab

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Document editor tab.

Action: Read the commit.

Expect: Any other commit fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-041-F3 Word repair prompt

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Version saved by the editor.

Action: Open it in Word.

Expect: A repair prompt fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-042

An editor run fills a document's merge fields and saves a new version; a run that tries to read a local file is refused; a conversion to PDF makes a new file linked to its source.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, editor, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

An editor run fills merge fields into a new version. A command that reads a local file is refused. PDF conversion creates a new file linked to the source.

### Positive

#### AC-042-P1 Merge fields save a new version

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic DOCX with merge fields and a matter that supplies the values.

Action: POST an editor run that fills those fields.

Expect: A new version exists.; The filled values match the matter.; The prior version is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-042-P2 PDF conversion links to the source

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic source file.

Action: POST a conversion to PDF.

Expect: The new file is a PDF.; Its link type points at the source.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-042-F1 Run that reads a local file

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Editor-run command whose parameter reads a local path.

Action: POST the run.

Expect: The run is refused.; No new version is written.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-042-F2 Conversion overwrites the source

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Source hash recorded.

Action: Convert to PDF.

Expect: A changed source hash fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-043

A staff upload, a client upload, a file written by the sample module and a drop-folder file are each processed within 10 minutes of arrival with no nightly run, and each comes out with the pattern name as its only name, a category, a summary, its folder and a saved Markdown copy.

Phases in scope: 4, 6, 7.
Full behavior phase: 6.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, modules.
Legacy Doc Tender failures: old_doc.
Status: pending.

Staff upload, client upload, drop folder, and a sample-module file each finish within 10 minutes with no nightly batch. The pattern name is the only name. The legacy .doc slot is the old binary Word failure. The OLE stand-in in the corpus is not evidence that wordcraft-cli converted it.

### Positive

#### AC-043-P1 Three arrival paths finish within 10 minutes

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Corpus files sources/staff-upload.txt, sources/client-upload.txt, and sources/drop/2026-0001/dropped.txt on an open matter.

Action: Deliver each and wait for its job.

Expect: Each job finishes within 10 minutes of arrival.; None waited for a nightly run.; Each file's only name is the naming pattern, with a category, a summary, a folder, and a saved Markdown copy.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-043-P2 Sample module file is processed the same way

Phase 6. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Corpus file sources/module/matter-clock.txt written through the sample module.

Action: Wait for the job.

Expect: It finishes within 10 minutes with the pattern name, category, summary, folder, and Markdown copy.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-043-P3 Legacy .doc converts and checks counts

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A .doc that wordcraft-cli opens. Do not use the corpus OLE stand-in hash.

Action: Process it and compare wordcraft-cli's word count and page count to the document's own counts.

Expect: The counts match.; The file is not the structural stand-in.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-043-F1 Job waits for a nightly batch

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A file that arrived now.

Action: Inspect the job queue before the nightly sweep.

Expect: A job that is not queued at arrival fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-043-F2 Arrival name is the displayed name

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Processed staff upload.

Action: Read the file name shown in the UI and the API.

Expect: The arrival name in that field fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-043-F3 High-tier category published on disagreement

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Label model says court_filing. Check model says correspondence.

Action: Read the published category.

Expect: court_filing is not published.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-043-F4 Stand-in .doc offered as the conversion

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Corpus legacy/old.doc.

Action: Submit its hash as AC-043 evidence.

Expect: The evidence gate refuses the stand-in hash.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-044

A 120-page PDF with scanned pages processes page by page, and a failure injected at page 49 resumes at page 49.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: scanned_pdf_page_49.
Status: pending.

Corpus file legacy/scanned-120.pdf has 120 image-only pages. The product injects the failure at page 49 and resumes at page 49. Pages 1 through 48 are not redone.

### Positive

#### AC-044-P1 120 scanned pages are stored as they finish

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: legacy/scanned-120.pdf on an open synthetic matter.

Action: Process it with no injected failure.

Expect: The job has 120 page units.; Each unit is stored before the next page starts.; The pages have no text layer.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-044-F1 Resume starts at page 1

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Failure injected after page 48 has passed its check.

Action: Resume the job.

Expect: The next unit is page 49.; A resume at page 1 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-044-F2 Pages 1 through 48 are redone

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Same injected failure.

Action: Compare unit attempt counts after resume.

Expect: A new attempt on pages 1 through 48 fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-045

Eight photos of a three-page letter and a five-page lease, uploaded together, come out as two PDFs and two Markdown copies with the pages in order.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: merged_then_split.
Status: pending.

Eight photos: a three-page letter and a five-page lease. Each photo paints LETTER or LEASE and PAGE n OF m. The pass is two PDFs and two Markdown copies, pages in order. The corpus bad-merge and bad-split files are the old wrong outputs, not a pass.

### Positive

#### AC-045-P1 Eight photos become two documents in order

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: legacy/merged-then-split/page-01.png through page-08.png uploaded together. Pages 1-3 read LETTER PAGE n OF 3. Pages 4-8 read LEASE PAGE n OF 5. Quiet time elapsed.

Action: Run grouping.

Expect: Two PDFs and two Markdown copies.; The letter PDF has pages 1, 2, 3 in that order.; The lease PDF has pages 4 through 8 in that order.; The photos stay linked as sources.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-045-F1 One merged PDF of eight pages

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The eight photos.

Action: Read the outputs.

Expect: A single eight-page PDF fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-045-F2 Split at four and four

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The eight photos.

Action: Read the page counts.

Expect: A 4/4 split fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-045-F3 Pages out of order

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The eight photos.

Action: Read page order inside each PDF.

Expect: A reversed or swapped page fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-046

An audio file and a video come out with Markdown transcripts with timestamps, and the video's report adds what's on screen.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

Audio gets a Markdown transcript with timestamps and no unexplained gap over 30 seconds. Video adds what is on screen, on the same timeline. The corpus tone and color frames are the synthetic inputs.

### Positive

#### AC-046-P1 Audio transcript covers the recording

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Corpus audio/tone.wav, duration 1000 ms, plus a longer synthetic recording used to test the 30 second gap rule.

Action: Process the audio.

Expect: The Markdown transcript has timestamps.; Timestamps cover the recording with no unexplained gap over 30 seconds.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-046-P2 Video report includes the picture

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Corpus video/frames.avi, three solid frames.

Action: Process the video.

Expect: The Markdown report has the transcript timestamps and an on-screen description on one timeline.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-046-F1 Unexplained gap over 30 seconds

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Synthetic audio with a silent gap over 30 seconds and no explanation in the transcript.

Action: Run the coverage check.

Expect: The check fails and the file is not published as correct.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-046-F2 Video report has no on-screen track

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Processed video.

Action: Read the Markdown report.

Expect: A transcript with no on-screen descriptions fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-047

A file on a closed matter makes no model call, and reopening the matter processes it.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, practice.
Legacy Doc Tender failures: none.
Status: pending.

A file on a closed matter makes no model call and is listed as Not processed (matter closed). Reopening the matter processes it.

### Positive

#### AC-047-P1 Closed matter spends nothing

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Closed synthetic matter. New file. AI call log row count recorded.

Action: Deliver the file.

Expect: The file is stored and listed as Not processed (matter closed).; The AI call log count is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-047-P2 Reopen processes the file

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The same file, matter reopened with a reason.

Action: Wait for the job.

Expect: A model call runs and the file leaves the not-processed state.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-047-F1 Model call while the matter is closed

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Closed matter.

Action: Read the AI call log after the file arrives.

Expect: A new model call fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-047-F2 Reopen leaves the file unprocessed

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Matter reopened.

Action: Read the file's job.

Expect: A job that stays Not processed (matter closed) fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-048

A password-protected PDF, a damaged file and a low-confidence label each land in the review queue, email the admin, and stay listed, viewable and downloadable with a flag.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: password_pdf.
Status: pending.

Password-protected, damaged, and low-confidence files go to the review queue, email the admin, and stay listed, viewable, and downloadable with a flag. The corpus password PDF is a structural stand-in. Evidence has to come from a file a PDF library reports as encrypted. Score 890 of 1000 queues. Score 900 does not queue for low confidence.

### Positive

#### AC-048-P1 Three exceptions stay available

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: A PDF the library reports as encrypted, corpus exceptions/damaged.pdf, and a label at 890 thousandths.

Action: Process each.

Expect: Each is in the review queue.; Each emails the admin.; Each is listed, viewable, and downloadable.; Each shows a flag naming the failure.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-048-F1 An exception is hidden or not downloadable

Phase 4. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: The three files above.

Action: List, view, and download each as an attorney.

Expect: A missing file or a blocked download fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-048-F2 Threshold boundary

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: One result at 900 thousandths and one at 899.

Action: Read the review queue.

Expect: 900 is not queued for low confidence.; 899 is queued.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-048-F3 Stand-in password PDF used as evidence

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: Corpus legacy/password.pdf.

Action: Submit its hash as AC-048 evidence.

Expect: The evidence gate refuses the stand-in hash.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-049

A name, category or matter a person set is never changed by a later reprocess; reprocessing adds a Markdown version and keeps the old one.

Phases in scope: 4, 7.
Full behavior phase: 4.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

A person-set name, category, or matter stays put on reprocess. Grouping is locked the same way. Reprocess adds a Markdown version and keeps the old one.

### Positive

#### AC-049-P1 Locked name, category, and matter survive

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: File whose name, category, and matter were set by a person.

Action: Reprocess it with a model output that proposes different values.

Expect: Name, category, and matter are unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-049-P2 Reprocess adds a Markdown version

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Existing Markdown version hash recorded.

Action: Reprocess.

Expect: A new Markdown version exists.; The old version's hash is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-049-F1 Reprocess changes a locked field

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Person-set name.

Action: Reprocess.

Expect: A changed name, category, or matter fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-049-F2 Reprocess overwrites the Markdown version

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Old Markdown hash.

Action: Reprocess and read that version number.

Expect: A changed hash at the old version number fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-049-F3 Reprocess changes a locked grouping

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Person-set photo grouping.

Action: Reprocess.

Expect: A changed grouping fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-050

The Markdown copy has the original's base name with .md, follows the original's walls, is in backups and is what Files search matches.

Phases in scope: 4, 7.
Full behavior phase: 7.
Recheck phases: none.
Responsible domains: qa, integrate-practice, files, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, files, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, files, integrate-finance, doc-tender, integrate-documents, integrate-extensions, platform.
Legacy Doc Tender failures: none.
Status: pending.

The Markdown copy uses the original base name plus .md. It follows the original's walls. Files search matches it. Phase 7 checks that the backup contains it. The arrival name is not the search name.

### Positive

#### AC-050-P1 Markdown name and wall

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Processed file example-2026-0001-motion-to-withdraw-2026-10-15.txt on a walled matter.

Action: Read the Markdown rendition as a user on the wall and as a user outside it.

Expect: The Markdown name is example-2026-0001-motion-to-withdraw-2026-10-15.md.; The user on the wall can read it.; The user outside the wall cannot.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-050-P2 Files search matches the Markdown

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: A phrase that exists only in the Markdown body.

Action: Search Files for that phrase.

Expect: The hit is this file.; The excerpt comes from the Markdown.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-050-P3 Backup contains the Markdown

Phase 7. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Backup after processing.

Action: Inspect the archive.

Expect: The Markdown bytes are in the archive and the SHA-256 matches.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-050-F1 Outside user reads the Markdown

Phase 4. Kind: security. Status: pending. Expected exit code: 0.

Arrange: User outside the wall.

Action: List, download, and search the Markdown.

Expect: Any hit or download fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-050-F2 Search matches only the arrival name

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Arrival name that is not the pattern name. Phrase in the Markdown.

Action: Search Files.

Expect: A hit that uses the arrival name as the file's name fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-051

A naming pattern edited on the Doc Tender tab names the next file, and a pattern with a status token is refused; a category added there is used for the next file.

Phases in scope: 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

The Doc Tender tab edits the pattern and the categories. A status token is refused and the saved pattern stays. A new category is used on the next file. received-date is allowed. received, draft, and file-status are not.

### Positive

#### AC-051-P1 Edited pattern names the next file

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin sets <clientlast>-<matter#>-<what-it-is>-<received-date>.<ext> on the Doc Tender tab.

Action: Process the next synthetic file.

Expect: That file's only name uses the new pattern.; The received-date token is accepted.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-051-P2 Added category is used

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin adds a synthetic category on the Doc Tender tab and maps a folder.

Action: Process a file the model places in that category.

Expect: The file's category is the new one.; It is filed in the mapped folder.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-051-F1 Status token is refused

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Saved pattern recorded. Admin submits a pattern containing <status>.

Action: Save it on the Doc Tender tab.

Expect: The save is refused.; The stored pattern is unchanged.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-051-F2 Status synonyms are refused

Phase 5. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Patterns containing <draft>, <file-status>, and <received>.

Action: Save each.

Expect: Each save is refused.; A pattern containing <received-date> is still allowed.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

## AC-052

With test keys set, the AI call log shows gpt-6.1-sol for labeling, and the AI tab offers no local endpoint for labeling or categorizing.

Phases in scope: 4, 5, 7.
Full behavior phase: 5.
Recheck phases: 7.
Responsible domains: qa, integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions, release.
Implementing domains: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Pending dependencies: integrate-practice, integrate-finance, doc-tender, integrate-documents, integrate-extensions.
Legacy Doc Tender failures: none.
Status: pending.

With test keys set, the labeling call log shows gpt-6.1-sol. The AI tab offers no local endpoint for labeling or categorizing.

### Positive

#### AC-052-P1 Labeling log shows gpt-6.1-sol

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: OpenAI test key set. Synthetic file that reaches labeling.

Action: Read the AI call log for that labeling call.

Expect: The model is gpt-6.1-sol.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

### Failure

#### AC-052-F1 AI tab offers a local endpoint for labeling

Phase 5. Kind: e2e. Status: pending. Expected exit code: 0.

Arrange: Admin on the AI tab.

Action: Open the model choices for labeling and for categorizing.

Expect: A local endpoint in either list fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.

#### AC-052-F2 Labeling log shows another model

Phase 4. Kind: integration. Status: pending. Expected exit code: 0.

Arrange: Test keys set.

Action: Read the labeling call.

Expect: Any model other than gpt-6.1-sol fails the criterion.

Evidence: Command, integer exit code, and the observation named above. A mock, a double, a skip, an oracle pass, or a structural stand-in hash is refused.


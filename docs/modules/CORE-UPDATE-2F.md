# Core update 2f: documents and intake blockers

This update extends CORE-23, CORE-29, CORE-30, CORE-31 and CORE-36 without a
release/version bump. `Accepts().core_items` already includes these five items;
`runtime_criteria` now identifies the individual behaviors below. Existing module
bearers need no signed-in user. All service calls retain approved scopes, live
limits, matter walls and module/source audit attribution.

| Gap | Core behavior | Regression evidence |
| --- | --- | --- |
| Permission reasons | API permissions include signed manifest reasons; Modules shows escaped reasons beside pending, approved and declined decisions, including candidate updates | `TestCoreUpdate2FPermissionReasons` |
| Form proxy | CSRF parsing preserves exact urlencoded bytes and its existing size ceiling; cookie writes honor session CSRF failures | `TestCoreUpdate2FProxyRestoresFormAfterCSRF` |
| Trashed files | `file.trashed` queues for subscribed modules with approved derived read scopes, through normal sender-bound core delivery | `TestCoreUpdate2FFileTrashDelivery` |
| Busy across walls | `calendar-events?view=busy` returns occupancy only; ordinary reads still omit/refuse hidden events | `TestCoreUpdate2FBusyAcrossWalls` |
| Additional named people | Conflict checks accept and retain `names`; service conflict records name the module as author/source service | `TestCoreUpdate2FNamedConflictService`, `TestCoreUpdate2FAdditionalConflictNames` |
| Counties | `getCounty` accepts settings:read service principals | `TestCoreUpdate2FCountyService` |
| Background files | Scoped services mark their own published files; publication accepts status and independently validated optional human authorship | `TestCoreUpdate2FPublicationAuthorAndStatus` |
| Generated documents | `document.generated` is allowed for documents.generate.v1 using the existing targeted envelope and sender-bound queue | `TestCoreUpdate2FDocumentGeneratedContractEnvelope` |
| Doc Tender | coretest runs the real job pipeline with deterministic outside-service adapters | `TestCoreUpdate2FDocTenderCategoryHint` |
| Intake card | processor.payment.v1 binds intent, consumer, amount, destination and provider; decline/pending precede any consumer matter write; uncertain approvals recover without reauthorization | `TestCoreUpdate2FProcessorDeclineBeforeMatterAndApprovalRecovery`, `TestCoreUpdate2FProcessorConcurrentAndInvalidRequests` |

See [module API](../api/modules.md), [public kit](MODULE-KIT.md) and
[contracts](../contracts/README.md) for the wire protocol and processor obligations.
Phone, phone-zoom, phone-google and workflow event envelopes stay unchanged. The
reserved `core` sender and both 2e signature headers remain mandatory. Existing
books-reader `contact.updated` delivery keeps its books visibility rules
(`TestCORE02BooksContactUpdatedPump`). Every non-targeted `file.*` event identifying
a file requires approved `events:read`, a matching subscription, approved
`files:read` and the module service principal's read access to the file's current
placement at enqueue. This includes `file.updated`, `file.downloaded`,
`file.trashed` and `file.purged`. Matter walls, attorney-only restrictions and
contact-only/library placement checks apply. After purge the retained row is
checked with only its purge marker ignored, so visible purge and older pending
events can still deliver; ordinary purged-file reads remain `not_found`.
Targeted file authorization grants retain their existing policy.

**Behavior change for existing subscribers:** a subscriber without approved
`files:read` stops receiving `file.updated` and `file.downloaded`. A subscriber
also stops receiving those events for walled, attorney-only or non-matter files
that its service principal cannot read, even if a delegated viewer can read them.
See the [batch 1 subscription inventory](MODULE-KIT.md#batch-1-file-event-subscriptions)
for the specified subscriptions and permissions; real-package compatibility
acceptance remains pending.

The source base did not contain the referenced contracts directory; this update documents
the existing core envelope there and does not introduce a second shape.

The processor module and module-repository acceptance runs are outside this repo.
Independent exact-base/head review, Docker image/Compose/smoke checks, final
clean-install acceptance and the combined signed release remain integration work.

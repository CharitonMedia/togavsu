# Test strategy

Task `qa`. PRD sections 11 and 12. The product is not accepted. This tree has no product evidence, and `app_complete` is false.

Tests use generated synthetic data only. They run in this VM and in GitHub-hosted CI. They do not use client files, production services, or Patrick's machines.

## What a pass is

`docs/build/acceptance.json` owns the 52 requirement sentences. The harness copies them. A rewritten sentence fails `tests/test_catalog.py`.

Each criterion has at least one positive case and one failure case. The case names the phase where that behavior can be true, the arrange step, the action, the observation, and the evidence a later task must record.

A criterion stays `pending` until every one of its cases has evidence. Evidence is a command, an integer exit code, and an observation. These kinds are refused: mock, skip, scaffold, documentation, todo, oracle, synthetic_fixture. A mocked flag is refused too. Oracle unit tests in this tree are the rules. They are not acceptance.

`app_complete` needs all 52 criteria accepted and a clean Docker Desktop install attestation (`kind: clean_install`, `host: docker_desktop`). Linux Compose CI does not satisfy that attestation. Phase 7 repeats earlier criteria. A criterion whose full behavior lands before phase 7 lists phase 7 as a recheck.

## Synthetic data

People in fixtures use `example.com`, `example.net`, or `example.org`. Phones use the fictional E.164 range `+15555550100` through `+15555550199`. An SSN, when a test needs one, uses the unissued `900-00-0000` form. The corpus person is Jordan Example, `jordan@example.com`, `+15555550100`, matter `2026-0001`.

`tests/harness/policy.py` rejects any other email host, any other phone, a client-export origin, and the snippets `patrick`, `nolan law`, `confidential client`, `client data export`, and `production database`.

## Records and defaults

AC-035 is the section 5 record list in `tests/harness/records.py`. The fresh install has to create each one, and the seed has to put at least one synthetic row in each. Records added after section 5 (`home_layouts`, `notifications`, `help_sections`, `help_items`, `custom_field_definitions`, `custom_field_values`, `fee_items`, `matter_markers`, `notification_preferences`) do not fill AC-035.

AC-029 is the named-default inventory in `tests/harness/defaults.py`. Each anchor is a phrase in `togavsu-core-prd.md`. Fixed rules stay fixed: append-only trust, forever audit retention, the below-zero flag, service-layer permission checks, and encryption that is always on for S3-compatible storage and Dropbox.

AC-017 needs at least 50 users, 20,000 matters, and 1,000,000 time entries. A p95 of 300 ms fails. A shorter seed cannot pass.

## Who runs what

Domains come from `docs/build/tasks.json`. `qa` and `release` are on every criterion. Implementing domains are the other tasks that list the id. Pending dependencies add the tasks whose code the case calls when those tasks do not list the id. AC-017 depends on `contracts` for the seed generator and `platform` for the running stack. No feature task lists AC-017.

This branch does not edit workflows or shared packages. `scripts/check_setup.py` remains a metadata check. It does not run this harness. Platform's product CI should call the unittest command below once that workflow exists. Until it does, the command is run in the agent VM.

## Harness ceilings

Two corpus files are structural stand-ins. Their hashes are refused as acceptance evidence.

- `legacy/old.doc` has the OLE magic, a v3 header, and the `WordDocument` stream name. Word and wordcraft-cli are not expected to open it. Replace it when wordcraft-cli at the pinned LawCraft commit can convert a real `.doc` and check word and page counts.
- `legacy/password.pdf` has a Standard encrypt dictionary in the trailer. The streams are still plaintext. Replace it when the product PDF library reports the file as encrypted.

The 120-page scanned PDF and the eight page photos are real fixture bytes. The manifest records the ceiling and the upgrade trigger on the two stand-ins. `PONYTAIL-DEBT.md` is outside this task's paths and was not edited. Debt ledger count on that file is unchanged: no product markers.

## Security checks the cases already require

Matter walls apply on list, search, widget, report, API, and module rows. An unnamed admin loses the matter too. Accountant writes are denied on the service, the UI, module tokens, and MCP. Attorney-only history values are redacted for staff. Walled files and their Markdown copies stay hidden. Module logins cannot read core contacts. Unsigned and tampered modules are refused before a container starts.

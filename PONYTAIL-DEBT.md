# Simplification debt

Product acceptance is pending. Each entry names the file, the deliberate ceiling, the upgrade path, and the concrete upgrade trigger.

The first two entries are harness stand-ins. They are not product-ready behavior. `product_ready` stays false and their file hashes are refused as acceptance evidence.

## tests/harness/corpus.py legacy/old.doc

ponytail: ceiling: OLE magic, a v3 header, and the WordDocument stream name. Word and wordcraft-cli are not expected to open it. Upgrade path: replace these bytes with a document wordcraft-cli opens at the pinned LawCraft commit, then set product_ready true. trigger: The doc-tender or editor package can shell out to wordcraft-cli and this file is rejected as an unreadable .doc rather than converted and checked.

## tests/harness/corpus.py legacy/password.pdf

ponytail: ceiling: The trailer points at a Standard encrypt dictionary. The streams are still plaintext, so a reader that ignores /Encrypt will open the page. Upgrade path: write a real Standard-security encrypted PDF once the product PDF library reports encryption, then set product_ready true. trigger: The doc-tender package opens this file as an ordinary one-page PDF instead of an encrypted file.

## Platform ceilings

1. `internal/supervisor/config.go` — firm settings come from environment values. Ceiling: no settings table. The core AC-030 walk skips `cmd/supervisor` and `internal/supervisor` because this is a separate process; a direct `ScanSource` call still reports a literal there. Upgrade: a SettingsReader backed by the core registry. Trigger: the settings registry is present in the database this process can read.
2. `internal/supervisor/ledger.go` — each schedule tick rereads the ledger. Ceiling: linear scan. Upgrade: keep a rebuilt tail offset beside the ledger. Trigger: a tick's scan exceeds 50ms or the file passes 50k events.
3. `internal/supervisor/mail.go` — admin notices append to an outbox file. Ceiling: no message leaves the host. Upgrade: core system mail drains the file. Trigger: SMTP settings or the email module can send.
4. `internal/supervisor/registry.go` — updates read one JSON release index. Ceiling: no OCI registry protocol. Upgrade: OCI tags plus cosign referrers. Trigger: phase 7 publishes a signed core image.
5. `internal/supervisor/destinations.go` — destination records live in a supervisor JSON file. Ceiling: not the core settings table. Upgrade: core backup-destination records push here. Trigger: `/api/v1/backup-destinations` is served by core.
6. `internal/supervisor/s3.go` — S3 copies use path-style SigV4 and buffer a put when the checksum is not already known. Ceiling: no virtual-hosted buckets and archive-sized buffers. Upgrade: virtual-hosted addressing and aws-chunked streaming. Trigger: a destination test returns PermanentRedirect or a backup copy exceeds 512 MiB.
7. `internal/supervisor/sftp.go` — SFTP shells out to OpenSSH. Ceiling: requires the `sftp` binary, a key, and `known_hosts`. Upgrade: an in-process client. Trigger: contracts permit `golang.org/x/crypto` or CI needs an in-process server.
8. `internal/supervisor/update.go` — automatic migration rollback returns `ErrPreserveWrites` and does not undo schema changes. Ceiling: later commits are kept and a failed migration's schema changes stay. Upgrade: a quiesced rollback that reverses only the migration. Trigger: a tested rollback preserves rows committed after the pre-update snapshot.
9. `Dockerfile` — the supervisor image runs as root for the Docker socket. Ceiling: one root container. Upgrade: a socket proxy limited to the engine calls this process makes. Trigger: phase 7 release hardening.
10. `compose.yaml` — the Caddy container runs as root so its entrypoint can read the upstream file and write certificates. Ceiling: root on the edge container. Upgrade: a non-root entrypoint image. Trigger: phase 7 release hardening.
11. `internal/supervisor/journal.go` — restore recovery rereads the journal. Ceiling: linear scan. Upgrade: keep the latest record per id beside the log. Trigger: a restart scan exceeds 50ms or the file passes 50k records.
12. `deploy/core/entrypoint.sh` — a 64-character hex field key is still exported as standard base64 of the same bytes. Ceiling: two on-disk encodings. Upgrade: reject hex. Trigger: bootstrap no longer finds a 64-character hex field_key.
13. `internal/supervisor/postgres.go` — `ApplyProductRestore` refuses an archive that lists file versions. Ceiling: database-only restore. Upgrade: a storage-backend adapter that writes each verified file and is covered by a restore test that reads the bytes back. Trigger: the files task registers that adapter with the supervisor.
14. `internal/supervisor/destinations.go`: `putFile` writes its receipt only after the temporary inode exists. Ceiling: a crash in that gap leaves at most one inode and two directory entries (the temp and, when linkat worked, its .own anchor) of 0 bytes or exactly the token prefix, and the later no-xattr clean file can add one more 0-byte name before its own journal, all with no receipt. Upgrade: journal an inode that is not yet linked. Trigger: the filesystem interface returns an inode before the directory entry exists.

## Contracts ceilings

1. `internal/db/seed.go` — synthetic seed inserts one Missouri county. Ceiling: the counties table has a row. Upgrade path: first-run loads every county of the firm's state. Trigger: setup stores the firm state.
2. `internal/domain/defaults.go` — marketplace address default is the sentinel `togavsu-marketplace`, not a hostname. Ceiling: the setting exists and can be changed. Upgrade path: replace the sentinel with the marketplace origin. Trigger: the marketplace task publishes the service URL.
3. `internal/settings/check.go` — `fixedSite` skips integer literals inside enumerated functions (cents, UUID layout, synthetic seed builders, version bands, migration indexes, the ledger baseline, bootstrap key length and process status, sealing key width, and setting upsert). Ceiling: a new configurable literal inside one of those functions is not reported. A different function in the same file is reported. The check is not AC-030 acceptance. Upgrade path: delete that function from `fixedSite`. Trigger: a configurable literal is added inside a listed function.
4. `internal/db/migrations/0001_core_schema.sql` — append-only tables are not immutable at the database. Interfaces omit update and delete, and there is no trigger because PRD section 6 forbids business-logic triggers. Ceiling: `TestAppendOnlyNotEnforcedAtDatabase` shows the application role can UPDATE and DELETE those rows. Upgrade path: the owning task rejects hostile UPDATE and DELETE, including raw SQL from a non-owner role, and replaces that gap test. Owners: identity `audit_log` (AC-024), practice `events` (AC-024), trust `trust_ledger` (AC-006), files `file_versions` and `file_renditions` (AC-039), modules `ai_call_log` (AC-031). Trigger: that task's acceptance claims the ledger cannot be edited or deleted.

Debt count: 20.

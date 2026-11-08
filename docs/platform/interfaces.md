# Interfaces the rest of the build implements

The supervisor listens on `http://supervisor:8081` inside the Compose network. It is not published on the host. Callers send `Authorization: Bearer <token>`. The token is the installer-generated file `/appsecrets/supervisor_token`. `/health` is open and returns `{"status":"ok"}`.

Core owns the public admin API (`/api/v1/backups`, `/api/v1/backup-destinations`, `/api/v1/restores`) and the audit log. Core checks the admin role, writes the audit event with actor and source, then calls the matching supervisor route. The supervisor records its own append-only ledger. That ledger is not the product audit log.

## Update

`POST /v1/updates` with `{"force":false}`.

Order when a newer release exists:

1. Refuse outside 02:00–04:00 firm time unless `force` is true. Default zone is `FIRM_TIMEZONE` (UTC until the settings registry is the source).
2. Refuse when `UPDATES_PAUSED` is set, unless `force` is true.
3. Read `TOGAVSU_RELEASE_INDEX`, a JSON document `{"image":"ghcr.io/charitonmedia/togavsu-core@sha256:<64 hex>","version":"1.2.3"}`. A tag without a digest is refused.
4. Verify that digest with `cosign verify --key $COSIGN_KEY`, pull that same digest, and migrate and start only that digest. A missing key, a failed verify, or a failed pull leaves the running core in place.
5. Take a `pre-update` backup.
6. Run the new image as a one-shot container with command `migrate`. The image must exit 0 and print `migrated=true` when it applied a migration. A failed migration or health check stops the new container and leaves the running core in place. The pre-update backup is kept and is not replayed while that core is still accepting writes. An operator restore uses the quiesced restore path.
7. Start `togavsu-core-next`, wait until its Docker health check is `healthy`, and refuse a missing health check.
8. Point Caddy at `<container>:8080` and record the dial address in `/runtime/upstream`.
9. Stop the previous core. If that stop fails, the new core stays in service and the result includes `stop_error`.

The core image entrypoint is `deploy/core/entrypoint.sh`. Compose and every replacement or migration container the supervisor creates run as uid 65532, mount the installer secret volume at `/appsecrets`, and use that entrypoint. It reads `database_url`, `field_key`, `supervisor_token`, and `domain` and exports `TOGAVSU_DATABASE_URL`, `TOGAVSU_ENCRYPTION_KEY` (standard base64 of the same 32 bytes), `TOGAVSU_DOMAIN`, and `TOGAVSU_SUPERVISOR_SECRET`. A 64-character hex field key is accepted and not rewritten. The supervisor passes `TOGAVSU_DOMAIN` only when its own environment has the same value. It does not put the database URL, field key, or supervisor secret in the container environment.

The core image must serve the product on port 8080 and define a Docker health check once the HTTP server exists. The current image only accepts the four bootstrap values and exits. Suggested check, when HTTP exists: `GET /health`.

## Modules

`POST /v1/modules/{name}/start` and `/stop`. `{name}` matches `[a-z0-9][a-z0-9-]{0,62}`. The supervisor starts or stops the container labeled `togavsu.module=<name>`. It does not create module containers; the modules assignment owns that.

## Backups and restores

| Method | Path | Behavior |
| --- | --- | --- |
| POST | `/v1/backups` | Body `{"reason":"manual"}`. Reasons: `nightly`, `pre-update`, `manual`, `safety`. |
| GET | `/v1/backups` | Local archive metadata. Record counts stay inside the encrypted archive. |
| GET | `/v1/backups/{id}` | One metadata record. |
| GET | `/v1/backups/{id}/archive` | Encrypted bytes after the sidecar checksum matches. |
| DELETE | `/v1/backups/{id}` | Removes the local archive and configured destination copies. |
| POST | `/v1/backup-destinations` | Adds a `path`, `s3`, or `sftp` destination. Secrets are not returned. |
| POST | `/v1/backup-destinations/{id}/test` | Writes and reads a probe object. |
| DELETE | `/v1/backup-destinations/{id}` | Forgets the destination. |
| POST | `/v1/restores` | Body `{"backup_id":"..."}`. Admission takes the operation gate before the passphrase is read or the archive is validated, and before an accepted id is published. A busy or shutting-down request does not publish an id. An invalid archive, and an archive that lists file versions, is rejected before writers stop. File bytes, module volumes, and remote storage objects are not restored yet. An accepted restore returns 202 with a unique operation id and continues on its own timeout after the caller disconnects. Intent is fsynced before writers are stopped and before the database rename. A second restore is rejected while that operation holds the gate. |
| GET | `/v1/restores/{id}` | One restore record: `id`, `backup_id`, `status` (`accepted`, `running`, `completed`, or `failed`), and, when set, `error`, `recovery`, and `cleanup`. Memory, the journal, and the ledger return that same shape. A committed swap whose previous database is still present is `completed` with `cleanup` `pending`. It is not `failed` and it is not rolled back. A commit whose journal write failed is not reported as unchanged data. A confirmed rollback is recorded before the loaded database is removed. |
| POST | `/v1/test-restores` | Body `{"backup_id":"..."}`. Restores into a throwaway database and compares counts. |

A missing passphrase returns `409 passphrase_required`. Copy failures keep the local archive and append an admin notice to `/var/lib/togavsu/mail-outbox.jsonl`. Core system mail drains that file.

Destination kinds:

- `path`: absolute directory, including a mounted SMB or NFS path. Copies go in a generated `namespace` directory under that path. Checksum is verified after the copy. The archive and the `.sha256` sidecar share one staged lifecycle: temp, no-xattr publication copy, anchor, quarantine rename, final object, and receipt. Each identity is journaled before its payload bytes. Shares that reject user xattrs or hardlinks stay supported, and the token prefix is not part of the published bytes. [Path upload lifecycle](path-upload-lifecycle.md) is the state table.
- `s3`: path-style AWS Signature Version 4. Fields: `endpoint`, `region`, `bucket`, `prefix`, `access_key`, `secret_key`, `retention_days`. The installation `namespace` is appended to `prefix`. `secret_key` is sealed with the installer field key before `destinations.json` is written (`destseal:v1:`). The plaintext credential and the key are not stored. A record that still has a plaintext secret is rewritten on the next open when that key is available. Compose sets `FIELD_KEY_FILE` to `/appsecrets/field_key`.
- `sftp`: OpenSSH `sftp` in batch mode with `StrictHostKeyChecking=yes`. The destination argument is a single `user@host` operand after `--`. Fields: `host`, `port`, `user`, `identity_file`, `known_hosts`, `remote_path`, `retention_days`. The installation `namespace` is a directory under `remote_path`. `identity_file` is a path to a key file. The file contents are not copied into the destination record.

Retention deletes only names in this installation's receipt file. Archives already stored in the shared directory, bucket prefix, or remote directory stay there.

## Settings the registry must take over

Until the settings table exists, these environment values carry the PRD defaults:

| Env | Default | PRD setting |
| --- | --- | --- |
| `FIRM_TIMEZONE` | `UTC` | Firm profile time zone |
| `UPDATE_WINDOW_START` / `UPDATE_WINDOW_END` | `02:00` / `04:00` | Update window |
| `UPDATES_PAUSED` | unset | Pause updates |
| `BACKUP_TIME` | `01:00` | Nightly backup time |
| `BACKUP_RETENTION_DAYS` | `30` | Local backup retention |
| `TEST_RESTORE_DAY` | `1` | Monthly test-restore day |
| `LOG_RETENTION_DAYS` | `400` | Application and access log retention |

Database URL, admin URL, field key, domain, and the supervisor token stay in Docker secrets. They are not settings. The supervisor reads the field key from `FIELD_KEY` or `FIELD_KEY_FILE` and uses it only to seal destination credentials. A 64-character hex field key and standard base64 of the same 32 bytes are both accepted. The secret file is not rewritten.

## Core migrate contract

The core image command `migrate` reads `DATABASE_URL`, applies embedded goose migrations, and prints `migrated=true` or `migrated=false`. A changed checksum on an applied migration exits non-zero. The supervisor keeps the old core. Automatic replay of the pre-update backup is pending: the shipped rollback returns `ErrPreserveWrites` so a live core is not pointed at a restored older snapshot. An operator can restore that backup through the quiesced restore path.

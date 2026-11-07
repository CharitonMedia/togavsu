# Interfaces the rest of the build implements

The supervisor listens on `http://supervisor:8081` inside the Compose network. It is not published on the host. Callers send `Authorization: Bearer <token>`. The token is the installer-generated file `/appsecrets/supervisor_token`. `/health` is open and returns `{"status":"ok"}`.

Core owns the public admin API (`/api/v1/backups`, `/api/v1/backup-destinations`, `/api/v1/restores`) and the audit log. Core checks the admin role, writes the audit event with actor and source, then calls the matching supervisor route. The supervisor records its own append-only ledger. That ledger is not the product audit log.

## Update

`POST /v1/updates` with `{"force":false}`.

Order when a newer release exists:

1. Refuse outside 02:00–04:00 firm time unless `force` is true. Default zone is `FIRM_TIMEZONE` (UTC until the settings registry is the source).
2. Refuse when `UPDATES_PAUSED` is set, unless `force` is true.
3. Read `TOGAVSU_RELEASE_INDEX`, a JSON document `{"image":"...","version":"1.2.3"}`.
4. Verify with `cosign verify --key $COSIGN_KEY`. A missing key or a failed verify refuses the update and leaves the running core in place.
5. Take a `pre-update` backup.
6. Run the new image as a one-shot container with command `migrate`. The image must exit 0 and print `migrated=true` when it applied a migration. A checksum mismatch or a failed migration aborts the swap. If a migration ran, the supervisor restores the pre-update backup.
7. Start `togavsu-core-next`, wait until its Docker health check is `healthy`, and refuse a missing health check.
8. Point Caddy at `<container>:8080` and record the dial address in `/runtime/upstream`.
9. Stop the previous core. If that stop fails, the new core stays in service and the result includes `stop_error`.

The core image must serve the product on port 8080 and define a Docker health check. Suggested check: `GET /health`.

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
| POST | `/v1/restores` | Body `{"backup_id":"..."}`. Stops core containers, applies the dump, starts them again. |
| GET | `/v1/restores/{id}` | Last ledger event for that restore. |
| POST | `/v1/test-restores` | Body `{"backup_id":"..."}`. Restores into a throwaway database and compares counts. |

A missing passphrase returns `409 passphrase_required`. Copy failures keep the local archive and append an admin notice to `/var/lib/togavsu/mail-outbox.jsonl`. Core system mail drains that file.

Destination kinds:

- `path`: absolute directory, including a mounted SMB or NFS path. Checksum is verified after the copy.
- `s3`: path-style AWS Signature Version 4. Fields: `endpoint`, `region`, `bucket`, `prefix`, `access_key`, `secret_key`, `retention_days`.
- `sftp`: OpenSSH `sftp` in batch mode with `StrictHostKeyChecking=yes`. Fields: `host`, `port`, `user`, `identity_file`, `known_hosts`, `remote_path`, `retention_days`.

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

Database URL, admin URL, field key, domain, and the supervisor token stay in Docker secrets. They are not settings.

## Core migrate contract

The core image command `migrate` reads `DATABASE_URL`, applies embedded goose migrations, and prints `migrated=true` or `migrated=false`. A changed checksum on an applied migration exits non-zero. The supervisor then keeps the old core and restores the pre-update backup when a migration had already run.

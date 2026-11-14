# Platform

This assignment builds the Compose project, Caddy entry point, Postgres 17, the supervisor, and product CI. It uses synthetic data only. It does not deploy to a firm and it does not claim a signed release.

`docker compose up` needs no edited config files. Bootstrap (`deploy/bootstrap/bootstrap-secrets.sh`) generates the database passwords, the supervisor token, and the field key into three volumes:

- `/appsecrets`: `database_url` (the `togavsu_app` login), `field_key`, `supervisor_token`, `domain`. Every core container, Compose's or a supervisor replacement, mounts it read-only.
- `/adminsecrets`: `admin_database_url`, the Postgres superuser URL. Only the `migrate` one-shot, the supervisor, and the update migrator container the supervisor starts mount it (read-only). A serving core never mounts it, and the core entrypoint refuses to serve if it can read a superuser URL.
- `/backupkey`: `backup_passphrase`, created empty. It stays unset until first-run writes `/backupkey/backup_passphrase`; it is the one secret the core writes, and the supervisor reads it read-only.

The core service is declared in `compose.yaml`. `deploy/core.Dockerfile` builds `cmd/core`, which serves HTTP on port 8080 after the `migrate` one-shot has applied the migrations and sealed the append-only ledgers; a fresh install opens on the firm-name, admin, and two-factor screen.

Module path: `github.com/CharitonMedia/togavsu`. The supervisor imports `github.com/CharitonMedia/togavsu/internal/supervisor`. The module file and the core schema come from the contracts assignment.

Read [interfaces](interfaces.md), [backup format](backup-format.md), [CI](ci.md), and [pending criteria](pending.md).

## Simplification review

Reviewed the diff from main `1b00ff0ce02d42bc03692509d9a8c24c085d91f9` to this branch. The change stays on the Go standard library and the existing supervisor, destination, and HTTP client paths. No PRD behavior was removed. HTTP 408 and 5xx stay inside `ambiguousRemote`, so `definiteRejection`, `recordMutation`, and the S3 client share one rule. The parent-directory retry, the post-rename uncertain receipt, and the delete directory sync are the durability behavior; cutting them would let a receipt be confirmed or dropped too early. Plaintext backup workspaces stay off the backup volume and are removed on recovery. Destination secrets are sealed with the existing installer field key. A raw local errno before publication is a completed failure. A rename is not called a rollback until recovery commits that operation row cancelled, and a late command rejects a cancelled or missing operation. Restore SQL is streamed instead of copied into the system temp directory. No further deletion was safe.

`git diff --shortstat 018bc3f2213b5e4b665db4f9e781304a9c8a9d45...HEAD` (current main): 85 files, 31912 added, 1 removed, net 31911.

This pass against `1ec2902f167bcd9e7bf279b0aca804518e6d1f19`: 6 files, 634 added, 57 removed, net 577.

Path-upload repair against `b0a086ea417abbbf71b500f81997eb5f543f2c07`: 16 files, 1844 added, 281 removed, net 1563. The pre-journal ceiling is a `ponytail` comment in `putFile` and in the path-upload lifecycle, recorded as platform ceiling 14 in `PONYTAIL-DEBT.md`.

Backup publication cleanup against `163d21cbbea8ac39c6b5f7e8fdd90502690fd162`: 2 files, 429 added, 5 removed, net 424. An interrupted publication now removes its partial archive, checksum and metadata, and a restart reconciles any intent a killed process left behind.

Review fixes against `82dcc8d29f4d843287dd99a734aaeaf4e41ef791`: 22 files, 1919 added, 81 removed, net 1838. Upload and destination restore reuse the publication intent, the restore decrypt checks, and each destination's existing `Get`; the decrypt step was split out of `openPlain` so the import validates before it publishes and admission does not decrypt the archive again; the apply step decrypts it once more, as a local restore does. The S3 and SFTP transfer timeout on that fetch is platform ceiling 15.

`PONYTAIL-DEBT.md` debt count: 21.

Mounted SMB and NFS shares are supported. User xattrs are used when the filesystem accepts them. A share that returns ENOTSUP or EOPNOTSUPP stores the same ownership token in a fixed prefix of the temporary file and publishes a copy of the payload without that prefix. A private hardlink is created when the filesystem allows it. Recovery never deletes by a name prefix or by an inode number alone. The staged objects, their receipt states, and the crash windows are in [path upload lifecycle](path-upload-lifecycle.md).

Pre-journal residual: the receipt cannot be written before the inode exists. A kill in the source gap leaves at most one inode and two directory entries (the `.tmp` name and, when hardlinks work, its `.own` anchor). The content is 0 bytes when the token is an xattr, or exactly 41 bytes of the `TGVTMP1` prefix. A kill after the no-xattr clean file is created and before that file's own journal leaves one additional 0-byte name. Neither gap contains an untracked archive payload. Recovery does not sweep these names. The limit is cleared only if an inode can be journaled before it is linked, which this filesystem interface does not allow.

This review does not accept AC-001, AC-016, or AC-018.

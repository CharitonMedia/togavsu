# Platform

This assignment builds the Compose project, Caddy entry point, Postgres 17, the supervisor, and product CI. It uses synthetic data only. It does not deploy to a firm and it does not claim a signed release.

`docker compose up` of the caddy, postgres, and supervisor services needs no edited config files. Bootstrap generates the database password, the supervisor token, and the field key into volumes. The backup passphrase stays unset until first-run writes `/appsecrets/backup_passphrase`.

The core service is declared in `compose.yaml`. `deploy/core.Dockerfile` builds `cmd/core`. That binary accepts the contracts bootstrap environment and does not start HTTP or migrations, so a fresh install does not yet reach the firm-name, admin, and two-factor screen.

Module path: `github.com/CharitonMedia/togavsu`. The supervisor imports `github.com/CharitonMedia/togavsu/internal/supervisor`. The module file and the core schema come from the contracts assignment.

Read [interfaces](interfaces.md), [backup format](backup-format.md), [CI](ci.md), and [pending criteria](pending.md).

## Simplification review

Reviewed the diff from main `1b00ff0ce02d42bc03692509d9a8c24c085d91f9` to this branch. The change stays on the Go standard library and the existing supervisor, destination, and HTTP client paths. No PRD behavior was removed. HTTP 408 and 5xx stay inside `ambiguousRemote`, so `definiteRejection`, `recordMutation`, and the S3 client share one rule. The parent-directory retry, the post-rename uncertain receipt, and the delete directory sync are the durability behavior; cutting them would let a receipt be confirmed or dropped too early. Plaintext backup workspaces stay off the backup volume and are removed on recovery. Destination secrets are sealed with the existing installer field key. A raw local errno before publication is a completed failure. A rename is not called a rollback until recovery commits that operation row cancelled, and a late command rejects a cancelled or missing operation. Restore SQL is streamed instead of copied into the system temp directory. No further deletion was safe.

`git diff --numstat 1b00ff0ce02d42bc03692509d9a8c24c085d91f9`: 74 files, 28084 added, 1 removed, net 28083.

This pass against `1ec2902f167bcd9e7bf279b0aca804518e6d1f19`: 6 files, 634 added, 57 removed, net 577.

`PONYTAIL-DEBT.md` debt count: 19.

Mounted SMB and NFS shares are supported. User xattrs are used when the filesystem accepts them. A share that returns ENOTSUP or EOPNOTSUPP stores the same ownership token in a fixed prefix of the temporary file and publishes a copy of the payload without that prefix. A private hardlink is created when the filesystem allows it. Recovery never deletes by a name prefix or by an inode number alone.

Cleanup limit: a kill in the interval after the empty temporary file is created and before its token is journaled can leave that one empty file. It contains no archive bytes. Recovery does not sweep for it, because a sweep would be able to delete a file this installation does not own. The limit is cleared if the token can be journaled before the file exists, which this filesystem interface does not allow. This is not complete cleanup of every empty file.

This review does not accept AC-001, AC-016, or AC-018.

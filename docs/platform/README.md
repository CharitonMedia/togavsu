# Platform

This assignment builds the Compose project, Caddy entry point, Postgres 17, the supervisor, and product CI. It uses synthetic data only. It does not deploy to a firm and it does not claim a signed release.

`docker compose up` of the caddy, postgres, and supervisor services needs no edited config files. Bootstrap generates the database password, the supervisor token, and the field key into volumes. The backup passphrase stays unset until first-run writes `/appsecrets/backup_passphrase`.

The core service is declared in `compose.yaml`. `deploy/core.Dockerfile` builds `cmd/core`. That binary accepts the contracts bootstrap environment and does not start HTTP or migrations, so a fresh install does not yet reach the firm-name, admin, and two-factor screen.

Module path: `github.com/CharitonMedia/togavsu`. The supervisor imports `github.com/CharitonMedia/togavsu/internal/supervisor`. The module file and the core schema come from the contracts assignment.

Read [interfaces](interfaces.md), [backup format](backup-format.md), [CI](ci.md), and [pending criteria](pending.md).

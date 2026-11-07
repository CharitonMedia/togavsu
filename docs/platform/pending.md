# Pending product behavior

These criteria are in this assignment's scope and are not accepted yet.

| ID | What this change actually does | Still pending |
| --- | --- | --- |
| AC-001 | Compose starts Caddy, Postgres 17, and the supervisor with generated installer secrets and no edited config files. | The core image, first-run firm name, first admin, and required two-factor. `deploy/core.Dockerfile` fails closed on purpose. |
| AC-016 | The supervisor refuses an update outside the window, a missing signature, a failed migration, or a failed health check, and it restores a backup when a migration ran. | Signed images, Cosign in the release pipeline, and a load test that shows no failed requests during the swap. |
| AC-018 | Encrypted backups, checksums, path and S3 and SFTP copies, and a Postgres restore that matches synthetic row counts. Admin notices go to the mail outbox. | The product audit log of every firm write with actor and source, download through the admin UI, and restore of a real firm schema. |

No signed release is published from this branch.

# Pending product behavior

These criteria are in this assignment's scope and are not accepted yet.

| ID | What this change actually does | Still pending |
| --- | --- | --- |
| AC-001 | Compose starts Caddy, Postgres 17, and the supervisor with generated installer secrets and no edited config files. | The core image, first-run firm name, first admin, and required two-factor. `deploy/core.Dockerfile` fails closed on purpose. |
| AC-016 | The supervisor refuses an update outside the window, a missing signature, a failed migration, or a failed health check. It leaves the running core in place and does not replay the pre-update backup, so commits made after that snapshot stay. | Automatic migration rollback that can undo a failed migration without discarding those commits. Signed images, Cosign, and a load test that shows no failed requests during the swap. |
| AC-018 | Encrypted backups, checksums, path and S3 and SFTP copies, and a Postgres restore that matches synthetic row counts. Admin notices go to the mail outbox. | The product audit log of every firm write with actor and source, download through the admin UI, and restore of a real firm schema. |

No signed release is published from this branch.

# Simplification debt

Each entry is a deliberate shortcut. The ceiling is the current limit, the upgrade path is the replacement, and the trigger is the condition that makes the replacement due.

1. `internal/supervisor/config.go` — firm settings come from environment values. Ceiling: no settings table. Upgrade: a SettingsReader backed by the core registry. Trigger: the settings registry is present in the database this process can read.
2. `internal/supervisor/ledger.go` — each schedule tick rereads the ledger. Ceiling: linear scan. Upgrade: keep a rebuilt tail offset beside the ledger. Trigger: a tick's scan exceeds 50ms or the file passes 50k events.
3. `internal/supervisor/mail.go` — admin notices append to an outbox file. Ceiling: no message leaves the host. Upgrade: core system mail drains the file. Trigger: SMTP settings or the email module can send.
4. `internal/supervisor/registry.go` — updates read one JSON release index. Ceiling: no OCI registry protocol. Upgrade: OCI tags plus cosign referrers. Trigger: phase 7 publishes a signed core image.
5. `internal/supervisor/destinations.go` — destination records live in a supervisor JSON file. Ceiling: not the core settings table. Upgrade: core backup-destination records push here. Trigger: `/api/v1/backup-destinations` is served by core.
6. `internal/supervisor/s3.go` — S3 copies use path-style SigV4 and buffer a put when the checksum is not already known. Ceiling: no virtual-hosted buckets and archive-sized buffers. Upgrade: virtual-hosted addressing and aws-chunked streaming. Trigger: a destination test returns PermanentRedirect or a backup copy exceeds 512 MiB.
7. `internal/supervisor/sftp.go` — SFTP shells out to OpenSSH. Ceiling: requires the `sftp` binary, a key, and `known_hosts`. Upgrade: an in-process client. Trigger: contracts permit `golang.org/x/crypto` or CI needs an in-process server.
8. `internal/supervisor/update.go` — automatic migration rollback returns `ErrPreserveWrites` and does not undo schema changes. Ceiling: later commits are kept and a failed migration's schema changes stay. Upgrade: a quiesced rollback that reverses only the migration. Trigger: a tested rollback preserves rows committed after the pre-update snapshot.
9. `Dockerfile` — the supervisor image runs as root for the Docker socket. Ceiling: one root container. Upgrade: a socket proxy limited to the engine calls this process makes. Trigger: phase 7 release hardening.
10. `compose.yaml` — the Caddy container runs as root so its entrypoint can read the upstream file and write certificates. Ceiling: root on the edge container. Upgrade: a non-root entrypoint image. Trigger: phase 7 release hardening.

Debt count: 10.

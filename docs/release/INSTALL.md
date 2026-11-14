# Installing and running Togavsu

This is the firm's install and operations guide for the Togavsu core (PRD sections 2, 6 and 12). The in-app user guide at /help covers every screen and admin tab; this page covers what happens outside the app: installing with Docker Desktop, first-run setup, updates, backups, restore and removal.

Status: the tagged, signed release is not published yet. Until it is, the install below builds the images from this repository's source on your computer. See [README.md](README.md) for what is still pending before the release.

## What you need

- A computer that stays on overnight (updates run in the update window, default 2:00 to 4:00 a.m.; backups run at 1:00 a.m. by default).
- Docker Desktop on Windows or macOS, or Docker Engine with the Compose plugin on Linux. Give Docker at least 4 CPUs, 8 GB of memory and 40 GB of disk; Doc Tender's readers and the editor build use most of that.
- Ports 80 and 443 free on the computer. Togavsu answers on one domain through its own reverse proxy (caddy).
- A domain name for the firm's install, or `localhost` for a single computer. Caddy issues a local certificate; browsers ask you to trust it once.

## Install

1. Get the release. For the signed release: download `compose.yaml` and the `deploy/` folder from the release page for your version. Before the release is published: `git clone https://github.com/CharitonMedia/togavsu` and `cd togavsu`.
2. Open a terminal in that folder.
3. Start the stack, naming the domain staff will use (leave it out for `localhost`):
   - macOS or Linux: `TOGAVSU_DOMAIN=togavsu.firm.example docker compose up -d --wait`
   - Windows PowerShell: `$env:TOGAVSU_DOMAIN="togavsu.firm.example"; docker compose up -d --wait`
4. The first start generates the database passwords, the supervisor's token and the field encryption key into Docker volumes. You edit no file and set no environment value other than the domain. A one-time `migrate` step migrates the database and locks the trust ledger and audit log so that the running app can only add to them; the core then seeds its reference rows on its own. Only that step and the supervisor can read the database administrator's password (the `adminsecrets` volume); the running core never can, and it refuses to start on a database whose ledgers are not locked.
5. Open `https://<your domain>/` (or `https://localhost/`). The browser shows first-run setup.

Nothing else is needed. If `docker compose up` stops with an error, run `docker compose ps -a` and `docker compose logs core supervisor` and see Troubleshooting below.

## First-run setup

First-run setup asks for:

- the firm name and time zone,
- the first admin's name, email and password (at least 12 characters),
- the backup passphrase. Every backup archive is encrypted with it (AES-256), and restore asks for it. Write it down and keep it somewhere other than this computer: without it no backup can be restored.

Then it shows a two-factor code. Scan it with an authenticator app and enter the six-digit code; two-factor is always required for the admin. Save the recovery codes it shows once.

After setup, follow Help & Training > Getting started > Admin, which walks through every admin tab. The two settings to do first are Administrative > Notifications and mail (SMTP, so trust flags, reminders, invoices and password resets can be emailed) and Administrative > AI and inference (a provider key, so Doc Tender starts processing files; until then it stores and lists every file and waits).

## Updates

Togavsu updates itself with no restart and no interruption:

- Each night in the update window the supervisor checks the registry for a newer core image, verifies its Cosign signature against the Togavsu release public key that ships in the supervisor image (`/usr/share/togavsu/cosign.pub`, the same file as `deploy/release/cosign.pub` in the repository), backs up the database, runs the migrations, starts the new core, waits for its health check, switches caddy to it and stops the old one. You install no key.
- Release signatures are made off GitHub by the Togavsu integration lead with a private key that is never stored on GitHub, and they are not written to the public Sigstore transparency log. That is why compose.yaml sets `COSIGN_VERIFY_FLAGS` to `--insecure-ignore-tlog=true`: it skips only the log lookup. The signature itself is always checked against the release key, and an unsigned image, or one signed by any other key, is refused.
- To check a release yourself: `cosign verify --key deploy/release/cosign.pub --insecure-ignore-tlog=true ghcr.io/charitonmedia/togavsu-core:<version>` (and the same for `togavsu-supervisor`).
- A failed signature check, migration or health check stops the update and keeps the running version. The supervisor does not replay the pre-update backup on its own, so commits made after that snapshot are kept; if a migration failed, restore that backup by hand (Restore, below). Automatic rollback of a failed migration is pending (docs/platform/pending.md, AC-016).
- The failure notice for the admins is written to the supervisor's mail outbox (`mail-outbox.jsonl` in the supervisor data volume). Sending it through the firm's mail is pending (`PONYTAIL-DEBT.md`).
- Administrative > Updates sets the window and pauses updates. Starting an update now and the update history are in the API today (`POST` and `GET /api/v1/updates`, admin role or an `updates:admin` token); the dashboard buttons for them are pending. Updates never run outside the window unless an admin starts one.

The supervisor container itself, caddy and Postgres are updated by pulling the new release's `compose.yaml` and running `docker compose up -d --wait` again. That restarts those containers briefly; the core's data stays in its volumes.

## Backups

- Automatic: a full backup every night (the database with every module schema, and the file bytes from every storage backend with backups on) and one before every update, kept 30 days on the local backup volume by default. Module data volumes are not in the archive yet (pending, docs/platform/pending.md).
- Administrative > Backups and logs sets the nightly backup time, the local retention, the log retention and the monthly test-restore day.
- Making a backup now, downloading one as one encrypted file, and adding destinations (a local or network path, S3-compatible storage, or SFTP) that receive a copy of every automatic backup with their own retention are in the API today: `/api/v1/backups` and `/api/v1/backup-destinations`, admin role or a `backups:admin` token, also on `/mcp`. The dashboard screens for them are pending.
- Every backup is checksummed when written and when copied. Once a month the supervisor test-restores the newest backup into a temporary database and compares record counts. A failure notice goes to the supervisor's mail outbox, as for updates.
- Keep at least one destination off this computer. A backup only on the computer's own disk does not survive losing the computer.

## Restore

Restore is the one operation that interrupts users. Tell them before you start: the dashboard warning, upload and destination picker for restore are pending (docs/platform/pending.md, AC-018), so restore runs through the API today, as an admin or with a `backups:admin` token.

1. Pick the backup: `GET /api/v1/backups` lists the ones this installation holds. For a downloaded file, send its bytes to `POST /api/v1/backup-uploads?backup_id=<the file name without .tgbk>`; the answer carries an `upload_token`.
2. `POST /api/v1/restores` with either `backup_id` or `upload_token`, and `passphrase`, the backup passphrase.
3. The supervisor checks the file, takes a safety backup of the current state, restores the database and the file bytes, and brings the core back with no manual step. Until the restored files are back in place, Togavsu shows a page saying it is finishing a restore and takes no work, so nothing done in that time can be lost. `GET /api/v1/restores/{restore_id}` shows its progress.

Module data volumes are not restored yet (pending).

To move Togavsu to a new computer: install it there (Install, steps 1 to 4), complete first-run setup with a throwaway admin, then restore the latest backup from the old computer. The restored firm's users, settings and files replace the throwaway setup.

## Removing Togavsu

`docker compose down` stops the stack and keeps every volume, so `docker compose up -d --wait` brings the same firm back. `docker compose down -v` deletes the database, the files, the backups on the local backup volume and the generated keys (the `appsecrets`, `adminsecrets` and `backupkey` volumes) for good; download a backup first, and keep the backup passphrase.

## Troubleshooting

- The browser warns about the certificate: caddy issues a local certificate for the domain. Trust it once, or put the install behind the firm's own certificate.
- `docker compose up` says port 80 or 443 is in use: stop the other web server on the computer, or run Togavsu on a computer of its own.
- The core keeps restarting: `docker compose logs core`. The core never prints passwords, keys or tokens. "refusing to serve: the append-only tables are not sealed" means the `migrate` step has not run or failed: `docker compose logs migrate`, then `docker compose up -d --wait` again.
- Report a problem from inside Togavsu with the sidebar's Report a problem link; the report goes to the firm's admins.

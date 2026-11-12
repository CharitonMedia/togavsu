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
4. The first start generates the database passwords, the supervisor's token and the field encryption key into Docker volumes. You edit no file and set no environment value other than the domain. The core migrates the database and seeds its reference rows on its own.
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

- Each night in the update window the supervisor checks the registry for a newer core image, verifies its Cosign signature against the Togavsu release key that ships in the image, backs up the database, runs the migrations, starts the new core, waits for its health check, switches caddy to it and stops the old one.
- A failed signature check, migration or health check stops the update, keeps the running version, restores the backup if a migration ran, and emails the admins.
- Administrative > Updates sets the window, pauses updates, starts an update now and lists the update history. Updates never run outside the window unless an admin starts one.

The supervisor container itself, caddy and Postgres are updated by pulling the new release's `compose.yaml` and running `docker compose up -d --wait` again. That restarts those containers briefly; the core's data stays in its volumes.

## Backups

- Automatic: a full backup every night (the database with every module schema, file bytes from every storage backend and module data volumes) and one before every update, kept 30 days on the local backup volume by default.
- Administrative > Backups and logs makes a backup now, downloads one to your computer as one encrypted file, adds destinations (a local or network path, S3-compatible storage, or SFTP) that receive a copy of every automatic backup with their own retention, and sets the monthly test-restore day.
- Every backup is checksummed when written and when copied. Once a month the supervisor test-restores the newest backup into a temporary database and compares record counts. Failures email the admins.
- Keep at least one destination off this computer. A backup only on the computer's own disk does not survive losing the computer.

## Restore

Restore is the one operation that interrupts users; the dashboard warns before it starts.

1. Administrative > Backups and logs > Restore. Upload a backup file, or pick one from the list or a destination.
2. Enter the backup passphrase.
3. The supervisor checks the file, takes a safety backup of the current state, restores the database and the file bytes, and brings the core back with no manual step.

To move Togavsu to a new computer: install it there (Install, steps 1 to 4), complete first-run setup with a throwaway admin, then restore the latest backup from the old computer. The restored firm's users, settings and files replace the throwaway setup.

## Removing Togavsu

`docker compose down` stops the stack and keeps every volume, so `docker compose up -d --wait` brings the same firm back. `docker compose down -v` deletes the database, the files, the backups on the local backup volume and the generated keys for good; download a backup first.

## Troubleshooting

- The browser warns about the certificate: caddy issues a local certificate for the domain. Trust it once, or put the install behind the firm's own certificate.
- `docker compose up` says port 80 or 443 is in use: stop the other web server on the computer, or run Togavsu on a computer of its own.
- The core keeps restarting: `docker compose logs core`. The core never prints passwords, keys or tokens.
- Report a problem from inside Togavsu with the sidebar's Report a problem link; the report goes to the firm's admins.

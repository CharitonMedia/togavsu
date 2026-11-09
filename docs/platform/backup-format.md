# Backup format TOGAVBK1

An archive is a tar file encrypted with AES-256-GCM. The passphrase is stretched with PBKDF2-HMAC-SHA256, 200000 iterations, a random 16-byte salt, and a random 4-byte nonce prefix. Each chunk is at most 1 MiB of plaintext. The chunk nonce is the prefix plus a big-endian counter. A wrong passphrase or a flipped ciphertext byte fails decryption.

On disk, under the backup volume:

- `{id}.tgbk` encrypted archive
- `{id}.sha256` hex digest of the encrypted bytes, checked before decrypt and after a destination copy
- `{id}.json` metadata: id, time, reason, checksum, size, destination errors

Plaintext dumps and extracted archives are written in a scratch directory outside that volume. Startup recovery and retention remove abandoned `staging-*` and `restore-*` directories on the volume and in that scratch directory. A workspace this process still has open is kept. Symlinks with those names are not followed. A missing destination directory before any upload is a completed failure. A timeout or a lost server response stays an uncertain receipt until the object is seen.

The id is `YYYYMMDDTHHMMSSZ-` plus 16 hex characters.

Each destination gets its own installation namespace (`namespace` on the destination record). New archives and their `.sha256` sidecars are written only under that directory or key prefix. On a path destination the two objects share the staged lifecycle in [path upload lifecycle](path-upload-lifecycle.md). A local receipt records the archive name after a successful copy. Retention deletes a receipt's archive and its sidecar together, and only after both deletes succeed. Files already in the configured root, including another firm's `.tgbk` names and checksums, are not adopted and are not deleted. A local delete keeps the metadata until every configured destination has removed that archive and its sidecar. Local retention still removes only the local volume and does not use that remote delete.

The tar contains:

- `manifest.json` with `format` `togavsu-backup-v1`, record counts, the dump checksum, and logical file names
- `db/dump.sql` plain `pg_dump` output
- `files/{sha256}` each immutable file version once

An archive uploaded for a restore or fetched from a destination is written as `{id}.tgbk` under the same publication intent. Its sidecar and `.json` record (with `source`) are published only after the received bytes match the supplied `.sha256` sidecar and the archive decrypts and passes the manifest checks below; otherwise the intent removes it. An existing id is never overwritten.

Restore rejects paths that escape the staging directory, rejects non-regular files, and refuses the dump when its checksum does not match the manifest. A manifest that lists file versions is rejected before writers stop and before the database is replaced. The supervisor does not yet restore those bytes, module data volumes, or objects on a remote storage backend. The monthly test-restore loads the dump into `togavsu_check_<hex>` on the admin database and compares those counts with the manifest. Only a mismatch is queued for admin mail.

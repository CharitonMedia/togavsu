#!/bin/sh
# Installer bootstrap. Generates the database password, supervisor token, and
# field key once. The backup passphrase file is created empty so UID 65532 can
# fill it at first-run; an empty file is not a passphrase.
#
# Three volumes, by who may read them (PRD section 6):
#   /appsecrets    database_url (togavsu_app), field_key, supervisor_token, domain;
#                  every core mounts it read-only.
#   /adminsecrets  admin_database_url, the Postgres superuser. Only the migrate one-shot
#                  and the supervisor mount it; a serving core never does.
#   /backupkey     backup_passphrase, the one secret the core writes.
set -eu
umask 077
mkdir -p /pgsecrets /appsecrets /adminsecrets /backupkey

# Matches deploy/core.Dockerfile USER and the Postgres image's postgres user.
core_uid=65532
core_gid=65532
postgres_gid=999

rand() {
  od -An -tx1 -N "$1" /dev/urandom | tr -d ' \n'
}

if [ ! -s /pgsecrets/postgres_password ]; then
  rand 24 > /pgsecrets/postgres_password
fi

# The core logs in as togavsu_app, a role without superuser, so audit_log privileges
# hold against it. deploy/postgres/core-role.sh creates that role with this password.
if [ ! -s /pgsecrets/app_password ]; then
  rand 24 > /pgsecrets/app_password
fi

if [ ! -s /appsecrets/supervisor_token ]; then
  rand 24 > /appsecrets/supervisor_token
fi
# New installs store standard base64 of 32 raw bytes. An existing file,
# including the earlier 64-character hex encoding, is left unchanged.
if [ ! -s /appsecrets/field_key ]; then
  head -c 32 /dev/urandom | base64 -w 0 > /appsecrets/field_key
  printf '\n' >> /appsecrets/field_key
fi

domain=${TOGAVSU_DOMAIN:-localhost}
if [ ! -s /appsecrets/domain ]; then
  printf '%s\n' "$domain" > /appsecrets/domain
else
  existing=$(tr -d ' \n\r' < /appsecrets/domain)
  if [ "$existing" != "$domain" ]; then
    echo "domain file does not match TOGAVSU_DOMAIN" >&2
    exit 1
  fi
fi

pass=$(tr -d '\n' < /pgsecrets/postgres_password)
app_pass=$(tr -d '\n' < /pgsecrets/app_password)
printf 'postgres://togavsu_app:%s@postgres:5432/togavsu?sslmode=disable\n' "$app_pass" > /appsecrets/database_url
printf 'postgres://togavsu:%s@postgres:5432/postgres?sslmode=disable\n' "$pass" > /adminsecrets/admin_database_url
# An earlier layout kept the superuser URL beside the core's secrets; remove it.
rm -f /appsecrets/admin_database_url

# An earlier layout kept the backup passphrase in /appsecrets; carry a set one over.
if [ ! -s /backupkey/backup_passphrase ] && [ -s /appsecrets/backup_passphrase ]; then
  cp /appsecrets/backup_passphrase /backupkey/backup_passphrase
fi
rm -f /appsecrets/backup_passphrase
if [ ! -f /backupkey/backup_passphrase ]; then
  : > /backupkey/backup_passphrase
fi

chown "root:${core_gid}" /appsecrets
chmod 750 /appsecrets
chown "root:${core_gid}" \
  /appsecrets/supervisor_token \
  /appsecrets/field_key \
  /appsecrets/domain \
  /appsecrets/database_url
chmod 640 \
  /appsecrets/supervisor_token \
  /appsecrets/field_key \
  /appsecrets/domain \
  /appsecrets/database_url
# The migrate one-shot runs as UID 65532 and reads this; the core never mounts it.
chown "root:${core_gid}" /adminsecrets /adminsecrets/admin_database_url
chmod 750 /adminsecrets
chmod 640 /adminsecrets/admin_database_url
chown "root:${core_gid}" /backupkey
chmod 750 /backupkey
chown "${core_uid}:${core_gid}" /backupkey/backup_passphrase
chmod 600 /backupkey/backup_passphrase

chown "root:${postgres_gid}" /pgsecrets /pgsecrets/postgres_password /pgsecrets/app_password
chmod 750 /pgsecrets
chmod 640 /pgsecrets/postgres_password /pgsecrets/app_password

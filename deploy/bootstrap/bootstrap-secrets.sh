#!/bin/sh
# Installer bootstrap. Generates the database password, supervisor token, and
# field key once. The backup passphrase file is created empty so UID 65532 can
# fill it at first-run; an empty file is not a passphrase.
set -eu
umask 077
mkdir -p /pgsecrets /appsecrets

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
printf 'postgres://togavsu:%s@postgres:5432/postgres?sslmode=disable\n' "$pass" > /appsecrets/admin_database_url

if [ ! -f /appsecrets/backup_passphrase ]; then
  : > /appsecrets/backup_passphrase
fi

chown "root:${core_gid}" /appsecrets
chmod 750 /appsecrets
chown "root:${core_gid}" \
  /appsecrets/supervisor_token \
  /appsecrets/field_key \
  /appsecrets/domain \
  /appsecrets/database_url \
  /appsecrets/admin_database_url
chmod 640 \
  /appsecrets/supervisor_token \
  /appsecrets/field_key \
  /appsecrets/domain \
  /appsecrets/database_url \
  /appsecrets/admin_database_url
chown "${core_uid}:${core_gid}" /appsecrets/backup_passphrase
chmod 600 /appsecrets/backup_passphrase

chown "root:${postgres_gid}" /pgsecrets /pgsecrets/postgres_password /pgsecrets/app_password
chmod 750 /pgsecrets
chmod 640 /pgsecrets/postgres_password /pgsecrets/app_password

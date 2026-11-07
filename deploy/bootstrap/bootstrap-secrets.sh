#!/bin/sh
# Installer bootstrap. Generates the database password, supervisor token, and
# field key once. It does not invent the backup passphrase: the admin sets
# that during first-run, which is still pending the core service.
set -eu
umask 077
mkdir -p /pgsecrets /appsecrets

rand() {
  od -An -tx1 -N "$1" /dev/urandom | tr -d ' \n'
}

if [ ! -s /pgsecrets/postgres_password ]; then
  rand 24 > /pgsecrets/postgres_password
fi
chmod 644 /pgsecrets/postgres_password

if [ ! -s /appsecrets/supervisor_token ]; then
  rand 24 > /appsecrets/supervisor_token
fi
if [ ! -s /appsecrets/field_key ]; then
  rand 32 > /appsecrets/field_key
fi

pass=$(tr -d '\n' < /pgsecrets/postgres_password)
printf 'postgres://togavsu:%s@postgres:5432/togavsu?sslmode=disable\n' "$pass" > /appsecrets/database_url
printf 'postgres://togavsu:%s@postgres:5432/postgres?sslmode=disable\n' "$pass" > /appsecrets/admin_database_url
chmod 600 /appsecrets/supervisor_token /appsecrets/field_key /appsecrets/database_url /appsecrets/admin_database_url

# Leave backup_passphrase absent until first-run writes it.
if [ -f /appsecrets/backup_passphrase ] && [ ! -s /appsecrets/backup_passphrase ]; then
  rm -f /appsecrets/backup_passphrase
fi

#!/bin/sh
# Bootstrap secrets are generated once and the backup passphrase stays unset.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/pgsecrets" "$work/appsecrets"
# Run the script with the volume paths redirected by a copy that uses the work dir.
sed "s#/pgsecrets#$work/pgsecrets#g; s#/appsecrets#$work/appsecrets#g" \
  "$root/deploy/bootstrap/bootstrap-secrets.sh" > "$work/bootstrap.sh"
sh "$work/bootstrap.sh"
sh "$work/bootstrap.sh"
test -s "$work/pgsecrets/postgres_password"
test -s "$work/appsecrets/supervisor_token"
test -s "$work/appsecrets/field_key"
test -s "$work/appsecrets/database_url"
test ! -e "$work/appsecrets/backup_passphrase"
pass1=$(cat "$work/pgsecrets/postgres_password")
# second run already happened; password must be unchanged
sh "$work/bootstrap.sh"
test "$pass1" = "$(cat "$work/pgsecrets/postgres_password")"
grep -q "postgres://togavsu:${pass1}@postgres:5432/togavsu" "$work/appsecrets/database_url"
echo "bootstrap secrets check passed"

#!/bin/sh
# Bootstrap secrets are generated once. The core UID can read them and write
# only the passphrase file. The Postgres UID can read its password.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'sudo rm -rf "$work"' EXIT
mkdir -p "$work/pgsecrets" "$work/appsecrets"
sed "s#/pgsecrets#$work/pgsecrets#g; s#/appsecrets#$work/appsecrets#g" \
  "$root/deploy/bootstrap/bootstrap-secrets.sh" > "$work/bootstrap.sh"
sudo sh "$work/bootstrap.sh"
sudo sh "$work/bootstrap.sh"
# The temp parent must be traversable. In Compose the secret volumes are mounted
# at /appsecrets and /pgsecrets, whose parent is the container root.
chmod 755 "$work"
sudo test -s "$work/pgsecrets/postgres_password"
sudo test -s "$work/appsecrets/supervisor_token"
sudo test -s "$work/appsecrets/field_key"
sudo test -s "$work/appsecrets/database_url"
sudo test -f "$work/appsecrets/backup_passphrase"
sudo test ! -s "$work/appsecrets/backup_passphrase"
pass1=$(sudo cat "$work/pgsecrets/postgres_password")
sudo sh "$work/bootstrap.sh"
test "$pass1" = "$(sudo cat "$work/pgsecrets/postgres_password")"
sudo grep -q "postgres://togavsu:${pass1}@postgres:5432/togavsu" "$work/appsecrets/database_url"

sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
open(sys.argv[1]).read()
open(sys.argv[2], "w").write("set-at-first-run\n")
try:
    open(sys.argv[3], "a").write("x")
except PermissionError:
    sys.exit(0)
sys.exit("core user overwrote the supervisor token")
' "$work/appsecrets/database_url" "$work/appsecrets/backup_passphrase" "$work/appsecrets/supervisor_token"

sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(999)
os.setuid(999)
open(sys.argv[1]).read()
' "$work/pgsecrets/postgres_password"

phrase=$(sudo cat "$work/appsecrets/backup_passphrase")
test "$phrase" = "set-at-first-run"
echo "bootstrap secrets check passed"

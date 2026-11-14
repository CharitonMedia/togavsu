#!/bin/sh
# Bootstrap secrets are generated once. The core UID can read them and write
# only the passphrase file. The Postgres UID can read its password. The superuser
# URL lives only in the admin volume, which a serving core never mounts, and the
# core entrypoint refuses to serve when it can see that URL (PRD 6, AC-006, AC-024).
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
hexwork=
cleanup() {
  sudo rm -rf "$work"
  if [ -n "${hexwork:-}" ]; then
    sudo rm -rf "$hexwork"
  fi
}
trap cleanup EXIT
mkdir -p "$work/pgsecrets" "$work/appsecrets" "$work/adminsecrets" "$work/backupkey"
# An earlier layout: the superuser URL and a set passphrase beside the core's secrets.
echo "postgres://legacy" > "$work/appsecrets/admin_database_url"
echo "legacy-phrase" > "$work/appsecrets/backup_passphrase"
sed "s#/pgsecrets#$work/pgsecrets#g; s#/appsecrets#$work/appsecrets#g; s#/adminsecrets#$work/adminsecrets#g; s#/backupkey#$work/backupkey#g" \
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
# The legacy files moved out of the core's volume; the set passphrase was carried over.
sudo test ! -e "$work/appsecrets/admin_database_url"
sudo test ! -e "$work/appsecrets/backup_passphrase"
test "$(sudo cat "$work/backupkey/backup_passphrase")" = "legacy-phrase"
# A fresh install starts with an empty passphrase file.
sudo sh -c ': > "$1"' sh "$work/backupkey/backup_passphrase"
sudo sh "$work/bootstrap.sh"
sudo test -f "$work/backupkey/backup_passphrase"
sudo test ! -s "$work/backupkey/backup_passphrase"
pass1=$(sudo cat "$work/pgsecrets/postgres_password")
app1=$(sudo cat "$work/pgsecrets/app_password")
sudo sh "$work/bootstrap.sh"
test "$pass1" = "$(sudo cat "$work/pgsecrets/postgres_password")"
test "$app1" = "$(sudo cat "$work/pgsecrets/app_password")"
test "$app1" != "$pass1"
# The core logs in as the non-superuser togavsu_app; the superuser URL is only in the
# admin volume (migrate and the supervisor mount it, a serving core does not).
sudo grep -q "postgres://togavsu_app:${app1}@postgres:5432/togavsu" "$work/appsecrets/database_url"
sudo grep -q "postgres://togavsu:${pass1}@postgres:5432/postgres" "$work/adminsecrets/admin_database_url"
if sudo grep -rq "$pass1" "$work/appsecrets" "$work/backupkey"; then
  echo "a volume the serving core mounts carries the superuser password" >&2
  exit 1
fi

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
' "$work/appsecrets/database_url" "$work/backupkey/backup_passphrase" "$work/appsecrets/supervisor_token"

sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(999)
os.setuid(999)
open(sys.argv[1]).read()
open(sys.argv[2]).read()
' "$work/pgsecrets/postgres_password" "$work/pgsecrets/app_password"

phrase=$(sudo cat "$work/backupkey/backup_passphrase")
test "$phrase" = "set-at-first-run"

sudo python3 -c '
import base64, sys
raw = open(sys.argv[1]).read().strip()
key = base64.b64decode(raw, validate=True)
if len(key) != 32:
    sys.exit("canonical key length %d" % len(key))
' "$work/appsecrets/field_key"
test "$(sudo cat "$work/appsecrets/domain")" = "localhost"

hexwork=$(mktemp -d)
chmod 755 "$hexwork"
mkdir -p "$hexwork/pgsecrets" "$hexwork/appsecrets" "$hexwork/adminsecrets" "$hexwork/backupkey"
python3 -c 'import sys; open(sys.argv[1], "w").write("11"*32 + "\n")' "$hexwork/appsecrets/field_key"
before=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$hexwork/appsecrets/field_key")
sed "s#/pgsecrets#$hexwork/pgsecrets#g; s#/appsecrets#$hexwork/appsecrets#g; s#/adminsecrets#$hexwork/adminsecrets#g; s#/backupkey#$hexwork/backupkey#g" \
  "$root/deploy/bootstrap/bootstrap-secrets.sh" > "$hexwork/bootstrap.sh"
sudo sh "$hexwork/bootstrap.sh"
after=$(sudo python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$hexwork/appsecrets/field_key")
test "$before" = "$after"
cat > "$hexwork/core-stub" <<'EOF'
#!/bin/sh
python3 - <<'PY'
import base64, os
key = base64.b64decode(os.environ["TOGAVSU_ENCRYPTION_KEY"], validate=True)
open(os.environ["RESULT"], "w").write("%d %s %d %s\n" % (
    len(key), os.environ["TOGAVSU_DOMAIN"], len(os.environ["TOGAVSU_SUPERVISOR_SECRET"]), key.hex()))
PY
EOF
chmod 755 "$hexwork/core-stub"
# Copy the adapter into this temp tree. GitHub checkout parents under
# /home/runner/work are not traversable by uid 65532.
cp "$root/deploy/core/entrypoint.sh" "$hexwork/entrypoint.sh"
cmp -s "$root/deploy/core/entrypoint.sh" "$hexwork/entrypoint.sh"
chmod 755 "$hexwork/entrypoint.sh"
mkdir -p "$hexwork/out"
chmod 777 "$hexwork/out"
result="$hexwork/out/result"
sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
os.environ["TOGAVSU_SECRETS_DIR"] = sys.argv[1]
os.environ["TOGAVSU_ADMIN_SECRETS_DIR"] = sys.argv[1] + "/no-admin-volume"
os.environ["CORE_BIN"] = sys.argv[2]
os.environ["RESULT"] = sys.argv[3]
os.environ["TOGAVSU_DOMAIN"] = "localhost"
os.execv("/bin/sh", ["sh", sys.argv[4]])
' "$hexwork/appsecrets" "$hexwork/core-stub" "$result" "$hexwork/entrypoint.sh"
sudo python3 -c '
import sys
length, domain, secret, keyhex = open(sys.argv[1]).read().split()
if length != "32" or domain != "localhost" or int(secret) < 16 or keyhex != "11"*32:
    sys.exit("adapter result %s %s %s %s" % (length, domain, secret, keyhex))
' "$result"

lowkey=$(python3 -c 'print("abcdef"*10 + "abcd")')
# bootstrap leaves this file root-owned and group-readable. Rewrite the bytes
# without changing that ownership, so uid 65532 can still open it.
sudo python3 -c '
import os, sys
path, text = sys.argv[1], sys.argv[2]
st = os.stat(path)
open(path, "w").write(text + "\n")
os.chown(path, st.st_uid, st.st_gid)
os.chmod(path, st.st_mode & 0o777)
' "$hexwork/appsecrets/field_key" "$lowkey"
lowbefore=$(sudo python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$hexwork/appsecrets/field_key")
rm -f "$result"
sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
os.environ["TOGAVSU_SECRETS_DIR"] = sys.argv[1]
os.environ["TOGAVSU_ADMIN_SECRETS_DIR"] = sys.argv[1] + "/no-admin-volume"
os.environ["CORE_BIN"] = sys.argv[2]
os.environ["RESULT"] = sys.argv[3]
os.environ["TOGAVSU_DOMAIN"] = "localhost"
os.execv("/bin/sh", ["sh", sys.argv[4]])
' "$hexwork/appsecrets" "$hexwork/core-stub" "$result" "$hexwork/entrypoint.sh"
sudo python3 -c '
import sys
want = sys.argv[2]
length, domain, secret, keyhex = open(sys.argv[1]).read().split()
if length != "32" or domain != "localhost" or int(secret) < 16 or keyhex != want:
    sys.exit("lowercase adapter result %s %s %s %s" % (length, domain, secret, keyhex))
' "$result" "$lowkey"
lowafter=$(sudo python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$hexwork/appsecrets/field_key")
test "$lowbefore" = "$lowafter"
# The entrypoint, as uid 65532, with a stub binary that records its arguments and
# whether it was handed the superuser URL.
cat > "$hexwork/args-stub" <<'EOF'
#!/bin/sh
printf '%s admin=%s\n' "$*" "${TOGAVSU_ADMIN_DATABASE_URL:+set}" > "$RESULT"
EOF
chmod 755 "$hexwork/args-stub"
entry() { # admin dir, then core arguments; prints the exit code
  admin_dir=$1
  shift
  sudo rm -f "$result"
  set +e
  sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
os.environ["TOGAVSU_SECRETS_DIR"] = sys.argv[1]
os.environ["TOGAVSU_ADMIN_SECRETS_DIR"] = sys.argv[2]
os.environ["CORE_BIN"] = sys.argv[3]
os.environ["RESULT"] = sys.argv[4]
os.environ["TOGAVSU_DOMAIN"] = "localhost"
os.environ["TOGAVSU_ADMIN_DATABASE_URL"] = "postgres://inherited"
os.execv("/bin/sh", ["sh", sys.argv[5]] + sys.argv[6:])
' "$hexwork/appsecrets" "$admin_dir" "$hexwork/args-stub" "$result" "$hexwork/entrypoint.sh" "$@" 2>/dev/null
  code=$?
  set -e
  echo "$code"
}
# migrate is handed the superuser URL from the admin volume.
test "$(entry "$hexwork/adminsecrets" migrate)" = 0
test "$(sudo cat "$result")" = "migrate admin=set"
# migrate without the admin volume fails before the binary runs.
test "$(entry "$hexwork/no-admin-volume" migrate)" = 2
sudo test ! -e "$result"
# The serving core never inherits the superuser URL from its environment ...
test "$(entry "$hexwork/no-admin-volume")" = 0
test "$(sudo cat "$result")" = " admin="
# ... and refuses to start when the admin volume is mounted on it.
test "$(entry "$hexwork/adminsecrets")" = 2
sudo test ! -e "$result"
echo "bootstrap secrets check passed"

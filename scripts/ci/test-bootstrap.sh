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
mkdir -p "$hexwork/pgsecrets" "$hexwork/appsecrets"
python3 -c 'import sys; open(sys.argv[1], "w").write("11"*32 + "\n")' "$hexwork/appsecrets/field_key"
before=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$hexwork/appsecrets/field_key")
sed "s#/pgsecrets#$hexwork/pgsecrets#g; s#/appsecrets#$hexwork/appsecrets#g" \
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
mkdir -p "$hexwork/out"
chmod 777 "$hexwork/out"
result="$hexwork/out/result"
sudo python3 -c '
import os, sys
os.setgroups([])
os.setgid(65532)
os.setuid(65532)
os.environ["TOGAVSU_SECRETS_DIR"] = sys.argv[1]
os.environ["CORE_BIN"] = sys.argv[2]
os.environ["RESULT"] = sys.argv[3]
os.environ["TOGAVSU_DOMAIN"] = "localhost"
os.execv("/bin/sh", ["sh", sys.argv[4]])
' "$hexwork/appsecrets" "$hexwork/core-stub" "$result" "$root/deploy/core/entrypoint.sh"
sudo python3 -c '
import sys
length, domain, secret, keyhex = open(sys.argv[1]).read().split()
if length != "32" or domain != "localhost" or int(secret) < 16 or keyhex != "11"*32:
    sys.exit("adapter result %s %s %s %s" % (length, domain, secret, keyhex))
' "$result"
sudo rm -rf "$hexwork"
echo "bootstrap secrets check passed"

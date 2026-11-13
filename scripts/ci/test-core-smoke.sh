#!/bin/sh
# Run the core image entrypoint against synthetic secrets. "core check" accepts the
# bootstrap configuration without touching the database; plain "core" must fail fast,
# not hang, when the database is unreachable. scripts/ci/test-compose-first-run.sh
# covers the served first-run on a real stack.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
if ! "$docker" image inspect togavsu-core:local >/dev/null 2>&1; then
  echo "core image is not built" >&2
  exit 1
fi
# The image ships LawCraft (core.WordcraftCLI, core.EditorDist) and the pinned Tesseract.
"$docker" run --rm --user 65532:65532 --entrypoint /bin/sh togavsu-core:local -c '
  set -eu
  /usr/local/bin/wordcraft-cli --help >/dev/null
  test -f /usr/share/togavsu/editor/index.html
  ls /usr/share/togavsu/editor/*.wasm >/dev/null
  tesseract --version 2>&1 | head -1 | grep -qx "tesseract 5.3.0"
  echo "7d4322bd2a7749724879683fc3912cb542f19906c83bcc1a52132556427170b2  /usr/share/tesseract-ocr/5/tessdata/eng.traineddata" | sha256sum -c - >/dev/null
'
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/appsecrets"
head -c 32 /dev/urandom | base64 -w 0 > "$work/appsecrets/field_key"
printf '\n' >> "$work/appsecrets/field_key"
printf 'postgres://togavsu:synthetic@postgres:5432/togavsu?sslmode=disable\n' > "$work/appsecrets/database_url"
printf 'synthetic-supervisor-secret\n' > "$work/appsecrets/supervisor_token"
printf 'firm.example\n' > "$work/appsecrets/domain"
chmod 755 "$work" "$work/appsecrets"
chmod 644 "$work/appsecrets/"*
out=$("$docker" run --rm --user 65532:65532 \
  -e TOGAVSU_DOMAIN=firm.example \
  -v "$work/appsecrets:/appsecrets:ro" \
  togavsu-core:local check)
printf '%s\n' "$out"
printf '%s\n' "$out" | grep -q "bootstrap configuration accepted"
printf '%s\n' "$out" | grep -q "HTTP service and migrations are not started"

# An existing hex key must export the same 32 bytes and must not be rewritten.
python3 -c 'import sys; open(sys.argv[1], "w").write(("abcdef"*10 + "abcd") + "\n")' "$work/appsecrets/field_key"
before=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$work/appsecrets/field_key")
out=$("$docker" run --rm --user 65532:65532 \
  -e TOGAVSU_DOMAIN=firm.example \
  -v "$work/appsecrets:/appsecrets:ro" \
  togavsu-core:local check)
printf '%s\n' "$out" | grep -q "bootstrap configuration accepted"
after=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read().hex())' "$work/appsecrets/field_key")
test "$before" = "$after"
# Serving without a reachable database exits non-zero instead of serving or hanging: the
# seal check (core.Config.RequireSealed) is the first thing that needs the database.
if timeout 60 "$docker" run --rm --user 65532:65532 --network none \
  -e TOGAVSU_DOMAIN=firm.example \
  -v "$work/appsecrets:/appsecrets:ro" \
  togavsu-core:local >"$work/serve.log" 2>&1; then
  echo "core served without a database" >&2
  exit 1
fi
grep -q "core: refusing to serve" "$work/serve.log"
echo "core image smoke passed"

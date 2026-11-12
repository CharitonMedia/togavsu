#!/bin/sh
# AC-018, AC-040 and AC-050 (backup part) on the real Compose stack. Synthetic data only.
#
# First-run setup; a staff user and token that get 403 on every backup route; synthetic
# records and files (Doc Tender makes Markdown copies of the text files against a synthetic
# OpenAI-compatible server, tests/e2e/fake_model.py); a network path destination (a host directory mounted into the
# supervisor) and an S3-compatible destination (rclone serve s3 on the Compose network;
# MinIO images are not pullable from this runner). A backup through /api/v1/backups is
# downloaded to "the admin's computer" (a temp dir) and compared byte for byte with the
# copies on both destinations. Then the database is dropped and the core volume wiped,
# first-run is done again with the same backup passphrase, the downloaded archive is
# uploaded and restored through /api/v1/restores, and the core row counts and the
# SHA-256 of every file version and rendition must match what was there before.
#
# Host ports: caddy on 28080/28443 only (an override resets 80/443). Nothing binds
# 5432-5440. The project is "bk-e2e-<pid>" and is removed with its volumes at the end.
#
# Env: TOGAVSU_CORE_IMAGE (default togavsu-core:bk; built when absent),
# TOGAVSU_BK_HTTPS_PORT (28443), TOGAVSU_BK_HTTP_PORT (28080), KEEP=1 keeps the stack.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
project=${TOGAVSU_CI_PROJECT:-bk-e2e-$$}
case "$project" in bk-*) ;; *) echo "project name must start with bk-" >&2; exit 2 ;; esac
https_port=${TOGAVSU_BK_HTTPS_PORT:-28443}
http_port=${TOGAVSU_BK_HTTP_PORT:-28080}
core_image=${TOGAVSU_CORE_IMAGE:-togavsu-core:bk}
supervisor_image=${TOGAVSU_SUPERVISOR_IMAGE:-togavsu-supervisor:bk-e2e}
base=https://localhost:$https_port
work=$(mktemp -d "${TMPDIR:-/tmp}/bk-e2e.XXXXXX")
mkdir -p "$work/netpath" "$work/s3/togavsu-backups" "$work/admin-computer"
chmod 0777 "$work/netpath" "$work/s3" "$work/s3/togavsu-backups"
state=$work/state.json

cat > "$work/override.yaml" <<EOF
services:
  caddy:
    ports: !override
      - "127.0.0.1:${http_port}:80"
      - "127.0.0.1:${https_port}:443"
  core:
    image: ${core_image}
  supervisor:
    image: ${supervisor_image}
    volumes:
      - ${work}/netpath:/mnt/netpath
  s3:
    image: rclone/rclone:latest
    user: "$(id -u):$(id -g)"
    command: ["serve", "s3", "/data", "--auth-key", "bkaccesskey,bksecretkey-synthetic", "--addr", ":9000"]
    volumes:
      - ${work}/s3:/data
    restart: "no"
  fakeai:
    image: python:3.12-alpine
    command: ["python3", "-I", "/fake_model.py"]
    volumes:
      - ${root}/tests/e2e/fake_model.py:/fake_model.py:ro
    restart: "no"
EOF

compose() { "$docker" compose -p "$project" -f "$root/compose.yaml" -f "$work/override.yaml" "$@"; }
step=start
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "FAIL during step: $step" >&2
    compose ps -a >&2 || true
    compose logs --no-color --tail 120 core supervisor s3 >&2 || true
  fi
  if [ "${KEEP:-0}" != 1 ]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
    # The supervisor writes the network path copies as root.
    "$docker" run --rm -v "$work:/w" postgres:17 sh -c 'rm -rf /w/netpath /w/s3' >/dev/null 2>&1 || true
    rm -rf "$work"
  else
    echo "kept project $project and $work" >&2
  fi
  exit "$status"
}
trap cleanup EXIT INT TERM

cd "$root"
build=--build
if "$docker" image inspect "$core_image" >/dev/null 2>&1; then
  build=--no-build
  # The supervisor image is small; build it so it carries this tree's code.
  compose build supervisor >/dev/null
fi
step="compose up"
compose up -d $build --wait --wait-timeout 300

wait_health() {
  i=0
  until curl -skf "$base/api/v1/health" >/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 150 ]; then
      echo "core did not answer $base/api/v1/health" >&2
      return 1
    fi
    sleep 2
  done
}
wait_health
echo "PASS: stack is up ($project, $base)"

py() { python3 -m tests.e2e.backup_e2e "$@"; }
sql() { compose exec -T postgres psql -v ON_ERROR_STOP=1 -U togavsu -d "${2:-togavsu}" -Atc "$1"; }
counts() {
  sql "SELECT t.table_name || ' ' || (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM core.%I', t.table_name), false, true, '')))[1]::text
       FROM information_schema.tables t WHERE t.table_schema = 'core' AND t.table_type = 'BASE TABLE' ORDER BY 1"
}

step=setup; py setup "$base" "$state"
step=denied; py denied "$base" "$state"
step=hashes; py hashes "$base" "$state" "$work/hashes-before.json"
db_objects() {
  sql "SELECT 'version ' || id || ' ' || sha256 FROM core.file_versions UNION ALL SELECT 'rendition ' || id || ' ' || sha256 FROM core.file_renditions ORDER BY 1"
}
db_objects > "$work/db-objects-before.txt"
if [ ! -s "$work/db-objects-before.txt" ]; then
  echo "no file versions in the database" >&2
  exit 1
fi
step=destinations; py destinations "$base" "$state"
step=mcp; py mcp "$base" "$state"
# Background workers (Doc Tender, events) must be idle: two reads 12 seconds apart agree.
settle() {
  i=0
  counts > "$1"
  while :; do
    sleep 12
    counts > "$1.next"
    if cmp -s "$1" "$1.next"; then
      rm -f "$1.next"
      return 0
    fi
    mv "$1.next" "$1"
    i=$((i + 1))
    if [ "$i" -gt 15 ]; then
      echo "core row counts did not settle" >&2
      return 1
    fi
  done
}
step=counts-before; settle "$work/counts-before.txt"
rows=$(awk '{s += $2} END {print s + 0}' "$work/counts-before.txt")
if [ "$rows" -lt 20 ]; then
  echo "too few rows to compare: $rows" >&2
  exit 1
fi
echo "PASS: recorded $(wc -l < "$work/counts-before.txt" | tr -d ' ') core tables, $rows rows, $(wc -l < "$work/db-objects-before.txt" | tr -d ' ') file objects"
step=backup; py backup "$base" "$state" "$work/netpath" "$work/s3" "$work/admin-computer"
archive=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["archive"])' "$state")

step=destroy
compose stop core >/dev/null
sql "DROP DATABASE togavsu WITH (FORCE)" postgres >/dev/null
sql "CREATE DATABASE togavsu" postgres >/dev/null
compose run --rm --no-deps dbinit >/dev/null
"$docker" run --rm -u 0 -v "${project}_core_data:/d" postgres:17 sh -c 'find /d -mindepth 1 -delete'
left=$("$docker" run --rm -u 0 -v "${project}_core_data:/d" postgres:17 sh -c 'find /d -mindepth 1 | wc -l')
if [ "$left" != "0" ]; then
  echo "core volume still holds $left entries" >&2
  exit 1
fi
compose start core >/dev/null
wait_health
tables=$(sql "SELECT count(*) FROM core.file_versions")
if [ "$tables" != "0" ]; then
  echo "file versions survived the wipe: $tables" >&2
  exit 1
fi
echo "PASS: database dropped and core volume wiped; the core came back empty"
step=resetup; py resetup "$base" "$state"

step=restore; py restore "$base" "$state" "$archive"
step=counts-after; counts > "$work/counts-after.txt"
if ! diff -u "$work/counts-before.txt" "$work/counts-after.txt" >"$work/counts.diff"; then
  echo "core row counts differ after the restore (before, after):" >&2
  cat "$work/counts.diff" >&2
  exit 1
fi
echo "PASS: $(wc -l < "$work/counts-after.txt" | tr -d ' ') core tables match their pre-backup row counts ($rows rows)"
db_objects > "$work/db-objects-after.txt"
if ! cmp -s "$work/db-objects-before.txt" "$work/db-objects-after.txt"; then
  diff -u "$work/db-objects-before.txt" "$work/db-objects-after.txt" >&2 || true
  exit 1
fi
echo "PASS: file version and rendition rows match by id and SHA-256"
step=signin; py signin "$base" "$state" "$work/hashes-before.json"
echo "backup e2e passed"

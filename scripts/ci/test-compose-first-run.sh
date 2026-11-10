#!/bin/sh
# AC-001 on a clean Docker host: one Compose up with no edits reaches first-run setup.
# Then: setup with two-factor, a matter write with its audit rows, proof that the core's
# database role cannot change audit_log, and a core restart that keeps the session and
# the matter. Synthetic data only. The project and its volumes are removed at the end.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
project=${TOGAVSU_CI_PROJECT:-togavsu-firstrun}
base=${TOGAVSU_CI_BASE:-https://localhost}
work=$(mktemp -d)
compose() { "$docker" compose -p "$project" -f "$root/compose.yaml" "$@"; }
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    compose ps -a || true
    compose logs --no-color --tail 80 core dbinit postgres || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT INT TERM

compose down -v --remove-orphans >/dev/null 2>&1 || true
compose up -d --build --wait --wait-timeout 300

wait_health() {
  i=0
  until curl -skf "$base/api/v1/health" >/dev/null; do
    i=$((i + 1))
    if [ "$i" -gt 90 ]; then
      echo "core did not answer $base/api/v1/health" >&2
      return 1
    fi
    sleep 1
  done
}
wait_health
cd "$root"
python3 -m tests.e2e.first_run setup "$base" "$work/state.json"

psql_as() {
  compose exec -T postgres psql -v ON_ERROR_STOP=1 -U togavsu -d togavsu -Atc "$1"
}
super=$(psql_as "SELECT rolsuper FROM pg_roles WHERE rolname = 'togavsu_app'")
if [ "$super" != "f" ]; then
  echo "core role togavsu_app is missing or a superuser: '$super'" >&2
  exit 1
fi
owner=$(psql_as "SELECT tableowner FROM pg_tables WHERE schemaname = 'core' AND tablename = 'audit_log'")
if [ "$owner" != "togavsu_app" ]; then
  echo "audit_log owner is '$owner', want togavsu_app" >&2
  exit 1
fi
rows=$(psql_as "SELECT count(*) FROM core.audit_log")
for stmt in "UPDATE core.audit_log SET new_value = 'tampered'" "DELETE FROM core.audit_log" "TRUNCATE core.audit_log"; do
  if psql_as "SET ROLE togavsu_app; $stmt" >/dev/null 2>&1; then
    echo "core role ran: $stmt" >&2
    exit 1
  fi
done
after=$(psql_as "SELECT count(*) FROM core.audit_log")
if [ "$rows" != "$after" ] || [ "$rows" -lt 1 ]; then
  echo "audit_log rows changed: $rows -> $after" >&2
  exit 1
fi
echo "audit_log is append-only for the core role ($rows rows)"

compose restart core >/dev/null
wait_health
python3 -m tests.e2e.first_run verify "$base" "$work/state.json"
echo "compose first-run passed"

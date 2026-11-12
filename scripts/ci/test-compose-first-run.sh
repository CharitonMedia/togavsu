#!/bin/sh
# AC-001 on a clean Docker host: one Compose up with no edits reaches first-run setup.
# Then: setup with two-factor, a matter write with its audit rows, proof that the core's
# database role cannot change, grant back, alter or drop the append-only tables, and a core restart that keeps the session and
# the matter. Also: dbinit hands a superuser-owned core schema from an older volume to
# togavsu_app. Synthetic data only. The project and its volumes are removed at the end.
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
    compose logs --no-color --tail 80 core migrate dbinit postgres || true
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
append_only() {
  # core migrate (compose service migrate) hands schema core and the append-only tables
  # to the NOLOGIN togavsu_ledger_owner; togavsu_app keeps SELECT and INSERT only.
  for t in audit_log trust_ledger events ai_call_log file_versions file_renditions; do
    owner=$(psql_as "SELECT tableowner FROM pg_tables WHERE schemaname = 'core' AND tablename = '$t'")
    if [ "$owner" != "togavsu_ledger_owner" ]; then
      echo "$t owner is '$owner', want togavsu_ledger_owner" >&2
      return 1
    fi
    for stmt in "UPDATE core.$t SET created_by = created_by" "DELETE FROM core.$t" "TRUNCATE core.$t" \
      "GRANT UPDATE, DELETE, TRUNCATE ON core.$t TO togavsu_app; UPDATE core.$t SET created_by = created_by" \
      "ALTER TABLE core.$t ADD COLUMN tampered text" "DROP TABLE core.$t"; do
      if psql_as "SET ROLE togavsu_app; $stmt" >/dev/null 2>&1; then
        echo "core role ran: $stmt" >&2
        return 1
      fi
    done
  done
  # SET ROLE is checked against the superuser session here, so ask the catalog instead.
  member=$(psql_as "SELECT pg_has_role('togavsu_app', 'togavsu_ledger_owner', 'MEMBER')")
  if [ "$member" != "f" ]; then
    echo "core role can act as togavsu_ledger_owner" >&2
    return 1
  fi
  rows=$(psql_as "SELECT count(*) FROM core.audit_log")
  for stmt in "UPDATE core.audit_log SET new_value = 'tampered'" "DELETE FROM core.audit_log" "TRUNCATE core.audit_log"; do
    if psql_as "SET ROLE togavsu_app; $stmt" >/dev/null 2>&1; then
      echo "core role ran: $stmt" >&2
      return 1
    fi
  done
  after=$(psql_as "SELECT count(*) FROM core.audit_log")
  if [ "$rows" != "$after" ] || [ "$rows" -lt 1 ]; then
    echo "audit_log rows changed: $rows -> $after" >&2
    return 1
  fi
  echo "audit_log is append-only for the core role ($rows rows)"
  # AC-006 at the database: the trust ledger refuses every change as well.
  for stmt in "UPDATE core.trust_ledger SET amount_cents = 0" "UPDATE core.trust_ledger SET description = 'x'" "DELETE FROM core.trust_ledger" "TRUNCATE core.trust_ledger"; do
    if psql_as "SET ROLE togavsu_app; $stmt" >/dev/null 2>&1; then
      echo "core role ran: $stmt" >&2
      return 1
    fi
  done
  echo "trust_ledger is append-only for the core role"
}
append_only

# A volume from an install where the core ran as the Postgres superuser has schema core
# and its relations owned by togavsu. Rebuild that state, rerun dbinit, and check that
# core-role.sh hands everything to togavsu_app and the core migrates and serves on it.
compose stop core >/dev/null
psql_as "ALTER SCHEMA core OWNER TO togavsu;
DO \$\$ DECLARE r record; BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'core' LOOP
    EXECUTE format('ALTER TABLE core.%I OWNER TO togavsu', r.tablename);
  END LOOP;
END \$\$;
CREATE SEQUENCE core.legacy_counter;
CREATE VIEW core.legacy_view AS SELECT 1 AS one;" >/dev/null
legacy=$(psql_as "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'core' AND c.relowner = 'togavsu'::regrole::oid")
if [ "$legacy" -lt 3 ]; then
  echo "legacy ownership was not staged: $legacy relations owned by togavsu" >&2
  exit 1
fi
compose run --rm --no-deps dbinit >/dev/null
schema_owner=$(psql_as "SELECT nspowner::regrole FROM pg_namespace WHERE nspname = 'core'")
left=$(psql_as "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'core' AND c.relkind IN ('r','p','v','m','f','S') AND c.relowner <> 'togavsu_app'::regrole::oid")
if [ "$schema_owner" != "togavsu_app" ] || [ "$left" != "0" ]; then
  echo "dbinit left core owned by '$schema_owner' with $left relations not owned by togavsu_app" >&2
  exit 1
fi
echo "dbinit handed a superuser-owned core schema to togavsu_app"
# core migrate seals the schema and the append-only tables again before the core starts.
compose run --rm --no-deps migrate >/dev/null
compose start core >/dev/null
wait_health
append_only

compose restart core >/dev/null
wait_health
python3 -m tests.e2e.first_run verify "$base" "$work/state.json"
echo "compose first-run passed"

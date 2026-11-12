#!/bin/sh
# Create or update the core's login role. Runs on every Compose up, after Postgres is
# healthy and before the core starts, so it also covers volumes from earlier installs.
# togavsu_app has no superuser or CREATEDB. It owns schema core and every core table but
# the append-only ones (audit_log, trust_ledger, events, ai_call_log, file_versions,
# file_renditions): after this script, the one-shot "core migrate" service applies the
# migrations as togavsu_app and, as this superuser, hands those tables to the NOLOGIN
# togavsu_ledger_owner, leaving togavsu_app SELECT and INSERT only (db.SealLedgers).
# togavsu_app is never a member of togavsu_ledger_owner, so it cannot grant UPDATE,
# DELETE or TRUNCATE back to itself. It has CREATEROLE so the module runtime can give each
# installed module its own NOINHERIT login and schema (PRD 9.9); on Postgres 16 and later
# a CREATEROLE role administers only the roles it created and can't grant SUPERUSER,
# CREATEDB, REPLICATION or BYPASSRLS, so this reaches no other role. The supervisor keeps the admin role
# for backup and restore; restore loads objects as togavsu_app.
set -eu
admin_pass=$(tr -d '\n' < /pgsecrets/postgres_password)
app_pass=$(tr -d '\n' < /pgsecrets/app_password)
case "$app_pass" in
  *[!0-9a-f]*|'') echo "core-role: app password must be hex" >&2; exit 1 ;;
esac
export PGPASSWORD="$admin_pass"
psql --set ON_ERROR_STOP=1 -h postgres -U togavsu -d togavsu <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_app') THEN
    CREATE ROLE togavsu_app LOGIN NOSUPERUSER NOCREATEDB CREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_audit_writer') THEN
    CREATE ROLE togavsu_audit_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
  END IF;
  -- The module runtime writes the AI call log as this append-only role (internal/modules).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'togavsu_ai_log_writer') THEN
    CREATE ROLE togavsu_ai_log_writer NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
  END IF;
END \$\$;
ALTER ROLE togavsu_app WITH LOGIN NOSUPERUSER NOCREATEDB CREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '$app_pass';
GRANT togavsu_audit_writer TO togavsu_app WITH ADMIN OPTION;
GRANT togavsu_ai_log_writer TO togavsu_app WITH ADMIN OPTION;
GRANT CONNECT, CREATE ON DATABASE togavsu TO togavsu_app;
CREATE SCHEMA IF NOT EXISTS core AUTHORIZATION togavsu_app;
-- A volume from an install where the core ran as togavsu has core and its tables owned
-- by togavsu. Hand them to togavsu_app, or the core cannot migrate. Sequences tied to a
-- column move with their table. A sealed schema and its append-only tables stay with
-- togavsu_ledger_owner; core migrate unseals the tables for its own run.
DO \$\$ BEGIN
  IF (SELECT pg_get_userbyid(nspowner) FROM pg_namespace WHERE nspname = 'core') <> 'togavsu_ledger_owner' THEN
    ALTER SCHEMA core OWNER TO togavsu_app;
  END IF;
END \$\$;
DO \$\$
DECLARE r record;
BEGIN
  FOR r IN
    SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'core' AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
      AND c.relowner <> 'togavsu_app'::regrole::oid
      AND pg_get_userbyid(c.relowner) <> 'togavsu_ledger_owner'
      AND NOT (c.relkind = 'S' AND EXISTS (SELECT 1 FROM pg_depend d
        WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a', 'i')))
  LOOP
    EXECUTE format('ALTER %s core.%I OWNER TO togavsu_app',
      CASE r.relkind WHEN 'S' THEN 'SEQUENCE' WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW'
        WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END, r.relname);
  END LOOP;
END \$\$;
SQL
echo "core-role: togavsu_app is ready"

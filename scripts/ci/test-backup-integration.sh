#!/bin/sh
# Backup and restore against a real Postgres. Synthetic rows only.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
url=${TOGAVSU_TEST_DATABASE_URL:-}
if [ -z "$url" ]; then
  echo "PENDING database integration: set TOGAVSU_TEST_DATABASE_URL" >&2
  exit 1
fi
dump=${TOGAVSU_PG_DUMP:-pg_dump}
psql=${TOGAVSU_PSQL:-psql}
export TOGAVSU_PG_DUMP="$dump"
export TOGAVSU_PSQL="$psql"
if [ -f "$root/go.mod" ]; then
  cd "$root"
else
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  mkdir -p "$work/internal"
  cp -a "$root/internal/supervisor" "$work/internal/supervisor"
  printf 'module github.com/CharitonMedia/togavsu\n\ngo 1.22\n' > "$work/go.mod"
  cd "$work"
fi
go test -tags=integration ./internal/supervisor/ -count=1 -run 'TestPostgresRestoreReplacesEvolvedSchema|TestUpdateRollbackKeepsWriteAfterSnapshot'

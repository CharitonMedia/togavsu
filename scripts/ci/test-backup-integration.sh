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
log=$(mktemp)
# Every integration-tagged restore test. -v is required so CI logs show each
# safety test actually ran; a name filter that omits one is a failure.
# The build tag also compiles the rest of the package. This list is every
# Test in the integration files, not the sshd and Compose tests.
if ! go test -tags=integration -count=1 -timeout 20m -v ./internal/supervisor/ -run '^(TestPostgresRestoreReplacesEvolvedSchema|TestUpdateRollbackKeepsWriteAfterSnapshot|TestPostgres17Swap|TestDelayedRenameCannotCommitAfterRecovery|TestInFlightRenameCannotCommitAfterAFailedRestore|TestCommittedRenameSurvivesLostJournal|TestOwnedTempDatabaseSurvivesOnlyWhenVerified|TestUnverifiedAllocationRestartsWriters|TestDifferentClusterIsRejected)($|/)' >"$log" 2>&1; then
  cat "$log" >&2
  rm -f "$log"
  exit 1
fi
cat "$log"
if grep -q "no tests to run" "$log"; then
  echo "integration suite matched nothing" >&2
  rm -f "$log"
  exit 1
fi
for name in \
  TestPostgresRestoreReplacesEvolvedSchema \
  TestUpdateRollbackKeepsWriteAfterSnapshot \
  TestPostgres17Swap \
  TestInFlightRenameCannotCommitAfterAFailedRestore \
  TestDelayedRenameCannotCommitAfterRecovery \
  TestCommittedRenameSurvivesLostJournal \
  TestOwnedTempDatabaseSurvivesOnlyWhenVerified \
  TestUnverifiedAllocationRestartsWriters \
  TestDifferentClusterIsRejected
do
  if ! grep -q "=== RUN   ${name}$" "$log"; then
    echo "backup integration did not run ${name}" >&2
    rm -f "$log"
    exit 1
  fi
done
rm -f "$log"

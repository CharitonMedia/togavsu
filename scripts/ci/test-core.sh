#!/bin/sh
# Core tests: vet and test every Go package in the module against real Postgres,
# including identity, practice, the composed core and its end-to-end HTTP tests.
# The supervisor's own suites stay in test-supervisor.sh and test-backup-integration.sh.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
if [ -z "${TOGAVSU_TEST_DATABASE_URL:-}" ]; then
  echo "TOGAVSU_TEST_DATABASE_URL is required: core tests run against real Postgres" >&2
  exit 1
fi
test -z "$(gofmt -l cmd internal api web)" || { gofmt -l cmd internal api web >&2; echo "gofmt" >&2; exit 1; }
go vet ./...
pkgs=$(go list ./...)
log=$(mktemp)
trap 'rm -f "$log"' EXIT
# -count=1 so a cached pass never stands in for a run against this database. The
# output goes to a file first: a pipe into tee would report tee's status, not go test's.
if ! go test -count=1 -timeout 15m $pkgs >"$log" 2>&1; then
  cat "$log"
  echo "core tests failed" >&2
  exit 1
fi
cat "$log"
# A skipped Postgres test is a failure here: CI must exercise the database paths.
# The phase 4 documents cases that need only Postgres are in this strict run; the editor
# case also needs wordcraft-cli (TOGAVSU_WORDCRAFT_CLI) and runs where it is built.
go test -count=1 -v -run 'PG|Postgres|Durable|Core|FirstRun|Contract|Walls|Bearer|Practice|Fresh|Setup|Finance|Trust|Billing|DocumentsCalendar|DocumentsStorage|DocumentsDocTender|PurgedFile|DTCorpus' ./internal/core/ ./internal/practice/ ./internal/identity/ ./internal/audit/ ./internal/db/ ./internal/trust/ ./internal/billing/ ./internal/calendar/ ./internal/doctender/ >"$log" 2>&1 || { cat "$log"; exit 1; }
if grep -E -- '--- SKIP' "$log"; then
  echo "a database test was skipped" >&2
  exit 1
fi
# -short keeps the race run's resumable upload at 64 MiB; the full run above sends 1 GiB.
go test -count=1 -race -short ./internal/core/ ./internal/practice/
echo "core tests passed: $(echo "$pkgs" | wc -l | tr -d ' ') packages"

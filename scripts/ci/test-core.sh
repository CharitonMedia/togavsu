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
# -count=1 so a cached pass never stands in for a run against this database.
if ! go test -count=1 -timeout 15m $pkgs 2>&1 | tee "$log"; then
  exit 1
fi
# A skipped Postgres test is a failure here: CI must exercise the database paths.
if go test -count=1 -run 'PG|Postgres|Durable|Core|FirstRun|Contract|Walls|Bearer|Practice|Fresh|Setup' -v ./internal/core/ ./internal/practice/ ./internal/identity/ ./internal/audit/ ./internal/db/ 2>&1 | grep -E -- '--- SKIP' ; then
  echo "a database test was skipped" >&2
  exit 1
fi
go test -count=1 -race ./internal/core/ ./internal/practice/
echo "core tests passed: $(echo "$pkgs" | wc -l | tr -d ' ') packages"

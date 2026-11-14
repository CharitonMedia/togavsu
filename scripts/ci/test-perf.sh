#!/bin/sh
# AC-017: p95 page and API response under 300 ms on seed data of 50 users, 20,000
# matters and 1,000,000 time entries. Runs TestReleasePerfP95 (internal/core) against
# TOGAVSU_TEST_DATABASE_URL: it seeds the database togavsu_perf once (kept for later
# runs; TOGAVSU_PERF_RESEED=1 starts over), then times staff and admin pages and API
# calls through the core's HTTP stack and prints p50, p95 and max per endpoint.
# Exits 0 only when every endpoint's p95 and the overall p95 are under 300 ms.
# TOGAVSU_PERF_N (default 200) and TOGAVSU_PERF_CONCURRENCY (default 4) size the run.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
if [ -z "${TOGAVSU_TEST_DATABASE_URL:-}" ]; then
  echo "TOGAVSU_TEST_DATABASE_URL is required for the AC-017 performance test" >&2
  exit 1
fi
if [ -n "${TOGAVSU_PERF_ONLY:-}" ]; then
  echo "TOGAVSU_PERF_ONLY runs a subset and cannot pass AC-017; unset it" >&2
  exit 1
fi
log=$(mktemp)
trap 'rm -f "$log"' EXIT
status=0
TOGAVSU_PERF=1 go test -count=1 -v -timeout 120m -run '^TestReleasePerfP95$' ./internal/core/ >"$log" 2>&1 || status=$?
cat "$log"
if [ "$status" -ne 0 ]; then
  echo "AC-017 performance test failed" >&2
  exit 1
fi
grep -q -- "--- PASS: TestReleasePerfP95 " "$log" || { echo "TestReleasePerfP95 did not pass" >&2; exit 1; }
echo "AC-017 performance test passed"

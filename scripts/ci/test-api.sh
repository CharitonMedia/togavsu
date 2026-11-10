#!/bin/sh
# API contract tests: the contract suite and spec validation (api/check.sh) in a clean
# virtualenv, then the runtime checks that the running core serves the contract on
# /api/v1 and /mcp, fails closed for every operation, and enforces token scopes.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
venv=${TOGAVSU_API_VENV:-$(mktemp -d)/venv}
python3 -m venv "$venv"
"$venv/bin/python" -m pip install --quiet -r api/requirements-dev.txt
PATH="$venv/bin:$PATH" sh api/check.sh
if [ -z "${TOGAVSU_TEST_DATABASE_URL:-}" ]; then
  echo "TOGAVSU_TEST_DATABASE_URL is required for the runtime contract tests" >&2
  exit 1
fi
log=$(mktemp)
trap 'rm -f "$log"' EXIT
if ! go test -count=1 -v -run '^(TestContractRoutesFailClosed|TestBearerScopesMCPAndSettings|TestFirstRunGate)$' ./internal/core/ >"$log" 2>&1; then
  cat "$log" >&2
  exit 1
fi
cat "$log"
for name in TestContractRoutesFailClosed TestBearerScopesMCPAndSettings TestFirstRunGate; do
  grep -q -- "--- PASS: $name " "$log" || { echo "$name did not pass" >&2; exit 1; }
done
echo "API contract tests passed"

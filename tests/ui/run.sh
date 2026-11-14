#!/bin/sh
# UI visual QC (WORKFLOW.md, PRD 12): opens every shell screen and admin tab as each role
# (TestUIVisualQC, internal/dashboard/compose/qc/ui-qc.mjs) and the module screens
# (TestExtensionsScreens, scripts/ci/documents-qc.mjs) in headless Chrome on seeded
# synthetic data. A page fails on an unexpected status, a console or page error, a failed
# request or a CSP violation. The documents screens run in scripts/ci/test-core-image-cases.sh,
# which has the image's editor build. Any skip or missing PASS fails.
# Needs Docker (a disposable postgres:17 when TOGAVSU_TEST_DATABASE_URL is unset), Go,
# Node with npm, and Chrome or Chromium (CHROME overrides). TOGAVSU_UI_QC_ARTIFACTS, when
# set, receives the screenshots and results.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
work=$(mktemp -d)
pg=""
cleanup() {
  [ -n "$pg" ] && docker rm -f "$pg" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

if [ -z "${CHROME:-}" ]; then
  for c in google-chrome chromium chromium-browser; do
    if command -v "$c" >/dev/null 2>&1; then CHROME=$(command -v "$c"); break; fi
  done
fi
[ -n "${CHROME:-}" ] || { echo "UI QC needs Chrome or Chromium (set CHROME)" >&2; exit 1; }
export CHROME

mods=${TOGAVSU_UI_QC_NODE_MODULES:-}
if [ -z "$mods" ]; then
  # Same pin as deploy/core-test.Dockerfile.
  npm install --prefix "$work/e2e" --no-audit --no-fund --silent puppeteer-core@23.11.1 >/dev/null
  mods=$work/e2e/node_modules
fi
[ -d "$mods/puppeteer-core" ] || { echo "no puppeteer-core in $mods" >&2; exit 1; }

if [ -z "${TOGAVSU_TEST_DATABASE_URL:-}" ]; then
  pg=togavsu-ui-qc-$$
  docker run -d --rm --name "$pg" -e POSTGRES_USER=togavsu -e POSTGRES_PASSWORD=synthetic-ui-qc \
    -e POSTGRES_DB=togavsu -p 127.0.0.1::5432 postgres:17 >/dev/null
  i=0
  until docker exec "$pg" pg_isready -U togavsu -d togavsu -h 127.0.0.1 >/dev/null 2>&1; do
    i=$((i + 1)); [ "$i" -lt 60 ] || { echo "postgres did not start" >&2; exit 1; }
    sleep 1
  done
  port=$(docker port "$pg" 5432/tcp | head -1 | sed 's/.*://')
  TOGAVSU_TEST_DATABASE_URL="postgres://togavsu:synthetic-ui-qc@127.0.0.1:$port/togavsu?sslmode=disable"
fi
export TOGAVSU_TEST_DATABASE_URL CI=1

shell=$work/shell
modules=$work/modules
mkdir -p "$shell" "$modules"
log=$work/ui-qc.log
status=0
TOGAVSU_UI_QC_OUT=$shell TOGAVSU_UI_QC_NODE_MODULES=$mods \
  go test -count=1 -timeout 20m -v -run '^TestUIVisualQC$' ./internal/dashboard/compose/ >"$log" 2>&1 || status=1
TOGAVSU_QC_DIR=$modules PUPPETEER_CORE=$mods/puppeteer-core \
  go test -count=1 -timeout 20m -v -run '^TestExtensionsScreens$' ./internal/core/ >>"$log" 2>&1 || status=1
cat "$log"
if [ -n "${TOGAVSU_UI_QC_ARTIFACTS:-}" ]; then
  mkdir -p "$TOGAVSU_UI_QC_ARTIFACTS"
  cp -R "$shell" "$modules" "$log" "$TOGAVSU_UI_QC_ARTIFACTS"/
fi
[ "$status" -eq 0 ] || { echo "UI QC failed" >&2; exit 1; }
if grep -E -- '--- SKIP' "$log"; then
  echo "a UI QC case was skipped" >&2
  exit 1
fi
for name in TestUIVisualQC TestExtensionsScreens; do
  grep -q -- "--- PASS: $name " "$log" || { echo "$name did not pass" >&2; exit 1; }
done
n=$(find "$shell" -name '*.png' | wc -l)
[ "$n" -gt 0 ] || { echo "the shell QC saved no screenshots" >&2; exit 1; }
test -s "$modules/qc.json" || { echo "the module QC wrote no qc.json" >&2; exit 1; }
echo "UI QC passed: $n shell screenshots, $(find "$modules" -name '*.png' | wc -l) module screenshots"

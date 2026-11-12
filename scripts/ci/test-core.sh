#!/bin/sh
# Core tests: vet and test every Go package in the module against real Postgres,
# including identity, practice, the composed core and its end-to-end HTTP tests, then
# the phase 4 cases inside the built core image. Needs Docker.
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
tools=$(mktemp -d)
trap 'rm -rf "$log" "$tools"' EXIT
# The core image (deploy/core.Dockerfile) builds LawCraft's wordcraft-cli and browser
# editor at the pin. The host run uses the image's own builds, so the lawcraft-editor
# case runs here instead of skipping; scripts/ci/test-core-image-cases.sh then runs the
# phase 4 cases inside the image with its Tesseract, Chromium and the editor build.
docker=${DOCKER:-docker}
sh scripts/ci/build-core-image.sh
cid=$("$docker" create togavsu-core:local)
"$docker" cp "$cid:/usr/local/bin/wordcraft-cli" "$tools/wordcraft-cli"
"$docker" cp "$cid:/usr/share/togavsu/editor" "$tools/editor"
"$docker" rm "$cid" >/dev/null
export TOGAVSU_WORDCRAFT_CLI="$tools/wordcraft-cli" TOGAVSU_REQUIRE_WORDCRAFT_CLI=1 TOGAVSU_EDITOR_DIST="$tools/editor"
# -count=1 so a cached pass never stands in for a run against this database. 25m covers
# the phase 4 cases (a 1 GiB upload, a 120-page scan). The
# output goes to a file first: a pipe into tee would report tee's status, not go test's.
if ! go test -count=1 -timeout 25m $pkgs >"$log" 2>&1; then
  cat "$log"
  echo "core tests failed" >&2
  exit 1
fi
cat "$log"
# A skipped Postgres test is a failure here: CI must exercise the database paths.
# Every phase 4 documents case and every integrate-extensions case is in this strict run, the editor case with the image's
# wordcraft-cli (TOGAVSU_REQUIRE_WORDCRAFT_CLI makes a missing CLI a failure, not a skip).
go test -count=1 -v -run 'PG|Postgres|Durable|Core|FirstRun|Contract|Walls|Bearer|Practice|Fresh|Setup|Finance|Trust|Billing|DocumentsCalendar|DocumentsStorage|DocumentsLawCraft|DocumentsDocTender|PurgedFile|DTCorpus|ExtensionsModule|ExtensionsMarketplace|ExtensionsAI|ExtensionsUI' ./internal/core/ ./internal/practice/ ./internal/identity/ ./internal/audit/ ./internal/db/ ./internal/trust/ ./internal/billing/ ./internal/calendar/ ./internal/doctender/ >"$log" 2>&1 || { cat "$log"; exit 1; }
if grep -E -- '--- SKIP' "$log"; then
  echo "a database test was skipped" >&2
  exit 1
fi
for name in TestDocumentsCalendarDeadlines TestDocumentsStorageVersionsMoves TestDocumentsLawCraftEditor TestDocumentsDocTenderCorpus TestDocumentsDocTenderTokensAndMCP \
  TestExtensionsModuleLifecycle TestExtensionsMarketplaceAlerts TestExtensionsAIMediation TestExtensionsUIModules; do
  grep -q -- "--- PASS: $name " "$log" || { cat "$log"; echo "$name did not pass in the strict run" >&2; exit 1; }
done
# -short keeps the race run's resumable upload at 64 MiB; the full run above sends 1 GiB.
go test -count=1 -race -short ./internal/core/ ./internal/practice/
sh scripts/ci/test-core-image-cases.sh
echo "core tests passed: $(echo "$pkgs" | wc -l | tr -d ' ') packages"

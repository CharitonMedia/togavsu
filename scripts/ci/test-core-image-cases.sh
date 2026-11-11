#!/bin/sh
# Phase 4 cases inside the core image (deploy/core-test.Dockerfile adds Go, Chromium and
# puppeteer-core), so CI runs them on what ships: the image's wordcraft-cli and browser
# editor (lawcraft-editor, AC-041/AC-042, with the browser round trip and visual QC) and
# its pinned Tesseract as Doc Tender's second reader (doctender-corpus, AC-043..AC-052).
# Every named test must pass; any skip fails. Needs Docker and TOGAVSU_TEST_DATABASE_URL
# reachable from the host network. Synthetic data only.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
if [ -z "${TOGAVSU_TEST_DATABASE_URL:-}" ]; then
  echo "TOGAVSU_TEST_DATABASE_URL is required" >&2
  exit 1
fi
if ! "$docker" image inspect togavsu-core:local >/dev/null 2>&1; then
  sh "$root/scripts/ci/build-core-image.sh"
fi
"$docker" build -q -f "$root/deploy/core-test.Dockerfile" -t togavsu-core-test:local "$root" >/dev/null
work=$(mktemp -d)
log=$work/cases.log
qc=$work/qc
mkdir -p "$qc"
chmod 777 "$work" "$qc"
trap 'rm -rf "$work"' EXIT
# The tools the cases rely on are the image's own.
"$docker" run --rm togavsu-core-test:local sh -c 'tesseract --version 2>&1 | head -1; command -v wordcraft-cli; test -f /usr/share/togavsu/editor/index.html'
uid=$(id -u)
gid=$(id -g)
run() {
  "$docker" run --rm --network host --user "$uid:$gid" \
    -v "$root:/src" -v "$qc:/qc" -w /src \
    -e TOGAVSU_TEST_DATABASE_URL -e CI=1 \
    -e TOGAVSU_WORDCRAFT_CLI=/usr/local/bin/wordcraft-cli -e TOGAVSU_REQUIRE_WORDCRAFT_CLI=1 \
    -e TOGAVSU_EDITOR_DIST=/usr/share/togavsu/editor \
    -e DOCTENDER_REQUIRE_TESSERACT=1 \
    -e TOGAVSU_BROWSER_E2E=1 -e CHROME=/usr/bin/chromium -e PUPPETEER_CORE=/opt/e2e/node_modules/puppeteer-core \
    -e TOGAVSU_QC_DIR=/qc -e DOCTENDER_CORPUS_DIR=/tmp/corpus \
    togavsu-core-test:local "$@"
}
cases='^(TestDocumentsLawCraftEditor|TestDocumentsDocTenderCorpus|TestDocumentsDocTenderTokensAndMCP|TestDocumentsScreens)$'
if ! run go test -count=1 -timeout 30m -v -run "$cases" ./internal/core/ >"$log" 2>&1 \
  || ! run go test -count=1 -timeout 20m -v ./internal/editor/ ./internal/doctender/ >>"$log" 2>&1; then
  cat "$log"
  echo "core image cases failed" >&2
  exit 1
fi
cat "$log"
if grep -E -- '--- SKIP' "$log"; then
  echo "a core image case was skipped" >&2
  exit 1
fi
for name in TestDocumentsLawCraftEditor TestDocumentsDocTenderCorpus TestDocumentsDocTenderTokensAndMCP TestDocumentsScreens TestBrowserEditPageE2E; do
  grep -q -- "--- PASS: $name " "$log" || { echo "$name did not pass" >&2; exit 1; }
done
test -s "$qc/qc.json" || { echo "visual QC wrote no qc.json" >&2; exit 1; }
if [ -n "${TOGAVSU_QC_OUT:-}" ]; then
  mkdir -p "$TOGAVSU_QC_OUT" && cp -R "$qc"/. "$TOGAVSU_QC_OUT"/
fi
echo "core image cases passed"

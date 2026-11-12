#!/bin/sh
# Full acceptance (release assignment): runs every check behind all 52 PRD 12 criteria
# and both release gates through scripts/release/full_acceptance.py, then prints its
# results. It exits non-zero while any criterion or gate is pending, so a job that runs
# it never turns unexecuted acceptance into a pass. Needs Docker, the core image's
# wordcraft-cli, and two Postgres 17 databases (TOGAVSU_TEST_DATABASE_URL and
# TOGAVSU_TEST_DATABASE_URL_B) with TOGAVSU_PG_DUMP and TOGAVSU_PSQL for the backup
# checks. TOGAVSU_ACCEPTANCE_OUT names the results directory.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
out=${TOGAVSU_ACCEPTANCE_OUT:-$(mktemp -d)}
if [ -z "${TOGAVSU_WORDCRAFT_CLI:-}" ]; then
  docker image inspect togavsu-core:local >/dev/null 2>&1 || sh scripts/ci/build-core-image.sh
  tools=$(mktemp -d)
  cid=$(docker create togavsu-core:local)
  docker cp "$cid:/usr/local/bin/wordcraft-cli" "$tools/wordcraft-cli"
  docker cp "$cid:/usr/share/togavsu/editor" "$tools/editor"
  docker rm "$cid" >/dev/null
  export TOGAVSU_WORDCRAFT_CLI="$tools/wordcraft-cli" TOGAVSU_REQUIRE_WORDCRAFT_CLI=1 TOGAVSU_EDITOR_DIST="$tools/editor"
fi
status=0
python3 scripts/release/full_acceptance.py --out "$out" "$@" || status=$?
echo "results: $out/results.json"
exit "$status"

#!/bin/sh
# AC-016 load test: tests/update/load.sh swaps the core under load and refuses bad images
# on a real Docker engine. A missing script, core sources or Docker engine fails this
# step; it is never reported as passed.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ ! -x "$root/tests/update/load.sh" ] || [ ! -d "$root/cmd/core" ]; then
  echo "FAIL AC-016: tests/update/load.sh or cmd/core is missing" >&2
  exit 1
fi
echo "running tests/update/load.sh"
exec "$root/tests/update/load.sh"

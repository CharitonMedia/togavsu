#!/bin/sh
# AC-016 load test wiring. A green scaffold run is not a passing load test.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ -x "$root/tests/update/load.sh" ] && [ -d "$root/cmd/core" ]; then
  echo "running tests/update/load.sh"
  exec "$root/tests/update/load.sh"
fi
echo "PENDING AC-016: zero-downtime load test waits for two signed core images"
exit 0

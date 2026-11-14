#!/bin/sh
# UI QC: runs the browser visual QC in tests/ui/run.sh (every shell screen and admin tab
# per role, and the module screens). It fails closed: a missing harness is a failure,
# never a pending pass.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ ! -f "$root/tests/ui/run.sh" ]; then
  echo "UI QC harness tests/ui/run.sh is missing" >&2
  exit 1
fi
exec sh "$root/tests/ui/run.sh"

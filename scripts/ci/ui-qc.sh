#!/bin/sh
# Wire UI QC. The harness belongs to the QA assignment. This script runs it
# when it exists and records a pending result until the product has pages.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ -x "$root/tests/ui/run.sh" ]; then
  echo "running tests/ui/run.sh"
  exec "$root/tests/ui/run.sh"
fi
if [ -f "$root/docs/testing/ui-qc.md" ]; then
  echo "PENDING UI QC: docs/testing/ui-qc.md is present and tests/ui/run.sh is not executable"
  exit 0
fi
echo "PENDING UI QC: no product pages in the platform assignment; AC-001 first-run is not claimed"
exit 0

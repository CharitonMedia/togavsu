#!/bin/sh
# Fail if this branch claims product acceptance for criteria that are still pending.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
doc="$root/docs/platform/pending.md"
test -f "$doc"
for id in AC-001 AC-016 AC-018; do
  grep -q "$id" "$doc"
done
grep -q "pending" "$doc"
if grep -E -i -q "acceptance passed|AC-001 passed|AC-016 passed|AC-018 passed" "$doc" "$root/docs/platform/README.md"; then
  echo "platform docs claim acceptance that is still pending" >&2
  exit 1
fi
echo "pending gate: AC-001 AC-016 AC-018 remain pending"

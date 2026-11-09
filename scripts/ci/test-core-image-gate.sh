#!/bin/sh
# The core image build is pending only while its prerequisites are absent.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if ! DOCKER=/bin/false TOGAVSU_ROOT="$work" sh "$root/scripts/ci/build-core-image.sh" | grep -q "PENDING core image"; then
  echo "missing prerequisites should stay pending" >&2
  exit 1
fi
mkdir -p "$work/cmd/core"
: > "$work/go.mod"
if DOCKER=/bin/false TOGAVSU_ROOT="$work" sh "$root/scripts/ci/build-core-image.sh"; then
  echo "a present core tree must fail when the image build fails" >&2
  exit 1
fi
echo "core image gate passed"

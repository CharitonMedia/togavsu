#!/bin/sh
# Build the core image when its sources exist. A missing go.mod or cmd/core
# is the contracts assignment, not a failed compile. Any other build error fails.
set -eu
root=${TOGAVSU_ROOT:-$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)}
docker=${DOCKER:-docker}
if [ ! -f "$root/go.mod" ] || [ ! -d "$root/cmd/core" ]; then
  echo "PENDING core image: go.mod or cmd/core is absent"
  exit 0
fi
exec "$docker" build -f "$root/deploy/core.Dockerfile" -t togavsu-core:local "$root"

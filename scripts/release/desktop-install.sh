#!/bin/sh
# Release gate and AC-001 on Docker Desktop (PRD 12; WORKFLOW.md "Final acceptance"): a
# clean install of the tested revision, from a fresh clone with no edited files, reaches
# first-run setup, the first admin and required two-factor in one Compose up, then runs
# the same checks as scripts/ci/test-compose-first-run.sh.
#
# It passes only on Docker Desktop. On any other engine (a Linux CI runner's Docker
# Engine, Colima, Podman) it prints PENDING and exits 3, because Linux Compose CI does not
# verify Windows or macOS Docker Desktop. Run it on a clean Windows or macOS machine with
# Docker Desktop and nothing from an earlier Togavsu install:
#   git clone https://github.com/CharitonMedia/togavsu && cd togavsu && git checkout <tag>
#   sh scripts/release/desktop-install.sh
# On Windows run it from Git Bash or WSL against Docker Desktop. Synthetic data only.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
os=$("$docker" info --format '{{.OperatingSystem}}' 2>/dev/null || true)
case "$os" in
  *"Docker Desktop"*) ;;
  *)
    echo "PENDING AC-001 Docker Desktop: this engine is '${os:-unreachable}', not Docker Desktop"
    exit 3
    ;;
esac
rev=$(git -C "$root" rev-parse HEAD)
if [ -n "$(git -C "$root" status --porcelain --untracked-files=no)" ]; then
  echo "the checkout has local edits; a clean install runs an unedited revision" >&2
  exit 1
fi
project=togavsu-desktop
if "$docker" volume ls -q | grep -q "^${project}_"; then
  echo "volumes from an earlier $project install exist; a clean install starts without them" >&2
  exit 1
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git clone -q "$root" "$work/togavsu"
git -C "$work/togavsu" checkout -q "$rev"
echo "Docker Desktop: $os; revision $rev; $( "$docker" version --format '{{.Server.Version}}')"
TOGAVSU_CI_PROJECT=$project sh "$work/togavsu/scripts/ci/test-compose-first-run.sh"
echo "desktop install passed on $os at $rev"

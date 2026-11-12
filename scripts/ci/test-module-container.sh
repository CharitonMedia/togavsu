#!/bin/sh
# Module container integration (PRD 6, 9.3): builds the Matter Clock sample image,
# publishes it to a throwaway local registry so it has a registry digest, drops the
# local copy so the supervisor really pulls by digest, then runs
# TestModuleContainerOnDocker: core supervisor client -> supervisor API -> Docker.
# Needs a Docker engine; uses synthetic secrets only.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
port=${TOGAVSU_IT_REGISTRY_PORT:-5055}
reg=togavsu-it-registry-$$
net=togavsu-it-net-$$
repo=localhost:$port/togavsu-matter-clock
cleanup() {
  docker ps -aq --filter label=togavsu.module=matter-clock | xargs -r docker rm -f >/dev/null 2>&1 || true
  docker rm -f "$reg" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create "$net" >/dev/null
docker run -d --name "$reg" -p "127.0.0.1:$port:5000" registry:2 >/dev/null
docker pull -q busybox:1.36 >/dev/null
docker build -q -f examples/matter-clock/Dockerfile -t "$repo:it" . >/dev/null
i=0
until docker push -q "$repo:it" >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 20 ] || { echo "registry did not accept the push" >&2; exit 1; }
  sleep 1
done
digest=$(docker image inspect "$repo:it" --format '{{range .RepoDigests}}{{println .}}{{end}}' | grep "^$repo@" | head -n1 | cut -d@ -f2)
[ -n "$digest" ] || { echo "no registry digest for $repo:it" >&2; exit 1; }
docker image rm -f "$repo:it" "$repo@$digest" >/dev/null 2>&1 || true
TOGAVSU_MODULE_IT_IMAGE=$repo TOGAVSU_MODULE_IT_DIGEST=$digest TOGAVSU_MODULE_IT_NETWORK=$net \
  go test -count=1 -v -run '^TestModuleContainerOnDocker$' ./internal/modules/

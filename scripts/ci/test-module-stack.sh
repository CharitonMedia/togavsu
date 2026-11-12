#!/bin/sh
# AC-012 on the real Compose stack: the Matter Clock image is pushed to a throwaway local
# registry, its package is signed with a throwaway key for that exact digest, and on a
# fresh install the admin trusts the key, installs the package from a local file, approves
# its permissions, sees its widget and tab served by the supervisor-run container through
# the core, has a time entry's event delivered to the container, and uninstalls it. The
# core container is the same one, never restarted, from first-run setup to uninstall.
# Needs Docker. Synthetic data only; everything it creates is removed on exit.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"
docker=${DOCKER:-docker}
project=${TOGAVSU_MODSTACK_PROJECT:-togavsu-modstack}
port=${TOGAVSU_MODSTACK_HTTPS_PORT:-38443}
regport=${TOGAVSU_MODSTACK_REGISTRY_PORT:-5057}
base=https://localhost:$port
reg=togavsu-modstack-registry-$$
repo=localhost:$regport/togavsu-matter-clock
work=$(mktemp -d)
export COMPOSE_PROJECT_NAME=$project
cat > "$work/override.yaml" <<YAML
services:
  caddy:
    ports: !override
      - "127.0.0.1:$((port - 363)):80"
      - "127.0.0.1:$port:443"
YAML
# TOGAVSU_CORE_IMAGE runs a core image built under another tag (default togavsu-core:local).
if [ -n "${TOGAVSU_CORE_IMAGE:-}" ]; then
  cat >> "$work/override.yaml" <<YAML
  core:
    image: $TOGAVSU_CORE_IMAGE
  migrate:
    image: $TOGAVSU_CORE_IMAGE
YAML
fi
compose() { "$docker" compose -p "$project" -f "$root/compose.yaml" -f "$work/override.yaml" "$@"; }
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    compose ps -a || true
    compose logs --no-color --tail 60 core supervisor || true
    "$docker" ps -a --filter label=togavsu.module=matter-clock --filter network=${project}_default || true
  fi
  "$docker" ps -aq --filter label=togavsu.module=matter-clock --filter network=${project}_default | xargs -r "$docker" rm -f >/dev/null 2>&1 || true
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  "$docker" rm -f "$reg" >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT INT TERM

"$docker" run -d --name "$reg" -p "127.0.0.1:$regport:5000" registry:2 >/dev/null
"$docker" build -q -f examples/matter-clock/Dockerfile -t "$repo:stack" . >/dev/null
i=0
until "$docker" push -q "$repo:stack" >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 20 ] || { echo "registry did not accept the push" >&2; exit 1; }
  sleep 1
done
digest=$("$docker" image inspect "$repo:stack" --format '{{range .RepoDigests}}{{println .}}{{end}}' | grep "^$repo@" | head -n1 | cut -d@ -f2)
[ -n "$digest" ] || { echo "no registry digest for $repo:stack" >&2; exit 1; }
"$docker" image rm -f "$repo:stack" >/dev/null 2>&1 || true
go run ./tests/e2e/modpack -image "$repo" -digest "$digest" -out "$work/matter-clock.signed.json" -pub "$work/key.pem"

compose down -v --remove-orphans >/dev/null 2>&1 || true
TOGAVSU_DOMAIN=localhost compose up -d --wait --wait-timeout 300
i=0
until curl -skf "$base/api/v1/health" >/dev/null; do
  i=$((i + 1)); [ "$i" -lt 90 ] || { echo "core did not answer $base" >&2; exit 1; }; sleep 1
done
core=$(compose ps -q core)
started=$("$docker" inspect -f '{{.State.StartedAt}} {{.RestartCount}}' "$core")
python3 -m tests.e2e.first_run setup "$base" "$work/state.json"
python3 -m tests.e2e.module_stack "$base" "$work/state.json" "$work/matter-clock.signed.json" "$work/key.pem" &
driver=$!
# While the driver waits for the widget, the module's container must appear, run the
# pushed digest and sit on the stack's network.
i=0
until [ -n "$("$docker" ps -q --filter label=togavsu.module=matter-clock --filter network=${project}_default)" ]; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then echo "no matter-clock container started" >&2; kill "$driver" 2>/dev/null || true; exit 1; fi
  sleep 1
done
mod=$("$docker" ps -q --filter label=togavsu.module=matter-clock --filter network=${project}_default | head -n1)
image=$("$docker" inspect -f '{{.Config.Image}}' "$mod")
case "$image" in
  *"$digest"*) ;;
  *) echo "module container runs $image, not $digest" >&2; exit 1 ;;
esac
"$docker" inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' "$mod" | grep -q "${project}_default" || {
  echo "module container is not on ${project}_default" >&2; exit 1; }
echo "supervisor started the module container $mod from $image"
wait "$driver"
i=0
while [ -n "$("$docker" ps -aq --filter label=togavsu.module=matter-clock --filter network=${project}_default)" ]; do
  i=$((i + 1)); [ "$i" -lt 60 ] || { echo "the module container stayed after uninstall" >&2; exit 1; }; sleep 1
done
after=$("$docker" inspect -f '{{.State.StartedAt}} {{.RestartCount}}' "$core")
[ "$(compose ps -q core)" = "$core" ] && [ "$after" = "$started" ] || {
  echo "the core container changed or restarted: $started -> $after" >&2; exit 1; }
schema=$(compose exec -T postgres psql -U togavsu -d togavsu -Atc "SELECT count(*) FROM pg_namespace WHERE nspname = 'mod_matter_clock'")
[ "$schema" = "1" ] || { echo "uninstall with keep dropped the module schema" >&2; exit 1; }
echo "module stack test passed: installed, framed, evented and uninstalled on one running core ($started)"

#!/bin/sh
# AC-016 (PRD 6 "Updates without restarts", PRD 12): an update swaps the core with no
# failed requests during a load test, and a bad image (failed signature or failed health
# check) is refused while the old version keeps serving.
#
# On this Docker host, with synthetic data only:
#  1. builds core 1.0.0 and 2.0.0 from deploy/core.Dockerfile, the supervisor image, and
#     three bad 3.0.x images (unsigned, signed with the wrong key, failing health check);
#  2. brings up the real compose.yaml stack (own project name, Caddy on 127.0.0.1 high
#     ports) plus a local registry:2 and a release-index file server that share the
#     supervisor's network namespace, and completes first-run setup over HTTPS;
#  3. pushes and signs the images with a throwaway cosign key pair (no transparency log),
#     installs the public key at the supervisor's COSIGN_KEY;
#  4. runs tests/update/loadgen through Caddy while asking the supervisor API for an
#     update to 2.0.0, then to each bad image, and checks zero failed requests, the
#     served version, the refusals and the update history.
# Everything it creates is removed on exit. Exit 0 only when every check passed.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
project=${TOGAVSU_UPD_PROJECT:-upd-load}
fail() {
  echo "FAIL AC-016: $*" >&2
  exit 1
}

for tool in "$docker" go python3 curl sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || fail "prerequisite missing: $tool"
done
# Host ports are free 127.0.0.1 ports unless set; the engine is shared with other stacks.
free_port() { python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])'; }
http_port=${TOGAVSU_UPD_HTTP_PORT:-$(free_port)}
https_port=${TOGAVSU_UPD_HTTPS_PORT:-$(free_port)}
sup_port=${TOGAVSU_UPD_SUPERVISOR_PORT:-$(free_port)}
reg_port=${TOGAVSU_UPD_REGISTRY_PORT:-$(free_port)}
idx_port=${TOGAVSU_UPD_INDEX_PORT:-15001}
base="https://localhost:$https_port"
sup="http://127.0.0.1:$sup_port"
reg="127.0.0.1:$reg_port"
cosign_version=v2.4.1
cosign_sha256_amd64=8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b
cosign_sha256_arm64=3b2e2e3854d0356c45fe6607047526ccd04742d20bd44afb5be91fa2a6e7cb4a
COMPOSE_PROJECT_NAME=$project
export COMPOSE_PROJECT_NAME

"$docker" info >/dev/null 2>&1 || fail "prerequisite missing: a reachable Docker engine"
"$docker" compose version >/dev/null 2>&1 || fail "prerequisite missing: docker compose"

work=$(mktemp -d)
mkdir -p "$work/bin" "$work/keys" "$work/index" "$work/dl"
chmod 755 "$work" "$work/index"
loadpid=
images=
compose() { "$docker" compose -p "$project" -f "$root/compose.yaml" -f "$work/override.yaml" "$@"; }
cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ -n "$loadpid" ]; then
    kill -TERM "$loadpid" 2>/dev/null || true
    wait "$loadpid" 2>/dev/null || true
  fi
  if [ "$status" -ne 0 ] && [ -f "$work/override.yaml" ]; then
    compose ps -a || true
    compose logs --no-color --tail 60 supervisor core caddy || true
  fi
  # Cores the supervisor started are not Compose services; remove them by network.
  for id in $("$docker" ps -aq --filter "network=${project}_default" --filter label=togavsu.role=core 2>/dev/null); do
    "$docker" rm -f "$id" >/dev/null 2>&1 || true
  done
  if [ -f "$work/override.yaml" ]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  "$docker" rm -f upd-keyinstall >/dev/null 2>&1 || true
  if [ "${TOGAVSU_UPD_KEEP_IMAGES:-0}" != "1" ]; then
    for img in $images; do
      "$docker" image rm -f "$img" >/dev/null 2>&1 || true
    done
  fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

json() { python3 -I -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {'d': d}))" "$1"; }

# --- cosign: the installed binary, or the pinned release checked against its SHA-256.
if command -v cosign >/dev/null 2>&1; then
  cosign=$(command -v cosign)
else
  case $(uname -m) in
    x86_64|amd64) arch=amd64 sum=$cosign_sha256_amd64 ;;
    aarch64|arm64) arch=arm64 sum=$cosign_sha256_arm64 ;;
    *) fail "prerequisite missing: no pinned cosign for $(uname -m)" ;;
  esac
  curl -fsSL -o "$work/dl/cosign" "https://github.com/sigstore/cosign/releases/download/$cosign_version/cosign-linux-$arch" \
    || fail "prerequisite missing: cosign $cosign_version download"
  echo "$sum  $work/dl/cosign" | sha256sum -c - >/dev/null || fail "cosign download failed its SHA-256 check"
  mv "$work/dl/cosign" "$work/bin/cosign"
  chmod 755 "$work/bin/cosign"
  cosign=$work/bin/cosign
fi
echo "cosign: $("$cosign" version 2>/dev/null | sed -n 's/^GitVersion:[[:space:]]*//p')"

# --- images. 1.0.0 and 2.0.0 are real builds of deploy/core.Dockerfile.
echo "building core 1.0.0, core 2.0.0 and the supervisor image"
"$docker" build -q -f "$root/deploy/core.Dockerfile" --build-arg TOGAVSU_VERSION=1.0.0 -t upd-core:1.0.0 "$root" >/dev/null
images="$images upd-core:1.0.0"
"$docker" build -q -f "$root/deploy/core.Dockerfile" --build-arg TOGAVSU_VERSION=2.0.0 -t upd-core:2.0.0 "$root" >/dev/null
images="$images upd-core:2.0.0"
"$docker" build -q -f "$root/Dockerfile" -t upd-supervisor:local "$root" >/dev/null
images="$images upd-supervisor:local"
thin() { # tag, then Dockerfile lines after FROM upd-core:2.0.0
  tag=$1
  shift
  printf 'FROM upd-core:2.0.0\n%s\n' "$*" | "$docker" build -q -t "$tag" - >/dev/null
  images="$images $tag"
}
thin upd-core:3.0.0-unsigned 'LABEL togavsu.version=3.0.0'
thin upd-core:3.0.1-wrongkey 'LABEL togavsu.version=3.0.1'
thin upd-core:3.0.2-badhealth 'LABEL togavsu.version=3.0.2
HEALTHCHECK --interval=1s --timeout=2s --start-period=0s --retries=2 CMD ["/bin/sh", "-c", "exit 1"]'

# --- stack. The registry and the release index join the supervisor's network namespace,
# so the supervisor (cosign) and the host engine (docker pull) both reach 127.0.0.1:$reg_port.
printf '{"image":"","version":""}\n' > "$work/index/index.json"
cat > "$work/override.yaml" <<EOF
services:
  caddy:
    ports: !override
      - "127.0.0.1:$http_port:80"
      - "127.0.0.1:$https_port:443"
  core:
    image: upd-core:1.0.0
  supervisor:
    image: upd-supervisor:local
    ports:
      - "127.0.0.1:$sup_port:8081"
      - "127.0.0.1:$reg_port:$reg_port"
    environment:
      TOGAVSU_RELEASE_INDEX: http://127.0.0.1:$idx_port/index.json
      COSIGN_VERIFY_FLAGS: "--insecure-ignore-tlog=true --allow-http-registry=true"
  registry:
    image: registry:2
    network_mode: "service:supervisor"
    environment:
      REGISTRY_HTTP_ADDR: ":$reg_port"
    depends_on:
      supervisor:
        condition: service_healthy
  releaseindex:
    image: caddy:2-alpine
    network_mode: "service:supervisor"
    command: ["caddy", "file-server", "--listen", ":$idx_port", "--root", "/srv"]
    volumes:
      - $work/index:/srv:ro
    depends_on:
      supervisor:
        condition: service_healthy
EOF
compose down -v --remove-orphans >/dev/null 2>&1 || true
echo "starting compose project $project"
compose up -d --no-build --wait --wait-timeout 300

i=0
until curl -skf "$base/api/v1/health" >/dev/null; do
  i=$((i + 1))
  [ "$i" -le 90 ] || fail "core did not answer $base/api/v1/health"
  sleep 1
done
(cd "$root" && python3 -m tests.e2e.first_run setup "$base" "$work/state.json")

version() { curl -skf --max-time 10 "$base/api/v1/health" 2>/dev/null | json 'd["version"]' 2>/dev/null || echo unreachable; }
v_before=$(version)
[ "$v_before" = "1.0.0" ] || fail "compose core reports version '$v_before', want 1.0.0"

# --- keys, push, sign. The keys live only in $work.
pass=$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')
(cd "$work/keys" && COSIGN_PASSWORD=$pass "$cosign" generate-key-pair >/dev/null 2>&1) || fail "cosign generate-key-pair"
(cd "$work/keys" && COSIGN_PASSWORD=$pass "$cosign" generate-key-pair --output-key-prefix wrong >/dev/null 2>&1) || fail "cosign generate-key-pair (wrong key)"
"$docker" run --rm --name upd-keyinstall -v "${project}_appsecrets:/appsecrets" -v "$work/keys/cosign.pub:/in/cosign.pub:ro" \
  --entrypoint /bin/sh upd-supervisor:local -c 'cp /in/cosign.pub /appsecrets/cosign.pub && chmod 644 /appsecrets/cosign.pub'

push() { # local tag, repository tag -> prints name@digest
  "$docker" tag "$1" "$reg/togavsu-core:$2"
  images="$images $reg/togavsu-core:$2"
  digest=$("$docker" push "$reg/togavsu-core:$2" | sed -n 's/.*digest: \(sha256:[0-9a-f]\{64\}\).*/\1/p' | tail -n 1)
  [ -n "$digest" ] || fail "push of $2 printed no digest"
  echo "$reg/togavsu-core@$digest"
}
sign() { # key file, image
  COSIGN_PASSWORD=$pass "$cosign" sign --yes --key "$1" --tlog-upload=false --allow-http-registry=true "$2" >/dev/null 2>&1 \
    || fail "cosign sign $2"
}
img_v2=$(push upd-core:2.0.0 2.0.0)
img_unsigned=$(push upd-core:3.0.0-unsigned 3.0.0)
img_wrongkey=$(push upd-core:3.0.1-wrongkey 3.0.1)
img_badhealth=$(push upd-core:3.0.2-badhealth 3.0.2)
sign "$work/keys/cosign.key" "$img_v2"
sign "$work/keys/wrong.key" "$img_wrongkey"
sign "$work/keys/cosign.key" "$img_badhealth"
"$cosign" verify --key "$work/keys/cosign.pub" --insecure-ignore-tlog=true --allow-http-registry=true "$img_v2" >/dev/null 2>&1 \
  || fail "the signed 2.0.0 image does not verify on the host"
images="$images $img_v2 $img_unsigned $img_wrongkey $img_badhealth"
echo "pushed and signed: $img_v2"

token=$(compose exec -T supervisor cat /appsecrets/supervisor_token)
[ -n "$token" ] || fail "supervisor token is empty"

release() { # image version
  printf '{"image":"%s","version":"%s"}\n' "$1" "$2" > "$work/index/index.json.tmp"
  mv "$work/index/index.json.tmp" "$work/index/index.json"
}
update_now() { # -> writes $work/update.json, prints the HTTP status
  # 409 operation_in_progress is the supervisor's gate held by another job (the
  # scheduler's catch-up backup after first-run); the attempt is retried, not counted.
  tries=0
  while :; do
    code=$(curl -s -o "$work/update.json" -w '%{http_code}' --max-time 600 -X POST \
      -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -d '{"force":true}' "$sup/v1/updates" || true)
    tries=$((tries + 1))
    if [ "$code" = "409" ] && grep -q '"operation_in_progress"' "$work/update.json" && [ "$tries" -lt 60 ]; then
      sleep 2
      continue
    fi
    echo "$code"
    return 0
  done
}
start_load() { # summary file
  "$work/bin/loadgen" -base "$base" -state "$work/state.json" -workers 8 > "$1" 2> "$1.err" &
  loadpid=$!
  sleep 5
  kill -0 "$loadpid" 2>/dev/null || fail "load generator exited early: $(cat "$1" "$1.err")"
}
stop_load() { # summary file -> checks zero failures
  kill -TERM "$loadpid"
  rc=0
  wait "$loadpid" || rc=$?
  loadpid=
  echo "load: $(cat "$1")"
  [ "$rc" -eq 0 ] || fail "load generator saw failures (exit $rc): $(cat "$1" "$1.err")"
  [ "$(json 'd["failed"]' < "$1")" = "0" ] || fail "failed requests: $(cat "$1")"
}
(cd "$root" && go build -o "$work/bin/loadgen" ./tests/update/loadgen)

# --- 1.0.0 -> 2.0.0 under load.
release "$img_v2" 2.0.0
start_load "$work/load-swap.json"
code=$(update_now)
echo "update to 2.0.0: HTTP $code $(cat "$work/update.json")"
sleep 5
stop_load "$work/load-swap.json"
[ "$code" = "200" ] || fail "update to 2.0.0 answered $code"
[ "$(json 'd["status"]' < "$work/update.json")" = "applied" ] || fail "update to 2.0.0 was not applied"
v_after=$(version)
[ "$v_after" = "2.0.0" ] || fail "after the update the core reports '$v_after', want 2.0.0"
seen=$(json '",".join(sorted(d["versions"]))' < "$work/load-swap.json")
[ "$seen" = "1.0.0,2.0.0" ] || fail "load saw versions '$seen', want 1.0.0 and 2.0.0"
echo "swap passed: $v_before -> $v_after with zero failed requests (versions seen under load: $seen)"

# --- bad images under load: each is refused and 2.0.0 keeps serving.
start_load "$work/load-bad.json"
refuse() { # image version reason label
  release "$1" "$2"
  code=$(update_now)
  echo "update to $2 ($4): HTTP $code $(cat "$work/update.json")"
  [ "$code" != "200" ] || fail "update to $4 image answered 200"
  got=$(json 'd.get("result", {}).get("reason", "")' < "$work/update.json")
  [ "$got" = "$3" ] || fail "update to $4 image refused with reason '$got', want $3"
  now=$(version)
  [ "$now" = "2.0.0" ] || fail "after the $4 image the core reports '$now', want 2.0.0"
}
refuse "$img_unsigned" 3.0.0 signature unsigned
refuse "$img_wrongkey" 3.0.1 signature wrong-key
refuse "$img_badhealth" 3.0.2 health failing-health-check
sleep 3
stop_load "$work/load-bad.json"
seen=$(json '",".join(sorted(d["versions"]))' < "$work/load-bad.json")
[ "$seen" = "2.0.0" ] || fail "during refusals the load saw versions '$seen', want only 2.0.0"
cores=$("$docker" ps -aq --filter "network=${project}_default" --filter label=togavsu.role=core | wc -l | tr -d ' ')
[ "$cores" = "1" ] || fail "$cores core containers on the network after the refusals, want 1"

# --- update history (GET /v1/updates).
curl -sf -H "Authorization: Bearer $token" "$sup/v1/updates" > "$work/history.json" || fail "GET /v1/updates"
python3 -I - "$work/history.json" "$img_v2" "$img_unsigned" "$img_wrongkey" "$img_badhealth" <<'EOF' || fail "update history is missing an entry"
import json, sys
events = json.load(open(sys.argv[1]))["updates"]
fields = [e.get("fields", e.get("Fields", {})) for e in events]
v2, unsigned, wrong, bad = sys.argv[2:]
want = [("applied", None, v2, "to"), ("refused", "signature", unsigned, "image"),
        ("refused", "signature", wrong, "image"), ("refused", "health", bad, "image")]
for status, reason, image, key in want:
    hit = [f for f in fields if f.get("status") == status and f.get(key) == image and (reason is None or f.get("reason") == reason)]
    if not hit:
        sys.exit("no %s %s entry for %s in %s" % (status, reason or "", image, fields))
print("update history: %d entries; applied 2.0.0, refused unsigned, wrong-key (signature) and failing health check" % len(fields))
EOF
echo "PASS AC-016: zero failed requests across the 1.0.0 -> 2.0.0 swap and three refused bad images; 2.0.0 kept serving"

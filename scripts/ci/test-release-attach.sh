#!/bin/sh
# scripts/release/attach-signature.sh, the attach-signature job of the proposed release
# workflow, against a local registry:2 with throwaway cosign keys made here (never the
# release key, which is kept off GitHub). It must refuse a digest the tag did not record,
# a payload naming another digest, and a signature the release key did not make, attach
# nothing when it refuses, and attach a good signature that cosign verify --key then
# accepts. Synthetic images only; everything is removed on exit.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
docker=${DOCKER:-docker}
fail() {
  echo "FAIL release attach: $*" >&2
  exit 1
}
for tool in "$docker" jq curl sha256sum python3; do
  command -v "$tool" >/dev/null 2>&1 || fail "prerequisite missing: $tool"
done
"$docker" info >/dev/null 2>&1 || fail "prerequisite missing: a reachable Docker engine"
work=$(mktemp -d)
regname=togavsu-attach-registry-$$
images=
cleanup() {
  status=$?
  "$docker" rm -f "$regname" >/dev/null 2>&1 || true
  for img in $images; do "$docker" image rm -f "$img" >/dev/null 2>&1 || true; done
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

cosign_version=v2.4.1
if command -v cosign >/dev/null 2>&1; then
  cosign=$(command -v cosign)
else
  case $(uname -m) in
    x86_64|amd64) arch=amd64 sum=8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b ;;
    aarch64|arm64) arch=arm64 sum=3b2e2e3854d0356c45fe6607047526ccd04742d20bd44afb5be91fa2a6e7cb4a ;;
    *) fail "no pinned cosign for $(uname -m)" ;;
  esac
  curl -fsSL -o "$work/cosign" "https://github.com/sigstore/cosign/releases/download/$cosign_version/cosign-linux-$arch" || fail "cosign download"
  echo "$sum  $work/cosign" | sha256sum -c - >/dev/null || fail "cosign download failed its SHA-256 check"
  chmod 755 "$work/cosign"
  cosign=$work/cosign
fi

port=$(python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
"$docker" run -d --name "$regname" -p "127.0.0.1:$port:5000" registry:2 >/dev/null
reg=127.0.0.1:$port
i=0
until curl -sf "http://$reg/v2/" >/dev/null; do
  i=$((i + 1)); [ "$i" -le 30 ] || fail "registry did not start"; sleep 1
done
tag=v0.0.1-attachtest
push() { # image name, content -> prints the pushed digest
  mkdir -p "$work/ctx-$1"
  printf '%s\n' "$2" > "$work/ctx-$1/content"
  printf 'FROM scratch\nCOPY content /content\n' > "$work/ctx-$1/Dockerfile"
  "$docker" build -q -t "$reg/$1:$tag" "$work/ctx-$1" >/dev/null
  images="$images $reg/$1:$tag"
  "$docker" push "$reg/$1:$tag" | sed -n 's/.*digest: \(sha256:[0-9a-f]\{64\}\).*/\1/p' | tail -n 1
}
core=$(push togavsu-core "synthetic core $$")
sup=$(push togavsu-supervisor "synthetic supervisor $$")
stray=$(push togavsu-core-stray "not a recorded image $$")
for d in "$core" "$sup" "$stray"; do printf '%s' "$d" | grep -Eq '^sha256:[0-9a-f]{64}$' || fail "push printed no digest"; done
jq -n --arg tag "$tag" --arg reg "$reg" --arg core "$core" --arg sup "$sup" \
  '{tag: $tag, signed: false, transparency_log: false,
    images: [{name: "togavsu-core", ref: ($reg + "/togavsu-core@" + $core), digest: $core},
             {name: "togavsu-supervisor", ref: ($reg + "/togavsu-supervisor@" + $sup), digest: $sup}]}' > "$work/digests.json"

# Throwaway keys: "release" stands in for the release key, "other" is any other key.
pass=$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')
(cd "$work" && COSIGN_PASSWORD=$pass "$cosign" generate-key-pair --output-key-prefix release >/dev/null 2>&1) || fail "generate-key-pair"
(cd "$work" && COSIGN_PASSWORD=$pass "$cosign" generate-key-pair --output-key-prefix other >/dev/null 2>&1) || fail "generate-key-pair"
payload_for() { "$cosign" generate --allow-http-registry=true "$1" 2>/dev/null; }
sign_blob() { # key, payload file -> base64 signature, made the way the release key is used off GitHub
  COSIGN_PASSWORD=$pass "$cosign" sign-blob --yes --key "$1" --tlog-upload=false "$2" 2>/dev/null
}
payload_for "$reg/togavsu-core@$core" > "$work/core.payload"
payload_for "$reg/togavsu-core@$stray" > "$work/stray.payload"
good_sig=$(sign_blob "$work/release.key" "$work/core.payload")
other_sig=$(sign_blob "$work/other.key" "$work/core.payload")
stray_sig=$(sign_blob "$work/release.key" "$work/stray.payload")
[ -n "$good_sig" ] && [ -n "$other_sig" ] || fail "sign-blob printed no signature"

attach() { # digest, base64 signature, payload file -> exit code
  set +e
  TAG=$tag DIGEST=$1 SIGNATURE=$2 PAYLOAD=$(base64 -w 0 < "$3") DIGESTS_FILE=$work/digests.json REGISTRY=$reg \
    COSIGN_BIN=$cosign RELEASE_KEY=$work/release.pub COSIGN_REGISTRY_FLAGS=--allow-http-registry=true \
    sh "$root/scripts/release/attach-signature.sh" > "$work/attach.log" 2>&1
  code=$?
  set -e
  echo "$code"
}
verified() { "$cosign" verify --key "$2" --insecure-ignore-tlog=true --allow-http-registry=true "$1" >/dev/null 2>&1; }

[ "$(attach "$stray" "$stray_sig" "$work/stray.payload")" != 0 ] || fail "attached to a digest the tag did not record"
grep -q "not one of release" "$work/attach.log" || fail "unrecorded digest: $(cat "$work/attach.log")"
[ "$(attach "$core" "$stray_sig" "$work/stray.payload")" != 0 ] || fail "attached a payload that names another digest"
grep -q "the payload signs" "$work/attach.log" || fail "foreign payload: $(cat "$work/attach.log")"
[ "$(attach "$core" "$other_sig" "$work/core.payload")" != 0 ] || fail "attached a signature the release key did not make"
grep -q "does not verify" "$work/attach.log" || fail "wrong key: $(cat "$work/attach.log")"
[ "$(attach "$core" "not base64 !" "$work/core.payload")" != 0 ] || fail "attached a signature that is not base64"
if verified "$reg/togavsu-core@$core" "$work/release.pub" || verified "$reg/togavsu-core@$stray" "$work/release.pub"; then
  fail "a refused attach left a signature behind"
fi
echo "refused: an unrecorded digest, a foreign payload, a wrong-key signature, a non-base64 signature"

[ "$(attach "$core" "$good_sig" "$work/core.payload")" = 0 ] || fail "good signature refused: $(cat "$work/attach.log")"
verified "$reg/togavsu-core@$core" "$work/release.pub" || fail "cosign verify --key rejects the attached signature"
if verified "$reg/togavsu-core@$core" "$work/other.pub"; then fail "another key verifies the release image"; fi
if verified "$reg/togavsu-supervisor@$sup" "$work/release.pub"; then fail "the unsigned supervisor image verifies"; fi
# The committed release key did not make the throwaway signature.
if verified "$reg/togavsu-core@$core" "$root/deploy/release/cosign.pub"; then fail "the release key verifies a throwaway signature"; fi
echo "release attach test passed: the attached signature verifies with the key that made it, and only that key"

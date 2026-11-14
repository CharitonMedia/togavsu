#!/bin/sh
# Release gate (PRD 2, 3, 12): the release tag's core and supervisor images on
# ghcr.io/charitonmedia verify with cosign against the Togavsu release public key,
# deploy/release/cosign.pub, which the supervisor image also ships at
# /usr/share/togavsu/cosign.pub (compose.yaml COSIGN_KEY) and checks every update
# against.
#
# Signing, as decided on 2026-11-12: the release private key is kept off GitHub, on the
# integration lead's machine. No repository secret holds it; the release workflow
# (docs/release/proposed-ci/release.yml) only builds, pushes and records digests, and
# attaches signatures made elsewhere. Release signatures are not written to the public
# transparency log, so verification passes --insecure-ignore-tlog=true; that skips only
# the log lookup, never the signature check against the release key.
#
# It fails, never passes, when the key is missing, the revision carries no release tag,
# an image is missing or unsigned, or the supervisor image ships a different key.
# Nothing here can sign.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
key=$root/deploy/release/cosign.pub
registry=${TOGAVSU_RELEASE_REGISTRY:-ghcr.io/charitonmedia}
if [ ! -s "$key" ]; then
  echo "FAIL signed release: deploy/release/cosign.pub (the Togavsu release public key) is missing" >&2
  exit 1
fi
tag=${TOGAVSU_RELEASE_TAG:-$(git -C "$root" describe --tags --exact-match HEAD 2>/dev/null || true)}
if [ -z "$tag" ]; then
  echo "PENDING signed release: $(git -C "$root" rev-parse HEAD) carries no release tag; the integration lead publishes and signs it off GitHub"
  exit 3
fi
cosign=${COSIGN_BIN:-cosign}
docker=${DOCKER:-docker}
command -v "$cosign" >/dev/null 2>&1 || { echo "cosign is required" >&2; exit 1; }
command -v "$docker" >/dev/null 2>&1 || { echo "docker is required to check the key in the supervisor image" >&2; exit 1; }
for image in togavsu-core togavsu-supervisor; do
  ref=$registry/$image:$tag
  "$cosign" verify --key "$key" --insecure-ignore-tlog=true "$ref" >/dev/null
  echo "verified $ref"
done
shipped=$(mktemp)
trap 'rm -f "$shipped"' EXIT
"$docker" run --rm --entrypoint cat "$registry/togavsu-supervisor:$tag" /usr/share/togavsu/cosign.pub > "$shipped"
cmp -s "$shipped" "$key" || { echo "FAIL signed release: the $tag supervisor image ships a different cosign.pub" >&2; exit 1; }
echo "release $tag images verify against the release key, and the supervisor image ships that key"

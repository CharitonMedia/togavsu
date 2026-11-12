#!/bin/sh
# Release gate (PRD 2, 3, 12): the release tag's core and supervisor images on
# ghcr.io/charitonmedia verify with cosign against the Togavsu release public key that
# ships in the image (deploy/release/cosign.pub). It fails, never passes, when the key is
# not in the repository, the revision carries no release tag, or an image is missing or
# unsigned. Signing happens in the release workflow with the release private key held as
# a repository secret; nothing here can sign.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
key=$root/deploy/release/cosign.pub
registry=${TOGAVSU_RELEASE_REGISTRY:-ghcr.io/charitonmedia}
if [ ! -s "$key" ]; then
  echo "PENDING signed release: deploy/release/cosign.pub (the Togavsu release public key) is not in the repository"
  exit 3
fi
tag=${TOGAVSU_RELEASE_TAG:-$(git -C "$root" describe --tags --exact-match HEAD 2>/dev/null || true)}
if [ -z "$tag" ]; then
  echo "PENDING signed release: $(git -C "$root" rev-parse HEAD) carries no release tag"
  exit 3
fi
cosign=${COSIGN_BIN:-cosign}
command -v "$cosign" >/dev/null 2>&1 || { echo "cosign is required" >&2; exit 1; }
for image in togavsu-core togavsu-supervisor; do
  ref=$registry/$image:$tag
  "$cosign" verify --key "$key" "$ref" >/dev/null
  echo "verified $ref"
done
echo "release $tag images verify against the release key"

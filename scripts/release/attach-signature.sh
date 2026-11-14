#!/bin/sh
# Attach one release signature made off GitHub (docs/release/proposed-ci/release.yml,
# job attach-signature). The release private key never reaches GitHub and signatures
# are not in the public transparency log. Refuses, before anything is attached, unless:
#   - TAG is a release tag and DIGEST an image digest;
#   - DIGEST is one of the images DIGESTS_FILE (the tag's digests.json) records for TAG;
#   - PAYLOAD (base64) is a simple-signing payload naming DIGEST;
#   - SIGNATURE (base64) verifies over that payload with the release public key.
# Then it runs cosign attach signature and cosign verify --key on the image.
# Env: TAG DIGEST SIGNATURE PAYLOAD DIGESTS_FILE; optional REGISTRY, COSIGN_BIN,
# RELEASE_KEY, COSIGN_REGISTRY_FLAGS (tests: --allow-http-registry=true).
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
registry=${REGISTRY:-ghcr.io/charitonmedia}
cosign=${COSIGN_BIN:-cosign}
key=${RELEASE_KEY:-$root/deploy/release/cosign.pub}
flags=${COSIGN_REGISTRY_FLAGS:-}
refuse() {
  echo "attach-signature refused: $*" >&2
  exit 1
}
printf '%s' "${TAG:-}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$' || refuse "not a release tag: ${TAG:-}"
printf '%s' "${DIGEST:-}" | grep -Eq '^sha256:[0-9a-f]{64}$' || refuse "not an image digest: ${DIGEST:-}"
[ -s "${DIGESTS_FILE:-}" ] || refuse "no digests.json for $TAG"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf '%s' "${SIGNATURE:-}" | base64 -d > "$work/signature.raw" 2>/dev/null && [ -s "$work/signature.raw" ] || refuse "signature is not base64"
printf '%s' "$SIGNATURE" > "$work/signature.b64"
printf '%s' "${PAYLOAD:-}" | base64 -d > "$work/payload.json" 2>/dev/null && [ -s "$work/payload.json" ] || refuse "payload is not base64"

ref=$(jq -r --arg d "$DIGEST" --arg t "$TAG" 'select(.tag == $t) | .images[] | select(.digest == $d) | .ref' "$DIGESTS_FILE") \
  || refuse "digests.json does not parse"
[ -n "$ref" ] && [ "$(printf '%s\n' "$ref" | wc -l)" -eq 1 ] || refuse "$DIGEST is not one of release $TAG's recorded images"
case "$ref" in
  "$registry/togavsu-core@$DIGEST" | "$registry/togavsu-supervisor@$DIGEST") ;;
  *) refuse "recorded ref $ref is not a release image in $registry" ;;
esac
named=$(jq -r '.critical.image["docker-manifest-digest"] // empty' "$work/payload.json" 2>/dev/null || true)
[ "$named" = "$DIGEST" ] || refuse "the payload signs '${named}', not $DIGEST"

"$cosign" verify-blob --key "$key" --insecure-ignore-tlog=true --signature "$work/signature.b64" "$work/payload.json" >/dev/null 2>&1 \
  || refuse "the signature does not verify over the payload with the release key"
# shellcheck disable=SC2086 # flags are separate words
"$cosign" attach signature $flags --payload "$work/payload.json" --signature "$work/signature.b64" "$ref"
# shellcheck disable=SC2086
"$cosign" verify --key "$key" --insecure-ignore-tlog=true $flags "$ref" >/dev/null
echo "attached and verified the release signature on $ref"

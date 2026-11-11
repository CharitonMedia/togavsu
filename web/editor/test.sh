#!/bin/sh
# Runs the wrapper's native tests (caption.rs: New Pleading filled from the matter) against
# LawCraft's crates at the pinned commit, as build.sh places the wrapper: in the checkout as
# apps/togavsu-editor, with LawCraft's Cargo.lock restored afterwards.
#
# usage: web/editor/test.sh LAWCRAFT_CHECKOUT
set -eu

lawcraft=$(cd "$1" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

pin=$(sed -n 's/^[[:space:]]*LawCraftCommit[[:space:]]*=[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' "$repo/internal/domain/defaults.go")
head=$(git -C "$lawcraft" rev-parse HEAD)
if [ -z "$pin" ] || [ "$head" != "$pin" ]; then
	echo "test.sh: LawCraft checkout is at $head; the wrapper is pinned to ${pin:-<unknown>}" >&2
	exit 1
fi

app="$lawcraft/apps/togavsu-editor"
rm -rf "$app"
mkdir -p "$app/src"
cp "$here/Cargo.toml" "$here/Trunk.toml" "$here/index.html" "$app/"
cp "$here/src/main.rs" "$here/src/togavsu.rs" "$here/src/caption.rs" "$here/src/profiles.rs" "$app/src/"
cp "$lawcraft/apps/wordcraft-web/src/web.rs" "$app/src/web.rs"
patch --forward --batch --no-backup-if-mismatch -p1 -d "$app/src" < "$here/web.patch"
status=0
(cd "$app" && cargo test -p togavsu-editor) || status=$?
git -C "$lawcraft" checkout -- Cargo.lock
rm -rf "$app"
exit $status

#!/bin/sh
# Builds LawCraft for Togavsu (PRD 8.20) from a LawCraft checkout at the pinned commit:
#   1. wordcraft-cli, the headless build the core runs for editor runs and conversions;
#   2. the browser editor: this wrapper crate, placed in the checkout as
#      apps/togavsu-editor (so LawCraft's crates and Cargo.lock are used as they are at
#      that commit), built for WebAssembly with trunk.
# LawCraft's repository is not changed; only the build tree gains the wrapper.
#
# usage: web/editor/build.sh LAWCRAFT_CHECKOUT OUT_DIR
#   OUT_DIR/wordcraft-cli   the headless binary
#   OUT_DIR/editor/         trunk's dist folder, served by the core at /editor/
set -eu

lawcraft=$(cd "$1" && pwd)
mkdir -p "$2"
out=$(cd "$2" && pwd)
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../.." && pwd)

pin=$(sed -n 's/^[[:space:]]*LawCraftCommit[[:space:]]*=[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' "$repo/internal/domain/defaults.go")
head=$(git -C "$lawcraft" rev-parse HEAD)
if [ -z "$pin" ] || [ "$head" != "$pin" ]; then
	echo "build.sh: LawCraft checkout is at $head; this build is pinned to ${pin:-<unknown>}" >&2
	exit 1
fi
if [ -n "$(git -C "$lawcraft" status --porcelain --untracked-files=no)" ]; then
	echo "build.sh: the LawCraft checkout has local changes; build from the pinned commit as it is" >&2
	exit 1
fi

# The headless build, before the wrapper joins the workspace (apps/*), against LawCraft's
# own lock; a wrapper left by an earlier build is taken out first.
app="$lawcraft/apps/togavsu-editor"
rm -rf "$app"
(cd "$lawcraft" && cargo build --release --locked -p wordcraft-cli)
install -m 0755 "$lawcraft/target/release/wordcraft-cli" "$out/wordcraft-cli"

mkdir -p "$app/src"
cp "$here/Cargo.toml" "$here/Trunk.toml" "$here/index.html" "$app/"
cp "$here/src/main.rs" "$here/src/togavsu.rs" "$here/src/caption.rs" "$app/src/"
cp "$lawcraft/apps/wordcraft-web/src/web.rs" "$app/src/web.rs"
# A patch that no longer applies (the pin moved) stops the build rather than guessing.
patch --forward --batch --no-backup-if-mismatch -p1 -d "$app/src" < "$here/web.patch"

# The wrapper brings no crate of its own: its dependencies are LawCraft's, at the versions
# LawCraft's lock names. Cargo adds only the wrapper's own package entry to the lock.
(cd "$app" && trunk build --release)
git -C "$lawcraft" checkout -- Cargo.lock
rm -rf "$out/editor"
cp -R "$lawcraft/dist/togavsu-editor" "$out/editor"
test -f "$out/editor/index.html"
echo "build.sh: wordcraft-cli and the browser editor built from LawCraft $pin"

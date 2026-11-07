#!/bin/sh
# Run supervisor tests. Uses the repo module when contracts have published
# go.mod, and a temporary module with the same path until then.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
if [ -f "$root/go.mod" ]; then
  cd "$root"
  go test ./internal/supervisor/ ./cmd/supervisor/
  go test -race ./internal/supervisor/ ./cmd/supervisor/
  go build -o /dev/null ./cmd/supervisor
  exit 0
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/cmd" "$work/internal"
cp -a "$root/cmd/supervisor" "$work/cmd/supervisor"
cp -a "$root/internal/supervisor" "$work/internal/supervisor"
printf 'module github.com/CharitonMedia/togavsu\n\ngo 1.22\n' > "$work/go.mod"
cd "$work"
go test ./internal/supervisor/ ./cmd/supervisor/
go test -race ./internal/supervisor/ ./cmd/supervisor/
go build -o /dev/null ./cmd/supervisor

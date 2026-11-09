#!/bin/sh
# Run supervisor tests. Uses the repo module when contracts have published
# go.mod, and a temporary module with the same path until then.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

compile_arm64_supervisor() {
  out=$(mktemp)
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c -o "$out" ./internal/supervisor/
  CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/supervisor
  q=""
  if command -v qemu-aarch64-static >/dev/null 2>&1; then
    q=qemu-aarch64-static
  elif command -v qemu-aarch64 >/dev/null 2>&1; then
    q=qemu-aarch64
  fi
  if [ -n "$q" ]; then
    "$q" "$out" -test.count=1 -test.timeout 60s -test.run '^TestRenameat2Number$'
  else
    echo "arm64 supervisor tests compiled; qemu-aarch64 is absent so the renameat2 syscall was not executed"
  fi
  rm -f "$out"
}
if [ -f "$root/go.mod" ]; then
  cd "$root"
  go test ./internal/supervisor/ ./cmd/supervisor/
  go test -race ./internal/supervisor/ ./cmd/supervisor/
  go test -count=1 -timeout 360s -v ./internal/supervisor/ -run '^(TestPathOwnedTemp|TestPathTempRestoreStall|TestPathShareFallback|TestPathDisposeRestoresForeign|TestAbortedPathUpload|TestPathCleanStageCrash|TestPathSidecarCrash|TestPathQuarantineStage|TestPathPublishDurability|TestPathAbortProof|TestPathNoHardlink|TestRenameat2Number|TestPathPrejournalResidual)$'
  go build -o /dev/null ./cmd/supervisor
  compile_arm64_supervisor
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
go test -count=1 -timeout 360s -v ./internal/supervisor/ -run '^(TestPathOwnedTemp|TestPathTempRestoreStall|TestPathShareFallback|TestPathDisposeRestoresForeign|TestAbortedPathUpload|TestPathCleanStageCrash|TestPathSidecarCrash|TestPathQuarantineStage|TestPathPublishDurability|TestPathAbortProof|TestPathNoHardlink|TestRenameat2Number|TestPathPrejournalResidual)$'
go build -o /dev/null ./cmd/supervisor
compile_arm64_supervisor

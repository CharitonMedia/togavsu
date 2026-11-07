#!/bin/sh
# Read the upstream the supervisor recorded so a Caddy restart keeps the
# core that passed its health check.
set -eu
if [ -s /runtime/upstream ]; then
  CORE_UPSTREAM=$(tr -d ' \n\r' < /runtime/upstream)
  export CORE_UPSTREAM
fi
exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile

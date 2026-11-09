#!/bin/sh
# Read the upstream the supervisor recorded so a Caddy restart keeps the
# core that passed its health check.
set -eu
mkdir -p /run/caddy-admin
chmod 755 /run/caddy-admin
if [ -s /runtime/upstream ]; then
  CORE_UPSTREAM=$(tr -d ' \n\r' < /runtime/upstream)
  export CORE_UPSTREAM
fi
exec caddy run --config /etc/caddy/Caddyfile --adapter caddyfile

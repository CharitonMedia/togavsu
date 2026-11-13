#!/bin/sh
# Adapt installer secret files to the core bootstrap contract.
# The core reads only TOGAVSU_DATABASE_URL, TOGAVSU_ENCRYPTION_KEY,
# TOGAVSU_DOMAIN, and TOGAVSU_SUPERVISOR_SECRET. Compose and containers
# created by the supervisor use this same script.
#
# A 64-character hex field key is the earlier installer format. It is decoded
# to the same 32 bytes and exported as standard base64. The file is not rewritten.
#
# ponytail: hex field keys are still accepted, ceiling: two on-disk encodings,
# upgrade: reject hex once every volume has a canonical key, trigger: bootstrap
# no longer finds a 64-character hex field_key.
set -eu
secrets=${TOGAVSU_SECRETS_DIR:-/appsecrets}
bin=${CORE_BIN:-/usr/local/bin/core}

read_trim() {
  tr -d ' \t\n\r' < "$1"
}

if [ ! -r "$secrets/database_url" ] || [ ! -r "$secrets/field_key" ] || [ ! -r "$secrets/supervisor_token" ]; then
  echo "core entrypoint: secret files are not readable" >&2
  exit 2
fi

db=$(read_trim "$secrets/database_url")
token=$(read_trim "$secrets/supervisor_token")
key_text=$(read_trim "$secrets/field_key")
raw=$(mktemp)
trap 'rm -f "$raw"' EXIT
if printf '%s' "$key_text" | grep -Eq '^[0-9a-fA-F]{64}$'; then
  # GNU basenc --base16 rejects lowercase a-f. Earlier installers wrote that
  # form with od -tx1. The file itself is left unchanged.
  printf '%s' "$key_text" | tr '[:lower:]' '[:upper:]' | basenc --base16 -d > "$raw"
else
  printf '%s' "$key_text" | base64 -d > "$raw"
fi
size=$(wc -c < "$raw" | tr -d ' ')
if [ "$size" != "32" ]; then
  rm -f "$raw"
  echo "core entrypoint: encryption key must decode to 32 bytes" >&2
  exit 2
fi
key=$(base64 -w 0 < "$raw")
rm -f "$raw"
trap - EXIT

file_domain=
if [ -r "$secrets/domain" ]; then
  file_domain=$(read_trim "$secrets/domain")
fi
env_domain=${TOGAVSU_DOMAIN:-}
if [ -n "$file_domain" ] && [ -n "$env_domain" ] && [ "$file_domain" != "$env_domain" ]; then
  echo "core entrypoint: TOGAVSU_DOMAIN does not match the installed domain" >&2
  exit 2
fi
domain=$env_domain
if [ -z "$domain" ]; then
  domain=$file_domain
fi
if [ -z "$domain" ]; then
  echo "core entrypoint: domain is not set" >&2
  exit 2
fi

export TOGAVSU_DATABASE_URL="$db"
export TOGAVSU_ENCRYPTION_KEY="$key"
export TOGAVSU_DOMAIN="$domain"
export TOGAVSU_SUPERVISOR_SECRET="$token"
# "core migrate" seals the append-only tables with the superuser (core.MigrateSealed)
# and fails without it. Only the migrate one-shot mounts the admin volume; a serving
# core that can read the superuser URL anywhere refuses to start (PRD section 6).
admin=${TOGAVSU_ADMIN_SECRETS_DIR:-/adminsecrets}
unset TOGAVSU_ADMIN_DATABASE_URL
case "${1:-}" in
  migrate)
    if [ ! -r "$admin/admin_database_url" ]; then
      echo "core entrypoint: migrate needs $admin/admin_database_url to seal the ledgers" >&2
      exit 2
    fi
    TOGAVSU_ADMIN_DATABASE_URL=$(read_trim "$admin/admin_database_url")
    export TOGAVSU_ADMIN_DATABASE_URL
    ;;
  check | healthcheck) ;;
  *)
    for f in "$admin/admin_database_url" "$secrets/admin_database_url"; do
      if [ -e "$f" ]; then
        echo "core entrypoint: refusing to serve: the superuser url $f is visible to the serving core" >&2
        exit 2
      fi
    done
    ;;
esac
exec "$bin" "$@"

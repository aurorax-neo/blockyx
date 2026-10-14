#!/bin/sh
# blockyx entrypoint: starts the blocky DNS server and the web UI in one container.
set -eu

CONFIG="${BLOCKYX_CONFIG:-/etc/blockyx/config.yml}"

# API key: use the provided one, or generate a random key and print it.
if [ -z "${BLOCKYX_API_KEY:-}" ]; then
  BLOCKYX_API_KEY="$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32)"
  export BLOCKYX_API_KEY
  echo "blockyx: BLOCKYX_API_KEY not set, generated one: ${BLOCKYX_API_KEY}" >&2
  echo "blockyx: set BLOCKYX_API_KEY env var to use your own key" >&2
fi

# The web UI reaches blocky's HTTP API on localhost and authenticates with the key.
export BLOCKY_API_URL="${BLOCKY_API_URL:-http://127.0.0.1:4000}"
export BLOCKY_REQUEST_HEADERS="{\"X-API-Key\":\"${BLOCKYX_API_KEY}\"}"
export QUERY_LOG_TYPE="${QUERY_LOG_TYPE:-csv}"
export QUERY_LOG_TARGET="${QUERY_LOG_TARGET:-/logs}"
export PORT=3000
export HOSTNAME=0.0.0.0
export NODE_ENV=production

if [ ! -f "$CONFIG" ]; then
  echo "blockyx: config not found at $CONFIG" >&2
  echo "blockyx: mount your config.yml there, see config.yml.example" >&2
  exit 1
fi

./blockyx --config "$CONFIG" &
BLOCKY_PID=$!

# shellcheck disable=SC2064
trap "kill ${BLOCKY_PID} 2>/dev/null; exit 0" TERM INT

# Web UI in the foreground; container stops when it exits.
node server.js

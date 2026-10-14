#!/bin/sh
# blockyx entrypoint: runs the single blockyx binary.
# It serves DNS, the HTTP API and the embedded web UI by itself.
set -eu

CONFIG="${BLOCKYX_CONFIG:-/etc/blockyx/config.yml}"

# API key: use the provided one, or generate a random key and print it.
# The binary refuses to start without a key.
if [ -z "${BLOCKYX_API_KEY:-}" ]; then
  BLOCKYX_API_KEY="$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32)"
  export BLOCKYX_API_KEY
  echo "blockyx: BLOCKYX_API_KEY not set, generated one: ${BLOCKYX_API_KEY}" >&2
  echo "blockyx: set BLOCKYX_API_KEY env var to use your own key" >&2
fi

if [ ! -f "$CONFIG" ]; then
  echo "blockyx: config not found at $CONFIG" >&2
  echo "blockyx: mount your config.yml there, see config.yml.example" >&2
  exit 1
fi

exec ./blockyx --config "$CONFIG"

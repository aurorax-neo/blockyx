#!/bin/sh
# blockyx entrypoint: fix volume permissions, then drop to non-root.
set -eu

CONFIG="${BLOCKYX_CONFIG:-/etc/blockyx/config.yml}"

# Fix ownership of mapped directories when running as root,
# so the non-root blockyx user can read/write them.
if [ "$(id -u)" = "0" ]; then
  for d in /etc/blockyx /logs /data; do
    if [ -e "$d" ]; then
      chown -R blockyx:blockyx "$d" 2>/dev/null || true
    fi
  done
  # Also fix the config file itself if it's a mounted single file
  if [ -f "$CONFIG" ]; then
    chown blockyx:blockyx "$CONFIG" 2>/dev/null || true
  fi

  exec su-exec blockyx:blockyx ./blockyx --config "$CONFIG"
fi

# Already non-root (e.g. docker --user): just run.
exec ./blockyx --config "$CONFIG"

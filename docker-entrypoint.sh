#!/bin/sh
set -eu

CONFIG_PATH="${CONFIG_PATH:-/config/config.json}"
INTERVAL="${SCHEDULE_INTERVAL_SECONDS:-}"

run_once() {
  date
  sub2toimage -config "$CONFIG_PATH"
}

if [ -z "$INTERVAL" ] || [ "$INTERVAL" = "0" ]; then
  exec sub2toimage -config "$CONFIG_PATH"
fi

if [ "${RUN_ON_START:-true}" = "true" ]; then
  run_once || true
fi

while true; do
  sleep "$INTERVAL"
  run_once || true
done

#!/usr/bin/env bash
# Starts all backend services (tgsignal only with TG_API_ID). If any one exits, stop the rest and
# exit non-zero so the platform restarts the container.
set -uo pipefail
cd /app

# Railway injects PORT for the public service; the gateway must listen on it.
if [ -n "${PORT:-}" ]; then
  export GATEWAY_ADDR=":${PORT}"
fi
export GATEWAY_ADDR="${GATEWAY_ADDR:-:9090}"
# mdengine's metrics default is :9090, which collides with the gateway.
export METRICS_ADDR="${METRICS_ADDR:-:9091}"
export STAGING_MODE="${STAGING_MODE:-false}"

pids=()
start() {
  echo "[entrypoint] starting $1"
  "/app/bin/$1" &
  pids+=("$!")
}

shutdown() {
  echo "[entrypoint] stopping services"
  kill -TERM "${pids[@]}" 2>/dev/null
  wait
}
trap 'shutdown; exit 0' TERM INT

# A fresh volume (new Railway account, lost disk) is refilled from MEGA
# before anything opens the databases. No-op when local data exists;
# never blocks startup for long or on failure.
timeout 300 /app/bin/archiver -restore || echo "[entrypoint] restore did not finish; continuing"
# Fill missing 1m index candles (BACKFILL_DAYS, every UNDERLYINGS index)
# from Angel before stratengine warms up; never blocks startup for long.
timeout 180 /app/bin/backfill || echo "[entrypoint] backfill did not finish; continuing"

start mdengine
sleep 2
start indengine
start stratengine
start analyst
# exitwatch: shadow-only exit advice (WATCH_* signals); never places orders.
start exitwatch
# archiver: after the close, uploads finished days to MEGA and prunes
# local SQLite (ARCHIVE_ENABLED=true). Never exits on its own errors.
start archiver
start api_gateway
# tgsignal: Telegram group calls (TELEGRAM_SIGNAL, paper unless named in
# STRAT_LIVE_STRATEGIES). Only with TG_API_ID. It runs in its own retry
# loop, so a cancelled Telegram session or a bad LLM key never takes the
# trading services down through wait -n. The session is seeded from
# TG_SESSION_B64 on a fresh volume (/app/data/tg.session) and reused after.
if [ -n "${TG_API_ID:-}" ]; then
  echo "[entrypoint] starting tgsignal (retry loop)"
  (
    trap 'kill -TERM "$child" 2>/dev/null; wait "$child"; exit 0' TERM INT
    while true; do
      /app/bin/tgsignal &
      child=$!
      wait "$child"
      echo "[entrypoint] tgsignal exited with code $?; retrying in 60s"
      sleep 60 &
      wait $!
    done
  ) &
  pids+=("$!")
else
  echo "[entrypoint] TG_API_ID not set; tgsignal not started"
fi

wait -n
code=$?
echo "[entrypoint] a service exited with code ${code}; shutting down"
shutdown
exit "${code:-1}"

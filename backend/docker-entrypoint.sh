#!/usr/bin/env bash
# Starts all five backend services. If any one exits, stop the rest and
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

start mdengine
sleep 2
start indengine
start stratengine
start analyst
start api_gateway

wait -n
code=$?
echo "[entrypoint] a service exited with code ${code}; shutting down"
shutdown
exit "${code:-1}"

#!/bin/bash
# ═══════════════════════════════════════════════════════
#  LOCAL runner — called by Air via scripts/air_local.sh
#  Production services (Angel One live feed) with the repo-root .env ONLY.
#  .env.prod is NOT loaded: it sets STRAT_LIVE_ORDERS=true.
# ═══════════════════════════════════════════════════════

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if [ -f "$REPO_ROOT/.env" ]; then
  set -a
  source "$REPO_ROOT/.env"
  set +a
fi

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

echo "LOCAL (.env only): STAGING_MODE=${STAGING_MODE:-} STRAT_LIVE_ORDERS=${STRAT_LIVE_ORDERS:-}"

./tmp/mdengine    2>&1 | sed 's/^/[mdengine]    /' &
./tmp/indengine   2>&1 | sed 's/^/[indengine]   /' &
./tmp/api_gateway 2>&1 | sed 's/^/[api_gateway] /' &
./tmp/stratengine 2>&1 | sed 's/^/[stratengine] /' &
./tmp/analyst     2>&1 | sed 's/^/[analyst]     /' &
./tmp/exitwatch   2>&1 | sed 's/^/[exitwatch]   /' &
./tmp/archiver    2>&1 | sed 's/^/[archiver]    /' &
# Telegram group calls: only once TG_API_ID is set and the session exists
# (go run ./cmd/tools/tglogin).
if [ -n "${TG_API_ID:-}" ]; then
  ./tmp/tgsignal  2>&1 | sed 's/^/[tgsignal]    /' &
fi

wait

#!/bin/bash
# ═══════════════════════════════════════════════════════
#  PRODUCTION runner — called by Air (.air.production.toml)
#  Services: mdengine, api_gateway, stratengine, analyst
#  Uses real Angel One feed (STAGING_MODE=false)
# ═══════════════════════════════════════════════════════

# Load env vars from repo root: base .env first, then .env.prod overrides
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if [ -f "$REPO_ROOT/.env" ]; then
  set -a
  source "$REPO_ROOT/.env"
  set +a
fi
if [ -f "$REPO_ROOT/.env.prod" ]; then
  set -a
  source "$REPO_ROOT/.env.prod"
  set +a
fi

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

echo "╔═══════════════════════════════════════════════════╗"
echo "║  PRODUCTION MODE                                 ║"
echo "║  Services: mdengine + api_gateway + stratengine + analyst + indengine ║"
echo "║  Strategy: NIFTY50_FNO (live orders)             ║"
echo "║  Data: Angel One Live Feed                       ║"
echo "╚═══════════════════════════════════════════════════╝"

./tmp/mdengine    2>&1 | sed 's/^/[mdengine]    /' &
./tmp/indengine   2>&1 | sed 's/^/[indengine]   /' &
./tmp/api_gateway  2>&1 | sed 's/^/[api_gateway]  /' &
./tmp/stratengine 2>&1 | sed 's/^/[stratengine] /' &
./tmp/analyst     2>&1 | sed 's/^/[analyst]     /' &

wait

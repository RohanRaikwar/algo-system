#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════
#  Start STAGING stack with Air (live-reload)
#  Services: tickserver + mdengine + indengine + api_gateway + stratengine
#  Env: .env.staging (STAGING_MODE=true)
#  Usage: ./scripts/air_staging.sh
# ═══════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="$PATH:$HOME/go/bin"

echo "🧪 Loading STAGING environment (.env.staging)..."
if [ -f .env.staging ]; then
    set -a
    source .env.staging
    set +a
fi

# Force staging mode
export STAGING_MODE=true

echo ""
echo "╔═══════════════════════════════════════════════════╗"
echo "║  🟡 STAGING — Air Live-Reload                    ║"
echo "║  tickserver  : ws://localhost:9001/ws             ║"
echo "║  mdengine    : sim feed from tickserver           ║"
echo "║  indengine   : :9095                             ║"
echo "║  api_gateway : http://localhost${GATEWAY_ADDR:-:9090}        ║"
echo "║  stratengine : strategy engine                   ║"
echo "║  frontend    : run: cd frontend && npm run dev   ║"
echo "║  Press Ctrl+C to stop                            ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

cd backend
exec air -c .air.staging.toml

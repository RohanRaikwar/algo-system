#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════
#  Start LOCAL stack with Air (live-reload) + frontend
#  Backend: production build, Angel One live feed, env from .env ONLY
#           (.env.prod is skipped — it turns on live orders)
#  Frontend: vite dev server (http://localhost:5173)
#  Usage: ./scripts/air_local.sh            # backend + frontend
#         NO_FE=1 ./scripts/air_local.sh    # backend only
# ═══════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/.."
export PATH="$PATH:$HOME/go/bin"

if [ ! -f .env ]; then
    echo "❌ .env not found in repo root" >&2
    exit 1
fi

FE_PID=""
if [ -z "${NO_FE:-}" ]; then
    echo "🖥  Starting frontend (vite) ..."
    # frontend/.env points VITE_API_URL at the Railway deploy; empty it so the
    # vite proxy sends /api and /ws to the local gateway (localhost:9090).
    (cd frontend && VITE_API_URL= npm run dev 2>&1 | sed 's/^/[frontend]    /') &
    FE_PID=$!
fi
trap '[ -n "$FE_PID" ] && pkill -P "$FE_PID" 2>/dev/null; kill "$FE_PID" 2>/dev/null || true' EXIT

cd backend
air -c .air.production.toml -build.full_bin "bash ./run_local.sh"

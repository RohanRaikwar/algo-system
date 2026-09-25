#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════
#  Test Dynamic Lot Size Fetching in Production
#  Tests instrument master loading and lot size retrieval
# ═══════════════════════════════════════════════════════
set -euo pipefail

cd "$(dirname "$0")/.."

echo "╔═══════════════════════════════════════════════════╗"
echo "║  🧪 Testing Dynamic Lot Size in Production       ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

# Load production environment
echo "📋 Loading production environment (.env.prod)..."
if [ -f .env.prod ]; then
    set -a
    source .env.prod
    set +a
    echo "✅ Environment loaded"
else
    echo "❌ .env.prod not found!"
    exit 1
fi

echo ""
echo "🔍 Testing Components:"
echo "  1. Instrument Master Download"
echo "  2. Lot Size Fetching"
echo "  3. Strike Resolution with Dynamic Lot Size"
echo ""

# Test 1: Instrument Master
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📥 Test 1: Instrument Master Download"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
cd backend/cmd/testlotsize
echo "Running: go run main.go"
echo ""
go run main.go
echo ""

# Test 2: Strike Resolution
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🎯 Test 2: Strike Resolution with Dynamic Lot Size"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
cd ../teststrike
echo "Running: go run main.go"
echo ""
go run main.go
echo ""

# Check cache
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "💾 Cache Status"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [ -d ".cache" ]; then
    echo "✅ Cache directory exists"
    ls -lh .cache/ 2>/dev/null || echo "  (empty)"
else
    echo "⚠️  No cache directory found"
fi
echo ""

# Summary
echo "╔═══════════════════════════════════════════════════╗"
echo "║  ✅ Dynamic Lot Size Tests Complete              ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""
echo "📊 Summary:"
echo "  • Instrument master: Downloads ~20MB file"
echo "  • Lot size fetching: Extracts from master file"
echo "  • Strike resolution: Uses dynamic lot size"
echo "  • Fallback: Uses NIFTY_LOT_SIZE=$NIFTY_LOT_SIZE if master fails"
echo ""
echo "🚀 Ready for production deployment!"
echo ""

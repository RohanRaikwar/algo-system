#!/bin/bash

# Test startup download configuration in production environment
echo "🧪 Testing Startup Download in Production Environment"
echo "======================================================"

cd backend

echo ""
echo "📋 Test 1: Production startup with valid cache"
echo "Environment: .env.prod"
echo "Expected: Load from cache, no download needed"
echo ""

# Use production environment
export $(cat ../.env.prod | grep -v '^#' | xargs)

# Run a quick test
timeout 10s go run cmd/testlotsize/main.go

echo ""
echo "📋 Test 2: Production startup with missing cache"
echo "Environment: .env.prod"
echo "Expected: Download on startup (INSTRUMENT_MASTER_STARTUP_DOWNLOAD=true)"
echo ""

# Remove cache to simulate fresh startup
rm -rf .cache

# Run test with missing cache
timeout 15s go run cmd/testlotsize/main.go

echo ""
echo "📋 Test 3: Production startup with disabled download"
echo "Environment: .env.prod with INSTRUMENT_MASTER_STARTUP_DOWNLOAD=false"
echo "Expected: Skip download, use fallback"
echo ""

# Remove cache again
rm -rf .cache

# Override startup download setting
export INSTRUMENT_MASTER_STARTUP_DOWNLOAD=false

# Run test with disabled startup download
timeout 10s go run cmd/testlotsize/main.go

echo ""
echo "✅ Production startup tests completed"
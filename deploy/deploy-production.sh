#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  FULL PRODUCTION DEPLOY — One command to set up + deploy backend
#  Run FROM YOUR LOCAL MACHINE (not the VM).
#
#  Usage:
#    ./deploy-production.sh <VM_IP> [SSH_USER] [SSH_KEY]
#
#  Examples:
#    ./deploy-production.sh 34.131.10.50
#    ./deploy-production.sh 34.131.10.50 ubuntu ~/.ssh/gcp_key
#
#  What it does:
#    1. Syncs code to VM via rsync
#    2. Installs Go, Redis on VM (if missing)
#    3. Builds 3 Go microservices on VM
#    4. Installs systemd service (auto-start on boot)
#    5. Starts the backend + health check
# ═══════════════════════════════════════════════════════════════════
set -euo pipefail

# ── Args (pre-filled for your EC2 instance) ──
VM_IP="${1:-ec2-13-48-136-245.eu-north-1.compute.amazonaws.com}"
SSH_USER="${2:-ubuntu}"
SSH_KEY="${3:-myVM.pem}"

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=10"
if [ -n "$SSH_KEY" ]; then
  SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
fi

SSH_CMD="ssh $SSH_OPTS $SSH_USER@$VM_IP"
RSYNC_SSH="ssh $SSH_OPTS"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REMOTE_DIR="/opt/trading-backend"

echo "╔═════════════════════════════════════════════════════════════╗"
echo "║  Production Deploy → $VM_IP"
echo "║  User: $SSH_USER | Dir: $REMOTE_DIR"
echo "╚═════════════════════════════════════════════════════════════╝"
echo ""

# ── Step 1: Check SSH connectivity ──
echo "→ [1/6] Testing SSH connection..."
if ! $SSH_CMD "echo ok" > /dev/null 2>&1; then
  echo "❌ Cannot SSH to $SSH_USER@$VM_IP"
  echo "   Check: IP, username, SSH key, firewall"
  exit 1
fi
echo "  ✅ SSH connected"

# ── Step 2: Sync code to VM ──
echo "→ [2/6] Syncing code to VM..."
$SSH_CMD "sudo mkdir -p $REMOTE_DIR && sudo chown $SSH_USER:$SSH_USER $REMOTE_DIR"
rsync -az --delete \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude 'frontend' \
  --exclude 'backend/tmp' \
  --exclude 'backend/data/*.db' \
  --exclude '*.db-journal' \
  -e "$RSYNC_SSH" \
  "$REPO_ROOT/backend/" "$SSH_USER@$VM_IP:~/trading-backend/"

# Copy .env file
if [ -f "$REPO_ROOT/.env" ]; then
  rsync -az -e "$RSYNC_SSH" "$REPO_ROOT/.env" "$SSH_USER@$VM_IP:~/trading-backend/.env"
  echo "  ✅ .env synced"
else
  echo "  ⚠️  No .env found at $REPO_ROOT/.env — create one on the VM!"
fi

# Move to /opt with sudo
$SSH_CMD "sudo rm -rf $REMOTE_DIR/cmd $REMOTE_DIR/internal $REMOTE_DIR/pkg $REMOTE_DIR/config $REMOTE_DIR/go.* 2>/dev/null; sudo cp -r ~/trading-backend/* $REMOTE_DIR/ && sudo cp ~/trading-backend/.env $REMOTE_DIR/.env 2>/dev/null; sudo chown -R root:root $REMOTE_DIR; sudo chown -R trading:trading $REMOTE_DIR/data $REMOTE_DIR/logs"
echo "  ✅ Code synced"

# ── Step 3: Install dependencies on VM ──
echo "→ [3/6] Installing dependencies (Go, Redis)..."
$SSH_CMD "sudo bash -s" << 'SETUP_SCRIPT'
export DEBIAN_FRONTEND=noninteractive
export PATH=$PATH:/usr/local/go/bin

# Install Redis + build tools
if ! command -v redis-server &>/dev/null; then
  apt-get update -qq
  apt-get install -y -qq build-essential redis-server sqlite3 curl gcc
  systemctl enable redis-server
  systemctl start redis-server
  echo "  Installed Redis"
else
  echo "  Redis already installed"
fi

# Install Go
GO_VERSION="1.21.13"
if ! /usr/local/go/bin/go version &>/dev/null; then
  echo "  Installing Go $GO_VERSION..."
  curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  echo "  Installed Go $GO_VERSION"
else
  echo "  Go already installed: $(/usr/local/go/bin/go version)"
fi

# Create app user if needed
id trading &>/dev/null || useradd -r -s /bin/bash -m trading 2>/dev/null || true

mkdir -p /opt/trading-backend/{bin,data,logs}
SETUP_SCRIPT
echo "  ✅ Dependencies ready"

# ── Step 4: Build microservices ──
echo "→ [4/6] Building Go microservices on VM..."
$SSH_CMD "sudo bash -s" << 'BUILD_SCRIPT'
set -euo pipefail
export PATH=$PATH:/usr/local/go/bin
cd /opt/trading-backend

CGO_ENABLED=1 go build -o bin/mdengine        ./cmd/mdengine/
CGO_ENABLED=1 go build -o bin/indengine       ./cmd/indengine/
CGO_ENABLED=1 go build -o bin/api_gateway     ./cmd/api_gateway/
CGO_ENABLED=1 go build -o bin/stratengine     ./cmd/stratengine/

echo "  Built: mdengine, indengine, api_gateway, stratengine"
BUILD_SCRIPT
echo "  ✅ Binaries built"

# ── Step 5: Credential guard ──
echo "→ [5/7] Checking credentials..."
if grep -q "your_api_key_here" "$REPO_ROOT/.env" 2>/dev/null; then
  echo "  ❌ .env contains placeholder credentials — refusing to deploy."
  echo "     Copy .env.example to .env and fill in real values first."
  exit 1
fi
echo "  ✅ Credentials look populated"

# ── Step 6: Install per-service systemd units ──
echo "→ [6/7] Installing systemd services..."
$SSH_CMD "sudo bash -s" << 'SERVICE_SCRIPT'
set -euo pipefail
APP_DIR="/opt/trading-backend"

# ── Helper: create a service unit ──
create_unit() {
  local NAME="$1"
  local BINARY="$2"
  local DESCRIPTION="$3"
  local AFTER="$4"
  local EXTRA_DEPS="${5:-}"

  cat > "/etc/systemd/system/trading-${NAME}.service" << UNIT
[Unit]
Description=Trading System — ${DESCRIPTION}
After=network.target ${AFTER}
Requires=${EXTRA_DEPS:-}
PartOf=trading-backend.target

[Service]
Type=simple
User=trading
Group=trading
WorkingDirectory=${APP_DIR}
EnvironmentFile=${APP_DIR}/.env
Environment=STAGING_MODE=false
ExecStart=${APP_DIR}/bin/${BINARY}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-${NAME}
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${APP_DIR}/data ${APP_DIR}/logs
MemoryMax=512M

[Install]
WantedBy=trading-backend.target
UNIT
}

# ── Create individual service units ──
create_unit "mdengine" "mdengine" \
  "Market Data Engine (tick ingest, OHLC aggregation, TF resampling)" \
  "redis-server.service" \
  "redis-server.service"

create_unit "indengine" "indengine" \
  "Indicator Engine (SMA, EMA, RSI computation)" \
  "redis-server.service trading-mdengine.service" \
  "redis-server.service"

create_unit "api-gateway" "api_gateway" \
  "API Gateway (REST + WebSocket hub)" \
  "redis-server.service" \
  "redis-server.service"

create_unit "stratengine" "stratengine" \
  "Strategy Engine (EMA MTF CALL/PUT signals)" \
  "redis-server.service trading-indengine.service" \
  "redis-server.service"

# ── Create grouping target ──
cat > /etc/systemd/system/trading-backend.target << TARGET
[Unit]
Description=Trading System Backend (all services)
Requires=trading-mdengine.service trading-indengine.service trading-api-gateway.service trading-stratengine.service
After=trading-mdengine.service trading-indengine.service trading-api-gateway.service trading-stratengine.service

[Install]
WantedBy=multi-user.target
TARGET

# ── Disable old monolithic unit if present ──
if systemctl is-enabled trading-backend.service 2>/dev/null; then
  systemctl disable trading-backend.service 2>/dev/null || true
  systemctl stop trading-backend.service 2>/dev/null || true
  rm -f /etc/systemd/system/trading-backend.service
  echo "  Removed old monolithic trading-backend.service"
fi

# Remove old run script
rm -f "$APP_DIR/bin/run-backend.sh"
rm -f "$APP_DIR/bin/stratengine_ind"

if systemctl list-unit-files | grep -q '^trading-stratengine-ind.service'; then
  systemctl disable trading-stratengine-ind.service 2>/dev/null || true
  systemctl stop trading-stratengine-ind.service 2>/dev/null || true
  rm -f /etc/systemd/system/trading-stratengine-ind.service
  echo "  Removed stale trading-stratengine-ind.service"
fi

systemctl daemon-reload
systemctl enable trading-backend.target
SERVICE_SCRIPT
echo "  ✅ Per-service systemd units installed"

# ── Step 7: Start + health check ──
echo "→ [7/7] Starting backend services..."
$SSH_CMD "sudo systemctl start trading-backend.target && sleep 5"

# Check each service
SERVICES="mdengine indengine api-gateway stratengine"
ALL_OK=true
for svc in $SERVICES; do
  STATUS=$($SSH_CMD "sudo systemctl is-active trading-${svc}.service 2>&1" || echo "inactive")
  if [ "$STATUS" = "active" ]; then
    echo "  ✅ trading-${svc} is running"
  else
    echo "  ⚠️  trading-${svc} is ${STATUS}"
    ALL_OK=false
  fi
done

HEALTH=$($SSH_CMD "curl -sf http://localhost:9090/api/config 2>&1" || echo "FAIL")
if [ "$HEALTH" != "FAIL" ]; then
  echo "  ✅ API Gateway responding on :9090"
else
  echo "  ⚠️  API Gateway not ready yet"
fi

echo ""
echo "╔══════════════════════════════════════════════════════════════════════╗"
echo "║  ✅ Production backend deployed! (per-service isolation)           ║"
echo "║                                                                     ║"
echo "║  API:     http://$VM_IP:9090                                        ║"
echo "║  WS:      ws://$VM_IP:9090/ws                                       ║"
echo "║  Metrics: http://$VM_IP:9091/metrics                                ║"
echo "║                                                                     ║"
echo "║  Commands:                                                          ║"
echo "║    All:     sudo systemctl start/stop trading-backend.target        ║"
echo "║    Single:  sudo systemctl restart trading-mdengine                 ║"
echo "║    Logs:    sudo journalctl -u trading-mdengine -f                  ║"
echo "║    Status:  sudo systemctl status trading-mdengine                  ║"
echo "╚══════════════════════════════════════════════════════════════════════╝"

#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  FULL VM SETUP — One-time provisioning for BOTH backend & frontend
#  Run this ON the VM as root (sudo bash setup-vm.sh)
#
#  Installs:
#    Backend:  Go 1.21, Redis, build-essential, sqlite3
#    Frontend: Node.js 20 LTS, serve (static file server)
#    System:   trading user, directories, systemd units, firewall
# ═══════════════════════════════════════════════════════════════════
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "❌ Run as root: sudo bash setup-vm.sh"
  exit 1
fi

echo "╔═══════════════════════════════════════════════════════════╗"
echo "║  Full VM Setup — Backend + Frontend (same machine)      ║"
echo "╚═══════════════════════════════════════════════════════════╝"
echo ""

export DEBIAN_FRONTEND=noninteractive

# ─────────────────────────────────────────────────────────────
#  1. System packages
# ─────────────────────────────────────────────────────────────
echo "→ [1/8] Installing system packages..."
apt-get update -qq
apt-get install -y -qq build-essential git curl redis-server sqlite3 gcc ufw
echo "  ✅ System packages installed"

# ─────────────────────────────────────────────────────────────
#  2. Install Go 1.21
# ─────────────────────────────────────────────────────────────
echo "→ [2/8] Installing Go..."
GO_VERSION="1.21.13"
if ! /usr/local/go/bin/go version &>/dev/null; then
  curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  export PATH=$PATH:/usr/local/go/bin
  echo "  Installed Go $GO_VERSION"
else
  echo "  Go already installed: $(/usr/local/go/bin/go version)"
fi

# ─────────────────────────────────────────────────────────────
#  3. Install Node.js 20 LTS
# ─────────────────────────────────────────────────────────────
echo "→ [3/8] Installing Node.js 20..."
if ! command -v node &>/dev/null; then
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  apt-get install -y -qq nodejs
  echo "  Installed Node $(node -v)"
else
  echo "  Node already installed: $(node -v)"
fi

# Install 'serve' globally (lightweight static file server)
npm install -g serve
echo "  serve: $(serve --version)"

# ─────────────────────────────────────────────────────────────
#  4. Enable Redis
# ─────────────────────────────────────────────────────────────
echo "→ [4/8] Configuring Redis..."
systemctl enable redis-server
systemctl start redis-server
echo "  Redis: $(redis-cli ping)"

# ─────────────────────────────────────────────────────────────
#  5. Create app user & directories
# ─────────────────────────────────────────────────────────────
echo "→ [5/8] Creating app user & directories..."
APP_USER="trading"
BACKEND_DIR="/opt/trading-backend"
FRONTEND_DIR="/opt/trading-frontend"

if ! id "$APP_USER" &>/dev/null; then
  useradd -r -s /bin/bash -m "$APP_USER"
  echo "  Created user: $APP_USER"
else
  echo "  User $APP_USER already exists"
fi

mkdir -p "$BACKEND_DIR"/{bin,data,logs,config}
mkdir -p "$FRONTEND_DIR"
chown -R "$APP_USER:$APP_USER" "$BACKEND_DIR"
chown -R "$APP_USER:$APP_USER" "$FRONTEND_DIR"
echo "  ✅ Directories created"

# ─────────────────────────────────────────────────────────────
#  6. Install backend systemd services (per-microservice)
# ─────────────────────────────────────────────────────────────
echo "→ [6/8] Installing backend systemd services..."

create_backend_unit() {
  local NAME="$1"
  local BINARY="$2"
  local DESCRIPTION="$3"
  local AFTER="$4"
  local EXTRA_DEPS="${5:-}"

  cat > "/etc/systemd/system/trading-${NAME}.service" <<UNIT
[Unit]
Description=Trading System — ${DESCRIPTION}
After=network.target ${AFTER}
Requires=${EXTRA_DEPS:-}
PartOf=trading-backend.target

[Service]
Type=simple
User=trading
Group=trading
WorkingDirectory=${BACKEND_DIR}
EnvironmentFile=${BACKEND_DIR}/.env
Environment=STAGING_MODE=false
ExecStart=${BACKEND_DIR}/bin/${BINARY}
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-${NAME}
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=${BACKEND_DIR}/data ${BACKEND_DIR}/logs
MemoryMax=512M

[Install]
WantedBy=trading-backend.target
UNIT
}

create_backend_unit "mdengine" "mdengine" \
  "Market Data Engine (tick ingest, OHLC aggregation, TF resampling)" \
  "redis-server.service" \
  "redis-server.service"

create_backend_unit "indengine" "indengine" \
  "Indicator Engine (SMA, EMA, RSI computation)" \
  "redis-server.service trading-mdengine.service" \
  "redis-server.service"

create_backend_unit "api-gateway" "api_gateway" \
  "API Gateway (REST + WebSocket hub)" \
  "redis-server.service" \
  "redis-server.service"

create_backend_unit "stratengine" "stratengine" \
  "Strategy Engine (EMA MTF CALL/PUT signals)" \
  "redis-server.service trading-indengine.service" \
  "redis-server.service"

# Grouping target
cat > /etc/systemd/system/trading-backend.target <<TARGET
[Unit]
Description=Trading System Backend (all services)
Requires=trading-mdengine.service trading-indengine.service trading-api-gateway.service trading-stratengine.service
After=trading-mdengine.service trading-indengine.service trading-api-gateway.service trading-stratengine.service

[Install]
WantedBy=multi-user.target
TARGET

# Remove old monolithic unit if present
if systemctl is-enabled trading-backend.service 2>/dev/null; then
  systemctl disable trading-backend.service 2>/dev/null || true
  systemctl stop trading-backend.service 2>/dev/null || true
  rm -f /etc/systemd/system/trading-backend.service
  echo "  Removed old monolithic trading-backend.service"
fi

# Remove stale indicator-consumer service if present. The current repo no
# longer ships cmd/stratengine_ind, so leaving the old unit around revives a
# stale binary on newer deployments.
if systemctl list-unit-files | grep -q '^trading-stratengine-ind.service'; then
  systemctl disable trading-stratengine-ind.service 2>/dev/null || true
  systemctl stop trading-stratengine-ind.service 2>/dev/null || true
  rm -f /etc/systemd/system/trading-stratengine-ind.service
  rm -f "${BACKEND_DIR}/bin/stratengine_ind"
  echo "  Removed stale trading-stratengine-ind.service"
fi

echo "  ✅ Backend services installed"

# ─────────────────────────────────────────────────────────────
#  7. Install frontend systemd service
# ─────────────────────────────────────────────────────────────
echo "→ [7/8] Installing frontend systemd service..."
SERVE_PATH=$(which serve)

cat > /etc/systemd/system/trading-frontend.service <<UNIT
[Unit]
Description=Trading Frontend (serve)
After=network.target

[Service]
Type=simple
ExecStart=${SERVE_PATH} -s ${FRONTEND_DIR}/dist -l 80
Restart=on-failure
RestartSec=3
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-frontend

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable trading-backend.target
systemctl enable trading-frontend
echo "  ✅ Frontend service installed"

# ─────────────────────────────────────────────────────────────
#  8. Firewall (ports 22, 80, 9090, 9091)
# ─────────────────────────────────────────────────────────────
echo "→ [8/8] Configuring firewall..."
ufw allow 22/tcp    # SSH
ufw allow 80/tcp    # Frontend
ufw allow 9090/tcp  # API Gateway
ufw allow 9091/tcp  # Metrics
ufw --force enable
echo "  ✅ Firewall configured (22, 80, 9090, 9091 open)"

echo ""
echo "╔═══════════════════════════════════════════════════════════╗"
echo "║  ✅ VM setup complete!                                  ║"
echo "║                                                          ║"
echo "║  What's installed:                                       ║"
echo "║    • Go $GO_VERSION                                      ║"
echo "║    • Node $(node -v) + serve                             ║"
echo "║    • Redis                                               ║"
echo "║    • 4 backend services + frontend service (systemd)     ║"
echo "║                                                          ║"
echo "║  Next steps:                                             ║"
echo "║    1. Set GitHub Secrets (VM_HOST, VM_USER,              ║"
echo "║       VM_SSH_KEY, ENV_FILE)                              ║"
echo "║    2. Push to main → CI/CD auto-deploys!                 ║"
echo "╚═══════════════════════════════════════════════════════════╝"

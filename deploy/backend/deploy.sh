#!/bin/bash
# ═══════════════════════════════════════════════════════════
#  Backend Deploy — builds Go services + installs systemd
#  Usage: sudo ./deploy.sh
# ═══════════════════════════════════════════════════════════
set -euo pipefail

APP_DIR="/opt/trading-backend"
APP_USER="trading"

echo "╔═══════════════════════════════════════════════╗"
echo "║  Backend Deploy                              ║"
echo "╚═══════════════════════════════════════════════╝"

# ── 0. Check .env ──
if [ ! -f "$APP_DIR/.env" ]; then
  echo "❌ $APP_DIR/.env not found! Copy it first."
  exit 1
fi

# ── 1. Build microservices ──
echo "→ Building Go microservices..."
export PATH=$PATH:/usr/local/go/bin
cd "$APP_DIR"

CGO_ENABLED=1 go build -o "$APP_DIR/bin/mdengine"        ./cmd/mdengine/
CGO_ENABLED=1 go build -o "$APP_DIR/bin/indengine"       ./cmd/indengine/
CGO_ENABLED=1 go build -o "$APP_DIR/bin/api_gateway"     ./cmd/api_gateway/
CGO_ENABLED=1 go build -o "$APP_DIR/bin/stratengine"     ./cmd/stratengine/
rm -f "$APP_DIR/bin/stratengine_ind"
echo "  ✅ Built: mdengine, indengine, api_gateway, stratengine"

# ── 2. Create run script ──
cat > "$APP_DIR/bin/run-backend.sh" << 'EOF'
#!/bin/bash
set -a
source /opt/trading-backend/.env
set +a

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

/opt/trading-backend/bin/mdengine        2>&1 | sed 's/^/[mdengine]    /' &
/opt/trading-backend/bin/indengine       2>&1 | sed 's/^/[indengine]   /' &
/opt/trading-backend/bin/api_gateway     2>&1 | sed 's/^/[api_gateway] /' &
/opt/trading-backend/bin/stratengine     2>&1 | sed 's/^/[stratengine] /' &

wait
EOF
chmod +x "$APP_DIR/bin/run-backend.sh"

# ── 3. Install systemd service ──
echo "→ Installing systemd service..."
cat > /etc/systemd/system/trading-backend.service << UNIT
[Unit]
Description=Trading System Backend (mdengine + indengine + api_gateway + stratengine)
After=network.target redis-server.service
Requires=redis-server.service

[Service]
Type=simple
User=$APP_USER
Group=$APP_USER
WorkingDirectory=$APP_DIR
EnvironmentFile=$APP_DIR/.env
ExecStart=$APP_DIR/bin/run-backend.sh
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-backend
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$APP_DIR/data $APP_DIR/logs

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable trading-backend

if systemctl list-unit-files | grep -q '^trading-stratengine-ind.service'; then
  systemctl disable trading-stratengine-ind.service 2>/dev/null || true
  systemctl stop trading-stratengine-ind.service 2>/dev/null || true
  rm -f /etc/systemd/system/trading-stratengine-ind.service
fi

# ── 4. Restart ──
echo "→ Restarting backend..."
systemctl restart trading-backend
sleep 2

# ── 5. Health check ──
if curl -sf http://localhost:9090/api/config > /dev/null 2>&1; then
  echo "  ✅ API Gateway responding on :9090"
else
  echo "  ⚠️  API Gateway not ready yet (check: journalctl -u trading-backend -f)"
fi

echo ""
echo "╔═══════════════════════════════════════════════╗"
echo "║  ✅ Backend deployed!                        ║"
echo "║  API:  http://<VM_IP>:9090                   ║"
echo "║  WS:   ws://<VM_IP>:9090/ws                  ║"
echo "║  Logs: journalctl -u trading-backend -f      ║"
echo "╚═══════════════════════════════════════════════╝"

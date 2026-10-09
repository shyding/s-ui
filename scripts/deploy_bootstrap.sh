#!/bin/bash
set -e

TARGET_DOMAIN="${1:-dash.icta.top}"

echo "=========================================================="
echo "   Antigravity Automated Self-Bootstrapping Deployment    "
echo "   Target Domain: ${TARGET_DOMAIN}                        "
echo "=========================================================="

# 1. Base directory structure initialization
echo "--> [1/8] Ensuring target directories exist..."
mkdir -p /usr/local/s-ui/bin
mkdir -p /usr/local/s-ui/scripts
mkdir -p /usr/local/s-ui/db
mkdir -p /usr/local/s-ui/certs

# 2. Stop running service and clean port conflicts
echo "--> [2/8] Stopping existing services and releasing ports..."
systemctl stop s-ui 2>/dev/null || true
pkill -9 -f /usr/local/s-ui/sui 2>/dev/null || true
if command -v fuser &> /dev/null; then
  fuser -k 40734/udp 54142/tcp 2096/tcp 2053/tcp 2>/dev/null || true
fi
sleep 1

# 3. Ensure required system utilities are installed
echo "--> [3/8] Checking and installing required packages..."
MISSING_PKGS=""
for pkg in curl openssl certbot sqlite3 python3; do
  if ! command -v "$pkg" &> /dev/null; then
    MISSING_PKGS="$MISSING_PKGS $pkg"
  fi
done
if [ -n "$MISSING_PKGS" ]; then
  echo "Installing missing packages: $MISSING_PKGS"
  apt-get update -y || true
  apt-get install -y --no-install-recommends $MISSING_PKGS python3-pip || true
fi

# 4. Configure systemd service
echo "--> [4/8] Installing / updating s-ui systemd unit..."
if [ -f /tmp/s-ui.service ]; then
  cp /tmp/s-ui.service /etc/systemd/system/s-ui.service
elif [ ! -f /etc/systemd/system/s-ui.service ]; then
  cat << 'EOF' > /etc/systemd/system/s-ui.service
[Unit]
Description=s-ui Service
After=network.target
Wants=network.target

[Service]
Type=simple
WorkingDirectory=/usr/local/s-ui/
ExecStart=/usr/local/s-ui/sui
Restart=on-failure
RestartSec=10s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF
fi
chmod 644 /etc/systemd/system/s-ui.service
systemctl daemon-reload
systemctl enable s-ui

# 5. Deploy binary and Python scripts
echo "--> [5/8] Deploying binary and scripts..."
if [ -f /usr/local/s-ui/sui ]; then
  cp /usr/local/s-ui/sui /usr/local/s-ui/sui.backup.$(date +%Y%m%d_%H%M%S) 2>/dev/null || true
fi
if [ -f /tmp/sui ]; then
  mv /tmp/sui /usr/local/s-ui/sui
  chmod +x /usr/local/s-ui/sui
fi

if [ -d /tmp/scripts ]; then
  if [ -d /tmp/scripts/scripts ]; then
    cp -rf /tmp/scripts/scripts/* /usr/local/s-ui/scripts/ 2>/dev/null || true
  fi
  cp -rf /tmp/scripts/* /usr/local/s-ui/scripts/ 2>/dev/null || true
  chmod +x /usr/local/s-ui/scripts/*.py 2>/dev/null || true
  chmod +x /usr/local/s-ui/scripts/*.sh 2>/dev/null || true
fi

# 6. Automated SSL Certificate Provisioning & Fallback
echo "--> [6/8] Provisioning domain TLS certificate for ${TARGET_DOMAIN}..."
CERT_VALID=false
CERT_FILE="/usr/local/s-ui/certs/fullchain.pem"
KEY_FILE="/usr/local/s-ui/certs/privkey.pem"

# Check if current cert exists and is valid for at least 7 days
if [ -f "$CERT_FILE" ] && [ -f "$KEY_FILE" ]; then
  if openssl x509 -checkend 604800 -noout -in "$CERT_FILE" 2>/dev/null; then
    CERT_VALID=true
    echo "Existing SSL certificate is valid for > 7 days."
  fi
fi

# Check if Let's Encrypt cert exists elsewhere on host
if [ "$CERT_VALID" != "true" ]; then
  LE_DIR="/etc/letsencrypt/live/${TARGET_DOMAIN}"
  if [ -f "${LE_DIR}/fullchain.pem" ] && [ -f "${LE_DIR}/privkey.pem" ]; then
    echo "Found active Let's Encrypt certificate at ${LE_DIR}, copying..."
    cp -L "${LE_DIR}/fullchain.pem" "$CERT_FILE"
    cp -L "${LE_DIR}/privkey.pem" "$KEY_FILE"
    CERT_VALID=true
  fi
fi

# If still needed, try standalone Certbot if domain resolves to this server
if [ "$CERT_VALID" != "true" ] && [ -n "$TARGET_DOMAIN" ]; then
  SERVER_IP=$(curl -s4 --connect-timeout 5 https://api.ipify.org 2>/dev/null || curl -s4 --connect-timeout 5 https://icanhazip.com 2>/dev/null || echo "")
  DOMAIN_IP=$(python3 -c "import socket; print(socket.gethostbyname('${TARGET_DOMAIN}'))" 2>/dev/null || echo "")
  echo "Server IP: ${SERVER_IP}, ${TARGET_DOMAIN} IP: ${DOMAIN_IP}"
  if [ -n "$SERVER_IP" ] && [ "$SERVER_IP" = "$DOMAIN_IP" ]; then
    echo "Domain matches server IP. Requesting Let's Encrypt certificate via Certbot..."
    if command -v fuser &> /dev/null; then
      fuser -k 80/tcp 2>/dev/null || true
    fi
    if certbot certonly --standalone --non-interactive --agree-tos --register-unsafely-without-email -d "${TARGET_DOMAIN}" --keep-until-expiring; then
      cp -L "/etc/letsencrypt/live/${TARGET_DOMAIN}/fullchain.pem" "$CERT_FILE"
      cp -L "/etc/letsencrypt/live/${TARGET_DOMAIN}/privkey.pem" "$KEY_FILE"
      CERT_VALID=true
      echo "Let's Encrypt certificate successfully provisioned!"
    else
      echo "Certbot challenge encountered an issue, will fallback to ECC self-signed cert."
    fi
  else
    echo "Domain does not yet resolve directly to server IP (or CDN proxy active)."
  fi
fi

# Self-signed ECC fallback to guarantee TLS always boots
if [ "$CERT_VALID" != "true" ]; then
  if [ ! -f "$CERT_FILE" ] || [ ! -f "$KEY_FILE" ]; then
    echo "Generating fallback self-signed ECC certificate for ${TARGET_DOMAIN}..."
    openssl req -x509 -nodes -newkey ec -pkeyopt ec_paramgen_curve:secp384r1 -days 3650 \
      -keyout "$KEY_FILE" \
      -out "$CERT_FILE" \
      -subj "/CN=${TARGET_DOMAIN}"
    echo "Fallback self-signed certificate generated."
  fi
fi

chmod 600 "$KEY_FILE" 2>/dev/null || true
chmod 644 "$CERT_FILE" 2>/dev/null || true

# 7. Database settings initialization / migration
echo "--> [7/8] Applying SQLite configuration checks..."
DB_FILE="/usr/local/s-ui/db/s-ui.db"
if [ -f "$DB_FILE" ] && command -v sqlite3 &> /dev/null; then
  sqlite3 "$DB_FILE" "UPDATE settings SET value = '2053' WHERE key = 'webPort' AND (value = '2095' OR value = '');" 2>/dev/null || true
  sqlite3 "$DB_FILE" "UPDATE settings SET value = '/usr/local/s-ui/certs/fullchain.pem' WHERE key IN ('webCertFile', 'subCertFile') AND (value = '' OR value IS NULL);" 2>/dev/null || true
  sqlite3 "$DB_FILE" "UPDATE settings SET value = '/usr/local/s-ui/certs/privkey.pem' WHERE key IN ('webKeyFile', 'subKeyFile') AND (value = '' OR value IS NULL);" 2>/dev/null || true
  # Deployment is not a connectivity test. Preserve measured availability and
  # last_test_time; geographic metadata alone cannot establish node health.
fi

# Firewall rules
if command -v ufw &> /dev/null; then
  if ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw allow 2053/tcp 2>/dev/null || true
    ufw allow 2096/tcp 2>/dev/null || true
    ufw allow 80/tcp 2>/dev/null || true
    ufw allow 443/tcp 2>/dev/null || true
    # Multi-protocol inbound ports (vless/vmess/trojan/hy2/tuic/ss/mixed)
    ufw allow 54142:54179/tcp 2>/dev/null || true
    ufw allow 54153:54154/udp 2>/dev/null || true
    ufw allow 54170:54171/udp 2>/dev/null || true
    ufw allow 54178:54182/udp 2>/dev/null || true
  fi
fi

# Kernel & BBR optimizations
sysctl -w net.ipv6.conf.all.disable_ipv6=1 2>/dev/null || true
modprobe tcp_bbr 2>/dev/null || true
sysctl -w net.core.default_qdisc=fq 2>/dev/null || true
sysctl -w net.ipv4.tcp_congestion_control=bbr 2>/dev/null || true
sysctl -w net.core.rmem_max=16777216 2>/dev/null || true
sysctl -w net.core.wmem_max=16777216 2>/dev/null || true
sysctl -w net.ipv4.tcp_rmem="4096 87380 16777216" 2>/dev/null || true
sysctl -w net.ipv4.tcp_wmem="4096 65536 16777216" 2>/dev/null || true

# 8. Start s-ui service & Health verification
echo "--> [8/8] Starting s-ui service..."
systemctl start s-ui
sleep 3
systemctl status s-ui --no-pager

echo "=== Health Verification ==="
echo "Web Panel probe (port 2053):"
if curl -skf --connect-timeout 5 https://127.0.0.1:2053/app/ > /dev/null; then
  echo "  [OK] Web Panel (2053 HTTPS) responding"
elif curl -sf --connect-timeout 5 http://127.0.0.1:2053/app/ > /dev/null; then
  echo "  [OK] Web Panel (2053 HTTP) responding"
else
  echo "  [WARN] Web Panel probe failed"
fi

echo "Sub Service probe (port 2096):"
if curl -skf --connect-timeout 5 https://127.0.0.1:2096/sub/my > /dev/null; then
  echo "  [OK] Sub Service (2096 HTTPS) responding"
elif curl -sf --connect-timeout 5 http://127.0.0.1:2096/sub/my > /dev/null; then
  echo "  [OK] Sub Service (2096 HTTP) responding"
else
  echo "  [WARN] Sub Service probe failed"
fi

echo "Deployment and bootstrap completed successfully!"


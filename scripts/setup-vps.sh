#!/usr/bin/env bash
# =====================================================================
# setup-vps.sh — One-Click Setup Script for GoClaw on VPS
# Target Domain: hoangquy.name.vn
# Target VPS IP: 65.86.32.120
# Repo: https://github.com/quyhoang1104-dev/GaAuto.git
# =====================================================================
set -euo pipefail

DOMAIN="hoangquy.name.vn"
APP_DIR="/opt/goclaw"
REPO_URL="https://github.com/quyhoang1104-dev/GaAuto.git"

echo "========================================================"
echo "    BẮT ĐẦU CÀI ĐẶT GOCLAW TRÊN VPS ($DOMAIN)"
echo "========================================================"

# 1. Update system packages
echo "--> [1/8] Cập nhật hệ thống..."
sudo apt update && sudo apt upgrade -y
sudo apt install -y curl wget git ufw openssl ca-certificates gnupg lsb-release

# 2. Install Docker & Docker Compose if not installed
echo "--> [2/8] Kiểm tra và cài đặt Docker..."
if ! command -v docker &> /dev/null; then
  echo "Docker chưa có, đang cài đặt..."
  curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
  sudo sh /tmp/get-docker.sh
  sudo systemctl enable docker
  sudo systemctl start docker
else
  echo "Docker đã được cài đặt."
fi

# 3. Install Nginx & Certbot
echo "--> [3/8] Cài đặt Nginx và Certbot SSL..."
sudo apt install -y nginx certbot python3-certbot-nginx
sudo systemctl enable nginx
sudo systemctl start nginx

# 4. Configure UFW Firewall
echo "--> [4/8] Thiết lập tường lửa UFW..."
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp comment 'SSH'
sudo ufw allow 80/tcp comment 'HTTP'
sudo ufw allow 443/tcp comment 'HTTPS'
# Đảm bảo không mở port 18790 và 5432 ra ngoài internet
sudo ufw --force enable

# 5. Clone or Update Repository in /opt/goclaw
echo "--> [5/8] Thiết lập thư mục mã nguồn tại $APP_DIR..."
if [ -d "$APP_DIR/.git" ]; then
  echo "Thư mục $APP_DIR đã tồn tại, đang pull cập nhật..."
  cd "$APP_DIR"
  git fetch origin main
  git reset --hard origin/main
else
  echo "Clone repository từ $REPO_URL..."
  sudo mkdir -p "$APP_DIR"
  sudo chown -R "$USER":"$USER" "$APP_DIR"
  git clone "$REPO_URL" "$APP_DIR"
  cd "$APP_DIR"
fi

# Cấp quyền thực thi cho các file script
chmod +x "$APP_DIR/prepare-env.sh" "$APP_DIR/scripts/"*.sh || true

# 6. Generate .env file
echo "--> [6/8] Khởi tạo tệp cấu hình bảo mật .env..."
if [ ! -f "$APP_DIR/.env" ]; then
  ./prepare-env.sh
  echo "Đã sinh khóa bảo mật GOCLAW_GATEWAY_TOKEN & GOCLAW_ENCRYPTION_KEY."
else
  echo ".env đã tồn tại, giữ nguyên các khóa hiện có."
fi

# Lấy token để hiển thị sau khi hoàn tất
GATEWAY_TOKEN=$(grep -E "^GOCLAW_GATEWAY_TOKEN=" "$APP_DIR/.env" | cut -d'=' -f2-)

# 7. Configure Nginx & SSL Let's Encrypt
echo "--> [7/8] Cấu hình Nginx và chứng chỉ SSL cho $DOMAIN..."
NGINX_CONF="/etc/nginx/sites-available/$DOMAIN"

# Tạo cấu hình HTTP tạm thời để Certbot xác thực
sudo bash -c "cat > $NGINX_CONF" <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN www.$DOMAIN;

    location / {
        proxy_pass http://127.0.0.1:18790;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;

        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
        client_max_body_size 100M;
    }
}
EOF

sudo ln -sf "$NGINX_CONF" /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t
sudo systemctl reload nginx

# Đăng ký chứng chỉ SSL với Let's Encrypt
echo "Đang đăng ký chứng chỉ SSL qua Certbot..."
sudo certbot --nginx -d "$DOMAIN" -d "www.$DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --redirect || {
  echo "Lưu ý: Không thể cấp chứng chỉ cho www.$DOMAIN, thử cấp riêng cho $DOMAIN..."
  sudo certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --register-unsafely-without-email --redirect
}

# 8. Start Docker Containers
echo "--> [8/8] Khởi chạy các container GoClaw & PostgreSQL..."
cd "$APP_DIR"
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml pull
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml up -d

echo ""
echo "========================================================"
echo "    TRIỂN KHAI GOCLAW HOÀN TẤT THÀNH CÔNG!"
echo "========================================================"
echo " Domain:           https://$DOMAIN"
echo " Gateway Token:    $GATEWAY_TOKEN"
echo " Thư mục cài đặt:  $APP_DIR"
echo " Trạng thái:       $(docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml ps --format 'table {{.Service}}\t{{.Status}}')"
echo ""
echo "👉 BƯỚC TIẾP THEO:"
echo "1. Mở trình duyệt truy cập: https://$DOMAIN"
echo "2. Dán mã Gateway Token ở trên để đăng nhập vào GoClaw Dashboard."
echo "3. Vào Settings -> Providers để cấu hình API Key LLM mà bạn muốn dùng."
echo "========================================================"

# HƯỚNG DẪN TRIỂN KHAI GOCLAW LÊN VPS (DOMAIN: TOTSYSTEM.COM)

Tài liệu này hướng dẫn chi tiết từng bước để triển khai GoClaw lên máy chủ VPS của bạn (`65.86.32.120`), cấu hình tên miền `totsystem.com`, cài đặt SSL Let's Encrypt, và tự động hóa cập nhật qua GitHub Actions từ kho mã nguồn `https://github.com/HoangQuy1104/AI-tr-l-.git`.

---

## 1. Yêu Cầu Chuẩn Bị Trước Khi Chạy

1. **VPS**: Ubuntu 20.04/22.04/24.04 (hoặc Debian 11/12), IP `65.86.32.120`.
2. **DNS Domain**:
   - Bản ghi `@` (A record): Trỏ về `65.86.32.120` (Đã cấu hình).
   - Bản ghi `www` (A record hoặc CNAME): Khuyến nghị trỏ về `65.86.32.120`.
3. **Quyền truy cập SSH**: Quyền `root` hoặc user có quyền `sudo` trên VPS.

---

## 2. Các Bước Cài Đặt GoClaw Trên VPS (Chỉ Cần 1 Lệnh Tự Động)

### Cách 1: Chạy Script Tự Động Hoá (Khuyến Nghị - Nhanh Nhất)

Đăng nhập vào VPS qua SSH bằng terminal trên máy tính của bạn:
```bash
ssh root@65.86.32.120
```

Sau khi đăng nhập vào VPS, dán và chạy câu lệnh sau:
```bash
curl -fsSL https://raw.githubusercontent.com/HoangQuy1104/AI-tr-l-/main/scripts/setup-vps.sh | bash
```

> **Script sẽ tự động làm toàn bộ mọi việc:**
> 1. Cập nhật hệ điều hành và các gói bảo mật.
> 2. Cài đặt Docker và Docker Compose plugin.
> 3. Cài đặt Nginx và Certbot SSL.
> 4. Cấu hình tường lửa UFW (Mở cổng 22, 80, 443; đóng chặt cổng 18790 và 5432 khỏi internet).
> 5. Clone repository `HoangQuy1104/AI-tr-l-` vào `/opt/goclaw`.
> 6. Sinh file `.env` với các khóa mã hóa chuẩn `AES-256-GCM` và `Gateway Token`.
> 7. Cấu hình Nginx Virtual Host và cấp chứng chỉ SSL Let's Encrypt cho `totsystem.com`.
> 8. Khởi chạy 2 container `goclaw` và `postgres`.

---

### Cách 2: Cài Đặt Thủ Công Từng Bước (Nếu Muốn Tự Kiểm Soát)

Nếu bạn muốn chạy từng bước trên VPS:

```bash
# 1. Cập nhật hệ thống
sudo apt update && sudo apt upgrade -y
sudo apt install -y curl git ufw nginx certbot python3-certbot-nginx

# 2. Cài Docker nếu chưa có
curl -fsSL https://get.docker.com | sudo sh

# 3. Mở cổng tường lửa
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw --force enable

# 4. Clone repo về thư mục /opt/goclaw
sudo mkdir -p /opt/goclaw
sudo chown -R $USER:$USER /opt/goclaw
git clone https://github.com/HoangQuy1104/AI-tr-l-.git /opt/goclaw
cd /opt/goclaw

# 5. Cấp quyền và sinh .env
chmod +x prepare-env.sh scripts/*.sh
./prepare-env.sh

# 6. Khởi chạy Docker containers
docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml up -d

# 7. Cấu hình Nginx
sudo cp nginx/totsystem.com.conf /etc/nginx/sites-available/totsystem.com
sudo ln -sf /etc/nginx/sites-available/totsystem.com /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx

# 8. Cấp chứng chỉ SSL Let's Encrypt
sudo certbot --nginx -d totsystem.com -d www.totsystem.com --non-interactive --agree-tos --register-unsafely-without-email --redirect
```

---

## 3. Lấy Gateway Token & Đăng Nhập Sử Dụng

Khi truy cập vào `https://totsystem.com`, giao diện sẽ yêu cầu **Gateway Token** để xác thực đăng nhập.

Để xem token trên VPS, chạy lệnh:
```bash
cat /opt/goclaw/.env | grep GOCLAW_GATEWAY_TOKEN
```
*Kết quả dạng:*
```
GOCLAW_GATEWAY_TOKEN=4f8b2c1e7a90... (chuỗi 64 ký tự hex)
```

1. Mở trình duyệt truy cập: **`https://totsystem.com`**
2. Sao chép chuỗi mã token phía sau dấu `=` và dán vào ô đăng nhập.
3. Sau khi vào Dashboard:
   - Vào mục **Settings / Providers** (hoặc Setup Wizard hiển thị lần đầu).
   - Điền API Key của nhà cung cấp bạn có (OpenAI, Anthropic, OpenRouter, Google Gemini, Groq, v.v.).
   - Tạo Agent đầu tiên và bắt đầu sử dụng!

---

## 4. Cấu Hình CI/CD Tự Động Triển Khai Qua GitHub Actions

Mỗi khi bạn sửa code, thêm cấu hình hay cập nhật repo và push lên `https://github.com/HoangQuy1104/AI-tr-l-`, GitHub Actions sẽ tự động SSH vào VPS và cập nhật phiên bản mới nhất mà bạn không cần gõ lệnh thủ công.

### Các Bước Cấu Hình GitHub Secrets:

1. Truy cập vào kho GitHub của bạn:
   👉 **`https://github.com/HoangQuy1104/AI-tr-l-/settings/secrets/actions`**
2. Nhấn nút **New repository secret** và thêm 3 secrets sau:

| Tên Secret | Giá trị |
| :--- | :--- |
| `VPS_HOST` | `65.86.32.120` |
| `VPS_USERNAME` | `root` (hoặc username bạn dùng để đăng nhập VPS) |
| `VPS_SSH_KEY` | Nội dung Private SSH Key của bạn (nằm trong file `id_ed25519` hoặc `id_rsa`) |
| `VPS_PORT` | `22` (nếu bạn không đổi cổng SSH mặc định) |

> **Cách tạo cặp SSH Key trên VPS nếu bạn chưa có:**
> Trên VPS chạy:
> ```bash
> ssh-keygen -t ed25519 -C "github-actions" -f ~/.ssh/github_deploy -N ""
> cat ~/.ssh/github_deploy.pub >> ~/.ssh/authorized_keys
> chmod 600 ~/.ssh/authorized_keys
> cat ~/.ssh/github_deploy
> ```
> *Sao chép toàn bộ nội dung xuất ra từ lệnh `cat ~/.ssh/github_deploy` và dán vào `VPS_SSH_KEY` trên GitHub.*

Từ nay, bất cứ khi nào bạn commit và `git push origin main`, GitHub Actions sẽ tự động kích hoạt workflow và cập nhật GoClaw trên VPS của bạn!

---

## 5. Lệnh Quản Trị & Bảo Trì Hữu Ích Trên VPS

Tất cả các lệnh thực hiện tại thư mục `/opt/goclaw`:

- **Xem trạng thái các container:**
  ```bash
  cd /opt/goclaw && docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml ps
  ```
- **Xem log GoClaw thời gian thực (realtime logs):**
  ```bash
  docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml logs -f goclaw
  ```
- **Khởi động lại toàn bộ dịch vụ:**
  ```bash
  docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml restart
  ```
- **Dừng toàn bộ dịch vụ:**
  ```bash
  docker compose -f docker-compose.yml -f docker-compose.postgres.yml -f docker-compose.prod.yml down
  ```
- **Sao lưu dữ liệu PostgreSQL:**
  ```bash
  docker exec -t goclaw-postgres-1 pg_dumpall -c -U goclaw > /opt/goclaw/backup_$(date +%Y%m%d_%H%M%S).sql
  ```
- **Đồng bộ cập nhật từ GoClaw gốc (Upstream):**
  ```bash
  cd /opt/goclaw
  git fetch upstream
  git merge upstream/main
  ./scripts/deploy.sh
  ```

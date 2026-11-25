# auth: runbook vận hành

Cập nhật 2026-11-23. Dành cho người trực vận hành riêng `auth`; tự chứa đủ để vận hành `auth` mà không cần mở runbook khác. Mọi tên biến, lệnh và giá trị lấy từ mã hiện tại (`internal/config`, `internal/bootstrap/cli.go`, `deploy/postgres-init/01-schemas.sh`); **chưa mục nào được diễn tập trên PostgreSQL và cụm thật** (xem mục 11). Cấu hình từng biến ở [config.md](config.md); lý do thiết kế ở [architecture.md](architecture.md).

## 1. Kiểm tra sức khỏe

| Lệnh / endpoint | Ý nghĩa |
|---|---|
| `GET /health/live`, `/health/startup`, `/healthz` | Process còn sống (liveness) |
| `GET /health/ready` | `200` khi nhận được lưu lượng; `503` khi đang tắt hoặc DB hỏng. Dùng cho readiness |
| `auth healthcheck` | Gọi probe cục bộ; dùng cho `HEALTHCHECK` của Docker (image distroless không có curl) |

`/health/ready` kiểm DB trong `READINESS_TIMEOUT` (mặc định 2s). Nếu `503`: kiểm `DATABASE_URL`, số kết nối còn trống (`DATABASE_MAX_OPEN_CONNS`, mặc định 20), và cho PostgreSQL thì schema `identity` còn truy cập được.

## 2. Khởi động, tắt, triển khai

- **Tắt êm** (SIGTERM): `/health/ready` chuyển `503`, chờ `SHUTDOWN_DRAIN_DELAY` (5s) để load balancer rút instance, rồi chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT` (25s), thoát mã 0 (e2e `TestGracefulShutdownExitsZero` kiểm). Đặt `terminationGracePeriodSeconds`/`stop_grace_period` lớn hơn tổng hai giá trị.
- **Khởi động sai cấu hình:** service từ chối chạy và in **mọi** lỗi một lượt, nêu tên biến ([config.md](config.md), mục "Ràng buộc ở staging/production"). Sửa theo thông báo; **không hạ `ENVIRONMENT`** để né.
- **Còn biến `AUTHEN_*`:** từ chối khởi động và liệt kê tên `AUTH_*` mới cần đổi (service đã đổi tên 2026-11-22).
- **Rolling update:** đặt `DATABASE_AUTO_MIGRATE=false`, đặt `DATABASE_MIGRATE_URL` (role `identity_migrate`) và chạy `auth migrate up` ở một bước riêng **trước** khi cuốn chiếu app (role `identity_app` chỉ có DML). Instance khởi động trên schema chưa migrate **từ chối chạy** (`schema chưa được migrate`): đó là cơ chế an toàn, không phải lỗi.
- **Hạn mức nhiều replica:** mặc định mỗi replica đếm riêng (N replica cho tổng hạn mức gấp N lần `AUTH_RATE_LIMIT` và `AUTH_LOGIN_RATE_LIMIT`, nên hạn mức đăng nhập thực tế lỏng hơn). Đặt `AUTH_RATE_LIMIT_SHARED=true` kèm `AUTH_REDIS_URL` để mọi replica chung một bộ đếm trên Redis (khóa `auth:rl:*`). Redis lỗi hoặc chậm quá 200ms: replica đó tạm đếm riêng 5 giây rồi thử lại Redis (log WARN `ratelimit: Redis lỗi`).
- **Thứ tự khi nâng cấp cả cụm:** `auth` trước, rồi `fhir-server`, rồi `ws-gateway` (migration luôn chạy trước khi cuốn chiếu app).

```bash
auth migrate status      # số migration chưa áp dụng
auth migrate up          # áp dụng
auth migrate down        # lùi MỘT bước: KHÔNG chạy trên production (xóa dữ liệu của bảng bị gỡ)
```

Quay lại phiên bản app cũ: khôi phục từ sao lưu (mục 8), không dùng `migrate down`.

## 3. Cơ sở dữ liệu

### 3.1 Mô hình

`auth` dùng **schema riêng `identity`** với role `identity_app`; schema `fhir` của `fhir-server` nằm cùng database nhưng `auth` không đọc hay ghi được, và ngược lại (role này không có quyền gì trên schema kia, kể cả đọc). Không ai tạo được bảng ở `public`. Cả hai service dùng cùng tên bảng (`fhir_resource`, `fhir_history`, `fhir_idx_*`, `fhirstore_schema_version`), nên schema riêng là điều bắt buộc, không phải tùy chọn.

| Role | Schema | `DATABASE_URL` (dev) |
|---|---|---|
| `identity_app` | `identity` | `postgres://identity_app:<mk>@postgres:5432/trivita` |
| `identity_migrate` (chỉ khi tách role migrate, mục 3.4) | `identity` (sở hữu) | `DATABASE_MIGRATE_URL` |

Tên `identity`, `identity_app`, `identity_migrate` **không đổi** khi service đổi tên từ `authen` sang `auth`.

### 3.2 Khởi tạo

`deploy/postgres-init/01-schemas.sh` (chạy một lần khi volume Postgres khởi tạo lần đầu, qua `docker-entrypoint-initdb.d`) tạo role và schema, thu quyền của `PUBLIC`, đặt `search_path` ở mức **role** (không phụ thuộc DSN, đúng cả sau PgBouncer). Mật khẩu của `auth` lấy từ `IDENTITY_DB_PASSWORD` (mặc định chỉ để dev: `identity`; **đổi ở môi trường thật**, mã hóa URL nếu có ký tự đặc biệt).

```bash
# volume đã có sẵn (script chạy lại được, không mất dữ liệu):
docker compose exec postgres sh /docker-entrypoint-initdb.d/01-schemas.sh
# dev muốn bắt đầu lại từ đầu:
docker compose down --volumes && docker compose up --build
```

**Dữ liệu cũ không tự chuyển.** Nếu trước đây `auth` dùng database riêng hoặc schema `public`, dữ liệu đó vẫn ở đó và app mới sẽ thấy rỗng. Chuyển bằng `COPY` từng bảng, kiểm số dòng và tổng kiểm, giữ bản cũ 7 ngày (quy trình ở [microservices-shared-db.md](../../../docs/plans/microservices-shared-db.md) mục 6).

### 3.3 `DATABASE_REQUIRE_SCHEMA`

`DATABASE_REQUIRE_SCHEMA=true` (chỉ PostgreSQL) làm `auth` **từ chối khởi động** và từ chối cả `migrate` nếu `search_path` của kết nối không phải **đúng một** schema riêng đã tồn tại. Mặc định tắt để không phá cách chạy cũ; ở staging/production không bật thì ghi cảnh báo lúc khởi động. Bật với SQLite là lỗi cấu hình.

| Thông báo lúc khởi động | Nguyên nhân | Cách xử lý |
|---|---|---|
| `search_path "\"$user\", public" có nhiều schema` | Role chưa đặt `search_path` hoặc đăng nhập bằng superuser | Dùng role của service, hoặc `ALTER ROLE identity_app SET search_path = identity` |
| `search_path "identity, public" có nhiều schema` | Còn `public` trong đường tìm kiếm: bảng cùng tên ở `public` che bảng của service | Bỏ `public` khỏi `search_path` |
| `search_path "public" không phải schema riêng` | Trỏ thẳng vào `public` | Đặt schema riêng |
| `schema "identity" ... chưa tồn tại (hoặc role không có quyền dùng nó)` | Chưa chạy init, hoặc role không sở hữu/không có `USAGE` | Chạy `01-schemas.sh`; kiểm quyền |
| `RequireSchema chỉ áp dụng cho PostgreSQL` | Bật cờ với SQLite | Tắt cờ hoặc dùng PostgreSQL |

### 3.4 Role migrate riêng (production)

Mặc định (dev) một role sở hữu schema nên có cả DDL và tự migrate. Ở staging/production nên tách: một lỗ hổng SQL injection ở app không được phép `DROP`/`ALTER` bảng.

| Role | Quyền | Dùng để |
|---|---|---|
| `identity_migrate` | **Sở hữu** schema và mọi bảng; chạy DDL | Chỉ lệnh `auth migrate` ở bước deploy |
| `identity_app` | `USAGE` schema; `SELECT/INSERT/UPDATE/DELETE` trên mọi bảng, `USAGE/SELECT` trên sequence, **kể cả bảng do migration tương lai tạo** (`ALTER DEFAULT PRIVILEGES`); không DDL, không `TRUNCATE` | App đang chạy |

**Bật:** chạy `deploy/postgres-init/01-schemas.sh` với `DB_SEPARATE_MIGRATION_ROLES=true` và đủ các mật khẩu (`IDENTITY_DB_PASSWORD`, `IDENTITY_MIGRATE_DB_PASSWORD`, cùng hai mật khẩu của role `fhir_*` của service kia; chế độ này không có mật khẩu mặc định, thiếu là script dừng). Chạy lại được trên database đã có: chuyển chủ sở hữu bảng sang role migrate bằng `REASSIGN OWNED`, giữ nguyên dữ liệu. **Không** hỗ trợ chiều ngược lại.

**Cấu hình `auth`:** `DATABASE_URL` là role `identity_app`; `DATABASE_MIGRATE_URL` là role `identity_migrate` (chỉ lệnh `migrate` dùng); `DATABASE_AUTO_MIGRATE=false` (**bắt buộc**: đặt `DATABASE_MIGRATE_URL` mà để tự migrate là lỗi cấu hình); `DATABASE_REQUIRE_SCHEMA=true`. Mỗi lần deploy chạy `auth migrate up` (bằng role migrate) **trước** khi cuốn chiếu app mới; thử trên máy bằng `docker-compose.db-roles.yml` (tác vụ `auth-migrate` chạy trước app).

| Triệu chứng | Nguyên nhân | Xử lý |
|---|---|---|
| `schema chưa được migrate` | Chưa chạy `migrate up`, hoặc chạy trước khi init chuyển quyền | Chạy `auth migrate up` (thông báo này là đúng ở chế độ tách) |
| `failed to create version table ... permission denied for schema` khi khởi động | `DATABASE_AUTO_MIGRATE=true` nhưng role app không có DDL | Đặt `DATABASE_AUTO_MIGRATE=false`, migrate bằng role migrate |
| `DATABASE_MIGRATE_URL: đã dùng role migrate riêng thì app không tự migrate` | Đặt `DATABASE_MIGRATE_URL` mà `DATABASE_AUTO_MIGRATE` còn `true` | Đặt `DATABASE_AUTO_MIGRATE=false` |
| Migration mới tạo bảng mà app báo `permission denied for table` | Bảng không do role migrate tạo (ví dụ tạo bằng superuser) nên không có quyền mặc định | Tạo bảng bằng role migrate, hoặc `GRANT SELECT, INSERT, UPDATE, DELETE ON <bảng> TO identity_app` |

### 3.5 Kiểm tra trạng thái

```bash
# schema identity phải có đủ bảng và bảng phiên bản riêng; public không có bảng nào của service
psql -d trivita -c "select table_schema, count(*) from information_schema.tables where table_schema in ('identity','fhir','public') group by 1"
# role của service kia không thấy schema identity (mong đợi: permission denied)
PGPASSWORD=<mk> psql -U fhir_app -h <host> -d trivita -c "select 1 from identity.fhir_resource"
```

### 3.6 Số kết nối

Mỗi replica `auth` mở tối đa `DATABASE_MAX_OPEN_CONNS` (mặc định 20) kết nối tới **cùng một máy chủ** với các service khác: tổng của mọi replica của mọi service phải nhỏ hơn `max_connections` của PostgreSQL (mặc định 100) trừ phần dự phòng. Vượt thì hạ `DATABASE_MAX_OPEN_CONNS` hoặc đặt PgBouncer. **Chưa kiểm** hành vi của PgBouncer với `search_path`; vì vậy `search_path` được đặt ở mức role chứ không ở DSN.

### 3.7 `statement_timeout`

Script init chỉ đặt khi có `IDENTITY_STATEMENT_TIMEOUT` (ví dụ `30s`); để trống thì **không** đặt (cố ý không có mặc định: một timeout chưa đo có thể làm hỏng việc chạy lâu). Truy vấn của `auth` ngắn nên đây là role nên đặt trước; role migrate không bao giờ bị đặt timeout.

## 4. Cấu hình production, triển khai lần đầu và smoke test

### 4.1 Cấu hình bắt buộc ở staging/production

Danh sách đầy đủ và điều bị cấm ở [config.md](config.md) (mục "Ràng buộc"). Giá trị cần chuẩn bị:

| Biến | Giá trị / cách tạo |
|---|---|
| `AUTH_BASE_URL` | Địa chỉ công khai `https://auth.example.org`; cũng là `iss` của token, **đúng từng ký tự** với `FHIR_JWT_ISSUER` của `fhir-server` |
| `AUTH_SIGNING_KEY_PATH` | Thư mục khóa, sinh bằng `auth keys generate -alg RS256`; mount volume bền, quyền 0600 |
| `DATABASE_URL`, `DATABASE_MIGRATE_URL` | Mục 3.4 |
| `AUTH_REDIS_URL` | Bắt buộc |
| `AUTH_MFA_SECRET_KEYS` | `kid:base64(32 byte)`, ví dụ `k1:$(openssl rand -base64 32)`; khóa đầu mã hóa, mọi khóa giải mã |
| `AUTH_APP_BASE_URL` | https; có thì bật `/oauth2/authorize` và đăng nhập ngoài |
| `AUTH_EXTERNAL_STATE_KEY` | Bắt buộc khi có `AUTH_APP_BASE_URL`: `openssl rand -base64 32` (≥ 32 byte), mọi instance dùng chung |
| `AUTH_ALLOWED_ORIGINS` | Các origin https được gọi API từ trình duyệt; không dùng `*` |
| `AUTH_TRUSTED_PROXIES` | Dải IP của reverse proxy |
| `AUTH_MAIL_PROVIDER` | `smtp` để gửi thư thật (đặt lại mật khẩu, xác minh email, mã MFA, mời user): cần `AUTH_SMTP_HOST`, `AUTH_SMTP_PORT`, `AUTH_SMTP_FROM`, và `AUTH_SMTP_USERNAME`/`AUTH_SMTP_PASSWORD` nếu relay đòi. TLS bắt buộc (`AUTH_SMTP_ALLOW_PLAINTEXT` bị cấm); relay dùng CA riêng thì đặt `AUTH_SMTP_CA_FILE`. Mặc định `none`; `log` bị cấm vì in link chứa secret |
| `AUTH_EMAIL_ENABLED` | Mặc định `false`. Bật `POST /email/v1/send` cho admin project có tính năng `email`; cần `AUTH_MAIL_PROVIDER=smtp`. `AUTH_EMAIL_ALLOW_PROJECT_SMTP` mặc định tắt (mật khẩu SMTP project nằm dạng rõ trong `Project.secret`); `AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND` **phải `false`**. Đính kèm `Binary/<id>`: đặt `AUTH_EMAIL_FHIR_URL` (https). Route cho phép gửi thư tới người nhận tùy ý: chỉ bật khi cần, giữ `AUTH_EMAIL_MAX_RECIPIENTS` thấp, cân nhắc hạn mức ở reverse proxy |
| `AUTH_SCIM_ENABLED` | Mặc định `false`. Bật thì project phải có tính năng `scim`. Bật với Basic (mặc định, Okta cần) thì **bắt buộc** `AUTH_SCIM_ALLOWED_IPS` (CIDR của IdP), không thì từ chối khởi động |
| `AUTH_ADMIN_ENABLED` | Mặc định `false`. Chỉ bật khi cần quản trị qua HTTP, và hạn chế mạng truy cập `/admin/` ở reverse proxy |
| `AUTH_ALLOW_PRIVATE_JWKS` | **Phải `false`** (bật thì tắt chống SSRF; bị cấm) |
| `AUTH_GOOGLE_CLIENT_ID`, `AUTH_FHIRCAST_HUB_URL`, `AUTH_FHIR_BASE_URL` | Tùy chọn |

### 4.2 Triển khai lần đầu

Thứ tự: Postgres và Redis → khóa ký → migrate → `auth` → dữ liệu đầu tiên (mục 5).

```bash
# 0. biến môi trường: dùng --env-file hoặc kho bí mật; KHÔNG commit
# 1. Postgres (+ Redis) lên và healthy (init schema/role chạy lần đầu, mục 3.2)
# 2. sinh khóa ký (một lần; lưu volume bền)
docker run --rm -v auth-keys:/keys -e AUTH_SIGNING_KEY_PATH=/keys trivita-auth:local keys generate -alg RS256
# 3. migrate bằng role migrate (tác vụ một lần, chạy trước app)
docker compose ... run --rm auth-migrate
# 4. app
docker compose ... up -d auth
# 5. tạo project, admin, client: mục 5
```

Các file compose trong repo là **mẫu chạy thử** (địa chỉ `localhost`, cổng published). Ở production hãy sao chép rồi sửa: địa chỉ công khai https, volume khóa, tên mạng; đừng dùng nguyên mật khẩu mặc định của dev. `docker-compose.db-roles.yml` không có mật khẩu mặc định (thiếu biến thì compose dừng).

### 4.3 Smoke test sau khi triển khai

```bash
curl -fsS https://auth.example.org/health/ready                                       # 200
curl -fsS https://auth.example.org/.well-known/openid-configuration | jq '.issuer, .jwks_uri'
curl -fsS https://auth.example.org/.well-known/jwks.json | jq '.keys[].kid'
TOKEN=$(curl -fsS https://auth.example.org/oauth2/token -d grant_type=client_credentials \
  -d client_id=$CLIENT_ID --data-urlencode client_secret=$CLIENT_SECRET | jq -r .access_token)
```

Đạt khi: `ready` là 200; `issuer` **đúng từng ký tự** `FHIR_JWT_ISSUER` của `fhir-server`; JWKS có khóa; lấy được token. Nếu đã có `fhir-server`: `curl -H "Authorization: Bearer $TOKEN" <fhir>/fhir/R4/metadata` trả 200 và không token thì `/fhir/R4/Patient` trả 401.

Trước release (trên máy build, không phải máy chủ): `make ci`, `make test-auth-pg`, `make test-e2e`.

## 5. Dữ liệu khởi tạo và việc quản trị hằng ngày

Tất cả qua lệnh `auth ...` (mật khẩu chỉ nhận qua biến môi trường hoặc stdin, không qua tham số dòng lệnh; secret của client chỉ hiện **một lần**: lưu vào kho bí mật ngay). Chạy trong container: `docker compose ... run --rm auth <lệnh>`.

| Việc | Lệnh |
|---|---|
| Tạo project đầu tiên cùng admin (một transaction, lỗi giữa chừng hoàn tác hết) | `AUTH_SEED_ADMIN_PASSWORD=... auth seed project -name <tên> -admin-email <email> -admin-first <tên> -admin-last <họ>` |
| Tạo client OAuth | `auth client create -project <id> -name <tên> -redirect-uri <uri> -grant-type <g>` |
| Đặt lại mật khẩu (thu hồi mọi login của user) | `AUTH_NEW_PASSWORD=... auth user set-password -email <email> [-project <id>]` |
| Cấu hình IdP ngoài cho một tên miền email | `auth domain add -domain ... -authorize-url ... -token-url ... -client-id ... -jwks-url ... -issuer ...` (URL phải https, không nội bộ; secret IdP qua `AUTH_DOMAIN_IDP_CLIENT_SECRET` hoặc stdin) |
| Xoay secret của một client không gián đoạn | admin project gọi `POST /admin/projects/{projectId}/client/{clientId}/rotate-secret` (cần `AUTH_ADMIN_ENABLED`): secret cũ thành `retiringSecret` và vẫn dùng được; đổi nơi triển khai sang secret mới rồi gọi lại với `{"retireOnly":true}` để vô hiệu secret cũ. Token đã cấp không bị thu hồi; log `audit=true` không chứa secret |
| Đổi email đăng nhập của một user | admin project gọi `POST /admin/projects/{projectId}/members/{membershipId}/update-email` với `{"email":"..."}` (cần `AUTH_ADMIN_ENABLED`): ghi email, `emailVerified=false`, thu hồi mọi login và vô hiệu mọi link đặt lại mật khẩu, thư báo tới địa chỉ cũ. Chỉ user thuộc project (user phạm vi server dùng CLI/SCIM không được). Không đổi `telecom` của hồ sơ |
| Đặt hoặc đổi secret của project (khóa API, SMTP riêng) | admin project gọi `POST /admin/projects/{projectId}/secrets` (cần `AUTH_ADMIN_ENABLED`) với MẢNG ĐẦY ĐỦ: thay toàn bộ `Project.secret`; giá trị cũ không đọc lại được nên gửi `********` ở secret giữ nguyên, secret không nêu bị xóa. Secret `smtp*` chỉ nhận khi `AUTH_EMAIL_ALLOW_PROJECT_SMTP=true`. Log `audit=true` chỉ có tên secret |
| Chuyển user toàn cục thành user của một project (User/$rescope) | super admin gọi `POST /admin/super/users/{userId}/rescope` với `{"projectId":"..."}` (cần `AUTH_SUPER_ADMIN_ENABLED`). Điều kiện: user chưa gắn project, có membership và MỌI membership thuộc project đích, project đích chưa có user cùng email. Mọi login của user bị thu hồi; không có chiều ngược lại |
| Cho phép một project tạo project khác qua HTTP (Project/$init) | `auth project super -project <id>` (bỏ bằng `-disable`) và bật `AUTH_SUPER_ADMIN_ENABLED=true`; admin của project đó gọi `POST /admin/super/projects` (xem api-contract, mục admin). Cờ có hiệu lực ngay, ghi log `audit=true`; chỉ bật khi cần, tắt cờ môi trường khi xong |
| Mở khóa tài khoản bị khóa do đăng nhập mật khẩu sai nhiều lần | `auth user unlock -email <email> [-project <id>]` (cần `AUTH_REDIS_URL`: không có Redis thì trạng thái khóa nằm trong bộ nhớ của service đang chạy, CLI không với tới; khi đó đặt lại mật khẩu hoặc chờ hết `AUTH_LOCKOUT_DURATION`). Đặt lại mật khẩu cũng mở khóa. Khóa tự hết sau `AUTH_LOCKOUT_DURATION` (15 phút) |
| Bật hoặc tắt tính năng project (ví dụ SCIM) | `auth project feature -project <id> -feature scim [-disable]` |
| Dọn login cũ (cắt cả refresh token của chúng) | `auth purge-logins -before <ngày> -dry-run` (đếm trước), rồi `-yes` để xóa **vĩnh viễn** |
| Khóa ký | `auth keys generate [-alg RS256\|ES256\|ES384] [-dir <thư mục>]`, `auth keys list` |

Sau khi có admin, quản trị tiếp có thể qua `/admin/projects/*` nếu bật `AUTH_ADMIN_ENABLED` (hạn chế mạng truy cập `/admin/` ở reverse proxy).

## 6. Xoay khóa và bí mật

| Bí mật | Cách xoay không gián đoạn | Ảnh hưởng |
|---|---|---|
| Khóa ký token (`AUTH_SIGNING_KEY_PATH`) | `auth keys generate` (khóa mới vào cùng thư mục) → triển khai để JWKS có **cả hai** khóa → đợi quá TTL cache của người xác minh (**10 phút**) → đặt `AUTH_ACTIVE_KID` = kid mới, triển khai → khi mọi token cũ hết hạn thì xóa file khóa cũ | `fhir-server` tự nhận khóa mới. Access token sống `AUTH_ACCESS_TTL` (15 phút), nhưng refresh token (14 ngày) vẫn cần khóa cũ nếu còn file: chỉ xóa khi chấp nhận buộc đăng nhập lại |
| `AUTH_MFA_SECRET_KEYS` | Thêm khóa mới **lên đầu** danh sách (`kid:base64(32 byte)`); giữ khóa cũ để giải mã; triển khai | Chưa có lệnh mã hóa lại hàng loạt, nên giữ khóa cũ vô thời hạn trừ khi tự mã hóa lại |
| `AUTH_EXTERNAL_STATE_KEY` | Đổi đồng loạt mọi instance | Các lần đăng nhập ngoài đang dở (≤ 10 phút) phải làm lại |
| Mật khẩu role DB | `ALTER ROLE ... PASSWORD`, cập nhật Secret, khởi động lại | |
| `clientSecret` | Tạo client mới (hoặc đổi `secret` qua `/admin/projects/{id}/client`), chuyển ứng dụng sang client mới, xóa client cũ | |
| Mật khẩu SMTP, khóa reCAPTCHA | Đổi ở kho bí mật rồi khởi động lại | |

Diễn tập từng dòng trên staging trước khi làm trên production.

## 7. Định cỡ và giới hạn mức

- **Scale:** theo số lượt đăng nhập và cấp token (stateless; trạng thái ở DB và Redis). Chưa có số đo (`bench`) cho `auth`; **chưa có định cỡ**.
- **`AUTH_TRUSTED_PROXIES`:** khai dải IP reverse proxy. Thiếu thì mọi yêu cầu thấy cùng IP của proxy, nên hạn mức theo IP làm **mọi người dùng bị `429` cùng lúc**. IP còn quyết định `ipAccessRule`, nên đặt sai là lỗ hổng.
- **Hạn mức theo IP đếm trong bộ nhớ từng instance:** chạy N replica thì hạn mức IP nhân N. Riêng hạn mức thử MFA và chống dùng lại mã MFA dùng Redis chung (`AUTH_REDIS_URL`).
- **Chưa có khóa tài khoản** theo số lần đăng nhập sai (chỉ có hạn mức theo IP và theo login/user cho MFA).

## 8. Sao lưu và khôi phục

- **Sao lưu:** `pg_dump` (hoặc snapshot/PITR) của schema `identity`, **và** thư mục khóa `AUTH_SIGNING_KEY_PATH`, **và** `AUTH_MFA_SECRET_KEYS`. Lưu khóa và `AUTH_MFA_SECRET_KEYS` **riêng** khỏi bản sao DB, trong kho bí mật.
- **Mất thư mục khóa:** mọi token đã cấp mất hiệu lực; sinh khóa mới (`auth keys generate`), người dùng đăng nhập lại.
- **Mất `AUTH_MFA_SECRET_KEYS`:** mọi secret TOTP đã mã hóa không giải mã được; người dùng phải đăng ký lại MFA.
- **Khôi phục:** vào database trống, khởi tạo schema và role (mục 3.2), nạp dump, chạy `auth migrate status` (phải 0 chưa áp dụng với cùng phiên bản app), rồi khởi động.
- Redis không phải nguồn sự thật: mất Redis làm mất trạng thái tạm (jti đã thấy, hạn mức thử MFA, secret MFA được mời chưa xác nhận, topic FHIRcast) nhưng **không mất tài khoản**.

## 9. Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Từ chối khởi động, in tên biến | Ràng buộc staging/production (thiếu `AUTH_SIGNING_KEY_PATH`, `AUTH_REDIS_URL`, `AUTH_MFA_SECRET_KEYS`, `AUTH_EXTERNAL_STATE_KEY`; URL không https; cờ dev đang bật) | Đặt đúng biến theo thông báo |
| `fhir-server` trả `401` cho token vừa cấp | `FHIR_JWT_ISSUER`/`FHIR_JWT_AUDIENCE` lệch `AUTH_BASE_URL` (hoặc `AUTH_ISSUER`) dù một ký tự (dấu `/` cuối, http/https); `FHIR_JWKS_URL` không với tới được `auth` | So `iss` trong token với biến; `curl` JWKS từ trong container `fhir-server` |
| Token cũ hỏng sau khi `auth` khởi động lại | Dùng khóa tạm trong bộ nhớ (thiếu `AUTH_SIGNING_KEY_PATH`, chỉ dev) hoặc mất volume khóa | Dev: bình thường. Production: kiểm volume khóa |
| `schema chưa được migrate` | Chưa chạy `migrate up` | Migrate bằng role migrate (mục 2 và 3.4) |
| `permission denied for schema` khi khởi động | `DATABASE_AUTO_MIGRATE=true` nhưng role app không có DDL | Đặt `DATABASE_AUTO_MIGRATE=false`, migrate bằng role migrate |
| `search_path ... có nhiều schema` / `schema "identity" ... chưa tồn tại` | `DATABASE_REQUIRE_SCHEMA=true` mà role chưa cấu hình đúng | Mục 3.3 |
| Mọi người dùng `429` cùng lúc | Thiếu `AUTH_TRUSTED_PROXIES` | Khai dải IP proxy |
| Không nhận được thư đặt lại mật khẩu, xác minh, mời | `AUTH_MAIL_PROVIDER=none` (mặc định ở production); hoặc SMTP lỗi: xem log `mail: gửi thất bại` (chỉ có host, cổng, mã SMTP; không có địa chỉ hay nội dung) | Đặt `smtp` và `AUTH_SMTP_*`; tạm dùng `auth user set-password` hoặc `/admin/projects/setpassword`. Lỗi `TLS`/`certificate`: relay dùng CA riêng thì đặt `AUTH_SMTP_CA_FILE`; lỗi `không hỗ trợ TLS`: relay thiếu STARTTLS (đừng bật plaintext ở production) |
| Đăng nhập ngoài báo `Invalid state` | Cookie `auth-external-state` không tới (khác trình duyệt, hết 10 phút, bị chặn); nhiều instance khác `AUTH_EXTERNAL_STATE_KEY` | Dùng chung một khóa; kiểm `AUTH_COOKIE_SECURE` và https |
| `/oauth2/authorize` trả `404` | Chưa đặt `AUTH_APP_BASE_URL` | Đặt (https ở production) |
| `/scim/v2` hoặc `/admin/...` trả `404` | Chưa bật `AUTH_SCIM_ENABLED`/`AUTH_ADMIN_ENABLED`, hoặc project chưa bật tính năng `scim` | [api-contract.md mục 5](api-contract.md) |
| Người dùng báo mật khẩu bị từ chối dù mạnh | Kiểm rò rỉ HIBP: lỗi gọi API cũng tính là từ chối (fail đóng) | Kiểm kết nối ra `api.pwnedpasswords.com` (hoặc `AUTH_PWNED_URL`) và `AUTH_PWNED_TIMEOUT`; **không** tắt `AUTH_PWNED_ENABLED` ở production (bị cấm) |
| Bệnh nhân nhận `403` ở mọi yêu cầu ngay sau khi bật cờ | `fhir-server` còn bản cũ, báo "nhiều hơn một rule" (compartment) | Nâng cấp mọi `fhir-server` trước (mục 12) hoặc tắt cờ |
| Bệnh nhân nhận `404` ở mọi yêu cầu (compartment) | Chưa có `Patient` cùng id trong `fhir-server` | `PUT /fhir/R4/Patient/<id>` (mục 12) |
| Log WARN `access policy cannot be issued as a token claim`; loại tài nguyên bị cấm | Thiếu tham số `patient` ở `ProjectMembership.access`, giá trị `%patient` sai định dạng, hoặc RelatedPerson bị `interaction` thu hẹp | Sửa membership/AccessPolicy; xem [api-contract.md](api-contract.md#claim-fhir_access_policy) |
| Token đã thu hồi vẫn gọi được FHIR một lúc | `fhir-server` chỉ kiểm chữ ký, không tra Login mỗi yêu cầu | Chờ tới hết `AUTH_ACCESS_TTL` (15 phút); hạ TTL nếu cần cửa sổ ngắn hơn |

## 10. Cần theo dõi

Quan sát qua `pkg/observability` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`), tên telemetry `trivita-auth`. Chưa có tài liệu metric riêng cho `auth` (khác `ws-gateway`, có metric OTLP `ws_gateway.*`); log là nguồn chính khi điều tra.

## 11. Giới hạn đã biết

1. Chưa diễn tập sao lưu và khôi phục, xoay khóa, hay các lệnh ở mục 2, 5, 6 trên PostgreSQL và cụm thật.
2. Chưa có định cỡ và kiểm tải cho `auth`. Không có manifest K8s: dự án chỉ triển khai bằng Docker và Docker Compose (quyết định của chủ dự án).
3. Hạn mức IP theo từng instance; chưa có khóa tài khoản theo số lần đăng nhập sai.
4. Thu hồi token có độ trễ tới hết `AUTH_ACCESS_TTL` ở `fhir-server`.
5. Chưa có hàng đợi hoặc thử lại khi gửi thư (gửi lỗi chỉ ghi log và bỏ qua với luồng công khai).

## 12. Bật compartment Patient

Cờ `AUTH_PATIENT_COMPARTMENT_ENABLED` (mặc định `false`) cho phép auth phát claim `fhir_access_policy` giới hạn bệnh nhân vào compartment của chính họ (`_compartment=Patient/<id>`, nhiều rule cùng loại là OR). Tắt thì token hành xử như trước.

**Thứ tự bắt buộc:**
1. Nâng cấp **mọi** instance `fhir-server` trước. Kiểm: `GET /fhir/R4/Observation?_compartment=Patient/x` trả `200` (bản cũ trả `400`). Token mới gặp bản cũ sẽ bị `403` vì "nhiều hơn một rule".
2. Với mỗi bệnh nhân, tạo `Patient` **cùng id** trong `fhir-server`: hồ sơ Patient mời ở auth nằm trong schema `identity`, không tự sang schema `fhir`. Dùng `PUT /fhir/R4/Patient/<id>` (cần `FHIR_UPDATE_AS_CREATE=true` ở `fhir-server`). Thiếu thì mọi truy cập của bệnh nhân trả `404`.
3. Đặt `AUTH_PATIENT_COMPARTMENT_ENABLED=true` rồi khởi động lại auth. Token cũ vẫn dùng được tới hết `AUTH_ACCESS_TTL` (tối đa 15 phút).
4. Kiểm: đăng nhập bằng một bệnh nhân, đọc `Patient/<id>` của mình (`200`), `Observation?_total=accurate` chỉ đếm bản ghi của mình, đọc `Patient/<id khác>` (`403` hoặc `404`).

**Hành vi ghi:** AccessPolicy Patient mặc định **không** chỉ đọc. Bệnh nhân tạo được bản ghi thuộc compartment của mình (ví dụ `Observation` cho chính mình: `201`), sửa được `Patient` của mình (`200`); ghi sang bệnh nhân khác bị `403` (hoặc `404`). Muốn chỉ đọc, đặt `readonly: true` trong AccessPolicy. Đã có e2e giữ nguyên các hành vi này (`e2e/patient_compartment_test.go`).

**Rollback:** đặt cờ về `false` và khởi động lại auth; token đã phát hết hạn trong tối đa 15 phút. Không cần hạ cấp `fhir-server`.

Sự cố liên quan: xem các dòng "compartment" ở mục 9.

# Runbook triển khai cả cụm (`auth`, `fhir-server`, `ws-gateway`)

Phạm vi: dựng, nâng cấp, xoay khóa, sao lưu và xử lý sự cố của cả cụm bằng Docker và Docker Compose. **Không có Kubernetes** (quyết định của chủ dự án). Chi tiết riêng từng service nằm ở [runbook.md](runbook.md) (`fhir-server`, `ws-gateway`, cơ sở dữ liệu dùng chung) và [apps/auth/README.md](../apps/auth/README.md) (bảng biến môi trường của `auth`). Tài liệu này là thứ tự các bước và các điều bắt buộc ở staging/production.

Trạng thái kiểm chứng: compose `auth + fhir-server + postgres` đã chạy thử bằng Docker (2026-11-21); toàn bộ luồng nhiều tiến trình kiểm bằng `make test-e2e`; `auth` trên PostgreSQL thật kiểm bằng `make test-auth-pg`. **Chưa** kiểm: chạy cụm đầy đủ ba service trên máy chủ thật, kiểm tải, sao lưu/khôi phục diễn tập, CI.

---

## 1. Sơ đồ và nguyên tắc

```
Trình duyệt / ứng dụng
   │ https                         │ https                     │ wss
   ▼                               ▼                           ▼
 auth (:8000) ── JWKS ─────────► fhir-server (:8080) ──────► ws-gateway (:8007)
   │  ký token (khóa riêng)         │  chỉ kiểm chữ ký           │  token bind do fhir-server cấp
   ▼                                ▼                            ▼
 PostgreSQL schema `identity`    PostgreSQL schema `fhir`     Redis (sự kiện, hiện diện)
```

- Mỗi service một image, một binary, cấu hình hoàn toàn bằng biến môi trường. Service không import code của nhau; chỉ nói chuyện qua HTTP, JWKS, Redis.
- `auth` giữ **khóa riêng**. `fhir-server` chỉ biết `FHIR_JWKS_URL`; khóa riêng không đi qua mạng.
- Một PostgreSQL, mỗi service một schema và một role riêng; role không thấy schema của service kia.
- Mọi service dùng cùng cơ chế: `ENVIRONMENT=staging|production` bật các ràng buộc chặt (cấm SQLite, bắt buộc https, cấm cờ dev). Cấu hình sai thì **service từ chối khởi động** và in rõ biến nào sai; đó là hành vi mong muốn.

## 2. Điều kiện trước khi triển khai

| Việc | Ghi chú |
|---|---|
| Docker 24+ và Compose v2 trên máy chủ | |
| PostgreSQL 15+ (compose có sẵn; production nên dùng bản quản lý riêng nếu có) | Với bản riêng: tự chạy `deploy/postgres-init/01-schemas.sh` (mục 4) |
| Redis | Bắt buộc ở staging/production cho `auth` (hạn mức MFA, `jti`, topic FHIRcast) và `ws-gateway` |
| TLS và tên miền công khai | Dừng TLS ở reverse proxy; mọi `AUTH_BASE_URL`, `AUTH_APP_BASE_URL`, origin phải là `https://` |
| Reverse proxy đặt `X-Forwarded-For` | Phải khai `AUTH_TRUSTED_PROXIES`, `FHIR_TRUSTED_PROXIES`, `WS_TRUSTED_PROXIES`, nếu không mọi người dùng chung một hạn mức theo IP |
| Mã nguồn của `trivita-observability-go` ở thư mục cạnh repo | Dockerfile nhận qua `--build-context observability=../trivita-observability-go` (`make docker` làm sẵn) |

Mật khẩu, khóa và secret: sinh bằng `openssl rand`, lưu trong kho bí mật của bạn. **Không** đặt vào argv, không commit, không để trong log.

## 3. Build image

```bash
make docker APP=auth
make docker APP=fhir-server
make docker APP=ws-gateway
# kiểm
docker image ls | grep trivita
```

Image chạy non-root (UID 65532), không shell, có `HEALTHCHECK`. Gắn tag theo phiên bản/commit khi đẩy lên registry; compose hiện dùng tag `local`.

## 4. Cơ sở dữ liệu

Chế độ khuyến nghị cho production: **role migrate riêng** (D-DB-4). Role `*_app` chỉ có DML nên một lỗ SQL injection không `DROP`/`ALTER` được bảng; role `*_migrate` chỉ dùng ở bước deploy.

1. Sinh bốn mật khẩu: `IDENTITY_DB_PASSWORD`, `FHIR_DB_PASSWORD`, `IDENTITY_MIGRATE_DB_PASSWORD`, `FHIR_MIGRATE_DB_PASSWORD`.
2. Khởi tạo schema và role (chạy một lần, chạy lại được, không mất dữ liệu):
   ```bash
   DB_SEPARATE_MIGRATION_ROLES=true IDENTITY_DB_PASSWORD=... FHIR_DB_PASSWORD=... \
   IDENTITY_MIGRATE_DB_PASSWORD=... FHIR_MIGRATE_DB_PASSWORD=... \
   sh deploy/postgres-init/01-schemas.sh
   ```
   Chế độ này không có mật khẩu mặc định; thiếu biến nào script dừng. Không hỗ trợ chiều ngược lại (từ tách về một role).
3. Cấu hình app: `DATABASE_URL` là role `*_app`; `DATABASE_MIGRATE_URL` là role `*_migrate`; `DATABASE_AUTO_MIGRATE=false` (**bắt buộc** khi có `DATABASE_MIGRATE_URL`); `DATABASE_REQUIRE_SCHEMA=true`.
4. Mỗi lần deploy chạy migration **trước** khi rollout app mới (mục 6).

Kiểm sau khởi tạo (mỗi schema đủ bảng, role không thấy schema kia): xem [runbook.md mục 12.3](runbook.md).

## 5. Cấu hình bắt buộc ở staging/production

Đặt `ENVIRONMENT=production` (hoặc `staging`) cho cả ba service.

### 5.1 `auth`

| Biến | Giá trị / cách tạo |
|---|---|
| `AUTH_BASE_URL` | địa chỉ công khai `https://auth.example.org`. Cũng là `iss` của token, **đúng từng ký tự** với `FHIR_JWT_ISSUER` |
| `AUTH_SIGNING_KEY_PATH` | thư mục khóa; sinh bằng `auth keys generate -alg RS256` (mục 7.1). Mount volume bền, quyền 0600 |
| `DATABASE_URL`, `DATABASE_MIGRATE_URL` | mục 4 |
| `AUTH_REDIS_URL` | bắt buộc |
| `AUTH_MFA_SECRET_KEYS` | `kid:base64(32 byte)`, ví dụ `k1:$(openssl rand -base64 32)`; khóa đầu mã hóa, mọi khóa giải mã |
| `AUTH_APP_BASE_URL` | https; có thì bật `/oauth2/authorize` và đăng nhập ngoài |
| `AUTH_EXTERNAL_STATE_KEY` | bắt buộc khi có `AUTH_APP_BASE_URL`: `openssl rand -base64 32` (≥ 32 byte), mọi instance dùng chung |
| `AUTH_ALLOWED_ORIGINS` | các origin https được gọi API từ trình duyệt; không dùng `*` |
| `AUTH_TRUSTED_PROXIES` | dải IP của reverse proxy |
| `AUTH_MAIL_PROVIDER` | `smtp` để gửi thư thật (đặt lại mật khẩu, xác minh email, mã MFA qua email, mời user): cần `AUTH_SMTP_HOST`, `AUTH_SMTP_PORT`, `AUTH_SMTP_FROM`, và `AUTH_SMTP_USERNAME`/`AUTH_SMTP_PASSWORD` nếu relay đòi đăng nhập. TLS bắt buộc (`AUTH_SMTP_ALLOW_PLAINTEXT` bị cấm ở production); relay nội bộ dùng chứng chỉ tự cấp thì đặt `AUTH_SMTP_CA_FILE`. Mặc định `none` (bỏ qua thư); `log` bị cấm vì in link chứa secret |
| `AUTH_EMAIL_ENABLED` | mặc định `false`; bật `POST /email/v1/send` cho admin project có tính năng `email`. Cần `AUTH_MAIL_PROVIDER=smtp`. `AUTH_EMAIL_ALLOW_PROJECT_SMTP` mặc định tắt (mật khẩu SMTP project nằm dạng rõ trong `Project.secret`); `AUTH_EMAIL_ALLOW_UNSAFE_OUTBOUND` **phải `false`** (production cấm). Đính kèm `Binary/<id>`: đặt `AUTH_EMAIL_FHIR_URL=https://<fhir-server nội bộ>/fhir/R4` (https ở production); `fhir-server` phải bật `FHIR_BINARY_STORAGE` hoặc còn Binary kiểu cũ, và token của người gọi phải được `fhir-server` chấp nhận (cùng issuer/audience/JWKS). Route này cho phép gửi thư tới người nhận tùy ý: chỉ bật khi cần, giữ `AUTH_EMAIL_MAX_RECIPIENTS` thấp, và cân nhắc hạn mức ở reverse proxy |
| `AUTH_SCIM_ENABLED` | mặc định `false`; bật thì project phải có tính năng `scim` (`auth project feature -project <id> -feature scim`). Bật với Basic (mặc định, Okta cần) thì **bắt buộc** `AUTH_SCIM_ALLOWED_IPS` (CIDR của IdP) ở production, không thì auth từ chối khởi động |
| `AUTH_ADMIN_ENABLED` | mặc định `false`; chỉ bật khi cần quản trị qua HTTP, và hạn chế mạng truy cập `/admin/` ở reverse proxy |
| `AUTH_ALLOW_PRIVATE_JWKS` | **phải `false`** (tắt chống SSRF nếu bật; production cấm) |
| `AUTH_GOOGLE_CLIENT_ID` | tùy chọn: bật `/auth/google` |
| `AUTH_FHIRCAST_HUB_URL`, `AUTH_FHIR_BASE_URL` | tùy chọn |

### 5.2 `fhir-server`

| Biến | Giá trị |
|---|---|
| `FHIR_BASE_URL` | địa chỉ công khai `https://fhir.example.org/fhir/R4` |
| `FHIR_JWT_ISSUER` | = `AUTH_BASE_URL` (từng ký tự) |
| `FHIR_JWT_AUDIENCE` | = `AUTH_AUDIENCE` (mặc định chính issuer) |
| `FHIR_JWKS_URL` | URL JWKS của `auth` mà **fhir-server với tới được** (có thể là địa chỉ nội bộ `http://auth:8000/.well-known/jwks.json`, khác địa chỉ công khai) |
| `FHIR_JOB_SIGNING_KEY` | ≥ 32 ký tự, mọi replica dùng chung (`openssl rand -hex 24`) |
| `FHIR_READ_TOKENS`, `FHIR_WRITE_TOKENS`, `FHIR_ADMIN_TOKENS` | đặt rỗng khi dùng JWT (compose đã làm); `FHIR_AUTH_DISABLED=false` |
| `FHIR_JWT_HMAC_SECRET` | **không dùng** ở production, chỉ `FHIR_JWKS_URL` |
| `FHIR_ALLOWED_ORIGINS`, `FHIR_TRUSTED_PROXIES` | như `auth` |
| `FHIR_SUBSCRIPTIONS=true`, `FHIR_WS_MODE=issuer`, `BROKER_DRIVER=redis`, `REDIS_URL`, `FHIR_WS_SIGNING_KEY`, `FHIR_WS_URL` | chỉ khi dùng websocket tách riêng (mục 5.3) |
| `FHIR_WEBHOOK_ALLOW_PRIVATE` | **phải `false`** |
| `FHIR_BINARY_STORAGE=file:<thư mục>`, `FHIR_STORAGE_SIGNING_KEY_PATH`, `FHIR_STORAGE_BASE_URL` | chỉ khi dùng Binary thô và `/storage`. Khóa ký: `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out storage.pem && chmod 600 storage.pem` (tạo trước, mount vào container). **Thư mục kho là ổ đĩa của một replica**: không chạy nhiều replica với `file:`; nhiều replica dùng `FHIR_BINARY_STORAGE=s3:<bucket>[/<prefix>]` kèm `FHIR_S3_REGION`, `FHIR_S3_ACCESS_KEY_ID`, `FHIR_S3_SECRET_ACCESS_KEY` (và `FHIR_S3_ENDPOINT` https cho MinIO/Ceph; khóa IAM tối thiểu: `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject` trên `<bucket>/<prefix>*`; chưa hỗ trợ IMDS/IRSA). Với S3 không cần sao lưu ổ đĩa kho, nhưng cần bật versioning/sao lưu bucket và chính sách vòng đời riêng. Xoay khóa: đặt khóa mới ở `FHIR_STORAGE_SIGNING_KEY_PATH` và khóa công khai cũ (`openssl pkey -in old.pem -pubout`) ở `FHIR_STORAGE_SIGNING_PREVIOUS_KEYS` cho tới khi URL cũ hết hạn (tối đa `FHIR_STORAGE_URL_TTL`). Sao lưu thư mục kho cùng CSDL |

### 5.3 `ws-gateway`

| Biến | Giá trị |
|---|---|
| `FHIR_WS_SIGNING_KEY` | **giống hệt** khóa của `fhir-server` (≥ 32 ký tự, không có giá trị mặc định) |
| `REDIS_URL` | cùng Redis với `fhir-server`, đặt tường minh |
| `WS_ALLOWED_ORIGINS`, `WS_TRUSTED_PROXIES`, `WS_MAX_CONNECTIONS`, `WS_RATE_LIMIT` | định cỡ theo [runbook.md mục 11.1](runbook.md) |
| `FHIRCAST_*` | chỉ khi bật FHIRcast: `FHIRCAST_PUBLIC_URL`, `FHIRCAST_JWT_ISSUER`, `FHIRCAST_JWT_AUDIENCE` đều bắt buộc, khớp `auth` |

`FHIR_WS_URL` (ở `fhir-server`) là địa chỉ **websocket công khai** của gateway (`wss://ws.example.org/ws/subscriptions-r4`), khác địa chỉ nội bộ giữa các container.

## 6. Triển khai lần đầu

Thứ tự: DB → migrate → auth → fhir-server → ws-gateway → kiểm.

```bash
# 0. biến môi trường: dùng --env-file hoặc kho bí mật; KHÔNG commit
# 1. Postgres (+ Redis) lên và healthy
docker compose -f docker-compose.yml -f docker-compose.auth.yml -f docker-compose.ws.yml \
  -f docker-compose.db-roles.yml up -d postgres redis

# 2. sinh khóa ký của auth (một lần; lưu volume bền)
docker run --rm -v auth-keys:/keys -e AUTH_SIGNING_KEY_PATH=/keys trivita-auth:local keys generate -alg RS256

# 3. migrate bằng role migrate (tác vụ một lần, chạy trước app)
docker compose ... run --rm auth-migrate
docker compose ... run --rm fhir-migrate

# 4. app
docker compose ... up -d auth
docker compose ... up -d fhir-server
docker compose ... up -d ws-gateway

# 5. tạo dữ liệu đầu tiên (project, admin, client): mục 8
```

Các file compose là **mẫu chạy thử** (có địa chỉ `localhost`, cổng published). Ở production hãy sao chép rồi sửa: địa chỉ công khai https, volume khóa, tên mạng; đừng dùng nguyên mật khẩu mặc định của dev. Chế độ `docker-compose.db-roles.yml` không có mật khẩu mặc định (thiếu biến thì compose dừng).

## 7. Kiểm sau khi triển khai (smoke test)

Thay địa chỉ cho đúng môi trường.

```bash
# 1. sức khỏe (ready = DB và trạng thái nhận traffic)
curl -fsS https://auth.example.org/health/ready
curl -fsS https://fhir.example.org/healthz
curl -fsS https://ws.example.org/health/ready

# 2. auth công bố khóa và discovery khớp cấu hình fhir-server
curl -fsS https://auth.example.org/.well-known/openid-configuration | jq '.issuer, .jwks_uri'
curl -fsS https://auth.example.org/.well-known/jwks.json | jq '.keys[].kid'

# 3. token -> fhir-server (client_credentials của client đã tạo ở mục 8)
TOKEN=$(curl -fsS https://auth.example.org/oauth2/token -d grant_type=client_credentials \
  -d client_id=$CLIENT_ID --data-urlencode client_secret=$CLIENT_SECRET | jq -r .access_token)
curl -fsS -H "Authorization: Bearer $TOKEN" https://fhir.example.org/fhir/R4/metadata | jq .fhirVersion
# không token phải bị từ chối
curl -s -o /dev/null -w '%{http_code}\n' https://fhir.example.org/fhir/R4/Patient    # mong 401
```

Đạt khi: ba `ready` là 200; `issuer` của discovery **đúng từng ký tự** `FHIR_JWT_ISSUER`; có token thì đọc được, không token thì 401.

Kiểm bằng bộ test trước khi release (máy build, không phải máy chủ): `make ci`, `make test-auth-pg`, `make test-e2e` (cần Docker cho phần PG).

## 8. Dữ liệu khởi tạo

Dùng CLI của `auth` (mật khẩu chỉ nhận qua biến môi trường hoặc stdin, không qua argv; secret client chỉ hiện một lần):

```bash
docker compose ... run --rm -e AUTH_SEED_ADMIN_PASSWORD="$ADMIN_PW" auth \
  seed project -name "Bệnh viện A" -admin-email admin@example.org -admin-first An -admin-last Nguyen
# in ra projectId, clientId, clientSecret: lưu clientSecret vào kho bí mật NGAY
docker compose ... run --rm auth client create -project <projectId> -name "Web" -redirect-uri https://app.example.org/cb -grant-type refresh_token
docker compose ... run --rm -e AUTH_NEW_PASSWORD="$NEW" auth user set-password -email admin@example.org
docker compose ... run --rm auth domain add ...        # đăng nhập qua IdP ngoài; URL phải https, không nội bộ
```

`seed project` ghi trong một transaction (lỗi giữa chừng hoàn tác hết). Sau khi có admin, quản trị tiếp có thể qua `/admin/projects/*` nếu bật `AUTH_ADMIN_ENABLED` (hợp đồng ở [api-contract/admin.md](api-contract/admin.md)).

## 9. Nâng cấp (rolling update)

1. Đọc ghi chú phiên bản: migration mới, biến môi trường mới.
2. Build và đẩy image mới; ghi lại tag cũ để quay lại.
3. **Migration trước**: chạy `auth migrate up` và `fhir-server migrate up` bằng role migrate (tác vụ `*-migrate`). Migration phải tương thích ngược một phiên bản (app cũ vẫn chạy trên schema mới trong lúc rollout); nếu không, ghi rõ ở ghi chú phát hành và chấp nhận downtime.
4. Cuốn chiếu từng service, theo thứ tự `auth` → `fhir-server` → `ws-gateway`. Mỗi service có `SHUTDOWN_DRAIN_DELAY` (nên ≥ thời gian load balancer gỡ instance) và `SHUTDOWN_TIMEOUT`: nhận `SIGTERM`, báo `/health/ready` 503, rồi tắt có trật tự. Đặt `stop_grace_period` của compose lớn hơn tổng hai giá trị này.
5. Chạy smoke test (mục 7).
6. **Không** chạy `migrate down` trên production: nó xóa dữ liệu của bảng bị gỡ. Quay lại bằng cách khôi phục từ sao lưu (mục 11).

Instance mới khởi động trên schema chưa migrate sẽ **từ chối chạy** (`schema chưa được migrate`); đó là cơ chế an toàn, không phải lỗi.

## 10. Xoay khóa và bí mật

| Bí mật | Cách xoay không gián đoạn |
|---|---|
| Khóa ký token của `auth` | `auth keys generate` (khóa mới vào cùng thư mục) → triển khai để JWKS có **cả hai** khóa → đợi quá TTL cache của người xác minh (**10 phút**) → đặt `AUTH_ACTIVE_KID` = kid mới, triển khai → khi mọi token cũ hết hạn (`AUTH_ACCESS_TTL` mặc định 15 phút; refresh token 14 ngày vẫn xác minh được bằng khóa cũ nếu còn file) thì xóa file khóa cũ. `auth keys list` cho biết khóa đang ký. `fhir-server` tự nhận khóa mới, không cần khởi động lại |
| `AUTH_MFA_SECRET_KEYS` | thêm khóa mới **lên đầu** danh sách (khóa cũ giữ lại để giải mã); triển khai. Chưa có lệnh mã hóa lại hàng loạt, nên giữ khóa cũ vô thời hạn trừ khi tự mã hóa lại |
| `AUTH_EXTERNAL_STATE_KEY` | đổi đồng loạt mọi instance; hậu quả: các lần đăng nhập ngoài đang dở (≤ 10 phút) phải làm lại |
| `FHIR_WS_SIGNING_KEY` | dùng `FHIR_WS_SIGNING_KEY_PREVIOUS`: (1) ws-gateway đặt khóa mới + `_PREVIOUS` = khóa cũ, rồi `fhir-server` cũng vậy, cuốn chiếu; (2) sau ≥ 5 phút bỏ `_PREVIOUS`. Gateway tách riêng thì đổi gateway **trước** |
| `FHIR_JOB_SIGNING_KEY` | đặt cùng giá trị mới trên mọi replica cùng lúc; job nền đang chờ sẽ lỗi, nên đổi khi không có export/reindex đang chạy |
| Mật khẩu role DB | `ALTER ROLE ... PASSWORD`, cập nhật Secret, khởi động lại dịch vụ tương ứng |
| `clientSecret` | tạo client mới (hoặc đổi `secret` qua `/admin/projects/{id}/client`) rồi chuyển ứng dụng sang client mới và xóa client cũ |

Diễn tập từng dòng trên staging trước khi làm trên production.

## 11. Sao lưu và khôi phục

- Sao lưu: `pg_dump` (hoặc snapshot) của **cả hai schema** và **thư mục khóa của `auth`**. Mất thư mục khóa nghĩa là mọi token đã cấp mất hiệu lực và phải sinh khóa mới; mất `AUTH_MFA_SECRET_KEYS` nghĩa là mọi secret TOTP đã mã hóa không giải mã được (người dùng phải đăng ký lại MFA). Lưu hai thứ này **riêng** khỏi bản sao DB, trong kho bí mật.
- Khôi phục vào database trống: khởi tạo schema/role (mục 4), nạp dump, rồi `auth migrate status` và `fhir-server migrate status` phải báo 0 migration chưa áp dụng với cùng phiên bản app, rồi khởi động.
- Chưa diễn tập khôi phục: làm trên staging trước khi coi là sẵn sàng production.

Chi tiết lệnh: [runbook.md mục 4](runbook.md).

## 12. Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Service từ chối khởi động, in tên biến | ràng buộc staging/production (thiếu `AUTH_SIGNING_KEY_PATH`, `AUTH_REDIS_URL`, `AUTH_MFA_SECRET_KEYS`, `AUTH_EXTERNAL_STATE_KEY`; URL không https; cờ dev đang bật) | đặt đúng biến theo thông báo; không hạ `ENVIRONMENT` để né |
| `fhir-server` trả 401 cho token vừa cấp | `FHIR_JWT_ISSUER`/`AUDIENCE` lệch `AUTH_BASE_URL` dù một ký tự (dấu `/` cuối, http/https); `FHIR_JWKS_URL` không với tới được `auth` | so `iss` trong token (`jwt.io` hoặc `jq`) với biến; `curl` JWKS từ trong container `fhir-server` |
| Token cũ hỏng sau khi `auth` khởi động lại | dùng khóa tạm trong bộ nhớ (thiếu `AUTH_SIGNING_KEY_PATH`) hoặc mất volume khóa | dev: bình thường. Production: không xảy ra vì khóa bắt buộc; kiểm volume |
| `schema chưa được migrate` | chưa chạy `migrate up` | chạy migrate bằng role migrate (mục 6, 9) |
| `permission denied for schema` khi khởi động | app đang `DATABASE_AUTO_MIGRATE=true` nhưng role app không có DDL | đặt `DATABASE_AUTO_MIGRATE=false`, migrate bằng role migrate |
| Mọi người dùng bị `429` cùng lúc | thiếu `*_TRUSTED_PROXIES`, nên mọi yêu cầu thấy cùng IP của proxy | khai dải IP proxy |
| Hạn mức MFA/đăng nhập dường như gấp N lần | hạn mức theo IP đếm trong bộ nhớ từng instance (chỉ hạn mức MFA dùng Redis) | biết trước: chạy N replica thì hạn mức IP nhân N (việc #14, chưa làm) |
| Không nhận được thư đặt lại mật khẩu/mời | `AUTH_MAIL_PROVIDER=none` (mặc định ở production); hoặc SMTP lỗi: xem log `mail: gửi thất bại` (chỉ có host, cổng, mã SMTP; không có địa chỉ hay nội dung) | đặt `smtp` và các biến `AUTH_SMTP_*`; tạm thời dùng `auth user set-password`, `/admin/projects/setpassword`. Lỗi `TLS`/`certificate`: relay dùng CA riêng thì đặt `AUTH_SMTP_CA_FILE`; lỗi `không hỗ trợ TLS`: relay thiếu STARTTLS (đừng bật plaintext ở production) |
| Websocket không nhận sự kiện | `FHIR_WS_SIGNING_KEY` hai bên khác nhau; Redis khác nhau; `FHIR_WS_URL` sai địa chỉ công khai | xem [runbook.md mục 11.4](runbook.md) |
| Đăng nhập ngoài báo `Invalid state` | cookie `auth-external-state` không tới (khác trình duyệt, hết 10 phút, cookie bị chặn), nhiều instance khác `AUTH_EXTERNAL_STATE_KEY` | dùng chung một khóa; kiểm `AUTH_COOKIE_SECURE` và https |

## 13. Giới hạn đã biết (chưa sẵn sàng production đầy đủ)

1. **Thư**: đã có kênh SMTP (`AUTH_MAIL_PROVIDER=smtp`) nhưng chưa kiểm với nhà cung cấp thật (Gmail, SES SMTP, Office 365...) ngoài máy chủ SMTP giả trong test; chưa có SMTP riêng theo project ở `auth` (cần mã hóa `Project.secret`, Q-E7), chưa có route `/email/v1/send`, chưa có hàng đợi/thử lại (gửi lỗi chỉ ghi log và bỏ qua với luồng công khai).
2. Hạn mức theo IP đếm trong bộ nhớ từng instance (việc #14).
3. Chưa có khóa tài khoản theo số lần đăng nhập sai (việc #13).
4. Thu hồi token có độ trễ ở `fhir-server`: nó kiểm chữ ký, không tra Login mỗi yêu cầu, nên token đã cấp còn hiệu lực tới hết `AUTH_ACCESS_TTL` (mặc định 15 phút) dù đã bị thu hồi hay membership đã bị xóa. Hạ `AUTH_ACCESS_TTL` nếu cần cửa sổ ngắn hơn.
5. Chưa có CI, chưa có số liệu kiểm tải, chưa diễn tập sao lưu/khôi phục và xoay khóa (xem [runbook.md mục 10](runbook.md)).
6. `fhir-worker` chưa tách: export lớn chạy chung tiến trình với API cho tới khi có số liệu đo tải (task #12c).
7. `dicomweb` chưa đóng gói.
8. Token Patient/RelatedPerson chưa đọc được dữ liệu lâm sàng (compartment chưa làm, task #16).

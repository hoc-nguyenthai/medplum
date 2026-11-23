# fhir-server: runbook vận hành

Cập nhật 2026-11-23. Dành cho người trực vận hành riêng `fhir-server`; tự chứa đủ để vận hành mà không cần mở runbook khác. Mọi tên biến, lệnh và giá trị lấy từ mã hiện tại (`internal/config`, `cmd/fhir-server`, `pkg/fhirstore`, `pkg/fhirrouter`, `pkg/fhirauth`, `deploy/postgres-init/01-schemas.sh`) và từ các runbook chung của repo mà phần này được tách ra; **chưa mục nào được diễn tập trên PostgreSQL và cụm thật** (xem mục 13). Cấu hình từng biến ở [config.md](config.md); lý do thiết kế ở [architecture.md](architecture.md).

## 1. Kiểm tra sức khỏe

| Lệnh / endpoint | Ý nghĩa |
|---|---|
| `GET /healthz` | Process còn sống (luôn 200 khi server chạy). Dùng cho liveness |
| `GET /readyz` | `200` khi nhận được lưu lượng; `503` khi đang tắt hoặc một phụ thuộc hỏng. Body `{"ready":bool,"checks":{...}}` gồm `accepting_traffic`, `database` và `broker` (chỉ khi bật `FHIR_SUBSCRIPTIONS`). Dùng cho readiness |
| `fhir-server healthcheck` | Gọi `/healthz`; dùng cho `HEALTHCHECK` của Docker (image distroless không có curl) |

`/readyz` kiểm DB trong `READINESS_TIMEOUT` (mặc định 2s). `database: false`: kiểm kết nối, `DATABASE_URL`, số kết nối còn trống (`DATABASE_MAX_OPEN_CONNS`, mặc định 20). `broker: false`: kiểm Redis (`REDIS_URL`).

## 2. Khởi động, tắt, triển khai

- **Tắt êm** (SIGTERM/SIGINT): `/readyz` chuyển 503 ngay, chờ `SHUTDOWN_DRAIN_DELAY` (5s) để load balancer rút instance, rồi dừng nhận yêu cầu mới và chờ yêu cầu đang chạy tối đa `SHUTDOWN_TIMEOUT` (25s). Đặt `terminationGracePeriodSeconds`/`stop_grace_period` lớn hơn tổng hai giá trị.
- Khi tắt: job nền đang chạy được chờ xong; job chưa xong **nhả lease** để instance khác (hoặc lần khởi động sau) chạy lại, không đánh dấu lỗi. Socket websocket nhúng đóng bằng 1001; client tự bind lại bằng token mới.
- **Cấu hình sai** được gom và báo một lượt khi khởi động (ví dụ thiếu `FHIR_JOB_SIGNING_KEY` khi dùng JWKS); sửa theo thông báo, **không hạ `ENVIRONMENT`** để né ([config.md](config.md), mục "Ràng buộc").
- **Rolling update nhiều replica:** đặt `DATABASE_AUTO_MIGRATE=false` và chạy `fhir-server migrate up` ở một bước riêng **trước** khi cuốn chiếu (xem mục 3.4 nếu dùng role migrate riêng). Instance khởi động trên schema chưa migrate **từ chối chạy** (`schema chưa được migrate`): đó là cơ chế an toàn, không phải lỗi.
- **Thứ tự khi nâng cấp cả cụm:** `auth` trước, rồi `fhir-server`, rồi `ws-gateway`.

```bash
fhir-server migrate status   # số migration chưa áp dụng
fhir-server migrate up       # áp dụng
fhir-server migrate down     # lùi MỘT bước: KHÔNG chạy trên production (xóa dữ liệu của bảng bị gỡ)
```

- Phiên bản schema nằm trong `fhirstore_schema_version` (goose); dữ liệu ở `fhir_resource` (bản hiện tại), `fhir_history` (mọi phiên bản) và các bảng chỉ mục `fhir_idx_*`.
- Quay lại phiên bản app cũ: khôi phục từ sao lưu (mục 10), không dùng `migrate down`.
- **Chỉ mục search là dữ liệu dẫn xuất** từ `content`. Sau khi nâng cấp có thêm tham số search (ví dụ composite), resource ghi trước đó chưa có chỉ mục mới: chạy `fhir-cli reindex <Type>` hoặc `POST [base]/<Type>/$reindex` (quyền admin) từng loại, chờ job xong (`GET [base]/job/{id}/status`).

## 3. Cơ sở dữ liệu

### 3.1 Mô hình

`fhir-server` dùng **schema riêng `fhir`** với role `fhir_app`; schema `identity` của `auth` nằm cùng database nhưng role này không có quyền gì trên nó (kể cả đọc), và không ai tạo được bảng ở `public`. Cả hai service dùng cùng tên bảng (`fhir_resource`, `fhir_history`, `fhir_idx_*`, `fhirstore_schema_version`), nên schema riêng là điều bắt buộc, không phải tùy chọn.

| Role | Schema | `DATABASE_URL` (dev) |
|---|---|---|
| `fhir_app` | `fhir` | `postgres://fhir_app:<mk>@postgres:5432/trivita` |
| `fhir_migrate` (chỉ khi tách role migrate, mục 3.4) | `fhir` (sở hữu) | `DATABASE_MIGRATE_URL` |

### 3.2 Khởi tạo

`deploy/postgres-init/01-schemas.sh` (chạy một lần khi volume Postgres khởi tạo lần đầu, qua `docker-entrypoint-initdb.d`) tạo role và schema, thu quyền của `PUBLIC`, đặt `search_path` ở mức **role** (không phụ thuộc DSN, đúng cả sau PgBouncer). Mật khẩu của `fhir-server` lấy từ `FHIR_DB_PASSWORD` (mặc định chỉ để dev: `fhir`; **đổi ở môi trường thật**, mã hóa URL nếu có ký tự đặc biệt).

```bash
# volume đã có sẵn (script chạy lại được, không mất dữ liệu):
docker compose exec postgres sh /docker-entrypoint-initdb.d/01-schemas.sh
# dev muốn bắt đầu lại từ đầu:
docker compose down --volumes && docker compose up --build
```

**Dữ liệu cũ không tự chuyển.** Nếu trước đây `fhir-server` dùng schema `public` hoặc database riêng, dữ liệu đó vẫn ở đó và app mới sẽ thấy rỗng. Chuyển bằng `COPY` từng bảng, kiểm số dòng và tổng kiểm, giữ bản cũ 7 ngày (quy trình ở [microservices-shared-db.md](../../../docs/plans/microservices-shared-db.md) mục 6).

### 3.3 `DATABASE_REQUIRE_SCHEMA`

`DATABASE_REQUIRE_SCHEMA=true` (chỉ PostgreSQL) làm `fhir-server` **từ chối khởi động** và từ chối cả `migrate` nếu `search_path` của kết nối không phải **đúng một** schema riêng đã tồn tại. Mặc định tắt để không phá cách chạy cũ; ở staging/production không bật thì ghi cảnh báo lúc khởi động. Bật với SQLite là lỗi cấu hình.

| Thông báo lúc khởi động | Nguyên nhân | Cách xử lý |
|---|---|---|
| `search_path "\"$user\", public" có nhiều schema` | Role chưa đặt `search_path` hoặc đăng nhập bằng superuser | Dùng role của service, hoặc `ALTER ROLE fhir_app SET search_path = fhir` |
| `search_path "fhir, public" có nhiều schema` | Còn `public` trong đường tìm kiếm: bảng cùng tên ở `public` che bảng của service | Bỏ `public` khỏi `search_path` |
| `search_path "public" không phải schema riêng` | Trỏ thẳng vào `public` | Đặt schema riêng |
| `schema "fhir" ... chưa tồn tại (hoặc role không có quyền dùng nó)` | Chưa chạy init, hoặc role không sở hữu/không có `USAGE` | Chạy `01-schemas.sh`; kiểm quyền |
| `RequireSchema chỉ áp dụng cho PostgreSQL` | Bật cờ với SQLite | Tắt cờ hoặc dùng PostgreSQL |

### 3.4 Role migrate riêng (production)

Mặc định (dev) một role sở hữu schema nên có cả DDL và tự migrate. Ở staging/production nên tách: một lỗ hổng SQL injection ở app không được phép `DROP`/`ALTER` bảng.

| Role | Quyền | Dùng để |
|---|---|---|
| `fhir_migrate` | **Sở hữu** schema và mọi bảng; chạy DDL | Chỉ lệnh `fhir-server migrate` ở bước deploy |
| `fhir_app` | `USAGE` schema; `SELECT/INSERT/UPDATE/DELETE` trên mọi bảng, `USAGE/SELECT` trên sequence, **kể cả bảng do migration tương lai tạo** (`ALTER DEFAULT PRIVILEGES`); không DDL, không `TRUNCATE` | App đang chạy |

**Bật:** chạy `deploy/postgres-init/01-schemas.sh` với `DB_SEPARATE_MIGRATION_ROLES=true` và đủ các mật khẩu (`FHIR_DB_PASSWORD`, `FHIR_MIGRATE_DB_PASSWORD`, cùng hai mật khẩu của role `identity_*` của service kia; chế độ này không có mật khẩu mặc định, thiếu là script dừng). Chạy lại được trên database đã có: chuyển chủ sở hữu bảng sang role migrate bằng `REASSIGN OWNED`, giữ nguyên dữ liệu. **Không** hỗ trợ chiều ngược lại.

**Cấu hình `fhir-server`:** `DATABASE_URL` là role `fhir_app`; `DATABASE_MIGRATE_URL` là role `fhir_migrate` (chỉ lệnh `migrate` dùng); `DATABASE_AUTO_MIGRATE=false` (**bắt buộc**: đặt `DATABASE_MIGRATE_URL` mà để tự migrate là lỗi cấu hình); `DATABASE_REQUIRE_SCHEMA=true`. Mỗi lần deploy chạy `fhir-server migrate up` (bằng role migrate) **trước** khi cuốn chiếu app mới; thử trên máy bằng `docker-compose.db-roles.yml` (tác vụ `fhir-migrate` chạy trước app).

| Triệu chứng | Nguyên nhân | Xử lý |
|---|---|---|
| `schema chưa được migrate` | Chưa chạy `migrate up`, hoặc chạy trước khi init chuyển quyền | Chạy `fhir-server migrate up` (thông báo này là đúng ở chế độ tách) |
| `failed to create version table ... permission denied for schema` khi khởi động | `DATABASE_AUTO_MIGRATE=true` nhưng role app không có DDL | Đặt `DATABASE_AUTO_MIGRATE=false`, migrate bằng role migrate |
| `DATABASE_MIGRATE_URL: đã dùng role migrate riêng thì app không tự migrate` | Đặt `DATABASE_MIGRATE_URL` mà `DATABASE_AUTO_MIGRATE` còn `true` | Đặt `DATABASE_AUTO_MIGRATE=false` |
| Migration mới tạo bảng mà app báo `permission denied for table` | Bảng không do role migrate tạo (ví dụ tạo bằng superuser) nên không có quyền mặc định | Tạo bảng bằng role migrate, hoặc `GRANT SELECT, INSERT, UPDATE, DELETE ON <bảng> TO fhir_app` |

### 3.5 Kiểm tra trạng thái

```bash
# schema fhir phải có đủ bảng và bảng phiên bản riêng; public không có bảng nào của service
psql -d trivita -c "select table_schema, count(*) from information_schema.tables where table_schema in ('identity','fhir','public') group by 1"
# role của service kia không thấy schema fhir (mong đợi: permission denied)
PGPASSWORD=<mk> psql -U identity_app -h <host> -d trivita -c "select 1 from fhir.fhir_resource"
```

### 3.6 Số kết nối

Mỗi replica `fhir-server` mở tối đa `DATABASE_MAX_OPEN_CONNS` (mặc định 20) kết nối tới **cùng một máy chủ** với các service khác: tổng của mọi replica của mọi service phải nhỏ hơn `max_connections` của PostgreSQL (mặc định 100) trừ phần dự phòng. Ví dụ 4 replica `fhir-server` và 2 replica `auth` ở mặc định là 120 kết nối, vượt giới hạn: hạ `DATABASE_MAX_OPEN_CONNS` hoặc đặt PgBouncer. **Chưa kiểm** hành vi của PgBouncer với `search_path`; vì vậy `search_path` được đặt ở mức role chứ không ở DSN.

### 3.7 `statement_timeout`

Script init chỉ đặt khi có `FHIR_STATEMENT_TIMEOUT` (ví dụ `30s`); để trống thì **không** đặt. Cố ý không có mặc định: `$reindex` và bulk export chạy dưới role `fhir_app` và có thể chạy lâu, một timeout chưa đo sẽ làm chúng hỏng. Đo thời gian truy vấn thật rồi mới đặt; role migrate không bao giờ bị đặt timeout.

## 4. Cấu hình production, triển khai lần đầu và smoke test

### 4.1 Cấu hình bắt buộc ở staging/production

Danh sách đầy đủ và điều bị cấm ở [config.md](config.md) (mục "Ràng buộc"). Giá trị cần chuẩn bị:

| Biến | Giá trị |
|---|---|
| `FHIR_BASE_URL` | Địa chỉ công khai `https://fhir.example.org/fhir/R4` |
| `FHIR_JWT_ISSUER` | `= AUTH_BASE_URL` (hoặc `AUTH_ISSUER`) của `auth`, **đúng từng ký tự** |
| `FHIR_JWT_AUDIENCE` | `= AUTH_AUDIENCE` (mặc định chính issuer) |
| `FHIR_JWKS_URL` | URL JWKS của `auth` mà `fhir-server` với tới được (có thể là địa chỉ nội bộ `http://auth:8000/.well-known/jwks.json`, khác địa chỉ công khai; https ở staging/production) |
| `FHIR_JOB_SIGNING_KEY` | >= 32 ký tự, mọi replica dùng chung (`openssl rand -hex 24`) |
| `FHIR_READ_TOKENS`, `FHIR_WRITE_TOKENS`, `FHIR_ADMIN_TOKENS` | Đặt rỗng khi dùng JWT; `FHIR_AUTH_DISABLED=false` |
| `FHIR_JWT_HMAC_SECRET` | **Không dùng** ở production, chỉ `FHIR_JWKS_URL` |
| `DATABASE_URL`, `DATABASE_MIGRATE_URL` | Mục 3.4 |
| `FHIR_ALLOWED_ORIGINS`, `FHIR_TRUSTED_PROXIES` | Origin https được gọi API từ trình duyệt (không dùng `*`); dải IP của reverse proxy |
| `FHIR_SUBSCRIPTIONS=true`, `FHIR_WS_MODE=issuer`, `BROKER_DRIVER=redis`, `REDIS_URL`, `FHIR_WS_SIGNING_KEY`, `FHIR_WS_URL` | Chỉ khi dùng websocket tách riêng. `FHIR_WS_SIGNING_KEY` phải **giống hệt** khóa của `ws-gateway`; `FHIR_WS_URL` là địa chỉ websocket **công khai** của gateway (`wss://ws.example.org/ws/subscriptions-r4`), khác địa chỉ nội bộ |
| `FHIR_WEBHOOK_ALLOW_PRIVATE` | **Phải `false`** (bật thì tắt chống SSRF) |
| `FHIR_BINARY_STORAGE`, `FHIR_STORAGE_SIGNING_KEY_PATH`, `FHIR_STORAGE_BASE_URL` | Chỉ khi dùng Binary thô và `/storage`. Khóa ký: `openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out storage.pem && chmod 600 storage.pem` (tạo trước, mount vào container). **Thư mục kho `file:` là ổ đĩa của một replica**: không chạy nhiều replica với `file:`; nhiều replica dùng `s3:<bucket>[/<prefix>]` kèm `FHIR_S3_*` |

### 4.2 Triển khai lần đầu

Thứ tự: Postgres (và Redis) → migrate → `auth` (cấp token) → `fhir-server`.

```bash
# 1. Postgres (+ Redis) lên và healthy (init schema/role chạy lần đầu, mục 3.2)
# 2. migrate bằng role migrate (tác vụ một lần, chạy trước app)
docker compose ... run --rm fhir-migrate
# 3. app
docker compose ... up -d fhir-server
```

Các file compose trong repo là **mẫu chạy thử** (địa chỉ `localhost`, cổng published). Ở production hãy sao chép rồi sửa: địa chỉ công khai https, tên mạng; đừng dùng nguyên mật khẩu mặc định của dev. `docker-compose.db-roles.yml` không có mật khẩu mặc định (thiếu biến thì compose dừng).

### 4.3 Smoke test sau khi triển khai

```bash
curl -fsS https://fhir.example.org/healthz                                  # 200
curl -fsS https://fhir.example.org/fhir/R4/metadata | jq .fhirVersion       # công khai, 4.0.1
curl -s -o /dev/null -w '%{http_code}\n' https://fhir.example.org/fhir/R4/Patient   # không token: mong 401
TOKEN=$(curl -fsS https://auth.example.org/oauth2/token -d grant_type=client_credentials \
  -d client_id=$CLIENT_ID --data-urlencode client_secret=$CLIENT_SECRET | jq -r .access_token)
curl -fsS -H "Authorization: Bearer $TOKEN" https://fhir.example.org/fhir/R4/Patient?_count=1   # có token: 200
```

Đạt khi: `healthz` 200, có token thì đọc được, không token thì 401. Trước release (trên máy build, không phải máy chủ): `make ci`, `make test-pg`, `make test-e2e`.

## 5. Việc quản trị hằng ngày

| Việc | Lệnh |
|---|---|
| Gán project cho dữ liệu cũ chưa có `meta.project` (sau khi bật JWT trên database đã có dữ liệu) | `fhir-server assign-project -project <id> [-type Patient,Observation] -dry-run`, rồi bỏ `-dry-run`. Chạy khi không có ghi đồng thời; idempotent |
| Quét nội dung Binary mồ côi trong kho (khóa không còn phiên bản Binary nào trong CSDL, quá ân hạn) | `fhir-server sweep-binary [-grace 24h] [-max-delete 1000] -dry-run` (chạy định kỳ bằng cron; cần `FHIR_BINARY_STORAGE`; không dùng được với bộ nhớ) |
| Lập lại chỉ mục search một loại | `fhir-cli reindex <Type>` hoặc `POST [base]/<Type>/$reindex` (quyền admin) |
| Xuất và nhập dữ liệu giữa môi trường (không thay thế sao lưu) | `fhir-cli export -o dir`, `fhir-cli import` vào máy chủ đích có `FHIR_UPDATE_AS_CREATE=true` để giữ id; bản export chỉ chứa phiên bản hiện tại, không có lịch sử, không có `Binary`/`AsyncJob` |

## 6. Xoay khóa và bí mật

| Bí mật | Cách xoay | Ảnh hưởng |
|---|---|---|
| Khóa JWT của `auth` (JWKS) | `auth` phát khóa mới (thêm trước, bỏ khóa cũ sau). `fhir-server` tự nhận: cache JWKS 10 phút, gặp `kid` lạ thì làm mới (tối đa mỗi 30 giây). IdP sập thì dùng khóa đã cache | Không cần khởi động lại. Token ký bằng khóa đã gỡ bị từ chối sau khi cache hết hạn |
| Token tĩnh (`FHIR_*_TOKENS`) | Thêm token mới cạnh token cũ, triển khai, đổi client, gỡ token cũ và triển khai lại | Rolling update, không gián đoạn. Secret tối thiểu 24 ký tự |
| `FHIR_JOB_SIGNING_KEY` (>= 32 ký tự, mọi replica dùng chung) | Đặt **cùng giá trị mới trên mọi replica cùng lúc** | Job nền đang chờ hoặc đang chạy tạo bằng khóa cũ sẽ **lỗi** ("sai chữ ký quyền") và phải gọi lại; đổi khi không có export/reindex đang chạy |
| `FHIR_WS_SIGNING_KEY` (>= 32 ký tự) | Dùng `FHIR_WS_SIGNING_KEY_PREVIOUS`: (1) đặt khóa mới ở `ws-gateway` (kèm `_PREVIOUS` = khóa cũ) **trước**; (2) rồi `fhir-server` đổi sang khóa mới, cuốn chiếu; (3) sau >= 5 phút (hạn token) bỏ `_PREVIOUS` | Không gián đoạn |
| Khóa ký URL `/storage` (`FHIR_STORAGE_SIGNING_KEY_PATH`) | Sinh PEM mới, đặt khóa cũ (chỉ phần CÔNG KHAI) vào `FHIR_STORAGE_SIGNING_PREVIOUS_KEYS` (tối đa 3), triển khai; bỏ khóa cũ khi URL cũ đã hết hạn (`FHIR_STORAGE_URL_TTL`, tối đa 24h) | URL ký cũ vẫn hợp lệ trong lúc đó |
| Secret của Subscription rest-hook (extension `subscription-secret`) | Sửa Subscription (PUT) với secret mới, báo bên nhận | Có hiệu lực ngay trên instance sửa, các instance khác sau tối đa 30 giây (cache Subscription) |
| Mật khẩu role DB, khóa truy cập S3 | `ALTER ROLE ... PASSWORD` hoặc đổi khóa ở S3, cập nhật Secret, khởi động lại | |

Diễn tập từng dòng trên staging trước khi làm trên production.

## 7. Job nền (reindex, bulk export, batch async) bị kẹt hoặc lỗi

Trạng thái ở resource `AsyncJob` (`accepted`, `completed`, `error`, `cancelled`). Với JWT, mỗi job mang chữ ký quyền; không có chữ ký hợp lệ job báo `error`, không chạy.

- **Xem job đang chờ/chạy:** `GET [base]/AsyncJob?status=accepted` (token admin). Job đang chạy có lease ở `transactionTime` (RFC 3339), gia hạn định kỳ (lease `FHIR_JOB_LEASE`, mặc định 5 phút).
- **Process chết giữa chừng:** không cần can thiệp. Vòng bảo trì (`FHIR_JOB_MAINTENANCE_INTERVAL`, mặc định 30s; `0` là tắt) đưa lại hàng đợi mọi job `accepted` đã hết lease và đủ cũ; nhiều replica cùng nhận thì chỉ một chạy (nhận bằng If-Match). Job chạy lại từ đầu, trừ batch async chạy tiếp từ checkpoint lưu trên `AsyncJob` (extension `https://trivita.ai/fhir/StructureDefinition/job-state`).
- **Job `accepted` mãi không chạy:** kiểm (a) `FHIR_JOB_MAINTENANCE_INTERVAL` có bằng 0 không; (b) job tạo trước khi bật JWT không có chữ ký quyền nên sẽ lỗi khi chạy lại, hãy hủy và tạo lại; (c) `FHIR_JOB_CONCURRENCY` (mặc định 2) có bị job dài chiếm hết không.
- **Job `error`:** `GET [base]/job/{id}/status` trả OperationOutcome nêu nguyên nhân (cũng có trong log ERROR `job lỗi`). Sửa nguyên nhân rồi gọi lại; export lỗi tự dọn tệp đã ghi dở.
- **Hủy hoặc xóa:** `DELETE [base]/job/{id}/status` (batch async; cần quyền ghi) hoặc `DELETE [base]/bulkdata/{id}/status` (export): hủy job đang chạy (dừng ở entry kế tiếp, dọn Binary tạm) hoặc xóa job đã xong cùng Binary kết quả. Hết `FHIR_EXPORT_RETENTION` (mặc định 24h, âm là không bao giờ xóa) thì vòng bảo trì tự xóa.
- **Export tốn dung lượng:** tệp export là `Binary` trong DB (mỗi tệp khoảng 8 MiB). Giữ `FHIR_EXPORT_RETENTION` ngắn và đừng dùng `fhir-cli export -keep` thường xuyên.

## 8. Subscription (rest-hook và websocket)

- Bật bằng `FHIR_SUBSCRIPTIONS=true`; nhiều replica cần `BROKER_DRIVER=redis` và `REDIS_URL`. Sự kiện đi qua Redis Streams: giao rest-hook là at-least-once theo nhóm; kênh websocket đọc stream trực tiếp (không bền, không replay).
- **Webhook không tới:** log WARN `fhirsub: giao thất bại` (có `subscription`, `resource`, `attempts`, `status`, `error`). Lỗi "địa chỉ đích bị chặn" là chống SSRF (đích nội bộ); chỉ dev mới đặt `FHIR_WEBHOOK_ALLOW_PRIVATE=true`. Thử lại tối đa `FHIR_WEBHOOK_MAX_ATTEMPTS` (5) với backoff gấp đôi; hàng đợi giao nằm ở bộ nhớ nên **mất khi restart** (sự kiện chưa xử lý vẫn còn trên Redis, nhưng lần giao đang thử lại thì không).
- **Subscription tự chuyển `off`:** khi đặt `FHIR_SUBSCRIPTION_AUTO_DISABLE`, log WARN `fhirsub: tự tắt Subscription vì giao thất bại lặp lại` (kèm `subscription`, `project`, `failureCount`, `timeWindowSeconds`) và `Subscription.error` ghi lý do. Sửa bên nhận rồi đặt lại `status=active` (PUT/PATCH); bộ đếm đã xóa lúc tắt.
- **Hàng đợi đầy:** log ERROR `hàng đợi giao đầy, bỏ thông báo`; tăng `FHIR_WEBHOOK_WORKERS` hoặc xử lý bên nhận chậm.
- **Websocket tách riêng (`FHIR_WS_MODE=issuer`):** kết nối do `ws-gateway` giữ; deploy hoặc khởi động lại `fhir-server` KHÔNG ngắt socket (đã kiểm bằng container thật), chỉ ngắt khi gateway khởi động lại (client nhận 1001 và bind lại). Giới hạn kết nối, hạn mức bind và Origin đặt ở gateway, không ở `fhir-server`. Redis là điểm phụ thuộc chung.
- **Websocket nhúng:** số kết nối tối đa mỗi instance `FHIR_WS_MAX_CONNECTIONS` (1000; vượt thì 503); hạn mức bind theo người `FHIR_WS_MAX_BINDINGS_PER_USER` (20; vượt thì đóng 1008, đếm theo instance nên cả cụm cho tối đa N x số replica); client chậm bị ngắt (1013). Cần `Upgrade` đi xuyên qua reverse proxy và timeout đọc lớn hơn `FHIR_WS_HEARTBEAT` (30s). Trình duyệt chỉ được từ cùng host trừ khi đặt `FHIR_WS_ALLOWED_ORIGINS`.

## 9. Nhiều project (tenant), super-admin, dữ liệu cũ

- Mỗi yêu cầu JWT bị ràng buộc vào project trong claim `fhir_project` (đổi tên bằng `FHIR_JWT_PROJECT_CLAIM`). Dữ liệu không có `meta.project` không ai thấy: xem `assign-project` ở mục 5.
- **Super-admin** chỉ có tác dụng khi `FHIR_JWT_SUPER_ADMIN=true`; token có scope `fhir.super` chọn project bằng header `X-Fhir-Project`. Mọi yêu cầu của super-admin (kể cả bị từ chối) ghi một dòng log WARN `super-admin truy cập project` với `audit=true` (subject, home_project, target_project, method, path, status; không có nội dung dữ liệu hay token). **Thu các dòng này vào kho log không sửa được và đặt cảnh báo** khi có hoạt động bất thường.
- Điều tra rò rỉ chéo project: mọi truy cập đi qua `meta.project`; so sánh `project` của token với `meta.project` của resource và kiểm các dòng `audit=true`.

## 10. Sao lưu và khôi phục

PostgreSQL là nguồn sự thật duy nhất; server không giữ trạng thái nào khác cần sao lưu (hàng đợi giao webhook ở bộ nhớ, sự kiện websocket không bền). Redis chỉ là broker sự kiện: mất Redis không mất dữ liệu FHIR, chỉ mất sự kiện chưa giao.

- **Sao lưu:** `pg_dump` (hoặc snapshot/PITR) của schema `fhir` (các bảng `fhir_*` và `fhirstore_schema_version`). Cần nhất quán giữa `fhir_resource`, `fhir_history` và `fhir_idx_*`: dump trong **một** transaction (mặc định của `pg_dump`). Kèm khóa ký `/storage` và nội dung kho Binary (`file:`/`s3:`) nếu bật.
- **Khôi phục:** vào database trống, khởi tạo schema và role (mục 3.2), nạp dump, chạy `fhir-server migrate status` (phải 0 chưa áp dụng với cùng phiên bản app), khởi động. Nghi chỉ mục lệch thì `$reindex` từng loại (mục 2).
- **Diễn tập** khôi phục định kỳ trên staging; chưa có kết quả diễn tập nào được ghi lại.

## 11. Sự cố thường gặp

| Triệu chứng | Nguyên nhân thường gặp | Xử lý |
|---|---|---|
| Token Patient nhận `403` "nhiều hơn một rule", hoặc `400` với `_compartment` | `fhir-server` bản cũ chưa hiểu compartment | Nâng cấp mọi instance trước khi bật `AUTH_PATIENT_COMPARTMENT_ENABLED` ([auth runbook §12](../../auth/docs/runbook.md#12-bật-compartment-patient)) |
| Từ chối khởi động, in tên biến | Ràng buộc staging/production (thiếu `FHIR_JOB_SIGNING_KEY` khi dùng JWKS, `FHIR_JWKS_URL` không https, `FHIR_AUTH_DISABLED`...) | Đặt đúng biến theo thông báo |
| `schema chưa được migrate` | `DATABASE_AUTO_MIGRATE=false` và chưa chạy migrate | `fhir-server migrate up` |
| `401 invalid_token` mọi yêu cầu | `FHIR_JWT_ISSUER`/`FHIR_JWT_AUDIENCE` không khớp claim `iss`/`aud` (dù một ký tự: dấu `/` cuối, http/https); `FHIR_JWKS_URL` không với tới được `auth`; `auth` chưa phát `kid` mới | So khớp claim, kiểm `FHIR_JWKS_URL` (`curl` từ trong container); xem log WARN `token bị từ chối` (có lý do) |
| `403` với token hợp lệ | Thiếu claim project, thiếu scope (`fhir.read`, `fhir.write`, `fhir.admin` hoặc SMART `system/<Type>.read`), hoặc AccessPolicy chặn loại/tiêu chí | Log WARN `token không dùng được cho FHIR`; kiểm scope và claim `fhir_access_policy` |
| `404` với resource vừa ghi | Đọc bằng token project khác | Đúng thiết kế (tách project) |
| `410` khi đọc id của project khác đã bị xóa | Giới hạn đã biết của tách project | |
| Search composite hoặc tham số mới không ra kết quả với dữ liệu cũ | Chưa lập chỉ mục sau nâng cấp | `$reindex` từng loại (mục 2) |
| Search chậm | Thiếu chỉ mục cho kiểu truy vấn lạ, `_filter` nhiều nhánh OR, chained/`_has` khớp quá nhiều (giới hạn 5000, lỗi 400) | Thu hẹp tiêu chí; xem `EXPLAIN` trên `fhir_idx_*`; chưa có bài kiểm tải |
| Cuộn trang thấy lặp hoặc sót | Phân trang bằng offset khi dữ liệu đổi giữa các trang | Giới hạn đã biết; dùng `_since`/`_lastUpdated` cho đồng bộ tăng dần |
| `$export` lỗi 403 với token giới hạn | Loại bị scope/AccessPolicy chặn (khi chỉ định `_type`) | Bỏ `_type` để tự bỏ qua loại không đọc được, hoặc cấp quyền đọc `Binary` để tải tệp |
| Mọi người gọi `429` cùng lúc | Thiếu `FHIR_TRUSTED_PROXIES`: mọi yêu cầu thấy cùng IP của proxy | Khai dải IP proxy |
| Websocket không nhận sự kiện | `FHIR_WS_SIGNING_KEY` hai bên khác nhau; Redis khác nhau; `FHIR_WS_URL` sai địa chỉ công khai | Mục 8 |
| `permission denied` về schema hoặc bảng | Role/migration | Mục 3.3 và 3.4 |

## 12. Cần theo dõi

Quan sát qua `pkg/observability` (`OTEL_*`, `LOG_LEVEL`, `LOG_JSON`), tên telemetry `trivita-fhir-server`. Chưa có tài liệu metric riêng; log là nguồn chính khi điều tra. Cần cảnh báo ít nhất: dòng `audit=true` của super-admin, `fhirsub: tự tắt Subscription`, `hàng đợi giao đầy`, `job lỗi`, và `/readyz` 503 kéo dài.

## 13. Giới hạn đã biết

1. Chưa diễn tập sao lưu và khôi phục, xoay khóa, hay các lệnh ở mục 2, 3, 5, 6 trên PostgreSQL và cụm thật.
2. Chưa có kiểm tải (search, bulk export, nhiều replica tranh job, websocket nhiều kết nối) và chưa có định cỡ. Không có manifest K8s: dự án chỉ triển khai bằng Docker và Docker Compose (quyết định của chủ dự án).
3. `fhir-worker` chưa tách: export lớn chạy chung tiến trình với API cho tới khi có số liệu đo tải (task #12c).
4. Kiểm toán bền: hiện chỉ có log `audit=true`, chưa ghi `AuditEvent` vào kho.
5. Hàng đợi giao rest-hook ở bộ nhớ, mất khi restart; sự kiện websocket không bền (at-most-once).
6. Token Patient/RelatedPerson chưa đọc được dữ liệu lâm sàng (compartment chưa làm, task #16).
7. Chưa có CI trong repo; chạy `make ci` bằng tay.
